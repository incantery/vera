// Package responses is OpenAI's Responses API, behind mote's Provider.
//
// It exists for one sentence in models.go: OpenAI's gpt-5.6 family
// refuses a reasoning_effort other than none when there are function
// tools on a **chat completion**. That is a fact about the wire, not
// about the model — the same model on /v1/responses takes the dial
// with the same tools in the request. mote speaks chat completions,
// which is the right default for the dozens of endpoints that speak
// its shape and nothing else, so the second wire lives here rather
// than there: a model whose table row asks for it is reached through
// this, and everything else is reached the way it always was.
//
// What is different from mote's OpenAI provider, beyond the path:
//
//   - The dial is reasoning.effort rather than reasoning_effort, and
//     the reasoning SUMMARY is a separate request for the summary
//     rather than something that arrives whether or not you wanted it.
//   - A turn is a list of typed items, not a list of messages: a tool
//     call is an item, a tool result is an item, and the model's
//     reasoning is an item too.
//   - That last one is why this asks for store:false and
//     reasoning.encrypted_content. A reasoning model that called a
//     tool has to be handed its own reasoning back on the round that
//     answers the tool, or it starts the thought again from nothing.
//     The alternative is letting OpenAI keep the conversation and
//     quoting previous_response_id at it, and a daemon that already
//     keeps its own history has no business storing a second copy on
//     somebody else's disk. The blob rides on provider.Message.Raw,
//     opaque, exactly as an Anthropic thinking signature does.
//
// Of a Request's hints it honours two: Effort becomes reasoning.effort,
// and ThinkingDisplay decides whether a summary of the reasoning is
// asked for. Thinking and CacheSystem are ignored — there is no
// separate switch for reasoning here (effort none is the switch), and
// OpenAI caches long prompts by itself with no way to be asked.
package responses

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/incantery/mote/provider"
	"github.com/incantery/mote/tool"
)

// Wire is one Responses endpoint.
type Wire struct {
	// Model is used when a Request does not name one.
	Model string
	// HTTP is the client to send with. Nil is http.DefaultClient.
	HTTP *http.Client

	base, key string
}

// New is the base URL and the key. An empty base is OpenAI's own.
func New(base, key string) *Wire {
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	return &Wire{base: strings.TrimRight(base, "/"), key: key}
}

// request is the body.
type request struct {
	Model           string       `json:"model"`
	Stream          bool         `json:"stream"`
	Store           bool         `json:"store"`
	Instructions    string       `json:"instructions,omitempty"`
	Input           []any        `json:"input"`
	Tools           []toolDef    `json:"tools,omitempty"`
	MaxOutputTokens int          `json:"max_output_tokens,omitempty"`
	Reasoning       *reasoningIn `json:"reasoning,omitempty"`
	Include         []string     `json:"include,omitempty"`
}

// reasoningIn is the dial and whether the working is shown. Summary is
// omitted rather than sent empty: an endpoint asked for a summary of
// reasoning it was told not to do has been asked two things at once.
type reasoningIn struct {
	Effort  string `json:"effort,omitempty"`
	Summary string `json:"summary,omitempty"`
}

// toolDef is a tool as this API wants it: flat. Chat completions nests
// the name and the schema under "function"; here they are the item.
type toolDef struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

func tools(in []tool.Definition) []toolDef {
	if len(in) == 0 {
		return nil
	}
	out := make([]toolDef, 0, len(in))
	for _, t := range in {
		out = append(out, toolDef{
			Type:        "function",
			Name:        t.Function.Name,
			Description: t.Function.Description,
			Parameters:  t.Function.Parameters,
		})
	}
	return out
}

// item is one thing in the input list. The union is wide and this is
// the part of it a tool-using conversation writes: a message, a call,
// and a call's answer. A reasoning item is not here — it goes back
// exactly as it arrived, see thought.
type item struct {
	Type    string    `json:"type"`
	Role    string    `json:"role,omitempty"`
	Content []content `json:"content,omitempty"`

	// A function call, and the answer to one.
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	Output    string `json:"output,omitempty"`
}

