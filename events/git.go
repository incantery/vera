package events

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The repositories half of the stream.
//
// Everything else in this package is reported to it: the fleet says a
// task finished, the mind says an exchange happened. Commits are the
// opposite — they are already durable, already queryable, and already
// in two places at once, and nothing is going to tell us about them.
// So this half goes and looks: every repository Vera knows, every few
// minutes, for commits it has not written down yet.
//
// It is what makes the stream cover "both repositories" in the way a
// person means it. A week in rook is not only the tasks Vera opened
// there; it is what landed, including everything the person did at
// their own keyboard while she was not watching.

// Repo is a checkout to watch: the name it is called by, and where it
// is. It mirrors fleet.Repo without depending on it — this package is
// a leaf on purpose, so the CLI can read the stream with no daemon and
// no fleet behind it.
type Repo struct {
	Name string
	Root string
}

// GitWatcher turns commits into events, once per commit ever.
//
// Exactly-once is the whole difficulty: it is asked the same question
// every few minutes, and a stream that repeats yesterday's commits at
// every sweep is unreadable within a day. So it keeps a cursor per
// repository beside the shards — the newest commit time it has
// emitted, and the shas at that edge, because several commits share a
// second and a timestamp alone would either drop them or repeat them.
type GitWatcher struct {
	// Dir is the events directory; cursors live in a subdirectory of
	// it, so the whole history is one directory to move or delete.
	Dir string
	// Lookback is how far back the first sight of a repository reaches.
	// Zero means a week: enough that a machine that starts recording
	// today can still answer "what happened this week", and little
	// enough that it is not importing the project's whole history.
	Lookback time.Duration
	// Max bounds one scan; zero means 500. A repository that has had
	// ten thousand commits since the last sweep is a repository that
	// was just cloned, and its ancient history is not news.
	Max int
	// Run executes git; nil runs the real one. Tests replace it.
	Run func(ctx context.Context, root string, args ...string) ([]byte, error)
}

const (
	defaultLookback = 7 * 24 * time.Hour
	defaultMax      = 500
	// edgeShas bounds the cursor's memory of the newest second. It only
	// has to cover commits sharing one timestamp; a hundred is already
	// absurd generosity.
	edgeShas = 100
)

// cursor is what one repository's scan remembers between sweeps.
type cursor struct {
	Root string    `json:"root"`
	At   time.Time `json:"at"`
	Seen []string  `json:"seen"`
}

// Scan returns the commits in repo that have not been reported before,
// oldest first, and advances the cursor. A directory that is not a git
// checkout, or a git that is not installed, is not an error worth
// bubbling up — it is a repository this machine cannot see today.
func (g *GitWatcher) Scan(ctx context.Context, repo Repo) ([]Event, error) {
	if strings.TrimSpace(repo.Root) == "" {
		return nil, nil
	}
	cur, err := g.readCursor(repo)
	if err != nil {
		return nil, err
	}
	since := cur.At
	if since.IsZero() {
		since = time.Now().Add(-g.lookback())
	}
	out, err := g.run(ctx, repo.Root, "log", "--all", "--date-order",
		fmt.Sprintf("-n%d", g.max()),
		"--since="+since.Format(time.RFC3339),
		"--pretty=format:%H%x1f%cI%x1f%an%x1f%D%x1f%P%x1f%s")
	if err != nil {
		return nil, nil
	}
	seen := map[string]bool{}
	for _, sha := range cur.Seen {
		seen[sha] = true
	}
	var evs []Event
	newest := cur.At
	var edge []string
	for _, line := range strings.Split(string(out), "\n") {
		c, ok := parseCommit(line)
		if !ok || seen[c.sha] {
			continue
		}
		evs = append(evs, c.event(repo))
		switch {
		case c.at.After(newest):
			newest, edge = c.at, []string{c.sha}
		case c.at.Equal(newest):
			edge = append(edge, c.sha)
		}
	}
	if len(evs) == 0 {
		return nil, nil
	}
	// git hands them back newest first; flip, then stable-sort, so
	// commits sharing a second keep the order they were made in.
	for i, j := 0, len(evs)-1; i < j; i, j = i+1, j-1 {
		evs[i], evs[j] = evs[j], evs[i]
	}
	sortOldestFirst(evs)
	// The edge is every sha at the newest commit time we have now
	// reported. When that time has not moved, the previous cursor's
	// shas were at the same instant and must be carried forward, or a
	// commit sharing its second with the last one reported comes back
	// as news on the next sweep.
	if newest.Equal(cur.At) {
		for _, sha := range cur.Seen {
			if len(edge) >= edgeShas {
				break
			}
			if !contains(edge, sha) {
				edge = append(edge, sha)
			}
		}
	}
	if len(edge) > edgeShas {
		edge = edge[:edgeShas]
	}
	if err := g.writeCursor(repo, cursor{Root: repo.Root, At: newest, Seen: edge}); err != nil {
		return evs, err
	}
	return evs, nil
}

