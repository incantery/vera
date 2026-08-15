// The usage collector's probes (`claude /usage -p`) each leave a
// transcript file behind — hundreds a week, none a conversation. The
// scanner already hides them (Skip); this sweeps the files themselves,
// by signature, so ~/.claude/projects stops accumulating them.
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// probeMaxBytes: a probe transcript is a few KB. Anything bigger is a
// conversation and is never even read.
const probeMaxBytes = 64 << 10

// probeMinAge: only settled files are swept — the newest probe may
// still be the scanner's freshest sample.
const probeMinAge = time.Hour

// pruneProbes deletes usage-probe transcripts under dir: small, old
// enough, living in the home directory, carrying the /usage command,
// and never titled by Claude. Every condition must hold — a real
// conversation fails at least two.
func pruneProbes(dir, home string, now time.Time) int {
	if dir == "" || home == "" {
		return 0
	}
	projects, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	n := 0
	cwdSig := []byte(`"cwd":"` + home + `"`)
	for _, p := range projects {
		if !p.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(dir, p.Name()))
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
				continue
			}
			info, err := f.Info()
			if err != nil || info.Size() > probeMaxBytes || now.Sub(info.ModTime()) < probeMinAge {
				continue
			}
			path := filepath.Join(dir, p.Name(), f.Name())
			b, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			if !bytes.Contains(b, []byte(`"content":"/usage"`)) ||
				!bytes.Contains(b, cwdSig) ||
				bytes.Contains(b, []byte(`"aiTitle"`)) {
				continue
			}
			if os.Remove(path) == nil {
				n++
			}
		}
	}
	return n
}
