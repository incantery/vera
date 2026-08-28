package dump

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/incantery/vera/price"
)

// Claude Code keeps each session as jsonl under
// ~/.claude/projects/<cwd with / and . as ->/<session id>.jsonl. That
// file is the agent's whole story — every prompt, thought, tool call
// and reply — and every assistant line carries the usage the API
// billed. This reads it for two things: which files belong to a task,
// and what they cost.

// projectDir is where Claude Code keeps sessions started in cwd.
func projectDir(claudeDir, cwd string) string {
	enc := strings.NewReplacer("/", "-", ".", "-").Replace(cwd)
	return filepath.Join(claudeDir, "projects", enc)
}

// Session is one Claude Code session file, summarized.
type Session struct {
	ID       string           `json:"id"`
	Path     string           `json:"-"`
	CWD      string           `json:"cwd,omitempty"`
	First    time.Time        `json:"first"`
	Last     time.Time        `json:"last"`
	Prompts  int              `json:"prompts"`
	Turns    int              `json:"turns"` // assistant messages
	ToolUses int              `json:"tool_uses"`
	Tokens   map[string]Usage `json:"tokens"` // by model
	Lines    int              `json:"lines"`
}

// Usage is what the API counted, summed.
type Usage struct {
	Input      int64 `json:"input"`
	CacheWrite int64 `json:"cache_write"`
	CacheRead  int64 `json:"cache_read"`
	Output     int64 `json:"output"`
}

func (u *Usage) add(o Usage) {
	u.Input += o.Input
	u.CacheWrite += o.CacheWrite
	u.CacheRead += o.CacheRead
	u.Output += o.Output
}

// Cost is the notional USD, and whether the model was priced at all.
// The table is the price package's, shared with the chat's status
// line: a dump and a status line that disagreed about what a turn cost
// would be worse than either of them alone.
func (u Usage) Cost(model string) (float64, bool) {
	return price.Of(model, price.Tokens(u))
}

// CostAll sums the session; priced is false if any model was unknown.
func (s *Session) CostAll() (usd float64, priced bool) {
	priced = true
	for model, u := range s.Tokens {
		c, ok := u.Cost(model)
		if !ok {
			priced = false
		}
		usd += c
	}
	return usd, priced
}

