<script>
	// The Rook Board — implemented from the claude.ai/design project
	// "Rook agent task management" (Rook Board.dc.html, nocturne DS).
	// ONE board, globally: tasks are units of work across every agent;
	// an agent is an assignment a card carries, not a namespace. The
	// working agent's card wears its live status; captures land as
	// unassigned backlog.
	import { api } from '$lib/state.svelte.js';

	let board = $state(null); // {tasks, inflight, spend, fleet, notice}
	let selId = $state(null);
	let capture = $state('');
	let triage = $state(null); // {id, near:{id,title}}
	let busy = $state(false);
	let err = $state('');

	async function refresh() {
		try {
			const r = await api('/api/tasks');
			if (!r.ok) throw new Error((await r.json()).error ?? 'the board did not answer');
			board = await r.json();
			err = '';
		} catch (e) {
			err = e.message;
		}
	}
	$effect(() => {
		refresh();
		const t = setInterval(refresh, 3000);
		return () => clearInterval(t);
	});

	const COLS = [
		['inbox', 'inbox', 'var(--color-neutral-600)'],
		['in progress', 'progress', 'var(--color-accent)'],
		['waiting', 'waiting', 'var(--color-accent-300)'],
		['done', 'done', 'var(--color-neutral-500)'],
		['dropped', 'dropped', 'var(--color-neutral-800)']
	];
	const columns = $derived(
		COLS.map(([name, key, dot]) => ({
			name,
			key,
			dot,
			tasks: (board?.tasks ?? []).filter((t) => t.col === key)
		}))
	);
	const sel = $derived(
		(board?.tasks ?? []).find((t) => t.id === selId) ?? (board?.tasks ?? [])[0] ?? null
	);
	// stateTone keeps nocturne's mono rule: accent or neutral, no
	// second hue.
	const stateTone = {
		working: 'var(--color-accent)',
		'blocked?': 'var(--color-accent-300)',
		'needs you': 'var(--color-accent-300)',
		idle: 'var(--color-neutral-600)'
	};

	function relAge(iso) {
		const s = (Date.now() - new Date(iso).getTime()) / 1000;
		if (s < 60) return 'now';
		if (s < 3600) return `${Math.floor(s / 60)}m`;
		if (s < 172800) return `${Math.floor(s / 3600)}h`;
		return `${Math.floor(s / 86400)}d`;
	}
	const cost = (v) => (v ? `$${v.toFixed(2)}` : '—');

	async function post(path, body) {
		busy = true;
		err = '';
		try {
			const r = await api(path, { method: 'POST', body: JSON.stringify(body ?? {}) });
			const out = await r.json();
			if (!r.ok) throw new Error(out.error ?? 'refused');
			await refresh();
			return out;
		} catch (e) {
			err = e.message;
		} finally {
			busy = false;
		}
	}

	async function submitCapture() {
		const text = capture.trim();
		if (!text || busy) return;
		const out = await post('/api/tasks', { text });
		if (!out) return;
		capture = '';
		selId = out.task.id;
		triage = out.near ? { id: out.task.id, near: out.near } : null;
	}
	const act = (tid, action, extra) => post(`/api/tasks/${tid}/act`, { action, ...extra });
	const start = (tid, extra) => post(`/api/tasks/${tid}/start`, extra);
	async function mergeTriage() {
		await act(triage.id, 'merge', { intoId: triage.near.id });
		selId = triage?.near?.id;
		triage = null;
	}
	async function accept(t) {
		if (t.proposalKind === 'start') {
			await start(t.id, { mode: startMode, ...(startIn ? { newIn: startIn } : {}) });
			startIn = '';
		} else await act(t.id, 'accept');
	}

	// Where an unassigned task starts: '' = the current agent; a cwd =
	// a fresh agent born there. Mode is the tool policy. A waiting
	// card's reply continues its drive. All reset when selection moves.
	let startIn = $state('');
	let startMode = $state('read');
	let replyText = $state('');
	$effect(() => {
		selId;
		startIn = '';
		startMode = 'read';
		replyText = '';
	});

	async function sendReply() {
		const text = replyText.trim();
		if (!text || busy) return;
		await post(`/api/tasks/${sel.id}/reply`, { text });
		replyText = '';
	}

	// "+ new scratch workspace…": roost creates a directory under its
	// managed parent — a place where nothing real is at stake. Not a
	// sandbox; a spare room.
	async function onRepoPick(e) {
		if (e.target.value !== '__new__') return;
		startIn = '';
		const name = window.prompt('Name the scratch workspace (letters, digits, dashes):');
		if (!name) return;
		const out = await post('/api/workspaces', { name: name.trim() });
		if (out?.cwd) startIn = out.cwd;
	}

	async function deleteScratch(name) {
		if (!window.confirm(`Delete the scratch workspace “${name}” and everything in it?`)) return;
		busy = true;
		err = '';
		try {
			const r = await api(`/api/workspaces/${name}`, { method: 'DELETE' });
			const out = await r.json();
			if (!r.ok) throw new Error(out.error ?? 'refused');
			await refresh();
		} catch (e2) {
			err = e2.message;
		} finally {
			busy = false;
		}
	}
