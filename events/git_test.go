package events

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func gitRun(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func newGitRepo(t *testing.T) Repo {
	t.Helper()
	root := filepath.Join(t.TempDir(), "proj")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, root, "init", "-q", "-b", "main")
	return Repo{Name: "proj", Root: root}
}

func addCommit(t *testing.T, repo Repo, subject string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo.Root, "f"), []byte(subject), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo.Root, "add", "f")
	gitRun(t, repo.Root, "commit", "-q", "-m", subject)
}

func TestGitScanReportsEachCommitOnce(t *testing.T) {
	repo := newGitRepo(t)
	addCommit(t, repo, "first thing")
	addCommit(t, repo, "second thing")
	g := &GitWatcher{Dir: t.TempDir()}

	evs, err := g.Scan(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 2 {
		t.Fatalf("want both commits, got %d: %+v", len(evs), evs)
	}
	if !strings.HasPrefix(evs[0].Text, "first thing") {
		t.Fatalf("want oldest first, got %q", evs[0].Text)
	}
	if evs[0].Repo != "proj" || evs[0].Kind != "git.commit" || evs[0].Project != repo.Root {
		t.Fatalf("want the repository named on the event, got %+v", evs[0])
	}
	if evs[0].Fields["sha"] == "" || evs[0].Subject == "" {
		t.Fatalf("want a sha on the event, got %+v", evs[0])
	}

	// Asked again with nothing new, it says nothing — the property the
	// whole cursor exists for.
	again, err := g.Scan(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Fatalf("want no repeats, got %+v", again)
	}

	addCommit(t, repo, "third thing")
	third, err := g.Scan(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(third) != 1 || !strings.HasPrefix(third[0].Text, "third thing") {
		t.Fatalf("want just the new commit, got %+v", third)
	}
}

// Commits in the same second are the case a timestamp cursor alone
// gets wrong, and a test repository makes them by default.
func TestGitScanHandlesCommitsInTheSameSecond(t *testing.T) {
	repo := newGitRepo(t)
	for _, s := range []string{"a", "b", "c"} {
		addCommit(t, repo, s)
	}
	g := &GitWatcher{Dir: t.TempDir()}
	first, err := g.Scan(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 3 {
		t.Fatalf("want three, got %d", len(first))
	}
	again, err := g.Scan(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Fatalf("want no repeats across a shared second, got %+v", again)
	}
}

func TestGitScanSeesWorkOnEveryBranch(t *testing.T) {
	repo := newGitRepo(t)
	addCommit(t, repo, "on main")
	g := &GitWatcher{Dir: t.TempDir()}
	if _, err := g.Scan(context.Background(), repo); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo.Root, "checkout", "-q", "-b", "side")
	addCommit(t, repo, "on a branch")
	gitRun(t, repo.Root, "checkout", "-q", "main")

	evs, err := g.Scan(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 || !strings.HasPrefix(evs[0].Text, "on a branch") {
		t.Fatalf("want the branch commit, got %+v", evs)
	}
	if evs[0].Fields["refs"] != "side" {
		t.Fatalf("want the branch named, got %q", evs[0].Fields["refs"])
	}
}

func TestGitScanIgnoresWhatIsNotARepository(t *testing.T) {
	g := &GitWatcher{Dir: t.TempDir()}
	evs, err := g.Scan(context.Background(), Repo{Name: "nope", Root: t.TempDir()})
	if err != nil || len(evs) != 0 {
		t.Fatalf("want silence for a directory that is not a checkout, got %v %v", evs, err)
	}
	if evs, err := g.Scan(context.Background(), Repo{Name: "empty"}); err != nil || evs != nil {
		t.Fatalf("want silence for a repository with no path, got %v %v", evs, err)
	}
}

func TestGitScanFirstSightIsBoundedByLookback(t *testing.T) {
	var asked []string
	g := &GitWatcher{
		Dir:      t.TempDir(),
		Lookback: 48 * time.Hour,
		Run: func(_ context.Context, _ string, args ...string) ([]byte, error) {
			asked = args
			return nil, nil
		},
	}
	if _, err := g.Scan(context.Background(), Repo{Name: "x", Root: "/tmp/x"}); err != nil {
		t.Fatal(err)
	}
	var since string
	for _, a := range asked {
		if strings.HasPrefix(a, "--since=") {
			since = strings.TrimPrefix(a, "--since=")
		}
	}
	when, err := time.Parse(time.RFC3339, since)
	if err != nil {
		t.Fatalf("want an RFC3339 --since, got %q (%v)", since, err)
	}
	if d := time.Since(when); d < 47*time.Hour || d > 49*time.Hour {
		t.Fatalf("want the lookback honoured, asked for %v ago", d)
	}
}

func TestScanAllKeepsGoingPastABadRepo(t *testing.T) {
	good := newGitRepo(t)
	addCommit(t, good, "real")
	g := &GitWatcher{Dir: t.TempDir()}
	evs := g.ScanAll(context.Background(), []Repo{{Name: "bad", Root: t.TempDir()}, good})
	if len(evs) != 1 {
		t.Fatalf("want the good repo's commit, got %+v", evs)
	}
}

func TestBranchOfDropsTheNoise(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"HEAD -> main, origin/main, tag: v1", "main"},
		{"", ""},
		{"origin/main", ""},
		{"side, HEAD -> side", "side"},
	} {
		if got := branchOf(c.in); got != c.want {
			t.Fatalf("branchOf(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
