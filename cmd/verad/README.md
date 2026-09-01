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
remember.go    deciding what was worth keeping
eval.go        the suite runner and its scorers
evals/         the cases themselves
main.go        wiring

../mux/        the multiplexer as Vera sees it; tmux backend
../fleet/      rooms Vera opens for coding agents, and the watch over them
../home/       her home on disk: memory as files, one fact per file
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
forces, so unlanded work is only ever discarded at the machine — and it
puts the question to the person first (see Hands).

Every hook is a doorbell, not a fact: the supervisor re-reads the pane
and the worktree after it rings, and a hook that stops firing degrades
to polling.

### Landing means running

A ship task that says done is landed by the supervisor: merged into the
default branch in the main checkout, its room closed. In **this**
repository that is not enough. A change to `verad` that is merged and
not built is a change nobody is using, and the person finds out by
watching old behaviour and disbelieving the notice — so after the merge
the landing rebuilds every `./cmd/*` into the directory the running
daemon's own binary came from (`os.Executable()`), and says so:

```
landed 05a40191 — vera and verad rebuilt; run vera restart to pick it up
```

Not `go install`: that writes to `$GOPATH/bin`, which is not where
`verad` runs from, which is exactly the bug this fixes. Each binary is
built beside its target and **renamed** over it rather than written in
place — one of them is the process doing the building, and writing new
code into the file a running binary is paged from is how you crash it;
a rename leaves the running inode alone. verad does not restart itself
either — a process cannot replace itself mid-exchange — so the notice
asks.

A build that fails is a landing that failed and goes down the same
path: the task goes `blocked` with the build error, its room stays
open, and a newer `done` is a reason to try again.

It is a convention, `[land] install = true` in `rook.toml` beside
`[land] check`, and it is **on by default for `vera` and off
everywhere else** — because vera is the one repository whose landing
changes the process doing the landing. `install = false` turns it off
here; `install = true` turns it on anywhere.

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
cost. The status line adds up what the exchange spent: while a turn is
in flight it shows that turn, and once it ends the whole conversation —
`seths-mbp · claude-opus-5 · high · chat-1 · $0.0595 · 12.8k tok`, with
where Vera believes you are on the right. The model sits with the
device and the conversation, on the left, where mote draws
`Options.Model` — it used to be on the right because mote read that
field once, at New, and the model can change under a conversation at
any moment. `tui.SetModel` ended that: the picker moves it, and so does
the poll when a `vera say -m` in another window moved it. Where the
model came from is deliberately not on the line — `/model` prints it.
The turn's own
model spend rides on the terminal `/say` frame (`usage`: the tokens the
provider counted, and what they would cost); the tool cards' dollars are
added to it, so one figure covers the model and everything it handed
work to. It survives quitting: the totals are written into the
conversation file with the turn, so `vera chat -c` reopens with the
conversation's cost still on the line. The rail on the right is the fleet — every open task, its title
from the brief, its last word underneath, and one of five states a
person acts on differently (working, idle, blocked, done, failed);
closed tasks come off it. A task whose state turns actionable, or that
lands, also says so in the transcript.

Slash commands are the fleet verbs by hand: `/tasks`, `/start [@repo]
<brief>` opens a room, `/scout`, `/resume`, `/report <id>` (which prints
what it wrote and marks it seen), `/answer <id> <text>`, `/land`,
`/stop [force]`, `/seen`, `/new` for a fresh conversation, `/paste`
(ctrl+v does the same) and `/image <path>` to send a picture with what
you say next, `/dump [note]` for a folder of everything, `/debug` for
what Vera currently believes about where you are — devices, focus,
terminal, integrations — from the same facts the model's preface is built from, and `/quit`. Two more are
about the model itself: `/model` — a card of everything verad can
reach, see below — `/effort`, the reasoning toggle, and `/costs`. `/help` is
mote's: it lists these and the keys. `esc` stops a reply in flight,
`ctrl+c` leaves, `ctrl+t` or `F2` (or `/rail`) hides the rail,
`tab`/`ctrl+o` walk and open tool cards. It exists so iterating on the
mind is typing, not picking up a phone.

**Inside rook the rail starts hidden.** rook has an agents pane showing
the same fleet, and two of them is one too many; the greeting says so
and the toggle still works. `$ROOK_MUX_SOCK` (or the older `$ROOK_SOCK`)
is how the chat knows, and `Options.SideClosed` is how mote is told —
it used to be a `ctrl+t` sent as the program started, which worked and
looked like the person had pressed it.

