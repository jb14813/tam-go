<script>
	import { untrack } from 'svelte';
	import { resolve } from '$app/paths';
	import { invalidateAll } from '$app/navigation';
	import { bS, iS, tS } from '$lib/client/styles';
	import { postJSON, pollJSON, readDetail, saves, API_UNREACHABLE } from '$lib/client/api';
	import HeaderBar from '$lib/client/components/HeaderBar.svelte';

	let { data } = $props();
	let loadError = $derived(data.loadError || '');
	// Editable working copy of the loaded settings (intentionally captured once).
	let settings = $state(untrack(() => ({ ...data.settings })));
	let status = $state({
		message: '',
		color: 'green'
	});

	const pageTitle = 'Settings | TAM';

	let reloadTimer;
	$effect(() => () => clearTimeout(reloadTimer));

	// --- Server section: pairing, discovered servers and the failed saves ---
	const NOT_SUPPORTED = 'This client does not support pairing yet';
	const SERVERS_POLL_MS = 5000;
	const STATUS_POLL_MS = 5000;

	let paired = $derived(!!data.settings.remote_server);
	let pairedName = $derived(data.settings.remote_name || data.settings.remote_server);
	let servers = $state([]);
	let pairingUnsupported = $state(false);
	// Fields for pairing by hand; a discovered server's Use button fills them.
	let pair = $state(untrack(() => pairFields(data.settings)));
	let busy = $state(false);
	let serverMsg = $state({ message: '', color: 'green' });
	// Saves the server rejected, from GET /api/status (0 in standalone mode).
	let failed = $state(0);

	function pairFields(s) {
		return {
			host: s.remote_server || '',
			port: s.remote_port || '8000',
			tls: !!s.remote_tls,
			password: ''
		};
	}

	function say(message, color = 'green') {
		serverMsg = { message, color };
	}

	// POST to one of the pairing/outbox routes; returns the answer's message or the error.
	async function post(url, body) {
		let res;
		try {
			res = await postJSON(url, body);
		} catch {
			return { ok: false, message: API_UNREACHABLE };
		}
		if (res.status === 404) return { ok: false, message: NOT_SUPPORTED };
		if (!res.ok) return { ok: false, message: await readDetail(res) };
		let answer = {};
		try {
			answer = await res.json();
		} catch {
			// no body
		}
		return { ok: true, message: (answer && answer.message) || 'Done' };
	}

	// Re-runs the page's load so the pairing state and the form show the saved settings.
	async function reloadSettings() {
		await invalidateAll();
		settings = { ...data.settings };
		pair = pairFields(data.settings);
	}

	function useServer(s) {
		pair.host = s.host || s.name || '';
		pair.port = String(s.port || (s.tls ? '8443' : '8000'));
		pair.tls = !!s.tls;
	}

	async function doPair() {
		if (busy) return;
		const host = String(pair.host || '').trim();
		const port = String(pair.port || '').trim();
		if (!host) return say('Enter the server host or pick one from the list', 'red');
		if (!port) return say('Enter the server port', 'red');
		busy = true;
		const r = await post('/api/pair', { host, port, tls: !!pair.tls, password: pair.password });
		busy = false;
		say(r.message, r.ok ? 'green' : 'red');
		if (r.ok) {
			pair.password = '';
			await reloadSettings();
		}
	}

	async function doUnpair() {
		if (busy) return;
		if (
			!confirm(
				`Unpair from ${pairedName}? This client goes back to standalone mode and keeps its local data.`
			)
		)
			return;
		busy = true;
		const r = await post('/api/unpair', {});
		busy = false;
		say(r.message, r.ok ? 'green' : 'red');
		if (r.ok) await reloadSettings();
	}

	async function retryFailed() {
		if (busy) return;
		busy = true;
		const r = await post('/api/outbox/retry', {});
		busy = false;
		say(r.message, r.ok ? 'green' : 'red');
		await pollStatus();
	}

	async function discardFailed() {
		if (busy) return;
		if (
			!confirm(
				`Discard the ${saves(failed)} that could not be sent? They will not reach the server.`
			)
		)
			return;
		busy = true;
		const r = await post('/api/outbox/discard', {});
		busy = false;
		say(r.message, r.ok ? 'green' : 'red');
		await pollStatus();
	}

	async function pollStatus() {
		const { status: code, data: s } = await pollJSON('/api/status');
		failed = code === 200 && s && s.mode === 'remote' ? Number(s.failed) || 0 : 0;
		return code;
	}

	// The connection state, for the "could not be sent" line, while the page is open.
	$effect(() => {
		let stopped = false;
		let timer;
		const loop = async () => {
			const code = await pollStatus();
			// An older client has no status route: no point asking again this page load.
			if (stopped || code === 404) return;
			timer = setTimeout(loop, STATUS_POLL_MS);
		};
		loop();
		return () => {
			stopped = true;
			clearTimeout(timer);
		};
	});

	// The servers found on the network, while not paired. An older client
	// answers 404: pairing is not available then.
	$effect(() => {
		if (paired || pairingUnsupported) return;
		let stopped = false;
		let timer;
		const loop = async () => {
			const { status: code, data: list } = await pollJSON('/api/servers');
			if (stopped) return;
			if (code === 404) {
				pairingUnsupported = true;
				return;
			}
			if (code === 200 && Array.isArray(list)) servers = list;
			timer = setTimeout(loop, SERVERS_POLL_MS);
		};
		loop();
		return () => {
			stopped = true;
			clearTimeout(timer);
		};
	});
