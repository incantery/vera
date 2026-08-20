// Rook's attention feed: vera's half of a contract rook defines
// (docs/attention.md in the rook repo). Rook renders a jsonl file —
// status bar, session picker, preview cards — without knowing who
// wrote it; vera is the first publisher. What vera publishes is the
// set the morning report calls "waiting on you": open cards parked in
// the waiting column with an ask. The file is the current set, not a
// log: rewritten atomically when the set changes, and re-stamped
// hourly so rook's 24h staleness guard never drops a living
// publisher's items.
package main

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/incantery/vera/transcript"
)

// attentionItem is one line of the feed, exactly as rook reads it.
type attentionItem struct {
	Dir      string    `json:"dir,omitempty"`
	Kind     string    `json:"kind"`
	Headline string    `json:"headline"`
	At       time.Time `json:"at"`
	Source   string    `json:"source"`
}

func defaultAttentionPath() string {
	return statePath("attention.jsonl")
}

type attentionSystem struct {
	s    *server
	path string // "" disables publishing
	last string // last canonical body published this process
}

func (*attentionSystem) Name() string { return "attention" }

// renderAttention builds the current set, deterministically ordered,
// timestamps unset — the canonical body must not churn on the clock.
func renderAttention(tasks []task) []attentionItem {
	var items []attentionItem
	for i := range tasks {
		t := &tasks[i]
		if !t.open() || t.Col != "waiting" || t.Ask == "" {
			continue
		}
		items = append(items, attentionItem{
			Dir:      t.Workspace,
			Kind:     "waiting",
			Headline: fmt.Sprintf("%s %s — %s", t.ID, transcript.Snip(t.Title, 40), transcript.Snip(t.Ask, 60)),
			Source:   "vera",
		})
	}
	sort.Slice(items, func(a, b int) bool { return items[a].Headline < items[b].Headline })
	return items
}

func canonical(items []attentionItem) string {
	var b strings.Builder
	for _, it := range items {
		line, _ := json.Marshal(it)
		b.Write(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// spendItem is the day's ledger as one info line for rook's subdued
// chrome: kind "spend" never counts as attention (only "waiting"
// does) — surfaces that want it, render it.
func (a *attentionSystem) spendItem(now time.Time) attentionItem {
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	workers, vera := a.s.spendWindow(midnight, now)
	return attentionItem{
		Kind:     "spend",
		Headline: fmt.Sprintf("$%.2f today", workers+vera),
		Source:   "vera",
	}
}

func (a *attentionSystem) Tick(w *World) []Action {
	if a.path == "" {
		return nil
	}
	items := append(renderAttention(w.Tasks), a.spendItem(w.Now))
	body := canonical(items)
	if body == a.last && a.fresh(w.Now) {
		return nil
	}
	h := fnv.New64a()
	h.Write([]byte(body))
	return []Action{{
		Key: fmt.Sprintf("attention/%x", h.Sum64()), Free: true,
		Reason: "publish the waiting-on-you set to rook's attention feed",
		Run:    func() { a.publish(items, body) },
	}}
}

// fresh says the file on disk is recent enough that rook still trusts
// it; an hour-old file gets re-stamped even when nothing changed.
func (a *attentionSystem) fresh(now time.Time) bool {
	fi, err := os.Stat(a.path)
	return err == nil && now.Sub(fi.ModTime()) < time.Hour
}

// publish rewrites the feed atomically. An empty set writes an empty
// file — a resolved ask must leave the bar, not linger on it.
func (a *attentionSystem) publish(items []attentionItem, body string) {
	if err := os.MkdirAll(filepath.Dir(a.path), 0o755); err != nil {
		return
	}
	now := time.Now()
	var b strings.Builder
	for i := range items {
		items[i].At = now
		line, _ := json.Marshal(items[i])
		b.Write(line)
		b.WriteByte('\n')
	}
	tmp := a.path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return
	}
	if err := os.Rename(tmp, a.path); err != nil {
		return
	}
	a.last = body
}
