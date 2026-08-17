package drive

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSalvagePlanTakesWhatCame(t *testing.T) {
	p := salvagePlan(
		"KIND: New\n" +
			"HOME: Life\n" +
			"NAME: Party-2026-08-28\n" +
			"CADENCE: once\n" +
			"DEADLINE: 2026-08-28\n" +
			"GOAL: Draft a menu, headcount checklist, and shopping list for the birthday party.\n" +
			"WHY: A one-off event with a hard date deserves its own workspace.\n")
	if p.Kind != "new" || p.Home != "life" || p.Name != "party-2026-08-28" {
		t.Fatalf("normalized shape: %+v", p)
	}
	if p.Cadence != "once" || p.Deadline != "2026-08-28" {
		t.Fatalf("cadence and deadline: %+v", p)
	}
	if !strings.HasPrefix(p.Goal, "Draft a menu") || p.Why == "" {
		t.Fatalf("goal and why survive: %+v", p)
	}

	// An unlabeled ramble contributes nothing; cadence defaults once.
	if q := salvagePlan("KIND: repo\nWHERE: /w/x\nGOAL: Continue.\nsome stray prose\n"); q.Kind != "repo" || q.Where != "/w/x" || q.Cadence != "once" {
		t.Fatalf("repo plan with defaulted cadence: %+v", q)
	}
	// A kind outside the vocabulary is not a kind.
	if q := salvagePlan("KIND: maybe\nGOAL: x\n"); q.Kind != "" {
		t.Fatalf("closed vocabulary holds: %+v", q)
	}
}

func TestPlanCarriesTheAskAndOffers(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{
				"content": "KIND: new\nHOME: code\nNAME: dirty-repos\nCADENCE: once\nGOAL: Build a CLI that lists git repos with uncommitted changes.\nWHY: A small fresh tool.",
			}}},
		})
	}))
	defer srv.Close()
	m := &LLM{Client: srv.Client(), Base: srv.URL, Name: "test-model"}
	p, err := m.Plan(context.Background(),
		"I need a little tool that tells me which of my git repos have uncommitted changes",
		[]string{"/w/go/src/thing"}, "2026-08-14")
	if err != nil {
		t.Fatal(err)
	}
	if p.Kind != "new" || p.Home != "code" || p.Name != "dirty-repos" {
		t.Fatalf("the plan comes back whole: %+v", p)
	}
	for _, want := range []string{"uncommitted changes", "/w/go/src/thing", "2026-08-14"} {
		if !strings.Contains(gotBody, want) {
			t.Fatalf("the wire request must carry %q: %s", want, gotBody)
		}
	}
}

func TestPlanRefusesAnUnusableAnswer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": "I think you should build something nice."}}},
		})
	}))
	defer srv.Close()
	m := &LLM{Client: srv.Client(), Base: srv.URL, Name: "test-model"}
	if _, err := m.Plan(context.Background(), "x", nil, "2026-08-14"); err == nil {
		t.Fatal("an answer with no KIND is unusable")
	}
	// KIND none needs no goal — the WHY is the whole answer.
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": "KIND: none\nCADENCE: once\nWHY: This needs a phone call, not a workspace."}}},
		})
	}))
	defer srv2.Close()
	m2 := &LLM{Client: srv2.Client(), Base: srv2.URL, Name: "test-model"}
	p, err := m2.Plan(context.Background(), "call my dentist", nil, "2026-08-14")
	if err != nil || p.Kind != "none" || p.Why == "" {
		t.Fatalf("a none plan stands on its why: %+v %v", p, err)
	}
}

func TestPlanAskIsAKind(t *testing.T) {
	p := salvagePlan("KIND: ask\nASK: Where does your personal site live — a repo, a host, a folder?\nWHY: I cannot see any site in the offered workspaces.\n")
	if p.Kind != "ask" || !strings.HasPrefix(p.Question, "Where does your personal site live") {
		t.Fatalf("the question survives: %+v", p)
	}
	// An ask without a question is unusable.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": "KIND: ask\nWHY: hm"}}},
		})
	}))
	defer srv.Close()
	m := &LLM{Client: srv.Client(), Base: srv.URL, Name: "test-model"}
	if _, err := m.Plan(context.Background(), "x", nil, "2026-08-14"); err == nil {
		t.Fatal("an ask with no question is unusable")
	}
}

func TestPlanNodesSalvageTheGraph(t *testing.T) {
	p := salvagePlan("KIND: new\nHOME: code\nNAME: pipeline\nCADENCE: once\n" +
		"GOAL: Build the collector.\n" +
		"NODE: implement | 1 | Build the summarizer over the collector's output.\n" +
		"NODE: review | 1, 2 | Read both pieces and report what is wrong.\n" +
		"NODE: verify | 2 | Run the tests.\n" +
		"WHY: several honest pieces.")
	if len(p.Nodes) != 3 {
		t.Fatalf("three nodes: %+v", p.Nodes)
	}
	if p.Nodes[0].Kind != "implement" || p.Nodes[0].Text != "Build the summarizer over the collector's output." {
		t.Fatalf("kind and instruction survive the split: %+v", p.Nodes[0])
	}
	// The shape a chain could not carry: one node waiting on two.
	if len(p.Nodes[1].Deps) != 2 || p.Nodes[1].Deps[0] != 1 || p.Nodes[1].Deps[1] != 2 {
		t.Fatalf("a node may wait on several: %+v", p.Nodes[1])
	}
	// Two nodes hanging off the same piece is what lets them run beside
	// each other — the whole reason the line became a graph.
	if len(p.Nodes[2].Deps) != 1 || p.Nodes[2].Deps[0] != 2 {
		t.Fatalf("verify waits on piece 2: %+v", p.Nodes[2])
	}
}

func TestPlanNodesSalvageTakesWhatCame(t *testing.T) {
	p := salvagePlan("KIND: repo\nWHERE: /tmp/x\nGOAL: Do it.\n" +
		"NODE: nonsense | - | A piece with a kind nobody defined.\n" +
		"NODE: review | not-a-number, 1 | Read it.\n" +
		"NODE: implement | 1 |   \n" +
		"NODE: missing the pipes entirely\n" +
		"NODE: verify | 1 | Run the tests.\n" +
		"NODE: review | 1 | One past the cap.\n" +
		"NODE: review | 1 | Two past the cap.\n")
	// An unusable kind is an implementation — what every piece was
	// before kinds existed — and a node with no instruction is nothing.
	if len(p.Nodes) != 4 {
		t.Fatalf("capped at four, empties and malformed lines dropped: %+v", p.Nodes)
	}
	if p.Nodes[0].Kind != "implement" {
		t.Fatalf("an unknown kind falls back to implement: %+v", p.Nodes[0])
	}
	if len(p.Nodes[1].Deps) != 1 || p.Nodes[1].Deps[0] != 1 {
		t.Fatalf("an unparseable dep is dropped, the rest survive: %+v", p.Nodes[1])
	}
}
