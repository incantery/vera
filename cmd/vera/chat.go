// The chat — the laptop's way of talking to Vera.
//
// The phone is the face; this is the workbench. A pane inside rook
// (pin it to the rail) that speaks the same wire the phone does —
// /say frames, /fleet, /status, the identity file for the secret.
// The screen itself is mote's: streaming markdown, tool calls as
// cards, a rail, slash commands. Everything here is what Vera puts
// on that screen — an agent over the wire, the fleet as the rail,
// the fleet verbs as commands.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/incantery/mote/agent"
	"github.com/incantery/mote/tui"
	"github.com/incantery/vera/dump"
	"github.com/incantery/vera/fleet"
)

// fleetEvery is how often the fleet is re-read. The rail is drawn
// from the cache far more often than that.
const fleetEvery = 5 * time.Second

func runChat(args []string) {
	fs := flag.NewFlagSet("vera chat", flag.ExitOnError)
	url := fs.String("url", "", "where verad listens (default: the running one, started if needed)")
	debug := fs.Bool("debug", false, "open with what Vera believes about where you are")
	_ = fs.Parse(args)

	base := strings.TrimRight(*url, "/")
	if base == "" {
		b, err := ensure()
		if err != nil {
			fmt.Fprintln(os.Stderr, "vera: "+err.Error())
			os.Exit(1)
		}
		base = b
	}
	id, err := loadIdentity(identityPath())
	if err != nil {
		fmt.Fprintln(os.Stderr, "vera: no identity at "+identityPath()+" ("+err.Error()+")")
		os.Exit(1)
	}
	c := &chatClient{base: base, secret: id.Secret, device: id.Name}
	st, err := c.status(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, "vera: cannot reach "+base+" ("+err.Error()+")")
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := newFleetWatch(c)
	go w.run(ctx, fleetEvery)

	s := &chatSession{c: c, w: w, conv: newConversation()}
	greeting := "**" + st.Name + "** — say something, or `/` for the fleet. " +
		"`/help` has the keys; the rail on the right is every task and what is believed about it."
	if *debug {
		greeting += "\n\n" + beliefMarkdown(st, s.conversation())
	}

	err = tui.Run(veraAgent{c}, tui.Options{
		Name:         st.Name,
		Model:        st.Mind,
		Conversation: s.conversation(),
		Greeting:     greeting,
		Side:         w.side,
		SideTitle:    "fleet",
		Notices:      w.notices,
		Commands:     chatCommands,
		Handle:       s.handle,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "vera: "+err.Error())
		os.Exit(1)
	}
}

func newConversation() string { return "chat-" + time.Now().Format("20060102-150405") }

// --- the fleet, watched ---------------------------------------------------

// fleetWatch keeps the fleet where the terminal can read it without a
// request: the rail is drawn on the UI goroutine, so it gets a cached
// answer, and anything worth saying out loud goes down notices.
type fleetWatch struct {
	c       *chatClient
	notices chan agent.Event

	mu     sync.Mutex
	views  []fleet.View
	states map[string]fleet.State // last state per task, to notice changes
}

func newFleetWatch(c *chatClient) *fleetWatch {
	return &fleetWatch{c: c, notices: make(chan agent.Event, 64), states: map[string]fleet.State{}}
}

func (w *fleetWatch) run(ctx context.Context, every time.Duration) {
	defer close(w.notices)
	t := time.NewTicker(every)
	defer t.Stop()
	w.poll(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.poll(ctx)
		}
	}
}

// poll re-reads the fleet now. A failed read leaves the last one
// standing: a rail that empties because one request timed out reads
// as "the tasks are gone".
func (w *fleetWatch) poll(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	v, err := w.c.tasks(ctx)
	if err != nil {
		return
	}
	for _, line := range w.absorb(v, time.Now()) {
		select {
		case w.notices <- agent.Notice(line):
		default: // nobody reading, or a burst: the rail still says it
		}
	}
}

