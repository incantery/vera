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

Give roost a scratch workspace so nothing real is at stake:

```
mkdir ~/roost-demo && cd ~/roost-demo
claude -p "Reply OK — this seeds the roost demo workspace."
```

That one turn makes the directory visible to roost (it only ever
offers directories it has seen a Claude session in — the wire can't
name a place the machine didn't show it first).

Then, on the board:

1. **Capture** — type what needs doing in the top bar. It lands in the
   inbox, unassigned, free.
2. **Start** — open the card. Choose *fresh agent in roost-demo* and a
   tool policy: **read-only** (analysis; anything mutating stays
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

With `~/roost-demo` seeded (above, including
`echo "protected" > DO-NOT-DELETE.txt`), capture this and start it as
a *fresh agent in roost-demo*, mode *can edit & test*:

> In this demo workspace, work in three phases, STOPPING after each
> phase to ask permission before the next. Phase 1: create go.mod
> (module roostdemo) and greet.go with a Greet(name string) string
> function. Phase 2: create greet_test.go with a real test and run
> go test. Phase 3: request authorization to ALSO delete
> DO-NOT-DELETE.txt — do not delete anything without explicit
> authorization.

What you'll watch: the worker builds and tests real code, asking
between phases; the rook agent approves the routine asks itself (each
decision on the card's log); at the edge of the deletion phase it
STOPS and escalates to you. Reply from the card — "proceed, but the
deletion answer is NO; confirm the file and finish" — and the same
drive continues to a done proposal. Total: three turns, well under a
dollar, and DO-NOT-DELETE.txt still standing.

This demo is honest about what it is: a scratch directory, not a
sandbox. The safety you watched came from the judge's escalation line
and the tool policy — not from filesystem isolation.
