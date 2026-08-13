// The index store and the API helpers. Both pages poll and mutate
// state in place, so cards and bubbles update without being recreated
// — which is what keeps a half-typed message alive across polls.

export const app = $state({
	sessions: [],
	current: '', // the agent whose lineage is freshest — the session you are living in
	drives: [],
	notice: '',
	turns: 4,
	usage: null,
	connected: true,
	loaded: false
});

export async function refresh() {
	try {
		const r = await fetch('/api/state');
		if (!r.ok) throw new Error(String(r.status));
		const st = await r.json();
		app.sessions = st.sessions ?? [];
		app.current = st.current ?? '';
		app.drives = st.drives ?? [];
		app.notice = st.notice ?? '';
		app.turns = st.turns ?? 4;
		app.usage = st.usage ?? null;
		app.connected = true;
		app.loaded = true;
	} catch {
		app.connected = false;
	}
}

export function startPolling(ms = 3000) {
	refresh();
	const t = setInterval(refresh, ms);
	return () => clearInterval(t);
}

export async function fetchAgent(id) {
	const r = await fetch(`/api/agent/${id}`);
	if (!r.ok) throw new Error((await r.json()).error ?? 'the agent did not answer');
	return r.json();
}

export async function sayTo(id, text, verbatim = false) {
	const r = await fetch(`/api/agent/${id}/say`, {
		method: 'POST',
		body: JSON.stringify({ text, verbatim })
	});
	if (!r.ok) throw new Error((await r.json()).error ?? 'the message was refused');
}

export async function startDrive(sessionId, goal) {
	const r = await fetch('/api/drive', {
		method: 'POST',
		body: JSON.stringify({ sessionId, goal })
	});
	if (!r.ok) throw new Error((await r.json()).error ?? 'the drive was refused');
}

export async function stopDrive(id) {
	await fetch('/api/drive/stop', { method: 'POST', body: JSON.stringify({ id }) });
}

// ---- the artifact shelf ----

async function must(r, fallback) {
	if (!r.ok) throw new Error((await r.json()).error ?? fallback);
	return r.json();
}

export async function listArtifacts(agentId) {
	const st = await must(await fetch(`/api/agent/${agentId}/artifacts`), 'the shelf did not answer');
	return st.artifacts;
}

export async function getArtifact(agentId, id) {
	return must(await fetch(`/api/agent/${agentId}/artifacts/${id}`), 'that artifact is gone');
}

export async function createArtifact(agentId, title, content) {
	return must(
		await fetch(`/api/agent/${agentId}/artifacts`, {
			method: 'POST',
			body: JSON.stringify({ title, content })
		}),
		'the artifact was refused'
	);
}

export async function updateArtifact(agentId, id, title, content) {
	return must(
		await fetch(`/api/agent/${agentId}/artifacts/${id}`, {
			method: 'PUT',
			body: JSON.stringify({ title, content })
		}),
		'the save was refused'
	);
}

export async function deleteArtifact(agentId, id) {
	return must(
		await fetch(`/api/agent/${agentId}/artifacts/${id}`, { method: 'DELETE' }),
		'the delete was refused'
	);
}
