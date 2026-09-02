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
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/incantery/mote/agent"
	"github.com/incantery/mote/session"
	"github.com/incantery/mote/tui"
	"github.com/incantery/vera/attach"
	"github.com/incantery/vera/costs"
	"github.com/incantery/vera/dump"
	"github.com/incantery/vera/events"
	"github.com/incantery/vera/fleet"
	"github.com/incantery/vera/home"
)

// fleetEvery is how often the fleet is re-read. The rail is drawn
// from the cache far more often than that.
const fleetEvery = 5 * time.Second

func runChat(args []string) {
	fs := flag.NewFlagSet("vera chat", flag.ExitOnError)
	url := fs.String("url", "", "where verad listens (default: the running one, started if needed)")
	debug := fs.Bool("debug", false, "open with what Vera believes about where you are")
	reopen := fs.String("c", "", "reopen this conversation (see `vera sessions`)")
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
	w.absorbStatus(st)
	go w.run(ctx, fleetEvery)

	// The conversation on disk. verad journals the exchange — that is
	// its record of the model. This is the terminal's record of the
	// screen: what was said, what was drawn, what was typed, kept so
	// that quitting is not the same as forgetting.
	dir := chatSessionDir()
	conv := *reopen
	if conv == "" {
		conv = newConversation()
	}
	sess, err := session.Open(dir, conv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "vera: cannot open "+dir+" ("+err.Error()+")")
		os.Exit(1)
	}
	// /new opens another one, and every file this run opened has to be
	// closed, so they are kept together.
	open := &openSessions{list: []*session.Session{sess}}
	defer open.closeAll()

	s := &chatSession{c: c, w: w, conv: conv, dir: dir, open: open, board: macPasteboard()}
	// The watch asks about whichever conversation is current, and /new
	// moves that — so it reads the id rather than being handed one.
	w.conv = s.conversation
	w.pollModel(ctx)
	greeting := chatGreeting(st, sess)
	if *debug {
		greeting += "\n\n" + beliefMarkdown(st, s.conversation())
	}

	// Not tui.Run: the watch needs a way to put a message on the
	// screen from off the UI goroutine. The model is the one thing on
	// the status line that another window can change — a `vera say -m`,
	// a `/model` on the phone — so when the poll notices it moved, it
	// says so with tui.SetModel rather than waiting for somebody to
	// type here. (The rail no longer needs anything: Options.SideClosed
	// is how mote is told to start it hidden.)
	p := tea.NewProgram(withPasteKey(tui.New(veraAgent{c: c, held: &s.held}, chatOptions(st, s, sess, greeting)), s))
	w.sendWith(p.Send)
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "vera: "+err.Error())
		os.Exit(1)
	}
}

// insideRook: this chat is a pane in rook, which has an agents pane of
// its own showing the same fleet. ROOK_MUX_SOCK is what rook exports
// for its socket (see mux.RookSock); ROOK_SOCK is the older name and
// is still honoured.
func insideRook() bool {
	return os.Getenv("ROOK_MUX_SOCK") != "" || os.Getenv("ROOK_SOCK") != ""
}

// staying is what a refused change leaves the conversation on, for
// the middle line of a failure. A watch that has not answered yet
// says the honest thing rather than naming a model it is guessing.
func staying(w *fleetWatch) string {
	if line := w.resolution().Short(); line != "" {
		return "this conversation stays on " + line
	}
	return "nothing changed"
}

// statusName is the name on the status line — and mostly there is
// none.
//
// The reference drops the hostname: this is a single-machine UI, and
// a machine name that never changes spends a column of the line on a
// constant, then truncates everything after it. It comes back the
// moment it stops being a constant — `vera chat -url` pointed at
// another Mac's verad, where which machine is answering is the most
// load-bearing fact on the screen.
//
// mote insists on a name (an empty one becomes "agent"), so the local
// case says "vera": the shortest true thing, and nothing that cuts.
func statusName(st *Status, base string) string {
	if elsewhere(base) && st != nil && st.Name != "" {
		return st.Name
	}
	return "vera"
}

