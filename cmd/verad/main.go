// verad — Vera Core, built from the loop outward. `vera` is the front
// door that keeps this running and talks to it.
//
// This began as a second attempt (an earlier prototype proved Vera
// deserves to exist, then accumulated a great deal of machinery for
// deciding what work is and which model should do it — the kind of
// scaffolding a better model makes worthless). So this one starts from
// the smallest thing that is unambiguously not scaffolding: a person
// talks to their phone, a machine answers, and you can tell whether that
// felt good. It has since grown senses — attention, a terminal eye, and
// a capability surface the phone drives — but the loop is still the core.
//
// The default bind is the LAN: this exists to be reached by a phone, and
// it has no keyless mode to fall back to — the secret is minted before
// the listener opens, and every exchange presents it.
package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/incantery/vera/fleet"
	"github.com/incantery/vera/home"
	"github.com/incantery/vera/journal"
	"github.com/incantery/vera/mux"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/grafana/agento11y/go/agento11y"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// version rides along on every span, so a change in behaviour can be
// told apart from a change in the world.
const version = "0.1.0"

func main() {
	// Telemetry and the like are configured by files, not by whichever
	// shell happened to start this: ~/.config/vera/*.env, KEY=VALUE,
	// never overriding what the environment already says.
	loadEnvFiles()
	addr := flag.String("addr", ":4780", "listen address")
	noPeer := flag.Bool("no-peer", false, "do not advertise over peer-to-peer")
	state := flag.String("state", "", "identity file (default ~/.local/state/vera/identity.json)")
	model := flag.String("model", "gpt-5.6-luna", "model (any OpenAI-compatible server's name for it)")
	apiBase := flag.String("api-base", "", "API base URL (default OpenAI; ollama etc. work)")
	keyFile := flag.String("key-file", "", "API key file (default $OPENAI_API_KEY, then ~/.config/vera/openai_key)")
	echoOnly := flag.Bool("echo", false, "answer by repeating, without a model")
	checkOnly := flag.Bool("check-telemetry", false, "send one exchange's worth of telemetry, report whether it landed, and exit")
	evalSuite := flag.String("eval", "", "run an eval suite against the real handler and exit (e.g. cmd/vera/evals/smoke.yaml)")
	evalPublish := flag.Bool("eval-publish", true, "publish the eval run to agent observability (needs AGENTO11Y_*)")
	prefaceFile := flag.String("preface-file", "", "replace the system prompt with the contents of a file")
	homeDir := flag.String("home", "", "Vera's home, where memory lives (default $VERA_HOME, then ~/vera)")
	noMemory := flag.Bool("no-memory", false, "answer without long-term memory, and learn nothing")
	workspace := flag.String("workspace", "", "where delegated work runs (default ~/.local/state/vera/workspace)")
	permission := flag.String("permission-mode", "acceptEdits", "how much the delegate may do without asking")
	noTools := flag.Bool("no-tools", false, "answer without delegating anything")
	noDelegateTelemetry := flag.Bool("no-delegate-telemetry", false, "do not have Claude Code report its own usage")
	usageEvery := flag.Duration("usage-interval", 15*time.Minute, "how often to report Claude Code subscription usage (0 to stop)")
	showUsage := flag.Bool("usage", false, "print what is left of the Claude Code subscription, and exit")
	toolTimeout := flag.Duration("tool-timeout", 10*time.Minute, "how long a delegated task may run")
	fleetModel := flag.String("fleet-model", "", "the model every fleet task runs on (default: opus for ship, sonnet for scout)")
	muxKind := flag.String("mux", "auto", "the multiplexer: rook (its socket), tmux (rook's -L socket), auto, or off")
	showMemory := flag.Bool("memories", false, "print what Vera remembers, and exit")
	forget := flag.String("forget", "", "forget fact ids (\"3,7\"), or \"all\", and exit")
	flag.Parse()

	// Structured from the start. It goes to a terminal today and to
	// Grafana shortly; the difference should be a handler, not a
	// rewrite of every call site.
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))

	id, err := loadOrCreateIdentity(identityPath(*state))
	if err != nil {
		fmt.Fprintln(os.Stderr, "vera: cannot establish an identity ("+err.Error()+")")
		os.Exit(1)
	}

	// Two transports now, which is what the interface was for. LAN is
	// the one that works at home and can be watched with curl; peer is
	// the one that survives a network which refuses to route between
	// its own clients.
	lan := newLAN(*addr, id)
	transports := []Transport{lan}
	var radio *peerTransport
	peering := "off (--no-peer)"
	if !*noPeer {
		sidecar, err := sidecarBinary()
		if err != nil {
			// Not fatal. A Mac without swiftc still serves the LAN,
			// and saying so is better than refusing to start.
			peering = "unavailable — " + err.Error()
		} else {
			radio = newPeer(id, filepath.Join(stateDir(), "vera", "peer.sock"), sidecar)
			transports = append(transports, radio)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	defer stop()

	// Telemetry before the mind, because the mind builds its
	// instruments from the global meter provider and one built against
	// the no-op provider stays no-op for the life of the process.
	telemetry := "off — set OTEL_EXPORTER_OTLP_ENDPOINT to send it somewhere"
	var otelp *providers
	if telemetryConfigured() {
		p, err := startTelemetry(ctx, version)
		if err != nil {
			fmt.Fprintln(os.Stderr, "vera: telemetry: "+err.Error())
			os.Exit(1)
		}
		otelp = p
		defer func() {
			// Its own context: ctx is already cancelled by the time
			// this runs, and a cancelled flush flushes nothing.
			flush, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			p.shutdown(flush)
		}()
		telemetry = os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
		if telemetry == "" {
			telemetry = "on"
		}
	}

	// Generation export is a second set of credentials with its own
	// scope, so having the OTLP ones says nothing about having these.
	var generations *agento11y.Client
	conversations := "off — set AGENTO11Y_ENDPOINT to feed the Conversations view"
	if generationExportConfigured() {
		c, err := newGenerationExport()
		if err != nil {
			fmt.Fprintln(os.Stderr, "vera: generation export: "+err.Error())
			os.Exit(1)
		}
		generations = c
		defer shutdownGenerations(c)
		conversations = os.Getenv("AGENTO11Y_ENDPOINT")
	}

	if *checkOnly {
		checkTelemetry(ctx, otelp, generations)
		return
	}

	// Varying the prompt is the whole point of having evals: change it,
	// run the suite, see what moved.
	var preface string
	if *prefaceFile != "" {
		b, err := os.ReadFile(*prefaceFile)
		if err != nil {
			fmt.Fprintln(os.Stderr, "vera: "+err.Error())
			os.Exit(1)
		}
		preface = strings.TrimSpace(string(b))
	}

	// Memory you cannot read is memory you cannot trust, and one wrong
	// fact silently colours every answer afterwards — so listing and
	// forgetting are first-class, not a debug hatch.
	// An eval run gets its own memory unless told otherwise. Sharing
	// the real one would let cases teach each other, make results
	// depend on run order, and quietly write test fixtures into what
	// Vera believes about you.
	if *evalSuite != "" && *homeDir == "" {
		scratch, err := os.MkdirTemp("", "vera-eval-home")
		if err != nil {
			fmt.Fprintln(os.Stderr, "vera: "+err.Error())
			os.Exit(1)
		}
		defer os.RemoveAll(scratch)
		*homeDir = scratch
	}

	// Home before anything that writes to it. A verad that cannot make
	// its own home is a verad with nowhere to put what it learns, and
	// that is worth saying out loud rather than discovering in a month
	// of empty memory.
	place, err := home.Open(home.Path(*homeDir))
	if err != nil {
		fmt.Fprintln(os.Stderr, "vera: cannot open "+home.Path(*homeDir)+" ("+err.Error()+")")
		os.Exit(1)
	}
	if n, err := place.Migrate(legacyMemoryPath()); err != nil {
		slog.Warn("could not migrate the old memory.json", "error", err.Error())
	} else if n > 0 {
		slog.Info("migrated memory.json into home", "facts", n, "home", place.Root)
	}
	slog.Info("home", "path", place.Root)
	memory := place.Memory()
	if *showUsage {
		u, err := scrapeUsage(ctx)
		if err != nil {
			fmt.Fprintln(os.Stderr, "vera: "+err.Error())
			os.Exit(1)
		}
		fmt.Println(u)
		return
	}
	if *showMemory {
		printMemories(place, memory)
		return
	}
	if *forget != "" {
		forgetFacts(memory, *forget)
		return
	}
	if *noMemory {
		memory = nil
	}

	// The delegate has a shell, so this is a real grant of capability
	// on this machine — it is a flag you can turn off, and it says so
	// at startup rather than being invisible.
	var hands *Delegate
	if !*noTools {
		hands = &Delegate{
			Workspace:  workspacePath(*workspace),
			Permission: *permission,
			Timeout:    *toolTimeout,
			// The delegate reports to the same place Vera does, under
			// its own service name, whenever Vera is reporting at all.
			Telemetry: telemetryConfigured() && !*noDelegateTelemetry,
		}
	}

	answer, mind, how := chooseMind(*echoOnly, *model, *apiBase, *keyFile, generations, preface, memory, hands)
	if mind != nil {
		// Every exchange, on disk, whatever else is watching: what
		// `vera dump` hands to whoever is asked why Vera did that.
		mind.Journal = &journal.Writer{Dir: filepath.Join(stateDir(), "vera", "conversations")}
	}
	lan.how = how
	// Speech to text lives on this machine; the phone sends audio and
	// this turns it into words.
	lan.stt = newParakeet()
	if mind != nil {
		// The Mac app reports where attention is over the LAN transport
		// and the mind reads it from there; the two meet here and
		// nowhere else.
		mind.Attention = lan.attention
		lan.cleaner = mind.clean
	}

	if *evalSuite != "" {
		// The scorers grade the model, so the run should say which one
		// it graded.
		os.Setenv("VERA2_EVAL_MODEL", *model)
		publish := *evalPublish && generationExportConfigured()
		err := runEvals(ctx, *evalSuite, answer, mind.Settle, publish)
		// Extraction runs behind the reply, so a run that exits the
		// moment the last case finishes would abandon it mid-thought.
		mind.Settle()
		if err != nil {
			fmt.Fprintln(os.Stderr, "vera: "+err.Error())
			os.Exit(1)
		}
		return
	}
	// Subscription usage is not telemetry the process emits — it is
	// account state, and the only way to it is to ask.
	if *usageEvery > 0 && telemetryConfigured() && !*noTools {
		go watchUsage(ctx, *usageEvery)
	}

	// The terminal, as an observer: which pane is in front of the
	// person. This machine is the device; the Mac app reports under the
	// same name. tmux on rook's socket is the backend today; the Mux
	// interface is where rook's own socket goes when it answers.
	var term mux.Mux
	switch *muxKind {
	case "auto":
		if _, err := os.Stat(mux.RookSock()); err == nil {
			term = mux.NewRook("")
		} else {
			term = mux.NewTmux("rook", "http://127.0.0.1"+portOf(*addr)+"/poke/rook")
		}
	case "rook":
		term = mux.NewRook("")
	case "tmux":
		term = mux.NewTmux("rook", "http://127.0.0.1"+portOf(*addr)+"/poke/rook")
	case "off":
	default:
		fmt.Fprintln(os.Stderr, "vera: --mux must be auto, rook, tmux or off")
		os.Exit(2)
	}
	if term != nil {
		t := newTerminal(term, id.Name)
		lan.onPoke("rook", t.Poke)
		lan.typer = t.Type
		lan.goer = t.GoTo
		lan.screener = t.Capture
		lan.narrower = t.Mobile
		lan.agenter = t.Agent
		go t.run(ctx, lan.attention.Observe)
	}

	// The fleet: rooms Vera opens for coding agents, and the watch over
	// them. It needs a mux to put a pane in; without one it is off.
	if term != nil && !*noTools {
		f := fleet.New(term, fleet.NewStore(filepath.Join(stateDir(), "vera", "fleet")))
		f.Projects = &fleet.Projects{Mux: term, File: filepath.Join(stateDir(), "vera", "projects.json")}
		f.HookURL = func(task, incarnation string) string {
			return "http://127.0.0.1" + portOf(*addr) + "/fleet/" + task + "/hook?incarnation=" + incarnation
		}
		if *fleetModel != "" {
			f.Model = func(*fleet.Task) string { return *fleetModel }
		}
		f.StatusURL = func(task string) string {
			return "http://127.0.0.1" + portOf(*addr) + "/fleet/" + task + "/status"
		}
		f.Env = fleetEnv(telemetryConfigured() && !*noDelegateTelemetry)
		// What the fleet learns about a repository goes in her home,
		// beside what she knows about the person.
		f.Notes = place
		f.Observe = func(ev fleet.Event) { lan.attention.Observe(fleetObservation(id.Name, ev)) }
		lan.fleet = f
		if mind != nil {
			mind.Fleet = f
			mind.Projects = f.Projects
		}
		go func() { _ = f.Supervise(ctx) }()
		// The side rail, when the mux has one: the fleet as rows.
		if side, ok := term.(mux.Sider); ok {
			focus := func() string {
				fctx, cancel := context.WithTimeout(ctx, time.Second)
				defer cancel()
				p, err := term.Focus(fctx)
				if err != nil || p.Path == "" {
					return ""
				}
				if r, err := fleet.FindRepo(p.Path); err == nil {
					return r.Root
				}
				return ""
			}
			rl := newRail(side, f, f.Projects, focus)
			prev := f.Observe
			f.Observe = func(ev fleet.Event) {
				if prev != nil {
					prev(ev)
				}
				rl.Poke()
			}
			go rl.run(ctx)
		}
	}

	go func() {
		// Asked rather than assumed: the sidecar takes a second to get
		// the radio up, and it can fail.
		if radio != nil {
			peering = radio.Ready(10 * time.Second)
		}
		announce(transports[0], id, *addr, how, telemetry, conversations, place, memory, hands, peering)
	}()

	// Every transport carries the same handler. A failure in the radio
	// is reported and does not take the LAN down — losing the radio
	// should not cost you the wifi. The LAN is different: it is the
	// front door, and a verad that cannot open it (the port is taken,
	// usually by an older verad) is not serving and must not pretend
	// to. It exits, loudly, and `vera` reports it.
	var wg sync.WaitGroup
	failed := make(chan error, 1)
	for _, t := range transports {
		wg.Add(1)
		go func(t Transport) {
			defer wg.Done()
			if err := t.Serve(ctx, answer); err != nil {
				slog.Error("transport stopped", "transport", t.Name(), "error", err.Error())
				if t.Name() == "lan" {
					select {
					case failed <- err:
					default:
					}
					stop()
				}
			}
		}(t)
	}
	// Leave a note of where this process is, for `vera` to find it —
	// only once the LAN is actually bound. Written any earlier, a verad
	// that then fails to bind (the port is an older verad's) would have
	// written over the live daemon's note; and the exit-early modes
	// above never reach here at all.
	if lan.bound(ctx, 5*time.Second) {
		if err := writeRunfile(*addr); err != nil {
			slog.Warn("verad: cannot record runfile", "error", err.Error())
		}
		defer removeRunfile()
	}
	wg.Wait()
	select {
	case err := <-failed:
		fmt.Fprintln(os.Stderr, "verad: "+err.Error())
		os.Exit(1)
	default:
	}
}

// echo is the stand-in for a mind.
//
// It streams rather than answering in one piece, and it streams at
// roughly the pace of a model rather than instantly, because the thing
// being evaluated today is what a reply FEELS like arriving — and an
// answer that appears all at once in zero milliseconds would tell us
// nothing about that. It is a deliberate lie about latency, and it is
// the only lie in here.
func echo(ctx context.Context, msg Message, reply func(Frame) error) error {
	text := strings.TrimSpace(msg.Text)
	if text == "" {
		return reply(Frame{Done: true})
	}

	words := strings.Fields("You said: " + text)
	for i, word := range words {
		select {
		case <-ctx.Done():
			// The phone hung up mid-sentence. Nothing to report.
			return nil
		case <-time.After(40 * time.Millisecond):
		}
		if i > 0 {
			word = " " + word
		}
		if err := reply(Frame{Delta: word}); err != nil {
			return nil // peer went away; not our error to raise
		}
	}
	return reply(Frame{Done: true})
}

// chooseMind: a model if there is a key for one, and otherwise the
// echo — said out loud rather than discovered. A binary that silently
// degrades to repeating your words is a binary you will spend an hour
// debugging the model behind.
func chooseMind(echoOnly bool, model, apiBase, keyFile string, generations *agento11y.Client, preface string, memory *home.Memory, hands *Delegate) (Handler, *Mind, string) {
	if echoOnly {
		return echo, nil, "echoing, no model (--echo)"
	}
	key := findKey(keyFile)
	if key == "" && apiBase == "" {
		return echo, nil, "echoing — no API key found, so there is no model to ask"
	}
	mind := &Mind{
		// No timeout on the client: the context carries the deadline,
		// and a streamed answer that is still arriving is not late.
		Client:      &http.Client{},
		Base:        apiBase,
		Key:         key,
		Model:       model,
		History:     newHistory(),
		Gen:         generations,
		Preface:     preface,
		Memory:      memory,
		Delegate:    hands,
		instruments: newInstruments(),
	}
	return mind.think, mind, model
}

// announce prints where to pair, once the listener has a real port.
func announce(t Transport, id Identity, addr, how, telemetry, conversations string, place *home.Home, memory *home.Memory, hands *Delegate, peering string) {
	time.Sleep(150 * time.Millisecond)
	fmt.Printf("vera — %s\n", id.Name)
	fmt.Printf("  answering with  %s\n", how)
	fmt.Printf("  telemetry  %s\n", telemetry)
	fmt.Printf("  generations  %s\n", conversations)
	fmt.Printf("  home  %s\n", place.Root)
	if memory == nil {
		fmt.Println("  memory  off (--no-memory)")
	} else {
		fmt.Printf("  memory  %s remembered, in %s/\n", quantity(memory.Count(), "thing", "things"), home.MemoryDir)
	}
	if hands == nil {
		fmt.Println("  delegate  off (--no-tools)")
	} else {
		fmt.Printf("  delegate  claude code in %s (%s)\n", hands.Workspace, hands.Permission)
		if hands.Telemetry {
			fmt.Printf("    reporting as service.name=%s, joined to Vera's traces\n", delegateService)
		}
	}
	fmt.Printf("  peer-to-peer  %s\n", peering)
	fmt.Printf("  pair at  http://localhost%s/\n", portOf(addr))
	for _, hint := range t.Hints() {
		fmt.Printf("  reachable at  %s\n", hint)
	}
	if len(t.Hints()) == 0 {
		fmt.Println("  no LAN address — a phone cannot reach this machine right now")
	}
}

func portOf(addr string) string {
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		return addr[i:]
	}
	return ":" + addr
}

