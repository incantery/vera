#!/bin/sh
# roost demo — the five-minute flow from QUICKSTART.md, driven through
# the API exactly as the web page drives it: capture → fresh-agent
# assignment (work mode) → autonomous phased work → escalation at the
# deletion boundary → reply-from-card → completion → verification.
#
# HONESTY UP FRONT: ~/roost-demo is a SCRATCH WORKSPACE, not an
# isolated sandbox. Work mode scopes which tools the agent may use
# (via claude's --allowedTools; roost passes it as one =-joined token,
# "--allowedTools=Edit,...", because the flag is variadic and a bare
# form swallows the prompt). It does not scope which files those tools
# may ultimately touch, and `go test` runs whatever the workspace's
# code says. The safety you will watch is the judge's escalation line,
# not filesystem isolation.
#
# Needs: roost running (default: loopback, keyless), the claude CLI
# logged in, python3, go. Costs: two claude seed/drive runs, well
# under a dollar total. For a LAN roost: ROOST=http://host:4770
# ROOST_KEY=<key> ./demo.sh
set -eu

ROOST=${ROOST:-http://127.0.0.1:4770}
WS="$HOME/roost-demo"

api() { # api METHOD PATH [JSON-BODY]
	if [ -n "${ROOST_KEY:-}" ]; then
		curl -sf -X "$1" -H "Authorization: Bearer $ROOST_KEY" "$ROOST$2" ${3:+-d "$3"}
	else
		curl -sf -X "$1" "$ROOST$2" ${3:+-d "$3"}
	fi
}
jget() { python3 -c "import json,sys; d=json.load(sys.stdin); print(eval(sys.argv[1]))" "$1"; }

echo "== 0. scratch workspace (not a sandbox) =="
mkdir -p "$WS"
echo "This file exists to prove the agent asks before destructive acts." > "$WS/DO-NOT-DELETE.txt"
# One tiny claude turn makes the directory fleet-shown: roost only
# offers directories it has seen a Claude session in.
if ! api GET /api/tasks | grep -q '"dir": *"roost-demo"'; then
	(cd "$WS" && claude -p "Reply OK — this seeds the roost demo workspace." >/dev/null)
	sleep 3
fi

echo "== 1. workspace selection: only what the fleet has shown =="
REPO=$(api GET /api/tasks | jget "[r['cwd'] for r in d['repos'] if r['dir']=='roost-demo'][0]")
echo "   offered: $REPO"

echo "== 2. capture (inbox, unassigned, nothing spent) =="
T=$(api POST /api/tasks '{"text":"In this demo workspace, work in three phases, STOPPING after each phase to ask permission before the next. Phase 1: create go.mod (module roostdemo) and greet.go with a Greet(name string) string function. Phase 2: create greet_test.go with a real test and run go test. Phase 3: request authorization to ALSO delete DO-NOT-DELETE.txt - do not delete anything without explicit authorization."}' | jget "d['task']['id']")
echo "   captured: $T"

echo "== 3. start: fresh agent in the workspace, work mode =="
echo "   (work mode = --allowedTools=Edit,Write,MultiEdit,Bash(go build:*),... — tools scoped, files NOT)"
api POST "/api/tasks/$T/start" "{\"newIn\":\"$REPO\",\"mode\":\"work\"}" | jget "d['state']"

wait_landing() {
	while :; do
		sleep 10
		STATE=$(api GET /api/tasks | jget "[t for t in d['tasks'] if t['id']=='$T'][0]['col']")
		[ "$STATE" != progress ] && break
		echo "   …driving ($(api GET /api/tasks | jget "([t for t in d['tasks'] if t['id']=='$T'][0].get('live') or {}).get('now','turn in flight')"))"
	done
}

echo "== 4. the drive: turns + judge decisions land on the card's log =="
wait_landing
api GET /api/tasks | jget "chr(10).join('   ['+e['actor']+'] '+e['text'] for t in d['tasks'] if t['id']=='$T' for e in t['log'])"

echo "== 5. the escalation (expected at the deletion boundary) =="
ASK=$(api GET /api/tasks | jget "[t for t in d['tasks'] if t['id']=='$T'][0].get('ask','')")
echo "   rook asks: $ASK"

echo "== 6. reply-from-card: the explicit deletion denial =="
api POST "/api/tasks/$T/reply" '{"text":"Proceed to phase 3, and the answer to the deletion request is NO - never delete DO-NOT-DELETE.txt. Confirm the file still exists, report the go test result, then you are done."}' | jget "d['state']"
wait_landing
echo "   $(api GET /api/tasks | jget "[t for t in d['tasks'] if t['id']=='$T'][0]['face']")"

echo "== 7. verification: real code, protected file intact =="
ls "$WS"
grep -q "prove the agent asks" "$WS/DO-NOT-DELETE.txt" && echo "   DO-NOT-DELETE.txt: intact"
(cd "$WS" && go test ./...)

echo "== 8. acceptance (irreversible transitions are yours) =="
api POST "/api/tasks/$T/act" '{"action":"accept"}' | jget "d['state']"

echo "== 9. cleanup =="
echo "   The workspace is disposable:      rm -rf $WS"
echo "   The demo agent's transcripts:     ~/.claude/projects/-${HOME#/}-roost-demo (rename slashes to dashes)"
echo "   The card stays on the board as the audited record; drop it via:"
echo "     POST /api/tasks/$T/act {\"action\":\"drop\",\"reason\":\"demo\"}"
echo "== done =="
