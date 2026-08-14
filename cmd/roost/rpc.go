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

// connectErr translates a core's HTTP-vocabulary refusal into
// Connect's, keeping the server's own words.
func connectErr(serr *sayErr) error {
	code := connect.CodeInvalidArgument
	switch serr.code {
	case 404:
		code = connect.CodeNotFound
	case 409:
		code = connect.CodeFailedPrecondition
	}
	return connect.NewError(code, errors.New(serr.msg))
}

// Say is the outbound rail on the typed wire — the same core the REST
// endpoint calls, refusals translated into Connect's vocabulary.
func (r *roostRPC) Say(ctx context.Context, req *connect.Request[roostv1.SayRequest]) (*connect.Response[roostv1.SayResponse], error) {
	m := req.Msg
	status, serr := r.s.say(m.Id, sayReq{
		Text: m.Text, Verbatim: m.Verbatim, Direct: m.Direct,
		Perm: m.Perm, Images: m.Images,
	})
	if serr != nil {
		return nil, connectErr(serr)
	}
	return connect.NewResponse(&roostv1.SayResponse{Status: status}), nil
}

// Interrupt is the cancel rail on the typed wire.
func (r *roostRPC) Interrupt(ctx context.Context, req *connect.Request[roostv1.InterruptRequest]) (*connect.Response[roostv1.InterruptResponse], error) {
	if serr := r.s.interrupt(req.Msg.Id); serr != nil {
		return nil, connectErr(serr)
	}
	return connect.NewResponse(&roostv1.InterruptResponse{}), nil
}

// Review, Commit, Discard: the verdict surface on the typed wire —
// the same cores the REST endpoints call.
func (r *roostRPC) Review(ctx context.Context, req *connect.Request[roostv1.ReviewRequest]) (*connect.Response[roostv1.ReviewResponse], error) {
	info, serr := r.s.agentReview(req.Msg.Id)
	if serr != nil {
		return nil, connectErr(serr)
	}
	resp := &roostv1.ReviewResponse{Dir: info.Dir, Branch: info.Branch}
	for _, f := range info.Files {
		resp.Files = append(resp.Files, &roostv1.ReviewFile{
			Path: f.Path, Add: int32(f.Add), Del: int32(f.Del),
			IsNew: f.New, Binary: f.Binary, Truncated: f.Truncated,
			Diff: f.Diff,
		})
	}
	return connect.NewResponse(resp), nil
}

func (r *roostRPC) Commit(ctx context.Context, req *connect.Request[roostv1.CommitRequest]) (*connect.Response[roostv1.CommitResponse], error) {
	hash, serr := r.s.agentCommit(req.Msg.Id, req.Msg.Message)
	if serr != nil {
		return nil, connectErr(serr)
	}
	return connect.NewResponse(&roostv1.CommitResponse{Commit: hash}), nil
}

func (r *roostRPC) Discard(ctx context.Context, req *connect.Request[roostv1.DiscardRequest]) (*connect.Response[roostv1.DiscardResponse], error) {
	if serr := r.s.agentDiscard(req.Msg.Id, req.Msg.Path, req.Msg.All); serr != nil {
		return nil, connectErr(serr)
	}
	return connect.NewResponse(&roostv1.DiscardResponse{}), nil
}

// WatchBoard streams the home screen's present: whole frames (the
// payload is small), one whenever anything in it changes. The hub
// pokes on transcript writes and board mutations; the slow tick
// catches what neither announces (ages, the usage collector).
func (r *roostRPC) WatchBoard(ctx context.Context, req *connect.Request[roostv1.WatchBoardRequest], stream *connect.ServerStream[roostv1.WatchBoardResponse]) error {
	s := r.s
	poke, cancel := s.hub.subscribe()
	defer cancel()
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()

	var prev uint64
	first := true
	for {
		resp, sum := s.boardView()
		if first || sum != prev {
			if err := stream.Send(resp); err != nil {
				return err
			}
			prev, first = sum, false
		}
		select {
		case <-ctx.Done():
			return nil
		case <-poke:
		case <-tick.C:
		}
	}
}

