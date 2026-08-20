# Vera on the Mac — the first native surface

Status: written 2026-08-20 against `4e72775`; the milestone in §5 is
implemented in the same change (see `macos/README.md`). Kept as the
record of what was decided and why.

## 1. What is already here (inspection)

- **Vera Core is `cmd/vera2`.** One Go process, `:4780`. `POST /say` takes
  `{text, conversation}` and streams ndjson `Frame`s (`run`, `status`,
  `delta`, `done`, `error`); `GET /resume?run=&from=` rejoins a run;
  `GET /ping` needs no secret and answers `{peer, name}`; `GET /pair.json`
  is **loopback-only** and hands out `{peer, secret, name, hints}`. Identity
  lives in `~/.local/state/vera2/identity.json`. The mind is one streamed
  OpenAI-compatible call with history, memory, and a `delegate` tool that
  runs `claude -p`.
- **Transcription is already on-device and already text-on-the-wire.**
  `ios/Vera2/Speech.swift` runs `SFSpeechRecognizer` + `AVAudioEngine` on
  the phone and POSTs the words. There is no audio upload path and no
  server-side STT, and that is a deliberate choice (README: "does not post
  what you said to a third party on the way to your own Mac"). The Mac app
  does the same thing.
- **Native Swift already lives in this repo**: `ios/Vera2.xcodeproj` is a
  hand-written 246-line pbxproj using a `PBXFileSystemSynchronizedRootGroup`
  (every `.swift` under the folder is in the target, no per-file entries),
  plus `cmd/vera2/peer/sidecar.swift`, compiled by `swiftc` at first run.
- **There is no context/observation path.** Nothing in vera2 knows which
  app has focus, or that a Mac exists as a device distinct from a phone.
- **Rook**: `../rook` has an attention-feed contract (`cmd/vera/attention.go`
  writes a jsonl file rook reads) and a *design-only* plugin vocabulary
  (`docs/plugins/VOCABULARY.md`). There is no capability-discovery RPC to
  reuse. `rook` is on `$PATH`. No Rook change is necessary for this
  milestone, and none is made.
- Toolchain on this machine: Xcode 26.2, Swift 6.2, macOS 26.5, two
  Apple Development identities.

## 2. Shape

```
macos/Vera.app (Swift)                 cmd/vera2 (Go, "Vera Core")
┌────────────────────────────┐         ┌──────────────────────────────┐
│ Hotkey   (Carbon hot key)  │         │ lan.go   POST /say (exists)  │
│ Listener (AVAudioEngine +  │  text   │          GET  /status   NEW  │
│           SFSpeech, local) ├────────►│          POST /observe  NEW  │
│ Focus    (NSWorkspace)     │ events  │ attention.go             NEW │
│ Overlay  (NSPanel+SwiftUI) │◄────────┤   current focus, recent      │
│ Core     (URLSession)      │ frames  │   observations, integrations │
│ Station  (app state)       │         │ mind.go  preface += attention│
│ Menu bar · Main window     │         │ providers.go rook detect NEW │
└────────────────────────────┘         └──────────────────────────────┘
           loopback HTTP :4780, bearer secret from /pair.json
```

Vera.app is the Mac's senses and face. It holds no model, no memory, no
routing. Everything it learns it reports as a semantic event; everything it
says it gets back as frames.

### IPC

Loopback HTTP to the existing LAN transport. No new stack: the app is one
more peer, and the only thing special about it is that it sits on the
machine, which is exactly what the loopback-only `/pair.json` exists for —
it fetches the secret from there on first launch and keeps it in
`UserDefaults` (it is a local-only bearer token the CLI already prints to a
web page; the Keychain can come with a real trust ceremony).

### Context events (transport-independent, JSON)

```json
{ "type": "app.focused", "device": "seths-mbp", "at": "2026-08-20T11:54:19Z",
  "app": { "name": "Ghostty", "bundle_id": "com.mitchellh.ghostty" } }
```

Types emitted today: `device.connected`, `device.disconnected` (by the
server, on a lost heartbeat), `app.focused`, `app.unfocused`,
`voice.capture.started`, `voice.capture.stopped`,
`voice.transcription.completed`, `surface.presented`, `surface.dismissed`.
The Go side stores whatever `type` arrives; unknown types are kept, not
rejected, which is how `editor.*` from Neovim lands next without a server
change. The editor events carry `editor`, `file`, `selection` etc. in the
same envelope.

What the model is told is deliberately only what is known: "Ghostty has
had focus for 2m (before: Chrome, Ghostty)". Never a guess about content.

## 3. Files

### Add — `macos/`
```
macos/Vera.xcodeproj/project.pbxproj   mirrors ios/Vera2.xcodeproj, macOS SDK
macos/Vera-Info.plist                  LSUIElement=NO, mic + speech strings
macos/Vera/VeraApp.swift               @main: MenuBarExtra + Window + delegate
macos/Vera/Station.swift               the app's one @Observable state object
macos/Vera/Core.swift                  /pair.json, /ping, /status, /say, /observe
macos/Vera/Events.swift                ContextEvent + the rolling event log
macos/Vera/Focus.swift                 NSWorkspace activation → app.focused
macos/Vera/Hotkey.swift                Carbon RegisterEventHotKey, press+release
macos/Vera/Listener.swift              AVAudioEngine + SFSpeechRecognizer (macOS)
macos/Vera/Overlay.swift               NSPanel host + SwiftUI surface + states
macos/Vera/MainWindow.swift            Overview · Settings · Connections · Health
macos/Vera/Settings.swift              hotkey, overlay placement, launch behaviour
macos/README.md                        how to build/run, permissions, the loop
macos/run.sh                           build + launch
```
Build: `xcodebuild -project macos/Vera.xcodeproj -scheme Vera -configuration Debug build`
or ⌘R in Xcode. Dev signing with the existing team so TCC grants survive
rebuilds.

### Add — `cmd/vera2/`
```
attention.go        Attention: per-device focus + bounded recent observations
attention_test.go
providers.go        Provider list: rook (LookPath), integrations seen via events
```
### Modify — `cmd/vera2/`
- `lan.go`  — `POST /observe`, `GET /status` (authed); `/ping` gains `version`.
- `transport.go` — `Message.Device` (optional), `Message.Source` (optional).
- `mind.go` — preface gains an attention paragraph when something is fresh.
- `main.go` — wire `Attention` into the transport and the mind.
- `README.md` — a section on the Mac app and observations.

## 4. macOS strategy per concern

| concern | choice | why |
|---|---|---|
| global hotkey | Carbon `RegisterEventHotKey` + `InstallEventHandler` for `kEventHotKeyPressed`/`Released` | works without Accessibility permission, fires on release (hold-to-talk), still fully supported on macOS 26 |
| interaction | **hold** to talk, release to send; a tap under 300ms **latches** until the next press | hold is preferred; the latch costs ten lines and saves a thumb on long sentences |
| default binding | ⌃⌥Space | ⌘Space/⌥Space are Spotlight/Alfred/Raycast territory |
| microphone | `AVAudioEngine` input tap (no `AVAudioSession` on macOS) | what the phone does |
| transcription | `SFSpeechRecognizer`, `requiresOnDeviceRecognition` when supported, behind a `Transcriber` protocol | the existing path; the protocol is the narrow provider boundary the brief asked for |
| floating surface | `NSPanel` `.nonactivatingPanel + .borderless`, `.floating` level, `canJoinAllSpaces + fullScreenAuxiliary`, SwiftUI content via `NSHostingView`, fade in/out by animating `alphaValue` | never takes key focus, visible over full-screen apps |
| placement | top-centre of the screen that has the mouse (settings: top / bottom) | near attention, off the working area |
| focus | `NSWorkspace.shared.notificationCenter` `didActivateApplicationNotification` | no Accessibility, no scraping |
| menu bar | SwiftUI `MenuBarExtra` | enough for a status dot, open, quit |
| main window | SwiftUI `Window` + `NavigationSplitView` | four surfaces, read-mostly |
| settings | `@AppStorage` | five keys, no speculative screens |
| lifecycle | `applicationShouldTerminateAfterLastWindowClosed = false` | closing the window leaves the menu bar item |

## 5. Sequence

1. vera2: `attention.go`, `/observe`, `/status`, preface — with tests.
2. Xcode project + app skeleton: menu bar, window, four views with stub data.
3. `Core.swift`: pair, ping loop, status → Overview/Connections/Health live.
4. `Focus.swift` → events in Health, posted to `/observe`.
5. `Hotkey.swift` → Health shows "registered", a press logs an event.
6. `Listener.swift` → words on screen.
7. `Overlay.swift` → listening / processing / result / error.
8. Wire: hotkey → overlay → listener → `/say` → frames → overlay → dismiss.
9. README, polish, run it for real.

## 6. Validation

- Go: `go test ./cmd/vera2/` (attention window, status shape, observe auth,
  preface text).
- Swift: `xcodebuild` clean build; manual loop against `go run ./cmd/vera2
  --echo --no-peer` (no key needed) and then against the real mind.
- Curl the same endpoints the app uses, so a silent app can be split into
  "transport" vs "app" in one command.

## 7. Tradeoffs and permission constraints

- **TCC**: mic and speech prompts appear once per signed bundle. Ad-hoc
  signing re-prompts on every rebuild; development signing with the team in
  the iOS project does not. The project uses the team.
- **Speech on macOS** requires `NSSpeechRecognitionUsageDescription`; on-device
  recognition may be unavailable for a locale, in which case Apple's server
  is used — the Health view says which.
- **Hold-to-talk and Carbon**: the release event is reliable as long as the
  hot key is registered; if the user changes the binding to a combination
  another app owns, registration fails and Health says so.
- **The secret in UserDefaults** — same threat model as the pairing web page.
- **Rook** is "installed / not installed" only. The provider list is the
  seam; it stays empty of capabilities until rook can be asked.
- **Device presence** is a heartbeat (`/ping` every 5s from the app, and
  the server marks a device stale after 30s). Presence *modelling* is later.
