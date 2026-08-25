# vera

The rebuild. A person talks to their phone, a Mac answers.

```
make install                      # vera + verad into ~/.local/bin
vera                              # starts verad if needed, opens the chat
open http://localhost:4780/       # scan to pair a phone; open ios/Vera.xcodeproj, ⌘R
```

## Shape

```
transport.go   the boundary: Message in, Frames out
lan.go         an HTTP listener behind it — the known-good baseline
run.go         work that outlives the connection that asked for it
peer.go        the transport that does not need the network
peer/          the Swift sidecar that owns the radio
pair.go        identity, secret, address hints
page.go        the button and the QR
mind.go        one streamed model call per exchange
history.go     what was said a moment ago, bounded
telemetry.go   OTel traces and metrics
generation.go  generation export — what feeds the Conversations view
delegate.go    handing work to Claude Code
rook.go        the terminal adapter: what is inside the terminal, over mux.Mux
fleet.go       the fleet over the wire
usage.go       what is left of the Claude Code subscription
usage_meter.go reporting that on a timer
memory.go      what survives a restart
remember.go    deciding what was worth keeping
eval.go        the suite runner and its scorers
evals/         the cases themselves
main.go        wiring

../mux/        the multiplexer as Vera sees it; tmux backend
../fleet/      rooms Vera opens for coding agents, and the watch over them
```

`Transport` is message-shaped rather than HTTP-shaped, because the next
implementation is a Swift sidecar speaking AWDL and it has no status
codes, headers or URL paths to smuggle. Nothing above that line moved
when `echo` became `think`, and nothing should move when `lan` becomes
`peer`.

## Attention

The Mac app (`macos/`) reports what has focus, and Vera Core keeps that
per device in `attention.go`:

```
POST /observe   {"type":"app.focused","device":"work-mac","app":{"name":"Ghostty","bundle_id":"…"}}
GET  /status    what Vera knows: devices, their focus, providers, integrations
```

The model reads it as a paragraph appended after the system prompt —
"On work-mac: Ghostty has had focus for 2 minutes. Before that: Chrome."
— with an explicit note that this names the application in front of
the person and says nothing about what is inside it. That limit is the
design: an app with no integration is *opaque*, and opaque is a true
thing to tell a model. When an editor or browser integration reports
`editor.selection` or the like, the envelope is the same and the
`source` field is what makes the Connections view say "connected".

`providers` is where capability providers answer "what can you do".
Rook's first answer is `terminal.focus`: `rook.go` is the adapter — the
one file that knows rook is a tmux server today — and it reports which
session and pane are in front of the person, and whether a coding agent
is running there, as a `terminal.focus` observation with `source: rook`.
Ghostty stops being opaque: "Inside it, rook shows Claude Code session
\"Vera native macOS surface\" (vera:1)". `--rook-tmux ""` turns it off.

## Fleet

Vera owns *what work exists and who is doing it*; the multiplexer owns
*where it is drawn and how you reach it*. `mux.Mux` is the line between
them — find the focused pane, spawn one, type into it, read its screen,
bring it forward, hear when something changed — written to what Vera
wants from a mux rather than to what tmux offers. Two backends:
`mux.Rook` speaks rook's socket (`$ROOK_MUX_SOCK`) and reads one
thing — the state feed, a JSON snapshot of everything the engine knows,
pushed on change — so focus, per-pane activity and exits arrive as
fields rather than as hooks and heuristics; `capture` returns a pane
as plain text; a block client holds a resize lease so the phone
narrows a pane without reflowing the desk; and `'N'` opens a workspace
without moving the person. `mux.Tmux` is the lossy reference on a
`-L rook` server. `--mux auto` picks rook when its socket exists.

`fleet/` is firstmate's supervisor
(github.com/kunchenguid/firstmate) as a Go package instead of forty
shell scripts. A **task** is a room: a git worktree beside the repo
(`<repo>--<name>`, branch of the same name, `rook.toml` copy/link
conventions honoured), a pane in the session of the same name running
`claude` with the brief, and a directory under
`~/.local/state/vera/fleet/<id>/` holding the record, an append-only
status log, and a cursor for how much of it a person has seen.

