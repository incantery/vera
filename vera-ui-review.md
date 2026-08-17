# Vera review — web surfaces and server rails

Scope: all of `cmd/vera/web/src` read in full (Vera home, Board, explorer, schedule, agent membrane + DriveView cockpit, artifact shelf, login, layout, `state.svelte.js`, `app.css`) plus an audit of the Go rails (`main.go`, `rpc.go`, `tasks.go`, `schedule.go`, `steward.go`, `engine.go`, `driver.go`, `recover.go`, `key.go`, `upload.go`, `workspaces.go`, `report.go`).

## 1. Navigation & information architecture

- **[High · Home+Board · Vera.svelte:160]** Idle task-less sessions vanish everywhere. Home skips them (`if (s.task || s.state === 'idle') continue;`) and the Board renders only `tasks` — the frame's `sessions` are never displayed. When a task-less conversation goes idle its row disappears, it isn't in Done, and the only doors left are the explorer's green dot (Explorer.svelte:214) or a remembered URL. **Fix:** list idle task-less sessions under Done or a collapsed "Quiet sessions" group.
- **[Med · Board · Board.svelte:674-770]** Inbox cards without a proposal can't be started from the board: start controls exist only inside `{#if sel.proposal}`. Home's inbox state has the full Start UI (Vera.svelte:1311-1326). **Fix:** add a start affordance or an explicit "decide on home →" link.
- **[Low · Board · board/+page.svelte:53]** "explore" routes `goto('/?explore=1')` → home → redirect effect → `/explore`. Use `goto('/explore')`.
- **[Low · All · state.svelte.js:56-57]** 401 redirect keeps only `location.pathname` — `/agent/x?mode=direct&review=1` returns as `/agent/x`, silently flipping direct mode to membrane. Encode `pathname + search`.
- **[Low]** Cross-links are asymmetric: home's footer links board/explorer/schedule; the other surfaces only link home.

## 2. Card / thread states

Server vocabulary (verified): session states `"needs you" | "blocked?" | "working" | "idle"` (transcript.go:38-41 — space and `?` are wire values); task columns `inbox | progress | waiting | done | dropped`; free-text `State`/`Face` lines. The UI maps these correctly (Vera.svelte:105-114, Board.svelte:72-77).

- **[High · Home+Board · state.svelte.js:253-295, tasks.go:94,138]** The WatchBoard stream drops `proposalText` and `autoStart`. REST carries both (`ProposalText string \`json:"proposalText,omitempty"\``; `AutoStart string \`json:"autoStart,omitempty"\``) but the proto has neither and `boardFrame()` maps neither — and the stream is the primary rail. Consequences: the Board shows **"Send Vera's reply"** (Board.svelte:779, gated on `proposalKind` alone) while the draft blockquote (Board.svelte:695, gated on `sel.proposalText`) never renders — directly under `<!-- the drafted reply, verbatim — nothing is sent unseen -->`; home's agenda never emits its `reply` and `autoStart` items (Vera.svelte:184-191), so the "I'm about to start … it runs unless you hold it" veto card never appears; the ask pane still says "Needs your answer — Vera drafted one" (:119) and "your words outrank the draft" (:1264) with the draft invisible. **Fix:** add `proposal_text` and `auto_start` to the proto, map in `boardFrame()`, gate the send button on the draft being present.
- **[Med · Server · rpc.go:339-363]** WatchBoard's change-hash omits rendered fields: `Repos` (bookmark/scratch creation never ships a frame — repo pickers at Vera.svelte:810 / Board.svelte:714 go stale), session `LastText`/`CtxPct`/`Branch`/`Driving`, task `Goal`/`Runs`/`CostUsd`. Home quotes `sel.session?.lastText` (Vera.svelte:1305) from exactly this stale field. **Fix:** hash everything the frame carries.
- **[Med · Server · tasks.go:487-509]** "Done is never automatic" has an exception: an adopted card whose session goes idle jumps straight to `done` (`"done · session went quiet"`), while judge-done always parks `waiting` for acceptance; home then shows "Done"/`'Closed.'` (Vera.svelte:625-630) for unaccepted work. **Fix:** route through the acceptance gate or reword.
- **[Med · Home · Vera.svelte:363-370]** Review-pane diff staleness: `loadDiff()` only runs when `!diffData`, reset only on selection change; the tab's live file count can disagree with the rendered diff. **Fix:** refetch when `treeFiles` changes.
- **[Low · Server · rpc.go:58-62, main.go:746-748]** External tree changes never push a WatchAgent frame by themselves; a failed digest is cached per process and never retried.
- **[Low · Board · Board.svelte:612]** `{sel.state}` prints raw free-text state lines in a pill. Related hazard: `renderReport` counts events by matching its own log sentences (report.go:123-135) — copy edits silently zero report counts.

