import { getJSON, errorMessage } from '$lib/client/api';

export const load = async ({ fetch }) => {
	let error = '';
	const safe = async (url, fallback) => {
		try {
			return await getJSON(url, { fetch });
		} catch (e) {
			// An unreachable API and an API error both end up here; show the real reason.
			error = errorMessage(e);
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