// absorb replaces the cache and returns what is worth saying about it.
func (w *fleetWatch) absorb(views []fleet.View, now time.Time) []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.views = views
	var out []string
	for _, v := range views {
		if v.Task == nil {
			continue
		}
		prev, seen := w.states[v.ID]
		w.states[v.ID] = v.State
		if line, ok := noticeFor(prev, seen, v, now); ok {
			out = append(out, line)
		}
	}
	return out
}

func (w *fleetWatch) snapshot() []fleet.View {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]fleet.View(nil), w.views...)
}

func (w *fleetWatch) side() []tui.SideItem { return sideItems(w.snapshot()) }

// lines is the fleet as the transcript shows it, for /tasks.
func (w *fleetWatch) lines() string {
	var b strings.Builder
	closed := 0
	for _, v := range w.snapshot() {
		if v.Task == nil {
			continue
		}
		if v.Closed {
			closed++
			continue
		}
		fmt.Fprintf(&b, "%s  %-9s %s", v.ID, v.State, trim(firstSentence(v.Brief), 60))
		if v.Last != nil && v.Last.Text != "" {
			b.WriteString(" — " + trim(oneLine(v.Last.Text), 80))
		}
		b.WriteString("\n")
	}
	if closed > 0 {
		fmt.Fprintf(&b, "%d closed\n", closed)
	}
	if b.Len() == 0 {
		return "no tasks"
	}
	return strings.TrimRight(b.String(), "\n")
}

// noticeFor says, in the transcript, when a task's state changed to
// one that needs the person — the nudge the rail alone cannot give.
// The first sighting of a task is not news.
func noticeFor(prev fleet.State, seen bool, v fleet.View, now time.Time) (string, bool) {
	if !seen || prev == v.State {
		return "", false
	}
	// Landed on its own: say what happened, in the supervisor's
	// words, so a wrong landing is visible at once.
	if v.State == fleet.Closed {
		line := "✓ " + v.ID + " — " + trim(firstSentence(v.Brief), 60)
		if v.Last != nil && v.Last.Text != "" {
			line += ": " + trim(v.Last.Text, 200)
		}
		return line, true
	}
	if !v.State.Actionable() {
		return "", false
	}
	line := fmt.Sprintf("● %s %s — %s", v.ID, fleetPhrase(v, now), trim(firstSentence(v.Brief), 60))
	if v.Last != nil && v.Last.Text != "" {
		line += ": " + trim(v.Last.Text, 200)
	}
	if v.Report != "" {
		line += "  (/report " + v.ID + ")"
	}
	return line, true
}

// sideState maps what Vera believes onto the five things a person
// does about it: leave it, look at it, answer it, land it, fix it.
// A state with no row — closed, or one this binary is too old to
// know — is left off the rail rather than guessed at.
func sideState(s fleet.State) (tui.State, bool) {
	switch s {
	case fleet.Running, fleet.Quiet:
		return tui.Working, true
	case fleet.Waiting, fleet.Stale, fleet.Held:
		return tui.Idle, true
	case fleet.Decision, fleet.Broken:
		return tui.Blocked, true
	case fleet.Finished:
		return tui.Done, true
	case fleet.Gone:
		return tui.Failed, true
	}
	return "", false
}

func sideItems(views []fleet.View) []tui.SideItem {
	var out []tui.SideItem
	for _, v := range views {
		if v.Task == nil || v.Closed {
			continue
		}
		st, ok := sideState(v.State)
		if !ok {
			continue
		}
		out = append(out, tui.SideItem{
			ID:       v.ID,
			Title:    trim(firstSentence(v.Brief), 60),
			Subtitle: sideSubtitle(v),
			State:    st,
		})
	}
	return out
}

// sideSubtitle is the one line under a task: what it last said, or
// failing that where it is working.
func sideSubtitle(v fleet.View) string {
	sub := shortPath(v.Project)
	if v.Last != nil && v.Last.Text != "" {
		sub = trim(oneLine(v.Last.Text), 80)
	}
	if n := len(v.Unread); n > 0 {
		sub = fmt.Sprintf("+%d %s", n, sub)
	}
	return sub
}

// --- the commands ---------------------------------------------------------

