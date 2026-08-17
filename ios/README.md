# Vera for iOS

Native SwiftUI, iOS 18+, no dependencies. It connects to one or more
Macs running vera on the local network, and holds the design's own
grammar over what they report. Three things live here:

- **the goal page** — passes 2–4, the grammar for work whose
  understanding changes;
- **the conversation surface** — pass 5, where Vera rather than the goal
  is the primary interaction object, and *conversation is fluid;
  consequences become structure*;
- **the connection to vera itself** — `WatchBoard` feeding Home,
  `WatchGoal` feeding a goal page, across as many machines as you point
  it at.

Pass 5 changed nothing about the goal page. It added the layer above it,
so an ordinary sentence can reach the whole grammar without a form.

```
open ios/Vera.xcodeproj      # ⌘R to run, ⌘U to test
```

## What v5 is

Ordinary turns are ephemeral. Structure appears only when consequence
appears. A conversation can log a set, keep a principle, take a watch,
accept a six-month goal, or leave nothing at all behind — and **the user
never picks which**. Classification is Vera's problem.

The five outcomes, and what each leaves behind:

| You say | What happens |
| --- | --- |
| "My shoulders are pretty sore." | nothing. A short talk; it simply ends. |
| "Remember I hate overnight flights." | a memory — a dashed *Kept* block, revisable later. |
| "I benched 185 for 5, 5, 4." | structured data — a *Logged* block that joins your history. |
| "Keep an eye out for the passport email." | a watch — *Watching*, ownership without ceremony. |
| "I want to get meaningfully better at singing." | ongoing work — *With Vera now*, the full goal grammar, entered without a form. |

## The goal page — five primitives

Pass 3 settled the invariant: five slots carry every kind of work, and
everything else is content.

| | |
| --- | --- |
| **Stance** | What Vera currently believes about the work. The strongest object on the page — a thesis, a design conviction, the shape of a day, or an honest "here's what I need to learn first." |
| **Pursuits** | What Vera is doing *because of* the stance. A pursuit exists to change the stance or the world; it is never a todo item. |
| **Developments** | Moments the stance materially moved — a finding, an insight, a tension, new information. Rare by definition. |
| **Your marks** | Human input that became structure. Yours are solid chips; Vera's elevated principles are hollow; the world's are grey. |
| **Outcome** | What the work produced, what was kept, and what deliberately stays open. |

Two dimensions ride on top and **never share a slot**: *lifecycle* lives
in the tag (Needs you / With Vera / Done), *understanding* lives in the
note under the stance (exploring · evidence accumulating · revised just
now · executing · resolved). A goal can be highly confident and still
Vera's; it can be Done with its subject still open.

## Eight transformations

Every visible change on a goal is one of these, which is why the page
never needs a changelog. They are methods on `Goal`, not drawings:

`supersede` · `combine` · `eliminate` · `park` · `elevate` ·
`incorporate` · `branch` · `conclude`

The three specimens in `Specimens.swift` are written as an opening state
plus a sequence of these calls — never as hand-drawn screens. That *is*
the claim being tested: if the vocabulary is sufficient, a debugging
session, a design iteration and a day's plan can be expressed with
nothing else. `VeraTests` checks that each specimen reaches Done through
transformations alone, passes through exactly one judgment boundary and
comes back, and that only the diagnostic ever *eliminates* — the other
two set things aside without ruling anything out.

The ledger renames itself accordingly: **Ruled out** (diagnostic) ·
**Set aside** (creative) · **Deferred on purpose** (adaptive).

## Connecting to vera

