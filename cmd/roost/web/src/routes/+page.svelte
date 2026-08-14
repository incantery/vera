<script>
	// The home screen: ONE global board of work, with the agents rail
	// ranked by relevance to that work — agents carrying open tasks
	// first, then live sessions, with everything stale folded away
	// rather than presented as an equal. The rail is navigation: each
	// row opens that agent's conversation.
	import { app, startPolling, watchBoard, boardFrame } from '$lib/state.svelte.js';
	import Board from '$lib/Board.svelte';
	import UsageBar from '$lib/UsageBar.svelte';

	// The home screen lives on the WatchBoard stream: one frame
	// whenever anything changes, no polling. If the stream dies, the
	// old poll rail takes over — same shapes, slower truth.
	let board = $state(null);
	$effect(() => {
		let stopPoll = null;
		const stop = watchBoard(
			(f) => {
				const b = boardFrame(f);
				board = b;
				app.sessions = b.sessions;
				app.current = b.current;
				app.usage = b.usage;
				app.notice = b.notice;
				app.connected = true;
				app.loaded = true;
			},
			() => {
				board = null;
				stopPoll = startPolling();
			}
		);
		return () => {
			stop();
			stopPoll?.();
		};
	});

	let showIdle = $state(false);

	// The three tiers of relevance. Within each, the server's order
	// stands (needs-you first, then recency).
	const onTask = $derived(app.sessions.filter((s) => s.task));
	const active = $derived(app.sessions.filter((s) => !s.task && s.state !== 'idle'));
	const idle = $derived(app.sessions.filter((s) => !s.task && s.state === 'idle'));

	// The mono palette holds: states are accent-or-neutral, never a
	// second hue (nocturne's rule).
	function dot(s) {
		if (s.state === 'working') return 'var(--color-accent)';
		if (s.state === 'needs you' || s.state === 'blocked?') return 'var(--color-accent-300)';
		return 'var(--color-neutral-700)';
	}
</script>

{#snippet agentRow(s)}
	<a
		href="/agent/{s.id}"
		style="display: flex; align-items: center; gap: 7px; width: 100%; text-decoration: none; padding: 6px 9px; border-radius: var(--radius-sm); font-size: 12.5px; background: {s.id ===
		app.current
			? 'var(--color-surface)'
			: 'transparent'}; color: {s.id === app.current
			? 'var(--color-neutral-100)'
			: 'var(--color-neutral-400)'};"
		title="{s.title} — {s.state} · open the conversation"
	>
		<span
			style="width: 5px; height: 5px; flex: none; border-radius: 99px; background: {dot(s)}; {s.id ===
			app.current
				? 'box-shadow: 0 0 8px var(--color-accent);'
				: ''}"
		></span>
		<span
			style="min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; flex: 1;"
		>
			{s.dir}{s.scratch ? ' ⌂' : ''}
			<span style="font-size: 11px; color: var(--color-neutral-600);">· {s.title}</span>
		</span>
		{#if s.task}
			<span
				style="flex: none; font-size: 10px; font-variant-numeric: tabular-nums; color: var(--color-accent-300); border: 1px solid var(--color-accent-800); border-radius: 4px; padding: 0 4px;"
				>{s.task}</span
			>
		{:else if s.id === app.current}
			<span
				style="flex: none; font-size: 10px; letter-spacing: 0.06em; color: var(--color-accent-300);"
				>now</span
			>
		{/if}
	</a>
{/snippet}

{#snippet railHeading(text)}
	<div
		style="font-size: 10.5px; letter-spacing: 0.07em; text-transform: uppercase; color: var(--color-neutral-600); padding: 8px 4px 4px;"
	>
		{text}
	</div>
{/snippet}

<div
	class="nk"
	style="height: 100dvh; display: flex; background: var(--color-bg); color: var(--color-text); font-family: var(--font-body);"
>
	<section
		style="width: 224px; flex: 0 0 224px; border-right: 1px solid var(--color-divider); padding: 14px 12px 14px; display: flex; flex-direction: column; gap: 4px; min-height: 0;"
	>
		<div style="flex: 1; min-height: 0; overflow-y: auto; display: flex; flex-direction: column; gap: 2px;">
			{#if !app.loaded}
				<div style="padding: 7px 9px; font-size: 12px; color: var(--color-neutral-600);">looking…</div>
			{:else}
				{@render railHeading(`on task · ${onTask.length}`)}
				{#if onTask.length === 0}
					<div style="padding: 2px 9px 6px; font-size: 11px; color: var(--color-neutral-700); line-height: 1.5;">
						no agent carries a task yet — capture one on the board and press Start drive
					</div>
				{:else}
					{#each onTask as s (s.id)}
						{@render agentRow(s)}
					{/each}
				{/if}

				{#if active.length}
					{@render railHeading(`active sessions · ${active.length}`)}
					{#each active as s (s.id)}
						{@render agentRow(s)}
					{/each}
				{/if}

				{#if idle.length}
					<button
						onclick={() => (showIdle = !showIdle)}
						style="display: flex; align-items: center; gap: 6px; cursor: pointer; background: none; border: none; font: inherit; font-size: 10.5px; letter-spacing: 0.07em; text-transform: uppercase; color: var(--color-neutral-600); padding: 8px 4px 4px; text-align: left;"
						title="quiet sessions from the last 48h — probably not what you're looking for"
					>
						<span style="display: inline-block; transform: rotate({showIdle ? 90 : 0}deg); transition: transform 0.12s;">›</span>
						idle · {idle.length}
					</button>
					{#if showIdle}
						{#each idle as s (s.id)}
							{@render agentRow(s)}
						{/each}
					{/if}
				{/if}

				{#if app.sessions.length === 0}
					<div style="padding: 7px 9px; font-size: 12px; color: var(--color-neutral-600); line-height: 1.5;">
						no sessions in the window — the board can still start fresh agents in a scratch workspace
					</div>
				{/if}
			{/if}
		</div>
		{#if !app.connected}
			<div style="font-size: 11px; color: var(--color-accent-300); padding: 0 4px;">
				roost is not answering — is the binary still running?
			</div>
		{/if}
		{#if app.notice}
			<div style="font-size: 11px; color: var(--color-neutral-500); line-height: 1.5; padding: 0 4px;">
				{app.notice}
			</div>
		{/if}
		<div style="padding: 0 4px;">
			<UsageBar usage={app.usage} />
		</div>
	</section>

	<Board data={board} />
</div>
