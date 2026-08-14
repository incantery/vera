<script>
	// The direct-drive cockpit, from the "Direct Mode" design: one
	// event timeline on a spine — human turns, reasoning, tool rows
	// that expand to their real results, diffs from the Edit calls'
	// own inputs — with a turns rail, a slide-in inspector, and a
	// keyboard layer. Everything rendered is real; what the headless
	// rail cannot do (mid-turn gates, choice chips, revert) is absent,
	// not faked.
	let {
		data,
		perm,
		setPerm,
		setMode,
		pendingSecs,
		interrupting,
		streaming,
		shelfOpen,
		onshelf,
		onsend,
		oninterrupt,
		oncompact,
		compacting
	} = $props();

	import { uploadImage, uploadUrl, imageParts } from './state.svelte.js';
	import Markdown from './Markdown.svelte';

	const EASE = 'cubic-bezier(0.2, 0.8, 0.25, 1)';
	const MONO = "'JetBrains Mono', ui-monospace, Menlo, monospace";

	let text = $state('');
	let showReason = $state(true);
	let rail = $state(true);
	let inspector = $state(false);
	let keysOpen = $state(false);
	let sel = $state(-1);
	let open = $state({});
	let chatEl = $state(null);
	let inputEl = $state(null);
	let nearBottom = true;

	const agentId = $derived(data?.agent?.id);
	const busy = $derived(data?.pending?.status === 'thinking' || data?.pending?.status === 'phrasing');
	const history = $derived(data?.history ?? []);

	function k(n) {
		if (!n) return '0';
		if (n >= 100000) return `${Math.round(n / 1000)}k`;
		if (n >= 1000) return `${(n / 1000).toFixed(1)}k`;
		return String(n);
	}

	// dur: turn wall-clock, the transcript's own timestamps.
	function dur(s) {
		if (s >= 3600) return `${Math.floor(s / 3600)}h ${Math.round((s % 3600) / 60)}m`;
		if (s >= 60) return `${Math.floor(s / 60)}m ${s % 60}s`;
		return `${s}s`;
	}

	// ── the event timeline ─────────────────────────────────────────
	// One transcript message fans out into typed events: a user msg is
	// a divider + a human event; an assistant msg is its reasoning,
	// its steps (rows, diffs, errors), then its prose.
	const KIND = {
		human: { color: 'var(--color-accent-400)', h: 10 },
		prose: { color: 'var(--color-neutral-600)', h: 8 },
		reason: { color: 'var(--color-neutral-800)', h: 5 },
		read: { color: 'var(--ev-rd-mid)', h: 5 },
		bash: { color: 'var(--ev-sh-mid)', h: 5 },
		diff: { color: 'var(--ev-add-mid)', h: 8 },
		error: { color: 'var(--ev-del-mid)', h: 10 },
		live: { color: 'var(--color-accent)', h: 10 },
		divider: { color: 'transparent', h: 3 },
		note: { color: 'var(--color-neutral-800)', h: 3 }
	};

	// verbTone/dot by the tool's nature: shell amber, edits green,
	// reads blue — the design's event palette.
	function toolKind(tool) {
		const t = (tool ?? '').toLowerCase();
		if (t === 'bash' || t === 'bashoutput' || t === 'killshell') return 'bash';
		if (t === 'edit' || t === 'write' || t === 'notebookedit') return 'diff';
		return 'read';
	}
	const VERB_TONE = {
		bash: 'var(--ev-sh)',
		diff: 'var(--ev-add)',
		read: 'var(--ev-rd-mid)',
		error: 'var(--ev-del)'
	};

	const events = $derived.by(() => {
		const out = [];
		let turn = 0;
		history.forEach((m, mi) => {
			if (m.role === 'user') {
				turn++;
				const reply = history[mi + 1]?.role === 'assistant' ? history[mi + 1] : null;
				const bits = [];
				if (reply?.ctx) bits.push(`${k(reply.ctx)} ctx`);
				if (reply?.tools) bits.push(`${reply.tools} tool ${reply.tools === 1 ? 'call' : 'calls'}`);
				if (reply?.secs) bits.push(dur(reply.secs));
				out.push({ kind: 'divider', id: `d${mi}`, turn, text: `turn ${turn}${bits.length ? ' · ' + bits.join(' · ') : ''}` });
				const parts = imageParts(m.text);
				out.push({
					kind: 'human', id: `m${mi}`, turn,
					text: m.rough || parts.text, names: parts.names,
					rough: m.rough && m.rough !== m.text ? m.text : null,
					via: !!m.rough
				});
			} else {
				(m.think ?? []).forEach((th, ti) => out.push({ kind: 'reason', id: `m${mi}t${ti}`, turn, text: th }));
				(m.steps ?? []).forEach((st, si) => {
					const id = `m${mi}s${si}`;
					if (st.diff) out.push({ kind: 'diff', id, turn, st });
					else if (st.err) out.push({ kind: 'error', id, turn, st });
					else out.push({ kind: toolKind(st.tool), id, turn, st, row: true });
				});
				if ((m.tools ?? 0) > (m.steps?.length ?? 0))
					out.push({ kind: 'note', id: `m${mi}n`, turn, text: `… and ${m.tools - m.steps.length} more tool calls` });
				if (m.text) out.push({ kind: 'prose', id: `m${mi}p`, turn, text: m.text });
			}
		});
		const p = data?.pending;
		if (p) {
			if (p.status === 'failed') {
				const parts = imageParts(p.text);
				out.push({ kind: 'human', id: 'pend', turn, text: parts.text, names: (p.images ?? []).map((x) => x.split('/').pop()), dim: true });
				out.push({
					kind: 'note', id: 'pendfail', turn,
					text: /interrupt/i.test(p.error ?? '') ? 'you interrupted · whatever already landed stays in the transcript' : `did not land — ${p.error}`,
					tone: /interrupt/i.test(p.error ?? '') ? undefined : 'var(--ev-del)'
				});
			} else {
				const parts = imageParts(p.sent && p.direct ? p.sent : p.text);
				out.push({ kind: 'human', id: 'pend', turn, text: parts.text, names: (p.images ?? []).map((x) => x.split('/').pop()), dim: true });
				out.push({
					kind: 'live', id: 'pendlive', turn, live: true,
					verb: data?.agent?.state === 'working' && data?.agent?.tool ? data.agent.tool.toLowerCase() : p.status === 'phrasing' ? 'rook' : 'claude',
					target: data?.agent?.state === 'working' && data?.agent?.toolDetail ? data.agent.toolDetail : p.status === 'phrasing' ? 'phrasing your message' : 'working'
				});
			}
		}
		return out;
	});

	// Fresh events animate in; everything present at first render is
	// history and stays still.
	let fresh = $state({});
	let seen = null;
	$effect(() => {
		const ids = events.map((e) => e.id);
		if (!seen) {
			seen = new Set(ids);
			return;
		}
		const add = ids.filter((id) => !seen.has(id));
		if (!add.length) return;
		add.forEach((id) => {
			seen.add(id);
			fresh[id] = true;
		});
		setTimeout(() => add.forEach((id) => delete fresh[id]), 950);
	});

	// ── the turns rail ─────────────────────────────────────────────
	const turns = $derived.by(() => {
		const out = [];
		history.forEach((m, mi) => {
			if (m.role !== 'user') return;
			const reply = history[mi + 1]?.role === 'assistant' ? history[mi + 1] : null;
			const bits = [m.rough ? 'you · via rook' : 'you'];
			if (reply?.ctx) bits.push(`${k(reply.ctx)} ctx`);
			if (reply?.tools) bits.push(`${reply.tools} events`);
			if (reply?.secs) bits.push(dur(reply.secs));
			out.push({ n: out.length + 1, id: `m${mi}`, title: (m.rough || imageParts(m.text).text).slice(0, 64), sub: bits.join(' · ') });
		});
		return out;
	});
	const curTurn = $derived(turns.length);
	const ticks = $derived(events.filter((e) => e.turn === curTurn && e.kind !== 'divider'));

	function tickLabel(e) {
		if (e.kind === 'human') return e.text;
		if (e.st) return e.st.detail || e.st.tool;
		return e.target || e.text || e.kind;
	}

	// ── phase: the one word the header pill and composer agree on ──
	const phase = $derived.by(() => {
		const p = data?.pending;
		if (p && (p.status === 'thinking' || p.status === 'phrasing')) return 'working';
		if (p?.status === 'failed' && /interrupt/i.test(p.error ?? '')) return 'interrupted';
		if (data?.agent?.state === 'working') return 'working';
		if (data?.agent?.state === 'needs you' || data?.agent?.state === 'blocked?') return 'waiting';
		return 'idle';
	});
	const pill = $derived(
		{
			working: { text: `working · ${pendingSecs}s`, tone: 'var(--color-accent-100)', bg: 'var(--color-accent-900)', edge: 'var(--color-accent-700)' },
			waiting: { text: 'needs you', tone: 'var(--color-accent-100)', bg: 'var(--color-accent-800)', edge: 'var(--color-accent-500)' },
			idle: { text: `idle · ${data?.agent?.age ?? ''}`, tone: 'var(--color-neutral-400)', bg: 'transparent', edge: 'var(--color-neutral-800)' },
			interrupted: { text: 'interrupted', tone: 'var(--color-neutral-300)', bg: 'transparent', edge: 'var(--color-neutral-700)' }
		}[phase]
	);
	const statusLine = $derived(
		{
			working: data?.queue?.length ? 'queued — lands when this turn ends' : 'claude is working · your message will queue',
			waiting: 'claude is waiting on you · reply below',
			idle: 'idle · your turn',
			interrupted: 'interrupted · resume or redirect'
		}[phase]
	);
	const statusTone = $derived(
		{ working: 'var(--color-accent-300)', waiting: 'var(--color-accent-200)', idle: 'var(--color-neutral-600)', interrupted: 'var(--color-neutral-400)' }[phase]
	);
	const composerEdge = $derived(
		phase === 'waiting' ? 'var(--color-accent-500)' : phase === 'working' ? 'var(--color-accent-800)' : 'var(--color-divider)'
	);

	// ── budgets: the subscription's own rate windows ───────────────
	const budgets = $derived.by(() => {
		const u = data?.usage;
		if (!u) return [];
		const tone = (pct) =>
			pct >= 90 ? 'var(--ev-del-mid)' : pct >= 70 ? 'var(--ev-sh-mid)' : 'var(--ev-add-mid)';
		const mk = (name, pct, resets) => ({
			name, pct: `${Math.min(pct ?? 0, 100)}%`, raw: pct ?? 0, color: tone(pct ?? 0),
			tone: (pct ?? 0) >= 70 ? tone(pct ?? 0) : 'var(--color-neutral-400)',
			title: resets ? `resets ${resets}` : ''
		});
		const out = [mk('session', u.sessionPct, u.sessionResets), mk('week', u.weekAllPct, u.weekAllResets)];
		if (u.weekModelName) out.push(mk(u.weekModelName.toLowerCase(), u.weekModelPct, u.weekModelResets));
		return out;
	});

	// ── inspector data ─────────────────────────────────────────────
	const ctxSegs = $derived.by(() => {
		const c = data?.ctx;
		if (!c?.tokens) return [];
		const w = c.window || 200000;
		const seg = (name, tokens, color) => ({ name, tokens, w: `${Math.max(0.4, (tokens / w) * 100)}%`, color });
		return [
			seg('cache read', c.cacheRead ?? 0, 'var(--ev-rd-fill)'),
			seg('cache write', c.cacheWrite ?? 0, 'var(--ev-rd-mid)'),
			seg('fresh in', c.in ?? 0, 'var(--ev-sh)'),
			seg('out', c.out ?? 0, 'var(--ev-add)')
		].filter((s) => s.tokens > 0);
	});
	const permRows = $derived([
		{ key: 'read', name: 'reads — read, grep, ls', on: true, fixed: true, state: 'granted' },
		{
			key: 'edit', name: 'edits + build/test (go, npm, make)',
			on: perm === 'edit' || perm === 'all',
			state: perm === 'edit' || perm === 'all' ? 'auto' : 'ask never · refused'
		},
		{
			key: 'all', name: 'everything — no permission gate',
			on: perm === 'all', danger: true,
			state: perm === 'all' ? 'NO GATE' : 'off'
		}
	]);
	function togglePerm(row) {
		if (row.fixed) return;
		if (row.key === 'edit') setPerm(perm === 'edit' || perm === 'all' ? 'read' : 'edit');
		else setPerm(perm === 'all' ? 'edit' : 'all');
	}
	const spendTotal = $derived((data?.spend?.claudeUsd ?? 0) + (data?.spend?.judgeUsd ?? 0));

	// ── attachments ────────────────────────────────────────────────
	let attachments = $state([]);
	let uploadErr = $state('');
	async function onPaste(e) {
		const items = [...(e.clipboardData?.items ?? [])].filter((it) => it.type.startsWith('image/'));
		if (!items.length) return;
		e.preventDefault();
		uploadErr = '';
		for (const it of items) {
			const file = it.getAsFile();
			if (!file) continue;
			try {
				const ans = await uploadImage(agentId, file);
				attachments.push({ ...ans, preview: URL.createObjectURL(file) });
			} catch (err) {
				uploadErr = err.message;
			}
		}
	}
	function dropAttachment(i) {
		URL.revokeObjectURL(attachments[i]?.preview);
		attachments.splice(i, 1);
	}

	// ── diffs and row bodies ───────────────────────────────────────
	function diffLines(d) {
		const out = [];
		if (d.old) for (const l of d.old.split('\n')) out.push({ sign: '−', text: l, del: true });
		if (d.new) for (const l of d.new.split('\n')) out.push({ sign: '+', text: l });
		return out;
	}
	function diffCounts(d) {
		const c = (s) => (s ? s.split('\n').length : 0);
		return { add: c(d.new), del: c(d.old) };
	}
	function rowMeta(st) {
		if (st.err) return 'failed';
		if (st.lines) return `${st.lines} ${st.lines === 1 ? 'line' : 'lines'}`;
		return '';
	}
	function bodyTone(t) {
		if (/PASS|^ok |installed|exit 0/.test(t)) return 'var(--ev-add)';
		if (/FAIL|warn|not found|error/i.test(t)) return 'var(--ev-sh)';
		return 'var(--color-neutral-400)';
	}

	// ── follow, selection, keyboard ────────────────────────────────
	const followKey = $derived(events.length + ':' + (data?.pending?.status ?? ''));
	$effect(() => {
		followKey;
		if (chatEl && nearBottom) chatEl.scrollTop = chatEl.scrollHeight;
	});
	function onScroll() {
		if (!chatEl) return;
		nearBottom = chatEl.scrollHeight - chatEl.scrollTop - chatEl.clientHeight < 160;
	}
	function jumpTo(id, select = false) {
		document.getElementById(`ev-${id}`)?.scrollIntoView({ behavior: 'smooth', block: 'center' });
		if (select) sel = events.findIndex((e) => e.id === id);
	}
	function openable(e) {
		return e.kind === 'reason' || (e.row && e.st?.out);
	}
	function toggleOpen(e) {
		if (!openable(e)) return;
		open[e.id] = !open[e.id];
	}
	function move(d) {
		if (!events.length) return;
		sel = Math.max(0, Math.min(events.length - 1, (sel < 0 ? events.length - 1 : sel) + d));
		const e = events[sel];
		if (e) document.getElementById(`ev-${e.id}`)?.scrollIntoView({ block: 'nearest' });
	}
	function jumpPrompt(d) {
		const at = sel < 0 ? events.length : sel;
		const idx = events
			.map((e, i) => ({ e, i }))
			.filter(({ e }) => e.kind === 'human')
			.map(({ i }) => i);
		const next = d > 0 ? idx.find((i) => i > at) : [...idx].reverse().find((i) => i < at);
		if (next !== undefined) {
			sel = next;
			document.getElementById(`ev-${events[next].id}`)?.scrollIntoView({ behavior: 'smooth', block: 'center' });
		}
	}
	function onGlobalKey(e) {
		if ((e.metaKey || e.ctrlKey) && (e.key === 'i' || e.key === 'I')) {
			e.preventDefault();
			inspector = !inspector;
			return;
		}
		const typing = e.target && (e.target.tagName === 'TEXTAREA' || e.target.tagName === 'INPUT');
		if (e.key === 'Escape') {
			if (keysOpen) {
				keysOpen = false;
				return;
			}
			if (busy) oninterrupt();
			return;
		}
		if (typing) return;
		if (e.key === 'i') {
			e.preventDefault();
			inputEl?.focus();
			return;
		}
		if (e.key === '?') {
			keysOpen = !keysOpen;
			return;
		}
		if (e.key === '\\') {
			rail = !rail;
			return;
		}
		if (e.key === 'j') return move(1);
		if (e.key === 'k') return move(-1);
		if (e.key === 'J') return jumpPrompt(1);
		if (e.key === 'K') return jumpPrompt(-1);
		if (e.key === 'o') {
			const cur = events[sel];
			if (cur) toggleOpen(cur);
		}
	}
	$effect(() => {
		const h = (e) => onGlobalKey(e);
		window.addEventListener('keydown', h);
		return () => window.removeEventListener('keydown', h);
	});

	const KEYMAP = [
		{ k: 'j / k', d: 'move through events' },
		{ k: '⇧J / ⇧K', d: 'jump between your prompts' },
		{ k: 'o', d: 'expand or collapse the selected event' },
		{ k: 'i', d: 'focus the composer' },
		{ k: 'esc', d: 'interrupt the current turn' },
		{ k: '⏎', d: 'send (or queue while working)' },
		{ k: '\\', d: 'collapse the turns rail' },
		{ k: '⌘I', d: 'context, permissions, spend' },
		{ k: '/', d: 'slash commands ride verbatim (/compact, /clear)' },
		{ k: '⌘V', d: 'paste an image into the composer' }
	];

	function submit(e) {
		e?.preventDefault();
		let t = text.trim();
		if (!t && !attachments.length) return;
		if (!t) t = attachments.length === 1 ? 'Look at the attached image.' : 'Look at the attached images.';
		const images = attachments.map((a) => a.path);
		attachments.forEach((a) => URL.revokeObjectURL(a.preview));
		attachments = [];
		text = '';
		nearBottom = true;
		onsend(t, images);
	}
	function onKeydown(e) {
		if (e.key === 'Enter' && !e.shiftKey) {
			e.preventDefault();
			submit();
		}
		if (e.key === 'Escape' && busy) oninterrupt();
	}
