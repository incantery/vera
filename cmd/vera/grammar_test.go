package main

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/incantery/mote/tui"
	"github.com/incantery/vera/fleet"
)

// The vocabulary itself: every state a rail row or a transcript line
// can be in gets its own SHAPE, so the screen reads with the colour
// off — and × is failure only, so a task that is asking for a word is
// not painted like one that crashed.
func TestEveryStateHasItsOwnShape(t *testing.T) {
	seen := map[string]fleet.State{}
	for _, st := range []fleet.State{
		fleet.Running, fleet.Waiting, fleet.Stale, fleet.Decision,
		fleet.Finished, fleet.Broken, fleet.Gone, fleet.Held, fleet.Interrupted, fleet.Quiet,
	} {
		v := fleet.View{Task: &fleet.Task{ID: "a1", Brief: "Port the chat"}, State: st}
		glyph, word := mark(v)
		if glyph == "" || word == "" {
			t.Fatalf("%s says nothing: %q %q", st, glyph, word)
		}
		if (glyph == glyphFailed) != (st == fleet.Broken || st == fleet.Gone) {
			t.Errorf("%s draws %q — the failure shape is for failures", st, glyph)
		}
		if prev, ok := seen[word]; ok && prev != st {
			// Two states MAY share a word where the person's move is
			// the same one; they must not share it by accident.
			if !oneMove(prev, st) {
				t.Errorf("%s and %s both say %q", prev, st, word)
			}
		}
		seen[word] = st
	}
	// The five shapes are five characters, not five colours.
	shapes := map[string]bool{glyphWorking: true, glyphNeedsYou: true, glyphQuiet: true, glyphDone: true, glyphFailed: true}
	if len(shapes) != 5 {
		t.Fatalf("the vocabulary collapsed: %v", shapes)
	}
	if shapes[glyphUnread] {
		t.Error("● is unread and only unread — it is not one of the five states")
	}
}

// oneMove: two states the person does the same thing about, and which
// are allowed to say the same word because of it. Waiting, blocked and
// held are all "say something to it"; running and quiet are both a
// task getting on with it, and how long it has been since it last drew
// anything is the rail's business, not a second word here.
func oneMove(a, b fleet.State) bool {
	class := func(s fleet.State) int {
		switch s {
		case fleet.Waiting, fleet.Decision, fleet.Held:
			return 1
		case fleet.Running, fleet.Quiet:
			return 2
		}
		return 0
	}
	return class(a) != 0 && class(a) == class(b)
}

// A lifecycle line is two lines: what it is and what it is called,
// then what it last said and the one command that moves it on. The id
// appears only inside that command — it is an argument, not a name.
func TestATaskNoticeSaysWhatToDoAboutIt(t *testing.T) {
	task := func(st fleet.State) fleet.View {
		return fleet.View{Task: &fleet.Task{ID: "a3f2", Kind: fleet.Ship, Brief: "Investigate Vera's /effort"}, State: st}
	}
	for _, c := range []struct {
		name       string
		v          fleet.View
		head, next string
	}{
		{"working", task(fleet.Running), "◐ Task working · Investigate Vera's /effort", ""},
		{"blocked", task(fleet.Decision), "◇ Task needs you · Investigate Vera's /effort", "/answer a3f2 <text>"},
		{"failed", task(fleet.Broken), "× Task failed · Investigate Vera's /effort", "/resume a3f2"},
		{"gone", task(fleet.Gone), "× Task is gone · Investigate Vera's /effort", "/resume a3f2"},
		{"finished", task(fleet.Finished), "✓ Task finished · Investigate Vera's /effort", "/land a3f2"},
	} {
		got := taskNotice(c.v, "")
		head, _, _ := strings.Cut(got, "\n")
		if head != c.head {
			t.Errorf("%s head:\n got %q\nwant %q", c.name, head, c.head)
		}
		if c.next != "" && !strings.HasSuffix(got, c.next) {
			t.Errorf("%s should end on the verb %q:\n%s", c.name, c.next, got)
		}
		if strings.Contains(head, "a3f2") {
			t.Errorf("%s: the id is an argument, not a name: %q", c.name, head)
		}
	}

	// A scout that has written and not been read is done AND unread —
	// two channels, one row.
	sc := fleet.View{Task: &fleet.Task{ID: "a3f2", Kind: fleet.Scout, Brief: "Look at it"},
		State: fleet.Finished, Report: "# Findings", Unread: []fleet.Status{{Text: "written"}}}
	got := taskNotice(sc, "")
	if !strings.HasPrefix(got, "✓ Scout reported · Look at it ●") {
		t.Errorf("done and unread:\n%s", got)
	}
	sc.Unread = nil
	if strings.Contains(taskNotice(sc, ""), glyphUnread) {
		t.Errorf("a report that was read is not unread: %s", taskNotice(sc, ""))
	}

	// A task that said something says it; one that said nothing still
	// says what state it is in rather than showing a bare command.
	said := task(fleet.Finished)
	said.Last = &fleet.Status{Text: "branch pushed"}
	if got := taskNotice(said, ""); !strings.Contains(got, "branch pushed") || strings.Contains(got, "ready to land") {
		t.Errorf("its own words, and no verb said twice:\n%s", got)
	}
	if got := taskNotice(task(fleet.Finished), ""); !strings.Contains(got, "ready to land") {
		t.Errorf("a task that said nothing still says where it got to:\n%s", got)
	}

	// One line when there is nothing to add to the head.
	if got := taskNotice(task(fleet.Running), ""); strings.Contains(got, "\n") {
		t.Errorf("nothing to add is nothing added:\n%s", got)
	}
}

