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

	// Status is what is happening while nothing is being said. Some
	// work takes minutes, and a silent screen for that long reads as
	// broken — so the wait gets narrated rather than hidden. It
	// replaces whatever status came before it and is never part of
	// the answer.
	Status string `json:"status,omitempty"`
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