type content struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// input is the conversation as a list of items.
//
// The one thing a straight translation would get wrong: an assistant
// turn's kept reasoning goes back in FRONT of the text and the calls
// it led to. That is where it was, and it is where the model expects
// to find it.
func input(msgs []provider.Message) []any {
	out := make([]any, 0, len(msgs)+1)
	for _, m := range msgs {
		switch m.Role {
		case provider.RoleAssistant:
			out = append(out, thought(m.Raw)...)
			if strings.TrimSpace(m.Text) != "" {
				out = append(out, item{
					Type:    "message",
					Role:    "assistant",
					Content: []content{{Type: "output_text", Text: m.Text}},
				})
			}
			for _, c := range m.Calls {
				out = append(out, item{
					Type:      "function_call",
					CallID:    c.ID,
					Name:      c.Name,
					Arguments: arguments(c.Arguments),
				})
			}
		case provider.RoleTool:
			// Nothing here marks a tool result as a failure, so the
			// text says it, the way it does on the other wire.
			out = append(out, item{Type: "function_call_output", CallID: m.CallID, Output: m.Text})
		default:
			out = append(out, item{
				Type:    "message",
				Role:    "user",
				Content: []content{{Type: "input_text", Text: m.Text}},
			})
		}
	}
	return out
}

// thought is the reasoning a previous assistant turn kept, as items for
// the next request.
//
// They go back BYTE FOR BYTE as they arrived, which is why they are
// raw: a reasoning item carries an id, a summary array that is required
// even when it is empty, and the encrypted block this endpoint will
// only accept in the shape it issued it. Nothing here has any business
// rewriting that. Raw that will not parse — somebody else's provider,
// or a session file that was edited — costs the reasoning and not the
// turn.
func thought(raw json.RawMessage) []any {
	if len(raw) == 0 {
		return nil
	}
	var kept []json.RawMessage
	if json.Unmarshal(raw, &kept) != nil {
		return nil
	}
	out := make([]any, 0, len(kept))
	for _, k := range kept {
		if kindOf(k) == "reasoning" {
			out = append(out, k)
		}
	}
	return out
}

// kindOf is one item's type, without decoding the rest of it.
func kindOf(raw json.RawMessage) string {
	var head struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &head) != nil {
		return ""
	}
	return head.Type
}

// effort is the Request's dial in this endpoint's words. The two above
// high are high — there is no word for them here, and working slightly
// less hard beats refusing the request. An empty Effort sends no
// reasoning object at all, and anything else is passed through as the
// caller wrote it, "none" included.
func effort(e provider.Effort) string {
	switch e {
	case provider.EffortXHigh, provider.EffortMax:
		return string(provider.EffortHigh)
	}
	return string(e)
}

// reasoning is what to ask for. Thinking is deliberately not consulted:
// on this wire there is no separate switch for it — reasoning is turned
// off by asking for effort none — so a Request that says both would
// have to have one of them ignored, and the dial is the one somebody
// typed.
func reasoning(req provider.Request) *reasoningIn {
	e := effort(req.Effort)
	if e == "" {
		return nil
	}
	r := &reasoningIn{Effort: e}
	if e != "none" && req.ThinkingDisplay != provider.DisplayOmitted {
		r.Summary = "auto"
	}
	return r
}

// Stream is one call to /responses.
func (w *Wire) Stream(ctx context.Context, req provider.Request, fn func(provider.Event)) (provider.Usage, error) {
	var used provider.Usage

	body := request{
		Model:           w.model(req),
		Stream:          true,
		Store:           false,
		Instructions:    req.System,
		Input:           input(req.Messages),
		Tools:           tools(req.Tools),
		MaxOutputTokens: req.MaxTokens,
		Reasoning:       reasoning(req),
		// Always, because store is always false: a reasoning item is
		// only accepted back on the next round if it comes with the
		// encrypted block, and it only comes with one if it was asked
		// for here. A turn that did no reasoning has none to send and
		// this costs it nothing.
		Include: []string{"reasoning.encrypted_content"},
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return used, err
	}

	r, err := http.NewRequestWithContext(ctx, http.MethodPost, w.base+"/responses", bytes.NewReader(buf))
	if err != nil {
		return used, err
	}
	r.Header.Set("Content-Type", "application/json")
	if w.key != "" {
		r.Header.Set("Authorization", "Bearer "+w.key)
	}

	client := w.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	res, err := client.Do(r)
	if err != nil {
		return used, err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
		return used, fmt.Errorf("the model answered %d: %s", res.StatusCode, strings.TrimSpace(string(detail)))
	}
	return w.read(res.Body, fn)
}

