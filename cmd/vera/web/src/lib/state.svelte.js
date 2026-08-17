// The index store and the API helpers. Both pages poll and mutate
// state in place, so cards and bubbles update without being recreated
// — which is what keeps a half-typed message alive across polls.

// The key: when vera serves beyond loopback, its startup URL carries
// ?key=…; stash it, strip it from the address bar, and send it on
// every API call. Loopback serving needs none and none is sent.
let apiKey = '';
if (typeof localStorage !== 'undefined') {
	try {
		const u = new URL(location.href);
		const qk = u.searchParams.get('key');
		if (qk) {
			localStorage.setItem('vera-key', qk);
			u.searchParams.delete('key');
			history.replaceState(null, '', u);
		}
		apiKey = localStorage.getItem('vera-key') ?? '';
	} catch {
		/* a browser that refuses storage still works on loopback */
	}
}

// setKey: the login screen's way in — same stash the ?key= URL uses.
export function setKey(k) {
	apiKey = k;
	try {
		localStorage.setItem('vera-key', k);
	} catch {
		/* storage refused — the key still lives for this page's lifetime */
	}
}

// checkAuth probes /api/auth and names the outcome: 'ok' (door
// open), 'denied' (wrong or missing key), 'down' (vera not
// answering). Plain fetch, not api() — a login attempt that fails
// must report, not redirect. A candidate key can be tried without
// touching the stash, so a typo never destroys a working key.
export async function checkAuth(candidate) {
	const k = candidate ?? apiKey;
	try {
		const headers = k ? { Authorization: `Bearer ${k}` } : {};
		const r = await fetch('/api/auth', { headers });
		return r.ok ? 'ok' : r.status === 401 ? 'denied' : 'down';
	} catch {
		return 'down';
	}
}

