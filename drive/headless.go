// The headless Turner: one turn is one `claude -p --resume` in the
// session's own directory. Print mode forks — the reply lands in a new
// session whose id rides back in the JSON envelope — and stdout is the
// reply, so the loop needs no transcript watching and no terminal.
//
// What this costs, honestly: a forked turn runs under print mode's
// permission rules (tools that would prompt are refused, not granted),
// and its tokens bill like any headless use. Goals that want analysis
// and hypotheses fit; goals that want the agent editing files belong
// on a live session with a human near the keyboard.
package drive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"time"
)

type Headless struct {
	Bin     string        // the claude binary; "" means "claude" from PATH
	Dir     string        // the session's cwd — resume looks the session up by project
	Timeout time.Duration // per turn; default 10m
	// AllowedTools is the turn's tool policy, passed straight to
	// claude's own permission system (--allowedTools). Empty means
	// print mode's default: permission-gated tools are refused.
	AllowedTools []string
}

func (h *Headless) bin() string {
	if h.Bin != "" {
		return h.Bin
	}
	return "claude"
}

func (h *Headless) timeout() time.Duration {
	if h.Timeout > 0 {
		return h.Timeout
	}
	return 10 * time.Minute
}

// resultEnvelope is claude's --output-format json answer, reduced to
// what the loop reads. The format is claude's own and drifts; every
// field is optional and a missing one degrades, never crashes.
type resultEnvelope struct {
	Type      string  `json:"type"`
	Subtype   string  `json:"subtype"`
	IsError   bool    `json:"is_error"`
	Result    string  `json:"result"`
	SessionID string  `json:"session_id"`
	CostUSD   float64 `json:"total_cost_usd"`
}

func (h *Headless) RunTurn(ctx context.Context, sessionID, prompt string) (Turn, error) {
	// Session ids are transcript filenames (UUIDs in practice). No
	// shell is involved here, but an id that is not filename-shaped is
	// not one of ours and is refused outright.
	if !safeID(sessionID) {
		return Turn{}, errors.New("session id is not resume-safe")
	}
	return h.exec(ctx, []string{"-p", "--resume", sessionID, "--output-format", "json", prompt})
}

// StartTurn births a session: the same print-mode turn with no
// --resume, run in the target directory — a fresh agent takes its
// first breath there, and the envelope names the session it now is.
func (h *Headless) StartTurn(ctx context.Context, prompt string) (Turn, error) {
	return h.exec(ctx, []string{"-p", "--output-format", "json", prompt})
}

func (h *Headless) exec(ctx context.Context, args []string) (Turn, error) {
	if len(h.AllowedTools) > 0 {
		// The tool policy rides before the trailing prompt, into
		// claude's own permission system. One =-joined token: the flag
		// is variadic, and a bare form would swallow the prompt.
		prompt := args[len(args)-1]
		args = append(append(args[:len(args)-1:len(args)-1],
			"--allowedTools="+strings.Join(h.AllowedTools, ",")), prompt)
	}
	ctx, cancel := context.WithTimeout(ctx, h.timeout())
	defer cancel()
	cmd := exec.CommandContext(ctx, h.bin(), args...)
	cmd.Dir = h.Dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()

	var env resultEnvelope
	parseErr := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &env)
	if runErr != nil {
		// claude exits nonzero on an errored turn but may still have
		// said why in the envelope; the envelope's word beats "exit 1".
		if parseErr == nil && env.Result != "" {
			return Turn{SessionID: env.SessionID, CostUSD: env.CostUSD},
				errors.New(snip(env.Result, 300))
		}
		return Turn{}, errors.New(runErr.Error() + ": " + snip(stderr.String(), 300))
	}
	if parseErr != nil {
		return Turn{}, errors.New("claude's answer did not parse: " + snip(stdout.String(), 200))
	}
	if env.IsError {
		return Turn{SessionID: env.SessionID, CostUSD: env.CostUSD},
			errors.New("claude reported an errored turn: " + snip(env.Result, 300))
	}
	return Turn{Reply: env.Result, SessionID: env.SessionID, CostUSD: env.CostUSD}, nil
}

// safeID: [A-Za-z0-9._-] only, nonempty — the charset a transcript
// filename can carry.
func safeID(id string) bool {
	if id == "" {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '_', c == '-':
		default:
			return false
		}
	}
	return true
}

func snip(s string, max int) string {
	s = string(bytes.Join(bytes.Fields([]byte(s)), []byte(" ")))
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && s[cut]&0xC0 == 0x80 {
		cut--
	}
	return s[:cut] + "…"
}
