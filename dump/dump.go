// Package dump collects everything about a conversation with Vera
// into a folder somebody else can read.
//
// The question it exists for is "Vera did something odd — here is
// what happened". Answering that needs more than the transcript: the
// prompt the model saw, the tools it reached for and what came back,
// the fleet tasks it opened and what those agents did (their Claude
// Code sessions, verbatim), what all of it cost, and verad's log for
// the same minutes. Each of those lives somewhere on disk already;
// this walks them and copies, redacting secrets on the way.
//
// It reads disk only — no verad needed, so it works on the day verad
// is the thing that broke.
package dump

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/incantery/vera/fleet"
	"github.com/incantery/vera/journal"
)

// Options says what to collect. With nothing named, the most recent
// conversation.
type Options struct {
	StateDir  string // ~/.local/state/vera
	ClaudeDir string // ~/.claude
	ConfigDir string // ~/.config/vera
	// Out is the folder to write; "" puts it under StateDir/dumps.
	Out string
	// Conversations to include, by id (a prefix will do). Tasks to
	// include beyond those the conversations touched. Since includes
	// every conversation and task active after that moment. All takes
	// everything.
	Conversations []string
	Tasks         []string
	Since         time.Time
	All           bool
	// Note is what the person says went wrong; it heads the README.
	Note    string
	Version string
	Now     time.Time
	// Tar also writes <Out>.tar.gz beside the folder.
	Tar bool
}

// Result is what was written.
type Result struct {
	Dir           string
	Tarball       string
	Conversations []string
	Tasks         []string
	Sessions      int
	// CostUSD is the notional total across every Claude Code session
	// found; Priced is false if any model had no price.
	CostUSD float64
	Priced  bool
	Files   int
}

type collected struct {
	conversations []conversation
	tasks         []taskBundle
	delegated     []*Session
	from, to      time.Time
}

type conversation struct {
	id      string
	entries []journal.Entry
}

type taskBundle struct {
	task     *fleet.Task
	dir      string
	statuses []fleet.Status
	sessions []*Session
}

// Build writes the dump and says where it is.
func Build(o Options) (Result, error) {
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	home, _ := os.UserHomeDir()
	if o.StateDir == "" {
		o.StateDir = filepath.Join(home, ".local", "state", "vera")
	}
	if o.ClaudeDir == "" {
		o.ClaudeDir = filepath.Join(home, ".claude")
	}
	if o.ConfigDir == "" {
		o.ConfigDir = filepath.Join(home, ".config", "vera")
	}
	if o.Out == "" {
		o.Out = filepath.Join(o.StateDir, "dumps", "vera-dump-"+o.Now.Format("20060102-150405"))
	}
	if _, err := os.Stat(o.Out); err == nil {
		return Result{}, fmt.Errorf("%s already exists", o.Out)
	}

	c, err := collect(o)
	if err != nil {
		return Result{}, err
	}
	w := &writer{dir: o.Out, red: newRedactor(o.ConfigDir, filepath.Join(o.StateDir, "identity.json"))}
	if err := os.MkdirAll(o.Out, 0o700); err != nil {
		return Result{}, err
	}

	res := Result{Dir: o.Out, Priced: true}
	for _, conv := range c.conversations {
		res.Conversations = append(res.Conversations, conv.id)
		w.jsonl("conversations/"+conv.id+".jsonl", conv.entries)
		w.text("conversations/"+conv.id+".md", renderConversation(conv))
		if n := len(conv.entries); n > 0 {
			w.text("conversations/"+conv.id+".system.md", conv.entries[n-1].System)
		}
	}
	for _, tb := range c.tasks {
		res.Tasks = append(res.Tasks, tb.task.ID)
		base := "fleet/" + tb.task.ID + "/"
		for _, name := range []string{"task.json", "status.log", "report.md", "claude.json", "run"} {
			w.copy(base+name, filepath.Join(tb.dir, name))
		}
		w.text(base+"env.keys", envKeys(filepath.Join(tb.dir, "env")))
		w.text(base+"brief.md", tb.task.Brief+"\n")
		var sum strings.Builder
		for _, s := range tb.sessions {
			w.copy(base+"sessions/"+s.ID+".jsonl", s.Path)
			sum.WriteString(s.describe() + "\n")
			res.Sessions++
			usd, priced := s.CostAll()
			res.CostUSD += usd
			res.Priced = res.Priced && priced
		}
		if sum.Len() > 0 {
			w.text(base+"sessions.md", sum.String())
		}
	}
	for _, s := range c.delegated {
		w.copy("delegate/"+s.ID+".jsonl", s.Path)
		w.text("delegate/"+s.ID+".md", s.describe())
		res.Sessions++
		usd, priced := s.CostAll()
		res.CostUSD += usd
		res.Priced = res.Priced && priced
	}

	w.copy("verad/verad.json", filepath.Join(o.StateDir, "verad.json"))
	w.copy("verad/projects.json", filepath.Join(o.StateDir, "projects.json"))
	w.text("verad/verad.log", logWindow(filepath.Join(o.StateDir, "verad.log"), c.from, c.to))
	w.copy("claude/settings.json", filepath.Join(o.ClaudeDir, "settings.json"))
	w.text("config.keys", configKeys(o.ConfigDir))
	w.text("versions.txt", versions(o.Version))
	w.text("costs.md", renderCosts(c, res))
	w.text("README.md", renderReadme(o, c, res))
	res.Files = w.files

	if w.err != nil {
		return res, w.err
	}
	if o.Tar {
		res.Tarball = o.Out + ".tar.gz"
		cmd := exec.Command("tar", "-czf", res.Tarball, "-C", filepath.Dir(o.Out), filepath.Base(o.Out))
		if out, err := cmd.CombinedOutput(); err != nil {
			return res, fmt.Errorf("tar: %s", strings.TrimSpace(string(out)))
		}
	}
	return res, nil
}

