import { getJSON } from '$lib/client/api';

export const load = async ({ fetch }) => {
	const prefix = {
		prefix: '',
		color: 'gray',
		weight: 0
	};
	const prefixes = await getJSON('/api/prefixes', { fetch });
	return { prefix, prefixes };
};