// How long a task has been waiting is on the second line, and only
// when it is knowable: "waiting on you for 0s" is worse than nothing.
func TestWaitedFor(t *testing.T) {
	now := time.Now()
	v := fleet.View{Task: &fleet.Task{ID: "a1", Brief: "x"}, State: fleet.Waiting}
	if got := waitedFor(v, now); got != "" {
		t.Errorf("no turn has ended: %q", got)
	}
	v.TurnEnded = now.Add(-9 * time.Minute)
	if got := waitedFor(v, now); got == "" {
		t.Error("a turn that ended has a duration")
	} else if !strings.Contains(taskNotice(v, got), "waiting "+got) {
		t.Errorf("the duration should reach the line: %s", taskNotice(v, got))
	}
	v.State = fleet.Running
	if got := waitedFor(v, now); got != "" {
		t.Errorf("a task that is working is not waiting on anybody: %q", got)
	}
}

// An error is three parts: what failed, what that left alone, and the
// next thing to try. The middle one is the point — after a command
// that would not run, "no" is not enough; the person has to know the
// machine is where they left it.
func TestAFailureSaysWhatItLeftAlone(t *testing.T) {
	got := failure("Luna does not expose a reasoning-effort control",
		"model and settings unchanged", "/model shows what does")
	want := "Luna does not expose a reasoning-effort control\n→ model and settings unchanged · /model shows what does"
	if got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
	if got := failure("it broke", "", ""); got != "it broke" {
		t.Errorf("nothing to add is nothing added: %q", got)
	}
	if got := failure("it broke", "", "/resume a1"); got != "it broke\n→ /resume a1" {
		t.Errorf("a verb with no consequence: %q", got)
	}
	if !strings.Contains(noDial("gpt-5.6-terra"), "does not expose a reasoning-effort control") {
		t.Errorf("noDial: %q", noDial("gpt-5.6-terra"))
	}
}

// A tool card reads as the call would be said out loud, for the tools
// this terminal knows the shape of — and mote is left to summarize
// the arguments of the ones it does not.
func TestToolReceiptsReadAsSentences(t *testing.T) {
	for _, c := range []struct{ name, args, want string }{
		{"fleet", `{"action":"start","kind":"scout","project":"vera","brief":"Investigate Vera's /effort handling"}`,
			`start scout vera "Investigate Vera's /effort handling"`},
		{"fleet", `{"action":"list"}`, "list"},
		{"fleet", `{"action":"land","task":"a3f2"}`, "land a3f2"},
		{"fleet", `{"action":"answer","task":"a3f2","text":"yes, go on"}`, `answer a3f2 "yes, go on"`},
		{"delegate", `{"task":"what is in README"}`, "what is in README"},
		{"read", `{"path":"/tmp/x"}`, ""},      // mote's job
		{"fleet", `{"nonsense`, ""},            // unreadable: mote's job
		{"fleet", `{"brief":"no action"}`, ""}, // not a call this knows
	} {
		if got := toolSays(c.name, c.args); got != c.want {
			t.Errorf("%s %s:\n got %q\nwant %q", c.name, c.args, got, c.want)
		}
	}
}

// The status line names no machine while there is only one — and says
// which the moment there is more than one.
func TestTheStatusLineNamesAMachineOnlyWhenItMatters(t *testing.T) {
	st := &Status{Name: "Seths-MacBook-Pro-2"}
	for _, base := range []string{"http://127.0.0.1:4780", "http://localhost:4780", "", "::::"} {
		if got := statusName(st, base); got != "vera" {
			t.Errorf("%q is this machine, so the hostname is a constant: %q", base, got)
		}
	}
	if got := statusName(st, "http://studio.local:4780"); got != "Seths-MacBook-Pro-2" {
		t.Errorf("another machine's verad: %q", got)
	}
	if got := statusName(nil, "http://studio.local:4780"); got != "vera" {
		t.Errorf("nothing said yet: %q", got)
	}
}

