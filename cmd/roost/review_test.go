package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// reviewRepo builds a repo with one commit, then one tracked edit and
// one untracked file — the smallest tree with both kinds of change.
func reviewRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		if out, err := reviewGit(dir, args...); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	git("init", "-q")
	git("config", "user.name", "reviewer")
	git("config", "user.email", "reviewer@test")
	if err := os.WriteFile(filepath.Join(dir, "kept.txt"), []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	git("commit", "-q", "-m", "base")
	if err := os.WriteFile(filepath.Join(dir, "kept.txt"), []byte("one\nchanged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fresh.txt"), []byte("a\nb\nc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestReviewChangesTellsTheWholeStory(t *testing.T) {
	dir := reviewRepo(t)
	files, err := reviewChanges(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("two changed files, got %d", len(files))
	}
	byPath := map[string]reviewFile{}
	for _, f := range files {
		byPath[f.Path] = f
	}
	kept := byPath["kept.txt"]
	if kept.Add != 1 || kept.Del != 1 || kept.New {
		t.Fatalf("kept.txt should read +1 −1 tracked: %+v", kept)
	}
	if !strings.Contains(kept.Diff, "-two") || !strings.Contains(kept.Diff, "+changed") {
		t.Fatalf("kept.txt diff must carry the lines: %q", kept.Diff)
	}
	fresh := byPath["fresh.txt"]
	if !fresh.New || fresh.Add != 3 {
		t.Fatalf("fresh.txt should read new +3: %+v", fresh)
	}
	if !strings.Contains(fresh.Diff, "+a") || !strings.Contains(fresh.Diff, "+c") {
		t.Fatalf("fresh.txt diff must carry its lines: %q", fresh.Diff)
	}
}

// An agent living in a subdirectory still reviews the whole repo —
// `git add -A` commits the whole repo, so the review must show it.
func TestReviewFromASubdirectoryCoversTheWholeRepo(t *testing.T) {
	dir := reviewRepo(t)
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	files, err := reviewChanges(sub)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("the root's two changes show from the subdir: %+v", files)
	}
	for _, f := range files {
		if f.Diff == "" {
			t.Fatalf("%s must carry its diff from the subdir too", f.Path)
		}
	}
	if err := reviewDiscard(sub, "fresh.txt", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "fresh.txt")); !os.IsNotExist(err) {
		t.Fatal("a root-relative discard works from the subdir")
	}
}

func TestReviewChangesRefusesANonRepo(t *testing.T) {
	if _, err := reviewChanges(t.TempDir()); err == nil {
		t.Fatal("a plain directory is not reviewable")
	}
}

func TestReviewCommitIsTheApproveVerdict(t *testing.T) {
	dir := reviewRepo(t)
	if _, err := reviewCommit(dir, ""); err == nil {
		t.Fatal("an empty message must refuse")
	}
	hash, err := reviewCommit(dir, "approved: the change")
	if err != nil {
		t.Fatal(err)
	}
	if hash == "" {
		t.Fatal("the new commit's hash comes back")
	}
	files, err := reviewChanges(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatalf("after approve the tree is clean: %+v", files)
	}
}

func TestReviewDiscardPutsOneFileBack(t *testing.T) {
	dir := reviewRepo(t)
	if err := reviewDiscard(dir, "kept.txt", false); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "kept.txt"))
	if string(b) != "one\ntwo\n" {
		t.Fatalf("kept.txt must be HEAD's again: %q", b)
	}
	if err := reviewDiscard(dir, "fresh.txt", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "fresh.txt")); !os.IsNotExist(err) {
		t.Fatal("an untracked discard deletes the file")
	}
	if err := reviewDiscard(dir, "kept.txt", false); err == nil {
		t.Fatal("a clean file has nothing to discard")
	}
}

func TestReviewDiscardAllResetsTheTree(t *testing.T) {
	dir := reviewRepo(t)
	if err := reviewDiscard(dir, "", true); err != nil {
		t.Fatal(err)
	}
	files, err := reviewChanges(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatalf("discard-all leaves a clean tree: %+v", files)
	}
}
