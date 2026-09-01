package main

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/incantery/mote/tool"
	"github.com/incantery/vera/events"
	"github.com/incantery/vera/fleet"
)

// serveEvents starts a transport with a stream behind it and hands
// back the base URL, the secret, and the stream.
func serveEvents(t *testing.T) (string, Identity, *eventStream) {
	t.Helper()
	stream := newEventStream(filepath.Join(t.TempDir(), "events"))
	base, id := serveLANWith(t, echo, func(l *lanTransport) { l.events = stream })
	return base, id, stream
}

func get(t *testing.T, url, secret string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Body.Close() })
	return res
}

func TestEventsNeedsTheSecretToRead(t *testing.T) {
	base, _, _ := serveEvents(t)
	if res := get(t, base+"/events", ""); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401 without the secret, got %d", res.StatusCode)
	}
}

func TestEventsPublishThenRead(t *testing.T) {
	base, id, _ := serveEvents(t)
	body := `{"source":"rook","kind":"rook.workspace","text":"opened the workspace scratch","repo":"rook"}`
	res, err := http.Post(base+"/events", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("publish: %d", res.StatusCode)
	}
	var count map[string]int
	json.NewDecoder(res.Body).Decode(&count)
	if count["recorded"] != 1 {
		t.Fatalf("want one recorded, got %+v", count)
	}

	var got []events.Event
	read := get(t, base+"/events?repo=rook", id.Secret)
	json.NewDecoder(read.Body).Decode(&got)
	if len(got) != 1 || got[0].Text != "opened the workspace scratch" || got[0].Source != "rook" {
		t.Fatalf("want the published event back, got %+v", got)
	}
	if got[0].At.IsZero() {
		t.Fatal("want a publisher that said no time to be stamped on arrival")
	}
}

func TestEventsPublishTakesABatchAndRefusesNonsense(t *testing.T) {
	base, id, _ := serveEvents(t)
	batch := `{"source":"rook","kind":"rook.a","text":"one"}
{"source":"rook","kind":"rook.b","text":"two"}`
	res, err := http.Post(base+"/events", "application/json", strings.NewReader(batch))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("batch: %d", res.StatusCode)
	}
	var got []events.Event
	read := get(t, base+"/events", id.Secret)
	json.NewDecoder(read.Body).Decode(&got)
	if len(got) != 2 {
		t.Fatalf("want both, got %+v", got)
	}

	for _, bad := range []string{`{"kind":"k","text":"t"}`, `{"source":"s","text":"t"}`, `not json`, ``} {
		res, err := http.Post(base+"/events", "application/json", strings.NewReader(bad))
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusBadRequest {
			t.Fatalf("want %q refused with a reason, got %d", bad, res.StatusCode)
		}
	}
}

// A publisher does not get to write the stream's history: a time from
// the future is the clock on this machine instead.
func TestEventsPublishWillNotBackdateFromTheFuture(t *testing.T) {
	base, id, _ := serveEvents(t)
	body := `{"source":"rook","kind":"rook.a","text":"from tomorrow","at":"2099-01-01T00:00:00Z"}`
	res, err := http.Post(base+"/events", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	var got []events.Event
	read := get(t, base+"/events", id.Secret)
	json.NewDecoder(read.Body).Decode(&got)
	if len(got) != 1 || got[0].At.After(time.Now().Add(time.Minute)) {
		t.Fatalf("want the arrival time, got %+v", got)
	}
}

func TestEventsFiltersAndFormats(t *testing.T) {
	base, id, stream := serveEvents(t)
	stream.Rec.Record(
		events.Event{At: time.Now().Add(-time.Hour), Repo: "rook", Source: "git", Kind: "git.commit", Text: "tabs: the ordinal sits (abc1234)"},
		events.Event{At: time.Now().Add(-time.Minute), Repo: "vera", Source: "fleet", Kind: "task.finished", Task: "T-1", Text: "alpha said it is done"},
		events.Event{At: time.Now().Add(-40 * 24 * time.Hour), Repo: "vera", Source: "git", Kind: "git.commit", Text: "ancient (0000000)"},
	)
	for _, c := range []struct {
		query string
		want  int
	}{
		{"", 2},                    // the default window is a day
		{"?since=90d", 3},          // …and it can be widened
		{"?since=all", 3},          //    or dropped
		{"?repo=rook&since=7d", 1}, //
		{"?kind=task.&since=7d", 1},
		{"?task=T-1&since=7d", 1},
		{"?q=ordinal&since=7d", 1},
		{"?limit=1&since=7d", 1},
	} {
		var got []events.Event
		res := get(t, base+"/events"+c.query, id.Secret)
		if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
			t.Fatalf("%q: %v", c.query, err)
		}
		if len(got) != c.want {
			t.Fatalf("%q: want %d, got %d (%+v)", c.query, c.want, len(got), got)
		}
	}

	res := get(t, base+"/events?format=text&since=7d", id.Secret)
	buf := make([]byte, 4096)
	n, _ := res.Body.Read(buf)
	text := string(buf[:n])
	for _, want := range []string{"what happened", "rook", "vera", "alpha said it is done"} {
		if !strings.Contains(text, want) {
			t.Fatalf("want %q in the text digest:\n%s", want, text)
		}
	}

	if res := get(t, base+"/events?since=soon", id.Secret); res.StatusCode != http.StatusBadRequest {
		t.Fatalf("want a window nobody can parse refused, got %d", res.StatusCode)
	}
}

