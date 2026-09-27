import { getJSON } from '$lib/client/api';

export const load = async ({ fetch }) => {
	const s = await getJSON('/api/settings', { fetch });
	return { remoteServer: s.remote_server || '' };
};
