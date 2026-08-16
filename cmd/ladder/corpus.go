// The corpus: tasks with a mechanical bar. Each task seeds a fresh
// directory, states one goal, and says how a machine decides pass or
// fail — a check command that must exit 0, substrings the final reply
// must carry, or both. The supervising judge never grades: it steers
// the drive arm, and grading a race it ran would be a thumb on the
// scale.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type task struct {
	ID   string `json:"id"`
	Goal string `json:"goal"`
	// Mode picks the tool policy: "work" (edits + build-and-test) or
	// "read" (print mode's default: gated tools refused). Default work.
	Mode string `json:"mode,omitempty"`
	// Files are seeded into the run's directory before the first turn,
	// path → content. Paths are relative and stay inside the dir.
	Files map[string]string `json:"files,omitempty"`
	// Check runs via `sh -c` in the run's directory after the run;
	// exit 0 is a pass. This is the bar for work tasks.
	Check string `json:"check,omitempty"`
	// Expect are case-insensitive substrings the final reply must all
	// carry. This is the bar for read tasks.
	Expect []string `json:"expect,omitempty"`
	// Tools overrides the mode's tool policy when set.
	Tools []string `json:"tools,omitempty"`
}

// workTools mirrors cmd/vera/tasks.go: edits plus the build-and-test
// commands a repo task needs — no git mutation, no network, no
// installs. The two lists drift apart only on purpose.
var workTools = []string{
	"Edit", "Write", "MultiEdit",
	"Bash(go build:*)", "Bash(go test:*)", "Bash(go vet:*)", "Bash(gofmt:*)",
	"Bash(npm test:*)", "Bash(npm run build:*)", "Bash(make:*)",
}

func (t *task) tools() []string {
	if len(t.Tools) > 0 {
		return t.Tools
	}
	if t.Mode == "read" {
		return nil
	}
	return workTools
}

func loadCorpus(path string) ([]task, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var tasks []task
	if err := json.Unmarshal(raw, &tasks); err != nil {
		return nil, errors.New(path + " did not parse: " + err.Error())
	}
	if len(tasks) == 0 {
		return nil, errors.New(path + " holds no tasks")
	}
	seen := map[string]bool{}
	for i := range tasks {
		if err := tasks[i].validate(); err != nil {
			return nil, fmt.Errorf("task %d (%q): %w", i+1, tasks[i].ID, err)
		}
		if seen[tasks[i].ID] {
			return nil, errors.New("duplicate task id: " + tasks[i].ID)
		}
		seen[tasks[i].ID] = true
	}
	return tasks, nil
}

func (t *task) validate() error {
	if !fileSafe(t.ID) {
		return errors.New("the id must be nonempty, filename-shaped ([A-Za-z0-9._-])")
	}
	if strings.TrimSpace(t.Goal) == "" {
		return errors.New("the goal is empty")
	}
	switch t.Mode {
	case "", "work", "read":
	default:
		return errors.New("mode must be \"work\" or \"read\", not " + t.Mode)
	}
	if t.Check == "" && len(t.Expect) == 0 {
		return errors.New("no bar: a task needs a check command, expected substrings, or both")
	}
	for p := range t.Files {
		if p == "" || filepath.IsAbs(p) || p != filepath.Clean(p) || strings.HasPrefix(p, "..") {
			return errors.New("file path escapes the run directory: " + p)
		}
	}
	return nil
}

// fileSafe: [A-Za-z0-9._-] only, nonempty — what a run-directory name
// can carry (drive.safeID's charset, restated here since it is
// unexported there).
func fileSafe(id string) bool {
	if id == "" {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '_', c == '-':
		default:
			return false
		}
	}
	return true
}
