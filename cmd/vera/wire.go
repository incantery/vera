package main

import (
	"encoding/json"
	"os"
	"strings"
	"time"
)

// The phone's wire, as the chat reads it. These mirror verad's types
// field for field but decode only what the chat renders; verad may
// send more and this stays correct. Keeping them separate is the
// point: `vera` is a client like the phone, not a friend of the
// server's internals.

type Message struct {
	Text         string `json:"text"`
	Conversation string `json:"conversation,omitempty"`
	Device       string `json:"device,omitempty"`
}

type Frame struct {
	Delta  string `json:"delta,omitempty"`
	Done   bool   `json:"done,omitempty"`
	Error  string `json:"error,omitempty"`
	Run    string `json:"run,omitempty"`
	Status string `json:"status,omitempty"`
}

type Identity struct {
	Peer   string `json:"peer"`
	Secret string `json:"secret"`
	Name   string `json:"name"`
}

type Status struct {
	Name         string              `json:"name"`
	Mind         string              `json:"mind"`
	Since        time.Time           `json:"since"`
	RunsInFlight int                 `json:"runs_in_flight"`
	Devices      []DeviceStatus      `json:"devices"`
	Integrations []IntegrationStatus `json:"integrations"`
}

type DeviceStatus struct {
	Name       string         `json:"name"`
	Fresh      bool           `json:"fresh"`
	Focus      *ObservedApp   `json:"focus,omitempty"`
	FocusSince *time.Time     `json:"focus_since,omitempty"`
	Terminal   *TerminalFocus `json:"terminal,omitempty"`
}

type ObservedApp struct {
	Name     string `json:"name"`
	BundleID string `json:"bundle_id,omitempty"`
}

type TerminalFocus struct {
	Session string `json:"session"`
	Window  string `json:"window"`
	Command string `json:"command,omitempty"`
	Title   string `json:"title,omitempty"`
	Path    string `json:"path,omitempty"`
	Agent   string `json:"agent,omitempty"`
}

// Describe matches verad's phrasing so the belief panel reads like
// the model's preface.
func (t TerminalFocus) Describe() string {
	where := t.Session + ":" + t.Window
	switch {
	case t.Agent == "claude-code":
		title := strings.TrimSpace(strings.TrimPrefix(t.Title, "✳"))
		if title == "" {
			return "a Claude Code session (" + where + ")"
		}
		return "Claude Code session \"" + title + "\" (" + where + ")"
	case t.Command != "":
		return t.Command + " in " + shortPath(t.Path) + " (" + where + ")"
	default:
		return "a shell in " + shortPath(t.Path) + " (" + where + ")"
	}
}

type IntegrationStatus struct {
	Name      string `json:"name"`
	Connected bool   `json:"connected"`
}

func shortPath(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 && i < len(p)-1 {
		return p[i+1:]
	}
	return p
}

func trim(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func firstSentence(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, ". "); i > 0 {
		return s[:i]
	}
	return s
}

func loadIdentity(path string) (Identity, error) {
	var id Identity
	b, err := os.ReadFile(path)
	if err != nil {
		return id, err
	}
	return id, json.Unmarshal(b, &id)
}
