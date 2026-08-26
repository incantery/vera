// The eval suite: the promises this code makes, held to.
//
// Every case checks something already claimed elsewhere. The voice
// prompt says brief, plain, no markdown. history.go says a conversation
// remembers its own turns and that conversations do not leak into each
// other. Those are assertions rather than preferences, and until now
// nothing would have noticed them breaking.
//
// The scorers here are DELIBERATELY deterministic. A judge model would
// grade "was that a good answer", which is the interesting question and
// the wrong one to start with: it is expensive, it is itself a thing
// that can drift, and it would obscure the plain regressions — markdown
// leaking back in, a history window off by one — that cost nothing to
// catch. Judged scoring is worth adding once these never fail by
// accident.
//
// Trials run through the REAL handler: the real model, the real history,
// the real export path. A mocked eval would only ever prove that the
// mock still matches the mock.
package main

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/grafana/agento11y/go/agento11y/experiments"
)

// A turn is one thing said. Turns of a case share a conversation unless
// the case says otherwise — which is how recall and isolation are told
// apart by the same runner.
type evalTurn struct {
	Say             string `yaml:"say"`
	NewConversation bool   `yaml:"new_conversation"`
	// Remembering happens behind the reply, so a case that teaches
	// something and immediately asks about it would be racing the
	// extractor. This waits for it — the one place an eval is allowed
	// to know that learning is asynchronous.
	AfterLearning bool `yaml:"after_learning"`
}

type evalInput struct {
	Turns []evalTurn `yaml:"turns"`
}

type evalExpect struct {
	Contains string `yaml:"contains"`
	Absent   string `yaml:"absent"`
}