// elsewhere: this chat is talking to a verad that is not on this
// machine. A base nobody could parse is treated as local, because the
// only thing that answers a URL like that is the one we started.
func elsewhere(base string) bool {
	u, err := url.Parse(base)
	if err != nil {
		return false
	}
	switch u.Hostname() {
	case "", "127.0.0.1", "localhost", "::1", "[::1]":
		return false
	}
	return true
}

// railToggle is mote's own ctrl+t, as a message. It is what /rail
// sends and what hides the rail on the way in.
func railToggle() tea.Msg { return tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl} }

func newConversation() string { return "chat-" + time.Now().Format("20060102-150405") }

// chatOptions is everything Vera puts on mote's screen, in one place,
// so that what a test drives is what `vera chat` runs.
func chatOptions(st *Status, s *chatSession, sess *session.Session, greeting string) tui.Options {
	return tui.Options{
		Name: statusName(st, s.c.base),
		// The model in use, beside her name. mote reads Options.Model
		// every frame and tui.SetModel is how it moves, so this is a
		// starting position rather than a fixed fact: the picker moves
		// it, and so does the watch when another window does.
		//
		// What is deliberately not here is the sentence about where it
		// came from. That is `/model`'s to print, and it changes
		// nothing about what is happening.
		Model:        s.w.resolution().Short(),
		Conversation: s.conversation(),
		Session:      sess,
		Greeting:     greeting,
		Side:         s.w.side,
		SideTitle:    "fleet",
		// Inside rook the rail is redundant with rook's own agents
		// pane, so it starts hidden. Closed, not gone: ctrl+t, F2 and
		// /rail all still bring it back.
		SideClosed: insideRook(),
		// Nothing on the right. The status line says what is true of
		// THIS conversation — the model, what it has spent — and
		// where Vera believes the person is is true of the world,
		// which is the rail's business and `/debug`'s. It used to be
		// here, and it was the half of the line that truncated.
		Notices:  s.w.notices,
		Commands: chatCommands,
		Handle:   s.handle,
		// The terminal owns the id — /new hands it one and it may hand
		// back another — so the chat is told rather than keeping a
		// guess of its own. /dump reads what it was told.
		OnConversation: s.setConversation,
	}
}

// chatSessionDir is where the terminal keeps its conversations: XDG
// state, beside verad's own files, one directory of its own because
// they are the chat's and not the daemon's.
func chatSessionDir() string { return filepath.Join(stateDir(), "chat") }

// chatGreeting is what is said once, at the top. A reopened
// conversation says so and names the file, because a person who quit
// and came back needs to know it was kept and where.
func chatGreeting(st *Status, sess *session.Session) string {
	g := "**" + st.Name + "** — say something, or `/` for the fleet. " +
		"`/help` has the keys; the rail on the right is every task and what is believed about it."
	if insideRook() {
		g = "**" + st.Name + "** — say something, or `/` for the fleet. " +
			"`/help` has the keys. The rail starts hidden here: rook already has an agents pane " +
			"showing the same fleet. `/rail`, ctrl+t or F2 brings it back."
	}
	if n := len(sess.Turns()); n > 0 {
		turns := "turns"
		if n == 1 {
			turns = "turn"
		}
		g = fmt.Sprintf("Reopened **%s** — %d %s above, from `%s`.\n\n", sess.ID(), n, turns, sess.Path()) + g
	}
	return g
}

// openSessions is every conversation file this run has opened, so
// that they can all be closed when it ends.
type openSessions struct {
	mu   sync.Mutex
	list []*session.Session
}

func (o *openSessions) add(s *session.Session) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.list = append(o.list, s)
}

func (o *openSessions) closeAll() {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, s := range o.list {
		s.Close()
	}
}

// --- the fleet, watched ---------------------------------------------------

// fleetWatch keeps what a timer can answer where the terminal can read
// it without a request: the rail is drawn on the UI goroutine, and so
// is the right of the status line, so both get a cached answer.
// Anything worth saying out loud goes down notices.
type fleetWatch struct {
	c       *chatClient
	notices chan agent.Event

	// conv says which conversation is current, so the model can be
	// asked about the right one. The terminal owns the id; this reads
	// it rather than keeping a second copy.
	conv func() string
	// send puts a message on the screen from off the UI goroutine. It
	// is the program's own Send, set once the program exists, and it
	// is used for exactly one thing: saying the model moved when
	// something other than this terminal moved it.
	send func(tea.Msg)

	mu     sync.Mutex
	views  []fleet.View
	states map[string]fleet.State // last state per task, to notice changes
	status *Status                // what /status last said, for the status line
	model  *Resolution            // which model this conversation is on
	shown  string                 // the model the status line is showing
}

