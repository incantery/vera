<script>
	// Home is the Vera surface (the "Vera" design): threads grouped by
	// owner, a state-first detail pane, one composer. The WatchBoard
	// stream feeds it one frame whenever anything changes; if the
	// stream dies, the old poll rail takes over — same shapes, slower
	// truth. The kanban lives on at /board; the directory explorer at
	// /explore.
	import { goto } from '$app/navigation';
	import { app, startPolling, watchBoard, boardFrame } from '$lib/state.svelte.js';
	import Vera from '$lib/Vera.svelte';

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

	// old deep links keep working: ?explore=1 was the left panel's mode
	$effect(() => {
		if (new URLSearchParams(location.search).get('explore') === '1') goto('/explore');
	});
</script>

<Vera data={board} />
