<script>
	// The work view: one goal, as choreography.
	//
	// The board answers "where is everything?" — columns and counts. This
	// answers the question a board cannot: what is happening to THIS
	// piece of work, and what moved while I was away. Two halves. The
	// graph says what the shape is and where the workers are standing in
	// it; the story says what has actually happened, in order, each line
	// pointing at the artifact it was read from.
	//
	// What it deliberately does not do:
	//
	//   - No percentage. Agentic work is not deterministic enough for a
	//     number to mean anything, and a confident fake one is worse than
	//     nothing. The state is a word the server derived from what the
	//     nodes are actually doing.
	//   - No narration the log did not record. Every line here is a
	//     projection of an event that named its source, so "two workers
	//     disagree" can be opened rather than believed. A view that
	//     generated its own prose would be a very pretty liar.
	//
	// The `new` marks are the ten-minutes-away affordance: everything
	// since this browser's last visit is flagged, so returning shows a
	// story rather than a wall.
	import { markSeen } from '$lib/state.svelte.js';

	let { goal = null, seen = 0, onopen = () => {}, onback = () => {} } = $props();

	// Mark as read on the way out, not on arrival — a glance that lands
	// mid-render should still get its marks.
	$effect(() => {
		const id = goal?.id;
		const cursor = goal?.cursor ?? 0;
		return () => {
			if (id && cursor) markSeen(id, cursor);
		};
	});

	const KIND = {
		implement: { label: 'build', tone: 'build' },
		investigate: { label: 'look', tone: 'read' },
		review: { label: 'review', tone: 'read' },
		verify: { label: 'verify', tone: 'check' },
		reconcile: { label: 'reconcile', tone: 'read' }
	};

	// The story's verbs, in the reader's language. An event kind with no
	// entry still renders — the text is the payload, the icon is a
	// courtesy — so a new verb on the server never blanks a line here.
	const EV = {
		'goal.accepted': '◇',
		'goal.ready': '✓',
		'plan.drawn': '⟐',
		'node.planned': '·',
		'node.opened': '▸',
		'node.moved': '↻',
		'node.landed': '■',
		'worker.spawned': '✦',
		'finding.raised': '!',
		'finding.closed': '✓',
		'approaches.split': '⑂',
		'needs.human': '?',
		'human.ruled': '✓'
	};

	const nodesById = $derived(new Map((goal?.nodes ?? []).map((n) => [n.id, n])));

	// A first visit has no "while you were away" — marking every line new
	// would light up the whole story and teach the reader to ignore the
	// colour that is supposed to mean something on the second visit.
	const returning = $derived(seen > 0);
	const isNew = (e) => returning && e.seq > seen;
	const fresh = $derived(returning ? (goal?.events ?? []).filter(isNew).length : 0);

	// Newest last: the story reads downward like a transcript, and the
	// thing that just happened is where the eye already is.
	const story = $derived([...(goal?.events ?? [])].sort((a, b) => a.seq - b.seq));

	function when(ms) {
		if (!ms) return '';
		const s = Math.max(0, (Date.now() - ms) / 1000);
		if (s < 60) return 'just now';
		if (s < 3600) return `${Math.floor(s / 60)}m ago`;
		if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
		return `${Math.floor(s / 86400)}d ago`;
	}

	const money = (n) => (n >= 0.01 ? `$${n.toFixed(2)}` : n > 0 ? '<$0.01' : '');
</script>

