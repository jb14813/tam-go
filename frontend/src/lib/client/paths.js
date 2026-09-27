import { resolve } from '$app/paths';

/**
 * The path of a page for one prefix: prefixPage('/tickets/[prefix]', '#1')
 * is /web/tickets/%231. resolve() puts a parameter into the path as it is,
 * and a prefix name may hold characters that end or change a path (#, ?, %),
 * so the name is encoded; the page gets it back decoded in its params. API
 * paths encode the name the same way, with encodeURIComponent.
 */
export function prefixPage(route, prefix) {
	return resolve(route, { prefix: encodeURIComponent(prefix) });
}
