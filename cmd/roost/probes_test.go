package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Every condition must hold before a file dies: small, settled, home,
// /usage, untitled. Each near-miss survives.
func TestPruneProbesDeletesOnlyTrueProbes(t *testing.T) {
	dir := t.TempDir()
	home := "/Users/someone"
	proj := filepath.Join(dir, "-Users-someone")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)

	probeLines := `{"type":"queue-operation","operation":"enqueue","content":"/usage"}
{"type":"user","cwd":"/Users/someone","message":{"role":"user","content":"caveat"}}
`
	write := func(name, body string, mtime time.Time) string {
		t.Helper()
		p := filepath.Join(proj, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mtime, mtime); err != nil {
			t.Fatal(err)
		}
		return p
	}

	probe := write("probe.jsonl", probeLines, old)
	fresh := write("fresh-probe.jsonl", probeLines, time.Now())
	titled := write("titled.jsonl",
		strings.Replace(probeLines, `"cwd":"/Users/someone"`, `"cwd":"/Users/someone","aiTitle":"a real chat"`, 1), old)
	elsewhere := write("elsewhere.jsonl",
		strings.ReplaceAll(probeLines, "/Users/someone", "/repo/alpha"), old)
	noUsage := write("chat.jsonl",
		strings.Replace(probeLines, `"content":"/usage"`, `"content":"hello"`, 1), old)

	if n := pruneProbes(dir, home, time.Now()); n != 1 {
		t.Fatalf("exactly the probe: %d", n)
	}
	if _, err := os.Stat(probe); !os.IsNotExist(err) {
		t.Fatal("the probe must be gone")
	}
	for _, p := range []string{fresh, titled, elsewhere, noUsage} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s must survive", filepath.Base(p))
		}
	}
}
