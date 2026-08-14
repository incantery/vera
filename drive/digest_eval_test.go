//go:build eval

// The digest quality evals: the production Digest prompt run against
// REAL replies from a real working session, judged by a cheap Claude
// model for the failure modes we have actually seen in the app.
//
// This suite is the reproduction, not the regression net — it is
// EXPECTED to fail while the digest prompt has the bug. The workflow:
//
//	go test -tags eval -run Digest ./drive/ -v
//
// reproduce the failure reliably first, then change the prompt, then
// watch this go green — and only then does it become a regression net.
//
// It is tagged out of the default build because every case costs real
// inference: one Digest call on the rook agent's model (needs
// $OPENAI_API_KEY or ~/.config/rook/openai_key) and one judge call via
// `claude -p --model haiku` (~$0.02). A full run at the default 3
// iterations is under a dollar.
//
// The failure modes, drawn from the live incident (a conversation
// whose recent turns all digested into imperative word-salad):
//
//   - agency_inversion: the assistant's own completed work or offered
//     proposals digested as instructions to the reader ("Verify the
//     endpoint is removed" — the assistant already verified it).
//   - invented_action: the reply asks nothing of the reader, yet a
//     bullet commands the reader anyway.
//   - content_salad: fragments of the reply strung into a bullet that
//     states no fact from the reply.
//   - wrong_headline: a headline the reply does not support.
package drive

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

type digestCase struct {
	Name   string `json:"name"`
	Note   string `json:"note"`
	Prompt string `json:"prompt"`
	Reply  string `json:"reply"`
}

type violation struct {
	Kind   string `json:"kind"`
	Bullet int    `json:"bullet"`
	Quote  string `json:"quote"`
	Why    string `json:"why"`
}

const judgeRubric = `You judge the DIGEST of an assistant's REPLY, shown in a conversation UI where a busy engineer reads the digest INSTEAD of the reply. The digest must faithfully compress the reply: right facts, right actor, nothing invented.

Report every instance of these failure modes:
- "agency_inversion": a bullet phrases, as an instruction to the reader, something the REPLY says the assistant already did, already verified, or offered to do itself. Example: reply says "I verified the endpoint is gone", digest says "Verify the endpoint is removed".
- "invented_action": the reply asks nothing of the reader, yet a bullet is an imperative aimed at the reader.
- "content_salad": a bullet strings fragments together without stating any coherent fact from the reply.
- "wrong_headline": the headline asserts something the reply does not support.

NOT failures: a bullet that quotes or compresses steps the REPLY ITSELF addresses to the reader (a how-to reply digested as how-to bullets is faithful, even where the assistant also ran those steps itself while demonstrating them); imperative phrasing inside quoted commands or file contents; the headline compressing aggressively but truthfully.

Bullets are numbered from 1. Answer with STRICT JSON only, no fences, no prose:
{"violations":[{"kind":"...","bullet":N,"quote":"the offending words","why":"one short sentence"}]}
Use bullet 0 for the headline. Empty list if the digest is clean.`

// evalLLM builds the same wire roost runs in production: same model,
// same effort, key from the same two places.
func evalLLM(t *testing.T) *LLM {
	t.Helper()
	key := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if key == "" {
		home, _ := os.UserHomeDir()
		b, err := os.ReadFile(filepath.Join(home, ".config", "rook", "openai_key"))
		if err != nil {
			t.Skip("no $OPENAI_API_KEY and no ~/.config/rook/openai_key — cannot run the production digest model")
		}
		key = strings.TrimSpace(string(b))
	}
	return &LLM{Key: key, Name: "gpt-5.6-luna", Effort: "low"}
}

// judge asks a cheap Claude to grade one digest against its reply.
// One judge hiccup (a timeout, a broken envelope) gets one retry —
// harness flake must not read as a digest verdict.
func judge(t *testing.T, c digestCase, headline string, bullets []string) []violation {
	t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\nTHE READER ASKED:\n%s\n\nTHE REPLY:\n%s\n\nTHE DIGEST:\nheadline: %s\n", judgeRubric, c.Prompt, c.Reply, headline)
	for i, l := range bullets {
		fmt.Fprintf(&b, "bullet %d: %s\n", i+1, l)
	}
	var lastErr error
	for attempt := 1; attempt <= 2; attempt++ {
		vs, err := judgeOnce(b.String())
		if err == nil {
			return vs
		}
		lastErr = err
		t.Logf("judge attempt %d failed: %v", attempt, err)
	}
	t.Fatalf("the judge failed twice: %v", lastErr)
	return nil
}

func judgeOnce(prompt string) ([]violation, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "claude", "-p", "--model", "haiku", "--output-format", "json", prompt)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("call: %v: %.200s", err, out)
	}
	var envelope struct {
		IsError bool   `json:"is_error"`
		Result  string `json:"result"`
	}
	if err := json.Unmarshal(out, &envelope); err != nil || envelope.IsError {
		return nil, fmt.Errorf("envelope (err %v): %.400s", err, out)
	}
	// The verdict is the FIRST JSON object in the answer — judges
	// sometimes wrap it in fences or append commentary after it.
	verdict := strings.TrimSpace(envelope.Result)
	i := strings.Index(verdict, "{")
	if i < 0 {
		return nil, fmt.Errorf("no JSON in the answer: %.400s", verdict)
	}
	var parsed struct {
		Violations []violation `json:"violations"`
	}
	if err := json.NewDecoder(strings.NewReader(verdict[i:])).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("broken JSON contract: %v: %.400s", err, verdict)
	}
	return parsed.Violations, nil
}

