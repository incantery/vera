# vera — quick start

From zero to talking to your Mac from your phone, and driving what's in
front of you.

## What you need

- **Go 1.25+** to run Vera Core.
- **Xcode** to build Vera.app (the Mac's senses) and the phone app.
- A Mac and a phone **on the same network**.
- For a real mind rather than an echo: a key for something to ask.
  Either an OpenAI-compatible one (`$OPENAI_API_KEY`, or
  `~/.config/vera/openai_key`, or `--api-base` at a local server), or
  `$ANTHROPIC_API_KEY` with `--model claude-opus-5`. A `claude-*` name
  and an Anthropic key go to the Messages API; anything else goes to
  `/chat/completions`, and the banner says which. `~/.config/vera/*.env`
  is read at startup, so a key can live in a file rather than a shell.
  Without any of it, run with `--echo` and Vera answers by repeating —
  enough to prove the channel.
- For voice: nothing up front. The first time you dictate, Vera offers to
  install **Parakeet** on the Mac (~600 MB, once). Your voice is
  transcribed there and never leaves your machines.

## 1. Run Vera Core

```
go run github.com/incantery/vera/cmd/verad --echo
```

Core comes up on `127.0.0.1:4780` and mints a pairing secret. Loopback
only by default; it opens to your LAN when the phone pairs. To reach a
real model, drop `--echo`.

## 2. Give the Mac senses

Vera Core is the loop; **Vera.app** is what sees the desk and acts on it
— which app is focused, your voice, and carrying out commands. Build and
launch it:

```
macos/run.sh
```

Grant it **Accessibility** when asked (it needs this to know what's
focused and to press keys in other apps). Without Vera.app running, the
phone sees your terminal panes but not your apps, and can't drive them.

## 3. Pair the phone

Open `http://localhost:4780/` on the Mac — it shows a QR. Build the phone
app (`ios/Vera.xcodeproj`) onto your device, open it, and scan. Pairing
is one loopback GET of the secret; after that the phone is just another
peer on the LAN.

## 4. Talk, and act

The phone's home screen is the places on your Mac, ranked by how much
you've been using them, the one in front of you marked. From here:

- **Tap a terminal pane** → you go into it. The Mac switches to it too.
  You see it live, and you can talk or type into it. Tap the mic, pace,
  talk, tap it off — the words land in the pane's input, waiting for you
  to press **Send**. (Nothing is sent until you do.)
- **A Claude Code pane** also shows **Compact** — its own command, one
  tap.
- **Tap an app** (Xcode, Chrome, …) → it comes to the front and the phone
  shows its buttons: Build / Run / Stop / Test for Xcode, Reload / New
  Tab for Chrome. Tap one; the Mac carries it out.

That's the whole loop: what you look at on the phone and what's on the
Mac stay the same, and the phone can reach across and work the machine.

## Reaching it from the phone

Core and phone find each other on the LAN automatically once paired. If
your network blocks peer discovery, Core prints ready-to-share URLs at
startup; the phone can also be pointed at `http://<your-mac>.local:4780`.

## Teaching Vera a new app

App control is data, not code. Add an entry to `appProfiles` in
`cmd/vera/capability.go` — a bundle id and a few `{title, icon, key,
mods}` shortcuts — rebuild Core, and the app's buttons appear on the
phone. No phone rebuild. That's the Tier-0 path; richer actions (parsing
output, reading state) graduate to a Go provider.

## Trying another model

`/model` in the chat is a card of every model verad can reach, with a
tick on the one answering; `/model claude-opus-5` moves **this
conversation** onto another one, sticks across `vera chat -c`, and is
undone by `/model` with nothing after it. `vera say -m claude-opus-5
"…"` does it for one exchange only. No restart either way.

How hard it thinks is a separate toggle: `/effort` offers low, medium
and high, and `/effort <level>` sets one straight. It moves the effort
and leaves the model alone, and the other way round — so switching
model does not silently restate the effort. A model with no dial says
so: the gpt-5.6 family (`gpt-5.6-luna`, `gpt-5.6-terra`) takes effort
`none` and nothing else, and moving onto one drops the effort rather
than sending one it would refuse.

`vera costs` (or `/costs` in the chat) then says what that was worth:
exchanges, tokens, dollars at list prices, how long until anything
reached the screen, and what the agents you started spent —
`--since 24h`, `--by model|conversation|day`.

## The list

`/todo` in the chat — or `vera todo` in any terminal — is *your* list,
as distinct from `/tasks`, which is the agents Vera has working in
repositories. It is one Markdown file, `TODO.md` in her home beside
`MEMORY.md`, so it opens in any editor and renders anywhere:

```
/todo Call the bank about the mortgage    put it on the list
/todo                                     what is left
/todo done bank                           cross one off — a number or a word from it
/todo undo 2 · /todo drop 2               put one back · take it off entirely
/todo all · /todo clear                   show the crossed off · sweep them
```

Nothing about it needs a model: the words above are parsed by hand, and
they work the same on a laptop with no key. When a reference matches
more than one item — or none — nothing is guessed; the candidates come
up as a card and you pick one.

With a model, the same list is one of Vera's tools, so *"remind me to
book the dentist"* and *"I've done the passport one"* reach the same
file — in the chat, on the phone, or out loud. Editing `TODO.md` by
hand is the third way in, and it is not second class: lines you wrote
yourself count as items, and anything in the file that is not an item
is never rewritten.

The list stays out of `vera dump`, deliberately. It is the most
personal file in her home, and it has never been the reason anything
went wrong.

## Troubleshooting

- **Apps missing from the home list / taps don't switch the Mac.**
  Vera.app isn't running (or lost Accessibility). It's the sensor; start
  it and the apps reappear within a second.
- **A button says "the Mac's app isn't running."** Same cause — the
  keystroke executor lives in Vera.app.
- **Dictation asks to set up Parakeet every time.** The Mac needs `uv`
  first (docs.astral.sh/uv); install it, then "Check again".
- **Stuck "reading the pane".** Core may predate a feature — restart it
  after pulling.
- **Vera did something odd.** `vera dump --note "what happened"` (or
  `/dump what happened` in the chat) writes a folder and a `.tar.gz`
  under `~/.local/state/vera/dumps/`: the conversation with every tool
  round and the prompt the model saw, the fleet tasks it touched with
  their agents' Claude Code sessions and what they cost, what she
  believed about you at the time (`home/MEMORY.md` and the memory
  files), and verad's log for those minutes — secrets redacted. Send
  the tarball.
- **She put the wrong thing on the list.** `vera todo drop <n>`, or
  open `TODO.md` in `vera home` and delete the line. Either is the
  same edit; she re-reads the file on every call.
- **She has a fact about you wrong.** Her memory is files: `vera home`
  prints the directory, `MEMORY.md` lists what she knows, and each
  memory is one Markdown file under `memory/`. Edit one, or delete it —
  a deleted file is a forgotten fact, and she reads the directory again
  on the next exchange.