type sessionLine struct {
	Type      string    `json:"type"`
	SessionID string    `json:"sessionId"`
	CWD       string    `json:"cwd"`
	Timestamp time.Time `json:"timestamp"`
	Message   *struct {
		ID      string          `json:"id"`
		Model   string          `json:"model"`
		Content json.RawMessage `json:"content"`
		Usage   *struct {
			Input      int64 `json:"input_tokens"`
			CacheWrite int64 `json:"cache_creation_input_tokens"`
			CacheRead  int64 `json:"cache_read_input_tokens"`
			Output     int64 `json:"output_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

// readSession summarizes one file. Claude Code writes one line per
// content block, and the lines of one API message repeat its usage —
// so usage is counted once per message id.
func readSession(path string) (*Session, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	s := &Session{ID: strings.TrimSuffix(filepath.Base(path), ".jsonl"), Path: path, Tokens: map[string]Usage{}}
	counted := map[string]bool{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		s.Lines++
		var l sessionLine
		if json.Unmarshal(sc.Bytes(), &l) != nil {
			continue
		}
		if l.CWD != "" && s.CWD == "" {
			s.CWD = l.CWD
		}
		if !l.Timestamp.IsZero() {
			if s.First.IsZero() || l.Timestamp.Before(s.First) {
				s.First = l.Timestamp
			}
			if l.Timestamp.After(s.Last) {
				s.Last = l.Timestamp
			}
		}
		switch l.Type {
		case "user":
			if l.Message != nil && !strings.Contains(string(l.Message.Content), `"tool_result"`) {
				s.Prompts++
			}
		case "assistant":
			if l.Message == nil {
				continue
			}
			s.ToolUses += strings.Count(string(l.Message.Content), `"type":"tool_use"`)
			if l.Message.Usage == nil || counted[l.Message.ID] {
				continue
			}
			counted[l.Message.ID] = true
			s.Turns++
			u := s.Tokens[l.Message.Model]
			u.add(Usage{l.Message.Usage.Input, l.Message.Usage.CacheWrite, l.Message.Usage.CacheRead, l.Message.Usage.Output})
			s.Tokens[l.Message.Model] = u
		}
	}
	return s, sc.Err()
}

// sessionsIn lists the sessions Claude Code kept for cwd that belong
// to a task, newest last. Belonging is the task id appearing in the
// file: the brief names the task's status URL, so the agent's session
// carries it from its first line. The directory alone is not enough —
// a scout works in the checkout itself, beside every session the
// person ever ran there.
func sessionsIn(claudeDir, cwd, task string, since time.Time) []string {
	dir := projectDir(claudeDir, cwd)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil || (!since.IsZero() && info.ModTime().Before(since)) {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if task != "" && !mentions(path, task) {
			continue
		}
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

// mentions: does the file contain the string? The first lines are
// enough — the brief is the first prompt — but a resumed session's
// nudge names the task too, so the whole file is read.
func mentions(path, needle string) bool {
	b, err := os.ReadFile(path)
	return err == nil && strings.Contains(string(b), needle)
}

// findSession finds a session by id under any project — where a
// delegation ran is not in the round, only which session it was.
func findSession(claudeDir, id string) string {
	matches, _ := filepath.Glob(filepath.Join(claudeDir, "projects", "*", id+".jsonl"))
	if len(matches) == 0 {
		return ""
	}
	return matches[0]
}

func (s *Session) describe() string {
	var b strings.Builder
	fmt.Fprintf(&b, "session %s\n", s.ID)
	if s.CWD != "" {
		fmt.Fprintf(&b, "  in       %s\n", s.CWD)
	}
	if !s.First.IsZero() {
		fmt.Fprintf(&b, "  from     %s to %s (%s)\n", s.First.Local().Format(time.RFC3339), s.Last.Local().Format(time.RFC3339), s.Last.Sub(s.First).Round(time.Second))
	}
	fmt.Fprintf(&b, "  prompts  %d · turns %d · tool uses %d\n", s.Prompts, s.Turns, s.ToolUses)
	models := make([]string, 0, len(s.Tokens))
	for m := range s.Tokens {
		models = append(models, m)
	}
	sort.Strings(models)
	for _, m := range models {
		u := s.Tokens[m]
		line := fmt.Sprintf("  %-22s in %d · cache write %d · cache read %d · out %d", m, u.Input, u.CacheWrite, u.CacheRead, u.Output)
		if c, ok := u.Cost(m); ok {
			line += fmt.Sprintf(" · ≈ $%.2f", c)
		} else {
			line += " · (no price known)"
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

// --- what a task or a delegation cost, for whoever else is asking ----

// `vera costs` reads the journal and has the same question this
// package already answers for a dump: what did the agent behind that
// round actually spend? The answer must be the same number in both
// places, so it is this code rather than a second copy of it.

// Spend is what one or more Claude Code sessions cost. Priced is false
// if any session ran on a model the price table does not know: the
// dollars are then a floor, not a total, and a caller should say so.
type Spend struct {
	USD      float64
	Priced   bool
	Sessions int
}

func (s *Spend) add(sess *Session) {
	usd, priced := sess.CostAll()
	s.USD += usd
	s.Sessions++
	if !priced {
		s.Priced = false
	}
}

// SessionSpend is what one Claude Code session cost, by id — a
// delegation, which the journal records as a session and nothing else.
// A session whose file is gone comes back with no sessions counted.
func SessionSpend(claudeDir, id string) Spend {
	spend := Spend{Priced: true}
	path := findSession(claudeDir, id)
	if path == "" {
		return spend
	}
	s, err := readSession(path)
	if err != nil {
		return spend
	}
	spend.add(s)
	return spend
}

// TaskSpend is what every session belonging to a fleet task cost. The
// task id in the file is what says a session belongs to it, exactly as
// a dump decides — a scout works in the checkout itself, beside every
// session the person ever ran there.
func TaskSpend(claudeDir, worktree, task string, since time.Time) Spend {
	spend := Spend{Priced: true}
	for _, path := range sessionsIn(claudeDir, worktree, task, since) {
		s, err := readSession(path)
		if err != nil {
			continue
		}
		spend.add(s)
	}
	return spend
}

// ClaudeDir is where Claude Code keeps its sessions.
func ClaudeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude")
}
