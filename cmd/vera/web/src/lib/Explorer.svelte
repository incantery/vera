<script>
	// The explorer: browse the machine's directories from the board and
	// say the first word anywhere — a fresh claude session is born there
	// and the direct cockpit opens on it. The server fences the walk to
	// the home dir (or the world); this panel just walks it.
	import { api } from '$lib/state.svelte.js';
	import { goto } from '$app/navigation';

	let { onclose } = $props();
	let view = $state(null); // {root, path, parent, git, dirs}
	let err = $state('');
	let text = $state('');
	let perm = $state('read');
	let birth = $state(null); // {id, status}
	let busy = $state(false);
	let pollT = null;

	async function load(path = '') {
		err = '';
		try {
			const r = await api('/api/dirs?path=' + encodeURIComponent(path));
			const out = await r.json();
			if (!r.ok) throw new Error(out.error ?? 'cannot browse');
			view = out;
		} catch (e) {
			err = e.message;
		}
	}
	$effect(() => {
		load();
		return () => clearInterval(pollT);
	});

	async function start() {
		const t = text.trim();
		if (!t || busy || !view) return;
		busy = true;
		err = '';
		try {
			const r = await api('/api/sessions', {
				method: 'POST',
				body: JSON.stringify({ cwd: view.path, text: t, perm })
			});
			const out = await r.json();
			if (!r.ok) throw new Error(out.error ?? 'refused');
			birth = { id: out.id, status: 'thinking' };
			pollT = setInterval(poll, 700);
		} catch (e) {
			err = e.message;
			busy = false;
		}
	}
	async function poll() {
		try {
			const r = await api('/api/births/' + birth.id);
			const out = await r.json();
			if (!r.ok) throw new Error(out.error ?? 'lost the birth ticket');
			if (out.status === 'born') {
				clearInterval(pollT);
				goto('/agent/' + out.root + '?mode=direct');
				onclose();
			} else if (out.status === 'failed') {
				clearInterval(pollT);
				err = out.err || 'the birth failed';
				busy = false;
				birth = null;
			}
		} catch (e) {
			clearInterval(pollT);
			err = e.message;
			busy = false;
			birth = null;
		}
	}
	function onKey(e) {
		if (e.key === 'Escape' && !busy) onclose();
	}
	const crumb = (v) => (v.path === v.root ? '~' : '~' + v.path.slice(v.root.length));
</script>

<svelte:window onkeydown={onKey} />

<div
	style="position: fixed; inset: 0; z-index: 60; background: rgba(0,0,0,0.55); display: flex; align-items: center; justify-content: center;"
	onclick={(e) => e.target === e.currentTarget && !busy && onclose()}
	role="presentation"
>
	<section
		style="width: min(680px, 92vw); max-height: 80vh; display: flex; flex-direction: column; background: var(--color-neutral-950); border: 1px solid var(--color-neutral-800); border-radius: var(--radius-lg, 10px); overflow: hidden;"
	>
		<header
			style="display: flex; align-items: baseline; gap: 10px; padding: 12px 16px; border-bottom: 1px solid var(--color-divider);"
		>
			<span
				style="font-family: var(--font-mono, monospace); font-size: 10px; letter-spacing: 0.12em; color: var(--color-accent-300);"
				>EXPLORER</span
			>
			{#if view}
				<span
					style="font-family: var(--font-mono, monospace); font-size: 12px; color: var(--color-neutral-300); overflow: hidden; text-overflow: ellipsis; white-space: nowrap;"
					>{crumb(view)}</span
				>
				{#if view.git}
					<span style="font-size: 10.5px; color: var(--color-accent-400);">git</span>
				{/if}
			{/if}
			<div style="flex: 1;"></div>
			<span style="font-size: 10.5px; color: var(--color-neutral-600);">esc closes</span>
		</header>

		{#if err}
			<div
				style="padding: 8px 16px; font-size: 12px; color: var(--color-accent-200); background: var(--color-accent-900); border-bottom: 1px solid var(--color-accent-700);"
			>
				{err}
			</div>
		{/if}

		<div style="flex: 1; min-height: 120px; overflow-y: auto; padding: 6px 8px;">
			{#if view?.parent}
				<button
					style="display: flex; width: 100%; gap: 8px; padding: 7px 10px; border-radius: var(--radius-sm, 6px); font-family: var(--font-mono, monospace); font-size: 12.5px; color: var(--color-neutral-500); text-align: left;"
					onclick={() => load(view.parent)}>..</button
				>
			{/if}
			{#each view?.dirs ?? [] as d (d.cwd)}
				<button
					style="display: flex; align-items: baseline; width: 100%; gap: 8px; padding: 7px 10px; border-radius: var(--radius-sm, 6px); font-size: 13px; color: var(--color-neutral-200); text-align: left;"
					onclick={() => load(d.cwd)}
				>
					<span style="flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;"
						>{d.name}/</span
					>
					{#if d.known}
						<span
							style="width: 5px; height: 5px; align-self: center; border-radius: 99px; background: var(--color-accent);"
							title="the fleet has a session here"
						></span>
					{/if}
					{#if d.git}
						<span style="font-size: 10.5px; color: var(--color-neutral-500);">git</span>
					{/if}
				</button>
			{:else}
				<div style="padding: 14px 10px; font-size: 12px; color: var(--color-neutral-600);">
					no directories under here — this is ground, not a hallway
				</div>
			{/each}
		</div>

		<footer style="border-top: 1px solid var(--color-divider); padding: 12px 16px;">
			{#if birth}
				<div style="font-size: 12.5px; color: var(--color-neutral-400); line-height: 1.5;">
					a session is being born in
					<span style="font-family: var(--font-mono, monospace); color: var(--color-neutral-200);"
						>{crumb(view)}</span
					>
					— the cockpit opens when it has a name…
				</div>
			{:else}
				<div style="font-size: 11px; color: var(--color-neutral-500); margin-bottom: 8px;">
					start a session in <span
						style="font-family: var(--font-mono, monospace); color: var(--color-neutral-300);"
						>{view ? crumb(view) : '…'}</span
					> — the first message births it, direct from the first word
				</div>
				<div style="display: flex; gap: 8px; align-items: stretch;">
					<input
						class="input"
						bind:value={text}
						onkeydown={(e) => e.key === 'Enter' && start()}
						placeholder="the first message…"
						style="flex: 1; background: transparent;"
					/>
					<select
						class="input"
						bind:value={perm}
						title="tool policy: read (mutations refused) · edit (files + scoped build/test) · all (no gate)"
						style="background: transparent; width: auto;"
					>
						<option value="read">read</option>
						<option value="edit">edit</option>
						<option value="all">all</option>
					</select>
					<button class="btn btn-primary" onclick={start} disabled={busy || !text.trim()}
						>start here</button
					>
				</div>
			{/if}
		</footer>
	</section>
</div>