// ScanAll sweeps every repository and returns everything new, oldest
// first. One repository failing does not stop the others.
func (g *GitWatcher) ScanAll(ctx context.Context, repos []Repo) []Event {
	var out []Event
	for _, r := range repos {
		evs, _ := g.Scan(ctx, r)
		out = append(out, evs...)
	}
	sortOldestFirst(out)
	return out
}

type commit struct {
	sha, author, refs, subject string
	at                         time.Time
	// merge: more than one parent. It is kept apart from an ordinary
	// commit because a merge is the branch landing and its content is
	// already in the stream as the branch's own commits — so a reader
	// asking what was actually done can say `--kind git.commit` and a
	// reader asking what went home can say `--kind git.merge`.
	merge bool
}

func parseCommit(line string) (commit, bool) {
	parts := strings.Split(strings.TrimSpace(line), "\x1f")
	if len(parts) != 6 || len(parts[0]) < 7 {
		return commit{}, false
	}
	at, err := time.Parse(time.RFC3339, parts[1])
	if err != nil {
		return commit{}, false
	}
	return commit{
		sha: parts[0], at: at, author: parts[2], refs: parts[3],
		merge:   strings.Contains(strings.TrimSpace(parts[4]), " "),
		subject: parts[5],
	}, true
}

func (c commit) event(repo Repo) Event {
	short := c.sha[:7]
	fields := map[string]string{"sha": c.sha}
	if c.author != "" {
		fields["author"] = c.author
	}
	if refs := branchOf(c.refs); refs != "" {
		fields["refs"] = refs
	}
	kind := "git.commit"
	if c.merge {
		kind = "git.merge"
	}
	return Event{
		At:      c.at,
		Repo:    repo.Name,
		Source:  "git",
		Kind:    kind,
		Subject: short,
		Project: repo.Root,
		Text:    c.subject + " (" + short + ")",
		Fields:  fields,
	}
}

// branchOf keeps the ref decoration short and drops the noise: a
// commit's whole ref list is mostly remotes of the same branch.
func branchOf(refs string) string {
	var out []string
	for _, r := range strings.Split(refs, ",") {
		r = strings.TrimSpace(r)
		r = strings.TrimPrefix(r, "HEAD -> ")
		if r == "" || r == "HEAD" || strings.HasPrefix(r, "origin/") || strings.HasPrefix(r, "tag: ") {
			continue
		}
		if !contains(out, r) {
			out = append(out, r)
		}
	}
	return strings.Join(out, ", ")
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func sortOldestFirst(evs []Event) {
	sort.SliceStable(evs, func(i, j int) bool { return evs[i].At.Before(evs[j].At) })
}

// run is git, or whatever a test put in its place.
func (g *GitWatcher) run(ctx context.Context, root string, args ...string) ([]byte, error) {
	if g.Run != nil {
		return g.Run(ctx, root, args...)
	}
	return git(ctx, root, args...)
}

func (g *GitWatcher) lookback() time.Duration {
	if g.Lookback > 0 {
		return g.Lookback
	}
	return defaultLookback
}

func (g *GitWatcher) max() int {
	if g.Max > 0 {
		return g.Max
	}
	return defaultMax
}

// cursorPath keys on the root rather than the name: a repository can be
// renamed in Vera's head and is still the same checkout, and two
// checkouts of the same project must not share a cursor. The hash keeps
// a path with slashes in it a file name.
func (g *GitWatcher) cursorPath(repo Repo) string {
	sum := sha256.Sum256([]byte(repo.Root))
	return filepath.Join(g.Dir, "cursors", "git-"+hex.EncodeToString(sum[:8])+".json")
}

func (g *GitWatcher) readCursor(repo Repo) (cursor, error) {
	b, err := os.ReadFile(g.cursorPath(repo))
	if err != nil {
		if os.IsNotExist(err) {
			return cursor{}, nil
		}
		return cursor{}, err
	}
	var c cursor
	if json.Unmarshal(b, &c) != nil {
		return cursor{}, nil
	}
	return c, nil
}

func (g *GitWatcher) writeCursor(repo Repo, c cursor) error {
	path := g.cursorPath(repo)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// git runs one read-only command in a checkout.
func git(ctx context.Context, root string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	return cmd.Output()
}