The phone holds **several machines at once**. That is not a power
feature bolted on: the design has named the machine since pass 2 ("Local
· Nik's MacBook Pro"), and the whole of 4i is a work Mac going offline
and Vera reporting it as *work* — "one pursuit paused, nothing else
cares" — rather than as an error. So machines are part of the world Vera
talks about, and they behave that way here:

- the header pill names the fleet and is the door to managing it;
- a goal remembers which machine it came from, shown on the row only
  once there is more than one machine to distinguish between;
- a machine out of reach gets a sentence under the headline, not a red
  banner. It reconnects on a backoff and says nothing while it does.

**Pairing is one paste.** Run vera with `--addr :4770` and it mints a
key and prints the whole URL with the key in it; paste that line. On the
same Mac, `localhost` is enough — vera serves loopback unkeyed and says
so. Keys go in the Keychain, not `UserDefaults`.

### The wire

`ConnectClient` speaks connectrpc over `URLSession` and nothing else —
no gRPC stack, no generated client, no dependency added to an app that
has none. Unary is a JSON POST; server streaming is a POST whose
response is a series of envelopes (one flag byte, a big-endian uint32
length, then the payload). `WatchBoard` feeds Home; `WatchGoal` fills in
the pursuits when a goal page opens.

Two things worth knowing:

- **Board frames are ~250KB** — mostly `tasks`, `sessions` and `usage`,
  which the phone doesn't read. `URLSession.bytes` hands those over one
  `UInt8` at a time, so the client uses a `URLSessionDataDelegate` and
  parses real chunks instead.
- **protojson sends 64-bit integers as strings**, which is why
  `updatedUnixMs` decodes as `String`.

### What the wire does not carry

The two vocabularies are not the same size, and the mapping fills in
what exists rather than inventing the rest. A remote goal has **no
strata** (no stance history is transmitted at all), **no marks**, **no
outcome**, and **no `Stakes`** — so Home can say *that* something needs
you, and Vera's own sentence about why, but it cannot rank one ask above
another the way it does for local ones. `GoalEvent` also doesn't
distinguish a development that moved the stance from activity that
didn't, so events are not turned into developments. Those are proto
additions, not client work; guessing them here would put the phone in
the business of deciding what counts as a finding.

## Home — selection, not inventory

Home reads the same `goals` array the goal page does. It shows about
five things out of fifteen, and every one of those calls is made by the
attention policy rather than written into the seed.

Pass 2 put three goals on Home as three cards and that was hierarchy.
Pass 4 ran the same design at fifteen and it became a feed — uniform
card weight collapsed, recency-as-ordering collapsed, and "moving"
pulses stopped meaning anything once four things pulsed at once. **What
survived is pass 2's grammar, not its layout:**

- rows lead with what Vera *believes*; the goal's name demotes to a kicker
- the strata glance mark — a stacked edge — says this understanding has
  history, without spending a line on it
- exactly one thing ever gets a container, and only if it genuinely
  needs a person

On top of that sit pass 4's two selection behaviors, which are policies
over existing primitives rather than new ontology:

- the **digest line** — "Also moving: Memory model · Rook integration —
  nothing worth your eyes yet." Vera's authored sentence for work she
  chose not to show, so she stays accountable for the choice.
- the **held ask** — a needs-you that lives inside its goal and never
  reaches Home. Home admits it exists ("another question is waiting
  quietly inside its goal") without spending attention on it.

`Attention` has five levels — ignore · record · surface on Home · notify
· needs you — decided by consequence of waiting, reversibility, whether
human judgment changes the outcome, and novelty. **No level is ever
rendered, and there are no priority numbers anywhere.**

Every sentence on Home is derived, so it cannot claim a calm it doesn't
have: the headline counts the asks, the subhead names what's blocked and
what's held, and with nothing pending the load sentence reads *"Eleven
things are with me. Three are moving, two are watching, one waits on
your work Mac. The rest are quiet on purpose."*

The composer and the card's buttons are the same door — "go with strict"
lands on exactly the choice the button would have, which `VeraTests`
checks by driving both and comparing the resulting goal.

## Layout

```
Vera/
  Design/       Nocturne.swift        tokens, transcribed from the design system's styles.css
                Components.swift      shared grammar: composer, crowned block, veil, ticks
  Model/        Connection.swift      a machine running vera; pairing, and the Keychain
                ConnectClient.swift   connectrpc over URLSession — unary and server streams
                VeraWire.swift        what vera sends, and how it becomes what the phone draws
                Fleet.swift           several machines, one merged picture
                Model.swift           conversation vocabulary — turns, consequences, structure
                GoalModel.swift       the five primitives and the eight transformations
                Specimens.swift       three kinds of work, as transformation sequences
                HomeSelection.swift   the five-level attention policy; what Home shows and why
                VeraBrain.swift       the classifier, the lift reader, the trend read-model
                VeraStore.swift       one store; every surface reads the same arrays
                Seed.swift            the world Vera already has
  Features/     Home, Conversation, Goal (+ GoalComponents), Training, Principles, UnderTheHood
```

There is **no tab bar and no per-screen state object**, because there
are no modules. Home, the conversation and the personal surfaces are
three views of the same structures — log a set in conversation and the
training surface moves, because it is reading the array you just wrote
to.

## Two rules enforced in code, not in review

- **A card must earn its container.** `surfaceCard` and `CrownedBlock`
  are reachable only from materialized consequences, developments, and
  real read-model objects. Talk is typography; pursuits are typography.
- **Motion only marks changes in understanding or ownership.** The
  stance underline sweeps once per succession and never again. The only
  ambient motion in the product is the 2.4s breathing dot on a live
  pursuit — sub-1Hz, opacity only — and it stops when the goal settles.
  Everything collapses to crossfades under Reduce Motion.

## What is computed rather than written down

Every figure on the training surface is derived from the logged
sessions via Epley — the trend sentence, the percentages, the weekly
bars, and the rep-PR flag. That is deliberate: it is the claim
"consequences become structure", held to. It also means the numbers
read *198 → 216, ↑9% over 7 weeks* rather than the mock's *205 → 223,
↑9%* — Epley on the walkthrough's own logged sets gives 216, and the
computed answer wins over the drawn one.

For the same reason Vera says "squat is under-sampled" only while it
is, and a weight you have never attempted is never called a PR.

## Which turns recede

Older turns dim and compress. The boundary is not "the last N turns" —
an exchange ends when a consequence lands:

> Everything up to and including the most recent materialization
> recedes, unless nothing has been said since, in which case the
> sentence that caused it stays live alongside it.

That single rule reproduces all four ageing treatments the design doc
draws (5f, 5j, 5l, 5u). `VeraTests` checks each one.

## Scaffolds, clearly marked

- **Long-press the "Vera" wordmark** for the walkthrough sheet — every
  state in the design docs, reachable on device. Each entry drives the
  real store rather than faking a screen.
- On a specimen (`2g`, `3d`, `3e`) a dashed beat rail appears above the
  composer for stepping through the transformations.
- `-scene 3d -beat 4` as launch arguments drops straight into one.
- `-connect "http://host:4770/?key=…,localhost"` pairs machines at
  launch, and `-sheet connections` opens the machines sheet, so the
  connected states can be driven without tapping.

None of it is product surface.

## The stand-in

`VeraBrain.read(_:)` is an ordered rule table, not a model call. The
screen's point is that classification happens on Vera's side and is
invisible, and a rule table demonstrates that as honestly as a model
would while staying deterministic enough to test. Swapping it for a real
model means replacing `read(_:)` and `Distiller` and nothing else — the
`Reading` type it returns is already the whole interface.

## Type

Nocturne is drawn in Inter. If `Inter*.ttf` is present in the target,
`VeraFont` uses it; otherwise SF stands in at the same sizes and
weights. Adding the font files is the only step to upgrade fidelity — no
code changes.

## Not done

The connection is read-only. Answering an ask changes the goal on the
phone and sends nothing back to vera — that wants an RPC, and the
`Decision` the phone draws doesn't exist on the wire to answer.

The conversation is local. `Say`/`WatchAgent` are per-agent-session,
and there is no endpoint that takes "what's on your mind" and returns a
classified consequence, so 5h's classifier runs here.

There is no discovery: machines are added by paste. Browsing would need
the Go side to advertise `_vera._tcp`, which it does not.

With no machines paired the app falls back to its seeded world. That is
a demo, not a cache — connect one and its board takes over, because a
demo world sitting underneath live work would make Home lie.
