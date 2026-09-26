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
