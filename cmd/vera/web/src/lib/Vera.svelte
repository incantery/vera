<script>
	// The Vera surface — implemented from the claude.ai/design project
	// "Vera" (Vera.dc.html). One screen: a sidebar of threads grouped
	// by OWNER (with you / with Vera / done), a detail pane whose first
	// tab is the thread's state (now, decision, review, summary), and
	// one composer. The design's demo simulation is discarded; every
	// element here is fed by vera's real rails — the WatchBoard frame,
	// the per-agent Connect stream, the tasks API, the working tree.
	// What the backend cannot do (pause, evidence ledgers) is absent,
	// not faked.
	import {
		api,
		app,
		fetchAgent,
		watchAgent,
		applyFrame,
		sayTo,
		agentDiff,
		commitTree,
		imageParts
	} from '$lib/state.svelte.js';
	import UsageBar from '$lib/UsageBar.svelte';
	import Markdown from '$lib/Markdown.svelte';

	let { data = null } = $props();

	// ── palette + voice (the Vera design's constants) ──────────────
	const INK = '#F2EEE7';
	const MUT = '#9299AA';
	const SUB = '#A9B0C2';
	const VIOLET = '#A78BFA';
	const AMBER = '#E8BB69';
	const MINT = '#6EDFC3';
	const MINT2 = '#7FE8CD';
	const RED = '#F49B9B';
	const PANEL = '#131722';
	const CODE = '#10141D';
	const MONO = "'IBM Plex Mono', ui-monospace, monospace";
	const HAIR = 'rgba(242,238,231,0.07)';
	const KICKER =
		'font-size: 10.5px; letter-spacing: 0.12em; text-transform: uppercase; font-weight: 600;';

	// ── the frame: stream first, REST poll as fallback ─────────────
	let polled = $state(null);
	const board = $derived(data ?? polled);
	let busy = $state(false);
	let err = $state('');

	async function refresh() {
		if (data) return;
		try {
			const r = await api('/api/tasks');
			if (!r.ok) throw new Error((await r.json()).error ?? 'vera did not answer');
			polled = await r.json();
			err = '';
		} catch (e) {
			err = e.message;
		}
	}
	$effect(() => {
		if (data) return;
		refresh();
		const t = setInterval(refresh, 3000);
		return () => clearInterval(t);
	});

	async function postJson(path, body) {
		busy = true;
		err = '';
		try {
			const r = await api(path, { method: 'POST', body: JSON.stringify(body ?? {}) });
			const out = await r.json();
			if (!r.ok) throw new Error(out.error ?? 'refused');
			await refresh();
			return out;
		} catch (e) {
			err = e.message;
		} finally {
			busy = false;
		}
	}

	// ── threads: tasks and taskless live sessions, one list ────────
	const tasks = $derived(board?.tasks ?? []);
	const sessions = $derived(board?.sessions ?? app.sessions);

	function taskState(t) {
		if (t.col === 'done') return 'done';
		if (t.col === 'dropped') return 'dropped';
		if (t.col === 'waiting') return 'ask';
		if (t.proposal) return 'proposal';
		if (t.col === 'inbox') return 'inbox';
		if (t.live?.state === 'working') return 'working';
		if (t.live?.state === 'needs you' || t.live?.state === 'blocked?') return 'attention';
		return 'quiet';
	}
	function phraseOf(th) {
		const t = th.task;
		switch (th.state) {
			case 'ask':
				return 'Needs your answer';
			case 'proposal':
				return t.proposalKind === 'start' ? 'Proposes to start — your call' : 'Ready to close — your call';
			case 'attention':
				return (t ? t.live?.state : th.session?.state) === 'blocked?'
					? 'Possibly blocked on an approval'
					: 'Waiting on you';
			case 'working':
				return (t ? t.live?.now : sessTool(th.session)) || 'Working';
			case 'quiet':
				return 'With an agent — quiet';
			case 'inbox':
				return 'Captured — not started';
			case 'dropped':
				return 'Dropped · ' + relAge(t.updatedAt);
			default:
				return relAge(t?.updatedAt ?? new Date().toISOString());
		}
	}
	const sessTool = (s) => (s?.tool ? s.tool + (s.toolDetail ? ' — ' + s.toolDetail : '') : '');

	const threads = $derived.by(() => {
		const out = tasks.map((t) => {
			const state = taskState(t);
			return {
				id: 'T:' + t.id,
				kind: 'task',
				task: t,
				session: null,
				agent: t.agent ?? null,
				name: t.title,
				state,
				owner:
					state === 'done' || state === 'dropped'
						? 'done'
						: ['ask', 'proposal', 'attention', 'inbox'].includes(state)
							? 'you'
							: 'vera'
			};
		});
		for (const s of sessions) {
			if (s.task || s.state === 'idle') continue;
			out.push({
				id: 'A:' + s.id,
				kind: 'session',
				task: null,
				session: s,
				agent: s.id,
				name: s.title || s.dir,
				state: s.state === 'working' ? 'working' : 'attention',
				owner: s.state === 'working' ? 'vera' : 'you'
			});
		}
		return out;
	});
	const RANK = { ask: 0, proposal: 1, attention: 2, inbox: 3 };
	const youRows = $derived(
		threads.filter((t) => t.owner === 'you').sort((a, b) => (RANK[a.state] ?? 9) - (RANK[b.state] ?? 9))
	);
	const veraRows = $derived(threads.filter((t) => t.owner === 'vera'));
	const doneRows = $derived(threads.filter((t) => t.owner === 'done'));

	// ── header presence ────────────────────────────────────────────
	const nDecisions = $derived(youRows.filter((t) => ['ask', 'proposal'].includes(t.state)).length);
	const statusLine = $derived(
		veraRows.length
			? 'Vera is working' + (youRows.length ? ' · ' + youRows.length + ' with you' : '')
			: nDecisions
				? 'Vera needs you · ' + nDecisions + (nDecisions === 1 ? ' decision' : ' decisions')
				: youRows.length
					? 'Vera is waiting · ' + youRows.length + ' with you'
					: 'Vera is available'
	);
	const topMode = $derived(veraRows.length ? 'vera' : youRows.length ? 'you' : 'idle');

	// ── selection + the selected thread's agent, live ──────────────
	let selId = $state(null);
	let tab = $state(0);
	// On a phone the two panes become one: the list, until a thread (or
	// the door, or the plan flow) claims the screen. Desktop never reads
	// this flag — the media query does.
	let mobileMain = $state(false);
	function pickThread(id) {
		selId = id;
		mobileMain = true;
	}
	function backToList() {
		mobileMain = false;
		selId = null;
		planView = null;
	}
	const sel = $derived(threads.find((t) => t.id === selId) ?? null);
	let agentData = $state(null);
	let reviseArm = $state(false);
	let draft = $state('');

	$effect(() => {
		selId;
		tab = 0;
		reviseArm = false;
		startIn = '';
		startMode = 'read';
		newWs = null;
		confirmDiscard = false;
		commitMsg = '';
	});

	// The selected thread's agent rides its own Connect stream — the
	// same rail as the cockpit, so history, pending, and the working
	// tree stay live while you look at the thread.
	$effect(() => {
		const aid = sel?.agent;
		agentData = null;
		if (!aid) return;
		let alive = true;
		let stop = null;
		let retry = null;
		fetchAgent(aid, { raw: true })
			.then((d) => alive && (agentData = d))
			.catch(() => {});
		const open = () => {
			stop = watchAgent(
				aid,
				true,
				(f) => {
					agentData = applyFrame(agentData, f);
				},
				() => {
					if (alive) retry = setTimeout(open, 5000);
				}
			);
		};
		open();
		return () => {
			alive = false;
			stop?.();
			clearTimeout(retry);
		};
	});
	// a live second-hand for "for 34s"
	let nowTick = $state(Date.now());
	$effect(() => {
		const t = setInterval(() => (nowTick = Date.now()), 1000);
		return () => clearInterval(t);
	});
	const pendingSecs = $derived(
		agentData?.pending?.at
			? Math.max(0, Math.round((nowTick - new Date(agentData.pending.at).getTime()) / 1000))
			: 0
	);

	// ── review: the working tree, whole ────────────────────────────
	const treeFiles = $derived(agentData?.tree ?? []);
	const isReview = $derived(
		!!sel && ['ask', 'attention', 'quiet'].includes(sel.state) && treeFiles.length > 0
	);
	let diffData = $state(null); // agentDiff result
	let diffSel = $state(0);
	let commitMsg = $state('');
	let confirmDiscard = $state(false);

	async function loadDiff() {
		if (!sel?.agent) {
			diffData = null;
			return;
		}
		try {
			diffData = await agentDiff(sel.agent);
			if (diffSel >= (diffData.files?.length ?? 0)) diffSel = 0;
		} catch (e) {
			err = e.message;
		}
	}
	$effect(() => {
		sel?.id;
		diffData = null;
		diffSel = 0;
	});
	$effect(() => {
		if ((tab === 1 || (tab === 0 && isReview)) && sel?.agent && !diffData) loadDiff();
	});

	function diffRows(text) {
		if (!text) return [];
		const lines = text.split('\n');
		const first = lines.findIndex((l) => l.startsWith('@@'));
		let ln = 0;
		return (first >= 0 ? lines.slice(first) : lines).map((l, i) => {
			const k = l.startsWith('@@')
				? 'hunk'
				: l.startsWith('+')
					? 'add'
					: l.startsWith('-')
						? 'del'
						: 'ctx';
			if (k !== 'del') ln++;
			return { id: i, k, ln: k === 'del' ? '−' : k === 'hunk' ? '' : String(ln), t: l || ' ' };
		});
	}
	async function approve() {
		if (!sel?.agent || busy) return;
		busy = true;
		err = '';
		try {
			await commitTree(sel.agent, commitMsg.trim() || sel.name);
			commitMsg = '';
			await loadDiff();
			agentData = { ...agentData, tree: [] };
		} catch (e) {
			err = e.message;
		}
		busy = false;
	}

	// ── the plan flow: "Give Vera work" ────────────────────────────
	let bigDraft = $state('');
	let planView = $state(null); // {phase:'thinking'|'bid'|'error', text, id, plan, err}
	let planAnswer = $state('');

	function goNew() {
		selId = null;
		planView = null;
		bigDraft = '';
		mobileMain = true;
	}
	async function launch(text) {
		selId = null;
		mobileMain = true;
		planView = { phase: 'thinking', text };
		try {
			const r = await api('/api/plan', { method: 'POST', body: JSON.stringify({ text }) });
			const out = await r.json();
			if (r.status === 409) {
				const c = await postJson('/api/tasks', { text });
				planView = null;
				if (c?.task) selId = 'T:' + c.task.id;
				return;
			}
			if (!r.ok) throw new Error(out.error ?? 'vera did not answer');
			planView = { phase: 'bid', text, id: out.id, plan: out.plan };
		} catch (e) {
			planView = { phase: 'error', text, err: e.message };
		}
	}
	async function makeItSo() {
		const out = await postJson('/api/plan/execute', { id: planView.id, plan: planView.plan });
		if (!out) return;
		planView = null;
		bigDraft = '';
		if (out.id) selId = 'T:' + out.id;
	}
	async function justCapture() {
		const out = await postJson('/api/tasks', { text: planView.text });
		if (!out) return;
		planView = null;
		bigDraft = '';
		if (out.task) selId = 'T:' + out.task.id;
	}
	async function answerPlan() {
		const a = planAnswer.trim();
		if (!a) return;
		const text = planView.text + ' — ' + a;
		planAnswer = '';
		await launch(text);
	}
	const planPlace = (p) =>
		p.kind === 'repo'
			? 'continue in ' + p.where
			: p.kind === 'new'
				? 'new ' + p.home + ' workspace · ' + p.name
				: p.kind === 'ask'
					? 'Vera needs one answer'
					: 'no workspace fits';

	// ── decisions: proposals, starts, replies ──────────────────────
	let startIn = $state('');
	let startMode = $state('read');
	let newWs = $state(null);

	const act = (tid, action, extra) => postJson(`/api/tasks/${tid}/act`, { action, ...extra });
	async function startWork(t, mode) {
		const out = await postJson(`/api/tasks/${t.id}/start`, {
			mode: mode ?? startMode,
			...(startIn ? { newIn: startIn } : {})
		});
		if (out) startIn = '';
	}
	async function acceptProposal(t) {
		if (t.proposalKind === 'start') await startWork(t);
		else await act(t.id, 'accept');
	}
	function onRepoPick(e) {
		if (e.target.value !== '__new__') return;
		startIn = '';
		newWs = '';
	}
	async function createWs() {
		const name = (newWs ?? '').trim();
		if (!name || busy) return;
		const out = await postJson('/api/workspaces', { name });
		if (out?.cwd) {
			startIn = out.cwd;
			newWs = null;
		}
	}

	// ── the composer: one input, state-routed ──────────────────────
	const composerPh = $derived(
		!sel
			? ''
			: reviseArm
				? 'Describe the revision…'
				: sel.state === 'ask'
					? 'Answer Vera — the work continues from here…'
					: sel.state === 'done' || sel.state === 'dropped'
						? 'Continue from this result…'
						: sel.state === 'proposal' || sel.state === 'inbox'
							? sel.agent
								? 'Steer Vera or add context…'
								: 'Decide above — the thread talks once the work starts'
							: 'Steer Vera or add context…'
	);
	const composerOn = $derived(
		!!sel && (sel.state === 'ask' || sel.state === 'done' || sel.state === 'dropped' || !!sel.agent)
	);
	let cmdEl = $state(null);
	async function submitComposer() {
		const text = draft.trim();
		if (!text || !sel || busy) return;
		err = '';
		try {
			if (sel.state === 'done' || sel.state === 'dropped') {
				draft = '';
				await launch(text);
				return;
			}
			if (reviseArm && sel.agent) {
				await sayTo(sel.agent, 'Revision requested: ' + text);
				reviseArm = false;
				tab = 2;
			} else if (sel.kind === 'task' && sel.task.col === 'waiting') {
				await postJson(`/api/tasks/${sel.task.id}/reply`, { text });
			} else if (sel.agent) {
				await sayTo(sel.agent, text);
				tab = 2;
			} else return;
			draft = '';
		} catch (e) {
			err = e.message;
		}
	}
	function onDraftKey(e) {
		if (e.key === 'Escape' && reviseArm) {
			reviseArm = false;
			return;
		}
		if (e.key === 'Enter') submitComposer();
	}

	// ── activity + conversation, from what's real ──────────────────
	function relAge(iso) {
		const s = (Date.now() - new Date(iso).getTime()) / 1000;
		if (s < 60) return 'now';
		if (s < 3600) return Math.floor(s / 60) + 'm';
		if (s < 172800) return Math.floor(s / 3600) + 'h';
		return Math.floor(s / 86400) + 'd';
	}
	const fmtTok = (n) => (n >= 1000 ? (n / 1000).toFixed(1) + 'k' : String(n ?? 0));
	const spendTotal = $derived(
		(agentData?.spend?.claudeUsd ?? 0) + (agentData?.spend?.judgeUsd ?? 0)
	);

	const traceRows = $derived.by(() => {
		if (sel?.task?.log?.length)
			return sel.task.log.map((e) => ({
				time: relAge(e.at),
				t: e.text,
				k: e.actor === 'human' ? 'mark' : e.actor === 'worker' ? 'tool' : 'step'
			}));
		// session threads: the transcript's tool steps stand in
		const out = [];
		for (const m of agentData?.history ?? []) {
			if (m.role !== 'assistant') continue;
			for (const st of m.steps ?? [])
				out.push({ time: '', t: st.tool.toLowerCase() + (st.detail ? ' ' + st.detail : ''), k: 'tool' });
		}
		return out;
	});
	const recentRows = $derived(traceRows.slice(-3));
	const olderCount = $derived(Math.max(traceRows.length - 3, 0));

	const convRows = $derived.by(() => {
		if (agentData?.history?.length)
			return agentData.history
				.map((m) => ({
					who: m.role === 'user' ? 'You' : 'Vera',
					md: m.role !== 'user',
					t: m.role === 'user' ? m.rough || imageParts(m.text).text : m.text
				}))
				.filter((m) => (m.t ?? '').trim());
		const out = [];
		for (const x of sel?.task?.exchanges ?? []) {
			out.push({ who: 'Vera', md: false, t: x.prompt });
			if (x.reply) out.push({ who: 'Agent', md: true, t: x.reply });
		}
		return out;
	});

	const nowAction = $derived(
		sel?.task?.live?.now ||
			(agentData?.agent?.tool
				? agentData.agent.tool + (agentData.agent.toolDetail ? ' — ' + agentData.agent.toolDetail : '')
				: '') ||
			'Working'
	);
	const doneNote = $derived.by(() => {
		const t = sel?.task;
		if (!t) return '';
		const last = t.log?.at(-1);
		return t.face || last?.text || 'Closed.';
	});

	const traceStyle = (k) =>
		k === 'tool'
			? `font-family: ${MONO}; font-size: 12px; color: ${SUB}; min-width: 0;`
			: k === 'mark'
				? `font-size: 12.5px; color: rgba(232,187,105,0.85); min-width: 0;`
				: `font-size: 13.5px; color: rgba(242,238,231,0.88); font-weight: 500; min-width: 0;`;

	const tab0Label = $derived(
		!sel
			? 'Now'
			: isReview
				? 'Review'
				: { working: 'Now', quiet: 'Now', ask: 'Decision', proposal: 'Decision', attention: 'Decision', inbox: 'Start', done: 'Summary', dropped: 'Summary' }[
						sel.state
					]
	);
	const tabStyle = (i) =>
		`background: transparent; border: 0; font: inherit; font-size: 13px; font-weight: 500; cursor: pointer; padding: 4px 1px 10px; color: ${tab === i ? INK : MUT}; box-shadow: ${tab === i ? 'inset 0 -2px 0 rgba(242,238,231,0.8)' : 'none'};`;

	// row visuals per design: amber ring for ready-for-you, solid dots elsewhere
	function rowDot(th) {
		if (th.state === 'ask' || th.state === 'proposal')
			return `background: transparent; box-shadow: inset 0 0 0 1.5px ${AMBER};`;
		const c =
			th.owner === 'done'
				? 'rgba(146,153,170,0.45)'
				: th.owner === 'you'
					? AMBER
					: VIOLET;
		return `background: ${c};`;
	}
