<script>
	// The direct-drive cockpit, from the "Direct drive" view of the
	// Rook Board design: turns rail · typed stream · truth rail.
	// Everything rendered is real — turns from the transcript, diffs
	// from the Edit calls' own inputs, the working tree from git, the
	// context bar from the last usage record. What the headless rail
	// cannot do (rewind, mid-turn gates, context eviction) is absent,
	// not faked.
	let {
		data,
		perm,
		setPerm,
		pendingSecs,
		interrupting,
		onsend,
		oninterrupt,
		oncompact,
		compacting
	} = $props();

	import { uploadImage, uploadUrl, imageParts } from './state.svelte.js';
	import Markdown from './Markdown.svelte';

	let text = $state('');
	let showThinking = $state(true);
	let chatEl = $state(null);
	let nearBottom = true;

	// Pasted images: uploaded the moment they land, held as chips until
	// the send carries their paths.
	let attachments = $state([]);
	let uploadErr = $state('');
	const agentId = $derived(data?.agent?.id);

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

	const busy = $derived(data?.pending?.status === 'thinking' || data?.pending?.status === 'phrasing');
	const history = $derived(data?.history ?? []);

	// The turns rail: a user message opens a turn; the assistant reply
	// that follows carries the window size the turn ended at.
	const turns = $derived.by(() => {
		const out = [];
		history.forEach((m, i) => {
			if (m.role !== 'user') return;
			const reply = history[i + 1]?.role === 'assistant' ? history[i + 1] : null;
			out.push({
				n: out.length + 1,
				i,
				what: (m.rough || m.text).slice(0, 72),
				by: m.rough ? 'you · via rook' : 'you',
				ctx: reply?.ctx ? k(reply.ctx) + ' ctx' : '',
				tools: reply?.tools ?? 0
			});
		});
		return out;
	});

	function k(n) {
		if (!n) return '0';
		if (n >= 100000) return `${Math.round(n / 1000)}k`;
		if (n >= 1000) return `${(n / 1000).toFixed(1)}k`;
		return String(n);
	}

	// The context bar: the window as its real composition.
	const ctxSegs = $derived.by(() => {
		const c = data?.ctx;
		if (!c?.tokens) return [];
		const w = c.window || 200000;
		const seg = (name, tokens, color) => ({ name, tokens, w: `${Math.max(0.4, (tokens / w) * 100)}%`, color });
		return [
			seg('fresh in', c.in ?? 0, 'var(--color-neutral-700)'),
			seg('cache read', c.cacheRead ?? 0, 'var(--color-accent-800)'),
			seg('cache write', c.cacheWrite ?? 0, 'var(--color-accent-500)'),
			seg('out', c.out ?? 0, 'var(--color-neutral-500)')
		].filter((s) => s.tokens > 0);
	});

	// The policy rows: our three real tiers as the design's toggles.
	const permRows = $derived([
		{ key: 'read', name: 'reads — always on', on: true, fixed: true, state: 'granted' },
		{
			key: 'edit',
			name: 'edits + build/test (go, npm, make)',
			on: perm === 'edit' || perm === 'all',
			state: perm === 'edit' || perm === 'all' ? 'auto' : 'refused'
		},
		{
			key: 'all',
			name: 'everything — no permission gate',
			on: perm === 'all',
			danger: true,
			state: perm === 'all' ? 'NO GATE' : 'off'
		}
	]);
	function togglePerm(row) {
		if (row.fixed) return;
		if (row.key === 'edit') setPerm(perm === 'edit' || perm === 'all' ? 'read' : 'edit');
		else setPerm(perm === 'all' ? 'edit' : 'all');
	}

	function diffLines(d) {
		const out = [];
		for (const l of (d.old ?? '').split('\n')) out.push({ sign: '-', text: l });
		for (const l of (d.new ?? '').split('\n')) out.push({ sign: '+', text: l });
		return out;
	}

	// Consecutive plain tool calls fold into one card; an edit's diff
	// stands alone. One turn reads as a few cards, not a wall of rows.
	function stepGroups(steps = []) {
		const groups = [];
		for (const st of steps) {
			const last = groups[groups.length - 1];
			if (st.diff) groups.push({ diff: st });
			else if (last?.tools) last.tools.push(st);
			else groups.push({ tools: [st] });
		}
		return groups;
	}

	const followKey = $derived(history.length + ':' + (data?.pending?.status ?? ''));
	$effect(() => {
		followKey;
		if (chatEl && nearBottom) chatEl.scrollTop = chatEl.scrollHeight;
	});
	function onScroll() {
		if (!chatEl) return;
		nearBottom = chatEl.scrollHeight - chatEl.scrollTop - chatEl.clientHeight < 160;
	}

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
	function jump(i) {
		document.getElementById(`turn-${i}`)?.scrollIntoView({ behavior: 'smooth', block: 'start' });
	}
