# vera

A voice and a pair of hands between your phone and your Mac.

Vera is a persistent assistant that lives on your machine and answers to
your phone. You talk; the Mac listens, thinks, and acts — dictating into
whatever you are working in, showing you what it is looking at, and
driving the apps in front of you. Speech never leaves your machines.

Three pieces, one small contract between them:

- **Vera Core** (`cmd/vera`, Go, `:4780`) — the loop. It pairs with your
  phone over the LAN, holds the conversation, keeps an attention model of
  what is in front of you (which app, which terminal pane), reads panes,
  and exposes a **capability surface** the phone renders and drives.
- **Vera.app** (`macos/`) — the Mac's senses and hands. It reports which
  app is focused, transcribes your voice locally, and carries out
  commands the core hands down (bring an app forward, press a shortcut).
- **The phone** (`ios/`) — the remote. Tap into anything on the Mac, see
  it, talk into it, and press its buttons — Compact a Claude Code
  session, Build in Xcode — from your pocket.

```
go run github.com/incantery/vera/cmd/vera        # Vera Core, on this Mac
```

Then run **Vera.app** (`macos/run.sh`) so the Mac has senses, and pair
the phone by scanning the QR at `http://localhost:4780/`. First ride:
**QUICKSTART.md**.

## What you get

- **Your phone is a microphone for the Mac.** Pace the room and talk;
  the words are transcribed *on the Mac* (Parakeet, a one-time local
  download) and typed into whatever rook is looking at. Type-only by
  default — nothing is sent until you press Send, because words typed
  into an agent can be unsent and words sent cannot.
- **One eye into the terminal.** Tap a pane and watch it live on the
  phone — the agent's reply forming, the comment you are about to
  answer. Read-only; reading the reply is what the Mac is for.
- **Control, not just sight.** The phone asks the Mac *"what can I do
  here?"* and draws whatever it is handed. A Claude Code pane offers
  **Compact**; Xcode offers **Build / Run / Stop / Test**; Chrome offers
  **Reload / New Tab**. Teaching Vera a new app is a descriptor on the
  Mac — the phone never changes. See `cmd/vera/capability.go`.
- **A screenshot is a sentence.** Paste one — ⌘V into the Mac's ask
  panel, Attach on the phone, `/paste` in the chat, `vera say -i` — and
  it rides your message. Vera cannot see it: she keeps it and hands the
  file to whichever agent she gives the work to, and Claude Code opens
  it. "What is wrong with this?" is a whole question again.
- **Attention you can jump to.** The home screen is the places on the
  Mac, ranked by frecency, the one in front of you marked. Tap to go
  there — the Mac follows you, and the phone follows the Mac.

## Speech stays home

Transcription runs on the Mac, not in a cloud. The phone records and
hands the audio across the paired, loopback-or-LAN channel; the Mac
recognises it locally. **Your voice never leaves your machines.**

## Capabilities and rook

rook — the native terminal (a separate project, [rookide.com](https://rookide.com))
— is Vera's first capability *provider*: Vera Core talks to it over
`tmux -L rook` to read panes, type, switch, and run a coding agent's own
commands. It is one provider among many; app control (Xcode, Chrome, …)
rides the same capability contract without rook. Run Core with
`--rook-tmux ""` to disable the terminal provider.

## Flags

```
--addr 127.0.0.1:4780   listen address (":4780" opens it to your LAN)
--model claude-opus-5   which model to ask; a claude-* name with an
                        ANTHROPIC_API_KEY goes to the Messages API, and
                        anything else to an OpenAI-compatible server.
                        Default: the profile's own model line.
--effort high           how hard to think, where the model has the dial
--echo                  answer by repeating — no model key needed
--no-peer               LAN only; skip the phone-to-Mac peer sidecar
--rook-tmux rook        rook's tmux server name ("" to disable)
--state <file>          identity file (default ~/.local/state/vera/identity.json)
```

## The pieces in this repo

```
cmd/vera/     Vera Core — the loop, attention, capabilities, rook adapter
macos/        Vera.app — senses (app focus, STT) and hands (AppDriver)
ios/          the phone app (SwiftUI)
cmd/ladder/   a drive-loop benchmark corpus (research tooling)
drive/ route/ shared libraries
```

Every piece speaks the same small schemas; use as much or as little as
you like.
