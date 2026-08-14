// Review: the human's verdict on what an agent did to a repo. The
// tree pane says WHICH files moved; this says WHAT changed, line by
// line, and takes the two verdicts that end a review — approve
// (commit, the human's own git identity) or discard (put it back).
// "Request changes" is not here: that verdict is just words, and the
// say rail already carries words.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// atoiSafe: numstat prints "-" for binary files; that reads as 0.
func atoiSafe(s string) int { n, _ := strconv.Atoi(s); return n }

// reviewFile is one changed file with its whole diff. Add/Del/New
// mirror treeFile so the two readouts can never disagree on a file.
type reviewFile struct {
	Path      string `json:"path"`
	Add       int    `json:"add"`
	Del       int    `json:"del"`
	New       bool   `json:"new,omitempty"`
	Binary    bool   `json:"binary,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
	Diff      string `json:"diff,omitempty"`
}

// diffFileCap bounds the review to something a human can actually
// review; past it the honest answer is "too big for this pane".
const diffFileCap = 100

// diffByteCap bounds one file's diff text on the wire.
const diffByteCap = 120 << 10

// reviewGit runs one git command in dir and returns its stdout.
// Stderr is the error message when git refuses — git's own words are
// better than any paraphrase.
func reviewGit(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(errb.String()); msg != "" {
			return out.String(), errors.New(msg)
		}
		return out.String(), err
	}
	return out.String(), nil
}

// repoTop resolves the repo root. Every review verb runs from here:
// an agent may live in a subdirectory, but `git add -A` commits the
// whole repo — so the review must read the whole repo, or approve
// would commit files the human never saw.
func repoTop(dir string) (string, error) {
	top, err := reviewGit(dir, "rev-parse", "--show-toplevel")
	return strings.TrimSpace(top), err
}

// reviewChanges reads the working tree's full story: every
// uncommitted file with its unified diff, untracked files rendered as
// all-new diffs. Error when dir is not a repo (a scratch workspace,
// say) — absence is shown, never faked.
func reviewChanges(dir string) ([]reviewFile, error) {
	dir, err := repoTop(dir)
	if err != nil {
		return nil, err
	}
	numstat, err := reviewGit(dir, "diff", "--numstat", "HEAD")
	if err != nil {
		return nil, err
	}
	var files []reviewFile
	for _, line := range strings.Split(strings.TrimSpace(numstat), "\n") {
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		f := reviewFile{Path: parts[2], Binary: parts[0] == "-"}
		f.Add = atoiSafe(parts[0])
		f.Del = atoiSafe(parts[1])
		files = append(files, f)
		if len(files) >= diffFileCap {
			break
		}
	}
	if out, err := reviewGit(dir, "ls-files", "--others", "--exclude-standard"); err == nil {
		for _, p := range strings.Split(strings.TrimSpace(out), "\n") {
			if p == "" || len(files) >= diffFileCap {
				continue
			}
			files = append(files, reviewFile{Path: p, New: true})
		}
	}
	for i := range files {
		f := &files[i]
		var out string
		if f.New {
			// --no-index exits 1 whenever the files differ, which for
			// /dev/null vs anything is always; the output is still the diff.
			out, _ = reviewGit(dir, "diff", "--no-index", "--", os.DevNull, filepath.Join(dir, f.Path))
		} else {
			out, _ = reviewGit(dir, "diff", "HEAD", "--", f.Path)
		}
		if strings.Contains(out, "\nBinary files ") || strings.HasPrefix(out, "Binary files ") {
			f.Binary = true
		}
		if f.New && !f.Binary {
			// Count the new file's lines so the list reads like the tree
			// pane. One "\n+" belongs to the "+++ b/…" header line.
			if n := strings.Count(out, "\n+"); n > 0 {
				f.Add = n - 1
			}
		}
		if len(out) > diffByteCap {
			out, f.Truncated = out[:diffByteCap], true
		}
		if !f.Binary {
			f.Diff = out
		}
	}
	return files, nil
}

// reviewCommit is the approve verdict: stage everything, commit with
// the human's message under the human's own git identity. The commit
// is theirs — roost adds no signature.
func reviewCommit(dir, message string) (string, error) {
	if strings.TrimSpace(message) == "" {
		return "", errors.New("a commit needs a message")
	}
	dir, err := repoTop(dir)
	if err != nil {
		return "", err
	}
	if _, err := reviewGit(dir, "add", "-A"); err != nil {
		return "", err
	}
	if _, err := reviewGit(dir, "commit", "-m", message); err != nil {
		return "", err
	}
	hash, err := reviewGit(dir, "rev-parse", "--short", "HEAD")
	return strings.TrimSpace(hash), err
}

// reviewDiscard puts one file back the way HEAD has it — or deletes
// it, when HEAD never had it. path="" with all=true resets the whole
// tree. Only paths git itself just listed are touched: membership in
// the current change set is the safety, not path parsing.
func reviewDiscard(dir, path string, all bool) error {
	dir, err := repoTop(dir)
	if err != nil {
		return err
	}
	if all {
		if _, err := reviewGit(dir, "reset", "--hard", "HEAD"); err != nil {
			return err
		}
		_, err := reviewGit(dir, "clean", "-fd")
		return err
	}
	files, err := reviewChanges(dir)
	if err != nil {
		return err
	}
	for _, f := range files {
		if f.Path != path {
			continue
		}
		if f.New {
			return os.Remove(filepath.Join(dir, f.Path))
		}
		_, err := reviewGit(dir, "checkout", "HEAD", "--", f.Path)
		return err
	}
	return errors.New("that file has no uncommitted changes")
}

// The transport-neutral cores: REST below and the Connect RPCs both
// call these; refusals use HTTP's vocabulary and each rail translates.

// reviewInfo is one whole review: the repo root the verdict would
// cover, its branch, and every changed file.
type reviewInfo struct {
	Dir    string       `json:"dir"`
	Branch string       `json:"branch"`
	Files  []reviewFile `json:"files"`
}

func (s *server) agentReview(id string) (*reviewInfo, *sayErr) {
	_, head := s.resolveAgent(id, time.Now())
	if head == nil {
		return nil, &sayErr{404, "that agent is gone from the window"}
	}
	files, err := reviewChanges(head.Cwd)
	if err != nil {
		return nil, &sayErr{409, "not reviewable: " + err.Error()}
	}
	// The repo root, not the agent's cwd: approve commits the whole
	// repo, so the header names what the verdict covers.
	top, _ := repoTop(head.Cwd)
	return &reviewInfo{Dir: top, Branch: head.Branch, Files: files}, nil
}

func (s *server) agentCommit(id, message string) (string, *sayErr) {
	defer s.hub.notify()
	_, head := s.resolveAgent(id, time.Now())
	if head == nil {
		return "", &sayErr{404, "that agent is gone from the window"}
	}
	hash, err := reviewCommit(head.Cwd, message)
	if err != nil {
		return "", &sayErr{409, err.Error()}
	}
	return hash, nil
}

func (s *server) agentDiscard(id, path string, all bool) *sayErr {
	defer s.hub.notify()
	_, head := s.resolveAgent(id, time.Now())
	if head == nil {
		return &sayErr{404, "that agent is gone from the window"}
	}
	if path == "" && !all {
		return &sayErr{400, "say which file — or all"}
	}
	if err := reviewDiscard(head.Cwd, path, all); err != nil {
		return &sayErr{409, err.Error()}
	}
	return nil
}

func (s *server) handleDiff(w http.ResponseWriter, r *http.Request) {
	info, serr := s.agentReview(r.PathValue("id"))
	if serr != nil {
		httpErr(w, serr.code, serr.msg)
		return
	}
	writeJSON(w, info)
}

func (s *server) handleCommit(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Message string `json:"message"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req) != nil {
		httpErr(w, 400, "the request did not parse")
		return
	}
	hash, serr := s.agentCommit(r.PathValue("id"), req.Message)
	if serr != nil {
		httpErr(w, serr.code, serr.msg)
		return
	}
	writeJSON(w, map[string]string{"commit": hash})
}

func (s *server) handleDiscard(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
		All  bool   `json:"all"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req) != nil {
		httpErr(w, 400, "the request did not parse")
		return
	}
	if serr := s.agentDiscard(r.PathValue("id"), req.Path, req.All); serr != nil {
		httpErr(w, serr.code, serr.msg)
		return
	}
	writeJSON(w, map[string]string{"status": "discarded"})
}
