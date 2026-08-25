// The chat — the laptop's way of talking to Vera.
//
// The phone is the face; this is the workbench. A pane inside rook
// (pin it to the rail) that speaks the same wire the phone does —
// /say frames, /fleet, /status, the identity file for the secret —
// and shows what the phone cannot: the status lines as they stream,
// tool calls as they happen, every task and its state, and what Vera
// currently believes about where you are. It exists so iterating on
// the mind is typing, not picking up a phone.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/incantery/vera/dump"
	"github.com/incantery/vera/fleet"
)

func runChat(args []string) {
	fs := flag.NewFlagSet("vera chat", flag.ExitOnError)
	url := fs.String("url", "", "where verad listens (default: the running one, started if needed)")
	debug := fs.Bool("debug", false, "start with the belief panel open")
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
	if _, err := c.status(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "vera: cannot reach "+base+" ("+err.Error()+")")
		os.Exit(1)
	}
	m := newChatModel(c, *debug)
	if _, err := tea.NewProgram(m, tea.WithAltScreen()).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "vera: "+err.Error())
		os.Exit(1)
	}
}

// --- the client: the phone's wire, from a terminal ----------------------

type chatClient struct {
	base, secret, device string
	http                 http.Client
}

func (c *chatClient) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, r)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.secret)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, errors.New(strings.TrimSpace(string(msg)))
	}
	return resp, nil
}

func (c *chatClient) getJSON(ctx context.Context, path string, v any) error {
	resp, err := c.do(ctx, "GET", path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(v)
}

func (c *chatClient) status(ctx context.Context) (*Status, error) {
	var s Status
	return &s, c.getJSON(ctx, "/status?device="+c.device, &s)
}

func (c *chatClient) tasks(ctx context.Context) ([]fleet.View, error) {
	var v []fleet.View
	return v, c.getJSON(ctx, "/fleet", &v)
}

func (c *chatClient) post(ctx context.Context, path string, body any) error {
	resp, err := c.do(ctx, "POST", path, body)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// say streams frames to fn until the terminal one.
func (c *chatClient) say(ctx context.Context, text, conversation string, fn func(Frame)) error {
	resp, err := c.do(ctx, "POST", "/say", Message{Text: text, Conversation: conversation, Device: c.device})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		var f Frame
		if json.Unmarshal(sc.Bytes(), &f) != nil {
			continue
		}
		fn(f)
		if f.Done || f.Error != "" {
			return nil
		}
	}
	return sc.Err()
}

// --- the model ----------------------------------------------------------

type chatLine struct {
	who  string // "you", "vera", "note", "err"
	text string
}

type chatModel struct {
	c            *chatClient
	conversation string

	lines    []chatLine
	partial  string // the answer as it streams
	status   string // the current status line, "" when quiet
	thinking bool

	tasks  []fleet.View
	states map[string]fleet.State // last state per task, to notice changes
	belief *Status
	debug  bool

	vp     viewport.Model
	input  textinput.Model
	width  int
	height int
	frames chan tea.Msg
}

type (
	frameMsg  struct{ f Frame }
	sayDone   struct{ err error }
	tasksMsg  struct{ v []fleet.View }
	beliefMsg struct{ s *Status }
	noteMsg   struct{ text string }
	tickMsg   struct{}
)

func newChatModel(c *chatClient, debug bool) *chatModel {
	in := textinput.New()
	in.Placeholder = "say something, or /help"
	in.Prompt = "› "
	in.Focus()
	in.CharLimit = 4000
	return &chatModel{
		c:            c,
		conversation: "chat-" + time.Now().Format("20060102-150405"),
		input:        in,
		vp:           viewport.New(80, 20),
		debug:        debug,
		frames:       make(chan tea.Msg, 64),
		states:       map[string]fleet.State{},
	}
}

func (m *chatModel) Init() tea.Cmd {
	return tea.Batch(m.refresh(), tick(), m.waitFrame())
}

func tick() tea.Cmd {
	return tea.Tick(5*time.Second, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m *chatModel) refresh() tea.Cmd {
	c := m.c
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		v, _ := c.tasks(ctx)
		return tasksMsg{v}
	}
}

func (m *chatModel) believe() tea.Cmd {
	c := m.c
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		s, _ := c.status(ctx)
		return beliefMsg{s}
	}
}

