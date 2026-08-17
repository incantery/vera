<script>
	import { page } from '$app/state';
	import { fetchAgent, sayTo, startDrive, stopDrive, interruptAgent, uploadImage, uploadUrl, imageParts, watchAgent, applyFrame } from '$lib/state.svelte.js';
	import UsageBar from '$lib/UsageBar.svelte';
	import ArtifactPane from '$lib/ArtifactPane.svelte';
	import DriveView from '$lib/DriveView.svelte';
	import Markdown from '$lib/Markdown.svelte';

	const id = $derived(page.params.id);

	let data = $state(null);
	let lost = $state('');
	let text = $state('');
	let driveMode = $state(false);
	let verbatim = $state(false);
	let shelfOpen = $state(false);
	let sending = $state(false);
	let error = $state('');
	let chatEl = $state(null);
	let nearBottom = true;

	// The two ways to talk: through the membrane (vera phrases, vera
	// digests) or direct (your words, claude's words, nothing between).
	// Both are identities, not toggles-per-message — sticky per agent.
	let mode = $state('membrane');
	let perm = $state('read'); // direct mode's tool policy: read | edit | all
	const direct = $derived(mode === 'direct');
	$effect(() => {
		// ?mode= deep-links a view (board links, shared URLs); the
		// stash keeps whichever way you last talked to this agent.
		// location, not page.url: the key stash rewrites history before
		// the router wakes, and the router's snapshot can miss it.
		id;
		let q = null;
		try {
			q = new URLSearchParams(location.search).get('mode');
		} catch {
			/* no window — SSR shell */
		}
		mode = q === 'direct' || q === 'membrane' ? q : (localStorage.getItem(`vera-mode:${id}`) ?? 'membrane');
		perm = localStorage.getItem(`vera-perm:${id}`) ?? 'read';
	});
	function setMode(m) {
		mode = m;
		localStorage.setItem(`vera-mode:${id}`, m);
		refresh();
	}
	function setPerm(p) {
		perm = p;
		localStorage.setItem(`vera-perm:${id}`, p);
	}

	async function refresh() {
		try {
			data = await fetchAgent(id, { raw: mode === 'direct' });
			lost = '';
		} catch (e) {
			lost = e.message;
		}
	}

	// The data loop: the Connect stream feeds frames the moment the
	// transcript moves; REST fills what the stream does not carry
	// (usage, drives, notice) and is the whole story if the stream
	// drops — polling is the fallback, not the norm.
	let streaming = $state(false);
	$effect(() => {
		id;
		mode; // re-arm on route or mode change
		refresh();
		let stop = null;
		let retry = null;
		let alive = true;
		const open = () => {
			stop = watchAgent(
				id,
				mode === 'direct',
				(frame) => {
					streaming = true;
					data = applyFrame(data, frame);
					lost = '';
				},
				() => {
					streaming = false;
					if (alive) retry = setTimeout(open, 5000);
				}
			);
		};
		open();
		let ticks = 0;
		const t = setInterval(() => {
			ticks++;
			// Streaming: a slow REST merge for usage/drives. Fallen back:
			// the old 3s poll.
			if (!streaming || ticks % 5 === 0) refresh();
		}, 3000);
		return () => {
			alive = false;
			stop?.();
			clearTimeout(retry);
			clearInterval(t);
			streaming = false;
		};
	});

	// A live second-hand for the in-flight turn — the wait is honest
	// when it's counted.
	let nowTick = $state(Date.now());
	$effect(() => {
		const t = setInterval(() => (nowTick = Date.now()), 1000);
		return () => clearInterval(t);
	});
	const pendingSecs = $derived(
		data?.pending?.at ? Math.max(0, Math.round((nowTick - new Date(data.pending.at).getTime()) / 1000)) : 0
	);

	let interrupting = $state(false);
	async function interrupt() {
		if (interrupting) return;
		interrupting = true;
		try {
			await interruptAgent(id);
			await refresh();
		} catch (err) {
			error = err.message;
		} finally {
			interrupting = false;
		}
	}

	const permLabel = { read: 'read-only', edit: 'edit + test', all: 'everything' };

	const spendTotal = $derived((data?.spend?.claudeUsd ?? 0) + (data?.spend?.judgeUsd ?? 0));

	// k renders token counts at the resolution a human reads: 300k, 1.2k, 800.
	function k(n) {
		if (!n) return '0';
		if (n >= 100000) return `${Math.round(n / 1000)}k`;
		if (n >= 1000) return `${(n / 1000).toFixed(1)}k`;
		return String(n);
	}

	let compacting = $state(false);
	async function sendCompact() {
		if (compacting) return;
		compacting = true;
		error = '';
		try {
			// /compact rides the same say rail as everything else —
			// verbatim, so the membrane can't phrase a command into prose.
			await sayTo(id, '/compact', { verbatim: true });
			await refresh();
		} catch (err) {
			error = err.message;
		} finally {
			compacting = false;
		}
	}

	// Autoscroll: follow the conversation unless the human scrolled up
	// to read — their scrollback outranks our convenience.
	const followKey = $derived(
		(data?.history?.length ?? 0) + ':' + (data?.pending?.status ?? '') + ':' + activeDrive()?.status
	);
	$effect(() => {
		followKey;
		if (chatEl && nearBottom) chatEl.scrollTop = chatEl.scrollHeight;
	});
	function onScroll() {
		if (!chatEl) return;
		nearBottom = chatEl.scrollHeight - chatEl.scrollTop - chatEl.clientHeight < 160;
	}

	function activeDrive() {
		return (data?.drives ?? []).find((d) => !d.finished);
	}
	function lastFailedDrive() {
		const ds = data?.drives ?? [];
		return ds.length && ds[0].finished && !ds[0].done ? ds[0] : null;
	}

	async function send(e) {
		e?.preventDefault();
		if ((!text.trim() && !attachments.length) || sending) return;
		sending = true;
		error = '';
		try {
			if (driveMode) await startDrive(id, text);
			else {
				const images = attachments.map((a) => a.path);
				const t = text.trim() || (images.length === 1 ? 'Look at the attached image.' : 'Look at the attached images.');
				await sayTo(id, t, { verbatim, images });
				attachments.forEach((a) => URL.revokeObjectURL(a.preview));
				attachments = [];
			}
			text = '';
			nearBottom = true;
			await refresh();
		} catch (err) {
			error = err.message;
		} finally {
			sending = false;
		}
	}

	// The cockpit's send: direct, under the sticky policy; a busy
	// agent queues it server-side.
	async function sendDirect(t, images = []) {
		error = '';
		try {
			await sayTo(id, t, { direct: true, perm, images });
			await refresh();
		} catch (err) {
			error = err.message;
		}
	}

	// Pasted images on the membrane composer: same upload rail as the
	// cockpit; the paths ride the next send whatever the phrasing does.
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
				const ans = await uploadImage(id, file);
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

	function onKeydown(e) {
		if (e.key === 'Enter' && !e.shiftKey) {
			e.preventDefault();
			send();
		}
	}

	const stateStyle = {
		'needs you': 'text-rose-400',
		'blocked?': 'text-rose-400',
		working: 'text-amber-400',
		idle: 'text-zinc-500'
	};
