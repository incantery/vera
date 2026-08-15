# The ladder

Five asks, easy to hard, that measure the one thing vera uniquely
moves: **human attention per completed piece of work**. Each rung is
an utterance — never an instruction — and scales on how much of the
operational plan is left unsaid. The planning half is benchmarked
cheaply and repeatably by the corpus (`go test -tags eval -run Plan
./drive/`); the rungs below are the live protocol, run in a fresh
world (`vera --world /tmp/wN`) so every run starts from nothing and
resets with `rm -rf`.

For every rung, record: **human messages after the utterance**,
**wall-clock of human attention**, **spend**, and pass/fail on an
acceptance check written before the run.

## Rung 1 — the unnamed mechanics

> "I need a little tool that tells me which of my git repos have
> uncommitted changes"

The artifact is named (a tool); nothing else is. Vera must choose new
workspace, code home, a name, and a goal; the nod must create the
repo, birth a worker, and drive to done. Accept: the built tool runs
against one clean and one dirty fixture repo and reports exactly the
dirty one. Pass bar: zero human messages after the nod.

## Rung 2 — the unnamed artifact

> "It bugs me that I can never tell how much I'm spending across my
> claude sessions in a week"

No artifact named at all. Vera must decide *what to build* and then do
everything rung 1 does. Accept: the deliverable answers the question
against real usage data; at most one clarifying question, asked before
work starts.

## Rung 3 — the ongoing need

> "I want to keep an eye on whether my homelab services are up"

"Keep" implies something running. Vera must infer build-and-operate:
the deliverable is alive when it reports back. Accept: vera
demonstrates it running, unprompted. (Today this rung is expected
red past the build: operating is not yet a verb vera has.)

## Rung 4 — the decomposition

> "I want a short morning summary of what my agents did overnight"

Honestly two or three pieces. Vera must break it into cards itself,
run them, and integrate. Accept: the decomposition appears on the
board without dictation, and the end-to-end result works. (Expected
red: plans are single-step today.)

## Rung 5 — the standing wish

> "I'd like my personal site to stop embarrassing me"

Wish-level, no shape. Vera must interrogate the current state, propose
a scope, get one nod, then go — deciding what "done enough" means.
Accept: one nod, then a reviewable demo. (Expected red: planning reads
the ask, not the ground; there is no survey step yet.)

## Results

| date | rung | world | outcome | human msgs | spend | notes |
|---|---|---|---|---|---|---|
| 2026-08-14 | pre-ladder: party food (life, deadline) | w1 | PASS — FOOD_PLAN.md, judged done, accepted | 0 after nod | $0.57 | deadline 2026-08-28 computed from "in two weeks" |
| 2026-08-15 | 1 | w2 | PASS — tool flagged exactly the dirty fixture repo | 0 after nod | $1.08 | 1 turn; worker wrote go.mod by hand (no `go mod` in workTools — didn't need it) |
| 2026-08-15 | 2 | w2 | PASS — claude-weekly-spend found the transcripts itself, priced per model/day, answered the question ($893.80 API-equivalent this week) | 0 after nod, 0 clarifying | $9.75 api-rate (subscription-covered) + cents | 1 turn |
| 2026-08-15 | 3 | plan-only | plan PASS (build a monitor, standing) · operate RED | — | cents | "alive when it reports back" needs an operate verb vera does not have |
| 2026-08-15 | 4 | plan-only | RED — planned as ONE card; no decomposition | — | cents | plans are single-step; multi-card plans are the missing shape |
| 2026-08-15 | 4 (retry) | plan-only | YELLOW — p5 plans STEP lines when the work is honestly several (home-office ask → research + 2 dependency-aware steps); the nod lays them as backlog cards | — | cents | chaining a finished card into the next is still unbuilt — steps start by hand |
| 2026-08-15 | 5 | plan-only | RED then FIXED at the planning layer — p2 invented an empty "personal-site" workspace to audit; that finding built KIND: ask (p4 asks "Where is the source for your personal site located?") | — | cents | surveying real ground before scoping remains unbuilt; the ask verb is the honest stopgap |

Every red rung names the next feature. That is the point.
