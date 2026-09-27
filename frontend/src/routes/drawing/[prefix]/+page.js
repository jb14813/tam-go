import { getJSON } from '$lib/client/api';

export const load = async ({ params, fetch }) => {
	const { prefix } = params;
	const prefixes = await getJSON('/api/prefixes', { fetch });
	const prefixObj = Array.from(prefixes).find((p) => p.prefix == prefix) || {
		prefix,
		color: 'gray',
		weight: 0
	};
	return { prefix: prefixObj, prefixes };
};
