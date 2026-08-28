// The transport boundary.
//
// Everything above this line is Vera talking to a phone. Everything
// below it is how the bytes got there, and there will be more than one
// answer: today a listener on the LAN, tomorrow a Swift sidecar
// speaking AWDL so a hotel access point with client isolation turned on
// stops being the end of the conversation.
//
// The interface is deliberately message-shaped rather than HTTP-shaped.
// An HTTP-shaped interface would smuggle status codes, headers and URL
// paths into every future transport that has none of those things, and
// the peer-to-peer one has none of those things.
//
// Reply is a callback rather than a returned slice because the second
// step of this project is a streamed model response, and a transport
// that can only hand back a finished answer would have to be redesigned
// to carry a partial one.
package main

import "context"

// Message is what the person said.
type Message struct {
	Text string `json:"text"`

	// Which run of conversation this belongs to. The server keeps no
	// history, so it cannot work this out for itself — the phone says.
	// It is what groups exchanges together in agent observability, and
	// it is the seam history will eventually be threaded through.
	Conversation string `json:"conversation,omitempty"`

	// Device is which machine the person spoke to — "seths-mbp" — so
	// the answer can lean on what that machine has reported about
	// where their attention is. A phone leaves it empty.
	Device string `json:"device,omitempty"`
}

// Frame is one piece of the reply. A reply is a sequence of Deltas
// followed by exactly one terminal frame — Done, or Error.
//
// Newline-delimited JSON on the wire: one frame per line. Length
// prefixes would be tidier and impossible to debug with curl, and at
// this stage being able to watch the conversation from a terminal is
// worth more than the bytes.
type Frame struct {
	Delta string `json:"delta,omitempty"`
	Done  bool   `json:"done,omitempty"`
	Error string `json:"error,omitempty"`

	// Run names the work this frame belongs to. Sent once, first, so a
	// phone that loses the connection knows what to reattach to.
	Run string `json:"run,omitempty"`

	// Status is what is happening while nothing is being said. Some
	// work takes minutes, and a silent screen for that long reads as
	// broken — so the wait gets narrated rather than hidden. It
	// replaces whatever status came before it and is never part of
	// the answer.
	Status string `json:"status,omitempty"`

	// ToolCall and ToolResult are the exchange's tool rounds as they
	// happen, so a terminal on the other end can show them as they
	// are — a card per call — rather than as a status line about them.
	// A client that does not know them ignores them.
	ToolCall   *ToolCallFrame   `json:"tool_call,omitempty"`
	ToolResult *ToolResultFrame `json:"tool_result,omitempty"`
	// ToolOutput is what a tool is printing while it runs, in pieces,
	// tied to its call by id. A command that takes a minute is not a
	// minute of silence.
	ToolOutput *ToolOutputFrame `json:"tool_output,omitempty"`

	// Usage rides on the terminal frame: what the exchange spent, so
	// a client with a screen can say so. It is the whole exchange —
	// every round of it, tools included — not the last round.
	Usage *UsageFrame `json:"usage,omitempty"`

	// Ask is a tool the policy will not run without a word from the
	// person. It does not end the exchange: the exchange is parked on
	// the answer, which comes back through POST /ask/{id}, and nothing
	// else arrives until it does. A client that cannot answer should
	// answer "no" rather than leave it hanging.
	Ask *AskFrame `json:"ask,omitempty"`
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

// AskFrame is the question, with everything needed to show it: which
// tool, with what arguments, and the policy's own sentence for why it
// is being asked at all.
type AskFrame struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Args string `json:"args"`
	Text string `json:"text"`
}

// UsageFrame is what an exchange spent. Tokens are what the provider
// counted; CostUSD is what those tokens would cost at API list prices,
// and Priced says whether anybody knew a price at all — zero dollars on
// an unknown model means "not known", not "free", and a client must be
// able to tell the two apart.
type UsageFrame struct {
	Model            string  `json:"model,omitempty"`
	InputTokens      int     `json:"input_tokens,omitempty"`
	OutputTokens     int     `json:"output_tokens,omitempty"`
	CacheReadTokens  int     `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int     `json:"cache_write_tokens,omitempty"`
	CostUSD          float64 `json:"cost_usd,omitempty"`
	Priced           bool    `json:"priced,omitempty"`
}

type ToolResultFrame struct {
	ID         string  `json:"id"`
	Result     string  `json:"result"`
	DurationMs int64   `json:"duration_ms"`
	CostUSD    float64 `json:"cost_usd,omitempty"`
}

// Handler answers one exchange: the peer's message in, reply frames
// out. Returning ends the exchange. It must not be called with a nil
// reply, and reply must not be called after Handler returns.
type Handler func(ctx context.Context, msg Message, reply func(Frame) error) error

// Transport carries exchanges between this machine and a paired peer.
type Transport interface {
	// Name is what the log calls it: "lan", later "peer".
	Name() string

	// Hints are addresses a peer could try right now, for the pairing
	// code. They are hints and nothing more — a transport that needs no
	// address (peer-to-peer does not) returns none, and a phone that
	// has paired once must survive every one of these changing.
	Hints() []string

	// Serve blocks until ctx is done, handing each exchange to h.
	Serve(ctx context.Context, h Handler) error
}