</script>

<!-- the presence glyph: ring + core, animated by mode -->
{#snippet glyph(mode, size)}
	{@const P = { vera: VIOLET, you: AMBER, done: MINT, idle: MUT, understanding: VIOLET }}
	{@const ring = P[mode] ?? MUT}
	<svg width={size} height={size} viewBox="0 0 24 24" style="flex: none; display: block;">
		<circle cx="12" cy="12" r="9.2" fill="none" stroke={ring} stroke-opacity="0.38" stroke-width="1.4" />
		{#if mode === 'vera'}
			<circle cx="12" cy="12" r="4.4" fill={ring} style="animation: vCore 3.4s ease-in-out infinite; transform-origin: center;" />
			<g style="animation: vOrbit 3.6s linear infinite; transform-origin: 12px 12px;">
				<circle cx="12" cy="4.6" r="1.5" fill={ring} opacity="0.9" />
			</g>
		{:else if mode === 'understanding'}
			<circle cx="12" cy="12" r="4.6" fill={ring} style="animation: vGather 2.6s ease-in-out infinite; transform-origin: center;" />
		{:else if mode === 'idle'}
			<circle cx="12" cy="12" r="3.6" fill={ring} style="animation: vBreathSlow 7s ease-in-out infinite;" />
		{:else}
			<circle cx="12" cy="12" r="4.4" fill={ring} opacity="0.85" />
		{/if}
	</svg>
{/snippet}

{#snippet threadRow(th)}
	<div
		class="vrow"
		role="button"
		tabindex="0"
		onclick={() => pickThread(th.id)}
		onkeydown={(e) => {
			if (e.key === 'Enter' || e.key === ' ') {
				e.preventDefault();
				pickThread(th.id);
			}
		}}
		style="display: flex; flex-direction: column; gap: 3px; padding: 9px 10px; border-radius: 10px; cursor: pointer; margin-bottom: 2px; background: {th.id === selId ? PANEL : 'transparent'}; box-shadow: {th.id === selId ? 'inset 2px 0 0 rgba(242,238,231,0.35)' : 'none'};"
	>
		<div style="display: flex; align-items: center; gap: 9px;">
			<span style="width: 7px; height: 7px; border-radius: 50%; flex: none; {rowDot(th)}"></span>
			<span
				style="flex: 1; min-width: 0; font-size: 14.5px; font-weight: 500; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; {th.owner === 'done' ? 'opacity: 0.62;' : ''}">{th.name}</span
			>
			{#if th.owner === 'done'}
				<span style="font-size: 11px; font-family: {MONO}; color: rgba(146,153,170,0.55);"
					>{th.state === 'dropped' ? 'dropped' : relAge(th.task.updatedAt)}</span
				>
			{/if}
		</div>
		{#if th.owner !== 'done'}
			<div
				style="font-size: 12.5px; padding-left: 16px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; color: {th.owner === 'you' ? AMBER : 'rgba(146,153,170,0.85)'};"
			>
				{phraseOf(th)}
			</div>
		{/if}
	</div>
{/snippet}

{#snippet fileList(files, compact)}
	{#each files as f, i (f.path)}
		<button
			class="vfile"
			onclick={() => (diffSel = i)}
			style="display: flex; gap: 12px; align-items: baseline; width: 100%; padding: 6px 10px; border-radius: 8px; background: {i === diffSel ? PANEL : 'transparent'}; border: 0; font: inherit; cursor: pointer; box-shadow: {i === diffSel ? 'inset 0 0 0 1px rgba(242,238,231,0.09)' : 'none'}; color: inherit;"
		>
			<span
				style="flex: 1; min-width: 0; font-family: {MONO}; font-size: 13.5px; color: rgba(242,238,231,0.94); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; text-align: left;">{f.path}</span
			>
			<span style="font-size: 11px; color: rgba(146,153,170,0.6); flex: none;"
				>{f.binary ? 'binary' : f.new ? 'created' : 'modified'}</span
			>
			<span
				style="font-family: {MONO}; font-size: 11.5px; font-variant-numeric: tabular-nums; flex: none; width: 86px; text-align: right;"
				><span style="color: {MINT};">+{f.add}</span>
				<span style="color: {RED};">{f.del ? '−' + f.del : ''}</span></span
			>
		</button>
	{/each}
	{#if diffData?.files?.[diffSel] && !diffData.files[diffSel].binary}
		{@const rf = diffData.files[diffSel]}
		<div style="background: {CODE}; border-radius: 12px; padding: 14px 4px 14px 16px; margin-top: 12px;">
			<div style="font-family: {MONO}; font-size: 11px; color: rgba(146,153,170,0.75); margin-bottom: 10px;">
				{rf.path}{rf.truncated ? ' · truncated' : ''}
			</div>
			<div style="max-height: {compact ? '440px' : '520px'}; overflow-y: auto; overflow-x: auto; padding-right: 12px;">
				{#each diffRows(rf.diff) as d (d.id)}
					<div
						style="display: flex; align-items: stretch; border-radius: 4px; background: {d.k === 'add' ? 'rgba(110,223,195,0.07)' : d.k === 'del' ? 'rgba(240,125,125,0.06)' : 'transparent'};"
					>
						<span
							style="flex: none; width: 34px; text-align: right; padding-right: 12px; font-family: {MONO}; font-size: 11px; line-height: 23px; color: {d.k === 'add' ? 'rgba(110,223,195,0.5)' : d.k === 'del' ? 'rgba(240,125,125,0.5)' : 'rgba(146,153,170,0.4)'}; user-select: none;">{d.ln}</span
						>
						<span
							style="font-family: {MONO}; font-size: 13px; line-height: 23px; white-space: pre; padding-right: 8px; min-width: 0; color: {d.k === 'add' ? MINT2 : d.k === 'del' ? RED : d.k === 'hunk' ? 'rgba(167,139,250,0.8)' : 'rgba(146,153,170,0.92)'};">{d.t}</span
						>
					</div>
				{/each}
			</div>
		</div>
	{/if}
{/snippet}

{#snippet decisionShell(kicker, ask, why)}
	<div style="display: flex; align-items: center; gap: 12px;">
		{@render glyph('you', 22)}
		<span style="{KICKER} letter-spacing: 0.14em; color: {AMBER};">{kicker}</span>
	</div>
	<h3 style="margin: 14px 0 0; font-size: 20px; font-weight: 600; letter-spacing: -0.01em;">{ask}</h3>
	{#if why}
		<p style="margin: 10px 0 0; font-size: 14.5px; line-height: 1.6; color: rgba(242,238,231,0.88); max-width: 66ch;">
			{why}
		</p>
	{/if}
{/snippet}

{#snippet startControls(t)}
	<div style="display: flex; gap: 8px; flex-wrap: wrap; margin-top: 14px;">
		{#if !t.agent}
			<select
				bind:value={startIn}
				onchange={onRepoPick}
				style="flex: 1; min-width: 220px; font: inherit; font-size: 13px; color: {INK}; background: {PANEL}; border: 0; box-shadow: inset 0 0 0 1px rgba(242,238,231,0.08); border-radius: 10px; padding: 8px 12px;"
			>
				<option value="">on the current agent</option>
				{#each board?.repos ?? [] as r (r.cwd)}
					<option value={r.cwd}>fresh agent in {r.dir}{r.scratch ? ' (scratch)' : ''}</option>
				{/each}
				<option value="__new__">+ new scratch workspace…</option>
			</select>
		{/if}
		<select
			bind:value={startMode}
			title="read: analysis only. work: edits plus scoped build/test commands."
			style="font: inherit; font-size: 13px; color: {INK}; background: {PANEL}; border: 0; box-shadow: inset 0 0 0 1px rgba(242,238,231,0.08); border-radius: 10px; padding: 8px 12px;"
		>
			<option value="read">read-only</option>
			<option value="work">can edit & test</option>
		</select>
	</div>
	{#if newWs !== null}
		<div style="display: flex; gap: 8px; margin-top: 8px;">
			<input
				bind:value={newWs}
				onkeydown={(e) => e.key === 'Enter' && createWs()}
				placeholder="name the scratch workspace — letters, digits, dashes"
				style="flex: 1; background: {PANEL}; border: 0; box-shadow: inset 0 0 0 1px rgba(242,238,231,0.08); border-radius: 10px; padding: 8px 12px; font: inherit; font-size: 13px; color: {INK}; outline: none;"
			/>
			<button class="vbtn" onclick={createWs} disabled={busy || !(newWs ?? '').trim()}>create</button>
			<button class="vghost" onclick={() => (newWs = null)}>cancel</button>
		</div>
	{/if}
{/snippet}

<div
	class="vx"
	style="height: 100dvh; display: flex; flex-direction: column; overflow: hidden; background: #0B0D12; color: {INK}; font-family: 'Source Sans 3', system-ui, sans-serif;"
>
	<header
		class="v-head"
		style="flex: none; display: flex; align-items: center; gap: 10px; padding: 0 20px; height: 54px; background: linear-gradient(to right, transparent, {HAIR} 48px, {HAIR} calc(100% - 48px), transparent) no-repeat bottom / 100% 1px;"
	>
		<button class="v-back" class:on={mobileMain} onclick={backToList} aria-label="back to the thread list">←</button>
		<span style="display: flex;">{@render glyph(topMode, 20)}</span>
		<span style="font-size: 15px; font-weight: 600; letter-spacing: 0.02em;">Vera</span>
		<span style="flex: 1;"></span>
		{#if board?.spend}
			<span
				style="font-size: 12px; color: rgba(146,153,170,0.7); font-family: {MONO}; font-variant-numeric: tabular-nums;"
				title="everything vera has spent this process">${board.spend.toFixed(2)}</span
			>
		{/if}
		<span class="v-status" style="font-size: 13.5px; color: {SUB}; font-variant-numeric: tabular-nums;">{statusLine}</span>
		<button class="vbtn" onclick={goNew}>Give Vera work</button>
	</header>

	{#if err}
		<div
			role="alert"
			style="margin: 10px 20px 0; font-size: 12.5px; color: {RED}; background: rgba(240,125,125,0.07); box-shadow: inset 0 0 0 1px rgba(240,125,125,0.25); border-radius: 10px; padding: 8px 12px;"
		>
			{err}
		</div>
	{/if}

	<div class="vshell" class:m-main={mobileMain} style="flex: 1; display: flex; min-height: 0;">
		<aside
			style="width: 296px; flex: none; display: flex; flex-direction: column; min-height: 0; background: linear-gradient(to bottom, transparent, {HAIR} 48px, {HAIR} calc(100% - 48px), transparent) no-repeat right / 1px 100%;"
		>
			<div style="flex: 1; overflow-y: auto; min-height: 0; padding: 8px 10px 8px;">
				{#if youRows.length}
					<div style="{KICKER} color: rgba(232,187,105,0.85); padding: 12px 10px 6px;">With you</div>
					{#each youRows as th (th.id)}{@render threadRow(th)}{/each}
				{/if}
				{#if veraRows.length}
					<div style="{KICKER} color: rgba(167,139,250,0.8); padding: 16px 10px 6px;">With Vera</div>
					{#each veraRows as th (th.id)}{@render threadRow(th)}{/each}
				{/if}
				{#if doneRows.length}
					<div style="{KICKER} color: rgba(146,153,170,0.65); padding: 16px 10px 6px;">Done</div>
					{#each doneRows as th (th.id)}{@render threadRow(th)}{/each}
				{/if}
				{#if !threads.length && board}
					<div style="padding: 16px 10px; font-size: 13px; color: {MUT}; line-height: 1.55;">
						Nothing yet — give Vera work and threads land here.
					</div>
				{/if}
			</div>
			<div
				style="flex: none; padding: 10px 16px 14px; display: flex; flex-direction: column; gap: 8px; background: linear-gradient(to right, transparent, {HAIR}, transparent) no-repeat top / 100% 1px;"
			>
				{#if !app.connected}
					<div role="alert" style="font-size: 11.5px; color: {RED}; line-height: 1.5;">
						vera is not answering — is the binary still running?
					</div>
				{/if}
				{#if app.notice}
					<div style="font-size: 11px; color: {MUT}; line-height: 1.5;">{app.notice}</div>
				{/if}
				<UsageBar usage={app.usage} />
				<div style="display: flex; gap: 14px; font-size: 11.5px;">
					<a href="/board" style="color: {MUT}; text-decoration: none;" title="the map view — every card by stage">board →</a>
					<a href="/explore" style="color: {MUT}; text-decoration: none;" title="browse directories · start a session anywhere">explorer →</a>
				</div>
			</div>
		</aside>

		<main style="flex: 1; min-width: 0; display: flex; flex-direction: column; min-height: 0;">
			{#if planView}
				<!-- the plan flow: understanding → Vera's bid → your nod -->
				<div class="v-plan" style="flex: 1; overflow-y: auto; min-height: 0; padding: 40px; display: flex; justify-content: center;">
					<div style="max-width: 640px; width: 100%; animation: vEnter 0.35s ease;">
						<div style="display: flex; align-items: center; gap: 12px;">
							{@render glyph('understanding', 22)}
							<span style="font-size: 16.5px; font-weight: 500;">
								{planView.phase === 'thinking' ? 'Understanding the request' : planView.phase === 'error' ? 'The plan did not come back' : 'Vera’s plan'}
							</span>
						</div>
						<p style="margin: 14px 0 0; font-size: 14.5px; line-height: 1.6; color: rgba(242,238,231,0.85);">
							{planView.text}
						</p>
						{#if planView.phase === 'error'}
							<p style="margin: 14px 0 0; font-size: 13.5px; color: {RED};">{planView.err}</p>
							<div style="display: flex; gap: 10px; margin-top: 18px;">
								<button class="vbtn" onclick={() => launch(planView.text)}>Try again</button>
								<button class="vghost" onclick={goNew}>Never mind</button>
							</div>
						{:else if planView.phase === 'bid'}
							{@const p = planView.plan}
							<div style="{KICKER} color: rgba(146,153,170,0.7); margin: 24px 0 4px;">Where</div>
							<div style="font-size: 13.5px; color: rgba(242,238,231,0.78); font-family: {MONO};">
								{planPlace(p)}{p.cadence === 'standing' ? ' · standing' : ''}{p.deadline ? ' · due ' + p.deadline : ''}
							</div>
							{#if p.kind === 'ask'}
								<h3 style="margin: 20px 0 0; font-size: 18px; font-weight: 600;">{p.question}</h3>
								<div style="display: flex; gap: 8px; margin-top: 14px;">
									<input
										bind:value={planAnswer}
										onkeydown={(e) => e.key === 'Enter' && answerPlan()}
										placeholder="your answer…"
										style="flex: 1; background: {PANEL}; border: 0; box-shadow: inset 0 0 0 1px rgba(242,238,231,0.08); border-radius: 12px; padding: 11px 14px; font: inherit; font-size: 14px; color: {INK}; outline: none;"
									/>
									<button class="vbtn" onclick={answerPlan} disabled={busy}>Answer → replan</button>
								</div>
							{:else if p.kind !== 'none'}
								{#if p.goal}
									<div style="{KICKER} color: rgba(146,153,170,0.7); margin: 20px 0 4px;">Goal</div>
									<div style="font-size: 14.5px; line-height: 1.6; color: rgba(242,238,231,0.92);">{p.goal}</div>
								{/if}
								{#if p.steps?.length}
									<div style="{KICKER} color: rgba(146,153,170,0.7); margin: 20px 0 4px;">Plan</div>
									{#each p.steps as st, i (i)}
										<div style="display: flex; align-items: baseline; gap: 10px; padding: 4px 0;">
											<span style="width: 8px; height: 8px; border-radius: 50%; flex: none; box-shadow: inset 0 0 0 1.2px rgba(146,153,170,0.5);"></span>
											<span style="font-size: 13.5px; color: rgba(242,238,231,0.65);">{st}</span>
										</div>
									{/each}
									<div style="font-size: 12px; color: rgba(146,153,170,0.6); margin-top: 4px;">later pieces land as their own threads</div>
								{/if}
							{/if}
							{#if p.why}
								<p style="margin: 16px 0 0; font-size: 13px; line-height: 1.55; color: {MUT};">{p.why}</p>
							{/if}
							<div style="display: flex; gap: 10px; margin-top: 24px; align-items: center;">
								{#if p.kind !== 'none' && p.kind !== 'ask'}
									<button class="vcta" onclick={makeItSo} disabled={busy}>Make it so</button>
								{/if}
								<button class="vghost" onclick={justCapture} disabled={busy}>Just capture</button>
								<button class="vghost" onclick={goNew}>Never mind</button>
							</div>
						{/if}
					</div>
				</div>
			{:else if !sel}
				<!-- no selection: the door -->
				<div class="v-door" style="flex: 1; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 16px; padding: 40px; animation: vEnter 0.35s ease;">
					<span>{@render glyph('idle', 34)}</span>
					<h1 style="margin: 0; font-size: 28px; font-weight: 600; letter-spacing: -0.01em;">What should we work on?</h1>
					<p style="margin: 0; font-size: 14.5px; color: {MUT};">I'll plan it, do the work, and bring you the decisions that matter.</p>
					<input
						bind:value={bigDraft}
						onkeydown={(e) => e.key === 'Enter' && bigDraft.trim() && launch(bigDraft.trim())}
						placeholder="Describe the outcome you want…"
						style="width: min(640px, 90%); background: {PANEL}; border: 0; box-shadow: inset 0 0 0 1px rgba(242,238,231,0.08); border-radius: 14px; padding: 16px 18px; font: inherit; font-size: 15px; color: {INK}; outline: none; margin-top: 8px;"
					/>
					<span style="font-size: 11.5px; font-family: {MONO}; color: rgba(146,153,170,0.55);">Enter to start · Vera plans before it spends</span>
				</div>
			{:else}
				<!-- selected thread -->
				<div class="v-thread-head" style="flex: none; padding: 24px 32px 0;">
					<div style="display: flex; align-items: center; gap: 12px; flex-wrap: wrap;">
						<h2 style="margin: 0; font-size: 22px; font-weight: 600; letter-spacing: -0.01em;">{sel.name}</h2>
						{#if sel.task?.deadline && !['done', 'dropped'].includes(sel.state)}
							<span style="font-size: 11.5px; font-family: {MONO}; color: {AMBER};">due {sel.task.deadline}</span>
						{/if}
						<span style="flex: 1;"></span>
						{#if sel.agent}
							<a
								href="/agent/{sel.agent}?mode=direct"
								style="font-size: 12.5px; color: {MUT}; text-decoration: none; box-shadow: inset 0 0 0 1px rgba(242,238,231,0.12); padding: 6px 12px; border-radius: 9px;"
								title="the cockpit: the raw event timeline, live">Cockpit →</a
							>
						{/if}
					</div>
					{#if sel.task?.intent && !sel.task.intent.toLowerCase().startsWith(sel.name.toLowerCase())}
						<p style="margin: 6px 0 0; font-size: 13.5px; color: {MUT}; max-width: 76ch; line-height: 1.5;">{sel.task.intent}</p>
					{/if}
					<div class="v-tabs" style="display: flex; gap: 22px; margin-top: 20px;">
						<button style={tabStyle(0)} onclick={() => (tab = 0)}>{tab0Label}</button>
						<button style={tabStyle(1)} onclick={() => (tab = 1)}
							>Changes{treeFiles.length ? ' · ' + treeFiles.length : ''}</button
						>
						<button style={tabStyle(2)} onclick={() => (tab = 2)}>Conversation</button>
						<button style={tabStyle(3)} onclick={() => (tab = 3)}>Activity</button>
					</div>
				</div>

				<div class="v-thread-body" style="flex: 1; overflow-y: auto; overflow-x: hidden; min-height: 0; padding: 24px 32px 28px;">
					{#if tab === 0}
						{#if isReview}
							<!-- REVIEW: the handoff — summary left, the whole tree right -->
							<div style="display: flex; flex-wrap: wrap; gap: 36px; animation: vEnter 0.35s ease;">
								<div style="flex: 2 1 320px; max-width: 470px; min-width: 0;">
									<h3 style="margin: 0; font-size: 24px; font-weight: 600; letter-spacing: -0.01em;">Ready for you</h3>
									{#if sel.task?.ask}
										<p style="margin: 10px 0 0; font-size: 15.5px; line-height: 1.6; color: rgba(242,238,231,0.94);">{sel.task.ask}</p>
									{:else if sel.task?.exchanges?.at(-1)?.reply}
										<p style="margin: 10px 0 0; font-size: 14.5px; line-height: 1.6; color: rgba(242,238,231,0.88); max-height: 200px; overflow: hidden;">
											{sel.task.exchanges.at(-1).reply.slice(0, 480)}
										</p>
									{:else}
										<p style="margin: 10px 0 0; font-size: 14.5px; line-height: 1.6; color: rgba(242,238,231,0.88);">
											The agent's working tree holds uncommitted changes. Approve them as one commit, or ask for a revision.
										</p>
									{/if}
									{#if sel.task?.runs?.length}
										<div style="{KICKER} color: rgba(110,223,195,0.9); margin: 24px 0 4px;">Runs</div>
										{#each sel.task.runs as r, i (i)}
											<div style="display: flex; gap: 9px; align-items: baseline; padding: 4px 0;">
												<span style="color: {MINT}; font-size: 12px; flex: none;">✓</span>
												<span style="font-size: 14px; color: rgba(242,238,231,0.92); min-width: 0;">{r.outcome}</span>
												<span style="font-size: 11.5px; color: {MUT}; flex: none; margin-left: auto; font-family: {MONO};">{r.kind}</span>
											</div>
										{/each}
									{/if}
									<div style="display: flex; flex-direction: column; gap: 8px; margin-top: 24px;">
										<input
											bind:value={commitMsg}
											onkeydown={(e) => e.key === 'Enter' && approve()}
											placeholder="commit message — defaults to the thread name"
											style="background: {PANEL}; border: 0; box-shadow: inset 0 0 0 1px rgba(242,238,231,0.08); border-radius: 10px; padding: 9px 12px; font-family: {MONO}; font-size: 12px; color: {INK}; outline: none;"
										/>
										<div style="display: flex; gap: 10px; align-items: center;">
											<button class="vcta" onclick={approve} disabled={busy}
												>Approve & commit {treeFiles.length} {treeFiles.length === 1 ? 'file' : 'files'}</button
											>
											<button
												class="vghost"
												style="text-decoration: underline; text-underline-offset: 3px;"
												onclick={() => {
													reviseArm = true;
													cmdEl?.focus();
												}}>or ask for a revision</button
											>
										</div>
									</div>
								</div>
								<div style="flex: 3 1 420px; min-width: 0;">
									<div style="{KICKER} color: rgba(146,153,170,0.7); margin-bottom: 8px;">
										Changes · {treeFiles.length}
										{treeFiles.length === 1 ? 'file' : 'files'}
									</div>
									{@render fileList(diffData?.files ?? treeFiles, true)}
								</div>
							</div>
						{:else if sel.state === 'working' || sel.state === 'quiet'}
							<div style="max-width: 680px; animation: vEnter 0.35s ease;">
								<div
									style="display: flex; align-items: center; gap: 14px; padding: 15px 18px; border-radius: 14px; background: linear-gradient(120deg, rgba(167,139,250,0.07), rgba(110,223,195,0.04)); box-shadow: inset 0 0 0 1px rgba(167,139,250,0.22); {sel.state === 'working' ? 'animation: vBreathe 4.5s ease-in-out infinite;' : ''}"
								>
									{@render glyph(sel.state === 'working' ? 'vera' : 'idle', 22)}
									<div style="flex: 1; min-width: 0;">
										<div style="{KICKER} letter-spacing: 0.14em; color: rgba(167,139,250,0.85);">
											{sel.state === 'working' ? 'Now' : 'Quiet'}
										</div>
										<div style="font-size: 16.5px; font-weight: 500; margin-top: 2px;">
											{sel.state === 'working' ? nowAction : 'The agent is quiet — steer it below, or open the cockpit'}
										</div>
									</div>
									{#if sel.state === 'working' && agentData?.pending}
										<span style="font-size: 11.5px; font-family: {MONO}; color: rgba(146,153,170,0.65);">for {pendingSecs}s</span>
									{/if}
								</div>
								{#if sel.task?.goal}
									<div style="{KICKER} color: rgba(146,153,170,0.7); margin: 24px 0 4px;">Goal</div>
									<div style="font-size: 13.5px; line-height: 1.6; color: rgba(242,238,231,0.78); font-family: {MONO};">{sel.task.goal}</div>
								{/if}
								{#if recentRows.length}
									<div style="{KICKER} color: rgba(146,153,170,0.7); margin: 24px 0 6px;">Recently</div>
									{#each recentRows as e, i (i)}
										<div style="display: flex; gap: 12px; align-items: baseline; padding: 3.5px 0;">
											<span style="flex: none; width: 58px; font-size: 11px; font-family: {MONO}; color: rgba(146,153,170,0.5); font-variant-numeric: tabular-nums;">{e.time}</span>
											<span style={traceStyle(e.k)}>{e.t}</span>
										</div>
									{/each}
								{/if}
								{#if olderCount}
									<button class="vghost" style="padding: 6px 0;" onclick={() => (tab = 3)}>{olderCount} earlier steps → Activity</button>
								{/if}
							</div>
						{:else if sel.state === 'ask'}
							<div style="max-width: 620px; animation: vEnter 0.35s ease;">
								{@render decisionShell('Needs your answer', sel.task.ask || 'The worker needs a word from you.', sel.task.exchanges?.at(-1)?.reply?.slice(0, 360))}
								<p style="margin: 16px 0 0; font-size: 12.5px; color: rgba(146,153,170,0.75);">
									Answer below — the same drive continues, seeded with its history.
								</p>
							</div>
						{:else if sel.state === 'proposal'}
							<div style="max-width: 620px; animation: vEnter 0.35s ease;">
								{@render decisionShell('Needs your decision', sel.task.proposal, sel.task.proposalWhy)}
								{#if sel.task.proposalKind === 'start'}
									{@render startControls(sel.task)}
								{/if}
								<div style="display: flex; flex-direction: column; gap: 10px; margin-top: 22px;">
									<button
										class="vopt"
										style="box-shadow: inset 0 0 0 1px rgba(167,139,250,0.35);"
										onclick={() => acceptProposal(sel.task)}
										disabled={busy}
									>
										<span style="display: flex; align-items: center; gap: 10px;"
											><span style="font-size: 14.5px; font-weight: 600; color: {INK};"
												>{sel.task.proposalKind === 'start' ? 'Start the work' : 'Accept as done'}</span
											><span style="{KICKER} letter-spacing: 0.08em; color: {VIOLET};">Vera's pick</span></span
										>
										<span style="display: block; font-size: 13px; color: {MUT}; margin-top: 4px; line-height: 1.5;"
											>{sel.task.proposalKind === 'start'
												? 'A drive starts under the chosen policy; you get the handoff before anything merges.'
												: 'The card closes; the log keeps the story.'}</span
										>
									</button>
									<button class="vopt" onclick={() => act(sel.task.id, 'decline')} disabled={busy}>
										<span style="font-size: 14.5px; font-weight: 600; color: {INK};">Not yet</span>
										<span style="display: block; font-size: 13px; color: {MUT}; margin-top: 4px; line-height: 1.5;">The proposal stands down; the thread stays where it is.</span>
									</button>
								</div>
							</div>
						{:else if sel.state === 'attention'}
							<div style="max-width: 620px; animation: vEnter 0.35s ease;">
								{@render decisionShell(
									'Waiting on you',
									sel.session?.state === 'blocked?' || sel.task?.live?.state === 'blocked?'
										? 'The agent looks blocked — possibly on a permission.'
										: 'The conversation is waiting on you.',
									sel.session?.lastText?.slice(0, 360)
								)}
								<p style="margin: 16px 0 0; font-size: 12.5px; color: rgba(146,153,170,0.75);">
									Say something below, or open the cockpit for the full timeline.
								</p>
							</div>
						{:else if sel.state === 'inbox'}
							<div style="max-width: 620px; animation: vEnter 0.35s ease;">
								{@render decisionShell('Start', 'Where should this run?', sel.task.intent)}
								{@render startControls(sel.task)}
								<div style="display: flex; flex-direction: column; gap: 10px; margin-top: 22px;">
									<button class="vopt" style="box-shadow: inset 0 0 0 1px rgba(167,139,250,0.35);" onclick={() => startWork(sel.task, 'read')} disabled={busy}>
										<span style="display: flex; align-items: center; gap: 10px;"><span style="font-size: 14.5px; font-weight: 600; color: {INK};">Start read-only</span><span style="{KICKER} letter-spacing: 0.08em; color: {VIOLET};">Vera's pick</span></span>
										<span style="display: block; font-size: 13px; color: {MUT}; margin-top: 4px; line-height: 1.5;">Analysis only — permission-gated tools stay refused.</span>
									</button>
									<button class="vopt" onclick={() => startWork(sel.task, 'work')} disabled={busy}>
										<span style="font-size: 14.5px; font-weight: 600; color: {INK};">Start with edits</span>
										<span style="display: block; font-size: 13px; color: {MUT}; margin-top: 4px; line-height: 1.5;">Edits plus scoped build and test commands; you review before anything merges.</span>
									</button>
								</div>
								<button class="vghost" style="margin-top: 16px;" onclick={() => act(sel.task.id, 'drop')} disabled={busy}>Drop this thread</button>
							</div>
						{:else}
							<!-- done / dropped -->
							<div style="max-width: 640px; animation: vEnter 0.35s ease;">
								<div style="display: flex; align-items: center; gap: 12px;">
									{@render glyph('done', 22)}
									<span style="{KICKER} letter-spacing: 0.14em; color: {sel.state === 'dropped' ? MUT : MINT};">{sel.state === 'dropped' ? 'Dropped' : 'Done'}</span>
								</div>
								<p style="margin: 14px 0 0; font-size: 15.5px; line-height: 1.6; color: rgba(242,238,231,0.92); max-width: 62ch;">{doneNote}</p>
								{#if sel.task?.runs?.length}
									<div style="{KICKER} color: rgba(146,153,170,0.7); margin: 22px 0 8px;">Runs</div>
									<div style="display: flex; gap: 10px; flex-wrap: wrap;">
										{#each sel.task.runs as r, i (i)}
											<div style="background: {PANEL}; border-radius: 12px; padding: 12px 16px; min-width: 180px; max-width: 320px;">
												<div style="font-size: 10px; letter-spacing: 0.1em; font-family: {MONO}; color: rgba(146,153,170,0.65); text-transform: uppercase;">{r.kind}</div>
												<div style="font-size: 13.5px; margin-top: 4px; line-height: 1.45;">{r.outcome}</div>
												{#if r.costUsd}
													<div style="font-size: 12px; color: rgba(146,153,170,0.7); margin-top: 2px; font-family: {MONO};">${r.costUsd.toFixed(2)}</div>
												{/if}
											</div>
										{/each}
									</div>
								{/if}
								<button class="vbtn" style="margin-top: 24px;" onclick={() => cmdEl?.focus()}>Continue from this result</button>
							</div>
						{/if}
					{:else if tab === 1}
						<div style="max-width: 860px; animation: vEnter 0.3s ease;">
							{#if !sel.agent}
								<p style="font-size: 13.5px; color: {MUT}; margin: 0;">No agent on this thread yet — changes appear once the work starts.</p>
							{:else if !treeFiles.length}
								<p style="font-size: 13.5px; color: {MUT}; margin: 0;">The working tree is clean — nothing uncommitted.</p>
							{:else}
								{@render fileList(diffData?.files ?? treeFiles, false)}
								<div style="display: flex; gap: 10px; margin-top: 18px; align-items: center; flex-wrap: wrap;">
									<input
										bind:value={commitMsg}
										onkeydown={(e) => e.key === 'Enter' && approve()}
										placeholder="commit message — defaults to the thread name"
										style="flex: 0 1 380px; background: {PANEL}; border: 0; box-shadow: inset 0 0 0 1px rgba(242,238,231,0.08); border-radius: 10px; padding: 9px 12px; font-family: {MONO}; font-size: 12px; color: {INK}; outline: none;"
									/>
									<button class="vcta" onclick={approve} disabled={busy}>Approve & commit</button>
								</div>
							{/if}
						</div>
					{:else if tab === 2}
						<div style="max-width: 640px; animation: vEnter 0.3s ease;">
							{#if !convRows.length}
								<p style="font-size: 13.5px; color: {MUT}; margin: 0;">Nothing yet — anything you type below lands here.</p>
							{/if}
							{#each convRows as m, i (i)}
								<div style="display: flex; gap: 14px; padding: 8px 0; animation: vEnter 0.3s ease;">
									<span
										style="flex: none; width: 44px; font-size: 11px; font-family: {MONO}; letter-spacing: 0.06em; text-transform: uppercase; padding-top: 3px; color: {m.who === 'You' ? 'rgba(146,153,170,0.8)' : VIOLET};">{m.who}</span
									>
									<div style="font-size: 14px; line-height: 1.55; color: rgba(242,238,231,0.9); min-width: 0; flex: 1;">
										{#if m.md}<div class="md"><Markdown text={m.t} /></div>{:else}<span style="white-space: pre-wrap;">{m.t}</span>{/if}
									</div>
								</div>
							{/each}
						</div>
					{:else}
						<div style="max-width: 720px; animation: vEnter 0.3s ease;">
							<div style="display: flex; gap: 30px; flex-wrap: wrap; margin-bottom: 20px;">
								{#if agentData?.ctx?.model}
									<div><div style="{KICKER} letter-spacing: 0.1em; color: rgba(146,153,170,0.6); margin-bottom: 3px; font-size: 10px;">Model</div><div style="font-size: 13px; font-family: {MONO};">{agentData.ctx.model.replace('claude-', '')}</div></div>
									<div><div style="{KICKER} letter-spacing: 0.1em; color: rgba(146,153,170,0.6); margin-bottom: 3px; font-size: 10px;">Context</div><div style="font-size: 13px; font-family: {MONO}; font-variant-numeric: tabular-nums;">{fmtTok(agentData.ctx.tokens)} · {agentData.agent?.ctxPct ?? '—'}%</div></div>
								{/if}
								{#if spendTotal || sel.task?.costUsd}
									<div><div style="{KICKER} letter-spacing: 0.1em; color: rgba(146,153,170,0.6); margin-bottom: 3px; font-size: 10px;">Cost</div><div style="font-size: 13px; font-family: {MONO}; font-variant-numeric: tabular-nums;">${(spendTotal || sel.task?.costUsd || 0).toFixed(2)}</div></div>
								{/if}
								{#if agentData?.agent?.branch}
									<div><div style="{KICKER} letter-spacing: 0.1em; color: rgba(146,153,170,0.6); margin-bottom: 3px; font-size: 10px;">Branch</div><div style="font-size: 13px; font-family: {MONO};">{agentData.agent.branch}</div></div>
								{/if}
								{#if agentData?.agent?.dir}
									<div><div style="{KICKER} letter-spacing: 0.1em; color: rgba(146,153,170,0.6); margin-bottom: 3px; font-size: 10px;">Where</div><div style="font-size: 13px; font-family: {MONO};">{agentData.agent.dir}</div></div>
								{/if}
							</div>
							{#if !traceRows.length}
								<p style="font-size: 13.5px; color: {MUT}; margin: 0;">No activity on record yet.</p>
							{/if}
							{#each traceRows as e, i (i)}
								<div style="display: flex; gap: 12px; align-items: baseline; padding: 3.5px 0;">
									<span style="flex: none; width: 62px; font-size: 11px; font-family: {MONO}; color: rgba(146,153,170,0.5); font-variant-numeric: tabular-nums;">{e.time}</span>
									<span style={traceStyle(e.k)}>{e.t}</span>
								</div>
							{/each}
							{#if sel.agent}
								<div style="display: flex; gap: 10px; margin-top: 18px; align-items: center;">
									<a href="/agent/{sel.agent}?mode=direct" class="vbtn" style="text-decoration: none;">Open the cockpit →</a>
									{#if agentData?.resume}
										<button class="vghost" onclick={() => navigator.clipboard?.writeText(`claude --resume ${agentData.resume}`)} title="copy the claude --resume command">copy resume</button>
									{/if}
								</div>
							{/if}
						</div>
					{/if}
				</div>

				<!-- composer -->
				<div
					class="v-composer"
					style="flex: none; padding: 14px 32px 18px; background: linear-gradient(to bottom, rgba(16,20,29,0), rgba(16,20,29,0.6)); box-shadow: 0 -1px 0 {HAIR};"
				>
					{#if reviseArm}
						<div style="max-width: 720px; margin-bottom: 6px; padding-left: 30px;">
							<span style="font-size: 11px; color: {AMBER}; font-weight: 500;">Revision mode — Vera reworks the handoff from your note. Esc to cancel.</span>
						</div>
					{/if}
					<div style="display: flex; align-items: center; gap: 12px; max-width: 720px;">
						<span style="display: flex;">{@render glyph(sel.owner === 'vera' ? 'vera' : sel.owner === 'done' ? 'done' : 'you', 20)}</span>
						<input
							bind:this={cmdEl}
							bind:value={draft}
							onkeydown={onDraftKey}
							placeholder={composerPh}
							disabled={!composerOn}
							style="flex: 1; background: {PANEL}; border: 0; border-radius: 12px; padding: 13px 17px; font: inherit; font-size: 15px; color: {INK}; outline: none; box-shadow: inset 0 0 0 1px {reviseArm ? 'rgba(232,187,105,0.4)' : 'rgba(242,238,231,0.07)'}; opacity: {composerOn ? 1 : 0.55};"
						/>
					</div>
				</div>
			{/if}
		</main>
	</div>
</div>

<style>
	.vx :global(::placeholder) {
		color: #5c6375;
	}
	.vrow:hover {
		background: rgba(242, 238, 231, 0.045) !important;
	}
	.vrow:focus-visible {
		outline: none;
		box-shadow: inset 0 0 0 1.5px rgba(242, 238, 231, 0.45) !important;
	}
	.vfile:hover {
		background: rgba(242, 238, 231, 0.04) !important;
	}
	.vx :global(a:hover) {
		color: #c4b5fd;
	}
	.vbtn {
		background: transparent;
		border: 0;
		box-shadow: inset 0 0 0 1px rgba(242, 238, 231, 0.14);
		color: #f2eee7;
		font: inherit;
		font-size: 13px;
		font-weight: 500;
		padding: 7px 14px;
		border-radius: 10px;
		cursor: pointer;
	}
	.vbtn:hover {
		background: rgba(242, 238, 231, 0.06);
	}
	.vcta {
		background: #f2eee7;
		color: #0b0d12;
		border: 0;
		font: inherit;
		font-size: 13.5px;
		font-weight: 600;
		padding: 9px 18px;
		border-radius: 10px;
		cursor: pointer;
	}
	.vcta:hover {
		background: #fffdf8;
	}
	.vcta:disabled,
	.vbtn:disabled,
	.vghost:disabled {
		opacity: 0.5;
		cursor: not-allowed;
	}
	.vghost {
		background: transparent;
		border: 0;
		color: #9299aa;
		font: inherit;
		font-size: 13px;
		padding: 6px 4px;
		cursor: pointer;
	}
	.vghost:hover {
		color: #f2eee7;
	}
	.vopt {
		text-align: left;
		background: #131722;
		border: 0;
		border-radius: 14px;
		padding: 14px 18px;
		cursor: pointer;
		font: inherit;
		color: inherit;
		box-shadow: inset 0 0 0 1px rgba(242, 238, 231, 0.06);
	}
	.vopt:hover {
		background: #1a2029;
	}
	.vx button:focus-visible,
	.vx input:focus-visible,
	.vx select:focus-visible,
	.vx a:focus-visible {
		outline: none;
		box-shadow: inset 0 0 0 1.5px rgba(167, 139, 250, 0.55) !important;
	}
	.vx *::-webkit-scrollbar {
		width: 8px;
		height: 8px;
	}
	.vx *::-webkit-scrollbar-thumb {
		background: rgba(146, 153, 170, 0.35);
		border-radius: 8px;
	}
	@media (max-width: 900px) {
		.vx aside {
			width: 232px !important;
		}
	}
	/* the phone layout: one pane at a time. The list is home; a picked
	   thread, the door, or the plan flow takes the screen; ← in the
	   header walks back. Media-query only — desktop never enters here. */
	.v-back {
		display: none;
		border: 0;
		background: transparent;
		color: #9299aa;
		font: inherit;
		font-size: 17px;
		line-height: 1;
		padding: 6px 8px 6px 0;
		cursor: pointer;
	}
	@media (max-width: 720px) {
		.v-head {
			padding: 0 12px !important;
		}
		.v-back.on {
			display: inline-flex;
		}
		.v-status {
			flex: 0 1 auto;
			min-width: 0;
			overflow: hidden;
			text-overflow: ellipsis;
			white-space: nowrap;
		}
		.vshell aside {
			width: 100% !important;
		}
		.vshell.m-main aside {
			display: none !important;
		}
		.vshell:not(.m-main) main {
			display: none !important;
		}
		.v-plan {
			padding: 20px 16px !important;
		}
		.v-door {
			padding: 24px 16px !important;
		}
		.v-thread-head {
			padding: 14px 16px 0 !important;
		}
		.v-tabs {
			overflow-x: auto;
			gap: 18px !important;
		}
		.v-thread-body {
			padding: 16px 16px 20px !important;
		}
		.v-composer {
			padding: 10px 12px 14px !important;
		}
	}
</style>
