<script>
	// The schedule, on its own ground: work that starts because its
	// time came. List the standing arrangements, lay a new one, remove
	// one — each entry births a board card when due, so this page is
	// about WHEN, and the board stays the record of WHAT happened.
	import { api } from '$lib/state.svelte.js';

	let entries = $state([]);
	let repos = $state([]);
	let err = $state('');
	let busy = $state(false);

	// The form: intent, ground, cadence. Cadence is a preset — once /
	// hourly / daily / weekly — plus an optional starting time.
	let intent = $state('');
	let workspace = $state('');
	let mode = $state('read');
	let cadence = $state('daily');
	let startAt = $state(''); // datetime-local; '' = one interval from now
	const EVERY = { hourly: '1h', daily: '24h', weekly: '168h' };

	async function refresh() {
		try {
			const r = await api('/api/schedule');
			const out = await r.json();
			if (!r.ok) throw new Error(out.error ?? 'vera did not answer');
			entries = out.entries ?? [];
			err = '';
		} catch (e) {
			err = e.message;
		}
	}
	async function fetchRepos() {
		try {
			const r = await api('/api/tasks');
			if (!r.ok) return;
			repos = (await r.json()).repos ?? [];
			if (!workspace && repos.length) workspace = repos[0].cwd;
		} catch {
			// the picker just starts empty
		}
	}
	$effect(() => {
		refresh();
		fetchRepos();
		const t = setInterval(refresh, 15000);
		return () => clearInterval(t);
	});

	async function add() {
		if (!intent.trim() || !workspace.trim()) {
			err = 'say what needs doing, and where';
			return;
		}
		if (cadence === 'once' && !startAt) {
			err = 'a one-shot needs its time';
			return;
		}
		busy = true;
		err = '';
		try {
			const body = { intent: intent.trim(), workspace: workspace.trim(), mode };
			if (cadence !== 'once') body.every = EVERY[cadence];
			if (startAt) body.at = new Date(startAt).toISOString();
			const r = await api('/api/schedule', { method: 'POST', body: JSON.stringify(body) });
			const out = await r.json();
			if (!r.ok) throw new Error(out.error ?? 'refused');
			intent = '';
			startAt = '';
			await refresh();
		} catch (e) {
			err = e.message;
		} finally {
			busy = false;
		}
	}

	// Removing is forever, so it earns the app's two-step: the first
	// click arms, the second commits — same as every other deletion.
	let confirmRm = $state(null); // entry id awaiting the second click
	async function remove(id) {
		if (confirmRm !== id) {
			confirmRm = id;
			return;
		}
		confirmRm = null;
		busy = true;
		try {
			const r = await api('/api/schedule/' + id, { method: 'DELETE' });
			if (!r.ok) throw new Error((await r.json()).error ?? 'refused');
			await refresh();
		} catch (e) {
			err = e.message;
		} finally {
			busy = false;
		}
	}

	async function resume(id) {
		busy = true;
		try {
			const r = await api('/api/schedule/' + id + '/resume', { method: 'POST', body: '{}' });
			if (!r.ok) throw new Error((await r.json()).error ?? 'refused');
			await refresh();
		} catch (e) {
			err = e.message;
		} finally {
			busy = false;
		}
	}

	function when(e) {
		const at = new Date(e.at);
		if (e.every) return 'every ' + e.every + (e.lastRun && !e.lastRun.startsWith('0001') ? '' : ' · first ' + at.toLocaleString());
		return 'once · ' + at.toLocaleString();
	}
	function lastFired(e) {
		if (!e.lastRun || e.lastRun.startsWith('0001')) return 'not yet fired';
		return 'fired ' + new Date(e.lastRun).toLocaleString() + (e.lastTask ? ' → ' + e.lastTask : '');
	}
	function base(p) {
		const parts = (p ?? '').split('/');
		return parts[parts.length - 1] || p;
	}
</script>

<div
	class="nk"
	style="height: 100dvh; display: flex; justify-content: center; background: var(--color-bg); color: var(--color-text); font-family: var(--font-body);"