</script>

<svelte:head>
	<title>{pageTitle}</title>
</svelte:head>

<div id="app_container" class="p-1">
	<HeaderBar>
		<div>Settings Sections:</div>
		{#if data.settings.remote_server}
			<a href={resolve('/settings/auth-keys')} class={bS.gray}>Auth Keys</a>
		{/if}
		<a href={resolve('/settings/prefixes')} class={bS.gray}>Prefixes</a>
		<a href={resolve('/settings/backuprestore')} class={bS.gray}>Backup/Restore</a>
	</HeaderBar>
	<h1 class="text-xl font-bold">{pageTitle}</h1>
	<div id="server_section" class="flex flex-col gap-1 w-full py-1">
		<h2 class="text-lg font-bold">Server:</h2>
		{#if paired}
			<div class="flex flex-row gap-1 items-center">
				<div>
					Paired with <span class="font-bold">{pairedName}:{data.settings.remote_port}</span>
					(TLS {data.settings.remote_tls ? 'on' : 'off'})
				</div>
				<button
					class="{bS.red} disabled:opacity-50 disabled:cursor-not-allowed"
					disabled={busy}
					onclick={doUnpair}>Unpair</button
				>
			</div>
		{:else if pairingUnsupported}
			<div>{NOT_SUPPORTED}</div>
		{:else}
			<div>Servers on this network:</div>
			{#each servers as s}
				<div class="flex flex-row gap-1 items-center">
					<div>
						<span class="font-bold">{s.name || s.host}</span>
						{s.host}:{s.port} (TLS {s.tls ? 'on' : 'off'}{s.version ? `, v${s.version}` : ''})
					</div>
					<button class={bS.gray} onclick={() => useServer(s)}>Use</button>
				</div>
			{:else}
				<div class="italic">Looking for servers on this network...</div>
			{/each}
			<div class="flex flex-row gap-1 items-center">
				<div>Host:</div>
				<input type="text" id="pair_host" class={iS.normal} bind:value={pair.host} />
			</div>
			<div class="flex flex-row gap-1 items-center">
				<div>Port:</div>
				<input type="text" id="pair_port" class={iS.normal} bind:value={pair.port} />
			</div>
			<div class="flex flex-row gap-1 items-center">
				<div>TLS:</div>
				<button
					class={bS.gray}
					onclick={() => {
						pair.tls = !pair.tls;
						pair.port = pair.tls ? '8443' : '8000';
					}}>{pair.tls ? 'Yes' : 'No'}</button
				>
			</div>
			<div class="flex flex-row gap-1 items-center">
				<div>Server password:</div>
				<input
					type="password"
					id="pair_password"
					autocomplete="off"
					class={iS.normal}
					onkeydown={(e) => {
						if (e.key == 'Enter') doPair();
					}}
					bind:value={pair.password}
				/>
			</div>
			<div class="flex flex-row gap-1 items-center">
				<button
					class="{bS.gray} disabled:opacity-50 disabled:cursor-not-allowed"
					disabled={busy}
					onclick={doPair}>Pair</button
				>
			</div>
		{/if}
		{#if failed > 0}
			<div class="flex flex-row gap-1 items-center">
				<div class={tS.red}>{saves(failed)} could not be sent</div>
				<button
					class="{bS.gray} disabled:opacity-50 disabled:cursor-not-allowed"
					disabled={busy}
					onclick={retryFailed}>Retry</button
				>
				<button
					class="{bS.red} disabled:opacity-50 disabled:cursor-not-allowed"
					disabled={busy}
					onclick={discardFailed}>Discard</button
				>
			</div>
		{/if}
		{#if serverMsg.message}
			<p class={tS[serverMsg.color]}>{serverMsg.message}</p>
		{/if}
	</div>
	<div class="flex flex-col gap-1 w-full py-1">
		<h2 class="text-lg font-bold">Remote Mode:</h2>
		<div class="flex flex-row gap-1 items-center">
			<div>Remote Server:</div>
			<input type="text" id="remote_server" class={iS.normal} bind:value={settings.remote_server} />
		</div>
		<div class="flex flex-row gap-1 items-center">
			<div>Remote Port:</div>
			<input type="text" id="remote_port" class={iS.normal} bind:value={settings.remote_port} />
		</div>
		<div class="flex flex-row gap-1 items-center">
			<div>Remote TLS:</div>
			<button
				class={bS.gray}
				onclick={() => {
					if (settings.remote_tls) {
						settings.remote_tls = false;
						settings.remote_port = '8000';
					} else if (!settings.remote_tls) {
						settings.remote_tls = true;
						settings.remote_port = '8443';
					}
				}}>{settings.remote_tls ? 'Yes' : 'No'}</button
			>
		</div>
		<h2 class="text-lg font-bold">Default Preferences:</h2>
		<div class="flex flex-row gap-1 items-center">
			<div>Contact Preference:</div>
			<button
				class={bS.gray}
				onclick={() => {
					settings.default_pref === 'CALL'
						? (settings.default_pref = 'TEXT')
						: (settings.default_pref = 'CALL');
				}}>{settings.default_pref}</button
			>
		</div>
		<div class="flex flex-row gap-1 items-center">
			<div>Venue Name:</div>
			<input type="text" id="venue_name" class={iS.normal} bind:value={settings.venue_name} />
		</div>
		<div class="flex flex-row gap-1 items-center">
			<div>Disable Attribution:</div>
			<button
				class={bS.gray}
				onclick={() => {
					settings.disable_attrib
						? (settings.disable_attrib = false)
						: (settings.disable_attrib = true);
				}}>{settings.disable_attrib ? 'Yes' : 'No'}</button
			>
		</div>
		<div class="flex flex-row gap-1 items-center">
			<button
				class="{bS.gray} disabled:opacity-50 disabled:cursor-not-allowed"
				disabled={!!loadError}
				onclick={async () => {
					if (loadError) return;
					let res;
					try {
						res = await postJSON('/api/settings', settings);
					} catch {
						status.message = 'Could not reach the TAM client API';
						status.color = 'red';
						return;
					}
					if (!res.ok) {
						status.message = `Error Code: ${res.status} (${await readDetail(res)})`;
						status.color = 'red';
					} else {
						const resData = await res.json();
						settings = { ...resData };
						status.message = 'Settings saved successfully!';
						status.color = 'green';
						clearTimeout(reloadTimer);
						reloadTimer = setTimeout(() => window.location.reload(), 3000);
					}
				}}>Save</button
			>
			<button
				class={bS.gray}
				onclick={() => {
					settings = { ...data.settings };
				}}>Cancel</button
			>
		</div>
		<div>
			<p class={tS[loadError ? 'red' : status.color]}>{loadError || status.message}</p>
		</div>
	</div>
</div>