func TestFleetEventsSaysEachKindOnce(t *testing.T) {
	task := &fleet.Task{ID: "T-1", Name: "alpha", Project: "/src/vera", Brief: "wire it up", Kind: fleet.Ship, Mode: fleet.LocalOnly}
	now := time.Now()
	cases := []struct {
		name string
		in   fleet.Event
		kind string
		text string
	}{
		{"spawned", fleet.Event{Kind: fleet.TaskSpawned, Task: task, At: now}, "task.spawned", "wire it up"},
		{"landed", fleet.Event{Kind: fleet.TaskLanded, Task: task, At: now}, "task.landed", "merged"},
		{"failed", fleet.Event{Kind: fleet.LandFailed, Task: task, Err: "conflicts", At: now}, "task.land-failed", "conflicts"},
		{"said", fleet.Event{Kind: fleet.TaskSaid, Task: task, Said: &fleet.Status{Verb: fleet.Blocked, Text: "which database?", By: "agent"}, At: now}, "task.said", "which database?"},
		{"state", fleet.Event{Kind: fleet.StateChanged, Task: task, State: fleet.Decision, At: now}, "task.decision", "blocked on a decision"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := fleetEvents(c.in)
			if len(got) != 1 {
				t.Fatalf("want one event, got %+v", got)
			}
			if got[0].Kind != c.kind || !strings.Contains(got[0].Text, c.text) {
				t.Fatalf("want %s carrying %q, got %+v", c.kind, c.text, got[0])
			}
			if got[0].Task != "T-1" || got[0].Project != "/src/vera" {
				t.Fatalf("want the task's keys, got %+v", got[0])
			}
		})
	}

	// The weather is not news, and neither is an event about no task.
	for _, quiet := range []fleet.Event{
		{Kind: fleet.StateChanged, Task: task, State: fleet.Running, At: now},
		{Kind: fleet.TaskSaid, Task: task, At: now},
		{Kind: fleet.TaskSpawned, At: now},
	} {
		if got := fleetEvents(quiet); len(got) != 0 {
			t.Fatalf("want silence for %+v, got %+v", quiet, got)
		}
	}
}

func TestRepoOfNamesAWorktreeAfterItsCheckout(t *testing.T) {
	s := newEventStream(t.TempDir())
	if got := s.repoOf(""); got != "" {
		t.Fatalf("want nothing for no path, got %q", got)
	}
	// Not a checkout: the directory's own name is the honest answer.
	dir := t.TempDir()
	if got := s.repoOf(dir); got != filepath.Base(dir) {
		t.Fatalf("want %q, got %q", filepath.Base(dir), got)
	}
}

func TestRecentToolReadsTheRecord(t *testing.T) {
	dir := t.TempDir()
	l := &events.Log{Dir: dir}
	now := time.Now()
	err := l.Append(
		events.Event{At: now.Add(-time.Hour), Repo: "rook", Source: "git", Kind: "git.commit", Text: "tabs: the ordinal sits (abc1234)"},
		events.Event{At: now.Add(-time.Minute), Repo: "vera", Source: "fleet", Kind: "task.decision", Task: "T-1", Text: "alpha is blocked on a decision"},
		events.Event{At: now.Add(-40 * 24 * time.Hour), Repo: "vera", Source: "git", Kind: "git.commit", Text: "ancient (0000000)"},
	)
	if err != nil {
		t.Fatal(err)
	}
	tl := &RecentTool{Dir: dir}
	ctx := context.Background()

	res, err := tl.Run(ctx, nil, tool.Handle{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "alpha is blocked") || !strings.Contains(res.Text, "tabs: the ordinal") {
		t.Fatalf("want the week, got:\n%s", res.Text)
	}
	if strings.Contains(res.Text, "ancient") {
		t.Fatalf("want the default window to be a week, got:\n%s", res.Text)
	}

	res, err = tl.Run(ctx, json.RawMessage(`{"repo":"rook"}`), tool.Handle{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Text, "alpha is blocked") {
		t.Fatalf("want only rook, got:\n%s", res.Text)
	}

	res, err = tl.Run(ctx, json.RawMessage(`{"since":"all"}`), tool.Handle{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "ancient") {
		t.Fatalf("want all of it, got:\n%s", res.Text)
	}

	if _, err := tl.Run(ctx, json.RawMessage(`{"since":"soon"}`), tool.Handle{}); err == nil {
		t.Fatal("want a window nobody can parse refused")
	}
	if _, err := (&RecentTool{}).Run(ctx, nil, tool.Handle{}); err == nil {
		t.Fatal("want a machine with no record to say so")
	}
	if tl.Scope(nil) != "read" || tl.Name() != "recent" {
		t.Fatalf("want a read-only tool called recent, got %q/%q", tl.Name(), tl.Scope(nil))
	}
}

// The description has to keep the model off the fleet, which is the
// tool it will otherwise reach for: the two read alike and are
// opposite in tense.
func TestRecentToolSaysItIsNotTheFleet(t *testing.T) {
	d := (&RecentTool{}).Description()
	for _, want := range []string{"fleet", "HISTORY", "already happened"} {
		if !strings.Contains(d, want) {
			t.Fatalf("want %q in the description", want)
		}
	}
	var schema map[string]any
	if err := json.Unmarshal((&RecentTool{}).Schema(), &schema); err != nil {
		t.Fatalf("the schema must be valid JSON: %v", err)
	}
}

func TestRecentToolIsAllowedWithoutAsking(t *testing.T) {
	got := policyTools(nil)
	if got["recent"] != tool.Allow {
		t.Fatalf("want reading the record to run without asking, got %v", got["recent"])
	}
	// And a profile that says otherwise still wins.
	said := policyTools(map[string]tool.Decision{"recent": tool.Ask})
	if said["recent"] != tool.Ask {
		t.Fatal("want the profile's own word to stand")
	}
}
