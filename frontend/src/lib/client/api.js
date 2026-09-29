import { error } from '@sveltejs/kit';
import { preserveDraft } from './drafts';

export const API_UNREACHABLE = 'Could not reach the TAM client API';

/** JSON numbers outside this range have already lost their exact identity. */
export function recordNumberError(value, { includeWinner = true } = {}) {
	for (const row of Array.isArray(value) ? value : [value]) {
		if (!row || typeof row !== 'object') continue;
		for (const field of ['t_id', 'b_id', 'winning_ticket']) {
			if (!(field in row) || (!includeWinner && field === 'winning_ticket')) continue;
			if (!Number.isSafeInteger(row[field]) || row[field] < 0) {
				return `Invalid ${field}: ticket and basket numbers must be whole numbers from 0 to ${Number.MAX_SAFE_INTEGER}.`;
			}
		}
	}
	return '';
}

export async function readRecords(res) {
	const data = await res.json();
	const problem = recordNumberError(data);
	if (problem) throw new Error(problem);
	return data;
}

/**
 * Reads the `detail` message of an API error response (`{detail: "..."}`),
 * falling back to the status text / code when the body is not JSON.
 */
export async function readDetail(res) {
	try {
		const data = await res.clone().json();
		if (data && typeof data.detail === 'string' && data.detail) return data.detail;
	} catch {
		// body was not JSON
	}
	return res.statusText || `Error Code: ${res.status}`;
}

/** Human readable message for anything thrown by fetch/getJSON. */
export function errorMessage(e) {
	if (e && typeof e === 'object') {
		if (e.body && typeof e.body.message === 'string') return e.body.message;
		if (typeof e.message === 'string') return e.message;
	}
	return String(e);
}

/**
 * GET a JSON endpoint. Throws a SvelteKit HttpError carrying the status and the
 * API `detail` on a non-ok response, or a 503 when the API cannot be reached.
 * Pass `{ fetch }` from a `load` function to use SvelteKit's fetch.
 */
export async function getJSON(url, { fetch: doFetch = globalThis.fetch, headers } = {}) {
	let res;
	try {
		res = await doFetch(url, headers ? { headers } : undefined);
	} catch {
		error(503, API_UNREACHABLE);
	}
	if (!res.ok) error(res.status, await readDetail(res));
	return readRecords(res);
}

/** A ticket placeholder has the same JSON shape as a saved blank ticket.
 * The client API headers distinguish existence and a shared-server answer
 * from this workstation's own local entries when the server is unavailable.
 */
export async function lookupTicket(prefix, id, { fetch: doFetch = globalThis.fetch } = {}) {
	const problem = recordNumberError({ t_id: id });
	if (problem) throw new Error(problem);
	let res;
	try {
		res = await doFetch(`/api/tickets/${encodeURIComponent(prefix)}/${id}`);
	} catch {
		error(503, API_UNREACHABLE);
	}
	if (!res.ok) error(res.status, await readDetail(res));
	return {
		ticket: await readRecords(res),
		found: res.headers.get('X-TAM-Found') === '1',
		source: res.headers.get('X-TAM-Source') === 'server' ? 'server' : 'local',
		mode: res.headers.get('X-TAM-Mode') === 'standalone' ? 'standalone' : 'remote'
	};
}

/**
 * POST a JSON body (always with `Content-Type: application/json`).
 * Returns the raw Response so callers can check `res.ok` and read the body.
 */
export function postJSON(
	url,
	body,
	{ fetch: doFetch = globalThis.fetch, headers = {}, keepalive = false } = {}
) {
	return doFetch(url, {
		method: 'POST',
		headers: { 'Content-Type': 'application/json', ...headers },
		body: JSON.stringify(body),
		// keepalive lets a save started from beforeunload outlive the page.
		keepalive
	});
}

/** What a form says when a save cannot reach the TAM client program. */
export const SAVE_UNREACHABLE =
	'Could not reach the TAM client program. Nothing was saved; your rows are still on this page.';

const latestSaves = new WeakMap();
const normalSaveQueues = new Map();
let updateDraft = () => true;
let editSession;
let editSequence = 0;

function nextEditHeaders() {
	// getRandomValues also works when a workstation is opened over plain LAN HTTP.
	editSession ||= Array.from(crypto.getRandomValues(new Uint8Array(16)),
		(byte) => byte.toString(16).padStart(2, '0')).join('');
	return {
		'X-TAM-Edit-Session': editSession,
		'X-TAM-Edit-Sequence': String(++editSequence)
	};
}

function sendMarked(url, body, keepalive, headers) {
	const send = () => postJSON(url, body, { keepalive, headers });
	// A queued callback cannot run after its page is destroyed. Send leave saves
	// immediately; the daemon uses the generation headers to reject older rows.
	if (keepalive) return send();
	const pending = (normalSaveQueues.get(url) || Promise.resolve()).then(send);
	const settled = pending.then(() => {}, () => {});
	normalSaveQueues.set(url, settled);
	settled.then(() => {
		if (normalSaveQueues.get(url) === settled) normalSaveQueues.delete(url);
	});
	return pending;
}

/** A load may replace rows only while their identities and values remain unchanged. */
export function unchangedRows(rows, value = (row) => JSON.stringify(row)) {
	const before = [...rows];
	const values = before.map(value);
	return (current) => current.length === before.length && current.every((row, index) =>
		row === before[index] && !row.changed && value(row) === values[index]);
}

