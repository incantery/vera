// Other people's tools.
//
// A profile is a directory a person can read, and mcp.toml is the
// third file in it: the servers this agent may reach, by the name its
// tools will be called after. Vera connects to them at startup and
// every tool every one of them offers lands in the same registry as
// read and write, under the same policy — which, for a supervisor
// whose default is ask, means the first `github__create_issue` stops
// and asks, and the person can write a line in policy.toml to stop
// being asked.
//
// The two decisions here that the file cannot make:
//
//   - the separator. mote's default is `files.read`, which is what a
//     person reads best, and neither wire will carry it: a function
//     name in an OpenAI request body and a tool name in an Anthropic
//     one must match [a-zA-Z0-9_-]{1,64}, and a dot is not in it. Vera
//     has met both, so she says `__` once, before anything connects.
//   - a server that will not answer is a line in the log and a line
//     in `vera mcp`, not a verad that will not start. One broken
//     server in a list of four is not a reason to have no Vera.
package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/incantery/mote/mcp"
)

// mcpSeparator is what goes between the server and the tool in a
// registered name. See the file comment: the dot mote defaults to is
// not a character either wire accepts in a tool name.
const mcpSeparator = "__"

// mcpConnect bounds the connecting, not the connection. A server that
// is slow to start should not hold a startup open for a minute.
const mcpConnect = 30 * time.Second

// Servers is a profile's MCP servers as Vera reports them: what the
// file declared, whether it answered, and what it offers under the
// names the model will use and a policy rule has to be written
// against.
type Servers struct {
	// Dir is the profile directory the file was looked for in.
	Dir  string       `json:"dir"`
	List []ServerInfo `json:"servers"`
}

// ServerInfo is one server, connected or not.
type ServerInfo struct {
	Name      string `json:"name"`
	Transport string `json:"transport"`
	// Where is the command it runs or the endpoint it posts to, as the
	// file wrote it — a `${TOKEN}` is still a `${TOKEN}` here, which is
	// the point of putting the secret in the environment.
	Where string `json:"where"`
	// Says is what the server called itself when it was initialized,
	// which is not the profile's name for it.
	Says      string     `json:"says,omitempty"`
	Connected bool       `json:"connected"`
	Error     string     `json:"error,omitempty"`
	Tools     []ToolInfo `json:"tools"`
}

// ToolInfo is one tool of one server, by the name the model sees.
type ToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// openServers reads the profile's mcp.toml and connects to everything
// in it, registering what each server offers.
//
// A missing file means no servers, which is not an error: most
// profiles have none, and a missing file is how they say so. A file
// that is there and wrong IS an error — a typo in a server's
// declaration should be found when verad starts rather than when a
// model reaches for a tool that was never registered.
//
// Called after Adopt, so the profile has already narrowed the registry
// and Own is the last word about what is in it.
func (h *Hands) openServers(ctx context.Context) error {
	if h == nil {
		return nil
	}
	// Before anything is named, and once: the name a tool is
	// registered under is the name a policy rule is written against
	// and the name that goes on the wire, and those have to agree.
	mcp.Separator = mcpSeparator

	servers, err := mcp.Load(h.Dir)
	if err != nil {
		return err
	}
	if len(servers) == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, mcpConnect)
	defer cancel()

	// One at a time rather than through mcp.Connect, because Connect
	// joins the failures into one error and a person asking `vera mcp`
	// wants to know WHICH server did not answer.
	h.mu.Lock()
	h.servers = servers
	h.failed = map[string]string{}
	h.mu.Unlock()
	for _, s := range servers {
		c, err := mcp.Open(ctx, s, h.registry)
		if err != nil {
			slog.Warn("mcp", "server", s.Name, "where", s.Where(), "error", err.Error())
			h.mu.Lock()
			h.failed[s.Name] = err.Error()
			h.mu.Unlock()
			continue
		}
		h.mu.Lock()
		h.clients = append(h.clients, c)
		h.mu.Unlock()
		slog.Info("mcp", "server", s.Name, "says", c.Says(), "tools", len(c.Tools()))
	}
	// The model's list changed, and it may change again on its own: a
	// server that says its tools changed writes to the same registry
	// from a goroutine of its own.
	h.refreshDefs()
	return nil
}

// closeServers ends every connection. The tools stay registered — a
// call to one then fails with the server's own error, which is more
// use to a model mid-conversation than a tool that vanished.
func (h *Hands) closeServers() {
	if h == nil {
		return
	}
	h.mu.Lock()
	clients := h.clients
	h.clients = nil
	h.mu.Unlock()
	for _, c := range clients {
		if err := c.Close(); err != nil {
			slog.Warn("mcp", "closing", c.Name(), "error", err.Error())
		}
	}
}

// Servers is what `vera mcp` prints: every server the profile
// declared, in the order it declared them, with what it offers now.
func (h *Hands) Servers() Servers {
	if h == nil {
		return Servers{}
	}
	h.mu.Lock()
	servers := h.servers
	failed := h.failed
	live := map[string]*mcp.Client{}
	for _, c := range h.clients {
		live[c.Name()] = c
	}
	h.mu.Unlock()

	out := Servers{Dir: h.Dir, List: []ServerInfo{}}
	for _, s := range servers {
		info := ServerInfo{
			Name:      s.Name,
			Transport: s.Transport(),
			Where:     s.Where(),
			Error:     failed[s.Name],
			Tools:     []ToolInfo{},
		}
		if c, ok := live[s.Name]; ok {
			info.Connected = true
			info.Says = c.Says()
			for _, t := range c.Tools() {
				info.Tools = append(info.Tools, ToolInfo{Name: t.Name(), Description: t.Description()})
			}
		}
		out.List = append(out.List, info)
	}
	return out
}
