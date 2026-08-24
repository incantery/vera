package fleet

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Worktrees: one task, one branch, one checkout — and a lifecycle that
// ends with the branch merged and the checkout gone.
//
// Ported from rook's internal/worktree, minus the session: rook tied a
// checkout to a place you work in it; here the place is the fleet's
// business and git's worktrees are just the isolation. Layout is the
// same so the two agree about where things are: a worktree for repo R
// named N lives beside the repo at <parent-of-R>/R--N.

// Repo is the main checkout a set of worktrees hangs off.
type Repo struct {
	Root string // the main worktree's top level
	Name string // its directory name, the prefix of every worktree dir
}

// Worktree is one checkout.
type Worktree struct {
	Name   string `json:"name"` // short name: the dir minus the repo prefix; "" for main
	Path   string `json:"path"`
	Branch string `json:"branch"` // "" when detached
	Head   string `json:"head"`   // short commit
	Main   bool   `json:"main"`   // the repo's own checkout
	Dirty  bool   `json:"dirty"`
	Ahead  int    `json:"ahead"`  // commits on Branch not on the main branch
	Behind int    `json:"behind"` // commits on the main branch not on Branch
}

// Conventions are what a checkout needs that git does not carry.
type Conventions struct {
	// Copy are repo-relative paths copied from the main checkout into a
	// new worktree (".env", "config/local.toml").
	Copy []string
	// Link are repo-relative paths symlinked to the main checkout's
	// copy: heavy caches no two worktrees need twice ("node_modules").
	Link []string
}

// FindRepo resolves the repo containing dir. A worktree answers with
// its true home, so any checkout of the repo gets the same Repo.
func FindRepo(dir string) (Repo, error) {
	out, err := git(dir, "rev-parse", "--git-common-dir")
	if err != nil {
		return Repo{}, fmt.Errorf("%s is not in a git repository", dir)
	}
	common := strings.TrimSpace(out)
	if !filepath.IsAbs(common) {
		common = filepath.Join(dir, common)
	}
	root := filepath.Dir(common)
	if filepath.Base(common) != ".git" {
		root = common // bare: the common dir IS the repo
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return Repo{}, err
	}
	// git reports real paths (/private/var, not /var on macOS); match
	// it so worktree paths compare equal to ours.
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	return Repo{Root: root, Name: filepath.Base(root)}, nil
}

// Path is where a worktree named name lives for this repo.
func (r Repo) Path(name string) string {
	return filepath.Join(filepath.Dir(r.Root), r.Name+"--"+name)
}

// Session is the mux session a worktree's pane belongs in — the
// directory name, unambiguous across repos.
func (r Repo) Session(name string) string {
	if name == "" {
		return r.Name
	}
	return r.Name + "--" + name
}

func (r Repo) nameOf(path string) string {
	base := filepath.Base(path)
	if rest, ok := strings.CutPrefix(base, r.Name+"--"); ok && filepath.Dir(path) == filepath.Dir(r.Root) {
		return rest
	}
	return ""
}

// DefaultBranch is what worktrees branch from and merge into: what
// origin/HEAD points at, else main, else master, else whatever the main
// checkout has out.
func (r Repo) DefaultBranch() string {
	if out, err := git(r.Root, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
		return strings.TrimPrefix(strings.TrimSpace(out), "origin/")
	}
	for _, b := range []string{"main", "master"} {
		if _, err := git(r.Root, "rev-parse", "--verify", "--quiet", "refs/heads/"+b); err == nil {
			return b
		}
	}
	out, _ := git(r.Root, "branch", "--show-current")
	return strings.TrimSpace(out)
}