func newFleetWatch(c *chatClient) *fleetWatch {
	return &fleetWatch{c: c, notices: make(chan agent.Event, 64), states: map[string]fleet.State{}}
}

// run polls until ctx ends. It never closes notices: a command can be
// polling on its own goroutine when the terminal goes away, and a
// channel closed under it would take the process out on the way to
// exiting anyway.
func (w *fleetWatch) run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	w.poll(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.poll(ctx)
			w.pollStatus(ctx)
			w.pollModel(ctx)
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
	for _, ev := range w.absorb(v, time.Now()) {
		select {
		case w.notices <- ev:
		default: // nobody reading, or a burst: the rail still says it
		}
	}
}

// pollStatus re-reads what Vera believes about where the person is.
// It goes on the same timer as the fleet because it feeds the same
// kind of thing: a line that is true all the time rather than news.
func (w *fleetWatch) pollStatus(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	st, err := w.c.status(ctx)
	if err != nil {
		return // the last belief stands; a timeout is not a change of scene
	}
	w.absorbStatus(st)
}

func (w *fleetWatch) absorbStatus(st *Status) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.status = st
}

// pollModel re-reads which model this conversation is on. verad is the
// authority: `/model` in another window, or a profile edited and a
// restart, changes the answer and the status line has to follow. A
// failed read leaves the last answer standing.
func (w *fleetWatch) pollModel(ctx context.Context) {
	if w.conv == nil {
		return
	}
	id := w.conv()
	if id == "" {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	res, err := w.c.model(ctx, id)
	if err != nil {
		return
	}
	w.mu.Lock()
	w.model = res
	line, send := res.Short(), w.send
	moved := line != "" && line != w.shown
	if moved {
		w.shown = line
	}
	w.mu.Unlock()
	// The status line is mote's to draw and this is not the UI
	// goroutine, so the change goes back as a message. Only when it
	// actually changed: the poll runs every few seconds and a message
	// per poll would redraw the screen for nothing.
	if moved && send != nil {
		if msg := tui.SetModel(line)(); msg != nil {
			send(msg)
		}
	}
}

// sendWith hands the watch the program's own Send. It is set after the
// program exists and read on the poll goroutine, so it goes under the
// same lock as everything else here.
func (w *fleetWatch) sendWith(fn func(tea.Msg)) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.send = fn
}

func (w *fleetWatch) resolution() *Resolution {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.model
}