// collect decides what belongs, then reads it.
func collect(o Options) (*collected, error) {
	c := &collected{}
	convDir := filepath.Join(o.StateDir, "conversations")
	files, err := journal.List(convDir)
	if err != nil {
		return nil, err
	}

	// Which conversations. Named ones by prefix; --since by content;
	// --all everything; nothing named and no tasks named means the
	// latest one.
	pick := map[string]bool{}
	for _, f := range files {
		switch {
		case o.All:
			pick[f.Conversation] = true
		case hasPrefix(f.Conversation, o.Conversations):
			pick[f.Conversation] = true
		case !o.Since.IsZero() && f.Modified.After(o.Since):
			pick[f.Conversation] = true
		}
	}
	if len(pick) == 0 && len(o.Conversations) == 0 && len(o.Tasks) == 0 && o.Since.IsZero() && len(files) > 0 {
		pick[files[0].Conversation] = true
	}
	for _, name := range o.Conversations {
		if !hasPrefixAny(name, pick) {
			return nil, fmt.Errorf("no conversation %q under %s", name, convDir)
		}
	}

	taskIDs := map[string]bool{}
	sessionIDs := map[string]bool{}
	for _, f := range files {
		if !pick[f.Conversation] {
			continue
		}
		entries, err := journal.Read(f.Path)
		if err != nil {
			return nil, err
		}
		if !o.Since.IsZero() {
			kept := entries[:0]
			for _, e := range entries {
				if e.At.After(o.Since) {
					kept = append(kept, e)
				}
			}
			entries = kept
		}
		if len(entries) == 0 {
			continue
		}
		c.conversations = append(c.conversations, conversation{id: f.Conversation, entries: entries})
		for _, e := range entries {
			c.widen(e.At, e.At.Add(time.Duration(e.TookMs)*time.Millisecond))
			for _, r := range e.Rounds {
				if r.Task != "" {
					taskIDs[r.Task] = true
				}
				if r.Session != "" {
					sessionIDs[r.Session] = true
				}
			}
		}
	}
	sort.Slice(c.conversations, func(i, j int) bool {
		return c.conversations[i].entries[0].At.Before(c.conversations[j].entries[0].At)
	})

	// Which tasks: the ones the conversations touched, the ones named,
	// and — with a window — the ones alive in it.
	store := fleet.NewStore(filepath.Join(o.StateDir, "fleet"))
	all, err := store.List()
	if err != nil {
		return nil, err
	}
	for _, t := range all {
		switch {
		case taskIDs[t.ID], hasPrefix(t.ID, o.Tasks), o.All:
			taskIDs[t.ID] = true
		case !o.Since.IsZero() && (t.Spawned.After(o.Since) || t.Resumed.After(o.Since) || t.TurnEnded.After(o.Since)):
			taskIDs[t.ID] = true
		case !c.from.IsZero() && overlaps(t, c.from, c.to):
			taskIDs[t.ID] = true
		}
	}
	for _, name := range o.Tasks {
		if !hasPrefixAny(name, taskIDs) {
			return nil, fmt.Errorf("no task %q", name)
		}
	}
	for _, t := range all {
		if !taskIDs[t.ID] {
			continue
		}
		tb := taskBundle{task: t, dir: store.TaskDir(t.ID)}
		tb.statuses, _ = store.Statuses(t.ID)
		for _, path := range sessionsIn(o.ClaudeDir, t.Worktree, t.Spawned.Add(-time.Minute)) {
			if s, err := readSession(path); err == nil {
				tb.sessions = append(tb.sessions, s)
			}
		}
		c.tasks = append(c.tasks, tb)
		c.widen(t.Spawned, latest(t.Resumed, t.TurnEnded, t.ClosedAt))
	}
	sort.Slice(c.tasks, func(i, j int) bool { return c.tasks[i].task.Spawned.Before(c.tasks[j].task.Spawned) })

	ids := make([]string, 0, len(sessionIDs))
	for id := range sessionIDs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if path := findSession(o.ClaudeDir, id); path != "" {
			if s, err := readSession(path); err == nil {
				c.delegated = append(c.delegated, s)
			}
		}
	}

	if len(c.conversations) == 0 && len(c.tasks) == 0 {
		return nil, errors.New("nothing to dump: no conversations have been journaled yet and no tasks match")
	}
	return c, nil
}