// List enumerates the repo's worktrees, main first.
func (r Repo) List() ([]Worktree, error) {
	out, err := git(r.Root, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, fmt.Errorf("git worktree list: %s", strings.TrimSpace(out))
	}
	base := r.DefaultBranch()
	var wts []Worktree
	var cur *Worktree
	flush := func() {
		if cur != nil {
			wts = append(wts, *cur)
			cur = nil
		}
	}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "worktree "):
			flush()
			p := strings.TrimPrefix(line, "worktree ")
			cur = &Worktree{Path: p, Name: r.nameOf(p)}
		case cur == nil:
		case strings.HasPrefix(line, "HEAD "):
			cur.Head = shortHash(strings.TrimPrefix(line, "HEAD "))
		case strings.HasPrefix(line, "branch "):
			cur.Branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		}
	}
	flush()
	for i := range wts {
		w := &wts[i]
		w.Main = w.Path == r.Root
		if st, err := git(w.Path, "status", "--porcelain"); err == nil {
			w.Dirty = strings.TrimSpace(st) != ""
		}
		if w.Branch != "" && !w.Main {
			w.Ahead, w.Behind = distance(r.Root, base, w.Branch)
		}
	}
	return wts, nil
}

// Get finds one worktree by short name.
func (r Repo) Get(name string) (Worktree, error) {
	wts, err := r.List()
	if err != nil {
		return Worktree{}, err
	}
	for _, w := range wts {
		if w.Name == name && !w.Main {
			return w, nil
		}
	}
	return Worktree{}, fmt.Errorf("no worktree named %q", name)
}

var goodName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// New cuts a worktree and a branch of the same name from `from` (the
// default branch when empty), then applies the conventions.
func (r Repo) New(name, from string, conv Conventions) (Worktree, error) {
	if !goodName.MatchString(name) || strings.Contains(name, "..") {
		return Worktree{}, fmt.Errorf("bad worktree name %q", name)
	}
	if from == "" {
		from = r.DefaultBranch()
	}
	path := r.Path(name)
	if _, err := os.Stat(path); err == nil {
		return Worktree{}, fmt.Errorf("%s already exists", path)
	}
	args := []string{"worktree", "add"}
	if _, err := git(r.Root, "rev-parse", "--verify", "--quiet", "refs/heads/"+name); err == nil {
		args = append(args, path, name) // the branch exists: check it out
	} else {
		args = append(args, "-b", name, path, from)
	}
	if out, err := git(r.Root, args...); err != nil {
		return Worktree{}, fmt.Errorf("git worktree add: %s", strings.TrimSpace(out))
	}
	for _, rel := range conv.Copy {
		src, dst := filepath.Join(r.Root, rel), filepath.Join(path, rel)
		if _, err := os.Lstat(src); err != nil {
			continue
		}
		if err := copyPath(src, dst); err != nil {
			fmt.Fprintf(os.Stderr, "fleet: copy %s: %v\n", rel, err)
		}
	}
	for _, rel := range conv.Link {
		src, dst := filepath.Join(r.Root, rel), filepath.Join(path, rel)
		if _, err := os.Lstat(src); err != nil {
			continue
		}
		_ = os.RemoveAll(dst)
		if err := os.Symlink(src, dst); err != nil {
			fmt.Fprintf(os.Stderr, "fleet: link %s: %v\n", rel, err)
		}
	}
	return r.Get(name)
}

// Merge lands the worktree's branch on the default branch in the main
// checkout, then removes the worktree and the branch. It refuses when
// either checkout is dirty or when the merge does not apply cleanly —
// the main checkout is left with the merge aborted, nothing half-done.
func (r Repo) Merge(name string) error {
	wt, err := r.Get(name)
	if err != nil {
		return err
	}
	if wt.Branch == "" {
		return fmt.Errorf("%s is detached; nothing to merge", wt.Name)
	}
	if wt.Dirty {
		return fmt.Errorf("%s has uncommitted changes; commit or stash them first", wt.Name)
	}
	base := r.DefaultBranch()
	if cur, _ := git(r.Root, "branch", "--show-current"); strings.TrimSpace(cur) != base {
		return fmt.Errorf("main checkout is on %q, not %s; check out %s first", strings.TrimSpace(cur), base, base)
	}
	if st, _ := git(r.Root, "status", "--porcelain"); strings.TrimSpace(st) != "" {
		return fmt.Errorf("main checkout has uncommitted changes; merge needs it clean")
	}
	if out, err := git(r.Root, "merge", "--no-edit", wt.Branch); err != nil {
		_, _ = git(r.Root, "merge", "--abort")
		return fmt.Errorf("merge %s: %s", wt.Branch, strings.TrimSpace(out))
	}
	return r.Remove(wt, true)
}

