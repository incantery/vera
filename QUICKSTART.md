# vera — quick start

Ten minutes from zero to a board that captures work, staffs it with a
fresh Claude agent, answers the agent's routine questions itself, and
escalates the rest to you.

## What you need

- The `claude` CLI, logged in (vera drives it headlessly; its turns
  bill to whatever claude itself bills to).
- Go 1.25+ to run vera.
- An OpenAI-compatible API key for the vera agent (the judge that
  supervises drives, writes digests, phrases your messages):
  `$OPENAI_API_KEY`, or a key file at `~/.config/vera/openai_key`, or
  `--api-base` pointed at any compatible local server (ollama works,
  no key needed). Without one, vera still watches; drives are off.

## Run it

```
go run github.com/incantery/vera/cmd/vera@latest
```

Open http://localhost:4770. Loopback needs no key.

To reach it from another machine: `vera --addr :4770`. Beyond
loopback a key guards the API — vera prints ready-to-share URLs at
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
history, digests, what-vera-sent provenance, /compact, costs).

## Your first task

Everything happens on the board — including making a place where
nothing real is at stake. (Real repositories appear in the picker once
vera has seen a Claude session in them; scratch workspaces are the
ones vera creates itself, under `~/vera-scratch/`.)

0. **Or just tell vera what you need** — the top bar's Enter asks vera
   for a plan first: where the work should live (an existing
   workspace, a fresh one — code under `go/src/`, everything else
   under the `vera/` home, git from birth — or honestly nowhere), its
   cadence, and the goal it would hand a worker. *Make it so* executes
   the whole chain: workspace made, card opened, worker born. *Just
   capture* skips the plan. Life work is welcome — "handle food for
   the birthday party in two weeks" plans as readily as a CLI.
1. **Capture** — the plain path: type what needs doing and hit the
   capture button. It lands in the inbox, unassigned, free.
   And **explore** opens the directory browser: walk anywhere under
   your home (or the world), pick a directory, and say the first
   word — a fresh session is born there and the direct cockpit opens
   on it. The last reason to run `claude` in a terminal, gone.
2. **Start** — open the card. In the *vera proposes* box, pick
   **+ new scratch workspace…** (or a real repo the fleet has shown)
   and a tool policy: **read-only** (analysis; anything mutating stays
   refused) or **can edit & test** (file edits plus scoped
   build/test commands — `go build/test/vet`, `npm test`, `make` —
   through claude's own permission system; no git mutation, no
   network). Hit *Start drive*.
3. **Watch** — the vera agent compiles your intent into a drive goal
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
vera agent's calls); the header sums the day; the rail shows your
subscription's rate-limit windows. Meters journal to disk — restarts
don't zero them.

## What vera is NOT (yet)

**A sandbox.** A drive runs in a real directory with real tools under
your user account. "Can edit & test" scopes which *tools* claude may
use, not which files those tools may ultimately touch — and `go test`
or `make` execute whatever the workspace's own code says. Point work
mode at scratch or trusted repositories. Per-task isolated sandboxes
are on the roadmap; until then the honest unit of isolation is "a
directory you don't mind changing."

## The five-minute demo

**GUIDE.md** (beside this file) walks the whole flow through the web
app, click by click:
a vera-made scratch workspace, a fresh agent in work mode building
and testing real Go code, the vera agent approving the routine asks,
you personally denying a file deletion from the card, verification in
the Conversation panel, acceptance, and one-click workspace cleanup.
(Prefer the terminal? `demo.sh` runs the same flow through the API.)

The demo is honest about what it is: a scratch directory, not a
sandbox. The safety you watch comes from the judge's escalation line
and the tool policy — not from filesystem isolation.