## 3. Mobile ≤720px

Strong overall: one-pane home (`.vshell.m-main`, Vera.svelte:1574-1692), docked "What should we work on?" pill, thumb-height rows, board slide-over pane, DriveView's stacked review + composer-adjacent suggestions with send/edit reversed for thumbs (DriveView.svelte:891-893), `100dvh`, iOS 16px anti-zoom rule (app.css:197-207), `pointer: coarse` heights (Explorer.svelte:288-296).

- **[Med · Cross-surface]** Three phone breakpoints: home/cockpit 720px, Board 840px (Board.svelte:1009), artifact shelf `sm` ≈640px (ArtifactPane.svelte:111). 721–840px yields desktop home + phone board. **Fix:** one shared breakpoint.
- **[Med · Home · Vera.svelte:1437-1444, 1089-1094]** The composer and door field are single-line `<input>`s — no wrap, no Shift+Enter — while every other composer is a textarea. **Fix:** auto-growing textareas.
- **[Low · Cockpit · DriveView.svelte:622,1218]** Keyboard copy leaks onto touch: ctx button always ends "· ⌘I"; review tooltip "close (esc)". Extend `.dv-desk`.
- **[Low · Home]** If the selected task disappears while `mobileMain` is set, the phone shows the door, not the list.

## 4. Copy

The voice is consistent and often excellent — "destroy all uncommitted work — sure?" (DriveView.svelte:1302), "everything in it goes — sure?" (Board.svelte:659), "it stops returning — sure?" (schedule:216); spend disclosure is honest ("claude turns at API rates — a subscription already covers them"; "vera's own call · one per turn · counted under spend").

