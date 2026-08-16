<script>
	// The subscription's rate-limit windows, as the claude CLI reports
	// them — session block and weekly, each a quiet meter. Colors come
	// from the shared ev-* tokens (green fine, amber close, red hot) so
	// the meter reads the same on every surface.
	let { usage } = $props();

	function tone(pct) {
		if (pct >= 90) return 'var(--ev-del-mid)';
		if (pct >= 70) return 'var(--ev-sh-mid)';
		return 'var(--ev-add-mid)';
	}
</script>

{#snippet meter(label, pct, resets)}
	<span class="flex items-center gap-1.5" title={resets ? `resets ${resets}` : ''}>
		{label}
		<span
			class="inline-block h-1 w-16 overflow-hidden rounded-full"
			style="background: var(--color-neutral-900);"
		>
			<span class="block h-full" style="width:{Math.min(pct, 100)}%; background:{tone(pct)};"
			></span>
		</span>
		{pct}%
	</span>
{/snippet}

{#if usage}
	<div
		class="flex flex-wrap items-center gap-x-5 gap-y-1 text-[11px]"
		style="color: var(--color-neutral-500);"
	>
		{@render meter('session', usage.sessionPct, usage.sessionResets)}
		{@render meter('week', usage.weekAllPct, usage.weekAllResets)}
		{#if usage.weekModelName}
			<span title={usage.weekModelResets ? `resets ${usage.weekModelResets}` : ''}>
				{usage.weekModelName.toLowerCase()}
				{usage.weekModelPct}%
			</span>
		{/if}
	</div>
{/if}