func (c *collected) widen(from, to time.Time) {
	if from.IsZero() {
		return
	}
	if c.from.IsZero() || from.Before(c.from) {
		c.from = from
	}
	if to.After(c.to) {
		c.to = to
	}
	if from.After(c.to) {
		c.to = from
	}
}

func overlaps(t *fleet.Task, from, to time.Time) bool {
	start := t.Spawned
	end := latest(t.Resumed, t.TurnEnded, t.ClosedAt)
	if end.IsZero() || !t.Closed {
		end = time.Now()
	}
	return !start.After(to) && !end.Before(from)
}

func latest(ts ...time.Time) time.Time {
	var out time.Time
	for _, t := range ts {
		if t.After(out) {
			out = t
		}
	}
	return out
}

func hasPrefix(id string, names []string) bool {
	for _, n := range names {
		if n != "" && strings.HasPrefix(id, n) {
			return true
		}
	}
	return false
}

func hasPrefixAny(name string, ids map[string]bool) bool {
	for id := range ids {
		if strings.HasPrefix(id, name) {
			return true
		}
	}
	return false
}

// --- writing --------------------------------------------------------------

type writer struct {
	dir   string
	red   *redactor
	files int
	err   error
}

func (w *writer) text(rel, text string) {
	if text == "" {
		return
	}
	path := filepath.Join(w.dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		w.err = err
		return
	}
	if err := os.WriteFile(path, []byte(w.red.apply(text)), 0o600); err != nil {
		w.err = err
		return
	}
	w.files++
}

// copy copies a file that may not exist; a missing one is not an error,
// it is a task that never wrote a report.
func (w *writer) copy(rel, src string) {
	b, err := os.ReadFile(src)
	if err != nil {
		return
	}
	w.text(rel, string(b))
}

func (w *writer) jsonl(rel string, entries []journal.Entry) {
	var b strings.Builder
	for _, e := range entries {
		line, err := json.Marshal(e)
		if err != nil {
			continue
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	w.text(rel, b.String())
}

// envKeys lists the names in a KEY=VALUE file, never the values.
func envKeys(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var keys []string
	for _, line := range strings.Split(string(b), "\n") {
		if k, _, ok := strings.Cut(strings.TrimPrefix(strings.TrimSpace(line), "export "), "="); ok && k != "" {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return ""
	}
	return strings.Join(keys, "\n") + "\n"
}

// configKeys says what is configured without saying what it is.
func configKeys(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "no " + dir + "\n"
	}
	var b strings.Builder
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		fmt.Fprintf(&b, "%s\n", e.Name())
		for _, k := range strings.Split(strings.TrimSpace(envKeys(filepath.Join(dir, e.Name()))), "\n") {
			if k != "" {
				fmt.Fprintf(&b, "  %s=…\n", k)
			}
		}
	}
	return b.String()
}

// logWindow is verad's log for the minutes in question, with ten
// minutes either side; with no window, the tail.
func logWindow(path string, from, to time.Time) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(b), "\n")
	if from.IsZero() {
		if len(lines) > 500 {
			lines = lines[len(lines)-500:]
		}
		return strings.Join(lines, "\n")
	}
	from, to = from.Add(-10*time.Minute), to.Add(10*time.Minute)
	var out []string
	var stamped struct {
		Time time.Time `json:"time"`
	}
	inWindow := false
	for _, line := range lines {
		if strings.HasPrefix(line, "{") && json.Unmarshal([]byte(line), &stamped) == nil && !stamped.Time.IsZero() {
			inWindow = !stamped.Time.Before(from) && !stamped.Time.After(to)
			if stamped.Time.After(to) {
				break
			}
		}
		// Unstamped lines (verad's banner, a stack trace) go with the
		// stamped line before them.
		if inWindow {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n") + "\n"
}

func versions(vera string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "vera    %s\n", vera)
	for _, cmd := range [][]string{{"claude", "--version"}, {"rook", "version"}, {"uname", "-srm"}, {"sw_vers", "-productVersion"}} {
		out, err := exec.Command(cmd[0], cmd[1:]...).Output()
		if err != nil {
			continue
		}
		fmt.Fprintf(&b, "%-7s %s\n", cmd[0], strings.TrimSpace(string(out)))
	}
	return b.String()
}