- **[Med · Home · Vera.svelte:978]** "Vera is available" (status) vs "vera is not answering — is the binary still running?" (same sidebar's footer). Pick one case per surface.
- **[Med · Home+Board · Vera.svelte:437-440, Board.svelte:144]** Informational downgrades wear the error costume: `'captured to the inbox without a plan'` lands in the red `role="alert"` box though the capture succeeded. **Fix:** neutral notice style.
- **[Low · Explorer.svelte:226]** "this is ground, not a hallway" is action-free; say "type the first message below".
- **[Low · DriveView.svelte:394]** Permission label "ask never · refused" won't parse; try "refused this session".
- **[Low · Vera.svelte:524-544]** "Continue from this result…" over-promises — `launch(text)` POSTs only the typed text; no thread context rides along. Thread the task id or soften copy.
- **[Low · Server]** Raw errors like `"that agent is gone from the window"` (main.go:605) hit the cockpit's `lost` banner with no next step.

## 5. Flows & failure handling

The dual-rail design (stream first, REST poll as bridge, identical shapes, 5s reconnect) is well executed on all three streaming pages; the plan-flow token guard (Vera.svelte:424-426) is exactly right; the 409 planning-off fallback never dead-ends; server reconcile/recover (90s grace, 2 retries, backoff, guards re-checked) is sound except:

- **[High · Server → all surfaces · drive/headless.go:129-133, tasks.go:1127, recover.go:87-199]** Recover resurrects a deliberately stopped drive. `/api/drive/stop` cancels the context → subprocess dies as `"signal: killed"` → classified transient ("the OS (memory pressure) or a human took the process") → `StopTransient: true` → recover restarts within ~30s: `"in progress · auto-recovering (retry 1 of 2)"`. The UI's Stop is not final. **Fix:** classify `context.Canceled` as non-transient before the signal heuristic.
- **[High · Server · tasks.go:205,277,308,454]** `taskStore` has no lock; `nextID()` is list-max+1; `syncBoard` (a write) runs on every `GET /api/tasks` and every WatchBoard recompute per client — duplicate `T-<n>` ids and lost updates under concurrency. **Fix:** mutex; single writer for adoption.
- **[Med · Home vs Cockpit · Vera.svelte:1179,1363 vs DriveView.svelte:1309,1316]** Two commit contracts: home commits on Enter with empty message allowed (defaults to thread name); DriveView requires a message and disables the button. **Fix:** align on the stricter.
- **[Med · Cockpit · DriveView.svelte:501-513,584]** Esc while typing interrupts the turn (Escape branch precedes the typing guard; textarea handler repeats it). **Fix:** require empty composer or double-Esc.
- **[Med · Explorer.svelte:77-79]** Escape closes the panel and discards a typed first-message draft. Guard on empty draft.
- **[Med · Home · Vera.svelte:307-309,1374]** `fetchAgent(...).catch(() => {})` — if both rails fail, the Conversation tab claims "Nothing yet — anything you type below lands here" over real history. Show "couldn't load — retrying".
- **[Med · Server · main.go:996-1013,1134]** Asymmetric cancel verbs: interrupt only kills says (a driven agent gets `"nothing in flight to interrupt"`); drive/stop only kills drives.
- **[Low · Schedule rails]** (a) a paused recurring entry resumed keeps its clock — waits a full interval from the pause stamp ("resume did nothing"); (b) with no LLM key a due entry becomes a card that never starts, marked only by log line `"no vera-agent key — captured, not started"` (schedule.go:224-228) — the page's `lastFired` shows the task id, not the stall; (c) `every: 24h` anchors to `LastRun + d`, so dailies drift later each firing while the form implies clockwork. Surface (b) on the row; document (a),(c).
- **[Low · main.go:493-497]** `wireSession.Recovered` ("so the UI can say 'brought back'") is never set — dead affordance.
- **[Low · main.go:855-920]** Successful say jobs are deleted with no terminal state; a poll between deletion and refresh shows neither bubble nor message.

## 6. Affordances

- **[Med · Home · Vera.svelte:816-824 vs 1316-1323]** Inbox pane has duplicate, conflicting start controls: pick "can edit & test" in the dropdown, tap "Start read-only · Vera's pick", get read-only. **Fix:** drop the select in the inbox state.
- **[Med · Home · Vera.svelte:349,293]** No discard path on home, plus dead code for one: `confirmDiscard` declared and reset, never rendered. Wire the two-step discard or delete the state.
- **[Med · Cockpit · DriveView.svelte:1032-1043]** Desktop suggestions send on single click ("SUGGESTED REPLIES · CLICK SENDS"); mobile already does tap→composer, explicit send. Make desktop match.
- **[Low · DriveView.svelte:130]** `suggOpen = $state(true)` — one billed LLM call per finished turn by default. Default collapsed.
- **[Low · Vera.svelte:1203,1359]** Review file list clickable before the diff loads; nothing says "loading".
- **[Low · schedule:117-120]** "fired … → T-12" is inert text, not a link to the card.
- **[Low · ArtifactPane.svelte:114-122]** ← discards unsaved edits with no dirty-check (delete correctly two-steps). Confirm when `dirty`.
- **[Low · Board.svelte:349-357]** Enter in the capture field triggers the billed `submitPlan()`; the cheap "capture" is a separate button.

## 7. Accessibility

Strengths: `role="alert"` on errors; `role="button"`+`tabindex`+keydown on clickable divs; correct `aria-pressed`/`aria-expanded`; visible `:focus-visible` in both systems (Vera.svelte:1530-1536, app.css:137-140); thorough `prefers-reduced-motion` incl. Tailwind `animate-*` (app.css:209-214).

- **[Med]** Micro-copy contrast fails AA: `rgba(146,153,170,0.5)` on `#0B0D12` at 10–11px ≈ 2.5:1 (Vera.svelte:1232,1409); `--color-neutral-700` (#595d6c on #161826) ≈ 2.6:1 on board log timestamps and "… and N more tool calls". **Fix:** floor at ~neutral-500 / alpha ≥ 0.7.
- **[Med · DriveView.svelte:494-545]** Single-key global shortcuts (`r s i j k \`) with no off switch (WCAG 2.1.4); the typing guard misses `<select>` — `r` with the permission select focused opens review. Guard `input,textarea,select,[contenteditable]`; add a disable.
- **[Low · Vera.svelte:1120-1127]** Half-ARIA tabs: no `aria-controls`/ids, no tabpanels, no arrow keys. Finish or use plain buttons.
- **[Low · Vera.svelte:686]** Presence glyph SVG lacks `aria-hidden="true"`.
- **[Low · DriveView.svelte:1197,1329-1335]** Overlays don't trap focus; the keyboard dialog backdrop is `role="button" tabindex="-1"`. Use `role="dialog" aria-modal="true"` + trap.
- **[Low · DriveView.svelte:782,1240]** `direction: rtl` leading-ellipsis garbles bidi path punctuation and screen-reader order; prefer JS middle-truncation.

## 8. Membrane / nocturne consistency

The three-voice split (home's #0B0D12 violet/amber + Source Sans 3; nocturne `nk` tokens #161826 + Inter on board/explorer/schedule/login; zinc Tailwind membrane) is documented intentional (app.css:11-13); all fonts actually load (app.html).

- **[Med · Vera.svelte:33-35 vs app.css:52-54]** Home breaks the one declared-global rule: app.css says the `ev-*` palette exists "so every surface names danger and success the same way", but home hardcodes `MINT = '#6EDFC3'` / `RED = '#F49B9B'` for diffs/errors. **Fix:** use `var(--ev-add)` / `var(--ev-del)`.
- **[Med · agent/[id]/+page.svelte:250-255 vs 590-592]** The membrane page disagrees with itself on "needs you": header rose (`'needs you': 'text-rose-400'`), presence footer sky ("waiting on you"). Reserve rose for `blocked?`/failures.
- **[Low]** Duplicated diff renderers drift: home's `diffRows` (Vera.svelte:372-393) computes real line numbers; DriveView's `reviewLines` (115-124) doesn't; colors differ. Share one component.
- **[Low]** "working" is amber (membrane), violet (home), accent (nocturne) — defensible per-surface, but the home→cockpit hop re-teaches the palette.

## 9. Server rails

**API/auth.** No SSE; Connect server-streams `WatchAgent` (rpc.go:36-86, snapshot then deltas) and `WatchBoard` (rpc.go:276-300, whole frames), fed by fsnotify pokes + 5s ticker. REST errors `{"error": "<msg>"}`; Connect keeps the server's words (rpc.go:90-101).

- **[High · key.go, main.go:242-243,445]** Loopback = zero auth, no Origin/Host check, JSON decoded regardless of Content-Type: any web page can POST `/api/agent/<id>/say` with `"perm":"all"` → `bypassPermissions` → tool execution as the user (no preflight needed). **Fix:** Origin/`Sec-Fetch-Site` checks + non-simple Content-Type on mutations, even on loopback.
- **[High · key.go:84-89, main.go:257, state.svelte.js:219-221]** LAN key rides `?key=` in the startup URL and every image URL (`uploadUrl`) — history/proxy logs; `==` compare is non-constant-time; client stashes the key in `localStorage` (XSS-readable). **Fix:** short-lived cookie for `<img>`; constant-time compare.

**Lifecycle.** Say-job statuses `"phrasing" | "thinking" | "failed"`; success deletes the job (no "done"). Task stop reasons `"error" | "escalated" | "circling" | "spend-cap" | "turns" | "done"` (tasks.go:110). Every run landing parks `waiting`; only human accept closes (tasks.go:1144-1149) — except the quiet-session auto-done (§2). In-memory `s.runs` are never pruned in-process (low).

**Failure machinery.** 10m turn deadline (30m budgeted autopilot); reconcile folds orphaned progress → waiting after 90s with `"the run vanished — vera restarted while it was in flight"`; recover retries transient stops twice with backoff, guards re-checked; retries reset on human touch. Defects: the stop-resurrection bug (§5) and **[Med]** an agent aging out of the 48h window aborts an open WatchAgent stream with NotFound `"that agent is gone from the window"` — the UI shows `lost` and retries forever; stream error deserves a terminal state.

**Schedule.** Not cron: `At` (RFC3339) + optional `Every` (Go duration ≥1m); recurring anchors to `LastRun + d` (drift); missed intervals coalesce (good); missing workspace at fire time pauses with `PausedWhy: "workspace missing at fire time: <path>"`, surfaced with resume on the schedule page (schedule:218-229) — good loop closure, subject to the resume-phase caveat (§5).

---

## ✅ The three highest-impact changes

1. **Complete the stream.** Add `proposal_text` + `auto_start` to the WatchBoard proto, map them in `boardFrame()` (state.svelte.js:253-295), and hash every rendered field in rpc.go:339-363. This stops the primary rail from offering "Send Vera's reply" without showing the draft (breaking "nothing is sent unseen"), restores home's auto-start veto card, and un-stales the repo pickers and session previews.
2. **Gate loopback mutations.** Origin/`Sec-Fetch-Site` + non-simple Content-Type checks in key.go/main.go, and move the LAN key out of URLs. Today any web page the user visits can execute tools as them via a no-preflight POST with `perm: "all"`.
3. **Make Stop final.** In drive/headless.go, classify owner cancellation (`context.Canceled`) as non-transient before the `"signal: killed"` heuristic so recover.go never restarts a deliberately stopped drive. Autonomy that un-stops itself within 30 seconds breaks the trust contract every surface is built on.

Runners-up: a home door for idle task-less sessions; one commit contract (message required) on both review surfaces; AA contrast floor for micro-copy.