// boardView computes one WatchBoard frame and its change hash. It is
// the typed twin of handleTaskList + handleState's rail.
func (s *server) boardView() (*roostv1.WatchBoardResponse, uint64) {
	now := time.Now()
	b := s.boardData(now)
	sessions, current := s.railSessions(now)

	resp := &roostv1.WatchBoardResponse{
		Inflight: int32(b.inflight), Spend: b.spend,
		Fleet:   &roostv1.Fleet{Agents: int32(b.agents), Working: int32(b.working)},
		Notice:  s.notice,
		Current: current,
	}
	for _, t := range b.tasks {
		resp.Tasks = append(resp.Tasks, protoTask(t))
	}
	for _, rp := range b.repos {
		resp.Repos = append(resp.Repos, &roostv1.Repo{Dir: rp["dir"], Cwd: rp["cwd"], Scratch: rp["scratch"] == "yes"})
	}
	for _, ws := range sessions {
		resp.Sessions = append(resp.Sessions, &roostv1.Session{
			Id: ws.ID, Title: ws.Title, State: ws.State, Dir: ws.Dir, Cwd: ws.Cwd,
			Branch: ws.Branch, Prompt: ws.Prompt, LastText: ws.LastText,
			CtxPct: int32(ws.CtxPct), Model: ws.Model, Age: ws.Age,
			Driving: ws.Driving, Tool: ws.Tool, ToolDetail: ws.ToolDetail,
			Task: ws.Task, Scratch: ws.Scratch,
		})
	}
	if u := s.uc.Latest(); u != nil {
		resp.Usage = &roostv1.Usage{
			Mode: u.Mode, SessionPct: int32(u.SessionPct), SessionResets: u.SessionResets,
			WeekAllPct: int32(u.WeekAllPct), WeekAllResets: u.WeekAllResets,
			WeekModelName: u.WeekModelName, WeekModelPct: int32(u.WeekModelPct),
			WeekModelResets: u.WeekModelResets,
		}
	}

	h := fnv.New64a()
	var buf [8]byte
	putI64(buf[:], int64(b.inflight)<<32|int64(b.agents)<<16|int64(b.working))
	h.Write(buf[:])
	putF64(buf[:], b.spend)
	h.Write(buf[:])
	h.Write([]byte(s.notice + "|" + current))
	for _, t := range resp.Tasks {
		h.Write([]byte(t.Id + t.Col + t.State + t.Face + t.Ask + t.Proposal))
		putI64(buf[:], t.UpdatedUnixMs)
		h.Write(buf[:])
		if t.Live != nil {
			h.Write([]byte(t.Live.State + t.Live.Now))
		}
		putI64(buf[:], int64(len(t.Log))<<16|int64(len(t.Exchanges)))
		h.Write(buf[:])
	}
	for _, ws := range resp.Sessions {
		h.Write([]byte(ws.Id + ws.State + ws.Age + ws.Tool + ws.ToolDetail + ws.Task + ws.Title))
	}
	if resp.Usage != nil {
		putI64(buf[:], int64(resp.Usage.SessionPct)<<32|int64(resp.Usage.WeekAllPct)<<16|int64(resp.Usage.WeekModelPct))
		h.Write(buf[:])
	}
	return resp, h.Sum64()
}

// protoTask lifts one card onto the wire, timestamps as unix ms.
func protoTask(t task) *roostv1.BoardTask {
	out := &roostv1.BoardTask{
		Id: t.ID, Title: t.Title, Intent: t.Intent, Agent: t.Agent,
		Goal: t.Goal, GoalActor: t.GoalActor,
		Col: t.Col, State: t.State, Ask: t.Ask, Face: t.Face,
		Pinned: t.Pinned, Proposal: t.Proposal, ProposalWhy: t.ProposalWhy,
		ProposalKind: t.ProposalKind, CostUsd: t.CostUSD,
		Workspace: t.Workspace, ScratchName: t.ScratchName, Mode: t.Mode,
		CreatedUnixMs: t.CreatedAt.UnixMilli(), UpdatedUnixMs: t.UpdatedAt.UnixMilli(),
	}
	for _, r := range t.Runs {
		out.Runs = append(out.Runs, &roostv1.TaskRun{Kind: r.Kind, Outcome: r.Outcome, CostUsd: r.CostUSD})
	}
	for _, e := range t.Log {
		out.Log = append(out.Log, &roostv1.TaskEvent{AtUnixMs: e.At.UnixMilli(), Actor: e.Actor, Text: e.Text})
	}
	for _, x := range t.Exchanges {
		out.Exchanges = append(out.Exchanges, &roostv1.Exchange{Prompt: x.Prompt, Reply: x.Reply})
	}
	if t.Live != nil {
		out.Live = &roostv1.TaskLive{Dir: t.Live.Dir, State: t.Live.State, Now: t.Live.Now}
	}
	return out
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
		Secs: int32(m.Secs),
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
	// Secs rides the open turn's hash: the duration ticking up must
	// ship a delta even when no other field moved.
	putI64(b[:], int64(m.Tools)<<40|int64(m.Secs)<<24|int64(len(m.Think))<<16|int64(len(m.Steps)))
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
