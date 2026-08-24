package fleet

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The brief is a contract, not a suggestion — firstmate's rule, and
// the reason its crewmates report in a vocabulary the supervisor can
// read. What the person asked for is the top of it, untouched. What
// follows is the part every task gets: where it is, what it may not
// do, and how to say where it stands.

// scaffold appends the standing terms to a brief.
func scaffold(t *Task, statusURL, reportPath string) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(t.Brief))
	b.WriteString("\n\n---\n\n")
	b.WriteString("You are working for Vera, who is relaying this task for the person and will relay your questions back to them. They are not watching this terminal.\n\n")
	switch t.Kind {
	case Ship:
		fmt.Fprintf(&b, "You are in a dedicated copy of the repository at %s, on branch %s. Work only there: never cd into %s or touch other checkouts. ", t.Worktree, t.Branch, t.Project)
		switch t.Mode {
		case DirectPR:
			b.WriteString("Commit as you go. When the work is done, push the branch, open a pull request with `gh pr create`, and report its URL. Do not merge.\n\n")
		case NoMistakes:
			b.WriteString("Commit as you go. When the work is done, run the project's tests and linters and fix what they find before reporting done. Do not push, do not merge.\n\n")
		default:
			b.WriteString("Commit as you go with clear messages. Do not merge and do not push: Vera lands the branch when the person says so.\n\n")
		}
	case Scout:
		fmt.Fprintf(&b, "You are in %s to investigate and report. Do not modify files, do not commit, do not run anything with side effects.\n\n", t.Worktree)
	}
	if reportPath != "" {
		switch t.Kind {
		case Scout:
			fmt.Fprintf(&b, "Write your report — findings, evidence with file paths, and a recommendation — as markdown to %s. That file is the deliverable; what you print in this terminal is not kept. Write it before you report done.\n\n", reportPath)
		default:
			fmt.Fprintf(&b, "When you are done, write a short summary of what you changed and why — with anything the person should know or decide — as markdown to %s. Write it before you report done.\n\n", reportPath)
		}
	}
	if statusURL != "" {
		fmt.Fprintf(&b, `Report where you stand by running this whenever it changes — it is how Vera knows without reading your screen:

  curl -s -X POST %s -H 'Content-Type: application/json' -d '{"verb":"<verb>","text":"<one line>"}'

Verbs: working (what you are on now), blocked (you need a decision or information from the person — say exactly what; then wait, they will answer in this terminal), paused (waiting on something external, say what), done (finished — say what you delivered, with the PR URL if there is one), failed (you cannot finish — say why). Send blocked or done the moment it is true; do not sit at a prompt without one.

`, statusURL)
	}
	b.WriteString("Do not ask the person for permission to proceed on ordinary steps; make the routine calls yourself and reserve blocked for real forks in the road.")
	return b.String()
}

// Claude Code asks, the first time it runs in a directory, whether to
// trust it. A fresh worktree is a fresh directory every time, and an
// agent nobody is watching would sit on that dialog forever. The
// answer is already known: the main checkout is trusted, and a
// worktree of it is the same code. inheritTrust copies that answer
// into Claude Code's own record before the agent starts.
//
// This is the one place Vera writes a file that is not its own. It is
// a read-modify-write of one key under one project path, and it does
// nothing at all when the main checkout is not trusted — the person
// has to say yes there first.
func inheritTrust(project, worktree string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	path := filepath.Join(home, ".claude.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return err // no Claude Code state yet: nothing to inherit from
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return err
	}
	var projects map[string]map[string]any
	if err := json.Unmarshal(top["projects"], &projects); err != nil || projects == nil {
		return errors.New("no projects recorded")
	}
	src, ok := projects[project]
	if !ok || src["hasTrustDialogAccepted"] != true {
		return errors.New("main checkout is not trusted by Claude Code")
	}
	dst := projects[worktree]
	if dst == nil {
		dst = map[string]any{}
	}
	if dst["hasTrustDialogAccepted"] == true {
		return nil
	}
	dst["hasTrustDialogAccepted"] = true
	projects[worktree] = dst
	pb, err := json.Marshal(projects)
	if err != nil {
		return err
	}
	top["projects"] = pb
	out, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".vera.tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
