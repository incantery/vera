<script>
	// The explorer: a first-class mode of the left panel. Browse the
	// machine's directories and say the first word anywhere — a fresh
	// claude session is born there and the direct cockpit opens on it.
	// The server fences the walk to the home dir (or the world); this
	// panel just walks it. `onclose` hands the panel back to the agents
	// rail (esc does the same).
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

	// Bookmarking: name this ground once and the planner's offers and
	// the board's pickers carry it forever after.
	let marking = $state(false);
	let markName = $state('');
	let markNote = $state('');
	async function saveMark() {
		const name = markName.trim();
		if (!name || !view) return;
		err = '';
		try {
			const r = await api('/api/bookmarks', {
				method: 'POST',
				body: JSON.stringify({ name, cwd: view.path, note: markNote.trim() })
			});
			const out = await r.json();
			if (!r.ok) throw new Error(out.error ?? 'refused');
			marking = false;
			markName = '';
			markNote = '';
			await load(view.path);
		} catch (e) {
			err = e.message;
		}
	}
	const crumb = (v) => (v.path === v.root ? '~' : '~' + v.path.slice(v.root.length));
</script>

<svelte:window onkeydown={onKey} />

<div style="flex: 1; min-height: 0; display: flex; flex-direction: column;">
	<header
		style="display: flex; flex-direction: column; gap: 6px; padding: 4px 4px 10px; border-bottom: 1px solid var(--color-divider);"
	>
		<div style="display: flex; align-items: center; gap: 8px;">
			{#if view}
				<span
					style="flex: 1; min-width: 0; font-family: var(--font-mono, monospace); font-size: 12px; color: var(--color-neutral-300); overflow: hidden; text-overflow: ellipsis; white-space: nowrap;"
					title={view.path}>{crumb(view)}</span
				>
				{#if view.git}
					<span style="flex: none; font-size: 10.5px; color: var(--color-accent-400);">git</span>
				{/if}
			{:else}
				<span style="flex: 1; font-size: 12px; color: var(--color-neutral-600);">looking…</span>
			{/if}
			{#if view?.marked}
				<span
					style="flex: none; font-size: 10.5px; color: var(--color-accent-400);"
					title="bookmarked — the planner knows this ground">★ {view.marked}</span
				>
			{:else if view}
				<button
					style="flex: none; cursor: pointer; background: none; border: none; font: inherit; font-size: 10.5px; color: var(--color-neutral-500); padding: 0;"
					onclick={() => {
						marking = !marking;
						markName = view.path === view.root ? '' : view.path.split('/').pop();
					}}
					title="bookmark this directory — named ground the planner can trust">☆ bookmark</button
				>
			{/if}
		</div>
	</header>

	{#if marking}
		<div
			style="display: flex; flex-direction: column; gap: 6px; padding: 8px 4px; border-bottom: 1px solid var(--color-divider);"
		>
			<input
				class="input"
				bind:value={markName}
				placeholder="name"
				style="background: transparent; font-size: 12.5px;"
			/>
			<input
				class="input"
				bind:value={markNote}
				onkeydown={(e) => e.key === 'Enter' && saveMark()}
				placeholder="one line the planner will read — what is this workspace?"
				style="background: transparent; font-size: 12.5px;"
			/>
			<div style="display: flex;">
				<button
					class="btn btn-primary"
					style="font-size: 12px;"
					onclick={saveMark}
					disabled={!markName.trim()}>save</button
				>
			</div>
		</div>
	{/if}

	{#if view?.bookmarks?.length}
		<div
			style="display: flex; gap: 6px; flex-wrap: wrap; padding: 8px 4px; border-bottom: 1px solid var(--color-divider);"
		>
			{#each view.bookmarks as bm (bm.name)}
				<button
					class="bm-chip"
					style="cursor: pointer; background: none; font: inherit; font-size: 11px; padding: 3px 9px; border: 1px solid var(--color-neutral-800); border-radius: 99px; color: var(--color-neutral-300);"
					onclick={() => load(bm.cwd)}
					title={bm.note || bm.cwd}>★ {bm.name}</button
				>
			{/each}
		</div>
	{/if}

	{#if err}
		<div
			role="alert"
			style="margin: 8px 0 0; padding: 7px 10px; font-size: 11.5px; line-height: 1.5; color: var(--ev-del); background: var(--ev-del-fill); border: 1px solid var(--ev-del-edge); border-radius: var(--radius-sm, 6px);"
		>
			{err}
		</div>
	{/if}

	<div style="flex: 1; min-height: 120px; overflow-y: auto; padding: 6px 0;">
		{#if view?.parent}
			<button
				class="dir-row"
				aria-label="up to the parent directory"
				style="display: flex; width: 100%; gap: 8px; padding: 6px 9px; border-radius: var(--radius-sm, 6px); cursor: pointer; background: none; border: none; font-family: var(--font-mono, monospace); font-size: 12.5px; color: var(--color-neutral-500); text-align: left;"
				onclick={() => load(view.parent)}>..</button
			>
		{/if}
		{#each view?.dirs ?? [] as d (d.cwd)}
			<button
				class="dir-row"
				style="display: flex; align-items: baseline; width: 100%; gap: 8px; padding: 6px 9px; border-radius: var(--radius-sm, 6px); cursor: pointer; background: none; border: none; font: inherit; font-size: 12.5px; color: var(--color-neutral-200); text-align: left;"
				onclick={() => load(d.cwd)}
			>
				<span style="flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;"
					>{d.name}/</span
				>
				{#if d.known}
					<span
						style="width: 5px; height: 5px; flex: none; align-self: center; border-radius: 99px; background: var(--color-accent);"
						title="the fleet has a session here"
					></span>
				{/if}
				{#if d.git}
					<span style="flex: none; font-size: 10.5px; color: var(--color-neutral-500);">git</span>
				{/if}
			</button>
		{:else}
			<div style="padding: 14px 9px; font-size: 12px; color: var(--color-neutral-600); line-height: 1.5;">
				no directories under here — this is ground, not a hallway
			</div>
		{/each}
	</div>

	<footer
		style="border-top: 1px solid var(--color-divider); padding: 10px 4px 0; display: flex; flex-direction: column; gap: 8px;"
	>
		{#if birth}
			<div style="font-size: 12px; color: var(--color-neutral-400); line-height: 1.5;">
				a session is being born in
				<span style="font-family: var(--font-mono, monospace); color: var(--color-neutral-200);"
					>{crumb(view)}</span
				>
				— the cockpit opens when it has a name…
			</div>
		{:else}
			<div style="font-size: 11px; color: var(--color-neutral-500); line-height: 1.5;">
				start a session in <span
					style="font-family: var(--font-mono, monospace); color: var(--color-neutral-300);"
					>{view ? crumb(view) : '…'}</span
				> — the first message births it, direct from the first word
			</div>
			<input
				class="input"
				bind:value={text}
				onkeydown={(e) => e.key === 'Enter' && start()}
				placeholder="the first message…"
				style="background: transparent; font-size: 12.5px;"
			/>
			<div style="display: flex; gap: 8px; align-items: stretch;">
				<select
					class="input"
					bind:value={perm}
					title="tool policy: read (mutations refused) · edit (files + scoped build/test) · all (no gate)"
					style="background: transparent; flex: 1; width: auto; font-size: 12px;"
				>
					<option value="read">read</option>
					<option value="edit">edit</option>
					<option value="all">all</option>
				</select>
				<button
					class="btn btn-primary"
					style="font-size: 12px;"
					onclick={start}
					disabled={busy || !text.trim()}>start here</button
				>
			</div>
		{/if}
	</footer>
</div>

<style>
	/* rows and chips answer the pointer — the walk should feel held */
	.dir-row:hover {
		background: var(--color-neutral-900);
	}
	.bm-chip:hover {
		border-color: var(--color-neutral-600);
		color: var(--color-neutral-100);
	}
	/* fingers get taller rows than mouse pointers do */
	@media (pointer: coarse) {
		.dir-row {
			padding-top: 11px !important;
			padding-bottom: 11px !important;
		}
		.bm-chip {
			padding: 7px 12px !important;
		}
	}
</style>