A scout on the rail is the one row that is done and still wants you:
its report is the deliverable, and until somebody has read it the row
carries mote's `Needs` flag — `◆ needs you · done · report waiting` —
rather than a tick that would say there is nothing to do here.

## Switching the model

The model is a property of the **exchange**, not of the process. Six
things can say what it should be, and the most specific wins:

| | |
| --- | --- |
| this conversation | `/model claude-opus-5`, `/effort high`, or `s` in either picker — remembered, and it sticks |
| this message | `vera say -m claude-opus-5 -e high` — one exchange |
| `--model` / `--effort` | what this daemon was started with |
| the saved default | Enter in the picker — `~/.local/state/vera/model.json` |
| the profile | `profiles/supervisor/profile.md` front matter `model:` |
| the built-in default | |

The profile used to outrank the flag. It does not any more: a profile is
a default about what this agent *is*, and a flag is somebody typing.
The saved default is between the two for the same reason — it is a
person overruling the profile, and it is not somebody typing right now.
So a daemon started with `--model` keeps it, and the choice is written
down for the next one rather than ignored; the answer to the request
says what is actually in force, which is how a caller finds out the
flag won.

`verad` is the single writer. The terminal keeps no idea of its own —
it asks, on the same timer as the rail, and draws the answer, so a
`/model` in one window and a `vera say -m` in another cannot disagree:

```
GET  /models[?conversation=…]                         what it can reach, and what is in force
PUT  /model                     {"model":"…","effort":"…"}   the daemon's own; both empty clears it
GET  /conversations/{id}/model                        what is in force, and who said so
POST /conversations/{id}/model  {"model":"…","effort":"…"}   set it; both empty clears it
```

The two fields on the two setting routes are **two toggles**, not one
setting in two halves. A field left empty is one nobody said anything
about, so `{"effort":"high"}` turns the dial and leaves the model where
it is, and `{"model":"gpt-5"}` moves the model and leaves the dial.
Both empty is the one exception and means "forget this choice". An
effort the named model will not take is refused by name, with what it
does take; an effort merely *carried* onto a model with no dial is
dropped instead, because moving onto `gpt-5.6-terra` is not a mistake
to be refused.

The two per-conversation routes and `PUT /model` answer the same shape:

```json
{"model":"claude-opus-5","effort":"high","provider":"anthropic",
 "model_from":"this conversation","effort_from":"the --effort flag"}
```

`POST /say` also takes optional `model` and `effort` for one exchange.

### What it can reach

`GET /models` is the picker's whole source. A **table** in `models.go`
— name, vendor, the efforts that model will actually accept, a note —
filtered by which keys are on the machine, and priced or not per
`price/`. Not a call to the vendor: no API answers "which of your
models will take this request with these tools at this effort", and the
fact worth having written down was found at the socket. **A provider
with no key contributes no rows**, because a picker offering
`claude-opus-5` on a laptop with no `ANTHROPIC_API_KEY` is a picker
that hands you an error three keystrokes later.

```json
{"default":{"model":"gpt-5.6-luna","effort":"none","from":"the built-in default"},
 "conversation":{"model":"gpt-5","effort":"medium"},
 "models":[{"name":"gpt-5.6-luna","provider":"openai","wire":"responses",
            "efforts":["none","low","medium","high"],
            "note":"the dial, via the responses API","priced":true},
           {"name":"gpt-5.6-terra","provider":"openai","efforts":["none"],
            "note":"effort none only (chat completions)","priced":true},
           {"name":"claude-opus-5","provider":"anthropic",
            "efforts":["low","medium","high","max"],"priced":true}]}
```

`wire` is which of a vendor's APIs reaches it, where the vendor has more
than one and they do not take the same request. Absent is the ordinary
one — `/chat/completions` for OpenAI, which is what mote speaks and what
every endpoint imitating it speaks. `responses` is OpenAI's own
`/v1/responses` (package `responses/`), and it is there for one reason:
**"gpt-5.6-luna takes effort none" was never a fact about the model.**
It is a fact about chat completions, which refuses a `reasoning_effort`
other than none when there are function tools in the request. The same
model on `/v1/responses` takes the dial with the same tools, so luna is
reached through that and its row says the four efforts it accepts.
`gpt-5.6-terra` has not been tried there and stays where it was; the
line below moves it without a rebuild if it turns out to behave the
same.

