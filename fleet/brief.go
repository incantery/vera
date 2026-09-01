package fleet

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/incantery/vera/attach"
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
			b.WriteString("Commit as you go with clear messages. Do not merge and do not push: when you say done, Vera lands the branch. If landing fails you will be told why; fix it, commit, and say done again.\n\n")
		}
	case Scout:
		fmt.Fprintf(&b, "You are in %s to investigate and report. Do not modify files, do not commit, do not run anything with side effects.\n\n", t.Worktree)
	}
	// The context nobody thought to hand over. An agent opened in a
	// repository it has never seen has no idea what has been going on
	// in it, and until there was a stream to read there was nothing to
	// point it at — so the brief either carried a paragraph of history
	// that went stale, or it carried none. One command, which the
	// agent runs only if it needs to.
	if name := baseName(t.Project); name != "" {
		fmt.Fprintf(&b, "You can catch up on what has been happening here without asking: `vera events --repo %s --since 7d` prints the recent record — the tasks that ran and what they said, what landed, what the person asked for. It reads files, so it works whether or not anything is running. The same record is `~/.local/state/vera/events/*.jsonl` if the command is not on your PATH.\n\n", name)
	}
	if reportPath != "" {
		switch t.Kind {
		case Scout:
			fmt.Fprintf(&b, "Write your report — findings, evidence with file paths, and a recommendation — as markdown to %s. That file is the deliverable; what you print in this terminal is not kept. Write it before you report done.\n\n", reportPath)
		default:
			fmt.Fprintf(&b, "When you are done, write a short summary of what you changed and why — with anything the person should know or decide — as markdown to %s. Write it before you report done.\n\n", reportPath)
		}
	}
	if len(t.Images) > 0 {
		// Named as evidence rather than as work: the agent is being
		// shown what the person was looking at, and the files live in
		// Vera's own state directory, outside the room. attach.Brief
		// says all of that in the words the delegate also uses, so a
		// task and a delegation read the same.
		b.WriteString(strings.TrimSpace(attach.Brief("", t.Images)))
		b.WriteString("\n\n")
	}
	if statusURL != "" {
		fmt.Fprintf(&b, `Report where you stand by running this whenever it changes — it is how Vera knows without reading your screen:

  curl -s -X POST %s -H 'Content-Type: application/json' -d '{"verb":"<verb>","text":"<one line>"}'

Verbs: working (what you are on now), blocked (you need a decision or information from the person — say exactly what; then wait, they will answer in this terminal), paused (waiting on something external, say what), done (finished — say what you delivered, with the PR URL if there is one), failed (you cannot finish — say why). Send blocked or done the moment it is true; do not sit at a prompt without one.

`, statusURL)
	}
	b.WriteString("Stay inside your room: do not install anything outside it, do not restart or kill services or processes you did not start, do not change the person's shell, editor or terminal, and do not touch the multiplexer you are running in. If landing your work needs any of that, write it in your report and stop; the person does it. ")
	b.WriteString("Do not ask the person for permission to proceed on ordinary steps; make the routine calls yourself and reserve blocked for real forks in the road. Your terminal's own permission prompts are answered by Vera or the person, but every one of them stalls you: prefer commands the project already uses.")
	return b.String()
}

// Claude Code asks, the first time it runs in a directory, whether to
// trust it. A fresh worktree is a fresh directory every time, and an
// agent nobody is watching would sit on that dialog forever. The
// answer is already known: the person named the repository and Vera
// made the room from it. inheritTrust writes that answer into Claude
// Code's own record before the agent starts.
//
// This is the one place Vera writes a file that is not its own. It is
// a read-modify-write of one key under one project path. (It used to
// require the main checkout to be trusted first; a repository Claude
// Code had never opened then left the agent on the dialog.)
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
	// Vera made this room from a repository the person named; that
	// is the trust decision, already taken. The main checkout's own
	// answer is not needed — a brand-new repository has none.
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

// claudeHasSession says whether Claude Code has a conversation to
// --continue in dir: its sessions live under ~/.claude/projects/<dir
// with every "/" and "." turned into "-">/, one jsonl each.
func claudeHasSession(dir string) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	enc := strings.NewReplacer("/", "-", ".", "-").Replace(dir)
	entries, err := os.ReadDir(filepath.Join(home, ".claude", "projects", enc))
	if err != nil {
		return false
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".jsonl") {
			return true
		}
	}
	return false
}
