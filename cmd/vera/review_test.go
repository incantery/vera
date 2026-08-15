package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	verav1 "github.com/incantery/vera/gen/vera/v1"
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

// The typed rail end to end: Review reads the repo, Discard deletes
// the untracked file, Commit approves the rest, and the next Review
// honestly reports a clean tree. Refusals arrive in Connect's codes.
func TestReviewRPCReadsCommitsAndDiscards(t *testing.T) {
	repo := reviewRepo(t)
	dir := t.TempDir()
	proj := filepath.Join(dir, "-repo-rev")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	line := fmt.Sprintf(
		`{"type":"assistant","timestamp":%q,"cwd":%q,"message":{"role":"assistant","stop_reason":"end_turn","content":[{"type":"text","text":"done"}]}}`,
		time.Now().Add(-time.Minute).UTC().Format(time.RFC3339), repo)
	if err := os.WriteFile(filepath.Join(proj, "sess-rev.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := testServer(t, dir)
	s.hub = newHub()
	r := &veraRPC{s: s}
	ctx := context.Background()

	if _, err := r.Review(ctx, connect.NewRequest(&verav1.ReviewRequest{Id: "stranger"})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("a gone agent must be NotFound: %v", err)
	}
	rev, err := r.Review(ctx, connect.NewRequest(&verav1.ReviewRequest{Id: "sess-rev"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(rev.Msg.Files) != 2 || rev.Msg.Dir == "" {
		t.Fatalf("both changes with the repo root: %+v", rev.Msg)
	}
	if _, err := r.Commit(ctx, connect.NewRequest(&verav1.CommitRequest{Id: "sess-rev"})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("an empty message must be FailedPrecondition: %v", err)
	}
	if _, err := r.Discard(ctx, connect.NewRequest(&verav1.DiscardRequest{Id: "sess-rev", Path: "fresh.txt"})); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repo, "fresh.txt")); !os.IsNotExist(err) {
		t.Fatal("the discard must delete the untracked file")
	}
	c, err := r.Commit(ctx, connect.NewRequest(&verav1.CommitRequest{Id: "sess-rev", Message: "approved over the wire"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Msg.Commit == "" {
		t.Fatal("the new commit's hash comes back")
	}
	rev, err = r.Review(ctx, connect.NewRequest(&verav1.ReviewRequest{Id: "sess-rev"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(rev.Msg.Files) != 0 {
		t.Fatalf("after approve the tree is clean: %+v", rev.Msg.Files)
	}
}
