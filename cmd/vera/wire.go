package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/incantery/vera/fleet"
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

	// The exchange's tool rounds, as they happen, so the terminal can
	// draw a card per call instead of a status line about one.
	ToolCall   *ToolCallFrame   `json:"tool_call,omitempty"`
	ToolResult *ToolResultFrame `json:"tool_result,omitempty"`
	// ToolOutput is what a tool is printing while it runs, in pieces.
	ToolOutput *ToolOutputFrame `json:"tool_output,omitempty"`
	// Ask is a tool verad will not run without a word from the person.
	// The exchange is parked on it: nothing else arrives until the
	// answer goes back through POST /ask/{id}.
	Ask *AskFrame `json:"ask,omitempty"`
}

type AskFrame struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Args string `json:"args"`
	Text string `json:"text"`
}

type ToolCallFrame struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Args string `json:"args"`
}

type ToolOutputFrame struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

type ToolResultFrame struct {
	ID         string  `json:"id"`
	Result     string  `json:"result"`
	DurationMs int64   `json:"duration_ms"`
	CostUSD    float64 `json:"cost_usd,omitempty"`
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

// --- the client: the phone's wire, from a terminal ----------------------

// chatClient is what every screenless verb and the terminal share. It
// is a client like the phone is a client — bearer token, JSON in,
// ndjson out — and knows nothing of verad's internals.
type chatClient struct {
	base, secret, device string
	http                 http.Client
}

func (c *chatClient) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, r)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.secret)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, errors.New(strings.TrimSpace(string(msg)))
	}
	return resp, nil
}

func (c *chatClient) getJSON(ctx context.Context, path string, v any) error {
	resp, err := c.do(ctx, "GET", path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(v)
}

func (c *chatClient) status(ctx context.Context) (*Status, error) {
	var s Status
	return &s, c.getJSON(ctx, "/status?device="+url.QueryEscape(c.device), &s)
}

func (c *chatClient) tasks(ctx context.Context) ([]fleet.View, error) {
	var v []fleet.View
	return v, c.getJSON(ctx, "/fleet", &v)
}

func (c *chatClient) post(ctx context.Context, path string, body any) error {
	resp, err := c.do(ctx, "POST", path, body)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// answer carries one word back to a tool call that is waiting on it.
func (c *chatClient) answer(ctx context.Context, id, choice string) error {
	return c.post(ctx, "/ask/"+url.PathEscape(id), map[string]string{"choice": choice})
}

// openSay starts an exchange. It is separate from reading it because
// an agent.Agent must fail before it returns a channel if the call
// could not start at all — a wrong secret is not a mid-stream error.
func (c *chatClient) openSay(ctx context.Context, text, conversation string) (io.ReadCloser, error) {
	resp, err := c.do(ctx, "POST", "/say", Message{Text: text, Conversation: conversation, Device: c.device})
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

// streamFrames hands each ndjson frame to fn until the terminal one.
func streamFrames(r io.Reader, fn func(Frame)) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		var f Frame
		if json.Unmarshal(sc.Bytes(), &f) != nil {
			continue
		}
		fn(f)
		if f.Done || f.Error != "" {
			return nil
		}
	}
	return sc.Err()
}

// say is openSay and streamFrames together — what a verb without a
// screen wants.
func (c *chatClient) say(ctx context.Context, text, conversation string, fn func(Frame)) error {
	body, err := c.openSay(ctx, text, conversation)
	if err != nil {
		return err
	}
	defer body.Close()
	return streamFrames(body, fn)
}
