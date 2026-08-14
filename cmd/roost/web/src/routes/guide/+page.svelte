<script>
	// The guide, readable where the work happens. It rides the artifact
	// mechanism: the canonical engine/GUIDE.md is mirrored onto the
	// docs shelf at startup, and this page reads the shelf — the same
	// storage model every artifact uses.
	import { marked } from 'marked';
	import { api } from '$lib/state.svelte.js';

	let html = $state('');
	let title = $state('');
	let failed = $state(false);

	$effect(() => {
		api('/api/docs/guide')
			.then((r) => (r.ok ? r.json() : Promise.reject()))
			.then((doc) => {
				title = doc.title;
				html = marked.parse(doc.content);
			})
			.catch(() => (failed = true));
	});
</script>

<div
	class="nk"
	style="min-height: 100dvh; background: var(--color-bg); color: var(--color-text); font-family: var(--font-body);"
>
	<div style="max-width: 720px; margin: 0 auto; padding: 20px 24px 80px;">
		<header
			style="display: flex; align-items: baseline; gap: 10px; padding: 0 0 14px; border-bottom: 1px solid var(--color-divider); margin-bottom: 8px;"
		>
			<a href="/" style="color: var(--color-neutral-500); text-decoration: none;">←</a>
			<span
				style="font-family: var(--font-heading); font-weight: 500; font-size: 15px; letter-spacing: 0.02em;"
				>rook</span
			>
			<span style="font-size: 12px; color: var(--color-neutral-500);">guide</span>
			<span style="flex: 1;"></span>
			<span style="font-size: 11px; color: var(--color-neutral-600);"
				>{title || 'the step-by-step rook agent-guiding-Claude-Code testing guide'}</span
			>
		</header>

		{#if failed}
			<p style="color: var(--color-accent-300); font-size: 13px;">
				the guide did not load — is roost still running?
			</p>
		{:else if !html}
			<p style="color: var(--color-neutral-600); font-size: 13px;">loading…</p>
		{:else}
			<article class="guide-prose">
				<!-- eslint-disable-next-line svelte/no-at-html-tags -- our own embedded document -->
				{@html html}
			</article>
		{/if}
	</div>
</div>

<style>
	.guide-prose {
		font-size: 14px;
		line-height: 1.65;
		color: var(--color-neutral-300);
	}
	.guide-prose :global(h1) {
		font-family: var(--font-heading);
		font-weight: 500;
		font-size: 24px;
		line-height: 1.25;
		color: var(--color-neutral-100);
		margin: 18px 0 10px;
	}
	.guide-prose :global(h2) {
		font-family: var(--font-heading);
		font-weight: 500;
		font-size: 18px;
		color: var(--color-neutral-100);
		margin: 28px 0 8px;
	}
	.guide-prose :global(h3) {
		font-family: var(--font-heading);
		font-weight: 500;
		font-size: 15px;
		color: var(--color-neutral-200);
		margin: 20px 0 6px;
	}
	.guide-prose :global(p) {
		margin: 0 0 12px;
	}
	.guide-prose :global(strong) {
		color: var(--color-neutral-100);
		font-weight: 600;
	}
	.guide-prose :global(a) {
		color: var(--color-accent-300);
	}
	.guide-prose :global(ul),
	.guide-prose :global(ol) {
		margin: 0 0 12px;
		padding-left: 22px;
	}
	.guide-prose :global(li) {
		margin: 4px 0;
	}
	.guide-prose :global(code) {
		font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
		font-size: 12.5px;
		background: var(--color-surface);
		border-radius: 4px;
		padding: 1px 5px;
		color: var(--color-accent-200);
	}
	.guide-prose :global(pre) {
		background: var(--color-surface);
		border: 1px solid var(--color-divider);
		border-radius: var(--radius-md);
		padding: 12px 14px;
		overflow-x: auto;
		margin: 0 0 14px;
	}
	.guide-prose :global(pre code) {
		background: none;
		padding: 0;
		color: var(--color-neutral-200);
	}
	.guide-prose :global(blockquote) {
		margin: 0 0 14px;
		padding: 10px 14px;
		border-left: 3px solid var(--color-accent-600);
		background: var(--color-accent-900);
		border-radius: 0 var(--radius-md) var(--radius-md) 0;
		color: var(--color-accent-100);
	}
	.guide-prose :global(blockquote p) {
		margin: 0;
	}
	.guide-prose :global(hr) {
		border: 0;
		height: 1px;
		margin: 24px 0;
		background: linear-gradient(
			to right,
			transparent,
			var(--color-divider) 48px,
			var(--color-divider) calc(100% - 48px),
			transparent
		);
	}
</style>