func identityPath(override string) string {
	if override != "" {
		return override
	}
	return filepath.Join(stateDir(), "vera", "identity.json")
}

// checkTelemetry sends exactly what a real exchange sends, flushes it,
// and says whether the far end took it. Setting this up means pasting
// four values from a portal, and the failure mode of getting one wrong
// is silence — so the check has to be a thing you can run.
func checkTelemetry(ctx context.Context, p *providers, generations *agento11y.Client) {
	if p == nil && generations == nil {
		fmt.Fprintln(os.Stderr, "vera: nothing is configured — set OTEL_EXPORTER_OTLP_ENDPOINT, or AGENTO11Y_ENDPOINT, or both")
		os.Exit(1)
	}

	// Generation export first: it is the path that feeds the
	// Conversations view, and Flush reports delivery synchronously —
	// so a wrong scope is an exit code here rather than an empty
	// screen an hour from now.
	if generations != nil {
		_, rec := generations.StartGeneration(ctx, agento11y.GenerationStart{
			ConversationID: "vera-check",
			AgentName:      serviceName,
			AgentVersion:   version,
			Model:          agento11y.ModelRef{Provider: "openai", Name: "check"},
		})
		rec.SetResult(agento11y.Generation{
			Input:  []agento11y.Message{agento11y.UserTextMessage("is this reaching Agent Observability?")},
			Output: []agento11y.Message{agento11y.AssistantTextMessage("if you are reading this in Conversations, yes.")},
			Usage:  agento11y.TokenUsage{InputTokens: 7, OutputTokens: 11},
		}, nil)
		rec.End()

		flush, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		if err := generations.Flush(flush); err != nil {
			fmt.Fprintln(os.Stderr, "vera: generations did not reach "+os.Getenv("AGENTO11Y_ENDPOINT")+": "+err.Error())
			os.Exit(1)
		}
		fmt.Println("generations reached " + os.Getenv("AGENTO11Y_ENDPOINT"))
	}

	if p == nil {
		return
	}

	mind := &Mind{History: newHistory(), Model: "check", instruments: newInstruments()}
	_, span := mind.tracer.Start(ctx, "chat check", trace.WithSpanKind(trace.SpanKindClient))
	span.SetAttributes(
		attribute.String("gen_ai.operation.name", "chat"),
		attribute.String("gen_ai.provider.name", "openai"),
		attribute.String("gen_ai.request.model", "check"),
		attribute.String("gen_ai.conversation.id", "vera-check"),
		attribute.String("gen_ai.input.messages", "is this reaching Grafana?"),
		attribute.String("gen_ai.output.messages", "if you are reading this in Agent Observability, yes."),
		attribute.Int("gen_ai.usage.input_tokens", 7),
		attribute.Int("gen_ai.usage.output_tokens", 11),
	)
	span.End()

	labels := []attribute.KeyValue{
		attribute.String("gen_ai.operation.name", "chat"),
		attribute.String("gen_ai.provider.name", "openai"),
		attribute.String("gen_ai.request.model", "check"),
	}
	mind.duration.Record(ctx, 0.42, metric.WithAttributes(labels...))
	mind.firstToken.Record(ctx, 0.21, metric.WithAttributes(labels...))
	mind.tokens.Record(ctx, 7, metric.WithAttributes(
		append(append([]attribute.KeyValue{}, labels...),
			attribute.String("gen_ai.token.type", "input"))...))

	if err := p.check(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "vera: "+err.Error())
		os.Exit(1)
	}
	fmt.Println("telemetry reached " + os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	fmt.Println("look for service.name=" + serviceName + " and gen_ai.conversation.id=vera-check")
}

