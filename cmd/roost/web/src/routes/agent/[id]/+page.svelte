<script>
	import { page } from '$app/state';
	import { fetchAgent, sayTo, startDrive, stopDrive } from '$lib/state.svelte.js';
	import UsageBar from '$lib/UsageBar.svelte';
	import ArtifactPane from '$lib/ArtifactPane.svelte';

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

	async function refresh() {
		try {
			data = await fetchAgent(id);
			lost = '';
		} catch (e) {
			lost = e.message;
		}
	}

	$effect(() => {
		id; // re-arm when the route changes
		refresh();
		const t = setInterval(refresh, 3000);
		return () => clearInterval(t);
	});

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
			await sayTo(id, '/compact', true);
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
		if (!text.trim() || sending) return;
		sending = true;
		error = '';
		try {
			if (driveMode) await startDrive(id, text);
			else await sayTo(id, text, verbatim);
			text = '';
			nearBottom = true;
			await refresh();
		} catch (err) {
			error = err.message;
		} finally {
			sending = false;
		}
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

<div class="mx-auto flex h-dvh gap-4 px-4 {shelfOpen ? 'max-w-6xl' : 'max-w-3xl'}">
<div class="flex h-dvh min-w-0 grow flex-col">
	<!-- header -->
	<header class="flex flex-wrap items-baseline gap-x-3 gap-y-1 border-b border-zinc-800 py-3">
		<a href="/" class="text-zinc-500 hover:text-zinc-300">←</a>
		{#if data}
			<span class="font-semibold">{data.agent.title}</span>
			<span class="text-xs text-zinc-500">
				{data.agent.dir}{data.agent.branch ? ` · ${data.agent.branch}` : ''}
			</span>
			<span class="grow"></span>
			<a
				href="/"
				class="text-xs text-zinc-500 hover:text-zinc-300"
				title="the board: tell rook what needs doing, it keeps the columns"
			>
				⌗ board
			</a>
			<button
				class="text-xs {shelfOpen ? 'text-sky-400' : 'text-zinc-500 hover:text-zinc-300'}"
				onclick={() => (shelfOpen = !shelfOpen)}
				title="the artifact shelf: documents that belong to this agent"
			>
				⧉ artifacts{data.artifacts ? ` (${data.artifacts})` : ''}
			</button>
			{#if spendTotal}
				<span
					class="text-xs text-zinc-500"
					title="spent on this agent by roost: claude ${(data.spend.claudeUsd ?? 0).toFixed(4)} + judge ${(data.spend.judgeUsd ?? 0).toFixed(4)}"
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
		{:else}
			<span class="text-zinc-500">…</span>
		{/if}
	</header>
	{#if data?.usage}
		<div class="border-b border-zinc-800/60 py-1.5">
			<UsageBar usage={data.usage} />
		</div>
	{/if}

	<!-- the session strip: what this conversation costs and the
	     controls it supports — inside the column, the margins keep
	     their silence -->
	{#if data}
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
			<span title="what roost has spent on this agent this process: claude-metered turns + the rook agent's own calls">
				claude <b class="font-medium text-zinc-300">${(data.spend?.claudeUsd ?? 0).toFixed(2)}</b>
				· rook <b class="font-medium text-zinc-300">${(data.spend?.judgeUsd ?? 0).toFixed(2)}</b>
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
				title="copy `claude --resume {data.resume}` — take this conversation to a terminal"
			>
				copy resume
			</button>
		</div>
	{/if}

	{#if lost}
		<div class="mt-3 rounded-lg border border-rose-900 bg-rose-950/40 px-3 py-2 text-[13px] text-rose-300">
			{lost}
		</div>
	{/if}

	<!-- conversation -->
	<div bind:this={chatEl} onscroll={onScroll} class="grow space-y-4 overflow-y-auto py-4">
		{#if data?.history?.length}
			{#each data.history as m, i (i)}
				{#if m.role === 'user'}
					<div class="flex flex-col items-end">
						<div
							class="max-w-[85%] rounded-2xl rounded-br-md border border-sky-900/60 bg-sky-950/40 px-4 py-2.5 text-[13px] whitespace-pre-wrap"
						>
							{m.rough || m.text}
						</div>
						{#if m.rough && m.rough !== m.text}
							<details class="mt-1 max-w-[85%] text-right">
								<summary class="cursor-pointer text-[11px] text-zinc-600 hover:text-zinc-400">
									⇒ what rook sent
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
						{#if m.tools}
							<div class="mb-1 text-[11px] text-zinc-600">⛭ {m.tools} tool {m.tools === 1 ? 'call' : 'calls'}</div>
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
								<summary class="cursor-pointer text-[11px] text-zinc-600 hover:text-zinc-400">
									full reply
								</summary>
								<div class="mt-1 border-l-2 border-zinc-800 pl-3 text-[13px] leading-relaxed whitespace-pre-wrap text-zinc-400">
									{m.text}
								</div>
							</details>
						{:else}
							{#if m.digest?.state === 'pending'}
								<div class="mb-1 animate-pulse text-[11px] text-zinc-600">summarizing…</div>
							{/if}
							{#if m.text}
								<div class="text-[13px] leading-relaxed whitespace-pre-wrap">{m.text}</div>
							{/if}
						{/if}
					</div>
				{/if}
			{/each}
		{:else if data}
			<div class="py-8 text-center text-[13px] text-zinc-600">no conversation on record yet</div>
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
						<summary class="cursor-pointer text-[11px] text-zinc-600 hover:text-zinc-400">
							⇒ what rook sent
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
				<div class="animate-pulse text-[13px] text-zinc-500">rook is phrasing it…</div>
			{:else if data.pending.status === 'thinking'}
				<div class="animate-pulse text-[13px] text-zinc-500">thinking…</div>
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
		<form class="flex items-end gap-2" onsubmit={send}>
			<textarea
				bind:value={text}
				onkeydown={onKeydown}
				rows="2"
				placeholder={driveMode
					? `a goal — the supervisor keeps pushing until it’s met (${data?.turns ?? 4} turns max)`
					: verbatim
						? 'your exact words go straight to claude'
						: 'tell rook what you want — it phrases the message for claude'}
				class="grow resize-none rounded-xl border border-zinc-800 bg-zinc-950 px-3 py-2 text-[13px] placeholder:text-zinc-600 focus:border-sky-400 focus:outline-none"
			></textarea>
			<div class="flex flex-col items-end gap-1.5">
				<div class="flex gap-1">
					{#if !driveMode}
						<button
							type="button"
							onclick={() => (verbatim = !verbatim)}
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
</div>

{#if shelfOpen}
	<ArtifactPane agentId={id} onchanged={refresh} />
{/if}
</div>
