package drive

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// scriptTurner hands out replies in order, forking the session id each
// turn the way print mode does.
type scriptTurner struct {
	replies []string
	sent    []string
}

func (t *scriptTurner) RunTurn(ctx context.Context, sessionID, prompt string) (Turn, error) {
	t.sent = append(t.sent, prompt)
	n := len(t.sent) - 1
	reply := "reply " + prompt
	if n < len(t.replies) {
		reply = t.replies[n]
	}
	return Turn{Reply: reply, SessionID: "fork-" + string(rune('a'+n)), CostUSD: 0.01}, nil
}

type scriptJudge struct {
	verdicts []Verdict
	seen     [][]Exchange
}

func (j *scriptJudge) Judge(ctx context.Context, goal string, history []Exchange) (Verdict, error) {
	j.seen = append(j.seen, append([]Exchange(nil), history...))
	n := len(j.seen) - 1
	if n >= len(j.verdicts) {
		return Verdict{Done: true, Reason: "script ran out"}, nil
	}
	return j.verdicts[n], nil
}

func TestRunFollowsTheForkAndStopsOnDone(t *testing.T) {
	tr := &scriptTurner{replies: []string{"I don't have access to that", "fine: three hypotheses"}}
	j := &scriptJudge{verdicts: []Verdict{
		{Prompt: "You do not need access — hypothesize anyway."},
		{Done: true, Reason: "hypotheses delivered"},
	}}
	res, err := (&Loop{Turner: tr, Judge: j}).Run(context.Background(), "orig", "get a hypothesis")
	if err != nil || !res.Done || res.Reason != "hypotheses delivered" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	// The conversation moved to the fork: turn two resumed fork-a, and
	// the record names the final fork for a human to resume.
	if res.SessionID != "fork-b" {
		t.Fatalf("final session: %q", res.SessionID)
	}
	if tr.sent[0] != "get a hypothesis" || !strings.Contains(tr.sent[1], "hypothesize anyway") {
		t.Fatalf("sent: %v", tr.sent)
	}
	if res.CostUSD < 0.019 || res.CostUSD > 0.021 {
		t.Fatalf("cost: %v", res.CostUSD)
	}
	if len(j.seen[1]) != 2 {
		t.Fatalf("the judge must see the whole history: %d rounds", len(j.seen[1]))
	}
}

func TestRunSpendsTheBudgetAndSaysSo(t *testing.T) {
	tr := &scriptTurner{}
	j := &scriptJudge{verdicts: []Verdict{{Prompt: "a"}, {Prompt: "b"}}}
	res, err := (&Loop{Turner: tr, Judge: j, MaxTurns: 2}).Run(context.Background(), "orig", "goal")
	if err != nil {
		t.Fatalf("budget exhaustion is not an error: %v", err)
	}
	if res.Done || !strings.Contains(res.Reason, "turn budget") || len(res.Turns) != 2 {
		t.Fatalf("res=%+v", res)
	}
}

func TestParseVerdictHoldsTheShape(t *testing.T) {
	v, err := ParseVerdict("DONE\nThe worker answered.")
	if err != nil || !v.Done || v.Reason != "The worker answered." {
		t.Fatalf("v=%+v err=%v", v, err)
	}
	if v, err = ParseVerdict("done."); err != nil || !v.Done {
		t.Fatalf("v=%+v err=%v", v, err)
	}
	v, err = ParseVerdict("CONTINUE\nPush harder.")
	if err != nil || v.Done || v.Prompt != "Push harder." {
		t.Fatalf("v=%+v err=%v", v, err)
	}
	if _, err = ParseVerdict("CONTINUE"); err == nil {
		t.Fatal("CONTINUE with nothing to say must refuse")
	}
	if _, err = ParseVerdict("Probably fine?"); err == nil {
		t.Fatal("a broken shape must refuse, never guess")
	}
}

func TestLLMJudgeShowsTheConversationAndMetersTheSpend(t *testing.T) {
	var sentUser string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []chatMsg `json:"messages"`
		}
		raw, _ := io.ReadAll(r.Body)
		json.Unmarshal(raw, &body)
		sentUser = body.Messages[1].Content
		io.WriteString(w, `{"choices":[{"message":{"content":"CONTINUE\nAsk directly."}}],"usage":{"prompt_tokens":1000,"completion_tokens":100}}`)
	}))
	defer srv.Close()
	var spent float64
	j := &LLMJudge{LLM: &LLM{Client: srv.Client(), Base: srv.URL, Key: "k", Name: "gpt-5-mini",
		Spend: func(c float64) { spent += c }}}
	v, err := j.Judge(context.Background(), "the goal", []Exchange{{Prompt: "asked", Reply: "deflected"}})
	if err != nil || v.Done || v.Prompt != "Ask directly." {
		t.Fatalf("v=%+v err=%v", v, err)
	}
	for _, want := range []string{"the goal", "asked", "deflected"} {
		if !strings.Contains(sentUser, want) {
			t.Fatalf("the judge did not see %q: %q", want, sentUser)
		}
	}
	if spent == 0 {
		t.Fatal("a judgment costs money and the meter must say so")
	}
}