>
	<section
		style="width: min(480px, 100%); display: flex; flex-direction: column; min-height: 0; padding: 14px 16px; border-right: 1px solid var(--color-divider); border-left: 1px solid var(--color-divider);"
	>
		<div style="display: flex; align-items: center; gap: 10px; padding-bottom: 10px;">
			<a
				href="/"
				aria-label="back home"
				style="font-size: 13px; line-height: 1; padding: 4px 6px; margin-left: -6px; color: var(--color-neutral-500); text-decoration: none; border-radius: var(--radius-sm);">←</a
			>
			<span style="font-family: var(--font-heading); font-weight: 500; font-size: 14px;">schedule</span>
			<span style="font-size: 11.5px; color: var(--color-neutral-600);"
				>work that starts because its time came</span
			>
		</div>

		{#if err}
			<div role="alert" style="font-size: 12px; color: var(--ev-del); padding: 6px 4px;">{err}</div>
		{/if}

		<!-- the new arrangement -->
		<div style="display: flex; flex-direction: column; gap: 8px; padding: 8px 4px 14px; border-bottom: 1px solid var(--color-divider);">
			<input
				bind:value={intent}
				onkeydown={(e) => e.key === 'Enter' && add()}
				placeholder="what needs doing, in your words…"
				style="width: 100%; background: var(--color-surface, rgba(255,255,255,0.04)); border: 1px solid var(--color-neutral-800); border-radius: var(--radius-sm, 6px); padding: 9px 11px; font: inherit; font-size: 13px; color: var(--color-text); outline: none;"
			/>
			<div style="display: flex; gap: 6px; flex-wrap: wrap; align-items: center;">
				<select
					bind:value={workspace}
					style="flex: 1; min-width: 140px; background: var(--color-surface, rgba(255,255,255,0.04)); border: 1px solid var(--color-neutral-800); border-radius: var(--radius-sm, 6px); padding: 7px 9px; font: inherit; font-size: 12px; color: var(--color-text);"
				>
					{#each repos as r (r.cwd)}
						<option value={r.cwd}>{r.dir}{r.bookmark ? ' · bookmarked' : r.scratch ? ' · scratch' : ''}</option>
					{/each}
					{#if !repos.length}<option value="" disabled>no offered ground yet — bookmark a repo first</option>{/if}
				</select>
				<select
					bind:value={mode}
					title="the worker's tool policy"
					style="background: var(--color-surface, rgba(255,255,255,0.04)); border: 1px solid var(--color-neutral-800); border-radius: var(--radius-sm, 6px); padding: 7px 9px; font: inherit; font-size: 12px; color: var(--color-text);"
				>
					<option value="read">read-only</option>
					<option value="work">can edit & test</option>
				</select>
			</div>
			<div style="display: flex; gap: 6px; flex-wrap: wrap; align-items: center;">
				{#each ['hourly', 'daily', 'weekly', 'once'] as c (c)}
					<button
						onclick={() => (cadence = c)}
						style="cursor: pointer; background: {cadence === c ? 'var(--color-accent-900, rgba(110,223,195,0.12))' : 'none'}; font: inherit; font-size: 11px; padding: 3px 10px; border: 1px solid {cadence === c ? 'var(--color-accent-400, #6edfc3)' : 'var(--color-neutral-800)'}; border-radius: 99px; color: {cadence === c ? 'var(--color-accent-300, #7fe8cd)' : 'var(--color-neutral-300)'};"
						>{c}</button>
				{/each}
				<input
					type="datetime-local"
					bind:value={startAt}
					title={cadence === 'once' ? 'when it fires' : 'first firing (optional — else one interval from now)'}
					style="background: var(--color-surface, rgba(255,255,255,0.04)); border: 1px solid var(--color-neutral-800); border-radius: var(--radius-sm, 6px); padding: 5px 8px; font: inherit; font-size: 11.5px; color: var(--color-neutral-300); color-scheme: dark;"
				/>
				<button
					onclick={add}
					disabled={busy}
					style="margin-left: auto; cursor: pointer; background: none; font: inherit; font-size: 12px; padding: 6px 14px; border: 1px solid var(--color-accent-400, #6edfc3); border-radius: 99px; color: var(--color-accent-300, #7fe8cd); opacity: {busy ? 0.5 : 1};"
					>schedule it</button>
			</div>
			<div style="font-size: 11px; color: var(--color-neutral-600); line-height: 1.5;">
				When it's due, a card is born on the board and a fresh worker starts on it — the card is the
				record, and everything it does lands on that card's log.
			</div>
		</div>

		<!-- the standing arrangements -->
		<div style="flex: 1; overflow-y: auto; min-height: 0; padding-top: 6px;">
			{#each entries as e (e.id)}
				<div style="display: flex; flex-direction: column; gap: 3px; padding: 10px 4px; border-bottom: 1px solid var(--color-divider);">
					<div style="display: flex; align-items: baseline; gap: 8px;">
						<span style="font-family: var(--font-mono, monospace); font-size: 11px; color: var(--color-neutral-600); flex: none;">{e.id}</span>
						<span style="flex: 1; min-width: 0; font-size: 13px; color: var(--color-neutral-200); overflow: hidden; text-overflow: ellipsis; white-space: nowrap;"
							>{e.title || e.intent}</span>
						{#if e.paused}<span style="flex: none; font-size: 10.5px; color: var(--ev-sh);">paused</span>{/if}
						<button
							onclick={() => remove(e.id)}
							disabled={busy}
							title="remove this arrangement (cards already born stay)"
							style="flex: none; cursor: pointer; background: none; border: none; font: inherit; font-size: 11px; color: {confirmRm === e.id ? 'var(--ev-del)' : 'var(--color-neutral-500)'}; padding: 0;"
							>{confirmRm === e.id ? 'it stops returning — sure?' : 'remove'}</button>
					</div>
					{#if e.paused}
						<!-- the engine stopped it; say why, and offer the way back -->
						<div style="font-size: 11px; color: var(--ev-sh); display: flex; gap: 10px; align-items: baseline; flex-wrap: wrap;">
							<span>{e.pausedWhy || 'paused'}</span>
							<button
								onclick={() => resume(e.id)}
								disabled={busy}
								title="un-pause — a one-shot re-arms whole; a recurring entry keeps its clock"
								style="flex: none; cursor: pointer; background: none; border: none; font: inherit; font-size: 11px; color: var(--color-accent-300); padding: 0;"
								>resume</button>
						</div>
					{/if}
					<div style="font-size: 11.5px; color: var(--color-neutral-500); display: flex; gap: 10px; flex-wrap: wrap;">
						<span>{when(e)}</span>
						<span style="font-family: var(--font-mono, monospace);">{base(e.workspace)}</span>
						<span>{e.mode === 'work' ? 'can edit & test' : 'read-only'}</span>
						<span>{lastFired(e)}</span>
					</div>
				</div>
			{:else}
				<div style="padding: 16px 4px; font-size: 12.5px; color: var(--color-neutral-600); line-height: 1.55;">
					Nothing scheduled yet. Lay an arrangement above and vera returns to it on its own — the
					engine's schedule system fires it, the board records it, the daily report accounts for it.
				</div>
			{/each}
		</div>
	</section>
</div>