// chatCommands are the fleet verbs by hand — the same calls the
// mind's fleet tool makes, for when you know exactly what you want
// and do not need to say it in prose. /help is mote's.
var chatCommands = []tui.Command{
	{Name: "tasks", Help: "every task and what is believed about it"},
	{Name: "start", Help: "/start [@repo] <brief> — put a task on the rail"},
	{Name: "scout", Help: "/scout [@repo] <brief> — a task that reports instead of landing"},
	{Name: "resume", Help: "/resume <id> — pick a task back up"},
	{Name: "report", Help: "/report <id> — what a task wrote (and mark it seen)"},
	{Name: "answer", Help: "/answer <id> <text> — reply to a task that asked"},
	{Name: "land", Help: "/land <id> — merge its branch and close it"},
	{Name: "stop", Help: "/stop <id> [force] — tear a task down"},
	{Name: "seen", Help: "/seen <id> — you have read it"},
	{Name: "new", Help: "a fresh conversation id"},
	{Name: "dump", Help: "/dump [note] — this conversation, in a folder, to report a problem"},
	{Name: "debug", Help: "what Vera believes about where you are"},
	{Name: "quit", Help: "leave"},
}

// chatSession is the little the commands need to share: the wire, the
// fleet cache, and which conversation is current.
type chatSession struct {
	c *chatClient
	w *fleetWatch

	mu   sync.Mutex
	conv string
}

func (s *chatSession) conversation() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conv
}

func (s *chatSession) setConversation(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.conv = id
}

func (s *chatSession) handle(name, rest string) tea.Cmd {
	rest = strings.TrimSpace(rest)
	id, arg, _ := strings.Cut(rest, " ")
	arg = strings.TrimSpace(arg)

	switch name {
	case "quit", "q":
		return tea.Quit

	case "tasks", "t":
		w := s.w
		return off(func(ctx context.Context) tea.Cmd {
			w.poll(ctx)
			return tea.Batch(tui.Note("%s", w.lines()), tui.Refresh())
		})

	case "new":
		conv := newConversation()
		s.setConversation(conv)
		return tea.Batch(tui.SetConversation(conv), tui.Note("new conversation %s", conv))

	case "dump":
		// This conversation and every task it touched, in a folder;
		// the rest of the line is the note at the top of its README.
		conv, note := s.conversation(), rest
		return off(func(context.Context) tea.Cmd {
			res, err := dump.Build(dump.Options{Conversations: []string{conv}, Note: note, Version: version, Tar: true})
			if err != nil {
				return tui.Fail("dump: %s", err)
			}
			return tui.Note("dumped → %s", strings.ReplaceAll(describeDump(res), "\n", " · "))
		})

	case "debug":
		// mote has one rail and the fleet is on it, so what Vera
		// believes about where you are is a block you ask for.
		c, conv := s.c, s.conversation()
		return off(func(ctx context.Context) tea.Cmd {
			st, err := c.status(ctx)
			if err != nil {
				return tui.Fail("status: %s", err)
			}
			return tui.Show(beliefMarkdown(st, conv))
		})

	case "start", "scout":
		// /start @rook <brief> names the repository; without it, the
		// one in front of them (verad resolves it — the chat's own cwd
		// means nothing).
		project := ""
		if strings.HasPrefix(rest, "@") {
			project, rest, _ = strings.Cut(rest[1:], " ")
			rest = strings.TrimSpace(rest)
		}
		if rest == "" {
			return tui.Fail("/%s [@repo] <brief>", name)
		}
		kind := fleet.Ship
		if name == "scout" {
			kind = fleet.Scout
		}
		where := project
		if where == "" {
			where = "the repo in front of you"
		}
		return s.post("/fleet", fleet.Request{Project: project, Kind: kind, Brief: rest}, "started in "+where)

	case "resume":
		if id == "" {
			return tui.Fail("/resume <id>")
		}
		return s.post("/fleet/"+id+"/resume", nil, "resumed "+id)

	case "answer", "a":
		if id == "" || arg == "" {
			return tui.Fail("/answer <id> <text>")
		}
		return s.post("/fleet/"+id+"/answer", map[string]string{"text": arg}, "sent to "+id)

	case "land":
		if id == "" {
			return tui.Fail("/land <id>")
		}
		return s.post("/fleet/"+id+"/land", nil, "landed "+id)

	case "stop":
		if id == "" {
			return tui.Fail("/stop <id> [force]")
		}
		path := "/fleet/" + id + "/teardown"
		if arg == "force" {
			path += "?force=1"
		}
		return s.post(path, nil, "stopped "+id)

	case "seen":
		if id == "" {
			return tui.Fail("/seen <id>")
		}
		return s.post("/fleet/"+id+"/seen", nil, "marked "+id+" seen")

	case "report", "r":
		if id == "" {
			return tui.Fail("/report <id>")
		}
		for _, v := range s.w.snapshot() {
			if v.Task == nil || v.ID != id {
				continue
			}
			if v.Report == "" {
				return tui.Note("%s has written no report yet", id)
			}
			// Read is seen: a scout whose report was read is done.
			return tea.Batch(tui.Show(v.Report), s.post("/fleet/"+id+"/seen", nil, ""))
		}
		return tui.Fail("no task %s", id)
	}
	return tui.Fail("unknown command /%s — /help", name)
}

