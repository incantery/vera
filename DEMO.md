# The roost demo — entirely in the web app

Five minutes on the board: you'll create a throwaway workspace, hand
rook a three-phase job, watch it staff a fresh Claude agent that
builds and tests real code, see the rook agent approve the routine
questions itself, personally deny a file deletion, verify the result,
accept the task, and delete the workspace — without leaving the page.

> **⚠️ Scratch workspace, not an isolated sandbox.** The directory
> roost creates for this demo is just a folder under
> `~/roost-scratch/` where nothing real is at stake. The agent works
> there with real tools under your user account: "can edit & test"
> scopes which *tools* it may use, not which files those tools can
> ultimately touch, and `go test` runs whatever the workspace's code
> says. The safety you are about to watch comes from the rook agent's
> escalation judgment and the tool policy — not from filesystem
> isolation.

## Before you start

Open roost in your browser. On this machine that's
http://localhost:4770 (no key needed). From another machine, use the
URL roost printed at startup; if you opened the bare address you'll
land on the login screen — type the key into the **key** field and
press Enter. Either way you arrive at **rook board**, the home screen.

The header should read `N working · M agents`; the rail on the left
lists your agents. If the amber banner says "no rook-agent key…",
drives are off — fix that first (see QUICKSTART.md).

## 1 · Capture the task

In the bar at the top — **Tell rook what needs doing…** — paste
exactly this, then press **Capture**:

> In this scratch workspace, work in three phases, STOPPING after each
> phase to ask permission before the next. Phase 1: create go.mod
> (module roostdemo) and greet.go with a Greet(name string) string
> function. Phase 2: create greet_test.go with a real test and run go
> test. Phase 3: request authorization to ALSO delete SCRATCH.txt — do
> not delete anything without explicit authorization.

A card appears in the **inbox** column, unassigned, with the face
"Captured, unassigned. Rook has spent nothing on it yet." The detail
rail on the right opens on it automatically.

## 2 · Create the scratch workspace and assign a fresh agent

In the rail's **rook proposes** box ("Start on the current agent"):

1. Open the first dropdown (it starts on **on the current agent**) and
   pick **+ new scratch workspace…**. Name it `demo` in the prompt.
   Roost creates `~/roost-scratch/demo`, drops a `SCRATCH.txt` marker
   inside — that marker is the file the agent will later ask to
   delete — and the dropdown now shows **fresh agent in demo
   (scratch)**.
2. Set the second dropdown from **read-only** to **can edit & test**.
   That's the tool policy: file edits plus scoped build/test commands
   (`go build/test/vet`, `npm test`, `make`), nothing else — no git,
   no network. The sets are fixed in code; the agent can't widen them.
3. Press **Start drive**.

The card moves to **in progress** with "a fresh agent is being born."
Rook compiles your words into a drive goal first — you'll find both
versions in the rail: your **Intent**, and the **compiled drive goal**
marked *written by rook*.

## 3 · Watch the drive

The card wears the agent's live state: a pulsing dot, the workspace
name, and the membrane's tool line (⛭ `Write — creating greet.go` and
the like). In the rail, two sections fill in as it works:

- **Log** — every automatic decision, as it happens:
  `[rook] turn 1 — answered the worker: Proceed with Phase 2: create
  greet_test.go…` — that's the rook agent granting a routine approval
  by itself, because your goal already authorized the phase.
- **Conversation** — the full exchange: rook's prompts (→, in accent)
  and the worker's replies. Long replies fold; click **(show all)**.

## 4 · The deletion moment

Within a minute or two the card lands in **waiting**. Depending on how
the judge reads phase 3, it arrives in one of two honest shapes:

- **"waiting · escalated to you"** — the card carries a *needs you*
  ask (e.g. "May I proceed to Phase 3?" or the deletion question
  itself). The judge refused to answer for you.
- **"waiting for acceptance"** — the judge counted the worker's
  authorization *request* as phase 3 delivered and proposed done. The
  request is right there in **Conversation**.

Either way, nothing has been deleted, and either way the rail has a
reply box — **answer the worker — the drive continues from here**.
Type the explicit denial:

> Proceed as needed, and the answer to the deletion request is NO —
> never delete SCRATCH.txt. Confirm the file still exists, report the
> go test result, then you are done.

Press **Reply & continue**. The card returns to **in progress** —
"continuing on your answer" — and the same drive resumes with its
whole history. Your reply appears in the Log as
`[human] replied — …`: the denial is on the permanent record.

## 5 · Verify, then accept

The card comes back to **waiting for acceptance**. Open
**Conversation** and read the worker's final reply — in our runs:
*"SCRATCH.txt: still exists… go test: ok… no deletion performed, per
your instruction."* Cross-check the **Log**: every rook approval,
your denial, and `[rook] judged done, proposed acceptance`, with the
cost rolled up under **Runs** (well under a dollar).

The **rook proposes** box now offers **Accept as done** — with the
reminder that this transition is irreversible and yours. Press it. The
card moves to **done · accepted by human**.

*(Want independent proof beyond the worker's word? Optional, outside
the app: `ls ~/roost-scratch/demo && cd ~/roost-scratch/demo && go
test ./...` — but the Conversation is the in-app verification
surface.)*

## 6 · Clean up

On the done card's rail header, press **delete scratch workspace**
and confirm. Roost removes `~/roost-scratch/demo` wholesale — it only
ever deletes folders it created (they carry its marker). The card
itself stays on the board as the audited record of what happened; if
you'd rather clear it, that's what **drop** is for on open cards, and
done cards can simply be left as history.

That's the whole loop: capture → staff → autonomous work → your one
necessary decision → verified completion → cleanup. Everything the
machine decided is on the log, everything it spent is on the meter,
and the only file it wanted to delete is still there.
