package mux

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// A snapshot in the shape statefeed.zig emits (schema 1).
const stateFixture = `{"rookMuxState":1,"epoch":"2f11a944","serial":6,"pid":50928,
"geometry":{"cols":103,"rows":42},
"focus":{"pane":4,"mode":"pane"},
"workspaces":[
 {"name":"main","current":false,"windows":[
   {"index":1,"name":"versions","current":true,"zoomed":false,"focus":1,"layout":{"pane":1}},
   {"index":2,"name":"zsh","current":false,"zoomed":false,"focus":3,"layout":{"split":"v","ratio":0.5,"a":{"pane":3},"b":{"pane":5}}}
 ],"pins":[]},
 {"name":"vera","current":true,"windows":[
   {"index":1,"name":"versions","current":true,"zoomed":false,"focus":4,"layout":{"pane":4}}
 ],"pins":[9]}
],
"panes":[
 {"id":1,"pid":10,"program":"versions","cwd":"/x/rook","cols":103,"rows":42,"rect":null,"focused":false,"visible":true,"wantsMouse":false,"exited":false,"lastOutputMs":1756050000000},
 {"id":3,"pid":11,"program":"zsh","cwd":"/x/rook","cols":51,"rows":42,"rect":null,"focused":false,"visible":false,"wantsMouse":false,"exited":false,"lastOutputMs":0},
 {"id":5,"pid":12,"program":"cat","cwd":"/x/rook","cols":51,"rows":42,"rect":null,"focused":false,"visible":false,"wantsMouse":false,"exited":true},
 {"id":4,"pid":13,"program":"claude","cwd":"/x/vera","cols":103,"rows":42,"rect":{"x":0,"y":1,"w":103,"h":41},"focused":true,"visible":true,"wantsMouse":false,"exited":false,"lastOutputMs":1756050001000},
 {"id":9,"pid":14,"program":"tail","cwd":"/x/vera","cols":30,"rows":42,"rect":null,"focused":false,"visible":true,"wantsMouse":false,"exited":false}
],
"pins":[{"pane":9,"scope":"vera"}],
"surfaces":[],"clients":[]}
`

func TestSnapshotPanes(t *testing.T) {
	var s snapshot
	if err := json.Unmarshal([]byte(stateFixture), &s); err != nil {
		t.Fatal(err)
	}
	panes, _ := s.panes()
	byID := map[string]Pane{}
	for _, p := range panes {
		byID[p.ID.Pane] = p
	}
	if len(byID) != 5 {
		t.Fatalf("got %d panes", len(byID))
	}
	if p := byID["1"]; p.ID != (ID{"main", "1", "1"}) || p.Command != "versions" || !p.Active.Equal(time.UnixMilli(1756050000000)) {
		t.Errorf("pane 1: %+v", p)
	}
	if p := byID["5"]; p.ID != (ID{"main", "2", "5"}) || !p.Dead {
		t.Errorf("pane 5 (split leaf, exited): %+v", p)
	}
	if p := byID["3"]; !p.Active.IsZero() {
		t.Errorf("lastOutputMs 0 should be zero time: %+v", p)
	}
	if p := byID["9"]; p.ID != (ID{"vera", "pin", "9"}) {
		t.Errorf("pin: %+v", p)
	}
	f, err := s.focus()
	if err != nil || f.ID != (ID{"vera", "1", "4"}) || f.Command != "claude" {
		t.Errorf("focus: %v %+v", err, f)
	}
	if id, ok := blockID(f.ID); !ok || id != 4 {
		t.Error("blockID")
	}
}

func TestSnapshotNoFocus(t *testing.T) {
	var s snapshot
	_ = json.Unmarshal([]byte(`{"rookMuxState":1,"focus":{"pane":0,"mode":"pane"},"workspaces":[],"panes":[]}`), &s)
	if _, err := s.focus(); err != ErrNoFocus {
		t.Error(err)
	}
}

// TestRookLive drives the engine the person is running. Opt-in: it
// creates a workspace in THEIR rook. With quiet spawn the view should
// stay put; the test checks that too.
func TestRookLive(t *testing.T) {
	if os.Getenv("VERA_ROOK_LIVE") == "" {
		t.Skip("set VERA_ROOK_LIVE=1 to run against the live rook")
	}
	r := NewRook("")
	ctx := context.Background()
	before, err := r.state(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if before.Version < 1 {
		t.Fatalf("server does not speak the state feed (rookMuxState=%d); rook kill to restart on the new build", before.Version)
	}
	focusBefore := before.Focus.Pane

	p, err := r.Spawn(ctx, Spawn{Session: "vera-test", Dir: t.TempDir(), Command: []string{"sh", "-c", "echo hello from vera; exec cat"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Kill(ctx, p.ID) })
	if p.ID.Session != "vera-test" {
		t.Fatalf("spawned into %+v", p.ID)
	}
	if after, _ := r.state(ctx); after.Focus.Pane != focusBefore {
		t.Errorf("quiet spawn moved focus: %d → %d", focusBefore, after.Focus.Pane)
	}

	time.Sleep(500 * time.Millisecond)
	lines, err := r.Capture(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(lines, "\n"), "hello from vera") {
		t.Fatalf("capture: %q", lines)
	}
	if err := r.Send(ctx, p.ID, "typed by vera"); err != nil {
		t.Fatal(err)
	}
	if err := r.Enter(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	lines, _ = r.Capture(ctx, p.ID)
	if strings.Count(strings.Join(lines, "\n"), "typed by vera") != 2 {
		t.Fatalf("cat should echo: %q", lines)
	}
	got, err := r.Get(ctx, p.ID)
	if err != nil || got.Active.IsZero() || time.Since(got.Active) > 10*time.Second {
		t.Errorf("activity stamp: %v %+v", err, got)
	}

	// A second spawn into the same workspace is a new window.
	p2, err := r.Spawn(ctx, Spawn{Session: "vera-test", Command: []string{"cat"}})
	if err != nil {
		t.Fatal(err)
	}
	if p2.ID.Session != "vera-test" || p2.ID.Pane == p.ID.Pane {
		t.Fatalf("second pane %+v", p2)
	}
	if err := r.Narrow(ctx, p2.ID, 40); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if s, _ := r.state(ctx); s != nil {
		for _, sp := range s.Panes {
			if sp.ID == mustID(p2.ID) && sp.Cols != 40 {
				t.Errorf("lease did not resize: %+v", sp)
			}
		}
	}
	if err := r.Widen(ctx, p2.ID); err != nil {
		t.Fatal(err)
	}
	if err := r.Kill(ctx, p2.ID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if g, err := r.Get(ctx, p2.ID); err == nil && !g.Dead {
		t.Errorf("after kill: %+v", g)
	}
	if _, err := r.Capture(ctx, ID{Pane: "999999"}); err != ErrNoPane {
		t.Errorf("capture of a missing pane: %v", err)
	}
}

func mustID(id ID) uint32 {
	n, _ := blockID(id)
	return n
}
