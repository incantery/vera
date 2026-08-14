<script>
	// The layout is the door frame: no route renders until the key has
	// been tried against /api/auth. Denied → the login screen, carrying
	// where you were headed. Loopback answers 'ok' keyless, and a dead
	// server renders anyway — the pages own their "not answering" line.
	import '../app.css';
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { checkAuth } from '$lib/state.svelte.js';

	let { children } = $props();
	let authed = $state(false);

	$effect(() => {
		if (authed || page.url.pathname === '/login') return;
		const dest = page.url.pathname;
		checkAuth().then((v) => {
			if (v === 'denied') goto(`/login?next=${encodeURIComponent(dest)}`);
			else authed = true;
		});
	});
</script>

{#if authed || page.url.pathname === '/login'}
	{@render children()}
{/if}
