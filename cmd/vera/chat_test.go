package main

import (
	"path/filepath"
	"testing"
	"time"
)

func TestChatStoreSurvivesRelaunch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vera-chat.jsonl")
	now := time.Now()

	st := newChatStore(path)
	st.add("owner", "what's waiting?", now)
	st.add("vera", "two cards: T-1, T-2.", now.Add(time.Second))

	// The popup dies; the daemon restarts; the thread is still there.
	st = newChatStore(path)
	turns := st.tail(10)
	if len(turns) != 2 || turns[0].Role != "owner" || turns[1].Text != "two cards: T-1, T-2." {
		t.Fatalf("thread must survive relaunch: %+v", turns)
	}

	if got := st.tail(1); len(got) != 1 || got[0].Role != "vera" {
		t.Fatalf("tail(1) must be the newest turn: %+v", got)
	}
}

func TestChatStoreNoPathStillConverses(t *testing.T) {
	st := newChatStore("")
	st.add("owner", "hi", time.Now())
	if len(st.tail(5)) != 1 {
		t.Fatal("a pathless store must still hold the running thread")
	}
}
