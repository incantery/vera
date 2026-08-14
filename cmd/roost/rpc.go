// The Connect service: roost's typed wire, starting where polling
// hurt — WatchAgent streams an agent's whole present, full snapshot
// first, tail deltas after. The same schema a phone client will
// speak; the REST rails keep serving what has not migrated yet.
package main

import (
	"context"
	"errors"
	"hash/fnv"
	"path/filepath"
	"time"

	"connectrpc.com/connect"
	roostv1 "github.com/incantery/rook-host/engine/gen/roost/v1"
	"github.com/incantery/rook-host/engine/transcript"
)

type roostRPC struct {
	s *server
}

// view is one recomputation of the watch payload with the hashes the
// delta diff runs on: one per history message, one for everything
// else.
type view struct {
	resp *roostv1.WatchAgentResponse
	msgs []uint64
	rest uint64
	cwd  string
	gone bool
}

func (r *roostRPC) WatchAgent(ctx context.Context, req *connect.Request[roostv1.WatchAgentRequest], stream *connect.ServerStream[roostv1.WatchAgentResponse]) error {
	s := r.s
	poke, cancel := s.hub.subscribe()
	defer cancel()
	// The slow tick refreshes what disk events cannot announce (age,
	// the working tree) and is the safety net if a watch goes quiet.
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()

	var prevMsgs []uint64
	var prevRest uint64
	first := true
	var tree []*roostv1.TreeFile
	var treeAt time.Time

	for {
		v := s.agentView(req.Msg.Id, req.Msg.Raw)
		if v.gone {
			return connect.NewError(connect.CodeNotFound, errors.New("that agent is gone from the window"))
		}
		// The tree costs two git execs — refresh it on the slow path,
		// not on every mid-turn write.
		if req.Msg.Raw && time.Since(treeAt) > 2*time.Second {
			tree = protoTree(gitTree(v.cwd))
			treeAt = time.Now()
		}
		v.resp.Tree = tree

		from := commonPrefix(prevMsgs, v.msgs)
		grew := len(v.msgs) > len(prevMsgs) || from < len(prevMsgs)
		if first || grew || v.rest != prevRest {
			resp := v.resp
			if first || len(v.msgs) < len(prevMsgs) {
				resp.Reset_ = true
			} else {
				resp.From = int32(from)
				resp.History = resp.History[from:]
			}
			if err := stream.Send(resp); err != nil {
				return err
			}
			prevMsgs, prevRest, first = v.msgs, v.rest, false
		}
		select {
		case <-ctx.Done():
			return nil
		case <-poke:
		case <-tick.C:
		}
	}
}