// stubClaude writes a fake claude binary that echoes a canned result
// envelope, so the headless turner is tested against the real exec
// path without spending anyone's tokens.
func stubClaude(t *testing.T, script string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stub is a shell script")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "claude")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestHeadlessParsesTheEnvelope(t *testing.T) {
	bin := stubClaude(t, `echo '{"type":"result","subtype":"success","is_error":false,"result":"three hypotheses","session_id":"fork-1","total_cost_usd":0.042}'`)
	h := &Headless{Bin: bin, Dir: t.TempDir()}
	turn, err := h.RunTurn(context.Background(), "abc-123", "hypothesize")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if turn.Reply != "three hypotheses" || turn.SessionID != "fork-1" || turn.CostUSD != 0.042 {
		t.Fatalf("turn: %+v", turn)
	}
}

func TestHeadlessSurfacesAnErroredTurn(t *testing.T) {
	bin := stubClaude(t, `echo '{"type":"result","is_error":true,"result":"No conversation found with session ID"}'`)
	h := &Headless{Bin: bin, Dir: t.TempDir()}
	_, err := h.RunTurn(context.Background(), "abc-123", "hi")
	if err == nil || !strings.Contains(err.Error(), "No conversation found") {
		t.Fatalf("err=%v", err)
	}
}

func TestHeadlessSurfacesANonzeroExitWithStderr(t *testing.T) {
	bin := stubClaude(t, `echo "boom: something local" >&2; exit 1`)
	h := &Headless{Bin: bin, Dir: t.TempDir()}
	_, err := h.RunTurn(context.Background(), "abc-123", "hi")
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err=%v", err)
	}
}

func TestHeadlessRefusesAnUnsafeSessionID(t *testing.T) {
	h := &Headless{Bin: "claude-not-called"}
	if _, err := h.RunTurn(context.Background(), "id; rm -rf /", "hi"); err == nil {
		t.Fatal("an unsafe id must be refused before any exec")
	}
}

// scriptStarter births "born-1" then behaves like its embedded turner.
type scriptStarter struct{ scriptTurner }

func (s *scriptStarter) StartTurn(ctx context.Context, prompt string) (Turn, error) {
	s.sent = append(s.sent, prompt)
	return Turn{Reply: "first breath: " + prompt, SessionID: "born-1", CostUSD: 0.02}, nil
}

