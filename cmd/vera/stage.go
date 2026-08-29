// What is waiting to go with the next thing you say.
//
// A picture and the words about it are one message, and they are typed
// at different moments: /paste, then a sentence, then Enter. So the
// picture waits here in between.
//
// It waits rather than being sent on its own because a screenshot with
// no words is usually half a thought — and because the exchange that
// carries it is the one that decides who the work goes to. Sending the
// picture early would mean deciding that twice.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/incantery/vera/attach"
)

// expandHome makes `~/Desktop/shot.png` mean what it looks like. A
// path typed into a TUI never met a shell, so nothing has done this
// for it.
func expandHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(path, "~"), "/"))
}

// stage is the pictures typed at but not yet sent. Written from the
// command handler, read from the send, both off the UI goroutine —
// hence the lock. Its zero value is an empty stage.
type stage struct {
	mu   sync.Mutex
	held []attach.Image
}

// add puts one on the stage and says what is now waiting.
func (s *stage) add(im attach.Image) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.held = append(s.held, im)
	return waiting(s.held)
}

// take hands over everything waiting and empties the stage. Called
// once per message: an image goes with exactly one thing you said.
func (s *stage) take() []attach.Image {
	s.mu.Lock()
	defer s.mu.Unlock()
	held := s.held
	s.held = nil
	return held
}

// count is how many are waiting, for a screen that wants to say so.
func (s *stage) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.held)
}

// clear drops what is waiting — /image with nothing to attach after a
// mistake, and what /new does so a fresh conversation starts empty.
func (s *stage) clear() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(s.held)
	s.held = nil
	return n
}

// waiting is the sentence the chat prints after each one. It names
// what is held rather than counting it, because the mistake this
// catches is attaching the wrong picture and not noticing.
func waiting(held []attach.Image) string {
	if len(held) == 0 {
		return "nothing attached"
	}
	names := make([]string, 0, len(held))
	for _, im := range held {
		name := strings.TrimSpace(im.Name)
		if name == "" {
			name = "image"
		}
		names = append(names, name)
	}
	noun := "images"
	if len(names) == 1 {
		noun = "image"
	}
	return strings.Join(names, ", ") + " — " + noun + " going with your next message"
}