<div class="work">
	{#if !goal}
		<div class="empty">
			<p>No goal open.</p>
		</div>
	{:else}
		<header>
			<button class="back" onclick={onback} aria-label="Back">←</button>
			<div class="head">
				<div class="crumb">{goal.id}</div>
				<h1>{goal.title}</h1>
			</div>
		</header>

		<!-- The state line. A word, never a number. -->
		<section class="state" data-state={goal.state.toLowerCase().replace(/\s+/g, '-')}>
			<div class="word">{goal.state}</div>
			<div class="face">{goal.face}</div>
			{#if goal.spend > 0}
				<div class="spend">{money(goal.spend)}</div>
			{/if}
		</section>

		<section class="graph">
			<h2>The shape</h2>
			{#each goal.nodes as n (n.id)}
				{@const k = KIND[n.kind] ?? { label: n.kind, tone: 'read' }}
				<button class="node" data-col={n.col} onclick={() => onopen(n.id)}>
					<div class="row">
						<span class="kind" data-tone={k.tone}>{k.label}</span>
						<span class="title">{n.title}</span>
						{#if n.model}
							<span class="model" title="routed to {n.model} ({n.tier})">{n.model}</span>
						{/if}
						{#if n.costUsd > 0}<span class="cost">{money(n.costUsd)}</span>{/if}
					</div>

					{#if n.live}
						<!-- A worker is on it right now. This is the one place the
						     view shows a moment rather than a record. -->
						<div class="live">
							<span class="pulse" aria-hidden="true"></span>
							{n.live.now || n.live.state}
						</div>
					{:else if n.ask}
						<div class="ask">{n.ask}</div>
					{:else if n.blockedBy.length}
						<div class="blocked">
							waits on {n.blockedBy.join(', ')}
							{#if n.readOnly}<span class="auto">· vera opens it herself</span>{/if}
						</div>
					{:else}
						<div class="sub">{n.state}</div>
					{/if}
				</button>
			{/each}
		</section>

		<section class="story">
			<h2>
				What happened
				{#if fresh > 0}<span class="fresh">{fresh} new</span>{/if}
			</h2>
			{#if !story.length}
				<p class="quiet">Nothing has moved yet.</p>
			{/if}
			{#each story as e (e.seq)}
				{@const node = nodesById.get(e.node)}
				<div class="ev" class:isnew={isNew(e)}>
					<span class="glyph" aria-hidden="true">{EV[e.kind] ?? '·'}</span>
					<div class="body">
						<div class="text">{e.text}</div>
						<div class="meta">
							<span class="ago">{when(e.at)}</span>
							{#if node}
								<button class="ref" onclick={() => onopen(node.id)}>{node.id}</button>
							{/if}
							<!-- The citation. This is what makes the story a
							     projection rather than a narration: every claim about
							     a worker names the fork it was read from, and that
							     fork is resumable. -->
							{#if e.src?.fork}
								<span class="src" title="read from fork {e.src.fork}, turn {e.src.msg}">
									↳ fork {e.src.fork.slice(0, 8)}{e.src.msg ? ` · turn ${e.src.msg}` : ''}
								</span>
							{/if}
						</div>
					</div>
				</div>
			{/each}
		</section>
	{/if}
</div>

<style>
	.work {
		height: 100%;
		overflow-y: auto;
		width: 100%;
		max-width: 46rem;
		margin: 0 auto;
		padding: 1rem 1rem 4rem;
		box-sizing: border-box;
	}
	.empty {
		display: grid;
		place-items: center;
		height: 60vh;
		color: var(--color-neutral-500);
	}
	header {
		display: flex;
		align-items: center;
		gap: 0.6rem;
		margin-bottom: 1rem;
	}
	.back {
		background: none;
		border: 0;
		color: var(--color-neutral-400);
		font-size: 1.2rem;
		cursor: pointer;
		padding: 0.2rem 0.4rem;
		border-radius: var(--radius-sm);
	}
	.back:hover {
		background: var(--color-surface);
		color: var(--color-text);
	}
	.crumb {
		font-family: var(--font-mono);
		font-size: 0.72rem;
		color: var(--color-neutral-600);
	}
	h1 {
		margin: 0;
		font-size: 1.05rem;
		font-weight: 600;
		line-height: 1.3;
	}
	h2 {
		font-size: 0.72rem;
		text-transform: uppercase;
		letter-spacing: 0.08em;
		color: var(--color-neutral-500);
		margin: 1.6rem 0 0.6rem;
		display: flex;
		align-items: center;
		gap: 0.5rem;
	}

	/* The state line: a word, never a number. */
	.state {
		background: var(--color-surface);
		border-radius: var(--radius-lg);
		padding: 0.9rem 1rem;
		box-shadow: var(--shadow-sm);
		border-left: 3px solid var(--color-neutral-700);
	}
	.state[data-state='needs-you'],
	.state[data-state='ready-for-you'] {
		border-left-color: var(--ev-sh-mid);
	}
	.state[data-state='building'],
	.state[data-state='reviewing'],
	.state[data-state='verifying'] {
		border-left-color: var(--color-accent-500);
	}
	.state[data-state='ready'] {
		border-left-color: var(--ev-add-mid);
	}
	.word {
		font-size: 1.15rem;
		font-weight: 600;
	}
	.face {
		color: var(--color-neutral-400);
		font-size: 0.86rem;
		margin-top: 0.15rem;
	}
	.spend {
		margin-top: 0.4rem;
		font-family: var(--font-mono);
		font-size: 0.72rem;
		color: var(--color-neutral-600);
	}

	/* The graph. */
	.node {
		display: block;
		width: 100%;
		text-align: left;
		background: var(--color-surface);
		border: 0;
		border-radius: var(--radius-md);
		box-shadow: var(--shadow-sm);
		padding: 0.6rem 0.75rem;
		margin-bottom: 0.4rem;
		color: inherit;
		font: inherit;
		cursor: pointer;
	}
	.node:hover {
		box-shadow: var(--shadow-md);
	}
	.node[data-col='done'] {
		opacity: 0.62;
	}
	.node[data-col='dropped'] {
		opacity: 0.42;
	}
	.row {
		display: flex;
		align-items: baseline;
		gap: 0.5rem;
	}
	.kind {
		font-family: var(--font-mono);
		font-size: 0.64rem;
		text-transform: uppercase;
		letter-spacing: 0.05em;
		padding: 0.1rem 0.35rem;
		border-radius: var(--radius-sm);
		flex: none;
		background: var(--color-neutral-800);
		color: var(--color-neutral-300);
	}
	.kind[data-tone='build'] {
		background: var(--color-accent-800);
		color: var(--color-accent-200);
	}
	.kind[data-tone='check'] {
		background: var(--ev-add-fill);
		color: var(--ev-add);
	}
	.title {
		flex: 1;
		font-size: 0.88rem;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.model,
	.cost {
		font-family: var(--font-mono);
		font-size: 0.66rem;
		color: var(--color-neutral-600);
		flex: none;
	}
	.sub,
	.blocked,
	.ask,
	.live {
		font-size: 0.76rem;
		margin-top: 0.25rem;
		color: var(--color-neutral-500);
	}
	.ask {
		color: var(--ev-sh);
	}
	.blocked .auto {
		color: var(--color-neutral-600);
	}
	.live {
		color: var(--color-accent-300);
		display: flex;
		align-items: center;
		gap: 0.4rem;
	}
	.pulse {
		width: 6px;
		height: 6px;
		border-radius: 50%;
		background: var(--color-accent-400);
		animation: pulse 1.6s ease-in-out infinite;
		flex: none;
	}
	@keyframes pulse {
		0%,
		100% {
			opacity: 0.35;
		}
		50% {
			opacity: 1;
		}
	}
	@media (prefers-reduced-motion: reduce) {
		.pulse {
			animation: none;
			opacity: 0.9;
		}
	}

	/* The story. */
	.fresh {
		font-family: var(--font-mono);
		font-size: 0.62rem;
		background: var(--color-accent-700);
		color: var(--color-accent-100);
		padding: 0.05rem 0.3rem;
		border-radius: var(--radius-sm);
		letter-spacing: 0;
		text-transform: none;
	}
	.quiet {
		color: var(--color-neutral-600);
		font-size: 0.84rem;
	}
	.ev {
		display: flex;
		gap: 0.6rem;
		padding: 0.45rem 0.2rem 0.45rem 0.4rem;
		border-left: 2px solid transparent;
	}
	.ev.isnew {
		border-left-color: var(--color-accent-500);
		background: color-mix(in srgb, var(--color-accent-900) 45%, transparent);
		border-radius: 0 var(--radius-sm) var(--radius-sm) 0;
	}
	.glyph {
		font-family: var(--font-mono);
		color: var(--color-neutral-600);
		flex: none;
		width: 1rem;
		text-align: center;
		line-height: 1.5;
	}
	.body {
		min-width: 0;
	}
	.text {
		font-size: 0.86rem;
		line-height: 1.45;
	}
	.meta {
		display: flex;
		flex-wrap: wrap;
		gap: 0.5rem;
		align-items: center;
		margin-top: 0.15rem;
		font-family: var(--font-mono);
		font-size: 0.66rem;
		color: var(--color-neutral-600);
	}
	.ref {
		background: none;
		border: 0;
		padding: 0;
		font: inherit;
		color: var(--color-accent-400);
		cursor: pointer;
	}
	.ref:hover {
		text-decoration: underline;
	}
	.src {
		color: var(--color-neutral-700);
	}
</style>
