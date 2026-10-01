import { getJSON } from '$lib/client/api';

export const load = async ({ params, fetch }) => {
	const [prefixes, reportLines, settings] = await Promise.all([
		getJSON('/api/prefixes', { fetch }),
		getJSON(`/api/reports/bybasket/${encodeURIComponent(params.prefix)}`, { fetch }),
		getJSON('/api/settings', { fetch })
	]);
	const prefix = Array.from(prefixes).find((p) => p.prefix == params.prefix) || {
		prefix: params.prefix,
		color: 'gray',
		weight: 0
	};
	return { prefixes, prefix, reportLines, venueName: settings.venue_name || '' };
};