What Vera believes about a task is never stored; it is classified from
evidence every look:

| evidence | source |
| --- | --- |
| pane alive, last output | the mux |
| newest write under the worktree | a bounded scan — a quiet pane while files change is an agent working, not a stall |
| turn ended | Claude Code's Stop hook, `POST /fleet/{id}/turn-ended` (loopback, carries only the incarnation) |
| the agent's own word | `POST /fleet/{id}/status` with one of `working paused blocked resolved done failed` |

which yields `running · quiet · stale · waiting · held · decision ·
finished · broken · gone · closed`. A change of belief is one
observation (`task.waiting`, source `fleet`) through the same door the
Mac app uses, so the mind's preface and `/status` carry it.

```
GET  /fleet                    every task with state, last word, unread lines
POST /fleet                    {"project","name","kind":"ship|scout","mode","brief"}
POST /fleet/{id}/answer        {"text"} — typed and sent into the pane; logged as resolved
POST /fleet/{id}/land          local-only: merge home, remove worktree+branch, close
POST /fleet/{id}/teardown      ?force=1 to discard unlanded work — never Vera's call
POST /fleet/{id}/seen          the phone rendered the log this far
```

The mind reaches the same thing through the `fleet` tool — one tool,
five verbs (`list start answer land stop`) — so "have someone add dark
mode while I'm out" opens a room in the repo in front of the person,
and "how's it going" reads back the picture above in their nouns.
`delegate` stays for the minute of work they wait on; `stop` never
forces, so unlanded work is only ever discarded at the machine.

Every hook is a doorbell, not a fact: the supervisor re-reads the pane
and the worktree after it rings, and a hook that stops firing degrades
to polling.

## Chat