</script>

<div class="mx-auto flex h-dvh {direct ? 'max-w-none gap-0 px-0' : shelfOpen ? 'max-w-6xl gap-4 px-4' : 'max-w-3xl gap-4 px-4'}">
<div class="flex h-dvh min-w-0 grow flex-col">
	{#if !direct}
	<!-- header -->
	<header class="flex flex-wrap items-baseline gap-x-3 gap-y-1 border-b border-zinc-800 py-3">
		<a href="/" aria-label="back home" class="px-1 text-zinc-500 hover:text-zinc-300">←</a>
		{#if data}
			<span class="font-semibold">{data.agent.title}</span>
			<span class="text-xs text-zinc-500">
				{data.agent.dir}{data.agent.branch ? ` · ${data.agent.branch}` : ''}
			</span>
			<span class="grow"></span>
			<div
				class="flex overflow-hidden rounded-md border border-zinc-700 text-[11px]"
				title="membrane: vera phrases and digests · direct: you and claude, nothing between"
			>
				<button
					onclick={() => setMode('membrane')}
					aria-pressed={!direct}
					class="px-2 py-0.5 {direct ? 'text-zinc-500 hover:text-zinc-300' : 'bg-sky-400/20 text-sky-300'}"
				>
					membrane
				</button>
				<button
					onclick={() => setMode('direct')}
					aria-pressed={direct}
					class="px-2 py-0.5 {direct ? 'bg-emerald-400/20 text-emerald-300' : 'text-zinc-500 hover:text-zinc-300'}"
				>
					direct
				</button>
			</div>
			<a
				href="/"
				class="text-xs text-zinc-500 hover:text-zinc-300"
				title="mission control: what needs you, and the fleet's now"
			>
				⌗ home
			</a>
			<button
				class="text-xs {shelfOpen ? 'text-sky-400' : 'text-zinc-500 hover:text-zinc-300'}"
				aria-pressed={shelfOpen}
				onclick={() => (shelfOpen = !shelfOpen)}
				title="the artifact shelf: documents that belong to this agent"
			>
				⧉ artifacts{data.artifacts ? ` (${data.artifacts})` : ''}
			</button>
			{#if spendTotal}
				<span
					class="text-xs text-zinc-500"
					title="this agent's bill: claude turns ${(data.spend.claudeUsd ?? 0).toFixed(4)} (at API rates — a subscription already covers them) + vera's own calls ${(data.spend.judgeUsd ?? 0).toFixed(4)}"
				>
					${spendTotal.toFixed(spendTotal < 0.1 ? 3 : 2)}
				</span>
			{/if}
			{#if data.agent.ctxPct}
				<span class="text-xs text-zinc-500">ctx {data.agent.ctxPct}%</span>
			{/if}
			<span class="text-xs {stateStyle[data.agent.state] ?? 'text-zinc-500'}">
				{data.agent.state} · {data.agent.age}
			</span>
			{#if streaming}
				<span class="text-[10px] text-emerald-400/80" title="the Connect stream is live — updates land as the transcript moves, no polling">⚡ live</span>
			{/if}
		{:else}
			<span class="text-zinc-500">…</span>
		{/if}
	</header>
	{#if data?.usage}
		<div class="border-b border-zinc-800/60 py-1.5">
			<UsageBar usage={data.usage} />
		</div>
	{/if}
	{/if}

	<!-- the session strip: what this conversation costs and the
	     controls it supports — inside the column, the margins keep
	     their silence. The cockpit carries its own truth rail. -->
	{#if data && !direct}
		<div class="flex flex-wrap items-center gap-x-4 gap-y-1 border-b border-zinc-800/60 py-2 text-[11px] text-zinc-500">
			{#if data.ctx}
				<span title="what the next request starts from, per the transcript's own usage record">
					context <b class="font-medium text-zinc-300">{Math.round((data.ctx.tokens / data.ctx.window) * 100)}%</b>
					of {k(data.ctx.window)} · {data.ctx.model}
				</span>
				<span class="text-zinc-600">
					in {k(data.ctx.in)} · cache {k(data.ctx.cacheRead)}<span class="text-zinc-700">+{k(data.ctx.cacheWrite)}w</span> · out {k(data.ctx.out)}
				</span>
			{:else}
				<span class="text-zinc-600">context unknown — no usage in the tail yet</span>
			{/if}
			<span class="grow"></span>
			<span title="claude turns are metered at API rates — a subscription already covers them; vera's own calls are real spend on the LLM endpoint">
				claude <b class="font-medium text-zinc-300">${(data.spend?.claudeUsd ?? 0).toFixed(2)}</b><span class="text-zinc-600">&nbsp;api-rate</span>
				· vera <b class="font-medium text-zinc-300">${(data.spend?.judgeUsd ?? 0).toFixed(2)}</b>
			</span>
			<button
				onclick={sendCompact}
				disabled={compacting || !!activeDrive() || data.pending?.status === 'thinking' || data.pending?.status === 'phrasing'}
				class="rounded-md border border-zinc-700 px-2 py-0.5 text-[11px] text-zinc-400 hover:border-zinc-500 disabled:opacity-40"
				title="send /compact — claude summarizes the conversation and the context shrinks; rides the same rail as chat"
			>
				{compacting ? 'compacting…' : 'compact'}
			</button>
			<button
				onclick={() => navigator.clipboard?.writeText(`claude --resume ${data.resume}`)}
				class="rounded-md border border-zinc-700 px-2 py-0.5 text-[11px] text-zinc-400 hover:border-zinc-500"
				title="copy the claude --resume command — take this conversation to a terminal"
			>
				copy resume
			</button>
		</div>
	{/if}

	{#if lost}
		<div class="mt-3 rounded-lg border border-rose-900 bg-rose-950/40 px-3 py-2 text-[13px] text-rose-300 {direct ? 'mx-4' : ''}">
			{lost}
		</div>
	{/if}
	{#if direct && error}
		<div class="mx-4 mt-2 text-xs text-rose-400">{error}</div>
	{/if}

	{#if direct}
		<DriveView
			{data}
			{perm}
			{setPerm}
			{setMode}
			{pendingSecs}
			{interrupting}
			{streaming}
			{shelfOpen}
			onshelf={() => (shelfOpen = !shelfOpen)}
			onsend={sendDirect}
			oninterrupt={interrupt}
			oncompact={sendCompact}
			{compacting}
		/>
	{:else}
	<!-- conversation -->
	<div bind:this={chatEl} onscroll={onScroll} class="grow space-y-4 overflow-y-auto py-4">
		{#if data?.history?.length}
			{#each data.history as m, i (i)}
				{#if m.role === 'user'}
					{@const parts = imageParts(m.text)}
					<div class="flex flex-col items-end">
						<div
							class="max-w-[85%] rounded-2xl rounded-br-md border border-sky-900/60 bg-sky-950/40 px-4 py-2.5 text-[13px] whitespace-pre-wrap"
						>
							{m.rough || parts.text}
						</div>
						{#if parts.names.length}
							<div class="mt-1 flex max-w-[85%] flex-wrap justify-end gap-2">
								{#each parts.names as n (n)}
									<a href={uploadUrl(id, n)} target="_blank" rel="noreferrer">
										<img src={uploadUrl(id, n)} alt="attachment" class="max-h-28 rounded-lg border border-zinc-800" />
									</a>
								{/each}
							</div>
						{/if}
						{#if m.rough && m.rough !== m.text}
							<details class="mt-1 max-w-[85%] text-right">
								<summary class="cursor-pointer text-[11px] text-zinc-500 hover:text-zinc-300">
									⇒ what vera sent
								</summary>
								<div
									class="mt-1 rounded-lg border border-zinc-800 bg-zinc-950/60 px-3 py-2 text-left text-[12px] whitespace-pre-wrap text-zinc-400"
								>
									{m.text}
								</div>
							</details>
						{/if}
					</div>
				{:else}
					<div class="max-w-[95%]">
						{#if m.steps?.length}
							<!-- the turn's work, step by step — the last turn stays
							     open while the agent works, so a running turn reads
							     like a terminal, not a spinner -->
							<details class="mb-1">
								<summary class="cursor-pointer text-[11px] text-zinc-500 hover:text-zinc-300">
									⛭ {m.tools} tool {m.tools === 1 ? 'call' : 'calls'}
								</summary>
								<div class="mt-1 space-y-0.5 border-l-2 border-zinc-800 pl-3">
									{#each m.steps as st, si (si)}
										<div class="flex gap-2 text-[11.5px]">
											<span class="flex-none font-medium text-zinc-500">{st.tool}</span>
											{#if st.detail}
												<span class="truncate font-mono text-zinc-600">{st.detail}</span>
											{/if}
										</div>
									{/each}
									{#if m.tools > m.steps.length}
										<div class="text-[11px] text-zinc-700">… and {m.tools - m.steps.length} more</div>
									{/if}
								</div>
							</details>
						{:else if m.tools}
							<div class="mb-1 text-[11px] text-zinc-500">⛭ {m.tools} tool {m.tools === 1 ? 'call' : 'calls'}</div>
						{/if}
						{#if m.digest?.state === 'ready'}
							<div class="text-[13px] leading-relaxed font-semibold">{m.digest.headline}</div>
							{#if m.digest.bullets?.length}
								<ul class="mt-1 space-y-0.5">
									{#each m.digest.bullets as b}
										<li class="flex gap-2 text-[13px] text-zinc-300">
											<span class="text-zinc-600">–</span><span>{b}</span>
										</li>
									{/each}
								</ul>
							{/if}
							<details class="mt-1">
								<summary class="cursor-pointer text-[11px] text-zinc-500 hover:text-zinc-300">
									full reply
								</summary>
								<div class="mt-1 border-l-2 border-zinc-800 pl-3 text-[13px] text-zinc-400">
									<Markdown text={m.text} />
								</div>
							</details>
						{:else}
							{#if m.digest?.state === 'pending'}
								<div class="mb-1 animate-pulse text-[11px] text-zinc-500">summarizing…</div>
							{/if}
							{#if m.text}
								<div class="text-[13px]"><Markdown text={m.text} /></div>
							{/if}
						{/if}
					</div>
				{/if}
			{/each}
		{:else if data}
			<div class="py-8 text-center text-[13px] text-zinc-500">no conversation on record yet</div>
		{/if}

		{#if data?.pending}
			<div class="flex flex-col items-end">
				<div
					class="max-w-[85%] rounded-2xl rounded-br-md border border-sky-900/60 bg-sky-950/40 px-4 py-2.5 text-[13px] whitespace-pre-wrap opacity-80"
				>
					{data.pending.text}
				</div>
				{#if data.pending.sent && data.pending.sent !== data.pending.text}
					<details class="mt-1 max-w-[85%] text-right" open>
						<summary class="cursor-pointer text-[11px] text-zinc-500 hover:text-zinc-300">
							⇒ what vera sent
						</summary>
						<div
							class="mt-1 rounded-lg border border-zinc-800 bg-zinc-950/60 px-3 py-2 text-left text-[12px] whitespace-pre-wrap text-zinc-400"
						>
							{data.pending.sent}
						</div>
					</details>
				{/if}
			</div>
			{#if data.pending.status === 'phrasing'}
				<div class="animate-pulse text-[13px] text-zinc-500">vera is phrasing it…</div>
			{:else if data.pending.status === 'thinking'}
				<div class="flex items-center gap-3 text-[13px] text-zinc-500">
					<span class="animate-pulse">
						{data.pending.direct ? 'claude is working' : 'thinking'}… {pendingSecs}s
					</span>
					{#if data.pending.direct && data.pending.perm && data.pending.perm !== 'read'}
						<span
							class="rounded border border-zinc-800 px-1.5 text-[10.5px] text-zinc-600"
							title="the tool policy this turn runs under"
						>
							{permLabel[data.pending.perm] ?? data.pending.perm}
						</span>
					{/if}
					{#if data.agent.tool && data.agent.state === 'working'}
						<span class="truncate text-[11.5px] text-zinc-600">
							⛭ {data.agent.tool}{data.agent.toolDetail ? ` — ${data.agent.toolDetail}` : ''}
						</span>
					{/if}
					<button
						onclick={interrupt}
						disabled={interrupting}
						class="rounded-md border border-zinc-700 px-2 py-0.5 text-[11px] text-zinc-400 hover:border-rose-500 hover:text-rose-300 disabled:opacity-40"
						title="kill this turn — whatever already landed stays in the transcript; the session resumes cleanly"
					>
						{interrupting ? 'stopping…' : 'stop'}
					</button>
				</div>
			{:else}
				<div class="text-[13px] text-rose-400">did not land — {data.pending.error}</div>
			{/if}
		{/if}

		{#if activeDrive()}
			<div class="rounded-xl border border-amber-900/60 bg-amber-950/30 px-4 py-2.5 text-[13px]">
				<div class="flex items-baseline gap-3">
					<span class="font-semibold text-amber-300">driving:</span>
					<span>{activeDrive().goal}</span>
					<span class="grow"></span>
					<button
						class="rounded-md border border-zinc-700 px-2 py-0.5 text-xs text-zinc-400 hover:border-zinc-500"
						onclick={() => stopDrive(activeDrive().id).then(refresh)}
					>
						stop
					</button>
				</div>
				<div class="mt-1 animate-pulse text-amber-400/90">{activeDrive().status}</div>
			</div>
		{:else if lastFailedDrive()}
			<div class="text-[13px] text-rose-400">
				drive ended — {lastFailedDrive().reason}
			</div>
		{/if}
	</div>

	<!-- the membrane: always-on presence, pinned where the eye rests.
	     Working pulses with the live tool call; quiet states say so and
	     keep the last tool as provenance. -->
	{#if data}
		<div class="flex items-baseline gap-2 border-t border-zinc-800 py-2 text-[13px]">
			{#if data.agent.state === 'working'}
				<span class="relative flex h-2 w-2 flex-none self-center">
					<span class="absolute inline-flex h-full w-full animate-ping rounded-full bg-amber-400 opacity-60"></span>
					<span class="relative inline-flex h-2 w-2 rounded-full bg-amber-400"></span>
				</span>
				<span class="font-semibold text-amber-400">working</span>
				{#if data.agent.tool}
					<span class="truncate text-amber-300/90">
						⛭ {data.agent.tool}{data.agent.toolDetail ? ` — ${data.agent.toolDetail}` : ''}
					</span>
				{/if}
			{:else if data.agent.state === 'blocked?'}
				<span class="h-2 w-2 flex-none self-center animate-pulse rounded-full bg-rose-400"></span>
				<span class="font-semibold text-rose-400">possibly waiting on an approval</span>
				{#if data.agent.tool}
					<span class="truncate text-zinc-500">⛭ {data.agent.tool}{data.agent.toolDetail ? ` — ${data.agent.toolDetail}` : ''}</span>
				{/if}
			{:else if data.agent.state === 'needs you'}
				<span class="h-2 w-2 flex-none self-center rounded-full bg-sky-400"></span>
				<span class="font-semibold text-sky-400">waiting on you</span>
				{#if data.agent.tool}
					<span class="truncate text-zinc-600">last ⛭ {data.agent.tool}{data.agent.toolDetail ? ` — ${data.agent.toolDetail}` : ''}</span>
				{/if}
			{:else}
				<span class="h-2 w-2 flex-none self-center rounded-full bg-zinc-600"></span>
				<span class="text-zinc-500">quiet · {data.agent.age}</span>
				{#if data.agent.tool}
					<span class="truncate text-zinc-600">last ⛭ {data.agent.tool}{data.agent.toolDetail ? ` — ${data.agent.toolDetail}` : ''}</span>
				{/if}
			{/if}
		</div>
	{/if}

	<!-- composer -->
	<footer class="border-t border-zinc-800 py-3">
		{#if error}
			<div class="mb-2 text-xs text-rose-400">{error}</div>
		{/if}
		{#if attachments.length || uploadErr}
			<div class="mb-2 flex flex-wrap items-center gap-2">
				{#each attachments as a, ai (a.name)}
					<span class="relative inline-flex">
						<img src={a.preview} alt={a.name} class="h-11 rounded-md border border-sky-900" />
						<button
							onclick={() => dropAttachment(ai)}
							aria-label="drop this attachment"
							class="absolute -top-1.5 -right-1.5 h-4 w-4 rounded-full border border-zinc-700 bg-zinc-950 text-[10px] leading-none text-zinc-400"
							title="drop this attachment"
						>✕</button>
					</span>
				{/each}
				{#if uploadErr}
					<span class="text-[11px] text-rose-400">{uploadErr}</span>
				{/if}
			</div>
		{/if}
		<form class="flex items-end gap-2" onsubmit={send}>
			<textarea
				bind:value={text}
				onkeydown={onKeydown}
				onpaste={onPaste}
				rows="2"
				placeholder={driveMode
					? `a goal — the supervisor keeps pushing until it’s met (${data?.turns ?? 4} turns max)`
					: data?.pending?.status === 'thinking'
						? 'claude is mid-turn — the membrane waits for it to land (direct mode can queue)'
						: verbatim
							? 'your exact words go straight to claude'
							: 'tell vera what you want — it phrases the message for claude'}
				class="grow resize-none rounded-xl border border-zinc-800 bg-zinc-950 px-3 py-2 text-[13px] placeholder:text-zinc-600 focus:border-sky-400 focus:outline-none"
			></textarea>
			<div class="flex flex-col items-end gap-1.5">
				<div class="flex items-center gap-1">
					{#if !driveMode}
						<button
							type="button"
							onclick={() => (verbatim = !verbatim)}
							aria-pressed={verbatim}
							class="rounded-md px-2 py-0.5 text-[11px] {verbatim
								? 'bg-zinc-400/20 text-zinc-300'
								: 'text-zinc-600 hover:text-zinc-400'}"
							title="skip the membrane: send your words exactly as typed"
						>
							verbatim
						</button>
					{/if}
					<button
						type="button"
						onclick={() => (driveMode = !driveMode)}
						aria-pressed={driveMode}
						class="rounded-md px-2 py-0.5 text-[11px] {driveMode
							? 'bg-amber-400/20 text-amber-300'
							: 'text-zinc-600 hover:text-zinc-400'}"
						title="drive mode: a supervisor judges each reply against your goal and keeps pushing"
					>
						drive
					</button>
				</div>
				<button
					type="submit"
					disabled={sending || !text.trim() || !!activeDrive() || data?.pending?.status === 'thinking'}
					class="rounded-xl px-4 py-2 font-semibold text-zinc-950 disabled:opacity-40 {driveMode
						? 'bg-amber-400'
						: 'bg-sky-400'}"
				>
					{driveMode ? 'drive' : 'send'}
				</button>
			</div>
		</form>
	</footer>
	{/if}
</div>

{#if shelfOpen}
	<ArtifactPane agentId={id} onchanged={refresh} onclose={() => (shelfOpen = false)} />
{/if}
</div>