func TestRunFreshBirthsAnAgentAndContinues(t *testing.T) {
	tr := &scriptStarter{}
	j := &scriptJudge{verdicts: []Verdict{
		{Prompt: "push further"},
		{Done: true, Reason: "the newborn delivered"},
	}}
	res, err := (&Loop{Turner: tr, Judge: j}).RunFresh(context.Background(), "the goal")
	if err != nil || !res.Done {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if res.Root != "born-1" {
		t.Fatalf("the newborn's identity must be the root: %q", res.Root)
	}
	if len(res.Turns) != 2 || res.Turns[0].Reply != "first breath: the goal" {
		t.Fatalf("turns: %+v", res.Turns)
	}
	// Turn two resumed the newborn, not nothing.
	if tr.sent[1] != "push further" {
		t.Fatalf("sent: %v", tr.sent)
	}
	if res.CostUSD < 0.029 || res.CostUSD > 0.031 {
		t.Fatalf("cost: %v", res.CostUSD)
	}
}

func TestHeadlessStartTurnNeverResumes(t *testing.T) {
	bin := stubClaude(t, `case "$*" in *--resume*) echo "resume leaked" >&2; exit 1;; esac
echo '{"type":"result","result":"hello","session_id":"born-9","total_cost_usd":0.01}'`)
	h := &Headless{Bin: bin, Dir: t.TempDir()}
	turn, err := h.StartTurn(context.Background(), "hi")
	if err != nil || turn.SessionID != "born-9" {
		t.Fatalf("turn=%+v err=%v", turn, err)
	}
}

func TestParseVerdictEscalate(t *testing.T) {
	v, err := ParseVerdict("ESCALATE\nThe worker wants to force-push; the goal grants no such thing.")
	if err != nil || !v.Escalate || !strings.Contains(v.Reason, "force-push") {
		t.Fatalf("v=%+v err=%v", v, err)
	}
	if _, err = ParseVerdict("ESCALATE"); err == nil {
		t.Fatal("an escalation without a question must refuse")
	}
}

func TestLoopEscalatesOnVerdictWithTheAskOnRecord(t *testing.T) {
	tr := &scriptTurner{replies: []string{"May I delete the old migrations?"}}
	j := &scriptJudge{verdicts: []Verdict{{Escalate: true, Reason: "The worker wants to delete migrations — allowed?"}}}
	res, err := (&Loop{Turner: tr, Judge: j}).Run(context.Background(), "s", "clean up")
	if err != nil || res.Done || !res.Escalated {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if !strings.Contains(res.Ask, "delete migrations") || len(res.Turns) != 1 {
		t.Fatalf("res=%+v", res)
	}
}

func TestLoopCirclingGuardEscalates(t *testing.T) {
	// The judge keeps issuing the identical prompt: laps, not progress.
	tr := &scriptTurner{}
	j := &scriptJudge{verdicts: []Verdict{
		{Prompt: "try again exactly"}, {Prompt: "try again exactly"}, {Prompt: "try again exactly"},
	}}
	res, err := (&Loop{Turner: tr, Judge: j, MaxTurns: 6}).Run(context.Background(), "s", "goal")
	if err != nil || !res.Escalated || !strings.Contains(res.Reason, "circling") {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if len(res.Turns) > 2 {
		t.Fatalf("the guard must fire before the lap bills: %d turns", len(res.Turns))
	}
}

func TestLoopSpendCapEscalates(t *testing.T) {
	tr := &scriptTurner{} // $0.01 per turn
	j := &scriptJudge{verdicts: []Verdict{{Prompt: "a"}, {Prompt: "b"}, {Prompt: "c"}, {Prompt: "d"}}}
	res, err := (&Loop{Turner: tr, Judge: j, MaxTurns: 10, MaxUSD: 0.02}).Run(context.Background(), "s", "goal")
	if err != nil || !res.Escalated || !strings.Contains(res.Reason, "spend cap") {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if len(res.Turns) != 2 {
		t.Fatalf("the cap must stop at the money line: %d turns", len(res.Turns))
	}
}

func TestLoopOnTurnSeesEveryDecision(t *testing.T) {
	tr := &scriptTurner{}
	j := &scriptJudge{verdicts: []Verdict{{Prompt: "next"}, {Done: true, Reason: "met"}}}
	var seen []Verdict
	l := &Loop{Turner: tr, Judge: j, OnTurn: func(_ int, _ Exchange, v Verdict) { seen = append(seen, v) }}
	if _, err := l.Run(context.Background(), "s", "goal"); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 2 || seen[0].Prompt != "next" || !seen[1].Done {
		t.Fatalf("audit feed: %+v", seen)
	}
}

func TestContinueSeedsTheJudgeWithHistory(t *testing.T) {
	tr := &scriptTurner{}
	j := &scriptJudge{verdicts: []Verdict{{Done: true, Reason: "resolved by the owner's answer"}}}
	seed := []Exchange{{Prompt: "the goal", Reply: "which db?"}}
	res, err := (&Loop{Turner: tr, Judge: j}).Continue(context.Background(), "s", "the goal", "use postgres", seed)
	if err != nil || !res.Done || len(res.Turns) != 2 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if tr.sent[0] != "use postgres" {
		t.Fatalf("the owner's reply must be the next prompt: %v", tr.sent)
	}
	if len(j.seen[0]) != 2 {
		t.Fatalf("the judge must see seed + new turn: %d", len(j.seen[0]))
	}
}

func TestHeadlessCarriesTheToolPolicy(t *testing.T) {
	bin := stubClaude(t, `case "$*" in *"--allowedTools=Edit,Bash(go test:*)"*) ;; *) echo "policy missing: $*" >&2; exit 1;; esac
echo '{"type":"result","result":"ok","session_id":"s1"}'`)
	h := &Headless{Bin: bin, Dir: t.TempDir(), AllowedTools: []string{"Edit", "Bash(go test:*)"}}
	if _, err := h.RunTurn(context.Background(), "abc", "go"); err != nil {
		t.Fatalf("resume with policy: %v", err)
	}
	if _, err := h.StartTurn(context.Background(), "go"); err != nil {
		t.Fatalf("start with policy: %v", err)
	}
	// And absent by default.
	bin2 := stubClaude(t, `case "$*" in *--allowedTools*) echo "policy leaked" >&2; exit 1;; esac
echo '{"type":"result","result":"ok","session_id":"s1"}'`)
	h2 := &Headless{Bin: bin2, Dir: t.TempDir()}
	if _, err := h2.RunTurn(context.Background(), "abc", "go"); err != nil {
		t.Fatalf("default must carry no policy: %v", err)
	}
}

func TestRunFreshEscalatesOnTheFirstTurn(t *testing.T) {
	tr := &scriptStarter{}
	j := &scriptJudge{verdicts: []Verdict{{Escalate: true, Reason: "The newborn wants to delete README.md — allowed?"}}}
	res, err := (&Loop{Turner: tr, Judge: j}).RunFresh(context.Background(), "the goal")
	if err != nil || !res.Escalated || !strings.Contains(res.Ask, "README.md") {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if res.Root != "born-1" {
		t.Fatalf("the newborn must still be on the record: %q", res.Root)
	}
}