func loadCases(t *testing.T) []digestCase {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("testdata", "digest", "*.json"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no fixtures under testdata/digest: %v", err)
	}
	var cases []digestCase
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		var c digestCase
		if err := json.Unmarshal(b, &c); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		cases = append(cases, c)
	}
	return cases
}

// TestJudgeCalibration pins the judge itself before it is trusted:
// a hand-written faithful digest of a real reply must pass clean, and
// the digest the production prompt actually emitted for that reply
// during the live incident must be flagged for agency inversion.
// Judge-only — two haiku calls, no digest-model spend.
func TestJudgeCalibration(t *testing.T) {
	var c digestCase
	b, err := os.ReadFile(filepath.Join("testdata", "digest", "revert-report.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}

	t.Run("faithful digest passes", func(t *testing.T) {
		vs := judge(t, c,
			"Revert complete and committed as `0d1f364` — `engine/GUIDE.md` stays canonical with no app integration.",
			[]string{
				"roost removed the docs namespace, startup sync, API route, guide page, header control, and the `marked` dependency.",
				"Verified: `/api/docs/guide` now returns only the SPA shell — the endpoint is gone.",
				"An 18-line manual-test artifact now sits on this session's shelf, beside the separate \"task management model — draft\".",
				"All four Go test packages pass.",
			})
		for _, v := range vs {
			t.Errorf("the judge flagged a faithful digest: [%s] bullet %d: %q — %s", v.Kind, v.Bullet, v.Quote, v.Why)
		}
	})

	// Verbatim from roost-digests.jsonl — what the app actually showed.
	t.Run("the live bad digest is flagged", func(t *testing.T) {
		vs := judge(t, c,
			"Revert complete; `engine/GUIDE.md` remains canonical, and the product has no guide integration.",
			[]string{
				"Verify `/api/docs/guide` returns the SPA HTML catch-all, confirming the endpoint is removed.",
				"Removed docs namespace, startup sync, API route, guide page, header control, `marked`, tests, and runtime docs.",
				"Keep the session-only artifact under 20 lines; it explains manual multi-turn Rook-to-Claude Code testing.",
				"Test permission gates, Log turn entries, denied deletion, Conversation verification, acceptance, and workspace deletion.",
				"Preserve separation from “task management model — draft”; commit `0d1f364` and all four Go test packages pass.",
			})
		inverted := false
		for _, v := range vs {
			t.Logf("[%s] bullet %d: %q — %s", v.Kind, v.Bullet, v.Quote, v.Why)
			if v.Kind == "agency_inversion" || v.Kind == "invented_action" {
				inverted = true
			}
		}
		if !inverted {
			t.Error("the judge missed the known agency inversion in the live incident's digest")
		}
	})
}

// TestDigestFidelity: each fixture digested by the production prompt
// and model, each digest graded by the judge, several iterations so a
// flaky pass cannot hide the failure. EVAL_RUNS overrides the count.
func TestDigestFidelity(t *testing.T) {
	m := evalLLM(t)
	runs := 3
	if v, err := strconv.Atoi(os.Getenv("EVAL_RUNS")); err == nil && v > 0 {
		runs = v
	}
	// The tolerance split: agency inversion is the bug this suite
	// pins — ONE occurrence in any run fails. Other quality noise
	// (a lossy headline, a muddled bullet) is stochastic in a small
	// digest model; one dirty run out of the set is logged and
	// tolerated, more than one fails.
	for _, c := range loadCases(t) {
		t.Run(c.Name, func(t *testing.T) {
			dirty := 0
			for i := 1; i <= runs; i++ {
				headline, bullets, err := m.Digest(context.Background(), c.Prompt, c.Reply)
				if err != nil {
					t.Fatalf("run %d: Digest: %v", i, err)
				}
				vs := judge(t, c, headline, bullets)
				if len(vs) == 0 {
					t.Logf("run %d/%d: clean", i, runs)
					continue
				}
				dirty++
				t.Logf("run %d/%d: %d violation(s) — headline: %s", i, runs, len(vs), headline)
				for _, v := range vs {
					t.Logf("  [%s] bullet %d: %q — %s", v.Kind, v.Bullet, v.Quote, v.Why)
					if v.Kind == "agency_inversion" || v.Kind == "invented_action" {
						t.Errorf("%s run %d: the pinned bug is back — %s (%s)", c.Name, i, v.Kind, c.Note)
					}
				}
			}
			if dirty > 1 {
				t.Errorf("%s: %d/%d runs produced an unfaithful digest (%s)", c.Name, dirty, runs, c.Note)
			}
		})
	}
}
