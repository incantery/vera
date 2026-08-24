package main

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// The whole point: the work is not the connection's property.
func TestWorkOutlivesTheCallerThatAskedForIt(t *testing.T) {
	started := make(chan struct{})
	finished := make(chan struct{})

	slow := func(ctx context.Context, msg Message, reply func(Frame) error) error {
		close(started)
		// Long enough that the caller is certainly gone first.
		time.Sleep(150 * time.Millisecond)
		_ = reply(Frame{Delta: "finished anyway"})
		_ = reply(Frame{Done: true})
		close(finished)
		return nil
	}

	base, id := serveLAN(t, slow)

	// Ask, then hang up almost immediately.
	ctx, hangUp := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "POST", base+"/say", strings.NewReader(`{"text":"go"}`))
	req.Header.Set("Authorization", "Bearer "+id.Secret)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}

	var run string
	scan := bufio.NewScanner(res.Body)
	if scan.Scan() {
		var f Frame
		_ = json.Unmarshal(scan.Bytes(), &f)
		run = f.Run
	}
	if run == "" {
		t.Fatal("no run id in the first frame; there is nothing to reattach to")
	}
	<-started
	hangUp()
	res.Body.Close()

	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("the work died with the connection")
	}

	// And the answer is still there for whoever comes back.
	req2, _ := http.NewRequest("GET", base+"/resume?run="+run+"&from=0", nil)
	req2.Header.Set("Authorization", "Bearer "+id.Secret)
	res2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer res2.Body.Close()

	var text strings.Builder
	scan2 := bufio.NewScanner(res2.Body)
	for scan2.Scan() {
		var f Frame
		_ = json.Unmarshal(scan2.Bytes(), &f)
		text.WriteString(f.Delta)
	}
	if !strings.Contains(text.String(), "finished anyway") {
		t.Fatalf("resumed stream was %q", text.String())
	}
}

func TestResumeSkipsWhatWasAlreadySeen(t *testing.T) {
	run := newRun("r1")
	run.append(Frame{Run: "r1"})
	run.append(Frame{Delta: "one "})
	run.append(Frame{Delta: "two "})
	run.append(Frame{Delta: "three"})
	run.append(Frame{Done: true})

	var got []Frame
	if err := run.follow(context.Background(), 3, func(f Frame) error {
		got = append(got, f)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Frames 3 and 4 only: "three" and the terminator.
	if len(got) != 2 || got[0].Delta != "three" {
		t.Fatalf("resumed with %+v — an answer would be read twice", got)
	}
}

func TestSeveralWatchersSeeTheSameRun(t *testing.T) {
	run := newRun("r2")
	var wg sync.WaitGroup
	texts := make([]string, 3)
	for i := range texts {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var b strings.Builder
			_ = run.follow(context.Background(), 0, func(f Frame) error {
				b.WriteString(f.Delta)
				return nil
			})
			texts[i] = b.String()
		}(i)
	}
	time.Sleep(20 * time.Millisecond)
	run.append(Frame{Delta: "hello"})
	run.append(Frame{Done: true})
	wg.Wait()

	for i, got := range texts {
		if got != "hello" {
			t.Fatalf("watcher %d saw %q", i, got)
		}
	}
}

// A watcher waiting on a run that will never speak again is worse than
// an error.
func TestARunThatDiesSilentlyStillEnds(t *testing.T) {
	run := newRun("r3")
	run.append(Frame{Delta: "half an ans"})
	run.finish()

	done := make(chan struct{})
	go func() {
		_ = run.follow(context.Background(), 0, func(Frame) error { return nil })
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("follow never returned on a run that stopped without finishing")
	}
}

func TestFinishedRunsAreEvictedButRunningOnesNever(t *testing.T) {
	rs := newRuns()
	rs.max = 3
	rs.keep = time.Millisecond

	live := rs.start() // never finishes
	for range 10 {
		r := rs.start()
		r.append(Frame{Done: true})
		time.Sleep(2 * time.Millisecond)
	}

	if rs.find(live.ID) == nil {
		t.Fatal("a run still in flight was evicted — that is the original bug wearing a hat")
	}
	if n := rs.inFlight(); n != 1 {
		t.Fatalf("%d runs in flight, want 1", n)
	}
}
