<script>
	// The explorer, on its own ground: browse the fenced directories
	// and say the first word anywhere — a session is born there and
	// the cockpit opens on it. Reuses the nk-styled Explorer panel;
	// closing hands you back to the Vera home surface.
	import { goto } from '$app/navigation';
	import Explorer from '$lib/Explorer.svelte';
</script>

<div
	class="nk"
	style="height: 100dvh; display: flex; justify-content: center; background: var(--color-bg); color: var(--color-text); font-family: var(--font-body);"
>
	<section
		style="width: min(420px, 100%); display: flex; flex-direction: column; min-height: 0; padding: 14px 16px; border-right: 1px solid var(--color-divider); border-left: 1px solid var(--color-divider);"
	>
		<div style="display: flex; align-items: center; gap: 10px; padding-bottom: 10px;">
			<a
				href="/"
				aria-label="back home"
				style="font-size: 13px; line-height: 1; padding: 4px 6px; margin-left: -6px; color: var(--color-neutral-500); text-decoration: none; border-radius: var(--radius-sm);">←</a
			>
			<span style="font-family: var(--font-heading); font-weight: 500; font-size: 14px;">explorer</span>
			<span style="font-size: 11.5px; color: var(--color-neutral-600);"
				>browse directories · start a session anywhere</span
			>
		</div>
		<Explorer onclose={() => goto('/')} />
	</section>
</div>