// post is one call to verad, off the UI goroutine, followed by a fresh
// read of the fleet so the rail is right by the time the note lands.
func (s *chatSession) post(path string, body any, ok string) tea.Cmd {
	c, w := s.c, s.w
	return off(func(ctx context.Context) tea.Cmd {
		if err := c.post(ctx, path, body); err != nil {
			return tui.Fail("%s", err)
		}
		w.poll(ctx)
		if ok == "" {
			return tui.Refresh()
		}
		return tea.Batch(tui.Note("%s", ok), tui.Refresh())
	})
}

// off runs a command's work away from the UI goroutine and hands back
// whatever it decided to put on the screen.
func off(fn func(context.Context) tea.Cmd) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		cmd := fn(ctx)
		if cmd == nil {
			return nil
		}
		return cmd()
	}
}

// beliefMarkdown is what Vera currently holds about where you are —
// the same facts the model's preface is built from.
func beliefMarkdown(s *Status, conversation string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "**%s** · %s · %d run(s) in flight · conversation `%s`\n", s.Name, s.Mind, s.RunsInFlight, conversation)
	if len(s.Devices) == 0 {
		b.WriteString("\nNo device has reported in.\n")
	}
	for _, d := range s.Devices {
		fresh := "quiet"
		if d.Fresh {
			fresh = "fresh"
		}
		line := "**" + d.Name + "** (" + fresh + ")"
		if d.Focus != nil {
			line += ": " + d.Focus.Name
			if d.FocusSince != nil {
				line += " for " + roughDuration(time.Since(*d.FocusSince))
			}
		}
		if d.Terminal != nil {
			line += " · inside, " + d.Terminal.Describe()
		}
		b.WriteString("\n- " + line)
	}
	var conn []string
	for _, i := range s.Integrations {
		if i.Connected {
			conn = append(conn, i.Name)
		}
	}
	if len(conn) > 0 {
		b.WriteString("\n\nconnected: " + strings.Join(conn, ", "))
	}
	return b.String()
}

// fleetPhrase says what is believed, in words a person would use —
// the same phrasing verad gives the mind.
func fleetPhrase(v fleet.View, now time.Time) string {
	switch v.State {
	case fleet.Running:
		return "working"
	case fleet.Quiet:
		return "working, quiet for a bit"
	case fleet.Stale:
		return "has gone quiet — worth a look"
	case fleet.Waiting:
		if !v.TurnEnded.IsZero() {
			return "waiting on you for " + roughDuration(now.Sub(v.TurnEnded))
		}
		return "waiting on you"
	case fleet.Decision:
		return "blocked on a decision from you"
	case fleet.Held:
		return "paused on something external"
	case fleet.Finished:
		return "finished — ready to land"
	case fleet.Broken:
		return "failed"
	case fleet.Gone:
		return "its terminal is gone — /resume picks it up"
	default:
		return string(v.State)
	}
}

// oneLine flattens a multi-line status into something a rail row or a
// single transcript line can hold.
func oneLine(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " ")), " ")
}
