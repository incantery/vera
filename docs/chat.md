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
gpt-5.6-luna                                        $0.0094 est · ctx 40.8k
```

The model on the left, with the state beside it when there is one
(waiting for you · choosing · ◐ working); the cost on the right,
labelled the estimate it is, and the context the next turn starts
from rather than a total that only grows. mote hands over everything
it knows (`tui.Status`) and `statusLine` in `grammar.go` decides what
goes back; mote fits it and puts its key hints in front of the right
when there is room.

Struck off it, and where each went:

| dropped | why | where it is now |
| --- | --- | --- |
| the hostname | a single-machine UI spends no column on a constant | back for `vera chat -url <another mac>` (`remoteName`) — the greeting follows the same rule |
| `vera` | the tab says it | the tab |
| the conversation id | an argument, not a name | `/status`, `/sessions` |
| `· none` | a dial nobody turned, on models with no dial | `/model` prints the resolution whole |
| where you are | true of the world, not of this conversation | `/debug`'s opening line |
| the keys | a reminder `/help` already holds | mote drops them itself when the line is full |

## What mote gives, and where the seam is

The reference asked for five things that were the terminal's rather
than Vera's. All five landed in `../mote/tui` (mote `eae0a25`,
`f38c1b7`), and this is the seam each one crosses:

- **The status line's layout** — `Options.Status`, above.
- **The completion popup** — a box of its own in the dim border, its
  keys on its last line (`↑↓ choose · ⏎ accept · esc dismiss · ⇧⏎
  newline`), the chosen command a reversed block. The editor under it
  is a box too, in the accent while the keyboard is in it and dim
  while it is not — under a question, a picker, or while a card or a
  notice has the focus. Nothing of Vera's: mote draws all of it.
- **Colour inside a notice** — `agent.Event.Tone`. `tone()` in
  `grammar.go` follows the shape: `◇` is `ToneNeeds`, `×` is
  `ToneFailed`, everything else is dim. mote tints the gutter and
  only the gutter; the words stay dim, and the line still reads with
  the colour off.
- **Where a notice wraps** — mote's `hang` no longer breaks a line at
  the slash of a slash command. A path in prose still wraps where it
  always did.
- **`⏎ open` on a task event** — `agent.Event.Open`. `opens()` in
  `grammar.go` is `/report <id>` when the task has written one and
  nothing otherwise: it is only ever a read. A notice carrying one is
  a stop for tab, wears the accent bar when it has the focus, and
  enter over an empty box runs the command. With something typed,
  enter sends that. The second line of the notice still names the
  command, for anybody who would rather type it.
