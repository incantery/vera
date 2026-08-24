package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := git(dir, args...)
	if err != nil {
		t.Fatalf("git %s: %s", strings.Join(args, " "), out)
	}
	return out
}

func newRepo(t *testing.T) Repo {
	t.Helper()
	parent := t.TempDir()
	root := filepath.Join(parent, "proj")
	os.MkdirAll(root, 0o755)
	gitRun(t, root, "init", "-q", "-b", "main")
	gitRun(t, root, "config", "user.email", "t@example.com")
	gitRun(t, root, "config", "user.name", "t")
	os.WriteFile(filepath.Join(root, "README"), []byte("hi\n"), 0o644)
	os.WriteFile(filepath.Join(root, ".env"), []byte("SECRET=1\n"), 0o644)
	os.WriteFile(filepath.Join(root, "rook.toml"), []byte("[worktree]\ncopy = [\".env\"]\n"), 0o644)
	os.WriteFile(filepath.Join(root, ".gitignore"), []byte(".env\n"), 0o644)
	gitRun(t, root, "add", "README", "rook.toml", ".gitignore")
	gitRun(t, root, "commit", "-q", "-m", "init")
	r, err := FindRepo(root)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestWorktreeLifecycle(t *testing.T) {
	r := newRepo(t)
	if r.DefaultBranch() != "main" {
		t.Fatalf("default branch %q", r.DefaultBranch())
	}
	conv := LoadConventions(r.Root)
	if len(conv.Copy) != 1 || conv.Copy[0] != ".env" {
		t.Fatalf("conventions %+v", conv)
	}
	wt, err := r.New("agent-a", "", conv)
	if err != nil {
		t.Fatal(err)
	}
	if wt.Branch != "agent-a" || wt.Path != r.Path("agent-a") || wt.Main {
		t.Fatalf("worktree %+v", wt)
	}
	if _, err := os.Stat(filepath.Join(wt.Path, ".env")); err != nil {
		t.Error(".env not copied")
	}
	if r.Session("agent-a") != "proj--agent-a" {
		t.Error(r.Session("agent-a"))
	}
	// FindRepo from inside the worktree answers with the main root.
	if r2, _ := FindRepo(wt.Path); r2.Root != r.Root {
		t.Errorf("FindRepo from worktree: %s", r2.Root)
	}

	// Work on it, commit, land it.
	os.WriteFile(filepath.Join(wt.Path, "new.txt"), []byte("x\n"), 0o644)
	if wt, _ = r.Get("agent-a"); !wt.Dirty {
		t.Error("should be dirty")
	}
	if err := r.Merge("agent-a"); err == nil {
		t.Error("merge of a dirty worktree should refuse")
	}
	gitRun(t, wt.Path, "add", "new.txt")
	gitRun(t, wt.Path, "commit", "-q", "-m", "work")
	if wt, _ = r.Get("agent-a"); wt.Ahead != 1 || wt.Dirty {
		t.Errorf("ahead=%d dirty=%v", wt.Ahead, wt.Dirty)
	}
	if err := r.Merge("agent-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(r.Root, "new.txt")); err != nil {
		t.Error("merge did not land new.txt")
	}
	if _, err := r.Get("agent-a"); err == nil {
		t.Error("worktree should be gone after merge")
	}
	if _, err := git(r.Root, "rev-parse", "--verify", "--quiet", "refs/heads/agent-a"); err == nil {
		t.Error("branch should be gone after merge")
	}
}

func TestWorktreeRemoveRefusesUnmerged(t *testing.T) {
	r := newRepo(t)
	wt, err := r.New("b", "", Conventions{})
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(wt.Path, "f"), []byte("x"), 0o644)
	gitRun(t, wt.Path, "add", "f")
	gitRun(t, wt.Path, "commit", "-q", "-m", "unmerged")
	wt, _ = r.Get("b")
	if err := r.Remove(wt, false); err == nil {
		t.Fatal("should refuse to drop unmerged commits")
	}
	if err := r.Remove(wt, true); err != nil {
		t.Fatal(err)
	}
}

func TestWorktreeBadNames(t *testing.T) {
	r := newRepo(t)
	for _, n := range []string{"", "../x", "a b", "-lead", ".hidden"} {
		if _, err := r.New(n, "", Conventions{}); err == nil {
			t.Errorf("%q accepted", n)
		}
	}
}

func TestTomlStrings(t *testing.T) {
	got := tomlStrings(`[".env", 'config/local.toml', node_modules]`)
	want := []string{".env", "config/local.toml", "node_modules"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("got %v", got)
	}
}