func (w *Wire) model(req provider.Request) string {
	if req.Model != "" {
		return req.Model
	}
	return w.Model
}

// event is one server-sent event's payload. Every one of them names its
// own type, so the `event:` line is not read: the JSON says.
type event struct {
	Type    string          `json:"type"`
	Delta   string          `json:"delta"`
	Refusal string          `json:"refusal"`
	Item    json.RawMessage `json:"item"`
	Message string          `json:"message"`
	Code    string          `json:"code"`

	Response *struct {
		Model             string `json:"model"`
		Status            string `json:"status"`
		IncompleteDetails *struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
		Usage *struct {
			InputTokens        int `json:"input_tokens"`
			InputTokensDetails struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"input_tokens_details"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	} `json:"response"`
}

// read is the stream: text and reasoning summary as they come, tool
// calls and reasoning blocks taken whole from the item that finished,
// and the terminal response event read for what the turn cost.
func (w *Wire) read(body io.Reader, fn func(provider.Event)) (provider.Usage, error) {
	var used provider.Usage
	var calls []call
	var kept []json.RawMessage

	scan := bufio.NewScanner(body)
	scan.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scan.Scan() {
		payload, ok := strings.CutPrefix(strings.TrimSpace(scan.Text()), "data:")
		if !ok {
			continue
		}
		payload = strings.TrimSpace(payload)
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var ev event
		if json.Unmarshal([]byte(payload), &ev) != nil {
			continue
		}
		switch ev.Type {
		case "response.output_text.delta":
			if ev.Delta != "" {
				fn(provider.Delta(ev.Delta))
			}
		case "response.reasoning_summary_text.delta":
			if ev.Delta != "" {
				fn(provider.Thought(ev.Delta))
			}
		case "response.refusal.done":
			// It happened and it was paid for, so it is not an error
			// from Stream — but the person should see it.
			fn(provider.Fail(ev.Refusal))
		case "response.output_item.done":
			switch kindOf(ev.Item) {
			case "function_call":
				var c call
				if json.Unmarshal(ev.Item, &c) == nil {
					calls = append(calls, c)
				}
			case "reasoning":
				kept = append(kept, ev.Item)
			}
		case "error", "response.failed":
			return used, fmt.Errorf("the model failed: %s", failure(ev))
		case "response.completed", "response.incomplete":
			r := ev.Response
			if r == nil {
				continue
			}
			used.Model = r.Model
			used.StopReason = r.Status
			if r.IncompleteDetails != nil && r.IncompleteDetails.Reason != "" {
				used.StopReason = r.IncompleteDetails.Reason
			}
			if r.Usage != nil {
				cached := r.Usage.InputTokensDetails.CachedTokens
				// input_tokens counts the cached ones too; Usage says
				// each token once.
				used.Input = r.Usage.InputTokens - cached
				used.CacheRead = cached
				used.Output = r.Usage.OutputTokens
			}
		}
	}
	if err := scan.Err(); err != nil {
		return used, err
	}

	for _, c := range calls {
		fn(provider.Calling(c.CallID, c.Name, arguments(c.Arguments)))
	}
	// Last, and only when there was something to keep: what this turn
	// hands back on the next one.
	if len(kept) > 0 {
		if raw, err := json.Marshal(kept); err == nil {
			fn(provider.Keeping(raw))
		}
	}
	return used, nil
}

// call is a finished function_call item, which is the only output item
// this reads for its fields rather than keeping whole.
type call struct {
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// failure is the sentence an error event puts on Stream's error. The
// shape differs between the bare `error` event and the one wrapped in a
// finished response, and a caller wants whichever one arrived.
func failure(ev event) string {
	if ev.Response != nil && ev.Response.Error != nil && ev.Response.Error.Message != "" {
		return ev.Response.Error.Message
	}
	if ev.Message != "" {
		return ev.Message
	}
	if ev.Code != "" {
		return ev.Code
	}
	return "no reason given"
}

// arguments is a call's JSON, made safe to send: a model that asked for
// a tool with no arguments sends nothing at all, and an empty string is
// not JSON.
func arguments(s string) string {
	if s == "" {
		return "{}"
	}
	return s
}
