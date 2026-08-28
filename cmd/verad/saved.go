// The daemon's own model, chosen at runtime rather than at startup.
//
// A conversation's model is kept because a conversation outlives the
// process (picks.go). This is the other half: the model everything
// with no conversation of its own runs on — the next chat, dictation,
// the fleet's supervisor — chosen from the picker and kept for the
// same reason. Somebody who moved Vera onto opus this morning did not
// mean "until this daemon restarts".
//
// It sits between the flag and the profile. A --model on the command
// line is somebody typing right now and still wins; the profile is a
// default about what this agent is, and a saved choice is a person
// overruling it. See pick.go for the whole order.
//
// One small JSON file, rewritten whole, next to models.json.
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Default is the daemon's own model, kept on disk.
type Default struct {
	Path string

	mu     sync.Mutex
	loaded bool
	pick   Pick
}

type storedDefault struct {
	Pick
	At time.Time `json:"at"`
}

// Get is what was chosen, if anything was.
func (d *Default) Get() (Pick, bool) {
	if d == nil {
		return Pick{}, false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.load()
	if d.pick.empty() {
		return Pick{}, false
	}
	return d.pick, true
}

// Set records it; an empty pick forgets it and puts the daemon back on
// whatever the profile or the flag said.
func (d *Default) Set(p Pick) error {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.load()
	d.pick = p
	return d.save()
}

// load reads the file once. A file that will not parse is treated as
// empty, for the same reason picks.go treats one that way: a daemon
// on the profile's model is a smaller loss than a daemon that will
// not start.
func (d *Default) load() {
	if d.loaded {
		return
	}
	d.loaded = true
	b, err := os.ReadFile(d.Path)
	if err != nil {
		return
	}
	var got storedDefault
	if json.Unmarshal(b, &got) == nil {
		d.pick = got.Pick
	}
}

func (d *Default) save() error {
	if d.Path == "" {
		return nil
	}
	if d.pick.empty() {
		if err := os.Remove(d.Path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(d.Path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(storedDefault{Pick: d.pick, At: time.Now()}, "", "  ")
	if err != nil {
		return err
	}
	tmp := d.Path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, d.Path)
}
