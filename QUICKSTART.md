# roost — quick start

Ten minutes from zero to a board that captures work, staffs it with a
fresh Claude agent, answers the agent's routine questions itself, and
escalates the rest to you.

## What you need

- The `claude` CLI, logged in (roost drives it headlessly; its turns
  bill to whatever claude itself bills to).
- Go 1.25+ to run roost.
- An OpenAI-compatible API key for the rook agent (the judge that
  supervises drives, writes digests, phrases your messages):
  `$OPENAI_API_KEY`, or a key file at `~/.config/rook/openai_key`, or
  `--api-base` pointed at any compatible local server (ollama works,
  no key needed). Without one, roost still watches; drives are off.

## Run it

```
go run github.com/incantery/rook-host/engine/cmd/roost@latest
```

Open http://localhost:4770. Loopback needs no key.

To reach it from another machine: `roost --addr :4770`. Beyond
loopback a key guards the API — roost prints ready-to-share URLs at
startup (`http://<your-machine>.local:4770/?key=…`, immune to DHCP
drift), or open the bare URL and type the key at the login screen.

## The board is the home screen

One global kanban across every Claude Code session on the machine:

- **inbox** — captured backlog, unassigned, nothing spent.
- **in progress** — an agent is on it; the card wears the agent's live
  state and its current tool call.
- **waiting** — the needs-you column: a proposal to accept, or an
  escalated question with a reply box.
- **done / dropped** — closed, with the reason on the record.

Your live sessions appear automatically: a working, titled session is
adopted as an in-progress card bearing its own title. The rail on the
left lists every agent; click through for the chat surface (full
history, digests, what-rook-sent provenance, /compact, costs).

## Your first task

Everything happens on the board — including making a place where
nothing real is at stake. (Real repositories appear in the picker once
roost has seen a Claude session in them; scratch workspaces are the
ones roost creates itself, under `~/roost-scratch/`.)

1. **Capture** — type what needs doing in the top bar. It lands in the
   inbox, unassigned, free.
2. **Start** — open the card. In the *rook proposes* box, pick
   **+ new scratch workspace…** (or a real repo the fleet has shown)
   and a tool policy: **read-only** (analysis; anything mutating stays
   refused) or **can edit & test** (file edits plus scoped
   build/test commands — `go build/test/vet`, `npm test`, `make` —
   through claude's own permission system; no git mutation, no
   network). Hit *Start drive*.
3. **Watch** — the rook agent compiles your intent into a drive goal
   (both on the record), a fresh claude is born in the workspace, and
   the judge supervises turn by turn. Routine questions — approvals
   the goal grants, option picks — it answers itself; every automatic
   decision lands on the card's log as it happens.
4. **Escalations come to you** — anything destructive, irreversible,
   credential-shaped, or beyond what the goal authorized stops the
   drive and puts a precise question on the card. Type your answer in
   the reply box: the same drive continues, seeded with its history.
   Runs also stop themselves on a circling conversation, a $5 spend
   cap, or a 4-turn budget — each an honest card, never a silent stall.
5. **Accept** — when the judge believes the goal is met, it proposes
   *Move to done*. Irreversible transitions are always yours.

## Costs, honestly

Every card rolls up what it cost (claude's own metered turns + the
rook agent's calls); the header sums the day; the rail shows your
subscription's rate-limit windows. Meters journal to disk — restarts
don't zero them.

## What roost is NOT (yet)

**A sandbox.** A drive runs in a real directory with real tools under
your user account. "Can edit & test" scopes which *tools* claude may
use, not which files those tools may ultimately touch — and `go test`
or `make` execute whatever the workspace's own code says. Point work
mode at scratch or trusted repositories. Per-task isolated sandboxes
are on the roadmap; until then the honest unit of isolation is "a
directory you don't mind changing."

## The five-minute demo

**DEMO.md** walks the whole flow through the web app, click by click:
a roost-made scratch workspace, a fresh agent in work mode building
and testing real Go code, the rook agent approving the routine asks,
you personally denying a file deletion from the card, verification in
the Conversation panel, acceptance, and one-click workspace cleanup.
(Prefer the terminal? `demo.sh` runs the same flow through the API.)

The demo is honest about what it is: a scratch directory, not a
sandbox. The safety you watch comes from the judge's escalation line
and the tool policy — not from filesystem isolation.