/**
 * Saves a form's marked rows (`changed` set) with one POST to `url` and
 * unmarks the rows saved. Returns '' when they were saved (or there was
 * nothing to save), otherwise the message to show: the API's own reason, or
 * that the client program could not be reached (it was shut down or crashed).
 * Either way nothing was saved and the rows stay marked.
 *
 * `saved(row)` is the part of a row the save stores, the whole row unless
 * the form says otherwise; a row whose part changed while the save was on
 * its way stays marked.
 * `payload(row)` selects the fields the form actually edits for the request.
 */
export async function saveMarked(
	url,
	rows,
	{ keepalive = false, saved = (r) => JSON.stringify(r), payload = (r) => r } = {}
) {
	updateDraft();
	if (rows.length === 0) return '';
	const payloadRows = rows.map(payload);
	const problem = recordNumberError(payloadRows);
	if (problem) return `Nothing was saved: ${problem} Your rows are still on this page.`;
	const sent = rows.map(saved);
	const request = {};
	rows.forEach((row) => latestSaves.set(row, request));
	let res;
	try {
		// Both the values and their generation belong to the invocation, even
		// when another normal save must finish before this one can be sent.
		const body = JSON.parse(JSON.stringify(payloadRows));
		const headers = nextEditHeaders();
		res = await sendMarked(url, body, keepalive, headers);
	} catch {
		return SAVE_UNREACHABLE;
	}
	if (!res.ok) {
		const reason = (await readDetail(res)).replace(/[\s.]+$/, '');
		return `Nothing was saved: ${reason}. Your rows are still on this page.`;
	}
	// A row typed in again while the save was on its way keeps its mark, so
	// the next save sends what is on the screen now. An older acknowledgement
	// cannot clear the mark while a newer save for that row is still in flight.
	rows.forEach((r, i) => {
		if (latestSaves.get(r) === request && saved(r) === sent[i]) r.changed = false;
	});
	updateDraft();
	return '';
}

/**
 * Keeps a form's marked rows when the volunteer leaves the page, and returns
 * the function that stops listening. A tab that is closed runs its unload
 * handlers, but one that is only hidden can end without them: a browser may
 * discard a tab left in the background to free memory, and a phone or tablet
 * may stop it. So `save({ keepalive: true })` runs when the page is hidden as
 * well as when it is left or closed; keepalive lets the request outlive the
 * page. Closing a tab also hides it, so rows already on their way are not
 * sent a second time.
 *
 * `marked()` gives the rows marked now.
 */
export function saveOnLeave(marked, save) {
	let sending = '';
	const preserve = () => preserveDraft(marked());
	updateDraft = preserve;
	const leave = (event) => {
		const rows = marked();
		if (rows.length === 0) return;
		const retained = preserve();
		if (!retained && event?.type === 'beforeunload') {
			event.preventDefault();
			event.returnValue = '';
		}
		const now = JSON.stringify(rows);
		if (now === sending) return;
		sending = now;
		Promise.resolve(save({ keepalive: true })).finally(() => {
			preserve();
			if (sending === now) sending = '';
		});
	};
	let navigating = false;
	const navigate = async (event) => {
		const link = event.target.closest?.('a[href]');
		if (!link || event.defaultPrevented || event.button !== 0 || event.ctrlKey || event.metaKey || event.shiftKey || event.altKey || link.download || (link.target && link.target !== '_self')) return;
		const destination = new URL(link.href, location.href);
		if (destination.origin !== location.origin || (destination.pathname === location.pathname && destination.hash)) return;
		if (!marked().length) return;
		event.preventDefault();
		if (navigating) return;
		navigating = true;
		try {
			preserve();
			if (await save() && !marked().length) location.assign(destination.href);
		} finally { navigating = false; }
	};
	const hidden = () => {
		if (document.visibilityState === 'hidden') leave();
	};
	window.addEventListener('beforeunload', leave);
	window.addEventListener('pagehide', leave);
	document.addEventListener('visibilitychange', hidden);
	document.addEventListener('click', navigate);
	return () => {
		if (updateDraft === preserve) updateDraft = () => true;
		window.removeEventListener('beforeunload', leave);
		window.removeEventListener('pagehide', leave);
		document.removeEventListener('visibilitychange', hidden);
		document.removeEventListener('click', navigate);
	};
}

/** Normalize a requested sheet without changing the currently displayed range. */
export function pageRange(range) {
	if (!range.every(Number.isSafeInteger)) return null;
	let [from, to] = range.map((id) => Math.max(0, id));
	if (from > to) [from, to] = [to, from];
	return [from, Math.min(to, from + 300)];
}

/**
 * GET a JSON endpoint for polling: never throws. Returns `{ status, data }`
 * with the HTTP status (0 when the client could not be reached) and the parsed
 * body (null when the body is not JSON).
 */
export async function pollJSON(url, { fetch: doFetch = globalThis.fetch } = {}) {
	let res;
	try {
		res = await doFetch(url);
	} catch {
		return { status: 0, data: null };
	}
	let data = null;
	try {
		data = await res.json();
	} catch {
		// body was not JSON
	}
	return { status: res.status, data };
}

/** "1 save" / "2 saves" */
export function saves(n) {
	return `${n} save${n === 1 ? '' : 's'}`;
}
