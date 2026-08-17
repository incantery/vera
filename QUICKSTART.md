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

One global kanban across every Claude Code session that is vera's
business:

- **inbox** — captured backlog, unassigned, nothing spent.
- **in progress** — an agent is on it; the card wears the agent's live
  state and its current tool call.
- **waiting** — the needs-you column: a proposal to accept, or an
  escalated question with a reply box.
- **done / dropped** — closed, with the reason on the record.

The board claims a session only when it can honestly say it's vera's:
a session vera itself birthed or drove (lineage), a session a card
assigns, or a session working on **registered ground** — a directory
you bookmarked from the explorer, or a scratch workspace vera made.
Everything else on the machine stays invisible to the board;
bookmarking a repo is how its sessions opt in. On claimed ground a
working, titled session is adopted as an in-progress card bearing its
own title. The rail on the left lists every agent; click through for
the chat surface (full history, digests, what-vera-sent provenance,
/compact, costs).

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
   And **explore** switches the left panel into explorer mode (the
   tabs above the rail switch it back): walk anywhere under your
   home (or the world), pick a directory, and say the first word —
   a fresh session is born there and the direct cockpit opens on
   it. The last reason to run `claude` in a terminal, gone.
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

## The engine

Vera keeps its own heartbeat: every fifteen seconds (and whenever the
board moves) a tick reads the world once and a fixed roster of
systems decides what should happen — game-engine style. What ships
today:

- **Reconcile** — a card claiming a run in flight after a vera
  restart is folded back to waiting instead of hanging forever.
- **Recover** — a run that died of *machinery* (a killed process, the
  turn clock, the judge's endpoint flaking) is restarted
  automatically: at most twice, with backoff, resuming from the
  card's recorded exchanges. Judgment stops — escalations, spend
  caps, real errors — are never retried; those are yours.
- **Schedule** — `POST /api/schedule` with an intent, a workspace,
  and a when (`at` RFC3339, `every` a duration like `"24h"`, or
  both) and the due moment births a card and starts it, exactly like
  an accepted chain step. `GET` lists, `DELETE /api/schedule/{id}`
  removes.
- **Steward** — the engine's one thinking pass: when the board has
  actually changed (fingerprinted) and a cooldown has passed, the
  vera agent reads the whole board — states, goals, the workers'
  last words, log tails — and proposes at most three moves: *this
  looks finished* (a done proposal), *start this next*, *here's the
  answer* (a drafted reply to an escalated card, parked as a one-tap
  "Send Vera's reply" — shown verbatim, never sent unseen, and never
  drafted for authorization, destructive, or credential asks), or a
  one-line note. A card newly entering "waiting on you" opens a
  3-minute fast lane; a merely-changed board waits the full half
  hour. Guarded server-side; it never closes anything, and one note
  per card per day.
- **Ignite** — read-mode self-start, the steward's acting half. A
  START on a card that already names **registered ground** (a
  bookmark or scratch workspace) queues it, and the ignite system
  starts the drive itself — read mode only, so nothing can mutate;
  the worst case is bounded spend, and each ignition counts against
  the autonomy budget individually. Cards without named ground still
  become proposals: picking ground is yours, and work mode (edits)
  always is.
- **Report** — the daily account of autonomy: once a day the engine
  writes what happened — started, judged done, accepted, recovered,
  steward moves, what it cost, and what waits on you — from the
  records that already exist. `GET /api/report` serves the latest;
  it also prints to vera's log.

- **Driver** — the marathon tier. Start a card with an **autopilot
  budget** (the `autopilot $` field beside the tool policy, or
  `budgetUsd` on `POST /api/tasks/{id}/start`, capped at $200) and
  vera drives it for hours by itself: every time a run stops on a
  turn budget, a per-run spend cap, or a routine escalation, the
  driver continues it — escalations get the standing answer "use
  your best judgment and log open questions" — until the card's
  metered spend meets your authorization. The budget is the only
  human boundary; everything else stays honest: circling parks
  (money does not fix a conversation going nowhere), machinery
  errors go to recover, DONE is still a proposal only you accept,
  and autopilot is **read-only by construction** — a budget big
  enough to run unattended is never a budget for unattended edits.

Standing needs are real now, too: accepting a standing card
(`cadence: standing`, from a plan) closes that pass and lays the next
one in the inbox — same intent, same ground, spending nothing until
its moment comes.

Autonomy has a ceiling: at most `--autonomy` spending actions per
hour (default 6; `0` turns recovery and scheduled starts off), and a
card's log records everything the engine did — including what it
*wanted* to do when the budget was spent. Acceptance boundaries are
untouched: the engine accelerates everything between your decisions,
never through them.

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