// waitFrame hands the next streamed frame to Update. A streaming reply
// is a goroutine feeding a channel; this is the channel's other end.
func (m *chatModel) waitFrame() tea.Cmd {
	ch := m.frames
	return func() tea.Msg { return <-ch }
}

func (m *chatModel) send(text string) tea.Cmd {
	m.lines = append(m.lines, chatLine{"you", text})
	m.partial, m.status, m.thinking = "", "", true
	m.render()
	c, conv, ch := m.c, m.conversation, m.frames
	return func() tea.Msg {
		err := c.say(context.Background(), text, conv, func(f Frame) { ch <- frameMsg{f} })
		ch <- sayDone{err}
		return nil
	}
}

func (m *chatModel) note(s string) { m.lines = append(m.lines, chatLine{"note", s}); m.render() }
func (m *chatModel) fail(s string) { m.lines = append(m.lines, chatLine{"err", s}); m.render() }

func (m *chatModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			return m, tea.Quit
		case "f2":
			m.debug = !m.debug
			m.layout()
			return m, m.believe()
		case "enter":
			text := strings.TrimSpace(m.input.Value())
			if text == "" {
				return m, nil
			}
			m.input.SetValue("")
			if strings.HasPrefix(text, "/") {
				return m, m.command(text)
			}
			if m.thinking {
				m.fail("still answering; wait for it")
				return m, nil
			}
			return m, m.send(text)
		case "pgup", "pgdown", "ctrl+u", "ctrl+d":
			var cmd tea.Cmd
			m.vp, cmd = m.vp.Update(msg)
			return m, cmd
		}

	case frameMsg:
		f := msg.f
		switch {
		case f.Error != "":
			m.fail(f.Error)
		case f.Status != "":
			m.status = f.Status
		case f.Delta != "":
			m.partial += f.Delta
			m.status = ""
		}
		m.render()
		return m, m.waitFrame()

	case sayDone:
		if msg.err != nil {
			m.fail(msg.err.Error())
		}
		if m.partial != "" {
			m.lines = append(m.lines, chatLine{"vera", m.partial})
		}
		m.partial, m.status, m.thinking = "", "", false
		m.render()
		return m, tea.Batch(m.waitFrame(), m.refresh(), m.believe())

	case tasksMsg:
		m.tasks = msg.v
		m.notice(msg.v)
		m.layout()
		return m, nil

	case beliefMsg:
		m.belief = msg.s
		return m, nil

	case noteMsg:
		if msg.text != "" {
			m.note(msg.text)
		}
		return m, m.refresh()

	case tickMsg:
		cmds := []tea.Cmd{m.refresh(), tick()}
		if m.debug {
			cmds = append(cmds, m.believe())
		}
		return m, tea.Batch(cmds...)
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// notice says, in the transcript, when a task's state changed to one
// that needs the person — the nudge the strip alone cannot give. The
// first sighting of a task is not news.
func (m *chatModel) notice(views []fleet.View) {
	for _, v := range views {
		prev, seen := m.states[v.ID]
		m.states[v.ID] = v.State
		if !seen || prev == v.State {
			continue
		}
		// Landed on its own: say what happened, in the supervisor's
		// words, so a wrong landing is visible at once.
		if v.State == fleet.Closed {
			line := "✓ " + v.ID + " — " + trim(firstSentence(v.Brief), 60)
			if v.Last != nil && v.Last.Text != "" {
				line += ": " + trim(v.Last.Text, 200)
			}
			m.note(line)
			continue
		}
		if !v.State.Actionable() {
			continue
		}
		line := fmt.Sprintf("● %s %s — %s", v.ID, fleetPhrase(v, time.Now()), trim(firstSentence(v.Brief), 60))
		if v.Last != nil && v.Last.Text != "" {
			line += ": " + trim(v.Last.Text, 200)
		}
		if v.Report != "" {
			line += "  (/report " + v.ID + ")"
		}
		m.note(line)
	}
}