// The dial goes on the status line when it is turned to something.
// "none" is the absence of a setting, not a setting — half the table
// has no dial at all — and the reference's rule for that line is that
// an absent capability gets no column. /model still prints it whole.
func TestTheStatusLineDropsADialNobodyTurned(t *testing.T) {
	none := &Resolution{Model: "gpt-5.6-luna", Effort: "none", ModelFrom: "the profile"}
	if got := none.Short(); got != "gpt-5.6-luna" {
		t.Errorf("short: %q", got)
	}
	if got := none.Line(); got != "gpt-5.6-luna · none" {
		t.Errorf("/model still prints it whole: %q", got)
	}
	turned := &Resolution{Model: "claude-opus-5", Effort: "high"}
	if got := turned.Short(); got != "claude-opus-5 · high" {
		t.Errorf("a dial somebody turned belongs on the line: %q", got)
	}
	var nothing *Resolution
	if got := nothing.Short(); got != "" {
		t.Errorf("nothing answered yet: %q", got)
	}
}

// The completion popup is a column: the name mote already printed,
// then what the command does. A help that opens by repeating its own
// name spends the readable half of the line on it.
func TestTheCompletionColumnSaysWhatEachCommandDoes(t *testing.T) {
	for _, c := range chatCommands {
		if strings.HasPrefix(c.Help, "/") {
			t.Errorf("/%s opens by repeating itself: %q", c.Name, c.Help)
		}
		if head, _, _ := strings.Cut(c.Help, " · "); len(head) > 56 {
			t.Errorf("/%s says too much before the · : %q", c.Name, head)
		}
	}

	outsideRook(t)
	f := newFakeVerad(t)
	c := f.client()
	w := newFleetWatch(c)
	s := &chatSession{c: c, w: w, conv: "chat-1", dir: t.TempDir(), open: &openSessions{}}
	w.conv = s.conversation
	m := tui.New(veraAgent{c: c}, headless(chatOptions(&Status{Name: "vera"}, s, nil, "")))
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 32})
	m.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	m.Update(tea.KeyPressMsg{Code: 'm', Text: "m"})

	v := screen(m)
	if !strings.Contains(v, "/model") || !strings.Contains(v, "which model answers") {
		t.Errorf("the popup should say what /model does:\n%s", v)
	}
}

// And on the screen: mote draws a notice down a gutter with the
// continuation indented, so the head and the second line read as one
// block. This is the reference's task-event card, in a terminal.
func TestALifecycleEventDrawsAsOneBlock(t *testing.T) {
	outsideRook(t)
	f := newFakeVerad(t)
	c := f.client()
	w := newFleetWatch(c)
	s := &chatSession{c: c, w: w, conv: "chat-1", dir: t.TempDir(), open: &openSessions{}}
	w.conv = s.conversation
	m := tui.New(veraAgent{c: c}, headless(chatOptions(&Status{Name: "vera"}, s, nil, "")))
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 32})

	v := fleet.View{Task: &fleet.Task{ID: "a3f2", Kind: fleet.Scout, Brief: "Investigate Vera's /effort"},
		State: fleet.Decision, Last: &fleet.Status{Text: "one rail or two?"}}
	deliver(m, tui.Note("%s", taskNotice(v, "")))

	var head, body string
	for _, line := range strings.Split(screen(m), "\n") {
		if strings.Contains(line, "◇ Scout needs you") {
			head = strings.TrimRight(line, " ")
		}
		if strings.Contains(line, "/answer a3f2") {
			body = strings.TrimRight(line, " ")
		}
	}
	if head == "" || body == "" {
		t.Fatalf("the event did not draw as two lines:\n%s", screen(m))
	}
	// The gutter is on the head; the second line is indented under it
	// rather than starting a second event.
	if !strings.HasPrefix(head, "  · ") {
		t.Errorf("head: %q", head)
	}
	if !strings.HasPrefix(body, "    ") || strings.Contains(body, "·  ") {
		t.Errorf("body: %q", body)
	}
	if !strings.Contains(body, "one rail or two?") {
		t.Errorf("its own last words belong on the line: %q", body)
	}
}

// The greeting is the top of the transcript, and it opened on the
// machine's name in bold — the same constant the status line stopped
// spending a column on, and read as though the laptop were greeting
// you. Same rule, same exception.
func TestTheGreetingNamesAMachineOnlyWhenItMatters(t *testing.T) {
	st := &Status{Name: "Seths-MacBook-Pro-2"}
	local := chatGreeting(st, "http://127.0.0.1:4780", emptySession(t))
	if strings.Contains(local, "Seths-MacBook-Pro-2") {
		t.Errorf("this machine does not introduce itself:\n%s", local)
	}
	if !strings.HasPrefix(local, "Say something") {
		t.Errorf("greeting:\n%s", local)
	}
	away := chatGreeting(st, "http://studio.local:4780", emptySession(t))
	if !strings.HasPrefix(away, "**Seths-MacBook-Pro-2** — ") {
		t.Errorf("another machine's verad should say so:\n%s", away)
	}
}
