package fleet

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Store is the fleet on disk: one directory per task holding the task
// record, its append-only status log, and how much of that log has been
// shown to a person. Vera restarting reads it all back; nothing about a
// task lives only in memory.
//
//	<dir>/<id>/task.json
//	<dir>/<id>/status.log     jsonl, append-only
//	<dir>/<id>/cursor         lines of status.log already presented
//	<dir>/<id>/claude.json    the harness settings the pane was started with
type Store struct {
	Dir string
	mu  sync.Mutex
}

func NewStore(dir string) *Store { return &Store{Dir: dir} }

func (s *Store) taskDir(id string) string { return filepath.Join(s.Dir, id) }

// TaskDir is where a task's files live; the hook settings go here.
func (s *Store) TaskDir(id string) string { return s.taskDir(id) }

// Save writes the task record atomically.
func (s *Store) Save(t *Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := s.taskDir(t.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, "task.json.tmp")
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, "task.json"))
}

// Load reads one task.
func (s *Store) Load(id string) (*Task, error) {
	b, err := os.ReadFile(filepath.Join(s.taskDir(id), "task.json"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNoTask
		}
		return nil, err
	}
	var t Task
	if err := json.Unmarshal(b, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// ErrNoTask: no task by that id.
var ErrNoTask = errors.New("no such task")

// List reads every task, oldest spawned first.
func (s *Store) List() ([]*Task, error) {
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var tasks []*Task
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		t, err := s.Load(e.Name())
		if err != nil {
			continue
		}
		tasks = append(tasks, t)
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].Spawned.Before(tasks[j].Spawned) })
	return tasks, nil
}

// Append adds one status line. Never rewrites.
func (s *Store) Append(id string, st Status) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st.At.IsZero() {
		st.At = time.Now()
	}
	if err := os.MkdirAll(s.taskDir(id), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(s.taskDir(id), "status.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	return err
}

// Statuses reads the whole log, oldest first.
func (s *Store) Statuses(id string) ([]Status, error) {
	f, err := os.Open(filepath.Join(s.taskDir(id), "status.log"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []Status
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var st Status
		if json.Unmarshal(sc.Bytes(), &st) == nil {
			out = append(out, st)
		}
	}
	return out, sc.Err()
}

// Last is the newest status, or nil.
func (s *Store) Last(id string) (*Status, error) {
	all, err := s.Statuses(id)
	if err != nil || len(all) == 0 {
		return nil, err
	}
	return &all[len(all)-1], nil
}

// Cursor is how many status lines a person has been shown. Unread is
// everything past it — the "what changed since you looked" a returning
// phone renders, and the reason the log is never rewritten.
func (s *Store) Cursor(id string) int {
	b, err := os.ReadFile(filepath.Join(s.taskDir(id), "cursor"))
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	return n
}

// Present marks the log read up to n lines.
func (s *Store) Present(id string, n int) error {
	return os.WriteFile(filepath.Join(s.taskDir(id), "cursor"), []byte(strconv.Itoa(n)), 0o644)
}

// Unread is what a person has not yet seen.
func (s *Store) Unread(id string) ([]Status, error) {
	all, err := s.Statuses(id)
	if err != nil {
		return nil, err
	}
	c := s.Cursor(id)
	if c > len(all) {
		c = len(all)
	}
	return all[c:], nil
}
