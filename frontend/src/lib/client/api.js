import { error } from '@sveltejs/kit';

export const API_UNREACHABLE = 'Could not reach the TAM client API';

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
	return res.json();
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
 */
export async function saveMarked(
	url,
	rows,
	{ keepalive = false, saved = (r) => JSON.stringify(r) } = {}
) {
	if (rows.length === 0) return '';
	const sent = rows.map(saved);
	let res;
	try {
		res = await postJSON(url, rows, { keepalive });
	} catch {
		return SAVE_UNREACHABLE;
	}
	if (!res.ok) {
		const reason = (await readDetail(res)).replace(/[\s.]+$/, '');
		return `Nothing was saved: ${reason}. Your rows are still on this page.`;
	}
	// A row typed in again while the save was on its way keeps its mark, so
	// the next save sends what is on the screen now.
	rows.forEach((r, i) => {
		if (saved(r) === sent[i]) r.changed = false;
	});
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
	const leave = () => {
		const rows = marked();
		if (rows.length === 0) return;
		const now = JSON.stringify(rows);
		if (now === sending) return;
		sending = now;
		Promise.resolve(save({ keepalive: true })).finally(() => {
			if (sending === now) sending = '';
		});
	};
	const hidden = () => {
		if (document.visibilityState === 'hidden') leave();
	};
	window.addEventListener('beforeunload', leave);
	window.addEventListener('pagehide', leave);
	document.addEventListener('visibilitychange', hidden);
	return () => {
		window.removeEventListener('beforeunload', leave);
		window.removeEventListener('pagehide', leave);
		document.removeEventListener('visibilitychange', hidden);
	};
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
