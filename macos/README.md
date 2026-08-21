# Vera for macOS

The Mac's senses and face. Native Swift — SwiftUI for the windows, AppKit
where macOS needs to be spoken to directly — and no intelligence of its
own: everything it hears goes to Vera Core (`cmd/vera`) as text, and
everything it learns about where your attention is goes there as an
observation.

```
go run ./cmd/vera --echo --no-peer     # Vera Core, in another terminal
./macos/run.sh                          # build + launch Vera.app
```

or `open macos/Vera.xcodeproj` and ⌘R.

## What it does today

- **Menu bar item** and a normal window with Overview · Settings ·
  Connections · Health. Closing the window leaves Vera in the menu bar.
- **Pairs itself** with Vera Core over loopback by reading the
  loopback-only `/pair.json`, and polls `/status` every five seconds —
  that poll is also the device heartbeat.
- **Knows what has focus.** `NSWorkspace` activation notifications become
  `app.focused` / `app.unfocused` observations: name, bundle id,
  timestamp. Nothing inside the window is read; an app without an
  integration is opaque, and the model is told so.
- **Hold ⌃⌥Space to talk.** Carbon `RegisterEventHotKey` — no
  Accessibility permission, and it reports the release, which is what
  makes hold-to-talk work. A tap under 300ms latches listening on until
  the next press. The binding is in Settings.
- **Hears you on this Mac.** `AVAudioEngine` → `SFSpeechRecognizer`,
  on-device when macOS offers it, the same arrangement as the phone.
  Only the words leave the process, as `POST /say` with
  `device` set so the answer can lean on this Mac's attention context.
- **A floating surface** — a non-activating `NSPanel` at the top or
  bottom of the screen the mouse is on — shows listening / thinking /
  the streamed answer / an error, then fades.

## Files

```
VeraApp.swift     @main, menu bar, app delegate
Station.swift     the one state object: hotkey → overlay → listener → core
Core.swift        pairing, /status poll, /observe, /say stream
Events.swift      ContextEvent (the wire shape) and the debug log
Focus.swift       NSWorkspace activation → FocusedApp
Hotkey.swift      Carbon hot key, press + release
Listener.swift    microphone + speech, behind `Transcriber`
Overlay.swift     the floating panel and its SwiftUI face
MainWindow.swift  Overview · Settings · Connections · Health
Settings.swift    five settings, UserDefaults
```

## Permissions

Microphone and Speech Recognition are asked for on the first press, not
at launch. They are remembered per signed bundle; the project signs with
the same team as the iOS app so a rebuild does not re-ask. Nothing needs
Accessibility.

## Debugging

Health shows the hotkey, both permissions, whether recognition is
on-device, the current focus, and a rolling log of every event sent and
every frame that came back. The same things can be asked of the core
from a terminal:

```
SECRET=$(curl -s localhost:4780/pair.json | jq -r .secret)
curl -s -H "Authorization: Bearer $SECRET" localhost:4780/status | jq .devices
```

If the app says connected and a press does nothing, it is the app; if
the curl above has no devices, it is the transport.