// Remove deletes a worktree and its branch. Without force it refuses
// dirty checkouts and unmerged branches. Every refusal happens before
// anything is touched: a half-removed worktree is worse than one that
// is still there.
func (r Repo) Remove(wt Worktree, force bool) error {
	if wt.Main {
		return fmt.Errorf("refusing to remove the main checkout")
	}
	if wt.Dirty && !force {
		return fmt.Errorf("%s has uncommitted changes (force to discard)", wt.Name)
	}
	if wt.Branch != "" && !force {
		if _, err := git(r.Root, "merge-base", "--is-ancestor", wt.Branch, r.DefaultBranch()); err != nil {
			return fmt.Errorf("%s has commits not on %s (merge it, or force to drop them)", wt.Branch, r.DefaultBranch())
		}
	}
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	if out, err := git(r.Root, append(args, wt.Path)...); err != nil {
		return fmt.Errorf("git worktree remove: %s", strings.TrimSpace(out))
	}
	if wt.Branch != "" {
		flag := "-d"
		if force {
			flag = "-D"
		}
		if out, err := git(r.Root, "branch", flag, wt.Branch); err != nil {
			return fmt.Errorf("worktree removed, but branch %s kept: %s", wt.Branch, strings.TrimSpace(out))
		}
	}
	return nil
}

// LoadConventions reads the [worktree] table of rook.toml in the main
// checkout, if there is one — the same file rook reads, so a repo's
// conventions are written once. Only `copy` and `link` string lists are
// understood; anything else is skipped.
func LoadConventions(root string) Conventions {
	var c Conventions
	b, err := os.ReadFile(filepath.Join(root, "rook.toml"))
	if err != nil {
		return c
	}
	in := false
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if i := strings.Index(line, "#"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if strings.HasPrefix(line, "[") {
			in = line == "[worktree]"
			continue
		}
		if !in {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		list := tomlStrings(strings.TrimSpace(val))
		switch strings.TrimSpace(key) {
		case "copy":
			c.Copy = list
		case "link":
			c.Link = list
		}
	}
	return c
}

func tomlStrings(v string) []string {
	v = strings.TrimSuffix(strings.TrimPrefix(v, "["), "]")
	var out []string
	for _, s := range strings.Split(v, ",") {
		s = strings.TrimSpace(s)
		if u, err := strconv.Unquote(s); err == nil {
			s = u
		} else {
			s = strings.Trim(s, `"'`)
		}
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func distance(dir, base, branch string) (ahead, behind int) {
	out, err := git(dir, "rev-list", "--left-right", "--count", branch+"..."+base)
	if err != nil {
		return 0, 0
	}
	f := strings.Fields(out)
	if len(f) == 2 {
		ahead, _ = strconv.Atoi(f[0])
		behind, _ = strconv.Atoi(f[1])
	}
	return ahead, behind
}

func shortHash(h string) string {
	if len(h) > 7 {
		return h[:7]
	}
	return h
}

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	return buf.String(), err
}

func copyPath(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		return os.Symlink(target, dst)
	case info.IsDir():
		if err := os.MkdirAll(dst, info.Mode().Perm()); err != nil {
			return err
		}
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err := copyPath(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
				return err
			}
		}
		return nil
	default:
		data, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dst, data, info.Mode().Perm())
	}
}
