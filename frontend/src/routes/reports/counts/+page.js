import { getJSON } from '$lib/client/api';

export const load = async ({ fetch }) => {
	const prefixes = await getJSON('/api/prefixes', { fetch });
	return { prefixes };
};
