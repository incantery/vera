# vera

The smallest useful piece of rook: one binary, no install, no account.
It watches every Claude Code session on your machine and gives you the
**membrane** — live session states on a local web page, and a *drive*:
hand a session a goal, and a supervisor agent keeps the conversation
going — prompt, judge the reply, prompt again — until the goal is met
or the turn budget runs out.

```
go run github.com/incantery/rook-host/engine/cmd/vera@latest
```

Open http://localhost:4770. That's it. (First ride: QUICKSTART.md.)

## What you get

- **The board is the home screen.** Tell rook what needs doing; it
  keeps a kanban of tasks per agent — captured, driven, judged, and
  proposed back to you, every transition an audited event. The agents
  rail on the left switches boards; `chat →` opens the conversation.
- **Every session, honestly.** States are derived from Claude Code's
  own transcripts (`~/.claude/projects`), read-only: *needs you*,
  *blocked?*, *working*, *idle* — plus titles, branches, and context
  occupancy. Nothing is installed into anything; stop the binary and
  no trace remains.
- **The drive.** The example that started this: someone went back and
  forth five times to get Claude to *hypothesize* about its own
  behavior — it kept deflecting with "I don't have access to that."
  A drive automates exactly that persistence. Type the goal; the loop
  prompts the session, an LLM judge reads each reply against the goal,
  and either declares it met or writes the next push. Bounded turns,
  every round on the record, stop button.
- **Forks, never keystrokes.** Drives run `claude -p --resume` — each
  turn continues the conversation in a *fork*, so your live terminal
  is never typed into and the original session is never touched. When
  a drive finishes, the page hands you `claude --resume <fork-id>` to
  take the wheel back with the drive's progress in context.

## The judge

Drives need an OpenAI-compatible endpoint for the judge (the watcher
works without one). In order: `$OPENAI_API_KEY`, then
`~/.config/rook/openai_key`, or point `--api-base` at any compatible
server — ollama and friends need no key.

## Flags

```
--addr 127.0.0.1:4770   listen address (":4770" opens it to your LAN — your call)
--dir  ~/.claude/projects
--turns 4               prompts a drive may send before giving up
--model gpt-5.6-luna    judge model
--api-base              judge endpoint (default OpenAI)
--claude                the claude binary (default: from PATH)
```

## Hacking on the UI

The page is a SvelteKit SPA (Svelte 5 + Tailwind 4) under
`cmd/vera/web`, built to `web/build` and embedded into the binary
— the build output is committed so `go run` needs no node. The loop:

```
cd engine/cmd/vera/web
npm install
npm run dev      # live-reloads against a running vera (proxies /api to :4770)
npm run build    # then rebuild the Go binary to re-embed
```

## The rest of rook

vera is the on-ramp, not the destination. The same membrane runs
richer inside the [rook terminal](https://rookide.com) — live pane
telemetry sharpens the states, drives type into real sessions under
human-wins gates, digests summarize finished turns — and
[rook-cloud](https://cloud.rookide.com) puts it on your phone. Every
piece speaks the schemas in this repository; use as much or as little
as you like.