</script>

<div class="nk" style="flex: 1; min-height: 0; display: flex; background: var(--color-bg); color: var(--color-text); font-family: var(--font-body);">

	<!-- turns rail -->
	<section style="width: 232px; flex: 0 0 232px; border-right: 1px solid var(--color-divider); display: none; flex-direction: column; min-height: 0;" class="lg:flex!">
		<div style="padding: 14px 14px 12px; border-bottom: 1px solid var(--color-divider); display: flex; flex-direction: column; gap: 6px;">
			<div style="display: flex; align-items: center; gap: 7px;">
				<span class="tag tag-accent" style="font-size: 10px;">you have the wheel</span>
			</div>
			<div style="font-family: var(--font-heading); font-weight: 500; font-size: 12.5px; line-height: 1.35; color: var(--color-neutral-100); text-wrap: pretty;">{data?.agent?.title ?? '…'}</div>
			<div style="font-size: 10.5px; color: var(--color-neutral-600); font-family: ui-monospace, Menlo, monospace;">{data?.resume?.slice(0, 8)} · {data?.agent?.dir}</div>
		</div>
		<div style="flex: 1; min-height: 0; overflow-y: auto; padding: 10px 8px 14px;">
			<div style="font-size: 10.5px; letter-spacing: 0.07em; text-transform: uppercase; color: var(--color-neutral-600); padding: 0 6px 8px;">turns · {turns.length}</div>
			{#each turns as t (t.i)}
				<button
					onclick={() => jump(t.i)}
					style="width: 100%; text-align: left; cursor: pointer; background: none; border: none; font: inherit; display: grid; grid-template-columns: 18px 1fr; gap: 8px; align-items: baseline; padding: 7px 8px; border-radius: var(--radius-sm); color: inherit;"
					class="hover:bg-[var(--color-neutral-900)]!"
				>
					<span style="font-size: 11px; color: {t.by === 'you' ? 'var(--color-accent-300)' : 'var(--color-neutral-600)'}; font-variant-numeric: tabular-nums;">{t.n}</span>
					<span style="display: flex; flex-direction: column; gap: 2px; min-width: 0;">
						<span style="font-size: 11.5px; line-height: 1.4; color: var(--color-neutral-300); overflow: hidden; text-overflow: ellipsis; white-space: nowrap;">{t.what}</span>
						<span style="font-size: 10.5px; color: var(--color-neutral-600);">{t.by}{t.ctx ? ` · ${t.ctx}` : ''}{t.tools ? ` · ⛭${t.tools}` : ''}</span>
					</span>
				</button>
			{/each}
		</div>
	</section>

	<!-- stream -->
	<main style="flex: 1; min-width: 0; display: flex; flex-direction: column; min-height: 0;">
		<div bind:this={chatEl} onscroll={onScroll} style="flex: 1; min-height: 0; overflow-y: auto; padding: 18px 22px 8px;">
		<div style="width: 100%; max-width: 860px; margin: 0 auto; display: flex; flex-direction: column; gap: 15px;">
			{#each history as m, i (i)}
				{#if m.role === 'user'}
					{@const parts = imageParts(m.text)}
					<div id="turn-{i}" style="display: flex; flex-direction: column; gap: 4px;">
						<span style="font-size: 10.5px; letter-spacing: 0.07em; text-transform: uppercase; color: var(--color-accent-300);">{m.rough ? 'you · via rook' : 'you'}</span>
						<span style="font-size: 13.5px; line-height: 1.6; color: var(--color-neutral-100); white-space: pre-wrap; text-wrap: pretty;">{m.rough || parts.text}</span>
						{#if parts.names.length}
							<div style="display: flex; gap: 8px; flex-wrap: wrap; margin-top: 2px;">
								{#each parts.names as n (n)}
									<a href={uploadUrl(agentId, n)} target="_blank" rel="noreferrer">
										<img src={uploadUrl(agentId, n)} alt="attachment" style="max-height: 120px; max-width: 220px; border-radius: var(--radius-md); border: 1px solid var(--color-neutral-800);" />
									</a>
								{/each}
							</div>
						{/if}
						{#if m.rough && m.rough !== m.text}
							<details style="margin-top: 2px;">
								<summary style="cursor: pointer; font-size: 10.5px; color: var(--color-neutral-600);">what rook sent</summary>
								<div style="margin-top: 4px; font-size: 12px; line-height: 1.55; color: var(--color-neutral-500); white-space: pre-wrap; border-left: 2px solid var(--color-neutral-800); padding-left: 10px;">{m.text}</div>
							</details>
						{/if}
					</div>
				{:else}
					{#if showThinking}
						{#each m.think ?? [] as th, ti (ti)}
							<div style="border: 1px dashed var(--color-neutral-800); border-radius: var(--radius-md); padding: 9px 12px; display: flex; flex-direction: column; gap: 4px;">
								<span style="font-size: 10.5px; letter-spacing: 0.07em; text-transform: uppercase; color: var(--color-neutral-600);">reasoning</span>
								<span style="font-size: 12px; line-height: 1.6; color: var(--color-neutral-500); font-style: italic; text-wrap: pretty;">{th}</span>
							</div>
						{/each}
					{/if}
					{#each stepGroups(m.steps) as g, gi (gi)}
						{#if g.diff}
							<div style="border: 1px solid var(--color-neutral-800); border-radius: var(--radius-md); background: var(--color-surface); overflow: hidden;">
								<div style="display: flex; align-items: center; gap: 10px; padding: 8px 12px; border-bottom: 1px solid var(--color-divider);">
									<span style="font-family: ui-monospace, Menlo, monospace; font-size: 11px; letter-spacing: 0.04em; color: var(--color-accent-300);">{g.diff.tool.toLowerCase()}</span>
									<span style="font-family: ui-monospace, Menlo, monospace; font-size: 12px; color: var(--color-neutral-200); overflow: hidden; text-overflow: ellipsis; white-space: nowrap;">{g.diff.diff.file}</span>
									{#if g.diff.diff.all}<span style="font-size: 10.5px; color: var(--color-neutral-600);">replace all</span>{/if}
								</div>
								<div style="padding: 7px 0; background: var(--color-bg); max-height: 260px; overflow-y: auto;">
									{#each diffLines(g.diff.diff) as l, li (li)}
										<div style="display: grid; grid-template-columns: 14px 1fr; gap: 8px; padding: 1px 12px; background: {l.sign === '+' ? 'var(--color-accent-900)' : 'var(--color-neutral-900)'};">
											<span style="font-family: ui-monospace, Menlo, monospace; font-size: 11.5px; color: {l.sign === '+' ? 'var(--color-accent-100)' : 'var(--color-neutral-600)'};">{l.sign}</span>
											<span style="font-family: ui-monospace, Menlo, monospace; font-size: 11.5px; line-height: 1.6; color: {l.sign === '+' ? 'var(--color-accent-100)' : 'var(--color-neutral-600)'}; white-space: pre-wrap;">{l.text}</span>
										</div>
									{/each}
								</div>
							</div>
						{:else}
							<div style="border: 1px solid var(--color-neutral-800); border-radius: var(--radius-md); background: var(--color-surface); padding: 4px 0;">
								{#each g.tools as st, si (si)}
									<div style="display: flex; align-items: baseline; gap: 10px; padding: 2.5px 12px;">
										<span style="font-family: ui-monospace, Menlo, monospace; font-size: 11px; letter-spacing: 0.04em; color: var(--color-accent-300); flex: 0 0 52px;">{st.tool.toLowerCase()}</span>
										<span style="font-family: ui-monospace, Menlo, monospace; font-size: 11.5px; color: var(--color-neutral-400); overflow: hidden; text-overflow: ellipsis; white-space: nowrap;">{st.detail}</span>
									</div>
								{/each}
							</div>
						{/if}
					{/each}
					{#if m.tools > (m.steps?.length ?? 0)}
						<div style="font-size: 10.5px; color: var(--color-neutral-700); padding-left: 2px;">… and {m.tools - m.steps.length} more tool calls</div>
					{/if}
					{#if m.text}
						<div style="display: flex; flex-direction: column; gap: 4px;">
							<span style="font-size: 10.5px; letter-spacing: 0.07em; text-transform: uppercase; color: var(--color-neutral-500);">claude code</span>
							<div style="font-size: 13.5px; color: var(--color-neutral-300);"><Markdown text={m.text} /></div>
						</div>
					{/if}
				{/if}
			{/each}

			{#if data?.pending}
				{#if data.pending.status === 'failed'}
					<div style="display: flex; align-items: center; gap: 9px; font-size: 11.5px; color: var(--color-neutral-600);">
						<div style="width: 20px; height: 1px; background: var(--color-neutral-800);"></div>
						<span>{data.pending.error}</span>
						<div style="flex: 1; height: 1px; background: linear-gradient(to right, var(--color-neutral-800), transparent);"></div>
					</div>
				{:else}
					<div style="display: flex; flex-direction: column; gap: 4px;">
						<span style="font-size: 10.5px; letter-spacing: 0.07em; text-transform: uppercase; color: var(--color-accent-300);">you</span>
						<span style="font-size: 13.5px; line-height: 1.6; color: var(--color-neutral-100); white-space: pre-wrap; opacity: 0.85;">{data.pending.text}</span>
						{#if data.pending.images?.length}
							<div style="display: flex; gap: 8px; flex-wrap: wrap;">
								{#each data.pending.images as p (p)}
									<img src={uploadUrl(agentId, p.split('/').pop())} alt="attachment" style="max-height: 90px; border-radius: var(--radius-md); border: 1px solid var(--color-neutral-800); opacity: 0.85;" />
								{/each}
							</div>
						{/if}
					</div>
					<div style="display: flex; align-items: center; gap: 10px; padding: 2px 0 6px;">
						<span style="width: 5px; height: 5px; border-radius: 99px; background: var(--color-accent); box-shadow: 0 0 8px var(--color-accent);"></span>
						<span style="font-size: 12px; color: var(--color-accent-300);">
							claude code is working · {pendingSecs}s{data?.agent?.tool ? ` · ⛭ ${data.agent.tool}${data.agent.toolDetail ? ' — ' + data.agent.toolDetail : ''}` : ''}
						</span>
						<button
							onclick={oninterrupt}
							disabled={interrupting}
							style="font: inherit; font-size: 10.5px; padding: 3px 8px; cursor: pointer; border-radius: var(--radius-sm); border: 1px solid var(--color-neutral-800); background: transparent; color: var(--color-neutral-400);"
						>
							{interrupting ? 'stopping…' : 'Interrupt · esc'}
						</button>
					</div>
				{/if}
			{/if}
		</div>
		</div>

		<!-- composer: same reading measure as the stream above it -->
		<div style="border-top: 1px solid var(--color-divider); padding: 11px 22px 12px;">
		<div style="width: 100%; max-width: 860px; margin: 0 auto; display: flex; flex-direction: column; gap: 8px;">
			{#if data?.queue?.length}
				<div style="display: flex; align-items: center; gap: 8px; font-size: 11.5px; color: var(--color-accent-200); flex-wrap: wrap;">
					<span style="color: var(--color-neutral-600);">queued</span>
					{#each data.queue as q, qi (qi)}
						<span class="tag tag-outline" style="font-size: 11px;">{q.text.slice(0, 48)}</span>
					{/each}
					<span style="color: var(--color-neutral-600);">lands when this turn ends</span>
				</div>
			{/if}
			{#if attachments.length || uploadErr}
				<div style="display: flex; align-items: center; gap: 8px; flex-wrap: wrap;">
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
						<span style="font-size: 11px; color: rgb(251,113,133);">{uploadErr}</span>
					{/if}
				</div>
			{/if}
			<form style="display: flex; align-items: flex-end; gap: 10px;" onsubmit={submit}>
				<textarea
					bind:value={text}
					onkeydown={onKeydown}
					onpaste={onPaste}
					rows="1"
					placeholder={busy ? 'interject — lands when this turn ends…' : 'tell claude code what to do next… (paste an image to attach)'}
					class="input"
					style="flex: 1; background: transparent; font-size: 13.5px; resize: none; font-family: inherit;"
				></textarea>
				<button type="submit" class="btn btn-primary" style="font-size: 12.5px;" disabled={!text.trim() && !attachments.length}>
					{busy ? 'Queue' : 'Send'}
				</button>
			</form>
			<div style="display: flex; align-items: center; gap: 14px; font-size: 10.5px; color: var(--color-neutral-600); flex-wrap: wrap;">
				<span>claude code{data?.ctx?.model ? ` · ${data.ctx.model.replace('claude-', '')}` : ''}</span>
				<span style="color: var(--color-neutral-800);">|</span>
				<span>⏎ send</span>
				<span>esc interrupt</span>
				<button onclick={() => (showThinking = !showThinking)} style="font: inherit; cursor: pointer; background: none; border: none; padding: 0; color: {showThinking ? 'var(--color-accent-300)' : 'var(--color-neutral-600)'};">
					reasoning {showThinking ? 'on' : 'off'}
				</button>
				<div style="flex: 1;"></div>
				<span>every turn logs to this agent's transcript with actor <span style="color: var(--color-neutral-400);">human</span></span>
			</div>
		</div>
		</div>
	</main>

	<!-- truth rail -->
	<aside style="width: 292px; flex: 0 0 292px; border-left: 1px solid var(--color-divider); display: none; flex-direction: column; min-height: 0; overflow-y: auto; background: linear-gradient(180deg, #1a1c2c, var(--color-bg) 300px);" class="xl:flex!">

		<div style="padding: 16px 16px 14px; border-bottom: 1px solid var(--color-divider); display: flex; flex-direction: column; gap: 9px;">
			<div style="display: flex; align-items: baseline; gap: 8px;">
				<span style="font-size: 10.5px; letter-spacing: 0.07em; text-transform: uppercase; color: var(--color-neutral-600);">context window</span>
				<div style="flex: 1;"></div>
				{#if data?.ctx?.tokens}
					<span style="font-size: 11.5px; color: var(--color-neutral-200); font-variant-numeric: tabular-nums;">{k(data.ctx.tokens)}</span>
					<span style="font-size: 11.5px; color: var(--color-neutral-600); font-variant-numeric: tabular-nums;">/ {k(data.ctx.window)}</span>
				{:else}
					<span style="font-size: 11px; color: var(--color-neutral-600);">no usage yet</span>
				{/if}
			</div>
			{#if ctxSegs.length}
				<div style="display: flex; height: 6px; border-radius: 99px; overflow: hidden; background: var(--color-neutral-900);">
					{#each ctxSegs as b (b.name)}
						<div style="width: {b.w}; background: {b.color};"></div>
					{/each}
				</div>
				<div style="display: flex; flex-wrap: wrap; gap: 4px 12px;">
					{#each ctxSegs as b (b.name)}
						<span style="display: flex; align-items: center; gap: 6px; font-size: 10.5px; color: var(--color-neutral-500);">
							<span style="width: 5px; height: 5px; border-radius: 99px; background: {b.color};"></span>
							{b.name} <span style="color: var(--color-neutral-600); font-variant-numeric: tabular-nums;">{k(b.tokens)}</span>
						</span>
					{/each}
				</div>
			{/if}
		</div>

		{#if data?.tree?.length}
			<div style="padding: 14px 16px; border-bottom: 1px solid var(--color-divider); display: flex; flex-direction: column; gap: 8px;">
				<div style="display: flex; align-items: baseline; gap: 8px;">
					<span style="font-size: 10.5px; letter-spacing: 0.07em; text-transform: uppercase; color: var(--color-neutral-600);">working tree</span>
					<div style="flex: 1;"></div>
					<span style="font-size: 10.5px; color: var(--color-neutral-600);">uncommitted</span>
				</div>
				{#each data.tree as f (f.path)}
					<div style="display: flex; align-items: center; gap: 9px;">
						<span style="font-family: ui-monospace, Menlo, monospace; font-size: 11px; color: var(--color-neutral-300); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; direction: rtl;">{f.path}</span>
						<div style="flex: 1;"></div>
						{#if f.new}
							<span style="font-size: 10.5px; color: var(--color-accent-300);">new</span>
						{:else}
							<span style="font-size: 10.5px; color: var(--color-accent-300); font-variant-numeric: tabular-nums;">+{f.add}</span>
							<span style="font-size: 10.5px; color: var(--color-neutral-500); font-variant-numeric: tabular-nums;">−{f.del}</span>
						{/if}
					</div>
				{/each}
			</div>
		{/if}

		<div style="padding: 14px 16px; border-bottom: 1px solid var(--color-divider); display: flex; flex-direction: column; gap: 7px;">
			<div style="font-size: 10.5px; letter-spacing: 0.07em; text-transform: uppercase; color: var(--color-neutral-600);">permissions · next turn</div>
			{#each permRows as p (p.key)}
				<button
					onclick={() => togglePerm(p)}
					disabled={p.fixed}
					style="display: flex; align-items: center; gap: 10px; padding: 4px 0; background: none; border: none; font: inherit; cursor: {p.fixed ? 'default' : 'pointer'}; text-align: left; color: inherit;"
				>
					<span style="width: 26px; height: 15px; border-radius: 99px; flex: 0 0 auto; position: relative; background: {p.on ? (p.danger ? 'rgba(244,63,94,0.3)' : 'var(--color-accent-800)') : 'transparent'}; border: 1px solid {p.on ? (p.danger ? 'rgb(244,63,94)' : 'var(--color-accent-600)') : 'var(--color-neutral-700)'};">
						<span style="position: absolute; top: 2px; left: {p.on ? '13px' : '2px'}; width: 9px; height: 9px; border-radius: 99px; background: {p.on ? (p.danger ? 'rgb(253,164,175)' : 'var(--color-accent-200)') : 'var(--color-neutral-600)'};"></span>
					</span>
					<span style="font-size: 11.5px; color: var(--color-neutral-300);">{p.name}</span>
					<div style="flex: 1;"></div>
					<span style="font-size: 10.5px; color: {p.danger && p.on ? 'rgb(251,113,133)' : 'var(--color-neutral-600)'};">{p.state}</span>
				</button>
			{/each}
			{#if perm === 'all'}
				<div style="font-size: 10.5px; line-height: 1.5; color: rgb(251,113,133); opacity: 0.85;">
					no gate at all — claude can edit, delete, and run anything you could
				</div>
			{/if}
		</div>

		<div style="padding: 14px 16px; border-bottom: 1px solid var(--color-divider); display: flex; flex-direction: column; gap: 6px;">
			<div style="font-size: 10.5px; letter-spacing: 0.07em; text-transform: uppercase; color: var(--color-neutral-600);">spend on this agent</div>
			<div style="display: flex; justify-content: space-between; font-size: 11.5px;">
				<span style="color: var(--color-neutral-500);">claude turns</span>
				<span style="color: var(--color-neutral-300); font-variant-numeric: tabular-nums;">${(data?.spend?.claudeUsd ?? 0).toFixed(2)}</span>
			</div>
			<div style="display: flex; justify-content: space-between; font-size: 11.5px;">
				<span style="color: var(--color-neutral-500);">rook's own calls</span>
				<span style="color: var(--color-neutral-300); font-variant-numeric: tabular-nums;">${(data?.spend?.judgeUsd ?? 0).toFixed(2)}</span>
			</div>
			<div style="height: 1px; background: var(--color-divider); margin: 2px 0;"></div>
			<div style="display: flex; justify-content: space-between; font-size: 12px;">
				<span style="color: var(--color-neutral-400);">rolled up</span>
				<span style="color: var(--color-neutral-100); font-variant-numeric: tabular-nums;">${((data?.spend?.claudeUsd ?? 0) + (data?.spend?.judgeUsd ?? 0)).toFixed(2)}</span>
			</div>
		</div>

		<div style="padding: 14px 16px 20px; display: flex; gap: 8px;">
			<button
				onclick={oncompact}
				disabled={compacting || busy}
				style="font: inherit; font-size: 11px; padding: 5px 10px; cursor: pointer; border-radius: var(--radius-sm); border: 1px solid var(--color-neutral-800); background: transparent; color: var(--color-neutral-400);"
			>
				{compacting ? 'compacting…' : 'compact'}
			</button>
			<button
				onclick={() => navigator.clipboard?.writeText(`claude --resume ${data?.resume}`)}
				style="font: inherit; font-size: 11px; padding: 5px 10px; cursor: pointer; border-radius: var(--radius-sm); border: 1px solid var(--color-neutral-800); background: transparent; color: var(--color-neutral-400);"
			>
				copy resume
			</button>
		</div>
	</aside>
</div>
