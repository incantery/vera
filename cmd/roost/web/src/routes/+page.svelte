<script>
	// The home screen: ONE global board of work, with the agents rail
	// as fleet context and the door to each agent's conversation. Tasks
	// live on the board; agents are who they're assigned to.
	import { app, startPolling } from '$lib/state.svelte.js';
	import Board from '$lib/Board.svelte';
	import UsageBar from '$lib/UsageBar.svelte';

	$effect(() => startPolling());

	// The mono palette holds: states are accent-or-neutral, never a
	// second hue (nocturne's rule).
	function dot(s) {
		if (s.state === 'working') return 'var(--color-accent)';
		if (s.state === 'needs you' || s.state === 'blocked?') return 'var(--color-accent-300)';
		return 'var(--color-neutral-700)';
	}
</script>

<div
	class="nk"
	style="height: 100dvh; display: flex; background: var(--color-bg); color: var(--color-text); font-family: var(--font-body);"
>
	<section
		style="width: 210px; flex: 0 0 210px; border-right: 1px solid var(--color-divider); padding: 18px 12px 14px; display: flex; flex-direction: column; gap: 14px; min-height: 0;"
	>
		<div
			style="font-size: 10.5px; letter-spacing: 0.07em; text-transform: uppercase; color: var(--color-neutral-600); padding: 0 4px;"
		>
			agents
		</div>
		<div
			style="flex: 1; min-height: 0; overflow-y: auto; display: flex; flex-direction: column; gap: 2px;"
		>
			{#if !app.loaded}
				<div style="padding: 7px 9px; font-size: 12px; color: var(--color-neutral-600);">
					looking…
				</div>
			{:else if app.sessions.length === 0}
				<div
					style="padding: 7px 9px; font-size: 12px; color: var(--color-neutral-600); line-height: 1.5;"
				>
					no agents in the window — run `claude` somewhere and come back
				</div>
			{:else}
				{#each app.sessions as s (s.id)}
					<a
						href="/agent/{s.id}"
						style="display: flex; align-items: center; gap: 8px; width: 100%; text-decoration: none; padding: 7px 9px; border-radius: var(--radius-sm); font-size: 12.5px; background: {s.id ===
						app.current
							? 'var(--color-surface)'
							: 'transparent'}; color: {s.id === app.current
							? 'var(--color-neutral-100)'
							: 'var(--color-neutral-500)'};"
						title="{s.title} — {s.state} · open the conversation"
					>
						<span
							style="width: 5px; height: 5px; flex: none; border-radius: 99px; background: {dot(
								s
							)}; {s.id === app.current ? 'box-shadow: 0 0 8px var(--color-accent);' : ''}"
						></span>
						<span style="flex: none;">{s.dir}</span>
						<span
							style="min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 11px; color: var(--color-neutral-600);"
							>{s.title}</span
						>
						{#if s.id === app.current}
							<span
								style="margin-left: auto; flex: none; font-size: 10px; letter-spacing: 0.06em; color: var(--color-accent-300);"
								>now</span
							>
						{/if}
					</a>
				{/each}
			{/if}
		</div>
		{#if !app.connected}
			<div style="font-size: 11px; color: var(--color-accent-300); padding: 0 4px;">
				roost is not answering — is the binary still running?
			</div>
		{/if}
		{#if app.notice}
			<div
				style="font-size: 11px; color: var(--color-neutral-500); line-height: 1.5; padding: 0 4px;"
			>
				{app.notice}
			</div>
		{/if}
		<div style="padding: 0 4px;">
			<UsageBar usage={app.usage} />
		</div>
	</section>

	<Board />
</div>
