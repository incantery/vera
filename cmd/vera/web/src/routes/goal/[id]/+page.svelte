<script>
	// The work view's route: one goal's live choreography. Same stream
	// discipline as the board — retry on drop, and say so rather than
	// silently showing a frozen frame, because a work view that has
	// stopped updating looks exactly like work that has stopped.
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { watchGoal, seenCursor } from '$lib/state.svelte.js';
	import Work from '$lib/Work.svelte';

	let goal = $state(null);
	let live = $state(false);
	// Read once, on arrival: the marks must not move under the reader
	// while they are looking at them.
	let seen = $state(0);

	$effect(() => {
		const id = page.params.id;
		seen = seenCursor(id);
		let stop = null;
		let retry = null;
		let alive = true;
		const open = () => {
			stop = watchGoal(
				id,
				(f) => {
					goal = f;
					live = true;
				},
				() => {
					live = false;
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
</script>

<div class="shell">
	{#if !live && goal}
		<div class="stale">Reconnecting — this is the last frame, not the present.</div>
	{/if}
	<Work
		{goal}
		{seen}
		onopen={(nodeId) => goto(`/board?task=${encodeURIComponent(nodeId)}`)}
		onback={() => goto('/')}
	/>
</div>

<style>
	.shell {
		height: 100dvh;
		display: flex;
		flex-direction: column;
		background: var(--color-bg);
		color: var(--color-text);
		font-family: var(--font-body);
	}
	.stale {
		background: var(--ev-sh-fill);
		color: var(--ev-sh);
		font-size: 0.76rem;
		padding: 0.4rem 0.75rem;
		text-align: center;
		flex: none;
	}
</style>