func printMemories(place *home.Home, m *home.Memory) {
	facts := m.All()
	fmt.Println(place.Root)
	if len(facts) == 0 {
		fmt.Println("\nVera remembers nothing yet.")
		return
	}
	fmt.Printf("\n%s:\n\n", quantity(len(facts), "thing remembered", "things remembered"))
	for _, f := range facts {
		fmt.Printf("  %s\n", f.Description)
		fmt.Printf("      %s/%s.md · %s · since %s\n", home.MemoryDir, f.Name, f.Type, f.Since.Format("2 Jan 2006"))
	}
}

// Forgetting is by slug, which is also the file name — so "forget it"
// and "delete that file" are the same act, and either one works.
func forgetFacts(m *home.Memory, spec string) {
	if strings.EqualFold(strings.TrimSpace(spec), "all") {
		fmt.Printf("forgot %s\n", quantity(m.ForgetAll(), "thing", "things"))
		return
	}
	var names []string
	for _, piece := range strings.Split(spec, ",") {
		if piece = strings.TrimSpace(piece); piece != "" {
			names = append(names, strings.TrimSuffix(piece, ".md"))
		}
	}
	if len(names) == 0 {
		fmt.Fprintln(os.Stderr, "vera: nothing to forget — pass slugs like \"lives-in-vienna,owns-a-dog\" or \"all\"")
		os.Exit(1)
	}
	fmt.Printf("forgot %s\n", quantity(m.Forget(names...), "thing", "things"))
}

// legacyMemoryPath is where memory lived before home: one JSON array
// under the state directory. Read once, migrated, and kept beside its
// replacement as memory.json.migrated.
func legacyMemoryPath() string {
	return filepath.Join(stateDir(), "vera", "memory.json")
}

func quantity(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// loadEnvFiles reads every ~/.config/vera/*.env into the environment.
// `vera start` and launchd both start verad without a login shell, so
// this is how grafana.env reaches it.
func loadEnvFiles() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	files, _ := filepath.Glob(filepath.Join(home, ".config", "vera", "*.env"))
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(b), "\n") {
			line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "export "))
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			k, v, ok := strings.Cut(line, "=")
			if !ok || strings.TrimSpace(k) == "" {
				continue
			}
			k, v = strings.TrimSpace(k), strings.TrimSpace(v)
			if u, err := strconv.Unquote(v); err == nil {
				v = u
			}
			if os.Getenv(k) == "" {
				_ = os.Setenv(k, v)
			}
		}
	}
}
