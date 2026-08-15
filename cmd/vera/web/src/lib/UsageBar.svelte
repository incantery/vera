<script>
	// The subscription's rate-limit windows, as the claude CLI reports
	// them — session block and weekly, each a quiet meter.
	let { usage } = $props();

	function tone(pct) {
		if (pct >= 90) return 'bg-rose-400';
		if (pct >= 70) return 'bg-amber-400';
		return 'bg-sky-400/70';
	}
</script>

{#if usage}
	<div class="flex flex-wrap items-center gap-x-5 gap-y-1 text-[11px] text-zinc-500">
		<span class="flex items-center gap-1.5" title={usage.sessionResets ? `resets ${usage.sessionResets}` : ''}>
			session
			<span class="inline-block h-1 w-16 overflow-hidden rounded-full bg-zinc-800">
				<span class="block h-full {tone(usage.sessionPct)}" style="width:{Math.min(usage.sessionPct, 100)}%"></span>
			</span>
			{usage.sessionPct}%
		</span>
		<span class="flex items-center gap-1.5" title={usage.weekAllResets ? `resets ${usage.weekAllResets}` : ''}>
			week
			<span class="inline-block h-1 w-16 overflow-hidden rounded-full bg-zinc-800">
				<span class="block h-full {tone(usage.weekAllPct)}" style="width:{Math.min(usage.weekAllPct, 100)}%"></span>
			</span>
			{usage.weekAllPct}%
		</span>
		{#if usage.weekModelName}
			<span title={usage.weekModelResets ? `resets ${usage.weekModelResets}` : ''}>
				{usage.weekModelName.toLowerCase()} {usage.weekModelPct}%
			</span>
		{/if}
	</div>
{/if}
