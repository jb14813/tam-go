import { getJSON, errorMessage } from '$lib/client/api';

const defaultSettings = {
	remote_server: '',
	remote_key: '',
	remote_port: '8000',
	remote_tls: false,
	default_pref: 'CALL',
	venue_name: '',
	disable_attrib: false
};

export const load = async ({ fetch }) => {
	try {
		const settings = await getJSON('/api/settings', { fetch });
		return { settings: { ...defaultSettings, ...settings }, loadError: '' };
	} catch (e) {
		return {
			settings: { ...defaultSettings },
			loadError: `Could not load settings: ${errorMessage(e)}`
		};
	}
};
