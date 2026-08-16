<script>
	// The map view: the whole kanban, one click from mission control.
	// Home answers "what needs me?"; this page answers "where is
	// everything?" — the backlog, the columns, the archive. Same
	// WatchBoard stream, same poll fallback.
	import { app, startPolling, watchBoard, boardFrame } from '$lib/state.svelte.js';
	import { goto } from '$app/navigation';
	import Board from '$lib/Board.svelte';

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
</script>

<div
	class="nk"
	style="height: 100dvh; display: flex; background: var(--color-bg); color: var(--color-text); font-family: var(--font-body);"
>
	<Board data={board} onexplore={() => goto('/?explore=1')} />
</div>