// command runs a slash command. These are the fleet verbs by hand —
// the same calls the mind's fleet tool makes, for when you know
// exactly what you want and do not need to say it in prose.
func (m *chatModel) command(text string) tea.Cmd {
	verb, rest, _ := strings.Cut(strings.TrimPrefix(text, "/"), " ")
	rest = strings.TrimSpace(rest)
	id, arg, _ := strings.Cut(rest, " ")
	c := m.c
	post := func(path string, body any, ok string) tea.Cmd {
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := c.post(ctx, path, body); err != nil {
				return noteMsg{"✗ " + err.Error()}
			}
			return noteMsg{ok}
		}
	}
	switch verb {
	case "help", "?":
		m.note("/tasks · /start [@repo] <brief> · /scout [@repo] <brief> · /resume <id> · /report <id> · /answer <id> <text> · /land <id> · /stop <id> [force] · /seen <id> · /new (conversation) · /dump [note] · /debug (F2) · /quit")
	case "quit", "q":
		return tea.Quit
	case "tasks", "t":
		return m.refresh()
	case "new":
		m.conversation = "chat-" + time.Now().Format("20060102-150405")
		m.note("new conversation " + m.conversation)
	case "dump":
		// This conversation and every task it touched, in a folder;
		// the rest of the line is the note at the top of its README.
		conv := m.conversation
		return func() tea.Msg {
			res, err := dump.Build(dump.Options{Conversations: []string{conv}, Note: rest, Version: version, Tar: true})
			if err != nil {
				return noteMsg{"✗ dump: " + err.Error()}
			}
			return noteMsg{"dumped → " + strings.ReplaceAll(describeDump(res), "\n", " · ")}
		}
	case "debug":
		m.debug = !m.debug
		m.layout()
		return m.believe()
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
			m.fail("/" + verb + " [@repo] <brief>")
			return nil
		}
		kind := fleet.Ship
		if verb == "scout" {
			kind = fleet.Scout
		}
		where := project
		if where == "" {
			where = "the repo in front of you"
		}
		return post("/fleet", fleet.Request{Project: project, Kind: kind, Brief: rest}, "started in "+where)
	case "resume":
		if id == "" {
			m.fail("/resume <id>")
			return nil
		}
		return post("/fleet/"+id+"/resume", nil, "resumed "+id)
	case "answer", "a":
		if id == "" || arg == "" {
			m.fail("/answer <id> <text>")
			return nil
		}
		return post("/fleet/"+id+"/answer", map[string]string{"text": arg}, "sent to "+id)
	case "land":
		if id == "" {
			m.fail("/land <id>")
			return nil
		}
		return post("/fleet/"+id+"/land", nil, "landed "+id)
	case "stop":
		if id == "" {
			m.fail("/stop <id> [force]")
			return nil
		}
		path := "/fleet/" + id + "/teardown"
		if arg == "force" {
			path += "?force=1"
		}
		return post(path, nil, "stopped "+id)
	case "report", "r":
		if id == "" {
			m.fail("/report <id>")
			return nil
		}
		for _, v := range m.tasks {
			if v.ID == id {
				if v.Report == "" {
					m.note(id + " has written no report yet")
				} else {
					m.note("report from " + id + ":\n" + v.Report)
					// Read is seen: a scout whose report was read is done.
					return post("/fleet/"+id+"/seen", nil, "")
				}
				return nil
			}
		}
		m.fail("no task " + id)
		return nil
	case "seen":
		if id == "" {
			m.fail("/seen <id>")
			return nil
		}
		return post("/fleet/"+id+"/seen", nil, "marked "+id+" seen")
	default:
		m.fail("unknown command /" + verb + " — /help")
	}
	return nil
}

// --- drawing ------------------------------------------------------------

var (
	youStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
	veraStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("5")).Bold(true)
	noteStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	errStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	statusStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Italic(true)
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	needStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Bold(true)
	doneStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	ruleStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
)

// layout sizes the transcript to what is left after the strip, the
// belief panel and the input.
func (m *chatModel) layout() {
	if m.width == 0 {
		return
	}
	fixed := 2 // input + rule
	fixed += len(m.stripLines())
	if m.debug {
		fixed += len(m.beliefLines())
	}
	h := m.height - fixed
	if h < 3 {
		h = 3
	}
	m.vp.Width = m.width
	m.vp.Height = h
	m.input.Width = m.width - 4
	m.render()
}