export function api(path, opts = {}) {
	const headers = { ...(opts.headers ?? {}) };
	if (apiKey) headers.Authorization = `Bearer ${apiKey}`;
	// Bodies are always JSON here; saying so keeps the request
	// non-simple, which is one more thing a cross-site form can't fake.
	if (opts.body && !headers['Content-Type']) headers['Content-Type'] = 'application/json';
	return fetch(path, { ...opts, headers }).then((r) => {
		// A 401 mid-session means the key changed under us — every
		// road leads back to the login screen, which returns you here.
		if (r.status === 401 && location.pathname !== '/login') {
			location.assign(`/login?next=${encodeURIComponent(location.pathname)}`);
		}
		return r;
	});
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

export async function fetchAgent(id, { raw = false } = {}) {
	// raw: direct mode reading the transcript as-is — the server skips
	// (and never bills) digests for this fetch.
	const r = await api(`/api/agent/${id}${raw ? '?digests=0' : ''}`);
	if (!r.ok) throw new Error((await r.json()).error ?? 'the agent did not answer');
	return r.json();
}

// sayTo rides the typed wire (the Say RPC) — the same core the REST
// endpoint still serves for anything that has not migrated. Connect
// errors carry the server's own words; strip the code prefix.
export async function sayTo(id, text, opts = {}) {
	const { verbatim = false, direct = false, perm = '', images = [] } = opts === true ? { verbatim: true } : opts;
	try {
		await veraClient.say({ id, text, verbatim, direct, perm, images });
	} catch (err) {
		throw new Error(err?.rawMessage || 'the message was refused');
	}
}

// ---- the typed wire: Connect streaming ----
// WatchAgent replaces polling on the agent page: full snapshot first,
// tail deltas after, every frame shaped exactly like the REST payload
// so the page cannot tell which rail fed it.
import { createClient } from '@connectrpc/connect';
import { createConnectTransport } from '@connectrpc/connect-web';
import { VeraService } from './gen/vera/v1/vera_pb';

const transport = createConnectTransport({
	baseUrl: '/',
	interceptors: [
		(next) => (req) => {
			if (apiKey) req.header.set('Authorization', `Bearer ${apiKey}`);
			return next(req);
		}
	]
});
const veraClient = createClient(VeraService, transport);

const n = (v) => Number(v ?? 0);

function fromMsg(m) {
	return {
		role: m.role,
		text: m.text,
		tools: m.tools || undefined,
		steps: m.steps?.length
			? m.steps.map((s) => ({
					tool: s.tool,
					detail: s.detail,
					diff: s.diff ? { file: s.diff.file, old: s.diff.old, new: s.diff.new, all: s.diff.replaceAll } : undefined,
					out: s.out || undefined,
					lines: s.lines || undefined,
					err: s.err || undefined
				}))
			: undefined,
		think: m.think?.length ? m.think : undefined,
		ctx: n(m.ctx) || undefined,
		secs: m.secs || undefined,
		rough: m.rough || undefined,
		digest: m.digest ? { state: m.digest.state, headline: m.digest.headline, bullets: m.digest.bullets } : undefined
	};
}

// applyFrame folds one stream frame into the page's data object,
// preserving the REST-only fields (usage, drives, notice, turns) the
// stream does not carry.
export function applyFrame(prev, f) {
	const history = f.reset || !prev?.history ? f.history.map(fromMsg) : [...prev.history.slice(0, f.from), ...f.history.map(fromMsg)];
	return {
		...(prev ?? {}),
		agent: {
			id: f.agent.id, title: f.agent.title, state: f.agent.state,
			dir: f.agent.dir, branch: f.agent.branch || undefined,
			tool: f.agent.tool || undefined, toolDetail: f.agent.toolDetail || undefined,
			ctxPct: f.agent.ctxPct || undefined, age: f.agent.age
		},
		history,
		ctx: f.ctx
			? { tokens: n(f.ctx.tokens), in: n(f.ctx.freshIn), cacheRead: n(f.ctx.cacheRead), cacheWrite: n(f.ctx.cacheWrite), out: n(f.ctx.out), window: n(f.ctx.window), model: f.ctx.model }
			: null,
		spend: f.spend ? { claudeUsd: f.spend.claudeUsd, judgeUsd: f.spend.judgeUsd } : (prev?.spend ?? null),
		pending: f.pending
			? { text: f.pending.text, sent: f.pending.sent || undefined, status: f.pending.status, error: f.pending.error || undefined, direct: f.pending.direct, perm: f.pending.perm, images: f.pending.images, at: new Date(n(f.pending.atUnixMs)).toISOString() }
			: null,
		queue: f.queue?.map((q) => ({ text: q.text, perm: q.perm })) ?? [],
		tree: f.tree?.map((t) => ({ path: t.path, add: t.add, del: t.del, new: t.isNew })) ?? [],
		resume: f.resume,
		artifacts: f.artifacts
	};
}

// watchAgent opens the stream and feeds frames until aborted or the
// stream errors; the caller owns the fallback story.
export function watchAgent(id, raw, onFrame, onDone) {
	const ac = new AbortController();
	(async () => {
		try {
			for await (const frame of veraClient.watchAgent({ id, raw }, { signal: ac.signal })) {
				onFrame(frame);
			}
			onDone?.(null);
		} catch (err) {
			if (!ac.signal.aborted) onDone?.(err);
		}
	})();
	return () => ac.abort();
}

// uploadImage stores one pasted image on the server; the answer's
// `path` rides the next say, its `name` serves the thumbnail back.
export async function uploadImage(id, blob) {
	const r = await api(`/api/agent/${id}/upload`, { method: 'POST', body: blob });
	if (!r.ok) throw new Error((await r.json()).error ?? 'the image was refused');
	return r.json();
}

// uploadUrl: <img> tags cannot send a Bearer header, so the key (when
// one guards the door) rides the query string the server also honors.
// The in-memory key, not the stash — a storage-refusing browser whose
// API calls work should see its images too.
export function uploadUrl(id, name) {
	return `/api/agent/${id}/uploads/${name}${apiKey ? `?key=${encodeURIComponent(apiKey)}` : ''}`;
}

// imageParts splits a message into its text and the attachment names
// the marker lines carry — the render-side twin of the server's
// withImages.
export function imageParts(text) {
	const names = [];
	const kept = [];
	for (const line of (text ?? '').split('\n')) {
		const m = line.match(/^\[image attached: (.+) — read this file to see it\]$/);
		if (m) names.push(m[1].split('/').pop());
		else kept.push(line);
	}
	return { text: kept.join('\n').trim(), names };
}

// interruptAgent rides the typed wire — the TUI's Esc as an RPC.
export async function interruptAgent(id) {
	try {
		await veraClient.interrupt({ id });
	} catch (err) {
		throw new Error(err?.rawMessage || 'nothing to interrupt');
	}
}

// ---- the board stream ----

const iso = (ms) => (ms ? new Date(Number(ms)).toISOString() : undefined);

// boardFrame reshapes one WatchBoard frame into exactly what the REST
// payloads served — the board and the rail cannot tell which rail fed
// them.
export function boardFrame(f) {
	return {
		tasks: (f.tasks ?? []).map((t) => ({
			id: t.id, title: t.title, intent: t.intent,
			agent: t.agent || undefined,
			goal: t.goal || undefined, goalActor: t.goalActor || undefined,
			col: t.col, state: t.state,
			ask: t.ask || undefined, face: t.face || undefined,
			pinned: t.pinned || undefined,
			proposal: t.proposal || undefined, proposalWhy: t.proposalWhy || undefined,
			proposalKind: t.proposalKind || undefined,
			proposalText: t.proposalText || undefined,
			autoStart: t.autoStart || undefined,
			budgetUsd: t.budgetUsd || undefined,
			costUsd: t.costUsd || undefined,
			runs: t.runs?.length ? t.runs.map((r) => ({ kind: r.kind, outcome: r.outcome, costUsd: r.costUsd })) : undefined,
			workspace: t.workspace || undefined, scratchName: t.scratchName || undefined,
			mode: t.mode || undefined,
			cadence: t.cadence || undefined, deadline: t.deadline || undefined,
			// The graph, when this card is a node in one — what lets the
			// board offer the work view instead of only a row.
			kind: t.kind || undefined, root: t.root || undefined,
			log: (t.log ?? []).map((e) => ({ at: iso(e.atUnixMs), actor: e.actor, text: e.text })),
			exchanges: t.exchanges?.length ? t.exchanges.map((x) => ({ prompt: x.prompt, reply: x.reply })) : undefined,
			createdAt: iso(t.createdUnixMs), updatedAt: iso(t.updatedUnixMs),
			live: t.live ? { dir: t.live.dir, state: t.live.state, now: t.live.now || undefined } : undefined
		})),
		inflight: f.inflight, spend: f.spend, notice: f.notice,
		fleet: { agents: f.fleet?.agents ?? 0, working: f.fleet?.working ?? 0 },
		repos: (f.repos ?? []).map((r) => ({
			dir: r.dir, cwd: r.cwd,
			...(r.scratch ? { scratch: 'yes' } : {}),
			...(r.bookmark ? { bookmark: 'yes' } : {})
		})),
		sessions: (f.sessions ?? []).map((s) => ({
			id: s.id, title: s.title, state: s.state, dir: s.dir, cwd: s.cwd,
			branch: s.branch || undefined, prompt: s.prompt || undefined,
			lastText: s.lastText || undefined, ctxPct: s.ctxPct || undefined,
			model: s.model || undefined, age: s.age, driving: s.driving,
			tool: s.tool || undefined, toolDetail: s.toolDetail || undefined,
			task: s.task || undefined, scratch: s.scratch || undefined
		})),
		current: f.current,
		usage: f.usage
			? {
					mode: f.usage.mode, sessionPct: f.usage.sessionPct, sessionResets: f.usage.sessionResets,
					weekAllPct: f.usage.weekAllPct, weekAllResets: f.usage.weekAllResets,
					weekModelName: f.usage.weekModelName, weekModelPct: f.usage.weekModelPct,
					weekModelResets: f.usage.weekModelResets
				}
			: null
	};
}

// watchBoard opens the home screen's stream; the caller owns the
// fallback story, same as watchAgent.
export function watchBoard(onFrame, onDone) {
	const ac = new AbortController();
	(async () => {
		try {
			for await (const frame of veraClient.watchBoard({}, { signal: ac.signal })) {
				onFrame(frame);
			}
			onDone?.(null);
		} catch (err) {
			if (!ac.signal.aborted) onDone?.(err);
		}
	})();
	return () => ac.abort();
}

// ---- the work view ----
// watchGoal opens one goal's stream. Frames are whole, so the page
// replaces rather than merges — a goal is a handful of nodes and a few
// dozen events, and a whole frame can never be stale in part.
export function watchGoal(id, onFrame, onDone) {
	const ac = new AbortController();
	(async () => {
		try {
			for await (const frame of veraClient.watchGoal({ id }, { signal: ac.signal })) {
				onFrame(goalFrame(frame));
			}
			onDone?.(null);
		} catch (err) {
			if (!ac.signal.aborted) onDone?.(err);
		}
	})();
	return () => ac.abort();
}

// goalFrame flattens the wire into what the view reads. Nothing is
// computed here that the server already decided — the semantic state
// is the server's word, so the phone and the web cannot disagree about
// what "Reviewing" means.
export function goalFrame(f) {
	return {
		id: f.id,
		title: f.title,
		state: f.state,
		face: f.face,
		spend: f.spend ?? 0,
		cursor: Number(f.cursor ?? 0),
		nodes: (f.nodes ?? []).map((n) => ({
			id: n.id,
			title: n.title,
			kind: n.kind,
			col: n.col,
			state: n.state,
			face: n.face,
			deps: n.deps ?? [],
			blockedBy: n.blockedBy ?? [],
			model: n.model,
			tier: n.tier,
			costUsd: n.costUsd ?? 0,
			readOnly: n.readOnly,
			ask: n.ask,
			live: n.liveState ? { state: n.liveState, now: n.liveNow } : null
		})),
		events: (f.events ?? []).map((e) => ({
			seq: Number(e.seq ?? 0),
			at: Number(e.atUnixMs ?? 0),
			kind: e.kind,
			node: e.node,
			text: e.text,
			src: e.src
				? { task: e.src.task, run: e.src.run, fork: e.src.fork, msg: e.src.msg, file: e.src.file }
				: null
		}))
	};
}

// The last sequence this browser has seen for a goal. Stored so that
// leaving for ten minutes and coming back shows what changed rather
// than an undifferentiated wall — which is the whole point of the view.
export function seenCursor(id) {
	try {
		return Number(localStorage.getItem(`vera-seen-${id}`) ?? 0);
	} catch {
		return 0;
	}
}

export function markSeen(id, cursor) {
	try {
		localStorage.setItem(`vera-seen-${id}`, String(cursor));
	} catch {
		/* storage refused — the marks are a nicety, not the record */
	}
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

// ---- the review surface ----
// The human's verdict on an agent's uncommitted work: read the whole
// diff, then approve (commit) or discard. "Request changes" is just
// words — the say rail already carries those. All three ride the
// typed wire (Review/Commit/Discard RPCs); errors keep the server's
// own words.

export async function agentDiff(id) {
	try {
		const r = await veraClient.review({ id });
		return {
			dir: r.dir,
			branch: r.branch,
			files: (r.files ?? []).map((f) => ({
				path: f.path, add: f.add, del: f.del,
				new: f.isNew || undefined, binary: f.binary || undefined,
				truncated: f.truncated || undefined, diff: f.diff
			}))
		};
	} catch (err) {
		throw new Error(err?.rawMessage || 'the diff did not answer');
	}
}

export async function commitTree(id, message) {
	try {
		return await veraClient.commit({ id, message });
	} catch (err) {
		throw new Error(err?.rawMessage || 'the commit was refused');
	}
}

export async function discardChange(id, path, all = false) {
	try {
		return await veraClient.discard({ id, path, all });
	} catch (err) {
		throw new Error(err?.rawMessage || 'the discard was refused');
	}
}

// ---- suggestions ----
// The vera agent's bid on your next move: a digest of the turn that
// just landed plus ranked replies you could send. Cached per turn
// server-side — refetching the same turn bills nothing.

export async function agentSuggest(id) {
	try {
		const r = await veraClient.suggest({ id });
		return { happened: r.happened, now: r.now, replies: r.replies ?? [] };
	} catch (err) {
		throw new Error(err?.rawMessage || 'vera did not answer');
	}
}
