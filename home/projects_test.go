package home

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAProjectFileIsMadeOnceAndAppendedTo(t *testing.T) {
	h := fresh(t)
	if err := h.Project("rook", "/src/rook", "main", []string{"checks before landing: `zig build test`"}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(h.Root, ProjectsDir, "rook.md")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# rook", "/src/rook", "default branch: `main`", "zig build test"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("the project file does not carry %q:\n%s", want, b)
		}
	}

	// A person editing "what this repo is" is the point, so a second
	// task must not write over them.
	os.WriteFile(path, []byte("# rook\n\n/src/rook — mine, hands off\n\n## What Vera has done here\n\n"), 0o600)
	if err := h.Project("rook", "/src/rook", "main", nil); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(path)
	if !strings.Contains(string(b), "hands off") {
		t.Fatalf("the second spawn rewrote the file:\n%s", b)
	}

	if err := h.Landed("rook", "/src/rook", "vera-73aeb6cf", "Vera's home: memory as files\nand a second line nobody needs"); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(path)
	want := "- " + time.Now().Format("2006-01-02") + " landed vera-73aeb6cf: Vera's home: memory as files"
	if !strings.Contains(string(b), want) {
		t.Fatalf("the landing was not recorded (%q):\n%s", want, b)
	}
	if strings.Contains(string(b), "second line") {
		t.Error("the whole brief went in; one line is the record")
	}
	// The supervisor can try a landing twice; the line should not
	// double.
	h.Landed("rook", "/src/rook", "vera-73aeb6cf", "Vera's home: memory as files\nand a second line nobody needs")
	b, _ = os.ReadFile(path)
	if strings.Count(string(b), "vera-73aeb6cf") != 1 {
		t.Errorf("the landing was recorded twice:\n%s", b)
	}
}

// Two checkouts share a basename often enough — "api" in two orgs —
// and quietly writing one repo's history into the other's file would
// be worse than an ugly name.
func TestTwoReposWithTheSameNameDoNotShareAFile(t *testing.T) {
	h := fresh(t)
	if err := h.Project("api", "/src/one/api", "main", nil); err != nil {
		t.Fatal(err)
	}
	if err := h.Project("api", "/src/two/api", "main", nil); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(filepath.Join(h.Root, ProjectsDir, "api.md"))
	second, err := os.ReadFile(filepath.Join(h.Root, ProjectsDir, "api-2.md"))
	if err != nil {
		t.Fatalf("the second repo had nowhere to go: %v", err)
	}
	if !strings.Contains(string(first), "/src/one/api") || !strings.Contains(string(second), "/src/two/api") {
		t.Errorf("the two repos were mixed up:\n%s\n---\n%s", first, second)
	}
	h.Landed("api", "/src/two/api", "vera-1", "the second one")
	first, _ = os.ReadFile(filepath.Join(h.Root, ProjectsDir, "api.md"))
	if strings.Contains(string(first), "vera-1") {
		t.Error("a landing in one repo was written into the other's file")
	}
}
