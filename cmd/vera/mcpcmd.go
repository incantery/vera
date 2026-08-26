// `vera mcp`: what other people's tools she can reach.
//
// A profile declares its MCP servers in mcp.toml, and verad connects
// to them at startup. The file says what was declared; only a
// connected server says what it actually offers, and under which
// name — so this asks verad rather than reading the file, and prints
// each tool under the name the model sees and a policy rule has to be
// written against.
package main

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"
	"time"
)

// Servers mirrors verad's reply, decoding only what this prints.
type Servers struct {
	Dir  string       `json:"dir"`
	List []ServerInfo `json:"servers"`
}

type ServerInfo struct {
	Name      string     `json:"name"`
	Transport string     `json:"transport"`
	Where     string     `json:"where"`
	Says      string     `json:"says,omitempty"`
	Connected bool       `json:"connected"`
	Error     string     `json:"error,omitempty"`
	Tools     []ToolInfo `json:"tools"`
}

type ToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func runMCP(args []string) error {
	base, err := ensure()
	if err != nil {
		return err
	}
	id, err := loadIdentity(identityPath())
	if err != nil {
		return err
	}
	c := &chatClient{base: base, secret: id.Secret, device: id.Name + " (cli)"}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var s Servers
	if err := c.getJSON(ctx, "/mcp", &s); err != nil {
		return err
	}
	writeServers(os.Stdout, s)
	return nil
}

func writeServers(w *os.File, s Servers) {
	if len(s.List) == 0 {
		where := s.Dir
		if where == "" {
			where = "her profile"
		}
		fmt.Fprintln(w, "no MCP servers — nothing declared in "+where+"/mcp.toml")
		return
	}
	for i, srv := range s.List {
		if i > 0 {
			fmt.Fprintln(w)
		}
		fmt.Fprintf(w, "%s  (%s) %s\n", srv.Name, srv.Transport, srv.Where)
		switch {
		case srv.Error != "":
			fmt.Fprintln(w, "  did not answer: "+srv.Error)
			continue
		case !srv.Connected:
			fmt.Fprintln(w, "  not connected")
			continue
		}
		if srv.Says != "" {
			fmt.Fprintln(w, "  calls itself "+srv.Says)
		}
		if len(srv.Tools) == 0 {
			fmt.Fprintln(w, "  offers no tools")
			continue
		}
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		for _, t := range srv.Tools {
			fmt.Fprintf(tw, "  %s\t%s\n", t.Name, trim(oneLine(t.Description), 80))
		}
		tw.Flush()
	}
}