// commonPrefix: how many leading message hashes still agree.
func commonPrefix(a, b []uint64) int {
	n := min(len(a), len(b))
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

// agentView computes one full watch payload. It is the typed twin of
// handleAgent; when the REST rail retires, this is what remains.
func (s *server) agentView(id string, raw bool) view {
	now := time.Now()
	root, head := s.resolveAgent(id, now)
	if head == nil {
		return view{gone: true}
	}
	agent := &roostv1.Agent{
		Id: root, Title: head.Title, State: string(head.State),
		Dir: filepath.Base(head.Cwd), Branch: head.Branch,
		Tool: head.ToolName, ToolDetail: head.ToolDetail,
		Age: transcript.RelAge(now.Sub(head.Mtime)),
	}
	if pct := transcript.CtxPct(head.CtxTokens, head.Model); pct >= 0 {
		agent.CtxPct = int32(pct)
	}
	resp := &roostv1.WatchAgentResponse{Agent: agent, Resume: head.ID}
	if head.CtxTokens > 0 {
		resp.Ctx = &roostv1.Ctx{
			Tokens: int64(head.CtxTokens), FreshIn: int64(head.CtxIn),
			CacheRead: int64(head.CtxCacheRd), CacheWrite: int64(head.CtxCacheWr),
			Out: int64(head.CtxOut), Window: int64(transcript.Window(head.Model)),
			Model: head.Model,
		}
	}
	history := s.membraneHistory(root, transcript.History(head.Path), raw)
	msgs := make([]uint64, len(history))
	for i, m := range history {
		resp.History = append(resp.History, protoMsg(m))
		msgs[i] = msgHash(m)
	}
	s.mu.Lock()
	if j := s.says[root]; j != nil {
		resp.Pending = &roostv1.Pending{
			Text: j.Text, Sent: j.Sent, Status: j.Status, Error: j.Err,
			Direct: j.Direct, Perm: j.Perm, Images: j.Images,
			AtUnixMs: j.At.UnixMilli(),
		}
	}
	for _, q := range s.queues[root] {
		resp.Queue = append(resp.Queue, &roostv1.Queued{Text: q.Text, Perm: q.Perm})
	}
	if sp := s.spend[root]; sp != nil {
		resp.Spend = &roostv1.Spend{ClaudeUsd: sp.ClaudeUSD, JudgeUsd: sp.JudgeUSD}
	}
	s.mu.Unlock()
	if s.shelf != nil {
		resp.Artifacts = int32(len(s.shelf.list(root)))
	}

	h := fnv.New64a()
	h.Write([]byte(agent.State + "|" + agent.Tool + "|" + agent.ToolDetail + "|" + agent.Age + "|" + agent.Title))
	if resp.Pending != nil {
		h.Write([]byte("|p" + resp.Pending.Status + resp.Pending.Error + resp.Pending.Text))
	}
	for _, q := range resp.Queue {
		h.Write([]byte("|q" + q.Text))
	}
	if resp.Spend != nil {
		var b [16]byte
		putF64(b[:8], resp.Spend.ClaudeUsd)
		putF64(b[8:], resp.Spend.JudgeUsd)
		h.Write(b[:])
	}
	if resp.Ctx != nil {
		h.Write([]byte(resp.Ctx.Model))
		var b [8]byte
		putI64(b[:], resp.Ctx.Tokens)
		h.Write(b[:])
	}
	return view{resp: resp, msgs: msgs, rest: h.Sum64(), cwd: head.Cwd}
}

func putI64(b []byte, v int64) {
	for i := 0; i < 8; i++ {
		b[i] = byte(v >> (8 * i))
	}
}

func putF64(b []byte, v float64) {
	putI64(b, int64(v*1e6))
}

func protoMsg(m wireMsg) *roostv1.Msg {
	out := &roostv1.Msg{
		Role: m.Role, Text: m.Text, Tools: int32(m.Tools),
		Think: m.Think, Ctx: int64(m.Ctx), Rough: m.Rough,
	}
	for _, st := range m.Steps {
		p := &roostv1.Step{Tool: st.Tool, Detail: st.Detail, Out: st.Out, Lines: int32(st.Lines), Err: st.Err}
		if st.Diff != nil {
			p.Diff = &roostv1.Diff{File: st.Diff.File, Old: st.Diff.Old, New: st.Diff.New, ReplaceAll: st.Diff.All}
		}
		out.Steps = append(out.Steps, p)
	}
	if m.Digest != nil {
		out.Digest = &roostv1.Digest{State: m.Digest.State, Headline: m.Digest.Headline, Bullets: m.Digest.Bullets}
	}
	return out
}

func protoTree(files []treeFile) []*roostv1.TreeFile {
	out := make([]*roostv1.TreeFile, 0, len(files))
	for _, f := range files {
		out = append(out, &roostv1.TreeFile{Path: f.Path, Add: int32(f.Add), Del: int32(f.Del), IsNew: f.New})
	}
	return out
}

// msgHash identifies a rendered message for delta purposes: any field
// the UI shows participates.
func msgHash(m wireMsg) uint64 {
	h := fnv.New64a()
	h.Write([]byte(m.Role))
	h.Write([]byte(m.Text))
	h.Write([]byte(m.Rough))
	var b [8]byte
	putI64(b[:], int64(m.Tools)<<32|int64(len(m.Think))<<16|int64(len(m.Steps)))
	h.Write(b[:])
	if m.Digest != nil {
		h.Write([]byte(m.Digest.State + m.Digest.Headline))
	}
	for _, st := range m.Steps {
		// Out/Err land after the call on the same message — a result
		// arriving must read as a change or the delta never ships.
		h.Write([]byte(st.Tool + st.Detail + st.Out))
		if st.Err {
			h.Write([]byte("!"))
		}
	}
	return h.Sum64()
}
