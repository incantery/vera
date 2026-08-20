// Capability providers: the things Vera can hand work to, as Vera sees
// them — a name, whether it is here, and what it says it can do.
//
// Rook is the first. Today the answer to "what can you do" is empty,
// because rook has no discovery surface to ask; what exists is the
// detection, and the shape the answer will take when it does. Vera does
// not import rook, does not know it needs tmux, and does not care — the
// day rook answers a capability query, this list fills in and nothing
// above it moves.
package main

import (
	"os"
	"os/exec"
)

// ProviderStatus is one provider as /status reports it.
type ProviderStatus struct {
	Name         string   `json:"name"`
	Installed    bool     `json:"installed"`
	Detail       string   `json:"detail,omitempty"`
	Capabilities []string `json:"capabilities"`
}

// detectProviders looks for what is installed. It is cheap enough to run
// on every /status, and running it every time means "installed" is a
// fact about now rather than about startup.
func detectProviders() []ProviderStatus {
	return []ProviderStatus{detectRook()}
}

func detectRook() ProviderStatus {
	p := ProviderStatus{Name: "rook", Capabilities: []string{}}
	path, err := exec.LookPath("rook")
	if err != nil {
		p.Detail = "not on PATH"
		return p
	}
	p.Installed = true
	p.Detail = path
	if sock := os.Getenv("ROOK_SOCK"); sock != "" {
		p.Detail += " (running: " + sock + ")"
	}
	// What the adapter in rook.go actually delivers. Listed here so the
	// Connections view and a future router read the same answer.
	p.Capabilities = []string{"terminal.focus"}
	return p
}
