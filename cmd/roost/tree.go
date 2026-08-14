// The working-tree readout: what the agent's directory has changed
// and not committed, read straight from git. Read-only — mutation is
// the review surface's job (review.go, always human-verdict-gated) —
// and honestly absent when the directory is not one.
package main

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type treeFile struct {
	Path string `json:"path"`
	Add  int    `json:"add"`
	Del  int    `json:"del"`
	New  bool   `json:"new,omitempty"` // untracked
}

const treeCap = 20

// gitTree lists uncommitted changes in dir: tracked files as +/− line
// counts against HEAD, untracked files marked new. nil when dir is not
// a git repo or git is slow — the pane simply doesn't render.
func gitTree(dir string) []treeFile {
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	var out []treeFile
	if b, err := exec.CommandContext(ctx, "git", "-C", dir, "diff", "--numstat", "HEAD").Output(); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			parts := strings.SplitN(line, "\t", 3)
			if len(parts) != 3 {
				continue
			}
			add, _ := strconv.Atoi(parts[0]) // "-" (binary) parses to 0
			del, _ := strconv.Atoi(parts[1])
			out = append(out, treeFile{Path: parts[2], Add: add, Del: del})
			if len(out) >= treeCap {
				return out
			}
		}
	} else {
		return nil
	}
	if b, err := exec.CommandContext(ctx, "git", "-C", dir, "ls-files", "--others", "--exclude-standard").Output(); err == nil {
		for _, p := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			if p == "" {
				continue
			}
			out = append(out, treeFile{Path: p, New: true})
			if len(out) >= treeCap {
				break
			}
		}
	}
	return out
}