`conversation` is absent when this conversation has chosen nothing of
its own, which is not the same as having chosen the default.
`$VERA_MODELS` adds rows, or corrects them, without a rebuild — one
entry per model, `name=provider:eff1|eff2`, with the wire after a slash
when it is not the ordinary one:

```
VERA_MODELS="my-local-7b=openai:none|high, gpt-5=openai:none"
VERA_MODELS="gpt-5.6-terra=openai/responses:none|low|medium|high"
```

An entry that will not parse is named in the log at startup and
dropped; it does not take the good ones with it, the same courtesy
`$VERA_PRICES` gets.

`/model` in the chat draws that list as a card (mote's `tui.Pick`):
one row per model, `via <provider> · <note>`, `unpriced` said out loud
when nobody has a price, and a tick on the one this conversation is
using. Enter makes it Vera's own default, `s` moves this conversation
only, Esc leaves nothing behind. `/model <name>` is still the typed
form, and still the fastest way when you know the answer.

`/effort` is a second card, and deliberately not a dial on the first.
They are two questions: which model answers is a list that comes and
goes with the keys on the machine, and how hard it thinks is the same
three words — **low, medium, high**, as in Claude Code — on whichever
model you are already on. A dial inside the model card restated the
effort on every model change and only caught an impossible combination
on the way out. Anthropic's `max` and the OpenAI reasoning models'
`minimal` are still reachable by typing `/effort max`; a toggle with
seven positions is a menu. A model whose row is `["none"]` has no dial
at all, and `/effort` says that rather than drawing three options verad
would refuse one at a time:

```
gpt-5.6-terra has no reasoning dial — it takes effort none. /model moves to one that does.
```

Which wire a model reaches is mote's decision, from the name and the
keys on this machine — except for the second OpenAI API, which is
verad's, from the row's `wire`. It is made per exchange through one
cached provider per model, and the daemon's own model comes out of the
same place as everything else, so the default cannot end up on a
different wire from the one its row names. Setting a model builds that wire first, so a
machine that cannot reach it at all says so on the way in — but whether
the far end has heard of the *name* is only knowable by asking, and a
name it has not heard of comes back as a 404 on the first thing said,
with the name in it.

The effort dial still belongs to the vendor: on the Anthropic side it is
passed through, and on the OpenAI-compatible one reasoning is turned off
and effort left at `none` unless somebody explicitly asked for one —
that endpoint refuses function tools otherwise. One function decides it
for startup and for every exchange after (`tune`).

Every journal line records the effort beside the model. The same model
at two efforts is two different bills and two different waits, and a
record that named only the model could not tell them apart.

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

It also keeps what the model kept. A model that thinks signs its
reasoning, and the Messages API wants those blocks handed back on the
next turn — in front of the tool call they led to — or it refuses the
whole conversation. mote's `provider.Message` carries them opaquely as
`Raw`; verad puts one on every assistant turn it builds, inside the
tool loop and across exchanges, and stamps the thread with the model
that produced it. Change model mid-conversation and what was said
survives; how it was reasoned does not, because one model's signature
is not another model's to read.

`--thinking-display omitted` asks for the reasoning to be kept and
signed but not returned; `summarized` is what happens when nobody
says. It is a different dial from whether the model thinks at all.

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

It lives in Vera's home, which is a directory of Markdown:

```
~/vera/                  $VERA_HOME overrides; `vera home` prints it
  MEMORY.md              the index — one line per memory, and the part
                         that goes into every prompt
  memory/<slug>.md       one fact per file: front matter (name,
                         description, type, since, from) then the fact
  projects/<name>.md     what she knows about a repository
  notes/                 hers, to write in later
  profiles/supervisor/   profile.md and policy.toml — see Hands
```

`$VERA_HOME` moves it. verad is usually started by launchd, which
passes no shell environment, so put it in `~/.config/vera/*.env` beside
everything else; `vera home` reads the same file, so the two never
disagree about where memory is.

```
vera home                    # where it all is
vera --memories              # what she remembers, and which file each is
vera --forget lives-in-vienna   # by slug; or --forget all
vera --no-memory             # answer without it, and learn nothing
```

It used to be one JSON array under `~/.local/state`. verad migrates
that file once on start, keeps it as `memory.json.migrated`, and says
so in the log. **The files are the truth and `MEMORY.md` is derived
from them**, so a file edited by hand wins, deleting a file is
forgetting it, and an index mangled by hand heals on the next write.
That is the whole point: a memory you cannot see is a memory you
cannot correct, and one wrong fact quietly colours every answer after
it.

Four decisions worth arguing with:

**She writes it herself.** There used to be a second model call behind
every reply that read the exchange and decided what was worth keeping.
It is gone. The preface tells her where the files are and what they
look like, her home is auto for `write` and `edit`, and she keeps the
index and the files saying the same thing. A diary written by somebody
else is the wrong shape: she never chose any of it, could not correct
it, and the one thing she could not do was throw a wrong fact away.
Relative dates are still resolved to absolute ones, because a fact that
expires quietly is worse than one never kept — the prompt asks for it
now instead of the extractor.

**The index goes in the prompt; nothing is retrieved.** One person
accumulates tens to low hundreds of durable facts — small enough to
send whole. Embeddings are the right answer at thousands and a way of
looking busy at fifty, and retrieval fails in the worst way: by
silently not finding the thing that mattered. What goes in is
`MEMORY.md`, capped at 6 kB, and a cap that trims says out loud that it
did — the bodies are for a person reading them, and for `read` and
`search` when she wants one.

**Facts are replaced, not accumulated.** Someone who moves from Denver
to Austin has not become a person who lives in two places. The slug is
what makes that mechanical: `lives-in-austin` written over
`lives-in-denver` is a file rewritten, and a second file would have
been a contradiction the model then arbitrates on every turn.

**The directory can change under us.** A person with an editor is the
ordinary case here, not a race to defend against, so every read checks
the directory and re-reads if it moved, and every write derives the
index from the files rather than from the copy in hand. One writer lock
per process; each file goes down through a temporary and a rename.

Memory is stated to the model as things known, with an explicit note
not to raise them unprompted. Without that a model reads a list of
facts about someone as a list of topics it has been asked to bring up,
and every answer becomes a performance of how much it remembers.

It is one person's memory. There is no user id anywhere.

**The fleet remembers repositories too.** The first task in a repo
writes `projects/<name>.md` — where it is, what it branches from, what
its `rook.toml` says — and a task that lands appends a line to it. The
fleet already learned all of that on every task and then threw it away.
Project files are created once and only appended to afterwards: a
person editing "what this repo is" is exactly the point.

A project file reaches the prompt only when the repository is in play:
they named it, or the fleet has a task open in it. At most two, capped.
Every known repository's file in every prompt would be most of the
prompt and none of it read.

The eval suite's memory cases now measure whether she *chooses* to
write, which is a harder and more honest thing to measure than whether
an extractor fired. A turn's `after_learning` is a no-op: memory is
written inside the exchange that decided to write it.

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

## Hands

Vera is never a coding agent, and she was also never able to open a
file. Those are different claims, and only the first one is worth
keeping. She has mote's seven now — `read`, `list`, `search`, `write`,
`edit`, `delete`, `run` — and `delegate` and `fleet` beside them in the
same registry, all of them decided by a policy that lives in her home
as a file a person can read:

```
~/vera/profiles/supervisor/
  profile.md     what she is; appended to the voice in mind.go
  policy.toml    allow / ask / deny, by tool, by path glob, by command
                 prefix, in the order the rules are tried
```

verad writes mote's worked example there the first time and never
again; after that the file is yours, and a typo in it is an error at
startup rather than a surprise at midnight. `--no-tools` turns them off
along with the delegate: both are a real grant of capability on this
machine.

**The boundary is the same one the delegate drew.** She may look at
anything. She may write in her own home. She may **not** change a
project — that goes to a task in its own copy of the repository — and
what the model is told when it tries is the profile's own sentence,
*"start a task for that"*, rather than a status code. A refusal that
says what to do instead is a refusal the model can act on.

There is one path from a call to a result: the policy decides it, the
tool runs it, the journal records it — the same for `read` as for
`fleet`. Every call is an `execute_tool` on the record now, labelled
with the tool's own name, so what a delegation cost and how long a
search took are read the same way. Handing work away is what she should reach for before doing it
herself, so `delegate` and `fleet` are listed first, which is the order
the model reads them in.

Four things are decided in code rather than in the file, because the
file cannot know them:

- **Where her home actually is.** The file says `~/vera`; `$VERA_HOME`
  can move it, and then the file's rule matches nothing.
- **Which repositories are projects.** `${root}` is the fleet's known
  projects, refreshed before every exchange, *added to* the list in the
  file rather than replacing it — a repository the fleet has not
  noticed yet is still not hers to edit.
- **That she may not edit her own profile.** `~/vera/**` is hers, and
  her profile lives under `~/vera`. Without this she could answer a
  policy she disliked by rewriting it — an escalation that survives a
  restart and reads, in the journal, like an ordinary allowed write. A
  rule she can rewrite is not a rule.
- **What to say about the two tools that are not mote's.** The profile
  chooses among built-ins by name and never heard of `delegate` or
  `fleet`, so a file with nothing to say about them would send every
  delegation to the phone to be asked about. Both are `allow` unless
  the file says otherwise — except `fleet` with `action: stop`, which
  asks, because it abandons work somebody did. A rule keys on the
  argument that is the whole of the question:

  ```toml
  [[rules]]
  tools = ["fleet"]
  when = { action = "stop" }
  then = "ask"
  reason = "stopping a task abandons the work in it — check they meant to"
  ```

  That is seeded into your `policy.toml`, so the file you read says
  what happens and you can change it; the same rule is built in behind
  everything the file says, for a home written before it existed.

They are the registry's **own** rather than the profile's: `tools:` in
`profile.md` narrows *around* them, so a profile that forgot to list
`fleet` is not a Vera who cannot hand work away.

### Other people's tools

The third file in a profile is `mcp.toml`:

```toml
[[servers]]
name    = "files"
command = "mcp-server-filesystem"
args    = ["~/notes"]

[[servers]]
name    = "docs"
url     = "https://mcp.example.com/mcp"
headers = { Authorization = "Bearer ${DOCS_TOKEN}" }
```

verad connects to them at startup, and every tool every server offers
lands in the same registry as `read` and `write` — as `files__read`,
with the server's own JSON Schema — decided by the same policy. The
supervisor's `default = "ask"` means the first call to one stops and
asks; a `[tools]` line by name is how you stop being asked.

The separator is `__` rather than the `.` mote reads best. A function
name in an OpenAI request body and a tool name in an Anthropic one
must match `[a-zA-Z0-9_-]{1,64}`, and a dot is not in it.

No file means no servers, which is not an error. A file that is there
and wrong stops verad — a typo somebody can fix. A **server** that
will not answer does not: it is a line in the log and a line in
`vera mcp`, which prints every server, whether it answered, and every
tool under the name the model sees:

```
$ vera mcp
files  (stdio) mcp-server-filesystem ~/notes
  calls itself filesystem 1.2
  files__read   Read a file.
  files__write  Write a file.
```

### The ask

A call the policy neither allows nor denies stops and asks.

```
/say  → {"ask":{"id","name","args","text"}}   the question, mid-stream
POST /ask/{id}  {"choice":"yes|no|always"}    the answer, on its own request
```

The exchange parks on the answer; nothing else arrives until it comes.
The frame is `agent.KindAsk` on mote's side, so the terminal draws it
as a card with `y` / `n` / `a` and posts the answer back — `vera`'s
agent is an `agent.Answerer`, which the terminal finds by type
assertion.

Two minutes of silence answers **no**. Silence is not consent, and a
call parked forever holds the exchange, and its model context, open
behind it. `vera say` answers no to every ask and prints what was
wanted to stderr: a script that answered yes on a person's behalf would
be the worst possible client.

An **always** is remembered for the rest of that conversation and no
other — the tool plus a reach, which is the directory for a file, the
program for a command, and whatever the tool itself says its scope is.
The fleet says its verb, so an always said to `fleet stop` covers
stopping and not starting. A grant that outlived the conversation would
be a policy edit, and policy edits belong in the file.

A tool's output streams as `tool_output` frames while it runs, capped
at 32 kB on the wire; the result is capped separately at 8 kB.

**Every decision is in the journal**, per round: `decision` is allow,
ask or deny, `answer` is what the person said, `reason` is the
sentence the model was told. `vera dump`'s transcript shows them —
"denied (start a task for that)", "asked → yes" — because the decision
is usually the answer to the question somebody came with.

## Pictures

A screenshot is the cheapest sentence there is: *this, here, look*.
Saying it in prose costs a paragraph and loses the part that mattered.
So every door Vera answers takes one — and every one of them hands it
to somebody who can actually see it, because **Vera cannot**.

That asymmetry is the design, and it is not laziness. Her own model is
reached through mote's `provider`, whose `Message` is text and nothing
else; there is no shape in that interface a picture fits. But the
agents she hands work to read images off the disk perfectly well. So an
image is **kept once and travels as a path**:

```
paste ──► POST /say {images:[{name,mime,data}]}
            │
            ├─ attach.Store  ~/.local/state/vera/images/<conversation>/<sha>.png
            │
            ├─ the turn the model reads gains one sentence:
            │  "They attached an image … you cannot see it yourself …
            │   any task you hand to the delegate or the fleet is given
            │   these files automatically"
            │
            └─ the paths ride the exchange's context onto every tool Handle
                 delegate ─► claude -p "<task>\n\nRead them before you start:\n  /…/ab12.png"
                 fleet    ─► the same paragraph in the brief, and a
                             one-line form in an answer typed into a room
```

The two forms exist because a brief is *handed over* as a subprocess
argument and an answer is *typed into a pane*. A newline typed into a
terminal is a Return, and a Return in the middle of an answer sends
half of it — so `attach.Line` says the same thing with no line breaks
of its own.

The model never copies a path around: it never saw one, and a path it
invented would be a file that is not there. It only has to decide *who
does the work*, which is the decision it was already making.

**Where they come from.** Four doors, and all of them paste:

| door | how |
| --- | --- |
| the phone | Attach ▸ Photo (library) or Paste (clipboard) |
| the Mac's ask panel | ⌘V — but only when the pasteboard holds a picture, so pasting a URL into the field is untouched |
| `vera chat` | ctrl+v takes the pasteboard; `/paste` is the same fetch typed, `/image <path>` takes a file |
| `vera say` | `-i shot.png`, repeatable |

A terminal **cannot be pasted into** — not by ⌘V, which is the
emulator's key and puts text on the wire and nothing else, in rook or
anywhere else. So the chat goes and reads the pasteboard itself, on a
key the terminal really does see: **ctrl+v** takes the picture if
there is one and the words if there is not, so the gesture means here
what it means everywhere else and text paste is untouched. `/paste` is
the same fetch, typed. The screenshot keystroke is still ⌘⇧⌃4, in
whatever you were looking at.

An attached picture **waits** for words rather than being sent on its
own, because the picture and the sentence about it are one message
typed at two different moments — and because the exchange that carries
it is the one that decides who the work goes to. It is taken by exactly
one message; `/new` drops it. A picture with **no** words is still a
whole message: pointing at something is a thing people do.

**What is kept, and what is refused.** PNG, JPEG, GIF and WebP,
sniffed rather than believed — a caller that says PNG and sends a PDF
is refused by name, because the alternative is an agent handed a file
it cannot read and a person told the work is under way. 16 MB an image,
eight an message, content-addressed so the same screenshot pasted twice
is one file, and a conversation nobody has added to for a month is
swept. `attach.Read` sniffs too, so a mistyped file name is refused
while you are typing rather than after an exchange has been paid for.

The wire carries **bytes, never a path**. A path field would mean "read
whatever file I name", arriving over a network, from a device, to be
handed to an agent with a shell. `attach.Read` is how a local caller
turns a file into a message, on its own side of the wire, where the
file is already its to read.

**A picture that could not be kept is put in the turn**, not sent as an
error frame, and she says so herself. That is a decision about clients
rather than about taste: an error frame is treated as *terminal* by two
of the four things that read this wire — the phone breaks its read loop
on one, the Mac panel throws — so a refusal sent that way would
truncate the answer it was trying to annotate. The turn reaches every
client, because the turn *is* the answer. The words are still answered.
The one behaviour worth ruling out is silently dropping the evidence
and answering anyway, which leaves somebody reading a reply about the
wrong thing with no way to tell why.

**Three platform edges, found the hard way:**

- **Vera has no eyes, and it is mote's to change.** `provider.Message`
  is `{Role, Text, Calls, CallID, Error, Raw}`. When it grows a picture,
  the first half of `images.go` changes and the second half — the paths
  onto the Handle, the delegate, the fleet — does not.
- **`the clipboard as «class PNGf»` does not work on a current macOS.**
  It is in every answer on the internet. It fails with
  `errAEAccessDenied (-10003)` even when `clipboard info` says the PNG
  is right there, and the failure is **not catchable by the script's
  own `try`**. `/paste` asks `NSPasteboard` directly, through JXA's ObjC
  bridge, and works.
- **A phone's own pictures are HEIC**, which none of the four formats
  covers. `ios/Vera/Attachment.swift` re-encodes anything that is not
  already one of them, shrinking by *quality* before *size* so the words
  in a screenshot survive. A screenshot arrives as a PNG and crosses
  untouched.

Two things this deliberately does not do. `/start` and `/scout` by hand
carry no picture — talk to Vera and she starts the task, which is the
designed path and the one that decides who the work goes to. And the
peer link's frame cap is 32 MB rather than the LAN's: over the radio a
very large message reads as broken, and the number is also an
allocation somebody else chooses.

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

## What a turn costs

Two different questions, and they must not be answered with two
different tables.

`vera dump` prices whole Claude Code sessions after the fact; the chat's
status line prices the turn you are watching. Both read
`price` — one table of API list prices per million tokens, by model
family, longest match wins. A family with no row gets **tokens and no
dollars**: an unknown model is not a free one, and the wire says so
explicitly (`priced`) so a screen can show the tokens and stay quiet
about the money rather than print a confident `$0.00`.

The figures are **notional** — API list prices, which a subscription
does not pay. What they are good for is comparison: which turn was
expensive, which task ran away. The number that can actually stop you is
below, under Subscription limits.

Rows whose cache rates are not published separately (the OpenAI ones)
leave them zero, and cached tokens are then charged at the input rate —
conservative, and never an invented discount.

To correct a price, or add a model, without a rebuild:

```
VERA_PRICES="gpt-5.6-luna=0.20/1.20,opus=5/6.25/0.5/25"
```

Each entry is `family=input/output` or
`family=input/cache-write/cache-read/output`, USD per million tokens.
Entries that cannot be read are logged by name at startup and the rest
are still used — a typo should be visible in the log, not in the
figure.

## Comparing what they cost

```
vera costs [--since 7d|24h|all] [--by model|conversation|day]
```

and `/costs 24h by day` in the chat, which prints the same table.

It reads the journal and asks nobody: `verad` does not have to be
running, and a machine that has been off for a week can still say what
last week cost. A row is exchanges, uncached / cached / output tokens,
dollars at list, the **median and p90 first sign** — the moment anything
at all reached the screen, which for a delegating exchange is a status
line long before the first word — tool rounds per exchange, and what the
agents those exchanges started spent.

```
the last 7d · 128 exchanges · by model, oldest 2026-08-21 09:14

model                 exch  input  cached  out   $        sign p50/p90  tools/exch  fleet $
claude-opus-5 · high  42    1.2M   980.0k  45.0k  $3.4100  1.2s / 4.8s  2.3         $12.4000
gpt-5.6-luna · none   86    340.0k 0       12.0k  $0.0800  600ms / 1.1s 0.4         —
total                 128   1.5M   980.0k  57.0k  $3.4900  900ms / 3.9s 1.6         $12.4000
```

Three things it deliberately does not do. It does not invent a price for
a model the table does not know — those rows show a dash and the report
names the models underneath. It does not average latency: the average of
a first-sign time is a number nobody has ever waited. And it does not
fold what a delegated agent spent into the exchange's own token count —
Claude Code bills its own way, and merging the two would make delegation
look free. The fleet column reads the same session files `vera dump`
does, through the same code, so the two cannot disagree.

Grouping by model includes the effort, because that is most of what is
being compared.

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
happens to contradict it and she chooses to act on it. Nothing ages,
decays, or is re-examined, so a plan that silently fell through stays
true forever.

**Nothing deletes.** The six tools do not include one, so dropping a
fact means `run rm`, which asks. Fine for something rare and wrong for
something routine.

**An ask reaches whoever is listening, not whoever asked.** The
question goes out on the /say stream the exchange is streaming to. A
phone that has gone into a pocket sees it on `resume`; a second client
watching the same run sees it too, and either may answer.

**MCP is tools and nothing else.** Resources, prompts, sampling and
progress notifications are ignored. A server that asks *our* model
something is a loop mote does not have yet.

**Nothing bounds an MCP call.** A server that never answers holds the
exchange open behind it; the exchange's own context is the only limit,
and the two-minute ask timeout does not cover a run.

**A shared secret, not a trust ceremony.** Good against the other guests
on the wifi; not against someone who has read your disk.
