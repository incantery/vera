package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	motemcp "github.com/incantery/mote/mcp"
	"github.com/incantery/mote/tool"
	"github.com/incantery/vera/home"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// The MCP tests run against a real server — the SDK's, in this
// process, over the streamable HTTP wire. Nothing is mocked: the
// bytes on the socket are the protocol's, and the only thing that is
// not real is that the server is a goroutine rather than somebody's
// npm package. It is mote's own test pattern, from the other side of
// the seam.

// fakeMCP is one server offering one tool, on a URL a profile can
// declare.
func fakeMCP(t *testing.T) string {
	t.Helper()
	s := sdk.NewServer(&sdk.Implementation{Name: "fake", Version: "v9"}, nil)
	s.AddTool(&sdk.Tool{
		Name:        "echo",
		Description: "Say a thing back.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`),
	}, func(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		var v struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(req.Params.Arguments, &v)
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "you said " + v.Text}}}, nil
	})
	handler := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return s }, nil)
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return ts.URL
}

// declare writes an mcp.toml into her profile directory, which is the
// only way a server ever gets there.
func declare(t *testing.T, root, body string) {
	t.Helper()
	dir := filepath.Join(root, filepath.FromSlash(home.ProfileDir))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mcp.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A server in the file is a tool in the registry, under the name the
// model sees — and under a separator both wires will actually carry.
func TestAServerInTheFileIsAToolInTheRegistry(t *testing.T) {
	h, root, _ := newHands(t)
	declare(t, root, "[[servers]]\nname = \"fake\"\nurl = \""+fakeMCP(t)+"\"\n")

	// openHands read the profile before the file was there, so this is
	// the same call startup makes, in the same order: hers first, then
	// other people's.
	if err := h.openServers(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.closeServers)

	want := "fake" + mcpSeparator + "echo"
	tl, ok := h.Tool(want)
	if !ok {
		t.Fatalf("no tool named %q — she has %v", want, h.Names())
	}
	if strings.ContainsAny(want, ".") {
		t.Errorf("a tool name with a dot in it goes nowhere: %q", want)
	}

	// It is in what the model is told, too — a registry nobody sends
	// is a registry nobody can call.
	var told bool
	for _, d := range h.Definitions() {
		if d.Function.Name == want {
			told = true
		}
	}
	if !told {
		t.Errorf("the model was never told about %q", want)
	}

	// And it is decided by the same policy as everything else. The
	// supervisor's default is ask, and nothing in her file has heard
	// of this tool.
	v, _ := h.Decide("c", tool.NewCall("call_1", tl, json.RawMessage(`{"text":"hello"}`)))
	if v.Decision != tool.Ask {
		t.Errorf("a brand new MCP tool was %s, not asked about (%s)", v.Decision, v.Rule)
	}

	// A tool the profile's `tools:` line never named and cannot drop.
	if !h.registry.Owns(want) {
		t.Error("an MCP tool is not the harness's own")
	}

	// It runs: the call goes to the server and the text comes back.
	res, err := tl.Run(context.Background(), json.RawMessage(`{"text":"hello"}`), tool.Handle{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "you said hello") {
		t.Errorf("the server answered %q", res.Text)
	}
}

// What `vera mcp` prints: the file's own words, and what the server
// turned out to offer.
func TestServersReportsWhatSheCanReach(t *testing.T) {
	h, root, _ := newHands(t)
	declare(t, root, "[[servers]]\nname = \"fake\"\nurl = \""+fakeMCP(t)+"\"\n\n"+
		"[[servers]]\nname = \"gone\"\nurl = \"http://127.0.0.1:1/mcp\"\n")
	if err := h.openServers(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.closeServers)

	got := h.Servers()
	if len(got.List) != 2 {
		t.Fatalf("declared two servers, reported %d", len(got.List))
	}
	if !got.List[0].Connected || got.List[0].Says != "fake v9" {
		t.Errorf("the connected server reads back as %+v", got.List[0])
	}
	if len(got.List[0].Tools) != 1 || got.List[0].Tools[0].Name != "fake"+mcpSeparator+"echo" {
		t.Errorf("its tools are %+v", got.List[0].Tools)
	}
	// One server that would not answer does not stop the other, and it
	// says so rather than disappearing.
	if got.List[1].Connected || got.List[1].Error == "" {
		t.Errorf("the dead server reads back as %+v", got.List[1])
	}
	if _, ok := h.Tool("fake" + mcpSeparator + "echo"); !ok {
		t.Error("a broken server took the working one's tools with it")
	}
}

// No file is no servers, which is not an error: most profiles have
// none, and a missing file is how they say so.
func TestNoFileIsNoServers(t *testing.T) {
	h, _, _ := newHands(t)
	before := len(h.Definitions())
	if err := h.openServers(context.Background()); err != nil {
		t.Fatalf("a profile with no mcp.toml failed to start: %v", err)
	}
	if got := len(h.Definitions()); got != before {
		t.Errorf("tools appeared from nowhere: %d → %d", before, got)
	}
	if got := h.Servers(); len(got.List) != 0 {
		t.Errorf("servers appeared from nowhere: %+v", got.List)
	}
}

// A file that is there and wrong is a startup error — a typo a person
// can fix, found now rather than when a model reaches for a tool that
// was never registered.
func TestABrokenFileStopsStartup(t *testing.T) {
	h, root, _ := newHands(t)
	declare(t, root, "[[servers]]\nname = \"fake\"\n")
	err := h.openServers(context.Background())
	if err == nil {
		t.Fatal("a server with neither a command nor a url started fine")
	}
	if !strings.Contains(err.Error(), motemcp.File) {
		t.Errorf("the error does not say which file: %v", err)
	}
}
