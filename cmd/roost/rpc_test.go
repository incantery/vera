package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	roostv1 "github.com/incantery/rook-host/engine/gen/roost/v1"
	"github.com/incantery/rook-host/engine/gen/roost/v1/roostv1connect"
)

// The Say RPC and the REST endpoint are one rail: refusals arrive in
// Connect's vocabulary with the server's own words intact.
func TestSayRPCTranslatesRefusals(t *testing.T) {
	dir := t.TempDir()
	writeTranscript(t, dir, "-repo-alpha", "sess-live", time.Now().Add(-time.Minute))
	s := testServer(t, dir)
	r := &roostRPC{s: s}
	ctx := context.Background()

	_, err := r.Say(ctx, connect.NewRequest(&roostv1.SayRequest{Id: "stranger", Text: "hi"}))
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("a gone agent must be NotFound: %v", err)
	}
	_, err = r.Say(ctx, connect.NewRequest(&roostv1.SayRequest{Id: "sess-live", Text: "   "}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("an empty message must be InvalidArgument: %v", err)
	}
	_, err = r.Say(ctx, connect.NewRequest(&roostv1.SayRequest{Id: "sess-live", Text: "hi", Perm: "sudo"}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("a bad perm must be InvalidArgument: %v", err)
	}
}

// Interrupt in Connect's vocabulary: a gone agent is NotFound, a
// quiet one FailedPrecondition.
func TestInterruptRPCTranslatesRefusals(t *testing.T) {
	dir := t.TempDir()
	writeTranscript(t, dir, "-repo-alpha", "sess-live", time.Now().Add(-time.Minute))
	s := testServer(t, dir)
	s.hub = newHub()
	r := &roostRPC{s: s}
	ctx := context.Background()

	_, err := r.Interrupt(ctx, connect.NewRequest(&roostv1.InterruptRequest{Id: "stranger"}))
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("a gone agent must be NotFound: %v", err)
	}
	_, err = r.Interrupt(ctx, connect.NewRequest(&roostv1.InterruptRequest{Id: "sess-live"}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("nothing in flight must be FailedPrecondition: %v", err)
	}
}

// The board stream: a full frame first, another when the board moves
// (here: a capture landing between frames), silence when it does not.
func TestWatchBoardStreamsFramesOnChange(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	writeWorkingTranscript(t, dir, "-repo-alpha", "live-1", "the live work", now)
	s := testServer(t, dir)
	s.hub = newHub()

	mux := http.NewServeMux()
	path, h := roostv1connect.NewRoostServiceHandler(&roostRPC{s: s})
	mux.Handle(path, h)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := roostv1connect.NewRoostServiceClient(srv.Client(), srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream, err := client.WatchBoard(ctx, connect.NewRequest(&roostv1.WatchBoardRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if !stream.Receive() {
		t.Fatalf("no first frame: %v", stream.Err())
	}
	first := stream.Msg()
	if len(first.Tasks) != 1 || first.Fleet.Working != 1 || len(first.Sessions) != 1 {
		t.Fatalf("first frame must carry the whole present: tasks=%d working=%d sessions=%d",
			len(first.Tasks), first.Fleet.Working, len(first.Sessions))
	}
	if first.Tasks[0].Live == nil || first.Tasks[0].Live.State != "working" {
		t.Fatalf("the adopted card must wear its live overlay: %+v", first.Tasks[0].Live)
	}

	// A capture lands; the hub pokes; the next frame carries it.
	if _, err := s.tasks.capture("new backlog", time.Now()); err != nil {
		t.Fatal(err)
	}
	s.hub.notify()
	if !stream.Receive() {
		t.Fatalf("no frame after the capture: %v", stream.Err())
	}
	if len(stream.Msg().Tasks) != 2 {
		t.Fatalf("the capture must ride the next frame: %d tasks", len(stream.Msg().Tasks))
	}
}

// The watch contract a phone will rely on: the first frame is a full
// snapshot; a new turn arrives as a tail delta, not a resend.
func TestWatchAgentStreamsSnapshotThenDelta(t *testing.T) {
	dir := t.TempDir()
	writeTranscript(t, dir, "-repo-alpha", "sess-live", time.Now().Add(-time.Minute))
	s := testServer(t, dir)
	s.hub = newHub()

	mux := http.NewServeMux()
	path, h := roostv1connect.NewRoostServiceHandler(&roostRPC{s: s})
	mux.Handle(path, h)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := roostv1connect.NewRoostServiceClient(srv.Client(), srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream, err := client.WatchAgent(ctx, connect.NewRequest(&roostv1.WatchAgentRequest{Id: "sess-live", Raw: true}))
	if err != nil {
		t.Fatal(err)
	}
	if !stream.Receive() {
		t.Fatalf("no first frame: %v", stream.Err())
	}
	first := stream.Msg()
	if !first.Reset_ || len(first.History) == 0 || first.Agent.Id != "sess-live" {
		t.Fatalf("first frame must be a full snapshot: reset=%v history=%d", first.Reset_, len(first.History))
	}
	base := len(first.History)

	// A new exchange lands in the transcript; the hub pokes; the next
	// frame must carry only the tail.
	p := filepath.Join(dir, "-repo-alpha", "sess-live.jsonl")
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	f.WriteString(`{"type":"user","timestamp":"` + now + `","message":{"role":"user","content":"a fresh question"}}` + "\n" +
		`{"type":"assistant","timestamp":"` + now + `","message":{"role":"assistant","stop_reason":"end_turn","content":[{"type":"text","text":"a fresh answer"}]}}` + "\n")
	f.Close()
	s.hub.notify()

	if !stream.Receive() {
		t.Fatalf("no delta frame: %v", stream.Err())
	}
	delta := stream.Msg()
	if delta.Reset_ {
		t.Fatalf("a grown history must be a delta, not a reset")
	}
	if int(delta.From) > base || len(delta.History) == 0 {
		t.Fatalf("delta from=%d history=%d (base %d)", delta.From, len(delta.History), base)
	}
	last := delta.History[len(delta.History)-1]
	if last.Text != "a fresh answer" {
		t.Fatalf("the tail must end on the new answer: %q", last.Text)
	}
}
