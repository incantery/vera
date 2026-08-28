// Package journal is the record of what Vera said and did, on disk.
//
// verad's History is a window: the last few turns, in memory, gone on
// restart. That is right for the model's context and wrong for the
// question that comes a day later — "why did it do that?" — which
// needs the whole exchange as it happened: the prompt the model saw,
// what it was told, every tool it reached for and what came back, and
// what it all cost. Grafana holds a copy when it is configured; this
// is the copy that is always there, and the one `vera dump` reads.
//
// One file per conversation, one line per exchange, append-only. A
// line is written after the exchange ends, failed or not: a failed
// exchange is the one most worth reading later.
package journal

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Round is one tool call inside an exchange, in order.
type Round struct {
	At     time.Time       `json:"at"`
	Tool   string          `json:"tool"`
	CallID string          `json:"call_id,omitempty"`
	Args   json.RawMessage `json:"args,omitempty"`
	Result string          `json:"result"`
	TookMs int64           `json:"took_ms"`
	// Task is the fleet task this round started or touched.
	Task string `json:"task,omitempty"`
	// Session is a Claude Code session this round ran (a delegation).
	Session string `json:"session,omitempty"`
	// CostUSD is what that session reported it cost, when it did.
	CostUSD float64 `json:"cost_usd,omitempty"`

	// What the policy said about this call, and what the person said
	// if it was put to them. Decision is allow, ask or deny; Answer is
	// yes, no or always, and is set only when it was asked. Reason is
	// the profile's own sentence — the one the model was told.
	//
	// A round with no Decision is a tool that has no policy: `fleet`
	// and `delegate` are Vera's own, and are not gated.
	Decision string `json:"decision,omitempty"`
	Answer   string `json:"answer,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// Entry is one exchange: a thing said, and everything until the reply
// was done.
type Entry struct {
	At           time.Time `json:"at"`
	Version      string    `json:"version"`
	Conversation string    `json:"conversation"`
	Device       string    `json:"device,omitempty"`
	Model        string    `json:"model"`
	// Effort is the reasoning dial the model was asked for — "high",
	// "none" — because the same model at two efforts is two different
	// bills and two different latencies, and a record that names only
	// the model cannot tell them apart.
	Effort string `json:"effort,omitempty"`
	// Provider is which wire answered — "openai" or "anthropic". A
	// model name does not always say: the same name can be reached
	// through somebody's proxy.
	Provider string `json:"provider,omitempty"`
	TraceID  string `json:"trace_id,omitempty"`
	// System is the whole system prompt, attention paragraph and all:
	// the point of the record is to see what the model saw.
	System       string `json:"system"`
	Said         string `json:"said"`
	Answered     string `json:"answered"`
	Error        string `json:"error,omitempty"`
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
	// CacheReadTokens and CacheWriteTokens are part of InputTokens, not
	// additions to it: how much of the prompt was read back from the
	// provider's cache, and how much was written into it. Zero from a
	// provider with no cache, or a prompt whose prefix keeps changing.
	CacheReadTokens  int `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int `json:"cache_write_tokens,omitempty"`
	// ThinkingParts is how many pieces of reasoning arrived before the
	// answer. The reasoning itself is not kept — it is the model's
	// working, not what it said — but that it thought is worth knowing.
	ThinkingParts int     `json:"thinking_parts,omitempty"`
	FirstSignMs   int64   `json:"first_sign_ms"`
	FirstTokenMs  int64   `json:"first_token_ms"`
	TookMs        int64   `json:"took_ms"`
	Rounds        []Round `json:"rounds,omitempty"`
}

// Writer appends entries under Dir.
type Writer struct {
	Dir string
	mu  sync.Mutex
}

// Path is where a conversation's file lives. A conversation with no id
// (curl, a stateless caller) goes in one shared file.
func Path(dir, conversation string) string {
	return filepath.Join(dir, fileName(conversation))
}

func fileName(conversation string) string {
	name := strings.TrimSpace(conversation)
	if name == "" {
		name = "stateless"
	}
	// An id is a file name; keep it one.
	name = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			return r
		}
		return '_'
	}, name)
	return name + ".jsonl"
}

// Write appends one entry. Arguments that are not JSON (a model can
// produce those) are kept as a string rather than sinking the line.
func (w *Writer) Write(e Entry) error {
	for i := range e.Rounds {
		if len(e.Rounds[i].Args) > 0 && !json.Valid(e.Rounds[i].Args) {
			e.Rounds[i].Args, _ = json.Marshal(string(e.Rounds[i].Args))
		}
	}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := os.MkdirAll(w.Dir, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(Path(w.Dir, e.Conversation), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(b, '\n'))
	return err
}

// Read loads one conversation's file. A line that does not parse is
// skipped: a half-written last line must not hide the rest.
func Read(path string) ([]Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var e Entry
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			out = append(out, e)
		}
	}
	return out, sc.Err()
}

// File is one conversation on disk.
type File struct {
	Conversation string
	Path         string
	Modified     time.Time
}

// List returns every conversation under dir, newest first.
func List(dir string) ([]File, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []File
	for _, d := range entries {
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".jsonl") {
			continue
		}
		info, err := d.Info()
		if err != nil {
			continue
		}
		out = append(out, File{
			Conversation: strings.TrimSuffix(d.Name(), ".jsonl"),
			Path:         filepath.Join(dir, d.Name()),
			Modified:     info.ModTime(),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Modified.After(out[j].Modified) })
	return out, nil
}