</script>

<div
	class="nk"
	style="flex: 1; min-height: 0; display: flex; flex-direction: column; background: var(--color-bg); color: var(--color-text); font-family: var(--font-body); overflow: hidden; --ev-add: oklch(0.86 0.10 155); --ev-add-mid: oklch(0.72 0.13 155); --ev-add-fill: oklch(0.30 0.045 155); --ev-del: oklch(0.85 0.10 22); --ev-del-mid: oklch(0.67 0.15 22); --ev-del-fill: oklch(0.29 0.06 22); --ev-del-edge: oklch(0.45 0.11 22); --ev-sh: oklch(0.88 0.09 82); --ev-sh-mid: oklch(0.75 0.12 82); --ev-sh-fill: oklch(0.31 0.05 82); --ev-rd: oklch(0.86 0.07 228); --ev-rd-mid: oklch(0.70 0.10 228); --ev-rd-fill: oklch(0.30 0.045 228);"
>
	<!-- header -->
	<header style="flex: 0 0 auto; display: flex; align-items: center; gap: 12px; padding: 9px 16px 9px 14px; border-bottom: 1px solid var(--color-divider);">
		<a href="/" style="font-size: 13px; line-height: 1; padding: 4px 6px; color: var(--color-neutral-600); border-radius: var(--radius-sm);" class="hover:text-[var(--color-neutral-200)]!">←</a>
		<div style="flex: 1 1 auto; display: flex; align-items: baseline; gap: 9px; min-width: 12ch; overflow: hidden;">
			<span style="flex: 0 1 auto; min-width: 12ch; font-family: {MONO}; font-size: 13px; font-weight: 500; color: var(--color-neutral-100); white-space: nowrap; overflow: hidden; text-overflow: ellipsis;">{data?.agent?.title ?? '…'}</span>
			<span style="flex: 0 1 auto; min-width: 0; font-family: {MONO}; font-size: 11.5px; color: var(--color-neutral-600); white-space: nowrap; overflow: hidden; text-overflow: ellipsis;">{data?.agent?.dir}{data?.agent?.branch ? ` · ${data.agent.branch}` : ''}{data?.resume ? ` · ${data.resume.slice(0, 8)}` : ''}</span>
		</div>
		<div style="flex: 0 0 auto; display: flex; align-items: center; gap: 2px; padding: 2px; border: 1px solid var(--color-neutral-800); border-radius: var(--radius-sm);" title="membrane: rook phrases and digests · direct: you and claude, nothing between">
			<button onclick={() => setMode('membrane')} style="font: inherit; font-family: {MONO}; font-size: 11px; padding: 3px 8px; cursor: pointer; border: none; background: transparent; color: var(--color-neutral-600); border-radius: 3px;" class="hover:text-[var(--color-neutral-300)]!">membrane</button>
			<span style="font-family: {MONO}; font-size: 11px; padding: 3px 8px; color: var(--color-accent-100); background: var(--color-accent-800); border-radius: 3px;">direct</span>
		</div>
		<a href="/" style="flex: 0 0 auto; white-space: nowrap; font-family: {MONO}; font-size: 11.5px; padding: 4px 7px; border: 1px solid transparent; border-radius: var(--radius-sm); color: var(--color-neutral-400);" class="hover:border-[var(--color-neutral-800)]! hover:text-[var(--color-neutral-100)]!">board</a>
		<button onclick={onshelf} style="flex: 0 0 auto; white-space: nowrap; font: inherit; font-family: {MONO}; font-size: 11.5px; padding: 4px 7px; cursor: pointer; border: 1px solid {shelfOpen ? 'var(--color-neutral-800)' : 'transparent'}; border-radius: var(--radius-sm); background: transparent; color: var(--color-neutral-400);" class="hover:border-[var(--color-neutral-800)]! hover:text-[var(--color-neutral-100)]!">
			artifacts{#if data?.artifacts}&nbsp;<span style="color: var(--color-accent-300);">({data.artifacts})</span>{/if}
		</button>
		<button
			onclick={() => (inspector = !inspector)}
			style="flex: 0 0 auto; white-space: nowrap; font: inherit; font-family: {MONO}; font-size: 11px; padding: 4px 8px; cursor: pointer; border-radius: var(--radius-sm); border: 1px solid {inspector ? 'var(--color-neutral-800)' : 'transparent'}; background: transparent; color: var(--color-neutral-500);"
			class="hover:border-[var(--color-neutral-800)]! hover:text-[var(--color-neutral-200)]!"
			title="context, permissions, working tree, spend (claude turns at API rates + rook's own calls)"
		>
			{data?.agent?.ctxPct ? `ctx ${data.agent.ctxPct}%` : 'ctx —'}{spendTotal ? ` · $${spendTotal.toFixed(2)}` : ''} · ⌘I
		</button>
		{#if streaming}
			<span style="flex: 0 0 auto; font-family: {MONO}; font-size: 10px; color: var(--color-accent-300);" title="the Connect stream is live — updates land as the transcript moves, no polling">⚡ live</span>
		{/if}
		<div style="flex: 0 0 auto; display: flex; align-items: center; gap: 8px; padding: 4px 10px 4px 8px; border-radius: 99px; border: 1px solid {pill.edge}; background: {pill.bg};">
			<span style="position: relative; width: 9px; height: 9px; flex: 0 0 auto;">
				{#if phase === 'working'}
					<span style="position: absolute; inset: 0; border-radius: 99px; border: 1.5px solid var(--color-accent-700); border-top-color: var(--color-accent-200); animation: rk-spin 0.9s linear infinite;"></span>
				{:else if phase === 'waiting'}
					<span style="position: absolute; inset: 1px; border-radius: 99px; background: var(--color-accent); animation: rk-glow 1.7s ease-in-out infinite;"></span>
				{:else}
					<span style="position: absolute; inset: 2px; border-radius: 99px; background: var(--color-neutral-600);"></span>
				{/if}
			</span>
			<span style="font-family: {MONO}; font-size: 11.5px; color: {pill.tone}; white-space: nowrap;">{pill.text}</span>
		</div>
	</header>

	<!-- budgets: the subscription's own windows; roost inherits them -->
	{#if budgets.length}
		<div style="flex: 0 0 auto; display: flex; align-items: center; gap: 18px; padding: 6px 16px; border-bottom: 1px solid var(--color-divider);">
			{#each budgets as b (b.name)}
				<div style="display: flex; align-items: center; gap: 8px;" title={b.title}>
					<span style="font-family: {MONO}; font-size: 10.5px; color: var(--color-neutral-600); white-space: nowrap;">{b.name}</span>
					<span style="position: relative; width: 56px; height: 3px; border-radius: 99px; background: var(--color-neutral-900); overflow: hidden;">
						<span style="position: absolute; inset: 0 auto 0 0; width: {b.pct}; background: {b.color};"></span>
					</span>
					<span style="font-family: {MONO}; font-size: 10.5px; color: {b.tone}; font-variant-numeric: tabular-nums;">{b.raw}%</span>
				</div>
			{/each}
			<div style="flex: 1;"></div>
			<span style="font-family: {MONO}; font-size: 10.5px; color: var(--color-neutral-600);" class="hidden sm:inline">budgets · claude stops at 100%</span>
		</div>
	{/if}

	<div style="flex: 1; min-height: 0; display: flex; position: relative;">
		<!-- turns rail -->
		<nav
			style="flex: 0 0 auto; width: {rail ? '228px' : '44px'}; border-right: 1px solid var(--color-divider); overflow: hidden; transition: width 200ms {EASE}; display: none; flex-direction: column;"
			class="lg:flex!"
		>
			<div style="display: flex; align-items: center; gap: 7px; padding: 11px 12px 9px; white-space: nowrap;">
				<button onclick={() => (rail = !rail)} style="font: inherit; font-family: {MONO}; font-size: 10.5px; letter-spacing: 0.08em; padding: 0; cursor: pointer; border: none; background: transparent; color: var(--color-neutral-600);" class="hover:text-[var(--color-neutral-300)]!">
					{rail ? `TURNS · ${turns.length} · \\` : '\\'}
				</button>
			</div>
			<div style="flex: 1; min-height: 0; overflow-y: auto; padding: 0 0 16px;">
				{#each turns as t (t.id)}
					<div style="white-space: nowrap;">
						<button
							onclick={() => jumpTo(t.id, true)}
							style="width: 100%; text-align: left; font: inherit; color: inherit; display: grid; grid-template-columns: 22px minmax(0, 1fr); gap: 8px; padding: 5px 12px 5px 10px; cursor: pointer; border: none; background: {events[sel]?.turn === t.n ? 'var(--color-neutral-900)' : 'transparent'}; border-left: 2px solid {events[sel]?.turn === t.n ? 'var(--color-accent-400)' : 'transparent'};"
							class="hover:bg-[var(--color-neutral-900)]!"
						>
							<span style="font-family: {MONO}; font-size: 11px; color: {t.n === curTurn ? 'var(--color-accent-300)' : 'var(--color-neutral-700)'}; text-align: right; font-variant-numeric: tabular-nums;">{t.n}</span>
							<span style="display: flex; flex-direction: column; gap: 2px; min-width: 0;">
								<span style="font-size: 12px; line-height: 1.35; color: var(--color-neutral-400); overflow: hidden; text-overflow: ellipsis;">{t.title}</span>
								<span style="font-family: {MONO}; font-size: 10px; color: var(--color-neutral-600); overflow: hidden; text-overflow: ellipsis;">{t.sub}</span>
							</span>
						</button>
						{#if t.n === curTurn && rail}
							<div style="padding: 2px 0 6px;">
								{#each ticks as e (e.id)}
									<button
										onclick={() => jumpTo(e.id, true)}
										style="width: 100%; text-align: left; font: inherit; color: inherit; border: none; background: transparent; display: flex; align-items: center; gap: 8px; padding: 1.5px 12px 1.5px 34px; cursor: pointer;"
										class="hover:bg-[var(--color-neutral-900)]!"
									>
										<span style="flex: 0 0 auto; width: 3px; height: {KIND[e.kind]?.h ?? 5}px; border-radius: 2px; background: {e.live ? 'var(--color-accent)' : (KIND[e.kind]?.color ?? 'var(--color-neutral-600)')};"></span>
										<span style="font-family: {MONO}; font-size: 10.5px; color: {events[sel]?.id === e.id ? 'var(--color-neutral-100)' : 'var(--color-neutral-600)'}; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;">{tickLabel(e)}</span>
									</button>
								{/each}
							</div>
						{/if}
					</div>
				{/each}
			</div>
		</nav>

		<!-- the stream -->
		<main style="flex: 1; min-width: 0; display: flex; flex-direction: column;">
			<div bind:this={chatEl} onscroll={onScroll} style="flex: 1; min-height: 0; overflow-y: auto; overflow-x: hidden;">
				<div style="max-width: 940px; margin: 0 auto; padding: 22px 28px 30px;">
					{#each events as e, i (e.id)}
						{#if e.kind !== 'reason' || showReason}
							{@const kd = KIND[e.kind] ?? KIND.prose}
							{@const selHere = sel === i}
							<div id="ev-{e.id}" style="display: grid; grid-template-columns: 30px minmax(0, 1fr); column-gap: 14px; position: relative; animation: {fresh[e.id] ? `rk-enter 240ms ${EASE} both` : 'none'};">
								<!-- spine + glyph -->
								<div style="position: relative;">
									<div style="position: absolute; left: 11px; top: 0; bottom: 0; width: 1px; background: {e.kind === 'human' ? 'var(--color-neutral-800)' : 'var(--color-neutral-900)'};"></div>
									{#if e.live}
										<div style="position: absolute; left: 10px; top: 0; width: 3px; height: 26px; border-radius: 2px; background: linear-gradient(to bottom, transparent, var(--color-accent), transparent); animation: rk-travel 1.9s linear infinite;"></div>
									{/if}
									<div style="position: absolute; left: 6px; top: {e.kind === 'human' ? '13px' : '9px'}; width: 11px; height: 11px; display: flex; align-items: center; justify-content: center;">
										{#if e.live}
											<span style="position: absolute; inset: 0; border-radius: 99px; border: 1.5px solid var(--color-accent-800); border-top-color: var(--color-accent-300); animation: rk-spin 0.85s linear infinite;"></span>
										{:else if e.kind !== 'divider' && e.kind !== 'note'}
											<span style="width: {e.kind === 'human' || e.kind === 'error' ? 7 : 5}px; height: {e.kind === 'human' || e.kind === 'error' ? 7 : 5}px; border-radius: {e.kind === 'diff' ? '1px' : '99px'}; background: {e.kind === 'human' ? 'var(--color-accent-400)' : e.kind === 'error' ? 'var(--ev-del-mid)' : kd.color}; border: 1px solid {selHere ? 'var(--color-accent-200)' : 'transparent'};"></span>
										{/if}
									</div>
								</div>

								<div style="padding-bottom: {e.kind === 'divider' ? '6px' : e.kind === 'human' ? '16px' : '13px'}; min-width: 0;">
									{#if e.kind === 'divider'}
										<div style="display: flex; align-items: center; gap: 10px; padding: 9px 0 5px;">
											<span style="font-family: {MONO}; font-size: 10.5px; letter-spacing: 0.07em; color: var(--color-neutral-600); white-space: nowrap;">{e.text}</span>
											<div style="flex: 1; height: 1px; background: linear-gradient(to right, var(--color-neutral-800), transparent);"></div>
										</div>
									{:else if e.kind === 'human'}
										<div style="margin: 6px 0 4px; padding: 2px 0 2px 13px; border-left: 2px solid var(--color-accent-500); max-width: 64ch; {e.dim ? 'opacity: 0.8;' : ''}">
											<div style="font-family: {MONO}; font-size: 10px; letter-spacing: 0.1em; color: var(--color-accent-400); margin-bottom: 5px;">{e.via ? 'YOU · VIA ROOK' : 'YOU'}</div>
											<div style="font-size: 15px; line-height: 1.55; color: var(--color-neutral-100); white-space: pre-wrap; text-wrap: pretty;">{e.text}</div>
											{#if e.names?.length}
												<div style="display: flex; gap: 8px; flex-wrap: wrap; margin-top: 6px;">
													{#each e.names as nm (nm)}
														<a href={uploadUrl(agentId, nm)} target="_blank" rel="noreferrer">
															<img src={uploadUrl(agentId, nm)} alt="attachment" style="max-height: 120px; max-width: 220px; border-radius: var(--radius-md); border: 1px solid var(--color-neutral-800);" />
														</a>
													{/each}
												</div>
											{/if}
											{#if e.rough}
												<details style="margin-top: 4px;">
													<summary style="cursor: pointer; font-family: {MONO}; font-size: 10.5px; color: var(--color-neutral-600);">what rook sent</summary>
													<div style="margin-top: 4px; font-size: 12.5px; line-height: 1.55; color: var(--color-neutral-500); white-space: pre-wrap;">{e.rough}</div>
												</details>
											{/if}
										</div>
									{:else if e.kind === 'prose'}
										<div class="md" style="max-width: 66ch; font-size: 14.5px; line-height: 1.68; color: var(--color-neutral-200); text-wrap: pretty;">
											<Markdown text={e.text} />
										</div>
									{:else if e.kind === 'reason'}
										<button onclick={() => toggleOpen(e)} style="display: inline-flex; align-items: center; gap: 8px; cursor: pointer; padding: 3px 0; font: inherit; border: none; background: transparent; color: inherit;">
											<span style="font-family: {MONO}; font-size: 11.5px; color: var(--color-neutral-600);">{open[e.id] ? '▾' : '▸'} reasoning</span>
										</button>
										{#if open[e.id]}
											<div style="max-width: 62ch; margin: 4px 0 2px; font-size: 13px; line-height: 1.65; color: var(--color-neutral-500); font-style: italic; text-wrap: pretty; animation: rk-enter 180ms {EASE} both;">{e.text}</div>
										{/if}
									{:else if e.kind === 'error'}
										<div style="margin: 4px 0 2px; border: 1px solid var(--ev-del-edge); border-left: 3px solid var(--ev-del-mid); border-radius: var(--radius-md); background: var(--ev-del-fill); overflow: hidden;">
											<div style="display: flex; align-items: center; gap: 10px; padding: 8px 11px;">
												<span style="font-family: {MONO}; font-size: 10px; letter-spacing: 0.12em; color: var(--color-bg); background: var(--ev-del); padding: 2px 5px; border-radius: 3px;">FAILED</span>
												<span style="font-family: {MONO}; font-size: 12.5px; color: var(--color-neutral-100); overflow: hidden; text-overflow: ellipsis; white-space: nowrap;">{e.st.detail || e.st.tool}</span>
												<div style="flex: 1;"></div>
												<span style="font-family: {MONO}; font-size: 11px; color: var(--ev-del);">{e.st.tool.toLowerCase()}</span>
											</div>
											{#if e.st.out}
												<div style="padding: 0 11px 9px;">
													<div style="font-family: {MONO}; font-size: 12px; line-height: 1.6; color: var(--ev-del); white-space: pre-wrap; word-break: break-word;">{e.st.out}</div>
												</div>
											{/if}
										</div>
									{:else if e.kind === 'diff'}
										{@const c = diffCounts(e.st.diff)}
										<div style="margin: 4px 0 2px; border: 1px solid var(--color-neutral-800); border-radius: var(--radius-md); background: var(--color-surface); overflow: hidden;">
											<div style="display: flex; align-items: center; gap: 10px; padding: 7px 10px; border-bottom: 1px solid var(--color-divider);">
												<span style="font-family: {MONO}; font-size: 11px; letter-spacing: 0.04em; color: var(--ev-add);">{e.st.tool.toLowerCase()}</span>
												<span style="font-family: {MONO}; font-size: 12.5px; color: var(--color-neutral-100); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; direction: rtl;">{e.st.diff.file}</span>
												<div style="flex: 1;"></div>
												{#if e.st.diff.all}<span style="font-family: {MONO}; font-size: 10.5px; color: var(--color-neutral-600);">replace all</span>{/if}
												<span style="font-family: {MONO}; font-size: 11px; color: var(--ev-add-mid); font-variant-numeric: tabular-nums;">+{c.add}</span>
												<span style="font-family: {MONO}; font-size: 11px; color: var(--ev-del-mid); font-variant-numeric: tabular-nums;">−{c.del}</span>
											</div>
											<div style="background: var(--color-bg); padding: 7px 0; max-height: 280px; overflow-y: auto;">
												{#each diffLines(e.st.diff) as l, li (li)}
													<div style="display: grid; grid-template-columns: 12px minmax(0, 1fr); gap: 6px; padding: 0.5px 10px; background: {l.del ? 'var(--ev-del-fill)' : 'var(--ev-add-fill)'}; animation: {fresh[e.id] ? `rk-wipe 260ms ${EASE} ${li * 14}ms both` : 'none'};">
														<span style="font-family: {MONO}; font-size: 12px; color: {l.del ? 'var(--ev-del)' : 'var(--ev-add)'};">{l.sign}</span>
														<span style="font-family: {MONO}; font-size: 12px; line-height: 1.62; color: {l.del ? 'var(--ev-del)' : 'var(--ev-add)'}; white-space: pre-wrap; word-break: break-word;">{l.text}</span>
													</div>
												{/each}
											</div>
										</div>
									{:else if e.kind === 'note'}
										<div style="font-size: 11.5px; color: {e.tone ?? 'var(--color-neutral-600)'}; padding: 3px 0;">{e.text}</div>
									{:else}
										<!-- a tool row: read/bash/live -->
										<div style="border-radius: var(--radius-sm); background: {selHere ? 'var(--color-neutral-900)' : 'transparent'}; position: relative; overflow: hidden;">
											<div
												onclick={() => toggleOpen(e)}
												onkeydown={(ev) => ev.key === 'Enter' && toggleOpen(e)}
												role="button"
												tabindex="-1"
												style="display: flex; align-items: center; gap: 10px; padding: 5px 9px 5px 3px; cursor: {openable(e) ? 'pointer' : 'default'};"
												class="hover:bg-[var(--color-neutral-900)]!"
											>
												<span style="font-family: {MONO}; font-size: 11px; letter-spacing: 0.04em; color: {e.live ? 'var(--color-accent-300)' : (VERB_TONE[e.kind] ?? 'var(--color-neutral-500)')}; flex: 0 0 auto;">{e.live ? e.verb : e.st.tool.toLowerCase()}</span>
												<span style="font-family: {MONO}; font-size: 12.5px; color: var(--color-neutral-200); overflow: hidden; text-overflow: ellipsis; white-space: nowrap;">{e.live ? e.target : e.st.detail || ''}</span>
												<div style="flex: 1; min-width: 12px; height: 1px; background: linear-gradient(to right, var(--color-neutral-900), transparent);"></div>
												<span style="font-family: {MONO}; font-size: 11px; color: {e.live ? 'var(--color-accent-200)' : 'var(--color-neutral-600)'}; white-space: nowrap; font-variant-numeric: tabular-nums;">{e.live ? `${pendingSecs}s` : rowMeta(e.st)}</span>
											</div>
											{#if open[e.id] && e.st?.out}
												<div style="padding: 7px 0 9px 3px; animation: rk-enter 180ms {EASE} both;">
													{#each e.st.out.split('\n') as l, li (li)}
														<div style="font-family: {MONO}; font-size: 12px; line-height: 1.6; color: {bodyTone(l)}; white-space: pre-wrap; word-break: break-word; padding: 0.5px 0;">{l}</div>
													{/each}
													{#if e.st.lines > e.st.out.split('\n').length}
														<div style="font-family: {MONO}; font-size: 10.5px; color: var(--color-neutral-700); padding-top: 3px;">… {e.st.lines} lines total</div>
													{/if}
												</div>
											{/if}
										</div>
									{/if}
								</div>
							</div>
						{/if}
					{/each}
				</div>
			</div>

			<!-- composer -->
			<div style="flex: 0 0 auto; position: relative;">
				<div style="height: 1px; background: {composerEdge};"></div>
				{#if phase === 'waiting'}
					<div style="position: absolute; top: -14px; left: 0; right: 0; height: 15px; background: linear-gradient(to top, var(--color-accent-800), transparent); opacity: 0.5; animation: rk-glow 2.1s ease-in-out infinite; pointer-events: none;"></div>
				{/if}
				<div style="max-width: 940px; margin: 0 auto; padding: 10px 28px 12px;">
					<div style="display: flex; align-items: center; gap: 9px; min-height: 18px; margin-bottom: 7px; flex-wrap: wrap;">
						<span style="font-family: {MONO}; font-size: 11px; color: {statusTone};">{statusLine}</span>
						{#each data?.queue ?? [] as q, qi (qi)}
							<span style="font-size: 11px; padding: 2px 7px; border-radius: 99px; border: 1px solid var(--color-accent-700); color: var(--color-accent-200); max-width: 320px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;">{q.text}</span>
						{/each}
						<div style="flex: 1;"></div>
						{#if busy}
							<button
								onclick={oninterrupt}
								disabled={interrupting}
								style="font: inherit; font-family: {MONO}; font-size: 11px; padding: 3px 8px; cursor: pointer; border-radius: var(--radius-sm); border: 1px solid var(--color-neutral-800); background: transparent; color: var(--color-neutral-400);"
								class="hover:border-[var(--color-accent-600)]! hover:text-[var(--color-accent-100)]!"
							>
								{interrupting ? 'stopping…' : 'interrupt · esc'}
							</button>
						{/if}
					</div>
					{#if attachments.length || uploadErr}
						<div style="display: flex; align-items: center; gap: 8px; flex-wrap: wrap; margin-bottom: 7px;">
							{#each attachments as a, ai (a.name)}
								<span style="position: relative; display: inline-flex;">
									<img src={a.preview} alt={a.name} style="height: 44px; border-radius: var(--radius-sm); border: 1px solid var(--color-accent-800);" />
									<button
										onclick={() => dropAttachment(ai)}
										style="position: absolute; top: -6px; right: -6px; width: 16px; height: 16px; line-height: 1; font-size: 10px; cursor: pointer; border-radius: 99px; border: 1px solid var(--color-neutral-700); background: var(--color-bg); color: var(--color-neutral-400);"
										title="drop this attachment"
									>✕</button>
								</span>
							{/each}
							{#if uploadErr}
								<span style="font-size: 11px; color: var(--ev-del);">{uploadErr}</span>
							{/if}
						</div>
					{/if}
					<form style="display: flex; align-items: flex-end; gap: 10px;" onsubmit={submit}>
						<textarea
							bind:this={inputEl}
							bind:value={text}
							onkeydown={onKeydown}
							onpaste={onPaste}
							rows="2"
							placeholder={phase === 'working' ? 'interject — lands when this turn ends…' : 'direct claude code…'}
							style="flex: 1; resize: none; font-family: var(--font-body); font-size: 14.5px; line-height: 1.55; color: var(--color-neutral-100); background: transparent; border: 1px solid {phase === 'waiting' ? 'var(--color-accent-600)' : 'var(--color-neutral-800)'}; border-radius: var(--radius-md); padding: 9px 11px; transition: border-color 180ms; outline: none;"
						></textarea>
						<button
							type="submit"
							disabled={!text.trim() && !attachments.length}
							style="font: inherit; font-size: 12.5px; padding: 8px 14px; cursor: pointer; border-radius: var(--radius-sm); border: 1px solid var(--color-accent); background: transparent; color: var(--color-accent-100); white-space: nowrap; opacity: {!text.trim() && !attachments.length ? 0.5 : 1};"
							class="hover:bg-[var(--color-accent-800)]!"
						>
							{phase === 'working' ? 'Queue' : 'Send'}
						</button>
					</form>
					<div style="display: flex; align-items: center; gap: 12px; padding-top: 8px; font-family: {MONO}; font-size: 10.5px; color: var(--color-neutral-600); flex-wrap: wrap; white-space: nowrap;">
						<span style="color: var(--color-neutral-500);">/ commands</span>
						<span>⌘V image</span>
						<span style="color: var(--color-neutral-800);">|</span>
						<span>claude code{data?.ctx?.model ? ` · ${data.ctx.model.replace('claude-', '')}` : ''}</span>
						<button
							onclick={() => (showReason = !showReason)}
							style="flex: 0 0 auto; white-space: nowrap; font: inherit; font-family: {MONO}; font-size: 10.5px; padding: 2px 7px; cursor: pointer; border-radius: 99px; border: 1px solid {showReason ? 'var(--color-accent-700)' : 'var(--color-neutral-800)'}; background: transparent; color: {showReason ? 'var(--color-accent-200)' : 'var(--color-neutral-500)'};"
							class="hover:border-[var(--color-accent-500)]!"
						>
							reasoning {showReason ? 'on' : 'off'}
						</button>
						<button
							onclick={() => (inspector = !inspector)}
							style="flex: 0 0 auto; white-space: nowrap; font: inherit; font-size: 10.5px; padding: 2px 7px; cursor: pointer; border-radius: 99px; border: 1px solid {perm === 'all' ? 'var(--color-accent-400)' : 'var(--color-neutral-800)'}; background: transparent; color: {perm === 'all' ? 'var(--color-accent-100)' : 'var(--color-neutral-500)'};"
							class="hover:border-[var(--color-accent-500)]!"
						>
							{perm === 'all' ? 'UNGATED' : perm === 'edit' ? 'gate: shell' : 'gate: writes'}
						</button>
						<div style="flex: 1;"></div>
						<span>⏎ send</span>
						<span class="hidden sm:inline">j k move</span>
						<button onclick={() => (keysOpen = !keysOpen)} style="font: inherit; font-size: 10.5px; padding: 0 4px; cursor: pointer; border: none; background: transparent; color: var(--color-neutral-500);" class="hover:text-[var(--color-neutral-100)]!">? keys</button>
					</div>
					<div style="padding-top: 4px; font-family: {MONO}; font-size: 10.5px; color: var(--color-neutral-600);">
						every turn logs to this agent's transcript with actor <span style="color: var(--color-neutral-400);">human</span>
					</div>
				</div>
			</div>
		</main>

		<!-- inspector -->
		<aside
			style="position: absolute; top: 0; right: 0; bottom: 0; width: 300px; border-left: 1px solid var(--color-divider); background: var(--color-surface); transform: translateX({inspector ? '0' : '308px'}); transition: transform 220ms {EASE}; overflow-y: auto; box-shadow: {inspector ? 'var(--shadow-lg, 0 12px 40px rgba(0,0,0,0.5))' : 'none'}; z-index: 20;"
		>
			<div style="display: flex; align-items: center; gap: 8px; padding: 11px 14px; border-bottom: 1px solid var(--color-divider);">
				<span style="font-family: {MONO}; font-size: 10.5px; letter-spacing: 0.08em; color: var(--color-neutral-500);">SESSION</span>
				<div style="flex: 1;"></div>
				<button onclick={() => (inspector = false)} style="font: inherit; font-size: 12px; line-height: 1; padding: 3px 5px; cursor: pointer; border: none; background: transparent; color: var(--color-neutral-600); border-radius: var(--radius-sm);" class="hover:text-[var(--color-neutral-100)]!">✕</button>
			</div>

			<div style="padding: 14px; border-bottom: 1px solid var(--color-divider); display: flex; flex-direction: column; gap: 9px;">
				<div style="display: flex; align-items: baseline; gap: 8px;">
					<span style="font-family: {MONO}; font-size: 10.5px; letter-spacing: 0.08em; color: var(--color-neutral-600);">CONTEXT</span>
					<div style="flex: 1;"></div>
					{#if data?.ctx?.tokens}
						<span style="font-family: {MONO}; font-size: 11.5px; color: var(--color-neutral-100); font-variant-numeric: tabular-nums;">{k(data.ctx.tokens)}</span>
						<span style="font-family: {MONO}; font-size: 11.5px; color: var(--color-neutral-600);">/ {k(data.ctx.window)}</span>
					{:else}
						<span style="font-family: {MONO}; font-size: 11px; color: var(--color-neutral-600);">no usage yet</span>
					{/if}
				</div>
				{#if ctxSegs.length}
					<div style="display: flex; height: 5px; border-radius: 99px; overflow: hidden; background: var(--color-neutral-900);">
						{#each ctxSegs as b (b.name)}
							<div style="width: {b.w}; background: {b.color}; transition: width 300ms {EASE};"></div>
						{/each}
					</div>
					<div style="display: flex; flex-wrap: wrap; gap: 4px 12px;">
						{#each ctxSegs as b (b.name)}
							<span style="display: flex; align-items: center; gap: 5px; font-family: {MONO}; font-size: 10.5px; color: var(--color-neutral-500);">
								<span style="width: 5px; height: 5px; border-radius: 99px; background: {b.color};"></span>{b.name}
								<span style="color: var(--color-neutral-600);">{k(b.tokens)}</span>
							</span>
						{/each}
					</div>
				{/if}
				<div style="display: flex; gap: 7px; padding-top: 2px;">
					<button
						onclick={oncompact}
						disabled={compacting || busy}
						style="font: inherit; font-family: {MONO}; font-size: 11px; padding: 4px 9px; cursor: pointer; border-radius: var(--radius-sm); border: 1px solid var(--color-neutral-800); background: transparent; color: var(--color-neutral-300); opacity: {compacting || busy ? 0.5 : 1};"
						class="hover:border-[var(--color-accent-600)]! hover:text-[var(--color-accent-100)]!"
						title="send /compact — claude summarizes the conversation and the context shrinks"
					>
						{compacting ? 'compacting…' : 'compact'}
					</button>
					<button
						onclick={() => navigator.clipboard?.writeText(`claude --resume ${data?.resume}`)}
						style="font: inherit; font-family: {MONO}; font-size: 11px; padding: 4px 9px; cursor: pointer; border-radius: var(--radius-sm); border: 1px solid var(--color-neutral-800); background: transparent; color: var(--color-neutral-300);"
						class="hover:border-[var(--color-accent-600)]! hover:text-[var(--color-accent-100)]!"
						title="copy the claude --resume command — take this conversation to a terminal"
					>
						copy resume
					</button>
				</div>
			</div>

			<div style="padding: 14px; border-bottom: 1px solid var(--color-divider); display: flex; flex-direction: column; gap: 8px;">
				<div style="font-family: {MONO}; font-size: 10.5px; letter-spacing: 0.08em; color: var(--color-neutral-600);">PERMISSIONS · NEXT TURN</div>
				{#each permRows as p (p.key)}
					<button
						onclick={() => togglePerm(p)}
						disabled={p.fixed}
						style="display: flex; align-items: center; gap: 10px; padding: 3px 0; background: none; border: none; font: inherit; cursor: {p.fixed ? 'default' : 'pointer'}; text-align: left; color: inherit;"
					>
						<span style="position: relative; width: 26px; height: 15px; border-radius: 99px; flex: 0 0 auto; background: {p.on ? (p.danger ? 'color-mix(in srgb, var(--ev-del-mid) 40%, transparent)' : 'var(--color-accent-800)') : 'transparent'}; border: 1px solid {p.on ? (p.danger ? 'var(--ev-del-mid)' : 'var(--color-accent-500)') : 'var(--color-neutral-700)'}; transition: background 160ms, border-color 160ms;">
							<span style="position: absolute; top: 2px; left: {p.on ? '13px' : '2px'}; width: 9px; height: 9px; border-radius: 99px; background: {p.on ? (p.danger ? 'var(--ev-del)' : 'var(--color-accent-100)') : 'var(--color-neutral-600)'}; transition: left 160ms {EASE};"></span>
						</span>
						<span style="font-size: 11.5px; color: var(--color-neutral-300); text-wrap: pretty;">{p.name}</span>
						<div style="flex: 1;"></div>
						<span style="font-family: {MONO}; font-size: 10.5px; color: {p.danger && p.on ? 'var(--ev-del)' : 'var(--color-neutral-600)'}; white-space: nowrap;">{p.state}</span>
					</button>
				{/each}
				{#if perm === 'all'}
					<div style="font-size: 11px; line-height: 1.5; color: var(--color-accent-200); border-left: 2px solid var(--color-accent-400); padding-left: 8px; text-wrap: pretty;">
						No gate at all — claude can edit, delete and run anything you could.
					</div>
				{/if}
			</div>

			{#if data?.tree?.length}
				<div style="padding: 14px; border-bottom: 1px solid var(--color-divider); display: flex; flex-direction: column; gap: 7px;">
					<div style="font-family: {MONO}; font-size: 10.5px; letter-spacing: 0.08em; color: var(--color-neutral-600);">WORKING TREE</div>
					{#each data.tree as f (f.path)}
						<div style="display: flex; align-items: center; gap: 9px;">
							<span style="font-family: {MONO}; font-size: 11.5px; color: var(--color-neutral-300); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; direction: rtl;">{f.path}</span>
							<div style="flex: 1;"></div>
							{#if f.new}
								<span style="font-family: {MONO}; font-size: 10.5px; color: var(--color-accent-300);">new</span>
							{:else}
								<span style="font-family: {MONO}; font-size: 10.5px; color: var(--ev-add-mid); font-variant-numeric: tabular-nums;">+{f.add}</span>
								<span style="font-family: {MONO}; font-size: 10.5px; color: var(--ev-del-mid); font-variant-numeric: tabular-nums;">−{f.del}</span>
							{/if}
						</div>
					{/each}
				</div>
			{/if}

			<div style="padding: 14px 14px 24px; display: flex; flex-direction: column; gap: 6px;">
				<div style="font-family: {MONO}; font-size: 10.5px; letter-spacing: 0.08em; color: var(--color-neutral-600);">SPEND</div>
				<div style="display: flex; justify-content: space-between; font-size: 11.5px;" title="what these turns would bill at API prices — a subscription has already paid for them">
					<span style="color: var(--color-neutral-500);">claude turns <span style="color: var(--color-neutral-600);">· api rate</span></span>
					<span style="color: var(--color-neutral-300); font-variant-numeric: tabular-nums;">${(data?.spend?.claudeUsd ?? 0).toFixed(2)}</span>
				</div>
				<div style="display: flex; justify-content: space-between; font-size: 11.5px;" title="judge, digests, phrasing — real spend on the LLM endpoint">
					<span style="color: var(--color-neutral-500);">rook's own calls</span>
					<span style="color: var(--color-neutral-300); font-variant-numeric: tabular-nums;">${(data?.spend?.judgeUsd ?? 0).toFixed(2)}</span>
				</div>
				<div style="height: 1px; background: var(--color-divider); margin: 3px 0;"></div>
				<div style="display: flex; justify-content: space-between; font-size: 12px;">
					<span style="color: var(--color-neutral-400);">rolled up</span>
					<span style="color: var(--color-neutral-100); font-variant-numeric: tabular-nums;">${spendTotal.toFixed(2)}</span>
				</div>
			</div>
		</aside>

		<!-- keyboard dialog -->
		{#if keysOpen}
			<div
				onclick={() => (keysOpen = false)}
				onkeydown={(e) => e.key === 'Escape' && (keysOpen = false)}
				role="button"
				tabindex="-1"
				style="position: absolute; inset: 0; display: flex; align-items: center; justify-content: center; background: rgba(10, 11, 18, 0.6); z-index: 30; animation: rk-enter 140ms ease-out both;"
			>
				<div style="width: 460px; max-width: calc(100% - 40px); padding: 18px 20px 20px; background: var(--color-surface); border: 1px solid var(--color-neutral-800); border-radius: var(--radius-lg); box-shadow: 0 18px 60px rgba(0,0,0,0.55);">
					<div style="font-family: {MONO}; font-size: 10.5px; letter-spacing: 0.1em; color: var(--color-neutral-500); margin-bottom: 12px;">KEYBOARD</div>
					{#each KEYMAP as km (km.k)}
						<div style="display: flex; align-items: baseline; gap: 12px; padding: 4px 0; border-bottom: 1px solid var(--color-divider);">
							<span style="font-family: {MONO}; font-size: 12px; color: var(--color-accent-200); min-width: 88px;">{km.k}</span>
							<span style="font-size: 12.5px; color: var(--color-neutral-300);">{km.d}</span>
						</div>
					{/each}
				</div>
			</div>
		{/if}
	</div>
</div>