`vera` (cmd/vera) is the front door and the workbench: bare `vera` makes
sure `verad` is running — detached, its output in
`~/.local/state/vera/verad.log` — and opens the chat; `vera start|stop|
restart|status|log|url` manage the daemon and `vera install` makes it a
launchd agent. The chat is a pane (pin it to rook's rail) that
speaks exactly the phone's wire — `/say` frames, `/fleet`, `/status`,
the identity file for the secret — and shows what the phone cannot.

The screen itself is **mote's** (`github.com/incantery/mote/tui`), driven
through the one interface a terminal needs — `agent.Agent`: say a thing,
get a stream of events. Vera is that agent over HTTP, so `/say` frames
become events: deltas into streaming markdown, status lines into the
line you read while you wait, and `tool_call`/`tool_result` into a card
per call with its arguments, its result, how long it took and what it
cost. The rail on the right is the fleet — every open task, its title
from the brief, its last word underneath, and one of five states a
person acts on differently (working, idle, blocked, done, failed);
closed tasks come off it. A task whose state turns actionable, or that
lands, also says so in the transcript.

Slash commands are the fleet verbs by hand: `/tasks`, `/start [@repo]
<brief>` opens a room, `/scout`, `/resume`, `/report <id>` (which prints
what it wrote and marks it seen), `/answer <id> <text>`, `/land`,
`/stop [force]`, `/seen`, `/new` for a fresh conversation, `/dump [note]`
for a folder of everything, `/debug` for what Vera currently believes
about where you are — devices, focus, terminal, integrations — from the
same facts the model's preface is built from, and `/quit`. `/help` is
mote's: it lists these and the keys. `esc` stops a reply in flight,
`ctrl+c` leaves, `ctrl+t` hides the rail, `tab`/`ctrl+o` walk and open
tool cards. It exists so iterating on the mind is typing, not picking up
a phone.

## Pairing

The QR code carries an **identity and a secret**, and mentions addresses
only as hints. A code containing `192.168.1.20:4780` pairs a phone to a
network; every hotel, coffee shop and eventual move to peer-to-peer then
becomes a re-pair. A code carrying a peer id is still true a year later
on a transport that had not been written when it was scanned.

The pairing page is loopback-only. It hands out the secret, so it is for
the person sitting at the machine.

## Telemetry

Grafana Cloud agent observability has **two ingest paths and they are not
interchangeable**. Perfect `gen_ai.*` spans alone leave the Conversations
view empty, which is a thing worth learning here rather than while
staring at a blank screen:

| path | goes to | powers |
| --- | --- | --- |
| OTLP traces + metrics | Tempo / Prometheus | charts, drilldown |
| generation export | the Agent Observability API | **Conversations** |

Both are off unless pointed somewhere. Standard OpenTelemetry variables, so the
Grafana Cloud portal's own copy-paste block is the whole setup:

```
export OTEL_EXPORTER_OTLP_ENDPOINT="https://otlp-gateway-<zone>.grafana.net/otlp"
export OTEL_EXPORTER_OTLP_HEADERS="Authorization=Basic <base64 instanceID:token>"
export OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf
```

A local Alloy or collector is the same thing with a different endpoint.

Generation export is a **separate credential with its own scope** —
having the OTLP token says nothing about having this one. It wants an
access policy with `sigil:write`:

```
export AGENTO11Y_ENDPOINT="https://agento11y-prod-<zone>.grafana.net"
export AGENTO11Y_PROTOCOL=http
export AGENTO11Y_AUTH_MODE=basic
export AGENTO11Y_AUTH_TENANT_ID="<instance id>"
export AGENTO11Y_AUTH_TOKEN="<glc_ token>"
```

`vera --check-telemetry` sends one exchange's worth down both paths and
force-flushes, so a wrong scope is an exit code rather than an empty
dashboard an hour later. Export failures otherwise happen on a
background goroutine; they are routed to `slog` at ERROR so they are
never silent.

When generation export is configured the SDK becomes the
instrumentation — it emits the same three metrics and its own spans, so
running both would double-count every exchange. The hand-rolled OTel
below is the fallback for running without Grafana at all.

Each exchange is one `chat <model>` client span plus the three metrics
agent observability reads: `gen_ai.client.operation.duration`,
`gen_ai.client.time_to_first_token`, `gen_ai.client.token.usage`.

**Span attributes and metric labels are deliberately different sets.** A
label is a time series forever, so `gen_ai.conversation.id` and the
message content ride on the span only. `TestMetricsCarryNoUnboundedLabels`
fails if that ever slips.

Content capture (`gen_ai.input.messages` / `gen_ai.output.messages`)
defaults **on**, against the convention's default, because the question
this exists to answer is "why did it say that" and the destination is
your own stack. `OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT=false`
turns it off.

The same exchange is also one `slog` JSON line, which is what you read
while the collector is not running.

## History

A conversation remembers its own last few turns, keyed by the
`conversation` id the phone sends. It lives on the **Mac**, not the
phone: otherwise the phone resends the whole conversation every
exchange, and — when memory arrives — the thing that remembers you has
to be the thing that persists, which is not a handset you might replace.

Bounded on the three axes that run away: **20 turns**, **24k characters**,
**6 hours idle**, and 200 live conversations. The window drops whole
exchanges from the front, never half of one, because a conversation
beginning with an answer to a vanished question reads as a non sequitur.
A failed exchange is not recorded at all — a user turn stored without
its answer leaves two user messages in a row, which the model reads as
the person repeating themselves.

The cost is visible rather than argued about; `turns_recalled` and
`gen_ai.usage.input_tokens` sit on the same log line:

```
turns  in_tok  out    ttft  prompt
    0      96   15  1448ms  I am flying to Vienna on Thursday
    2     122   99  1140ms  what should I pack
    4     183   13   832ms  what day did I say
```

It is held in memory and dies with the process. A conversation is a
session; a restart ends it. That is the honest description of what this
is, and it stops it being mistaken for the memory system it is not.

## Evals

```
vera --eval cmd/vera/evals/smoke.yaml                   # runs and publishes
vera --eval … --eval-publish=false                       # local only
vera --eval … --preface-file other-prompt.txt            # try a variant
```

Six cases against the **real** handler — real model, real history, real
export. A mocked eval only ever proves the mock still matches the mock.

Every case checks something the code already claims. The voice prompt
promises brief, plain, no markdown; `history.go` promises a conversation
remembers its own turns and that conversations do not leak. Until now
nothing would have noticed those breaking.

The scorers are **deliberately deterministic**. A judge model grades
"was that any good", which is the interesting question and the wrong
one to start with: it costs money, it drifts, and it would bury the
plain regressions — markdown creeping back in, a history window off by
one — that cost nothing to catch. Judged scoring is worth adding once
these never fail by accident.

They have teeth. Pointed at a prompt that asks for bulleted lists, the
suite goes 0/6 and says why:

```
FAIL  no-markdown-under-pressure     ·answered ×no_markdown ×brief
        no_markdown: the reply used markdown, which does not survive being spoken
        brief: 172 words; the voice prompt promises a sentence or two (limit 90)
```

`--preface-file` exists so a prompt change can be measured rather than
argued about: run the suite, compare pass rate AND token cost against
the previous run.

## Memory

**History is what survives a turn; memory is what survives a restart.**
Everything here follows from that: a fact worth keeping is one still
true in a conversation that has not happened yet.

```
vera --memories              # what it remembers
vera --forget 3,7            # or --forget all
vera --no-memory             # answer without it, and learn nothing
```

Three decisions worth arguing with:

**Writing is asynchronous, reading is synchronous.** Extraction is a
second model call. In front of the reply it would add its latency to
every exchange, to serve a fact not needed until the *next* one. So the
reply goes out and remembering happens behind it. `Mind.Settle` waits
for it when a process is quitting.

**Everything goes in the prompt; nothing is retrieved.** One person
accumulates tens to low hundreds of durable facts — small enough to
send whole. Embeddings are the right answer at thousands and a way of
looking busy at fifty, and retrieval fails in the worst way: by
silently not finding the thing that mattered.

**Facts are replaced, not accumulated.** Someone who moves from Denver
to Austin has not become a person who lives in two places. Corrections
keep the fact's id, so it stays the same fact rather than a second one
sitting beside the first.

Relative dates are resolved at extraction — "starts in two weeks"
becomes "starts on 2 September 2026" — because a fact that expires
quietly is worse than one never kept.

Memory is stated to the model as things known, with an explicit note
not to raise them unprompted. Without that a model reads a list of
facts about someone as a list of topics it has been asked to bring up,
and every answer becomes a performance of how much it remembers.

Extraction is metered under `gen_ai.operation.name="remember"`, so the
cost of remembering can be told apart from the cost of answering rather
than quietly inflating it.

It is one person's memory. There is no user id anywhere.

## Peer-to-peer

LAN works at home and fails in a hotel: access points routinely isolate
clients from each other at layer 2, so the phone and the Mac are on the
same wifi and cannot exchange a packet. No amount of retrying fixes
something being done on purpose. The way around it is a link that does
not involve the access point — AWDL, which on Apple hardware means
Network.framework, for which there is no Go.

So a **Swift sidecar owns the radio and nothing else**: it advertises
`_vera._tcp` over peer-to-peer, accepts peers, and copies bytes into a
unix socket. It knows nothing about Vera's protocol, its messages, or
its secret. That is deliberate — a clever sidecar means maintaining the
protocol twice, and the arrangement is meant to let Android later get
its own equally stupid sidecar over its own radio while nothing above
the byte stream moves.

The sidecar is **built on first run** from source embedded in the
binary, cached by content hash. Shipping a compiled one would need
signing, would drift from the source beside it, and would be the first
thing to break on a new macOS.

The protocol over that link is **not HTTP** — four bytes of big-endian
length, then JSON, one request per connection. `Transport` was made
message-shaped precisely so this implementation would not have to
invent status codes and header parsing for a link that has neither.

Both transports run at once, each with the same handler; a failure in
one does not take the other down, because losing the radio should not
cost you the wifi. The phone tries the network first — fast, and warm
when you are at home — and goes around it only when nothing answered.
A refused secret does not fall through: a Mac that rejected it over
wifi will reject it over the radio, and retrying turns one clear error
into two slow ones.

The peer id rides in the Bonjour TXT record, so the phone knows *which*
Mac it has found before saying anything to it. Names are for people and
are not unique. **`Hints()` returns nothing for this transport, and
that is the point** — it has no address to offer, which is exactly why
the pairing code was built to carry an identity instead of one.

The app needs `NSBonjourServices` listing `_vera._tcp`; without it
`NWBrowser` returns nothing, with no error, forever.

## Runs

A delegated task used to die when the phone went away — a lift, a
locked screen, a backgrounded app — and a ten-minute task is exactly
the kind you walk away from. The cause was ownership: the work belonged
to the HTTP request, so the request ending ended the work.

Ownership is inverted now. **The run is the thing that exists; a
connection is a view onto it.** `POST /say` starts a run on a detached
context and then watches it; `GET /resume?run=…&from=N` rejoins one,
skipping the frames already read. Any number of watchers can come and
go. The first frame carries the run id, so a phone always knows what to
reattach to.

The phone persists that id the moment it arrives — not on settle, since
the case this exists for is the app dying mid-answer, which would
otherwise be the one case with nothing to rejoin. On launch it rejoins
anything left unfinished and quietly replaces "Interrupted." with what
actually happened.

Frames are kept, not merely forwarded, which is what makes resuming
from the middle possible. Finished runs are held 30 minutes — a
delivery buffer, not a transcript; the transcript lives on the phone.
A run still in flight is never evicted, since that would be the
original bug wearing a hat. A run that stops without a terminal frame
is closed with an error, because a watcher waiting on something that
will never speak again is the one state worse than a failure.

## Delegation

Vera is **never a coding agent**. It knows you, decides what deserves
attention, and hands execution to something already excellent at it.
That keeps Vera off a frontier it would lose on, and turns every future
capability question from "how do I build that" into "who do I hand it
to".

```
vera --workspace DIR          # where delegated work runs
vera --permission-mode MODE   # how much it may do without asking
vera --no-tools               # answer without delegating anything
vera --tool-timeout 10m
```

One tool, `delegate`, running `claude -p` in Vera's own workspace. The
model decides; "what is the capital of Austria" is answered directly and
"write a file and read it back" is handed over. Both are eval cases,
because both failures cost — delegating trivia spends a minute and real
money to produce a sentence the model already had.

**Delegated work is narrated, not hidden.** A task takes seconds to
minutes, and a silent screen for that long reads as broken, so a
`Frame.Status` goes out the moment work starts and the phone shows it
beside the thinking dots. The dots mean *thinking*; a sentence means
*doing*, and only the second is worth minutes of someone's patience.

**The workspace is a floor, not a fence.** The delegate has a shell, and
a shell goes anywhere its user can. What the workspace buys is that the
DEFAULT is contained: an ambiguous task does not begin life in a
repository you care about. `--no-tools` is the actual off switch, and
the startup banner always says which it is.

Cost is money, not tokens — Claude Code bills its own way, so it is
metered separately as `vera.tool.cost` rather than hidden inside the
exchange's token count, which would make delegation look free. A task
runs about **$0.15**.

The failure mode to watch is Vera growing opinions about HOW delegated
work should go. The moment `delegate.go` describes steps rather than
intent, the boundary has moved.

## Watching the delegate

Claude Code speaks OpenTelemetry natively, so this is not a wrapper
reporting on a subprocess from outside — it reports on itself, to the
same place, and the two halves join up.

**One trace, two processes.** Claude Code reads an inbound W3C
`TRACEPARENT` in `-p` mode, so its spans arrive as children of the tool
span Vera opened:

```
streamText gpt-5.6-luna          (vera, root)
└── execute_tool delegate        (vera)
    └── claude_code.interaction  (claude-code)
        ├── claude_code.llm_request ×3
        └── claude_code.tool
            ├── claude_code.tool.execution
            └── claude_code.tool.blocked_on_user
```

Its spend lands under `service.name=claude-code` **on purpose**. Vera's
cost is OpenAI; the delegate's is Anthropic, on a different account and
possibly a different kind of plan. Adding them together would produce a
number that means nothing.

Three env settings are forced on the child, each for a reason found the
hard way:

- `OTEL_METRIC_EXPORT_INTERVAL=2000` — a delegated task lives seconds
  and the OTel default interval is sixty of them, so a short run exports
  its resource and exits before one reading of what it cost.
- **`OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE=cumulative`** —
  Prometheus is cumulative. Grafana Cloud accepts delta metrics with a
  **200 and then drops them**, reporting nothing wrong. Traces are
  unaffected, which is what makes it such a convincing dead end. Vera's
  own metrics were never affected because the Go SDK defaults to
  cumulative.
- `OTEL_SERVICE_NAME=claude-code` — set by stripping the key first, not
  by appending over it.

## The dashboard

`https://bravehalibut2439.grafana.net/d/vera-overview/vera` — four rows,
each one a question rather than a pile of metrics:

- **What can actually stop you** — the subscription windows, as stat
  tiles with thresholds. First, because on a subscription this is the
  only number that can halt work.
- **Does it feel alive** — first sign and first token, log scale.
- **What it costs — two currencies, never added together** — Vera's
  tokens and the delegate's dollars in separate panels. Putting them on
  one axis would be the single most common chart mistake and would imply
  a total that does not exist.
- **What it is doing** — operations per minute by kind.

### first sign vs first token

Building this surfaced a measurement bug. `gen_ai.client.time_to_first_token`
is correct by the convention and misleading about what matters: on a
delegating exchange the model emits no text before the tool call, so its
first *token* arrives only once the delegate has finished.

    first sign   984ms      first token  11671ms      (same exchange)

The person was not staring at nothing for twelve seconds — the status
line appeared in one. So `vera.time_to_first_sign` measures time until
anything at all reached the phone, a status line included, and the
convention's metric is left alone. The gap between the two lines is the
status line earning its place.

## Subscription limits

The one number telemetry cannot give you, and the one that can actually
stop you working.

```
vera --usage                  # print it
vera --usage-interval 15m     # report it (0 to stop)
```

```
session        3%  (resets in 3h)
week          33%  (resets in 2d)
week (Fable)   53%  (resets in 2d)
last 24h     296 requests, 9 sessions
```

On a subscription the dollar figures — `claude_code.cost.usage`, and
the `$0.15` logged per delegation — are **notional**: what those tokens
would have cost on the API, which is not what you pay. What you can run
out of is a percentage of a weekly window, and it exists nowhere except
the text of `claude /usage`.

So this scrapes it, which makes it the one fragile thing here. A
human-readable report is not a contract and its wording will change. It
is therefore written to **fail rather than cope** — a parse that finds
nothing is an error, because a gauge quietly reading 0% is
indistinguishable from a fresh week and would be believed.

Gauges, not counters: a percentage of a limit is a level, and it goes
down when the window rolls over. A counter would read every reset as
data loss.

Scraping is cheap — it creates no session and barely a request — but
the reading is this Mac's view only: *"does not include other devices or
claude.ai"*. Good enough for a dashboard, wrong for an alert you would
trust.

## Not done

**Nothing tells you when it finished.** Work survives the connection
now, but you only learn the answer by opening the app. Being told
wants push notifications, which want a paid Apple developer account.

**Runs do not survive a restart of vera itself.** They are in memory;
quitting the binary abandons whatever is in flight.

**Memory is never revisited.** A fact is corrected only if the person
happens to contradict it. Nothing ages, decays, or is re-examined, so a
plan that silently fell through stays true forever.

**A shared secret, not a trust ceremony.** Good against the other guests
on the wifi; not against someone who has read your disk.
