// Worlds: --world re-roots everything mutable under one disposable
// directory and scopes the board to sessions working inside it. The
// value of the sandbox is exactly these two properties — state that
// cannot leak out, and a view that cannot leak in.
package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/incantery/vera/transcript"
)

func TestWorldRerootsEveryMutablePath(t *testing.T) {
	old := worldRoot
	defer func() { worldRoot = old }()
	worldRoot = filepath.Join("/", "w1")

	for name, got := range map[string]string{
		"spend":    defaultSpendPath(),
		"digests":  defaultDigestPath(),
		"suggests": defaultSuggestPath(),
		"lineage":  defaultLineagePath(),
		"key":      defaultKeyPath(),
		"uploads":  defaultUploadsDir(),
		"tasks":    defaultTasksDir(),
		"shelf":    defaultArtifactsDir(),
	} {
		if !strings.HasPrefix(got, filepath.Join("/", "w1", "state")+string(filepath.Separator)) {
			t.Fatalf("%s must live under the world's state dir: %s", name, got)
		}
	}
	if got := defaultScratchParent(); got != filepath.Join("/", "w1", "scratch") {
		t.Fatalf("scratch lives in the world: %s", got)
	}

	// No world: nothing points into it.
	worldRoot = ""
	if p := defaultSpendPath(); strings.Contains(p, "w1") {
		t.Fatalf("the real machine must not inherit a world path: %s", p)
	}
}

func TestWorldScopesTheBoard(t *testing.T) {
	skip := skipOutsideWorld("/w1")
	if skip(&transcript.Session{Cwd: "/w1/go/tool"}) || skip(&transcript.Session{Cwd: "/w1"}) {
		t.Fatal("sessions working under the world stay")
	}
	// The prefix trap: a sibling directory sharing the world's prefix
	// is outside — /w1-other is not /w1/*.
	if !skip(&transcript.Session{Cwd: "/w1-other/repo"}) {
		t.Fatal("a same-prefix sibling is outside the world")
	}
	if !skip(&transcript.Session{Cwd: "/home/u/real-repo"}) {
		t.Fatal("the real machine is outside the world")
	}
}