func (m *chatModel) render() {
	var b strings.Builder
	w := m.width
	if w < 20 {
		w = 80
	}
	wrap := lipgloss.NewStyle().Width(w - 2)
	for _, l := range m.lines {
		switch l.who {
		case "you":
			b.WriteString(youStyle.Render("you ") + wrap.Render(l.text) + "\n")
		case "vera":
			b.WriteString(veraStyle.Render("vera ") + wrap.Render(l.text) + "\n")
		case "note":
			b.WriteString(noteStyle.Render(wrap.Render(l.text)) + "\n")
		case "err":
			b.WriteString(errStyle.Render(wrap.Render(l.text)) + "\n")
		}
	}
	if m.partial != "" {
		b.WriteString(veraStyle.Render("vera ") + wrap.Render(m.partial) + "\n")
	}
	if m.status != "" {
		b.WriteString(statusStyle.Render(m.status) + "\n")
	} else if m.thinking && m.partial == "" {
		b.WriteString(statusStyle.Render("…") + "\n")
	}
	m.vp.SetContent(b.String())
	m.vp.GotoBottom()
}

// stripLines is the fleet, one line per open task, in the person's
// nouns; closed ones are counted, not listed.
func (m *chatModel) stripLines() []string {
	var open []fleet.View
	closed := 0
	for _, v := range m.tasks {
		if v.Closed {
			closed++
		} else {
			open = append(open, v)
		}
	}
	if len(open) == 0 && closed == 0 {
		return nil
	}
	lines := []string{ruleStyle.Render(strings.Repeat("─", max(m.width, 20)))}
	for _, v := range open {
		state := string(v.State)
		style := dimStyle
		if v.State.Actionable() {
			style = needStyle
		}
		if v.State == fleet.Running {
			style = doneStyle
		}
		unread := ""
		if n := len(v.Unread); n > 0 {
			unread = fmt.Sprintf(" +%d", n)
		}
		last := ""
		if v.Last != nil && v.Last.Text != "" {
			last = " — " + trim(v.Last.Text, 60)
		}
		lines = append(lines, fmt.Sprintf("%s %s %s%s%s",
			dimStyle.Render(v.ID), style.Render(fmt.Sprintf("%-9s", state)),
			trim(firstSentence(v.Brief), 50), dimStyle.Render(unread), dimStyle.Render(last)))
	}
	if closed > 0 {
		lines = append(lines, dimStyle.Render(fmt.Sprintf("%d closed", closed)))
	}
	return lines
}

// beliefLines is what Vera currently holds about where you are — the
// same facts the model's preface is built from.
func (m *chatModel) beliefLines() []string {
	lines := []string{ruleStyle.Render(strings.Repeat("─", max(m.width, 20)))}
	if m.belief == nil {
		return append(lines, dimStyle.Render("belief: …"))
	}
	s := m.belief
	lines = append(lines, dimStyle.Render(fmt.Sprintf("%s · %s · %d runs in flight · conversation %s", s.Name, s.Mind, s.RunsInFlight, m.conversation)))
	for _, d := range s.Devices {
		fresh := "quiet"
		if d.Fresh {
			fresh = "fresh"
		}
		line := d.Name + " (" + fresh + ")"
		if d.Focus != nil {
			line += ": " + d.Focus.Name
			if d.FocusSince != nil {
				line += " for " + roughDuration(time.Since(*d.FocusSince))
			}
		}
		if d.Terminal != nil {
			line += " · inside, " + d.Terminal.Describe()
		}
		lines = append(lines, dimStyle.Render(line))
	}
	var conn []string
	for _, i := range s.Integrations {
		if i.Connected {
			conn = append(conn, i.Name)
		}
	}
	if len(conn) > 0 {
		lines = append(lines, dimStyle.Render("connected: "+strings.Join(conn, ", ")))
	}
	return lines
}

func (m *chatModel) View() string {
	var b strings.Builder
	b.WriteString(m.vp.View())
	b.WriteString("\n")
	for _, l := range m.stripLines() {
		b.WriteString(l + "\n")
	}
	if m.debug {
		for _, l := range m.beliefLines() {
			b.WriteString(l + "\n")
		}
	}
	b.WriteString(ruleStyle.Render(strings.Repeat("─", max(m.width, 20))) + "\n")
	b.WriteString(m.input.View())
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