// runEvals plays the suite through the handler and publishes the run.
func runEvals(ctx context.Context, path string, answer Handler, settle func(), publish bool) error {
	suite, err := experiments.LoadSuite(path)
	if err != nil {
		return err
	}

	var client *experiments.Client
	if publish {
		client, err = experiments.NewClientFromEnv()
		if err != nil {
			return err
		}
		defer client.Shutdown(context.Background())
	}

	planned := len(suite.TestCases)
	fmt.Printf("%s — %d cases\n\n", suite.Name, planned)

	var failures int
	record := func(ctx context.Context, trial *experiments.Trial, c experiments.TestCase) error {
		said, answered, spent, err := playCase(ctx, c, answer, settle)
		if err != nil {
			return err
		}
		checks := judge(c, answered)

		// Unlike the core client, a nil *Trial is not safe to call —
		// the local path passes one, so every use is guarded.
		if trial != nil {
			trial.RecordIO(experiments.RecordIOOptions{
				Input:        said,
				Output:       answered,
				AgentName:    serviceName,
				ModelName:    os.Getenv("VERA2_EVAL_MODEL"),
				InputTokens:  &spent.in,
				OutputTokens: &spent.out,
			})
		}

		passed := true
		for _, ch := range checks {
			if !ch.ok {
				passed = false
			}
			if trial != nil {
				// The explanation is phrased as the reason a check
				// FAILED, so attaching it to a pass would store a
				// sentence that is not true ("the reply was empty" on
				// a reply that was not).
				opts := experiments.ScoreOptions{}
				if !ch.ok {
					opts.Explanation = ch.why
				}
				if _, err := trial.CheckScore(ch.name, ch.ok, opts); err != nil {
					return err
				}
			}
		}
		if !passed {
			failures++
		}

		mark := "ok  "
		if !passed {
			mark = "FAIL"
		}
		fmt.Printf("%s  %-30s %s\n", mark, c.TestCaseID, summarize(checks))
		for _, ch := range checks {
			if !ch.ok {
				fmt.Printf("        %s: %s\n", ch.name, ch.why)
			}
		}

		if trial != nil {
			score := 1.0
			if !passed {
				score = 0
			}
			if _, err := trial.FinalScore(score, experiments.ScoreOptions{Passed: &passed}); err != nil {
				return err
			}
			if _, err := trial.Flush(ctx); err != nil {
				return err
			}
		}
		return nil
	}

	if client == nil {
		// Local run: same cases, same scorers, nothing published. This
		// is the mode that works on a plane, and the one a test can call.
		for _, c := range suite.TestCases {
			if err := record(ctx, nil, c); err != nil {
				return err
			}
		}
	} else {
		_, err = experiments.WithExperiment(ctx, client, experiments.ExperimentOptions{
			Name:              suite.Name,
			Suite:             suite,
			PlannedTrialCount: &planned,
			Candidate: &experiments.Candidate{
				AgentName: serviceName,
				ModelName: os.Getenv("VERA2_EVAL_MODEL"),
			},
		}, func(ctx context.Context, run *experiments.Experiment) error {
			for _, c := range suite.TestCases {
				if err := run.WithTrial(ctx, c, func(ctx context.Context, trial *experiments.Trial) error {
					return record(ctx, trial, c)
				}); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}

	fmt.Printf("\n%d/%d passed\n", planned-failures, planned)
	if failures > 0 {
		return fmt.Errorf("%d case(s) failed", failures)
	}
	return nil
}

// playCase runs the turns and returns what was said and the LAST answer,
// which is the one every expectation is about — the earlier turns exist
// to set up the state the last one is testing.
func playCase(ctx context.Context, c experiments.TestCase, answer Handler, settle func()) (said, answered string, spent usageSink, err error) {
	in, err := decodeInput(c.Input)
	if err != nil {
		return "", "", spent, err
	}
	if len(in.Turns) == 0 {
		return "", "", spent, fmt.Errorf("case %s has no turns", c.TestCaseID)
	}

	// A case is several model calls; what it cost is all of them. Token
	// counts live inside the handler, so the sink rides in on the
	// context rather than being bolted onto the wire.
	ctx, sink := withUsageSink(ctx)

	// A conversation id unique to this run, so a re-run is not read as
	// a continuation of the previous one.
	base := fmt.Sprintf("eval-%s-%d", c.TestCaseID, time.Now().UnixNano())
	conversation := base

	var saidAll []string
	for i, turn := range in.Turns {
		// Nothing runs behind a reply any more: memory is written
		// inside the exchange that decided to write it, so a turn that
		// waits for learning is already past it. The flag stays for
		// the day something does.
		if turn.AfterLearning && settle != nil {
			settle()
		}
		if turn.NewConversation {
			conversation = fmt.Sprintf("%s-b%d", base, i)
		}
		var reply strings.Builder
		err := answer(ctx, Message{Text: turn.Say, Conversation: conversation},
			func(f Frame) error {
				reply.WriteString(f.Delta)
				if f.Error != "" {
					return fmt.Errorf("%s", f.Error)
				}
				return nil
			})
		if err != nil {
			return "", "", *sink, err
		}
		saidAll = append(saidAll, turn.Say)
		answered = reply.String()
	}
	return strings.Join(saidAll, " / "), answered, *sink, nil
}

// MARK: - The scorers

type check struct {
	name string
	ok   bool
	why  string
}

var (
	// Bullets, numbered lists, headings, bold, and fenced code — the
	// shapes a voice answer should never have.
	markdownish = regexp.MustCompile(`(?m)^\s*([-*+]\s|\d+\.\s|#{1,6}\s)|\*\*|` + "```")
)

const briefWords = 90

func judge(c experiments.TestCase, answered string) []check {
	out := []check{
		{"answered", strings.TrimSpace(answered) != "", "the reply was empty"},
		{"no_markdown", !markdownish.MatchString(answered),
			"the reply used markdown, which does not survive being spoken"},
	}

	words := len(strings.Fields(answered))
	out = append(out, check{"brief", words <= briefWords,
		fmt.Sprintf("%d words; the voice prompt promises a sentence or two (limit %d)", words, briefWords)})

	if exp, err := decodeExpect(c.Expected); err == nil {
		low := strings.ToLower(answered)
		if exp.Contains != "" {
			out = append(out, check{"contains", strings.Contains(low, strings.ToLower(exp.Contains)),
				fmt.Sprintf("expected the reply to mention %q", exp.Contains)})
		}
		if exp.Absent != "" {
			out = append(out, check{"absent", !strings.Contains(low, strings.ToLower(exp.Absent)),
				fmt.Sprintf("the reply knew %q, which this conversation was never told", exp.Absent)})
		}
	}
	return out
}

func summarize(checks []check) string {
	var parts []string
	for _, c := range checks {
		mark := "·"
		if !c.ok {
			mark = "×"
		}
		parts = append(parts, mark+c.name)
	}
	return strings.Join(parts, " ")
}

// MARK: - Reading the suite

// The YAML lands as plain maps. Decoding them by hand rather than
// round-tripping through a YAML library keeps this file free of a
// dependency it would otherwise need for six fields.

func decodeInput(raw any) (evalInput, error) {
	m, ok := raw.(map[string]any)
	if !ok {
		return evalInput{}, fmt.Errorf("input is %T, want a mapping with turns", raw)
	}
	list, ok := m["turns"].([]any)
	if !ok {
		return evalInput{}, fmt.Errorf("input has no turns")
	}
	var in evalInput
	for _, item := range list {
		turn, ok := item.(map[string]any)
		if !ok {
			return evalInput{}, fmt.Errorf("a turn is %T, want a mapping", item)
		}
		say, _ := turn["say"].(string)
		fresh, _ := turn["new_conversation"].(bool)
		learned, _ := turn["after_learning"].(bool)
		in.Turns = append(in.Turns, evalTurn{Say: say, NewConversation: fresh, AfterLearning: learned})
	}
	return in, nil
}

func decodeExpect(raw any) (evalExpect, error) {
	m, ok := raw.(map[string]any)
	if !ok {
		return evalExpect{}, fmt.Errorf("no expectations")
	}
	var e evalExpect
	e.Contains, _ = m["contains"].(string)
	e.Absent, _ = m["absent"].(string)
	return e, nil
}

// MARK: - Counting what a case cost

// The Handler signature is deliberately about a conversation, not about
// tokens, and it should stay that way — so the eval runner asks for the
// count through the context instead of widening the interface or
// putting usage on the wire the phone reads.

type usageKeyType struct{}

type usageSink struct{ in, out int }

func withUsageSink(ctx context.Context) (context.Context, *usageSink) {
	sink := &usageSink{}
	return context.WithValue(ctx, usageKeyType{}, sink), sink
}

// spend is called by the mind once per model call. Cases run one at a
// time, so no locking; a concurrent runner would need some.
func spend(ctx context.Context, in, out int) {
	if sink, ok := ctx.Value(usageKeyType{}).(*usageSink); ok {
		sink.in += in
		sink.out += out
	}
}
