// What Vera knows about a repository.
//
// The fleet already learns this and then throws it away: every task
// resolves a repo, reads its conventions, works on a branch and lands.
// A project file is that, kept — so the second task in a repository
// starts from what the first one found, and so a person can read what
// Vera thinks the place is before she acts on it.
//
// One file per repository, created on the first task in it and never
// rewritten afterwards except to append. Not rewritten because a
// person editing "what this repo is" is exactly the point, and a
// machine that regenerates the file every morning would erase them.
package home

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// Project makes sure projects/<name>.md exists, with what the fleet
// knows: where the repo is, what it branches from, and the conventions
// it found. Existing files are left exactly as they are.
func (h *Home) Project(name, root, branch string, conventions []string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	path, existing := h.projectFile(name, root)
	if existing {
		return nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "---\nname: %s\nroot: %s\nsince: %s\n---\n\n", name, root, time.Now().Format("2006-01-02"))
	fmt.Fprintf(&b, "# %s\n\n", name)
	fmt.Fprintf(&b, "- root: `%s`\n", root)
	if branch != "" {
		fmt.Fprintf(&b, "- default branch: `%s`\n", branch)
	}
	if len(conventions) == 0 {
		b.WriteString("- conventions: none found (no `rook.toml`)\n")
	} else {
		b.WriteString("- conventions, from `rook.toml`:\n")
		for _, c := range conventions {
			fmt.Fprintf(&b, "  - %s\n", c)
		}
	}
	b.WriteString("\n## What Vera has done here\n\n")
	return write(path, b.String())
}

// Landed appends one line for a task that finished. The brief's first
// line is the whole record on purpose: this is a log of what happened
// in a repository, not a second copy of the fleet's store.
func (h *Home) Landed(name, root, task, brief string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	path, existing := h.projectFile(name, root)
	if !existing {
		// A task can land in a repo whose file was deleted; the line
		// still has somewhere to go.
		if err := write(path, fmt.Sprintf("# %s\n\n- root: `%s`\n\n## What Vera has done here\n\n", name, root)); err != nil {
			return err
		}
	}
	line := fmt.Sprintf("- %s landed %s: %s\n", time.Now().Format("2006-01-02"), task, oneLine(brief))
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	body := string(b)
	if strings.Contains(body, line) {
		return nil // idempotent: the supervisor may try a landing twice
	}
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	return write(path, body+line)
}

// projectFile resolves the file for a repository. Two checkouts can
// share a basename ("api" in two orgs), and quietly writing one repo's
// history into the other's file would be worse than an ugly name — so
// a file that names a different root sends this one to a suffixed
// name, and both callers resolve the same way.
func (h *Home) projectFile(name, root string) (path string, existing bool) {
	name = slug(name)
	if name == "" {
		name = "project"
	}
	for i := 0; i < 20; i++ {
		candidate := name
		if i > 0 {
			candidate = fmt.Sprintf("%s-%d", name, i+1)
		}
		path = h.path(ProjectsDir, candidate+".md")
		b, err := os.ReadFile(path)
		if err != nil {
			return path, false
		}
		if root == "" || strings.Contains(string(b), root) {
			return path, true
		}
	}
	return path, true
}

// Note is what she knows about one repository, for the prompt. Missing
// is not an error: a repo nobody has run a task in has no file yet.
func (h *Home) Note(name, root string) (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	path, existing := h.projectFile(name, root)
	if !existing {
		return "", false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	text := strings.TrimSpace(stripFrontMatter(string(b)))
	return text, text != ""
}

// stripFrontMatter drops the machine half. A prompt wants what she
// knows about the place, not the bookkeeping that got it there.
func stripFrontMatter(s string) string {
	rest, ok := strings.CutPrefix(strings.TrimLeft(s, "\n"), "---\n")
	if !ok {
		return s
	}
	_, after, found := strings.Cut(rest, "\n---")
	if !found {
		return s
	}
	return after
}