</script>

<div
	style="height: 100%; flex: 1; min-width: 0; background: var(--color-bg); color: var(--color-text); font-family: var(--font-body); display: flex; flex-direction: column;"
>
	<header
		style="display: flex; align-items: center; gap: 17px; padding: 14px 24px; border-bottom: 1px solid var(--color-divider);"
	>
		<div style="display: flex; align-items: baseline; gap: 10px;">
			<span
				style="font-family: var(--font-heading); font-weight: 500; font-size: 15px; letter-spacing: 0.02em;"
				>rook</span
			>
			<span style="font-size: 12px; color: var(--color-neutral-500);">board</span>
		</div>
		<div style="width: 1px; height: 20px; background: var(--color-divider);"></div>
		{#if board?.fleet}
			<div
				style="display: flex; align-items: center; gap: 8px; font-size: 12px; color: var(--color-neutral-400);"
			>
				<span
					style="width: 6px; height: 6px; border-radius: 99px; background: var(--color-accent); box-shadow: 0 0 8px var(--color-accent);"
				></span>
				<span style="color: var(--color-neutral-200);"
					>{board.fleet.working} working</span
				>
				<span style="color: var(--color-neutral-600);">·</span>
				<span>{board.fleet.agents} agents</span>
			</div>
		{/if}
		<div style="flex: 1;"></div>
		<div
			style="display: flex; align-items: center; gap: 18px; font-size: 12px; color: var(--color-neutral-500);"
		>
			<span
				>spend
				<span style="color: var(--color-neutral-200); font-variant-numeric: tabular-nums;"
					>{cost(board?.spend)}</span
				></span
			>
			{#if board?.inflight}
				<span style="display: flex; align-items: center; gap: 6px;"
					><span
						style="width: 6px; height: 6px; border-radius: 99px; background: var(--color-accent-400);"
					></span>{board.inflight} run{board.inflight === 1 ? '' : 's'} in flight</span
				>
			{/if}
		</div>
	</header>

	{#if err}
		<div
			style="margin: 10px 24px 0; font-size: 12.5px; color: #e08585; border: 1px solid #6a3a3a; border-radius: var(--radius-md); padding: 8px 12px;"
		>
			{err}
		</div>
	{/if}

	<div style="display: flex; flex: 1; min-height: 0;">
		<main
			style="flex: 1; min-width: 0; display: flex; flex-direction: column; padding: 20px 0 0 24px;"
		>
			<div
				style="display: flex; align-items: center; gap: 12px; padding-right: 24px; margin-bottom: 18px;"
			>
				<input
					class="input"
					bind:value={capture}
					onkeydown={(e) => e.key === 'Enter' && submitCapture()}
					placeholder="Tell rook what needs doing…"
					style="flex: 1; background: transparent;"
				/>
				<button class="btn btn-primary" onclick={submitCapture} disabled={busy}>Capture</button>
			</div>

			{#if triage}
				<div
					style="margin: -6px 24px 16px 0; padding: 12px 14px; border: 1px solid var(--color-accent-700); border-radius: var(--radius-md); background: var(--color-accent-900); display: flex; align-items: center; gap: 14px; font-size: 12.5px;"
				>
					<span style="color: var(--color-accent-200);">Opened as a new task.</span>
					<span style="color: var(--color-neutral-400);"
						>Looks close to <span style="color: var(--color-neutral-200);"
							>{triage.near.id} · {triage.near.title}</span
						>.</span
					>
					<div style="flex: 1;"></div>
					<button class="btn btn-ghost" style="font-size: 12px;" onclick={mergeTriage}
						>Merge into that</button
					>
					<button
						class="btn btn-ghost"
						style="font-size: 12px; color: var(--color-neutral-500);"
						onclick={() => (triage = null)}>Keep separate</button
					>
				</div>
			{/if}

			<div
				style="flex: 1; min-height: 0; display: flex; gap: 14px; overflow-x: auto; padding-bottom: 24px; padding-right: 24px;"
			>
				{#each columns as col (col.key)}
					<section
						style="width: 244px; flex: 0 0 244px; display: flex; flex-direction: column; min-height: 0;"
					>
						<div style="display: flex; align-items: center; gap: 8px; padding: 0 2px 10px;">
							<span style="width: 5px; height: 5px; border-radius: 99px; background: {col.dot};"
							></span>
							<span
								style="font-family: var(--font-heading); font-weight: 500; font-size: 12.5px; letter-spacing: 0.06em; text-transform: uppercase; color: var(--color-neutral-300);"
								>{col.name}</span
							>
							<span
								style="font-size: 11.5px; color: var(--color-neutral-600); font-variant-numeric: tabular-nums;"
								>{col.tasks.length}</span
							>
						</div>
						<div
							style="height: 1px; background: linear-gradient(to right, var(--color-divider), transparent);"
						></div>
						<div
							style="flex: 1; min-height: 0; overflow-y: auto; display: flex; flex-direction: column; gap: 8px; padding: 10px 2px 4px;"
						>
							{#each col.tasks as t (t.id)}
								<article
									onclick={() => (selId = t.id)}
									onkeydown={(e) => e.key === 'Enter' && (selId = t.id)}
									role="button"
									tabindex="0"
									style="cursor: pointer; text-align: left; background: var(--color-surface); border: 1px solid {sel?.id ===
									t.id
										? 'var(--color-accent-600)'
										: t.ask
											? 'var(--color-accent-800)'
											: 'var(--color-neutral-800)'}; border-radius: var(--radius-md); padding: 11px 12px; display: flex; flex-direction: column; gap: 8px; box-shadow: {sel?.id ===
									t.id
										? 'var(--shadow-md)'
										: 'none'};"
								>
									<div style="display: flex; align-items: center; gap: 6px;">
										<span
											style="font-size: 10.5px; letter-spacing: 0.08em; color: var(--color-neutral-600); font-variant-numeric: tabular-nums;"
											>{t.id}</span
										>
										{#if t.pinned}
											<span style="font-size: 10.5px; color: var(--color-accent-400);">pinned</span>
										{/if}
										<div style="flex: 1;"></div>
										<span
											style="font-size: 10.5px; color: var(--color-neutral-600); font-variant-numeric: tabular-nums;"
											>{cost(t.costUsd)}</span
										>
									</div>
									<div
										style="font-family: var(--font-heading); font-weight: 500; font-size: 13.5px; line-height: 1.35; color: var(--color-neutral-100); text-wrap: pretty;"
									>
										{t.title}
									</div>
									{#if t.face}
										<div
											style="font-size: 11.5px; line-height: 1.45; color: var(--color-neutral-500); text-wrap: pretty;"
										>
											{t.face}
										</div>
									{/if}
									{#if t.ask}
										<div
											style="border-top: 1px solid var(--color-divider); padding-top: 8px; display: flex; gap: 7px; align-items: flex-start;"
										>
											<span
												style="color: var(--color-accent-400); font-size: 11px; line-height: 1.45;"
												>needs you</span
											>
											<span
												style="font-size: 11.5px; line-height: 1.45; color: var(--color-accent-200); text-wrap: pretty;"
												>{t.ask}</span
											>
										</div>
									{/if}
									{#if t.live}
										<!-- the assigned agent's present: derived overlay, never the log -->
										<div
											style="border-top: 1px solid var(--color-divider); padding-top: 8px; display: flex; align-items: baseline; gap: 7px; font-size: 11px; line-height: 1.45;"
										>
											<span
												style="width: 5px; height: 5px; flex: none; align-self: center; border-radius: 99px; background: {stateTone[
													t.live.state
												] ?? 'var(--color-neutral-600)'}; {t.live.state === 'working'
													? 'box-shadow: 0 0 6px var(--color-accent);'
													: ''}"
											></span>
											<span style="color: var(--color-neutral-300);">{t.live.dir}</span>
											<span style="color: {stateTone[t.live.state] ?? 'var(--color-neutral-500)'};"
												>{t.live.state}</span
											>
											{#if t.live.now}
												<span
													style="min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--color-neutral-500);"
													>⛭ {t.live.now}</span
												>
											{/if}
										</div>
									{/if}
									<div
										style="display: flex; align-items: center; gap: 8px; font-size: 10.5px; color: var(--color-neutral-600);"
									>
										<span>{t.log?.at(-1)?.actor ?? 'human'}</span>
										<span>·</span>
										<span>{relAge(t.updatedAt)}</span>
									</div>
								</article>
							{/each}
						</div>
					</section>
				{/each}
			</div>
		</main>

		{#if sel}
			<aside
				style="width: 396px; flex: 0 0 396px; border-left: 1px solid var(--color-divider); display: flex; flex-direction: column; min-height: 0; background: linear-gradient(180deg, #1a1c2c, var(--color-bg) 320px);"
			>
				<div style="padding: 20px 22px 14px; border-bottom: 1px solid var(--color-divider);">
					<div style="display: flex; align-items: center; gap: 8px; margin-bottom: 10px;">
						<span
							style="font-size: 10.5px; letter-spacing: 0.08em; color: var(--color-neutral-600); font-variant-numeric: tabular-nums;"
							>{sel.id}</span
						>
						<span class="tag tag-outline" style="font-size: 10.5px;">{sel.state}</span>
						{#if sel.agent}
							<a
								href="/agent/{sel.agent}"
								style="font-size: 11px; color: var(--color-accent-300); text-decoration: none;"
								title="open this agent's conversation"
							>
								{sel.live?.dir ?? 'agent'} · chat →
							</a>
						{:else}
							<span style="font-size: 11px; color: var(--color-neutral-600);">unassigned</span>
						{/if}
						<div style="flex: 1;"></div>
						<button
							class="btn btn-ghost"
							style="font-size: 11px; color: var(--color-neutral-500);"
							onclick={() => act(sel.id, 'pin')}>{sel.pinned ? 'unpin' : 'pin'}</button
						>
						{#if sel.col !== 'done' && sel.col !== 'dropped'}
							<button
								class="btn btn-ghost"
								style="font-size: 11px; color: var(--color-neutral-500);"
								onclick={() => act(sel.id, 'drop')}>drop</button
							>
						{:else if sel.scratchName}
							<button
								class="btn btn-ghost"
								style="font-size: 11px; color: var(--color-neutral-500);"
								title="remove ~/roost-scratch/{sel.scratchName} and everything in it"
								onclick={() => deleteScratch(sel.scratchName)}>delete scratch workspace</button
							>
						{/if}
					</div>
					<h1
						style="margin: 0; font-family: var(--font-heading); font-weight: 500; font-size: 19px; line-height: 1.3; color: var(--color-neutral-100); text-wrap: pretty;"
					>
						{sel.title}
					</h1>
				</div>

				<div
					style="flex: 1; min-height: 0; overflow-y: auto; padding: 18px 22px 28px; display: flex; flex-direction: column; gap: 20px;"
				>
					{#if sel.proposal}
						<div
							style="border: 1px solid var(--color-accent-700); border-radius: var(--radius-md); padding: 13px 14px; background: var(--color-accent-900); display: flex; flex-direction: column; gap: 10px;"
						>
							<div
								style="font-size: 11px; letter-spacing: 0.07em; text-transform: uppercase; color: var(--color-accent-400);"
							>
								rook proposes
							</div>
							<div
								style="font-size: 13px; line-height: 1.5; color: var(--color-accent-100); text-wrap: pretty;"
							>
								{sel.proposal}
							</div>
							{#if sel.proposalWhy}
								<div
									style="font-size: 11.5px; line-height: 1.5; color: var(--color-neutral-400); text-wrap: pretty;"
								>
									{sel.proposalWhy}
								</div>
							{/if}
							{#if sel.proposalKind === 'start'}
								<div style="display: flex; gap: 6px; flex-wrap: wrap;">
									{#if !sel.agent}
										<!-- who works it: the current agent, or a fresh
										     one born in a repo the fleet has shown -->
										<select
											bind:value={startIn}
											onchange={onRepoPick}
											style="flex: 1; font: inherit; font-size: 12px; color: var(--color-text); background: var(--color-surface); border: 1px solid var(--color-divider); border-radius: var(--radius-sm); padding: 5px 8px;"
										>
											<option value="">on the current agent</option>
											{#each board?.repos ?? [] as r (r.cwd)}
												<option value={r.cwd}
													>fresh agent in {r.dir}{r.scratch ? ' (scratch)' : ''}</option
												>
											{/each}
											<option value="__new__">+ new scratch workspace…</option>
										</select>
									{/if}
									<!-- the tool policy: code-side sets, never LLM-chosen -->
									<select
										bind:value={startMode}
										title="read: analysis only, permission-gated tools stay refused. work: edits plus scoped build/test commands."
										style="font: inherit; font-size: 12px; color: var(--color-text); background: var(--color-surface); border: 1px solid var(--color-divider); border-radius: var(--radius-sm); padding: 5px 8px;"
									>
										<option value="read">read-only</option>
										<option value="work">can edit & test</option>
									</select>
								</div>
							{/if}
							<div style="display: flex; gap: 8px; margin-top: 2px;">
								<button
									class="btn btn-primary"
									style="font-size: 12px;"
									disabled={busy}
									onclick={() => accept(sel)}
									>{sel.proposalKind === 'start' ? 'Start drive' : 'Accept as done'}</button
								>
								<button
									class="btn btn-ghost"
									style="font-size: 12px; color: var(--color-neutral-400);"
									onclick={() => act(sel.id, 'decline')}>Not yet</button
								>
							</div>
						</div>
					{/if}

					{#if sel.col === 'waiting'}
						<!-- the escalation's return path: answer here and the
						     SAME drive continues, seeded with its history -->
						<div
							style="border: 1px solid var(--color-divider); border-radius: var(--radius-md); padding: 12px 13px; display: flex; flex-direction: column; gap: 9px;"
						>
							{#if sel.ask}
								<div
									style="font-size: 12.5px; line-height: 1.5; color: var(--color-accent-200); text-wrap: pretty;"
								>
									{sel.ask}
								</div>
							{/if}
							<textarea
								bind:value={replyText}
								rows="2"
								placeholder="answer the worker — the drive continues from here"
								onkeydown={(e) => {
									if (e.key === 'Enter' && !e.shiftKey) {
										e.preventDefault();
										sendReply();
									}
								}}
								style="font: inherit; font-size: 12.5px; resize: none; color: var(--color-text); background: var(--color-surface); border: 1px solid var(--color-divider); border-radius: var(--radius-sm); padding: 7px 9px;"
							></textarea>
							<div style="display: flex;">
								<button
									class="btn btn-primary"
									style="font-size: 12px;"
									disabled={busy || !replyText.trim()}
									onclick={sendReply}>Reply & continue</button
								>
							</div>
						</div>
					{/if}

					<div style="display: flex; flex-direction: column; gap: 12px;">
						<div
							style="font-size: 11px; letter-spacing: 0.07em; text-transform: uppercase; color: var(--color-neutral-600);"
						>
							Intent
						</div>
						<div
							style="font-size: 13.5px; line-height: 1.55; color: var(--color-neutral-200); white-space: pre-wrap; text-wrap: pretty;"
						>
							{sel.intent}
						</div>
						<div
							style="border-left: 2px solid var(--color-accent-700); padding: 2px 0 2px 12px; display: flex; flex-direction: column; gap: 5px;"
						>
							<div
								style="font-size: 10.5px; letter-spacing: 0.07em; text-transform: uppercase; color: var(--color-neutral-600);"
							>
								compiled drive goal
							</div>
							<div
								style="font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 12px; line-height: 1.55; color: var(--color-accent-200); text-wrap: pretty;"
							>
								{sel.goal || '(not compiled — task not started)'}
							</div>
							<div style="font-size: 11px; color: var(--color-neutral-600);">
								written by {sel.goalActor || '—'}
							</div>
						</div>
					</div>

					{#if sel.exchanges?.length}
						<!-- the drive's own conversation: what rook sent, what
						     the worker said — the review surface for every
						     judge approval and the worker's verification -->
						<div style="display: flex; flex-direction: column; gap: 10px;">
							<div
								style="font-size: 11px; letter-spacing: 0.07em; text-transform: uppercase; color: var(--color-neutral-600);"
							>
								Conversation
							</div>
							{#each sel.exchanges as ex, i (i)}
								<div
									style="border-left: 2px solid var(--color-divider); padding: 2px 0 2px 12px; display: flex; flex-direction: column; gap: 5px;"
								>
									<div
										style="font-size: 12px; line-height: 1.5; color: var(--color-accent-300); text-wrap: pretty;"
									>
										→ {ex.prompt}
									</div>
									{#if (ex.reply ?? '').length > 420}
										<details>
											<summary
												style="cursor: pointer; font-size: 12px; line-height: 1.5; color: var(--color-neutral-300);"
											>
												{ex.reply.slice(0, 220)}… <span style="color: var(--color-neutral-600);">(show all)</span>
											</summary>
											<div
												style="margin-top: 4px; font-size: 12px; line-height: 1.55; white-space: pre-wrap; color: var(--color-neutral-300);"
											>
												{ex.reply}
											</div>
										</details>
									{:else}
										<div
											style="font-size: 12px; line-height: 1.55; white-space: pre-wrap; color: var(--color-neutral-300);"
										>
											{ex.reply}
										</div>
									{/if}
								</div>
							{/each}
						</div>
					{/if}

					{#if sel.runs?.length}
						<div style="display: flex; flex-direction: column; gap: 10px;">
							<div
								style="font-size: 11px; letter-spacing: 0.07em; text-transform: uppercase; color: var(--color-neutral-600);"
							>
								Runs
							</div>
							{#each sel.runs as r}
								<div
									style="display: flex; align-items: baseline; gap: 10px; font-size: 12px; padding: 7px 0; border-bottom: 1px solid var(--color-divider);"
								>
									<span
										style="font-family: ui-monospace, Menlo, monospace; color: var(--color-neutral-500); font-size: 11.5px;"
										>{r.kind}</span
									>
									<span style="flex: 1; color: var(--color-neutral-300); text-wrap: pretty;"
										>{r.outcome}</span
									>
									<span style="color: var(--color-neutral-500); font-variant-numeric: tabular-nums;"
										>{cost(r.costUsd)}</span
									>
								</div>
							{/each}
							<div
								style="display: flex; justify-content: space-between; font-size: 12px; padding-top: 2px;"
							>
								<span style="color: var(--color-neutral-500);">rolled up</span>
								<span
									style="color: var(--color-neutral-100); font-variant-numeric: tabular-nums;"
									>{cost(sel.costUsd)}</span
								>
							</div>
						</div>
					{/if}

					<div style="display: flex; flex-direction: column; gap: 10px;">
						<div style="display: flex; align-items: center; gap: 10px;">
							<span
								style="font-size: 11px; letter-spacing: 0.07em; text-transform: uppercase; color: var(--color-neutral-600);"
								>Log</span
							>
							<span style="font-size: 11px; color: var(--color-neutral-700);"
								>append-only · the board is derived</span
							>
						</div>
						<div style="display: flex; flex-direction: column;">
							{#each [...(sel.log ?? [])].reverse() as e}
								<div
									style="display: grid; grid-template-columns: 54px 1fr auto; gap: 10px; align-items: baseline; padding: 7px 0; border-top: 1px solid var(--color-divider);"
								>
									<span
										style="font-size: 10.5px; letter-spacing: 0.04em; color: {e.actor === 'human'
											? 'var(--color-accent-300)'
											: e.actor === 'worker'
												? 'var(--color-neutral-600)'
												: 'var(--color-neutral-400)'};">{e.actor}</span
									>
									<span
										style="font-size: 12px; line-height: 1.5; color: var(--color-neutral-300); text-wrap: pretty;"
										>{e.text}</span
									>
									<span
										style="font-size: 10.5px; color: var(--color-neutral-700); font-variant-numeric: tabular-nums;"
										>{relAge(e.at)}</span
									>
								</div>
							{/each}
						</div>
					</div>
				</div>
			</aside>
		{/if}
	</div>
</div>
