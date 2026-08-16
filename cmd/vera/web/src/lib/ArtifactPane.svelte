<script>
	import {
		listArtifacts,
		getArtifact,
		createArtifact,
		updateArtifact,
		deleteArtifact
	} from '$lib/state.svelte.js';

	let { agentId, onchanged, onclose = null } = $props();

	let list = $state(null); // null = loading
	let sel = $state(null); // the open artifact, or null for the list
	let title = $state('');
	let content = $state('');
	let dirty = $state(false);
	let saving = $state(false);
	let error = $state('');

	async function load() {
		try {
			list = await listArtifacts(agentId);
			error = '';
		} catch (e) {
			error = e.message;
		}
	}
	$effect(() => {
		agentId;
		sel = null;
		load();
	});

	async function open(id) {
		try {
			sel = await getArtifact(agentId, id);
			title = sel.title;
			content = sel.content;
			dirty = false;
			confirmDel = false;
			error = '';
		} catch (e) {
			error = e.message;
		}
	}

	function startNew() {
		sel = { id: '', title: '', content: '' };
		title = '';
		content = '';
		dirty = true;
	}

	async function save() {
		if (saving) return;
		saving = true;
		error = '';
		try {
			sel = sel.id
				? await updateArtifact(agentId, sel.id, title, content)
				: await createArtifact(agentId, title, content);
			title = sel.title;
			dirty = false;
			await load();
			onchanged?.();
		} catch (e) {
			error = e.message;
		} finally {
			saving = false;
		}
	}

	// Deleting is forever — the first click arms, the second commits.
	let confirmDel = $state(false);
	async function remove() {
		if (!sel?.id) return;
		if (!confirmDel) {
			confirmDel = true;
			return;
		}
		confirmDel = false;
		try {
			await deleteArtifact(agentId, sel.id);
			sel = null;
			await load();
			onchanged?.();
		} catch (e) {
			error = e.message;
		}
	}

	function onKeydown(e) {
		if ((e.metaKey || e.ctrlKey) && e.key === 's') {
			e.preventDefault();
			if (dirty) save();
		}
	}

	function relAge(iso) {
		const s = (Date.now() - new Date(iso).getTime()) / 1000;
		if (s < 60) return 'now';
		if (s < 3600) return `${Math.floor(s / 60)}m`;
		if (s < 172800) return `${Math.floor(s / 3600)}h`;
		return `${Math.floor(s / 86400)}d`;
	}
</script>

<!-- below sm the shelf leaves the flow and takes the screen; the ✕
     (phone-only) hands it back -->
<aside
	class="flex w-full max-w-sm shrink-0 flex-col border-l border-zinc-800 pl-4 max-sm:fixed max-sm:inset-0 max-sm:z-40 max-sm:max-w-none max-sm:border-l-0 max-sm:bg-zinc-950 max-sm:px-4 max-sm:pb-3"
>
	<div class="flex items-baseline gap-2 border-b border-zinc-800 py-3">
		{#if sel}
			<button
				aria-label="back to the artifact list"
				class="px-1 text-zinc-500 hover:text-zinc-300"
				onclick={() => {
					sel = null;
					confirmDel = false;
				}}>←</button
			>
			<span class="text-xs font-semibold tracking-widest text-zinc-500 uppercase">artifact</span>
			<span class="grow"></span>
			{#if sel.id}
				<button
					class="text-[11px] {confirmDel
						? 'font-semibold text-rose-400'
						: 'text-zinc-500 hover:text-rose-400'}"
					onclick={remove}>{confirmDel ? 'delete — sure?' : 'delete'}</button
				>
			{/if}
			<button
				class="rounded-md bg-sky-400 px-2.5 py-0.5 text-[11px] font-semibold text-zinc-950 disabled:opacity-40"
				disabled={!dirty || saving}
				onclick={save}
			>
				{saving ? 'saving…' : dirty ? 'save' : 'saved'}
			</button>
		{:else}
			<span class="text-xs font-semibold tracking-widest text-zinc-500 uppercase">artifacts</span>
			<span class="grow"></span>
			<button class="text-[11px] text-zinc-500 hover:text-zinc-300" onclick={startNew}>+ new</button>
		{/if}
		{#if onclose}
			<button
				aria-label="close the artifact shelf"
				class="px-1 text-zinc-500 hover:text-zinc-300 sm:hidden"
				onclick={onclose}>✕</button
			>
		{/if}
	</div>

	{#if error}
		<div class="mt-2 text-[12px] text-rose-400">{error}</div>
	{/if}

	{#if sel}
		<!-- the editor -->
		<input
			type="text"
			bind:value={title}
			oninput={() => (dirty = true)}
			onkeydown={onKeydown}
			placeholder="title"
			class="mt-3 rounded-lg border border-zinc-800 bg-zinc-950 px-3 py-1.5 text-[13px] font-semibold placeholder:text-zinc-600 focus:border-sky-400 focus:outline-none"
		/>
		<textarea
			bind:value={content}
			oninput={() => (dirty = true)}
			onkeydown={onKeydown}
			placeholder="the document — markdown welcome (⌘S saves)"
			class="mt-2 grow resize-none rounded-lg border border-zinc-800 bg-zinc-950 px-3 py-2 text-[12.5px] leading-relaxed placeholder:text-zinc-600 focus:border-sky-400 focus:outline-none"
		></textarea>
	{:else if list === null}
		<div class="py-6 text-center text-[12px] text-zinc-600">looking…</div>
	{:else if list.length === 0}
		<div class="py-6 text-center text-[12px] text-zinc-600">
			nothing on the shelf — “+ new” starts one
		</div>
	{:else}
		<div class="mt-2 space-y-1 overflow-y-auto">
			{#each list as a (a.id)}
				<button
					class="flex w-full items-baseline gap-2 rounded-lg border border-zinc-800/70 bg-zinc-900/50 px-3 py-2 text-left hover:border-zinc-600"
					onclick={() => open(a.id)}
				>
					<span class="truncate text-[13px] font-medium">{a.title}</span>
					<span class="grow"></span>
					<span class="flex-none text-[11px] text-zinc-600">{relAge(a.updatedAt)}</span>
				</button>
			{/each}
		</div>
	{/if}
</aside>
