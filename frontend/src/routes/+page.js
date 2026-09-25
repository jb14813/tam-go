import { getJSON, API_UNREACHABLE } from '$lib/client/api';

export const load = async ({ fetch }) => {
	let error = '';
	const safe = async (url, fallback) => {
		try {
			return await getJSON(url, { fetch });
		} catch {
			error = API_UNREACHABLE;
			return fallback;
		}
	};
	const [whoami, prefixes, settings] = await Promise.all([
		safe('/api', {}),
		safe('/api/prefixes', []),
		safe('/api/settings', {})
	]);
	return {
		whoami: undefined,
		authenticated: undefined,
		healthy: undefined,
		...whoami,
		prefixes: Array.isArray(prefixes) ? prefixes : [],
		venueName: settings.venue_name || '',
		disableAttrib: settings.disable_attrib || false,
		error
	};
};
