# The chat's vocabulary

`vera chat` is a pane inside rook that speaks the phone's wire. The
screen is mote's (`../mote/tui`): streaming markdown, tool cards, a
rail, slash commands, an input box and a status line. What is written
on it is Vera's, and this is the vocabulary — the counterpart to
rook's `docs/surfaces.md`, so that the terminal and the terminal it
runs inside can be checked against each other by eye.

It comes from a design reference: Claude Design project **"TUI app
Polish Discussion"**, turn 3, `TUI Implementation Reference.dc.html`
(`https://claude.ai/design/p/fcc77384-2e58-4e98-82b2-66d3d6ff358c`).
Nine options, 3a–3i, over the existing screenshot; they add no
concepts and change only grammar, hierarchy and state. 3a–3c are
rook's and landed there (rook `c2f2e5c`). 3d–3g are this terminal's.

The rule running through all nine: **one state per channel.**

## The glyphs

| glyph | means | fleet states |
| --- | --- | --- |
| `◐` | working | running, quiet |
| `◇` | needs you | waiting, blocked, held |
| `○` | quiet | stale, interrupted |
| `✓` | done | finished, landed, closed |
| `×` | failed | broken, gone |
| `●` | unread | *not a state* — a second channel |

Three rules go with them, and they are the difference between this
list and a palette:

- **Every state has its own shape.** The line reads with the colour
  off, which is what a notice in mote's transcript is: one dim style
  for the whole block.
- **`×` is failure only.** A task waiting on a word has not gone
  wrong — it is asking — and painting the ask like a crash teaches a
  glance to read alarm where there is none. Blocked is `◇`.
- **`●` means unread and nothing else.** A task can be `✓` done *and*
  `●` unread in the same breath; they are two channels on one row.

`cmd/vera/grammar.go` is the only place any of it is written down, and
`TestEveryStateHasItsOwnShape` fails if a new state collapses into an
existing word by accident.

## A lifecycle event

Two lines: what it is and what it is called, then what it last said
and the one command that moves it on.

```
  · ✓ Scout reported · Find out why it is slow ●
    Findings — the cache prefix changes every turn · /report 1ea6a4b5
```

The id appears **only inside the command that takes one**. An id is an
argument, not a name; the head is the task in the person's own words.

Nothing on the second line restates a verb the command already says —
"ready to land" gives way to `/land a1` — except when the task said
nothing at all, where a line with only a command on it never says what
happened.

## An error

Three parts: what failed, what that left alone, what to do.

```
✗ gpt-5.6-terra does not expose a reasoning-effort control
  → model and settings unchanged · /model shows what does
```

The middle part is the point. After a command that would not run, "no"
is not enough: the person has to know the machine is where they left
it. `failure()` in `grammar.go` builds it.

A capability that is **absent** is said to be absent — not described
as a setting turned to "none". A model whose only effort is `none` has
no dial; `takesEffort` in `cmd/verad/models.go` says so, and the
status line drops `· none` for the same reason (`Resolution.Short`).

## A tool receipt

One dim line that reads like a sentence, expandable for the arguments:

```
▸ ✓ fleet · start scout vera "Investigate…" · 193ms
```

`translate` in `cmd/vera/agent.go` hands mote a summary for the two
tools this terminal knows the shape of (`fleet`, `delegate`); mote
summarizes the arguments itself for everything else, which is the
right answer for a tool the chat has never heard of.

## The completion column

`Options.Commands`' Help is one column of the popup, beside a name the
popup has **already** printed. So it says what the command does first
and how to type it after the `·`, and never opens by repeating its own
name — a line that has to be truncated to fit is a line whose first
half was the only half anybody read.

## The status line

What is true of **this conversation**, and nothing else:

```
vera · gpt-5.6-luna · chat-20260902-072301 · $0.0015 · 6.9k tok
```

Struck off it, and where each went:

| dropped | why | where it is now |
| --- | --- | --- |
| the hostname | a single-machine UI spends no column on a constant | back for `vera chat -url <another mac>` (`statusName`) — the greeting follows the same rule |
| `· none` | a dial nobody turned, on models with no dial | `/model` prints the resolution whole |
| where you are | true of the world, not of this conversation | `/debug`'s opening line |
| the keys | a reminder `/help` already holds | mote drops them itself when the line is full |

## What is still owed, and by whom

These are mote's (`../mote/tui`), not Vera's, and the reference asks
for them:

- **The left of the status line.** The reference has the model and
  nothing else there, with the estimated cost and labelled context
  (`$0.0094 est · ctx 40.8k`) on the right. `Model.statusLine` composes
  `name · model · conversation · state · spent` on the left and will
  not take an empty name, so `vera` and the conversation id stay. An
  `Options.Status` that let the application lay the line out, or an
  Options field to drop either, would finish 3g.
- **The completion popup's own hint line** (`↑↓ choose · ⏎ accept ·
  esc dismiss · ⇧⏎ newline`), the accent border on the focused editor
  against a dim border on the popup, and the selected row as a solid
  block rather than a `▸`. All of 3f's chrome is `renderSuggestions`.
- **Colour inside a notice.** mote paints a notice in one dim style,
  so the glyphs carry the whole distinction. The reference tints the
  gutter — yellow for an ask, red for a failure — which needs a notice
  that can say what kind it is.
- **Where a notice wraps.** `hang` wraps on `" -/"`, so a second line
  too long for the pane breaks *inside* the slash command at its
  slash — `· /` / `answer a3f2 <text>`. The breakpoints are right for
  prose and wrong for the one token on the line that must not be cut.
- **`⏎ open` on a task event.** The reference opens the task from the
  transcript row. Nothing in mote focuses a notice, so the second line
  carries a slash command instead.
