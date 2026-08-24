package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// The runfile is verad's note to `vera`: here is where I am. Written
// on start, removed on a clean exit; a stale one (the pid is gone)
// is how `vera` tells a crash from a stop.
type runfile struct {
	PID     int       `json:"pid"`
	Addr    string    `json:"addr"`
	Started time.Time `json:"started"`
	Version string    `json:"version"`
}

func runfilePath() string { return filepath.Join(stateDir(), "vera", "verad.json") }

func writeRunfile(addr string) error {
	if err := os.MkdirAll(filepath.Dir(runfilePath()), 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(runfile{PID: os.Getpid(), Addr: addr, Started: time.Now(), Version: version})
	if err != nil {
		return err
	}
	return os.WriteFile(runfilePath(), b, 0o644)
}

// removeRunfile takes the note down only if it is this process's:
// another verad may have written its own since.
func removeRunfile() {
	b, err := os.ReadFile(runfilePath())
	if err != nil {
		return
	}
	var r runfile
	if json.Unmarshal(b, &r) == nil && r.PID != os.Getpid() {
		return
	}
	_ = os.Remove(runfilePath())
}