// absorb replaces the cache and returns what is worth saying about it.
// Each notice is about a task rather than about a moment: a task that
// changes twice rewrites its line instead of adding one, and a landing
// rewrites it one last time.
func (w *fleetWatch) absorb(views []fleet.View, now time.Time) []agent.Event {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.views = views
	var out []agent.Event
	for _, v := range views {
		if v.Task == nil {
			continue
		}
		prev, seen := w.states[v.ID]
		w.states[v.ID] = v.State
		if line, ok := noticeFor(prev, seen, v, now); ok {
			out = append(out, agent.About(v.ID, line))
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
//
// Every line here is a claim about what happened, so each one is
// checked against what actually did. A scout that finished did not
// become "ready to land": it has nothing to land and never did, and
// what the person wants is its report. A ship task that finished is
// only "ready to land" when landing is theirs to do — with the
// supervisor landing on its own, the next thing they will hear is
// "landed", and telling them to land it in the meantime is telling
// them to do a job somebody else already started.
//
// What each line LOOKS like is grammar.go's: a glyph, what the task
// is, what it last said, and the one command that moves it on. This
// decides which of them is worth saying at all.
func noticeFor(prev fleet.State, seen bool, v fleet.View, now time.Time) (string, bool) {
	if !seen || prev == v.State {
		return "", false
	}
	// A scout's whole deliverable is its report, so that is the
	// notice: what it found, and how to read the rest.
	if v.Kind == fleet.Scout && (v.State == fleet.Finished || v.State == fleet.Closed) {
		if v.State == fleet.Closed && prev == fleet.Finished {
			return "", false // already said, when it finished
		}
		head := glyphDone + " Scout reported · " + trim(firstSentence(v.Brief), 60)
		if unreadReport(v) {
			head += " " + glyphUnread
		}
		return event(head, trim(firstLine(v.Report, v.Last), 90), "/report "+v.ID), true
	}
	// Closed: it landed, or it was torn down. Which of the two is the
	// supervisor's own last word, so say that rather than a tick.
	if v.State == fleet.Closed {
		word := "closed"
		if v.Last != nil && v.Last.Verb == fleet.Done {
			word = "landed"
		}
		head := glyphDone + " " + kindWord(v) + " " + word + " · " + trim(firstSentence(v.Brief), 60)
		detail := ""
		if v.Last != nil && v.Last.Text != "" {
			detail = trim(oneLine(v.Last.Text), 120)
		}
		// A ship task writes a summary of what it changed and why, and
		// with the supervisor landing on its own this line is the last
		// the person hears of it. Without the pointer the summary is
		// written for nobody.
		next := ""
		if v.Report != "" {
			next = "/report " + v.ID
		}
		return event(head, detail, next), true
	}
	if !v.State.Actionable() {
		return "", false
	}
	return taskNotice(v, waitedFor(v, now)), true
}

// waitedFor is how long a task has been waiting on the person, in the
// words a sentence would use. Empty when it has not ended a turn —
// "waiting on you for 0s" is worse than "waiting on you".
func waitedFor(v fleet.View, now time.Time) string {
	if v.State != fleet.Waiting || v.TurnEnded.IsZero() {
		return ""
	}
	return roughDuration(now.Sub(v.TurnEnded))
}

// firstLine is the first line of a report — the sentence a scout put
// at the top — falling back to what it last said.
func firstLine(report string, last *fleet.Status) string {
	for _, line := range strings.Split(report, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(line, "# "))
		if line != "" {
			return line
		}
	}
	if last != nil && last.Text != "" {
		return oneLine(last.Text)
	}
	return "it has written nothing"
}

// sideState maps what Vera believes onto the five things a person
// does about it: leave it, look at it, answer it, land it, fix it.
// A state with no row — closed, or one this binary is too old to
// know — is left off the rail rather than guessed at.
func sideState(v fleet.View) (tui.State, bool) {
	switch v.State {
	case fleet.Running, fleet.Quiet:
		return tui.Working, true
	case fleet.Waiting, fleet.Stale, fleet.Held, fleet.Interrupted:
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

// waitingReport: a scout that has written and not been read.
//
// This is what SideItem.Needs is for, and why it is not a state. Such
// a scout IS done — it did everything it was asked — and it is also
// the one row on the rail that wants the person. A tick alone would
// say "nothing to do here", which is the opposite of true.
func waitingReport(v fleet.View) bool {
	return v.Kind == fleet.Scout && v.Report != "" && len(v.Unread) > 0
}

func sideItems(views []fleet.View) []tui.SideItem {
	var out []tui.SideItem
	for _, v := range views {
		if v.Task == nil || v.Closed {
			continue
		}
		st, ok := sideState(v)
		if !ok {
			continue
		}
		out = append(out, tui.SideItem{
			ID:       v.ID,
			Title:    trim(firstSentence(v.Brief), 60),
			Subtitle: sideSubtitle(v),
			State:    st,
			Needs:    waitingReport(v),
		})
	}
	return out
}

// sideSubtitle is the one line under a task: what it last said, or
// failing that where it is working.
func sideSubtitle(v fleet.View) string {
	if waitingReport(v) {
		return "report waiting"
	}
	sub := shortPath(v.Project)
	if v.Last != nil && v.Last.Text != "" {
		sub = trim(oneLine(v.Last.Text), 80)
	}
	if n := len(v.Unread); n > 0 {
		sub = fmt.Sprintf("+%d %s", n, sub)
	}
	return sub
}

// --- reading a report -----------------------------------------------------

// pickReport finds the task a /report meant. An id may be a prefix —
// eight hex characters is more than anyone wants to retype, and the
// command line has always taken a prefix, so the chat does too.
//
// With no id at all it is the report that is waiting: the person just
// read "a1b2c3d4 reported" on the screen and reached for the obvious
// next thing. When more than one is waiting, nothing is guessed —
// picking one at random is worse than naming them.
func pickReport(views []fleet.View, id string) (fleet.View, error) {
	if id == "" {
		var waiting []fleet.View
		for _, v := range views {
			// Not waitingReport: that one is the rail's question, and
			// it is only ever asked of a scout. A ship task's report
			// is the summary of what it changed, and an unread one is
			// just as much a thing the person came here to read.
			if v.Task != nil && !v.Closed && v.Report != "" && len(v.Unread) > 0 {
				waiting = append(waiting, v)
			}
		}
		switch len(waiting) {
		case 0:
			return fleet.View{}, errors.New("no report is waiting — name a task to re-read one")
		case 1:
			return waiting[0], nil
		}
		return fleet.View{}, fmt.Errorf("reports waiting from %s — name one", idsOf(waiting))
	}
	var hits []fleet.View
	for _, v := range views {
		if v.Task == nil {
			continue
		}
		if v.ID == id {
			return v, nil
		}
		if strings.HasPrefix(v.ID, id) {
			hits = append(hits, v)
		}
	}
	switch len(hits) {
	case 0:
		return fleet.View{}, fmt.Errorf("no task %s", id)
	case 1:
		return hits[0], nil
	}
	return fleet.View{}, fmt.Errorf("%s matches %s — say more of it", id, idsOf(hits))
}

func idsOf(views []fleet.View) string {
	ids := make([]string, 0, len(views))
	for _, v := range views {
		ids = append(ids, v.ID)
	}
	return strings.Join(ids, ", ")
}

// reportMarkdown is the report as a person reviews it: a line saying
// whose it is and where it worked, the report itself, and the one
// thing to do about it now. A report on its own is a wall of markdown
// with no name on it — after two of them nobody remembers which task
// wrote which, and the next verb is a guess.
func reportMarkdown(v fleet.View) string {
	where := shortPath(v.Project)
	if v.Branch != "" {
		where += " · " + v.Branch
	}
	var b strings.Builder
	// Each line is its own paragraph: a markdown renderer folds two
	// lines into one, and the header would come out wearing the brief.
	fmt.Fprintf(&b, "**%s** · %s · %s · %s\n", v.ID, v.Kind, v.State, where)
	if brief := firstSentence(v.Brief); brief != "" {
		fmt.Fprintf(&b, "\n*%s*\n", trim(oneLine(brief), 100))
	}
	b.WriteString("\n" + strings.TrimSpace(v.Report) + "\n")
	if next := reportNext(v); next != "" {
		b.WriteString("\n---\n" + next + "\n")
	}
	return b.String()
}

// reportNext is the verb that fits what the task is actually in the
// middle of. Every line is a claim about what happens next, so a task
// the supervisor is already landing says so rather than asking the
// person to land it again, and a task still working is not offered a
// landing it has not earned.
func reportNext(v fleet.View) string {
	switch v.State {
	case fleet.Decision:
		return "It is blocked on you — `/answer " + v.ID + " <text>`."
	case fleet.Waiting:
		return "It ended its turn and nobody has answered — `/answer " + v.ID + " <text>`."
	case fleet.Held:
		return "It is paused on something outside — `/answer " + v.ID + " <text>` when it can go on."
	case fleet.Broken:
		return "It failed — `/resume " + v.ID + "` to pick it up, `/stop " + v.ID + "` to tear the room down."
	case fleet.Gone:
		return "Its room is gone — `/resume " + v.ID + "` reopens it."
	case fleet.Closed:
		return ""
	case fleet.Finished:
		if v.Kind == fleet.Scout {
			if v.AutoLand {
				return "Nothing to land. Vera closes it now that you have read it."
			}
			return "Nothing to land — `/land " + v.ID + "` closes it."
		}
		if v.AutoLand {
			return "Vera is landing it; nothing to do."
		}
		branch := v.Branch
		if branch == "" {
			branch = "its branch"
		}
		return "`/land " + v.ID + "` merges " + branch + ", `/stop " + v.ID + "` throws it away."
	}
	return "It is still going — `/answer " + v.ID + " <text>` to say something to it."
}

// plainText drops the markdown a screen renders and a pipe does not.
func plainText(s string) string { return strings.ReplaceAll(s, "`", "") }

// --- the commands ---------------------------------------------------------

// chatCommands are the fleet verbs by hand — the same calls the
// mind's fleet tool makes, for when you know exactly what you want
// and do not need to say it in prose. /help is mote's.
//
// Each Help is one column of the completion popup, beside a name the
// popup has already printed — so it says what the command DOES first
// and how to type it after the ·, and never opens by repeating its
// own name. A line that has to be truncated to fit is a line whose
// first half was the only half anybody read.
var chatCommands = []tui.Command{
	{Name: "model", Help: "which model answers · /model <name> moves straight there"},
	{Name: "effort", Help: "how hard it thinks · /effort low|medium|high"},
	{Name: "costs", Help: "what the exchanges have cost · /costs 7d by model"},
	{Name: "events", Help: "what has been going on · /events 7d @repo words"},
	{Name: "rail", Help: "show or hide the fleet rail · ctrl+t, F2"},
	{Name: "tasks", Help: "every task and what is believed about it"},
	{Name: "todo", Help: "your own list · /todo <something>, /todo done <n>"},
	{Name: "start", Help: "put a task on the rail · /start [@repo] <brief>"},
	{Name: "scout", Help: "a task that reports instead of landing · /scout [@repo] <brief>"},
	{Name: "resume", Help: "pick a task back up · /resume <id>"},
	{Name: "report", Help: "what a task wrote, and what to do about it · /report [id]"},
	{Name: "answer", Help: "reply to a task that asked · /answer <id> <text>"},
	{Name: "land", Help: "merge its branch and close it · /land <id>"},
	{Name: "stop", Help: "tear a task down · /stop <id> [force]"},
	{Name: "seen", Help: "you have read it · /seen <id>"},
	{Name: "paste", Help: "attach the picture on the pasteboard · ctrl+v does it too"},
	{Name: "image", Help: "attach a picture from a file · /image <path>"},
	{Name: "new", Help: "a fresh conversation, in its own file"},
	{Name: "sessions", Help: "the conversations on disk"},
	{Name: "dump", Help: "this conversation, in a folder, to report a problem · /dump [note]"},
	{Name: "debug", Help: "what Vera believes about where you are"},
	{Name: "quit", Help: "leave"},
}

// chatSession is the little the commands need to share: the wire, the
// fleet cache, where the conversations are kept, and which one is
// current. The id is not the chat's own idea — the terminal hands it
// over through Options.OnConversation — but /dump needs to know it
// without a screen to ask.
type chatSession struct {
	c    *chatClient
	w    *fleetWatch
	dir  string        // where the conversation files live
	open *openSessions // the ones this run opened, to close at the end
	// held is what /paste and /image have attached and not yet sent.
	// The agent takes it on the next message; see stage.go. Its zero
	// value is an empty stage, so a chat built without one works.
	held stage
	// board is the pasteboard /paste and ctrl+v read. Its zero value
	// is a machine with none — which is every machine that is not a
	// Mac, and every test that does not hand one over.
	board pasteboard

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

	case "rail":
		// mote's own toggle, sent the way a key press arrives. The
		// command exists so that /help says the rail is there at all —
		// inside rook it starts hidden and a person who never presses
		// F2 would never learn it.
		return func() tea.Msg { return railToggle() }

	case "model":
		// verad is the authority throughout: this asks, and it draws
		// or prints what it is told. With no argument it puts the card
		// up — see modelpick.go. With one it is a change, typed, and
		// the change sticks to this conversation.
		c, conv := s.c, s.conversation()
		w := s.w
		if rest == "" {
			return off(func(ctx context.Context) tea.Cmd {
				ans, err := c.models(ctx, conv)
				if err != nil {
					// A verad too old to list them can still say what
					// this conversation is on, and that answer is
					// worth more than an error about a missing route.
					res, err2 := c.model(ctx, conv)
					if err2 != nil {
						return tui.Fail("model: %s", err)
					}
					return tui.Note("%s — %s", res.Line(), res.Says())
				}
				if len(ans.Models) == 0 {
					return tui.Fail("no models: verad has no key for any provider")
				}
				return tui.Choose(s.modelPick(ans))
			})
		}
		want, effort, _ := strings.Cut(rest, " ")
		effort = strings.TrimSpace(effort)
		return off(func(ctx context.Context) tea.Cmd {
			res, err := c.chooseModel(ctx, conv, want, effort)
			if err != nil {
				// What failed, what it left alone, and where to look.
				// A refusal that only says no leaves the person
				// wondering what it moved on the way past.
				return tui.Fail("%s", failure(err.Error(),
					"staying on "+staying(w), "/model on its own lists what verad can reach"))
			}
			w.pollModel(ctx)
			return tea.Batch(tui.SetModel(res.Short()),
				tui.Note("%s — %s", res.Line(), res.Says()), tui.Refresh())
		})

	case "effort":
		// The other half of the same question, and a separate toggle
		// because it is a separate question: which model answers is a
		// list that changes with the keys on the machine, how hard it
		// thinks is the same three words on whichever model you are on.
		// verad merges the halves, so this sends no model at all.
		c, conv := s.c, s.conversation()
		w := s.w
		if rest == "" {
			return off(func(ctx context.Context) tea.Cmd {
				ans, err := c.models(ctx, conv)
				if err != nil {
					return tui.Fail("%s", failure("could not ask verad which efforts this model takes: "+err.Error(),
						"nothing changed", "/effort <level> still sends one straight through"))
				}
				dial := effortsFor(ans)
				if len(dial) == 0 {
					// Not an empty card: a model with no dial is a
					// fact about that model, and the honest shape for
					// it is the one every refusal here wears — what
					// cannot be done, what that leaves alone, and the
					// command that can. The capability is ABSENT; it
					// is not set to "none".
					return tui.Fail("%s", noDial(ans.using().Model))
				}
				return tui.Choose(s.effortPick(ans, dial))
			})
		}
		return off(func(ctx context.Context) tea.Cmd {
			res, err := c.chooseEffort(ctx, conv, rest)
			if err != nil {
				return tui.Fail("%s", failure(err.Error(),
					"model and settings unchanged", "/effort on its own shows what this model takes"))
			}
			w.pollModel(ctx)
			return tea.Batch(tui.SetModel(res.Short()),
				tui.Note("%s — %s", res.Line(), res.Says()), tui.Refresh())
		})

	case "costs":
		// The same numbers `vera costs` prints, on the screen that is
		// already open. Arguments are the same words in either order:
		// `/costs 24h by conversation`.
		spec := rest
		return off(func(context.Context) tea.Cmd {
			o, err := costOptionsFrom(spec)
			if err != nil {
				return tui.Fail("costs: %s", err)
			}
			rep, err := costs.Build(o)
			if err != nil {
				return tui.Fail("costs: %s", err)
			}
			return tui.Show(rep.Markdown())
		})

	case "events", "ev":
		// The same stream `vera events` prints, on the screen that is
		// already open. `/events 7d @rook blocked` is the whole
		// grammar: a window, a repository, and words to look for.
		spec := rest
		return off(func(context.Context) tea.Cmd {
			q, err := eventQueryFrom(spec)
			if err != nil {
				return tui.Fail("events: %s", err)
			}
			evs, err := events.Read(eventsDir(), q)
			if err != nil {
				return tui.Fail("events: %s", err)
			}
			return tui.Show(events.Summarize(evs, q.Since, time.Now()).Markdown())
		})

	case "todo", "td":
		// Yours, not the fleet's. Everything about what the line means
		// is decided in verad; this only draws the answer.
		return s.todoCommand(rest)

	case "tasks", "t":
		w := s.w
		return off(func(ctx context.Context) tea.Cmd {
			w.poll(ctx)
			return tea.Batch(tui.Note("%s", w.lines()), tui.Refresh())
		})

	case "paste":
		// The pasteboard is read now, not at send time: a person who
		// pastes and then copies something else meant the first one.
		// ctrl+v is this same fetch on a keystroke — see pastekey.go.
		board, held := s.board, &s.held
		return off(func(context.Context) tea.Cmd {
			im, err := board.picture()
			if err != nil {
				return tui.Fail("%s", err)
			}
			return tui.Note("%s", held.add(im))
		})

	case "image", "img":
		held := &s.held
		if rest == "" {
			if n := held.clear(); n > 0 {
				return tui.Note("forgot %d attached %s", n, plural(n, "image"))
			}
			return tui.Fail("/image <path> — a PNG, JPEG, GIF or WebP to send with your next message")
		}
		path := expandHome(rest)
		return off(func(context.Context) tea.Cmd {
			im, err := attach.Read(path)
			if err != nil {
				return tui.Fail("%s", err)
			}
			return tui.Note("%s", held.add(im))
		})

	case "new":
		// A new id needs a new file, or the next conversation is
		// written into the last one's. The chat's own copy of the id
		// is not set here: the terminal says so when the change lands.
		conv := newConversation()
		next, err := session.Open(s.dir, conv)
		if err != nil {
			return tui.Fail("session: %s", err)
		}
		s.open.add(next)
		// A picture attached to the conversation you just left does
		// not follow you into the next one.
		s.held.clear()
		return tea.Batch(tui.SetConversation(conv), tui.SetSession(next),
			tui.Note("new conversation %s — the last one is still in %s", conv, s.dir))

	case "sessions":
		list, err := session.List(s.dir)
		if err != nil {
			return tui.Fail("sessions: %s", err)
		}
		if len(list) == 0 {
			return tui.Note("no conversations in %s", s.dir)
		}
		return tui.Note("%s", sessionLines(list, s.conversation(), time.Now()))

	case "dump":
		// This conversation and every task it touched, in a folder;
		// the rest of the line is the note at the top of its README.
		conv, note := s.conversation(), rest
		return off(func(context.Context) tea.Cmd {
			res, err := dump.Build(dump.Options{Conversations: []string{conv}, Note: note, Version: version, Tar: true, HomeDir: home.Path(veraHomeSetting())})
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
		// Read the fleet first. A report is asked for seconds after the
		// notice that it exists, and the cache behind the rail can be a
		// poll behind — "it has written no report yet" about a report
		// sitting on disk is the one answer this must never give.
		w := s.w
		return off(func(ctx context.Context) tea.Cmd {
			w.poll(ctx)
			v, err := pickReport(w.snapshot(), id)
			if err != nil {
				return tui.Fail("%s", err)
			}
			if v.Report == "" {
				return tui.Note("%s has written no report yet", v.ID)
			}
			// Read is seen: a scout whose report was read is done.
			return tea.Batch(tui.Show(reportMarkdown(v)), s.post("/fleet/"+v.ID+"/seen", nil, ""))
		})
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
	// Where she believes the person is. It was on the status line and
	// is not any more — it is a fact about the world rather than about
	// this conversation, and it is the first thing this block is for.
	if where := whereYouAre(s); where != "" {
		fmt.Fprintf(&b, "\nWhere you are: %s\n", where)
	}
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

// whereYouAre is what Vera believes about where the person is, in a
// phrase: the app they are in front of and, if that app is a terminal
// she can see into, what is in it. With nothing focused — no device
// has reported, or the report has gone stale — the honest thing left
// to say is what she is doing herself.
//
// It used to be the right of the status line. The reference took it
// off: that line says what is true of the conversation in front of
// you, and this is true of the world — so it opens `/debug`'s block
// now, where it has room and nothing truncates.
func whereYouAre(s *Status) string {
	if s == nil {
		return ""
	}
	for _, d := range s.Devices {
		if !d.Fresh || d.Focus == nil {
			continue
		}
		where := d.Focus.Name
		if d.Terminal != nil {
			where += " · " + d.Terminal.Describe()
		}
		return trim(oneLine(where), 72)
	}
	switch n := s.RunsInFlight; {
	case n == 1:
		return "1 run in flight"
	case n > 1:
		return fmt.Sprintf("%d runs in flight", n)
	}
	return ""
}

// oneLine flattens a multi-line status into something a rail row or a
// single transcript line can hold.
func oneLine(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " ")), " ")
}
