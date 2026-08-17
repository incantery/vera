<script>
	// The map view: the whole kanban, one click from mission control.
	// Home answers "what needs me?"; this page answers "where is
	// everything?" — the backlog, the columns, the archive. Same
	// WatchBoard stream, same poll fallback.
	import { app, startPolling, watchBoard, boardFrame } from '$lib/state.svelte.js';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import Board from '$lib/Board.svelte';

	let board = $state(null);
	$effect(() => {
		// The stream retries like the agent page's does — the poll is a
		// bridge while it's down, not a permanent demotion.
		let stopPoll = null;
		let stop = null;
		let retry = null;
		let alive = true;
		const open = () => {
			stop = watchBoard(
				(f) => {
					const b = boardFrame(f);
					board = b;
					app.sessions = b.sessions;
					app.current = b.current;
					app.usage = b.usage;
					app.notice = b.notice;
					app.connected = true;
					app.loaded = true;
					stopPoll?.();
					stopPoll = null;
				},
				() => {
					board = null;
					stopPoll ??= startPolling();
					if (alive) retry = setTimeout(open, 5000);
				}
			);
		};
		open();
		return () => {
			alive = false;
			stop?.();
			clearTimeout(retry);
			stopPoll?.();
		};
	});
</script>

<div
	class="nk"
	style="height: 100dvh; display: flex; background: var(--color-bg); color: var(--color-text); font-family: var(--font-body);"
>
	<Board data={board} select={page.url.searchParams.get('task')} onexplore={() => goto('/?explore=1')} />
</div>
