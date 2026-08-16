<script>
	// The login screen: the human way through the LAN door when the
	// ?key= URL is out of reach — a different browser, a cleared stash,
	// a bare bookmark. One field, one try, back to where you were going.
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { setKey, checkAuth } from '$lib/state.svelte.js';

	let pass = $state('');
	let error = $state('');
	let busy = $state(false);

	// ?next= is trusted only as an in-app path — anything else goes home.
	function dest() {
		const n = page.url.searchParams.get('next') ?? '/';
		return n.startsWith('/') && !n.startsWith('//') ? n : '/';
	}

	async function submit(e) {
		e.preventDefault();
		if (busy) return;
		busy = true;
		error = '';
		setKey(pass.trim());
		const v = await checkAuth();
		busy = false;
		if (v === 'ok') goto(dest());
		else if (v === 'denied') error = 'that is not the key';
		else error = 'vera is not answering — is the binary still running?';
	}
</script>

<div
	class="nk"
	style="height: 100dvh; display: flex; align-items: center; justify-content: center; background: var(--color-bg); color: var(--color-text); font-family: var(--font-body);"
>
	<form
		onsubmit={submit}
		style="width: min(300px, calc(100vw - 40px)); display: flex; flex-direction: column; gap: 14px; padding: 26px 24px; background: var(--color-surface); border: 1px solid var(--color-divider); border-radius: var(--radius-lg);"
	>
		<div style="display: flex; align-items: baseline; gap: 8px;">
			<span style="font-family: var(--font-heading); font-weight: 600; font-size: 16px;">vera</span>
			<span
				style="font-size: 10.5px; letter-spacing: 0.07em; text-transform: uppercase; color: var(--color-neutral-600);"
				>key required</span
			>
		</div>
		<div style="font-size: 12px; color: var(--color-neutral-500); line-height: 1.5;">
			this vera serves beyond the machine — enter the key it printed at startup
		</div>
		<!-- svelte-ignore a11y_autofocus -->
		<input
			class="input"
			type="password"
			placeholder="key"
			autofocus
			bind:value={pass}
			disabled={busy}
		/>
		{#if error}
			<div role="alert" style="font-size: 12px; color: var(--ev-del); line-height: 1.5;">{error}</div>
		{/if}
		<button class="btn btn-primary" type="submit" disabled={busy || !pass.trim()}>
			{busy ? 'trying…' : 'open'}
		</button>
	</form>
</div>
