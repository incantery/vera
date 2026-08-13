// The index store and the API helpers. Both pages poll and mutate
// state in place, so cards and bubbles update without being recreated
// — which is what keeps a half-typed message alive across polls.

// The key: when roost serves beyond loopback, its startup URL carries
// ?key=…; stash it, strip it from the address bar, and send it on
// every API call. Loopback serving needs none and none is sent.
let apiKey = '';
if (typeof localStorage !== 'undefined') {
	try {
		const u = new URL(location.href);
		const qk = u.searchParams.get('key');
		if (qk) {
			localStorage.setItem('roost-key', qk);
			u.searchParams.delete('key');
			history.replaceState(null, '', u);
		}
		apiKey = localStorage.getItem('roost-key') ?? '';
	} catch {
		/* a browser that refuses storage still works on loopback */
	}
}

export function api(path, opts = {}) {
	const headers = { ...(opts.headers ?? {}) };
	if (apiKey) headers.Authorization = `Bearer ${apiKey}`;
	return fetch(path, { ...opts, headers });
}

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
		const r = await api('/api/state');
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
	const r = await api(`/api/agent/${id}`);
	if (!r.ok) throw new Error((await r.json()).error ?? 'the agent did not answer');
	return r.json();
}

export async function sayTo(id, text, verbatim = false) {
	const r = await api(`/api/agent/${id}/say`, {
		method: 'POST',
		body: JSON.stringify({ text, verbatim })
	});
	if (!r.ok) throw new Error((await r.json()).error ?? 'the message was refused');
}

export async function startDrive(sessionId, goal) {
	const r = await api('/api/drive', {
		method: 'POST',
		body: JSON.stringify({ sessionId, goal })
	});
	if (!r.ok) throw new Error((await r.json()).error ?? 'the drive was refused');
}

export async function stopDrive(id) {
	await api('/api/drive/stop', { method: 'POST', body: JSON.stringify({ id }) });
}

// ---- the artifact shelf ----

async function must(r, fallback) {
	if (!r.ok) throw new Error((await r.json()).error ?? fallback);
	return r.json();
}

export async function listArtifacts(agentId) {
	const st = await must(await api(`/api/agent/${agentId}/artifacts`), 'the shelf did not answer');
	return st.artifacts;
}

export async function getArtifact(agentId, id) {
	return must(await api(`/api/agent/${agentId}/artifacts/${id}`), 'that artifact is gone');
}

export async function createArtifact(agentId, title, content) {
	return must(
		await api(`/api/agent/${agentId}/artifacts`, {
			method: 'POST',
			body: JSON.stringify({ title, content })
		}),
		'the artifact was refused'
	);
}

export async function updateArtifact(agentId, id, title, content) {
	return must(
		await api(`/api/agent/${agentId}/artifacts/${id}`, {
			method: 'PUT',
			body: JSON.stringify({ title, content })
		}),
		'the save was refused'
	);
}

export async function deleteArtifact(agentId, id) {
	return must(
		await api(`/api/agent/${agentId}/artifacts/${id}`, { method: 'DELETE' }),
		'the delete was refused'
	);
}
