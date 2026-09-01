# The event stream

Vera writes down a great deal and, until this, none of it was one
thing you could read. The journal has every exchange with the model,
the fleet store has every task and its status log, the attention model
has where the person was looking, rook publishes a state snapshot, git
holds the commits. All of it is true. A person coming back on Monday —
or, much more often here, a fresh agent with no context at all — had to
open five stores in three formats across two repositories and
reconstruct a week by hand.

The stream is that one thing: an append-only record of the moments
worth remembering, across every repository this machine works in.

It is an **index, not a second copy**. An event says a task went to a
decision; the fleet store still holds what the decision was. Follow the
keys back to the store that owns the detail.

## The file

`$XDG_STATE_HOME/vera/events/` (default `~/.local/state/vera/events/`),
one file per UTC day, one JSON object per line:

```
~/.local/state/vera/events/2026-09-01.jsonl
~/.local/state/vera/events/2026-09-02.jsonl
~/.local/state/vera/events/cursors/          # how far each git scan got
```

```json
{"at":"2026-09-01T23:04:11-04:00","repo":"rook","source":"git","kind":"git.commit","text":"tabs: the ordinal sits against its chip (d81123f)","subject":"d81123f","project":"/Users/you/src/rook","fields":{"sha":"d81123f…","author":"seth","refs":"main"}}
```

| field | meaning |
|---|---|
| `at` | RFC3339, required |
| `source` | who reported it — `fleet`, `vera`, `git`, `machine`, `rook`, or an outside publisher's own name. **Required** |
| `kind` | what sort of moment, dotted general-to-specific. **Required** |
| `text` | the whole event in one past-tense line, readable with no other column visible. **Required**, bounded at 500 bytes |
| `repo` | the short repository name — `vera`, `rook` — or absent when it is about neither |
| `subject` | what it was about in the source's own naming: a short sha, a workspace, a conversation id |
| `task` | the fleet task id, when the moment belonged to one |
| `project` | the repository root on disk. `repo` is what you filter on; this is what you `cd` to |
| `fields` | anything else the source wanted kept, as strings |

A line that does not parse is skipped, never fatal. Shards older than
90 days are deleted whole.

**Why a text file.** The stream is written by a daemon that gets
killed, read by a CLI that must work when that daemon is down, and —
the deciding case — read by coding agents who reach for `grep` before
they reach for anything else. JSONL day-shards are greppable, append
atomically under the size of a pipe write, lose at most their last line
to a crash, and prune by `rm`. A database would buy indexes that a
stream of a few hundred human-rate lines a day does not need. It is the
same shape as the journal next door on purpose: two record formats in
one state directory is one too many.

## What is in it

| kind | who says it | when |
|---|---|---|
| `task.spawned` | fleet | a room was opened for an agent |
| `task.said` | fleet | a line was appended to a task's status log — **the agent's own words**, kept verbatim |
| `task.waiting` `task.decision` `task.held` `task.stale` `task.finished` `task.broken` `task.gone` `task.closed` `task.interrupted` | fleet | Vera came to believe something new about a task |
| `task.landed` / `task.land-failed` | fleet | the branch went home, or would not |
| `vera.asked` / `vera.failed` | vera | one exchange: the question and the shape of the answer, never the answer — the journal has that |
| `git.commit` | git | a commit appeared in a repository Vera knows |
| `git.merge` | git | …and it had two parents: a branch went home. Kept apart because its content is already in the stream as the branch's own commits — `--kind git.commit` is what was done, `--kind git.merge` is what landed |
| `machine.away` / `machine.back` | machine | the lid shut, or the network went |
| `rook.gone` / `rook.back` | rook | the terminal engine stopped answering, or answered again |

`task.running` and `task.quiet` are deliberately **not** in it. The
stream is what mattered, and a task going quiet for four minutes and
coming back did not.

The commits half goes and looks rather than being told: every
repository Vera knows (`fleet.Projects` — the ones with a pane open and
the ones a task has ever run in) is swept every five minutes, with a
cursor per repository so each commit is reported exactly once. That is
what makes a week in rook as present in the stream as a week in vera.

## Reading it

```sh
vera events                        # the last day, grouped by repository
vera events --since 7d             # a week
vera events --repo rook            # one repository
vera events --kind task.           # every task moment
vera events --task T-9e4           # one task's whole history
vera events -q "which database"    # words anywhere in the line
vera events --flat                 # one line each, for a pipe
vera events --json                 # one object per line, for a program
```

It reads files and asks nobody: verad does not have to be running.
That is not a convenience — the commonest reader is an agent handed a
repository and no context, and the second is a person working out why
the daemon is down.

In the chat, `/events [7d] [@repo] [words]` — the same thing, on the
screen already open. To Vera herself it is the `recent` tool, which is
how she answers "what have we been doing".

Over the wire, authed like the rest of the LAN surface:

```sh
curl -H "Authorization: Bearer $SECRET" \
  'http://127.0.0.1:4780/events?since=7d&repo=rook&kind=task.&limit=50'
curl -H "Authorization: Bearer $SECRET" \
  'http://127.0.0.1:4780/events?since=7d&format=text'     # or format=markdown
```

`since` takes `24h`, `7d`, `2w` or `all`; `repo`, `source`, `task` match
exactly; `kind` matches exactly or as a prefix when it ends in a dot;
`q` is a substring. The default window is a day.

And, always, the file itself:

```sh
grep -h 'task.decision' ~/.local/state/vera/events/*.jsonl | tail -20
```

## Publishing into it

```
POST http://127.0.0.1:4780/events
```

Loopback only, **no secret**, exactly like the fleet's hooks: everything
that would publish into this is a program on this Mac, and a secret they
have to carry is a secret in a dotfile. Reading is authed because the
stream says what the person has been doing all week; writing is not,
because it does not say anything back.

The body is one JSON event, or a stream of them (concatenated or
newline-delimited), up to 256 KB. `source`, `kind` and `text` are
required and anything missing one is refused **with the reason** — a
publisher that is silently dropped writes into the void for months. `at`
is optional; a missing one, or one from the future, becomes the arrival
time. A publisher does not get to write the stream's history.

```sh
curl -s -X POST http://127.0.0.1:4780/events -d '
{"source":"rook","kind":"rook.workspace","repo":"rook","text":"opened the workspace scratch"}'
```

### From rook

Rook publishes nothing into this today and does not need to: what
happens in the rook *repository* arrives through the commit sweep, and
what happens to rook *tasks* arrives through the fleet. The engine's own
outages Vera notices herself, because every pane she is watching
disappears at once.

When rook does want a say, it already has the right shape for it.
`rook watch` is newline-delimited JSON, snapshot first, one per change,
and rook's own position is that hooks belong in a shell loop on top of
it rather than in the engine (`docs/surfaces.md`, "Not hooks"). So a
publisher is a loop, not a patch:

```sh
rook watch | while read -r snapshot; do
  # …decide what in this snapshot is worth remembering, then:
  curl -s -X POST http://127.0.0.1:4780/events -d "$line"
done
```

The one thing worth agreeing on across the two repositories is the
`source` name — `rook` — and the `rook.` prefix on kinds, so that
`vera events --source rook` means what it looks like. Nothing else needs
coordinating: the door takes any `kind` it has never heard of.

This mirrors rook's own inbound contract (`rook/docs/attention.md`)
turned the other way round. Rook's attention feed **is** the current
set — a publisher rewrites its whole file and stale items expire in 24
hours, because the bar shows attention debt, not activity. This is the
opposite obligation: it is a log, it is never rewritten, and nothing in
it expires until the shard is pruned. The two are complementary, and
neither should grow into the other.
