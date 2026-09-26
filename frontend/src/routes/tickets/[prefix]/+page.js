import { getJSON } from '$lib/client/api';

export const load = async ({ params, fetch }) => {
	const { prefix } = params;
	const [prefixes, settings] = await Promise.all([
		getJSON('/api/prefixes', { fetch }),
		// The workstation's default contact preference for new rows; CALL when
		// the settings cannot be read.
		getJSON('/api/settings', { fetch }).catch(() => ({}))
	]);
	const prefixObj = Array.from(prefixes).find((p) => p.prefix == prefix) || {
		prefix,
		color: 'gray',
		weight: 0
	};
	const defaultPref = settings && settings.default_pref ? String(settings.default_pref) : 'CALL';
	return { prefix: prefixObj, prefixes, defaultPref };
};
