<script>
	import { untrack } from 'svelte';
	import { resolve } from '$app/paths';
	import { invalidateAll } from '$app/navigation';
	import { bS, iS, tS } from '$lib/client/styles';
	import { postJSON, pollJSON, readDetail, errorMessage, saves, API_UNREACHABLE } from '$lib/client/api';
	import HeaderBar from '$lib/client/components/HeaderBar.svelte';

	let { data } = $props();
	let loadError = $derived(data.loadError || '');
	// Editable working copy of the loaded settings (intentionally captured once).
	let settings = $state(untrack(() => ({ ...data.settings })));
	let savedSettings = $state(untrack(() => ({ ...data.settings })));
	let saving = $state(false);
	let unsaved = $derived(Object.keys(settings).some((key) => settings[key] !== savedSettings[key]));
	let status = $state({
		message: '',
		color: 'green'
	});

	const pageTitle = 'Settings | TAM';

	// --- Server section: pairing, discovered servers and the failed saves ---
	const SERVERS_POLL_MS = 5000;
	const STATUS_POLL_MS = 5000;

	// A server is set either by pairing, which also names it, or by typing
	// it into the Remote Mode fields below, where the
	// key comes from Auth Keys. Only the first is "paired".
	let configured = $derived(!!data.settings.remote_server);
	let paired = $derived(configured && !!data.settings.remote_name);
	let pairedName = $derived(data.settings.remote_name || data.settings.remote_server);
	let serverAddress = $derived(`${data.settings.remote_server}:${data.settings.remote_port}`);
	let pairVerb = $derived(paired ? 'Pair again' : 'Pair');
	let servers = $state([]);
	let discoveryError = $state('');
	// Fields for pairing; a discovered server's Use button fills them, and
	// while paired they hold the current server, for pairing again.
	let pair = $state(untrack(() => pairFields(data.settings)));
	let busy = $state(false);
	let serverMsg = $state({ message: '', color: 'green' });
	// Saves the server rejected, and saves still waiting to reach it, from
	// GET /api/status (0 in standalone mode).
	let failed = $state(0);
	let pending = $state(0);
	// The connection state, from GET /api/status ('' in standalone mode).
	let connState = $state('');
	// A refused key and a changed certificate are both fixed by pairing again.
	let mustPairAgain = $derived(connState === 'unauthenticated' || connState === 'certificate');

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
		const before = { ...savedSettings };
		await invalidateAll();
		savedSettings = { ...data.settings };
		for (const [key, value] of Object.entries(data.settings)) {
			if (settings[key] === before[key]) settings[key] = value;
		}
		pair = pairFields(data.settings);
	}

	async function saveSettings() {
		if (loadError || saving || busy) return;
		const submitted = { ...settings };
		saving = true;
		status = { message: 'Saving settings…', color: 'green' };
		try {
			let res;
			try { res = await postJSON('/api/settings', submitted); }
			catch { throw new Error(API_UNREACHABLE); }
			if (!res.ok) throw new Error(`Error Code: ${res.status} (${await readDetail(res)})`);
			const accepted = await res.json();
			savedSettings = { ...accepted };
			// The acknowledgement belongs to the submitted snapshot. Keep any
			// fields typed since then, including while the page load refreshes.
			for (const [key, value] of Object.entries(accepted)) {
				if (settings[key] === submitted[key]) settings[key] = value;
			}
			status = {
				message: Object.keys(settings).some((key) => settings[key] !== accepted[key])
					? 'Settings saved. Newer edits on this page still need saving.'
					: 'Settings saved successfully!',
				color: 'green'
			};
			await invalidateAll();
		} catch (e) {
			status = { message: errorMessage(e), color: 'red' };
		} finally { saving = false; }
	}

	function useServer(s) {
		if (busy || saving) return;
		pair.host = s.host || s.name || '';
		pair.port = String(s.port || (s.tls ? '8443' : '8000'));
		pair.tls = !!s.tls;
	}

	async function doPair() {
		if (busy || saving) return;
		const host = String(pair.host || '').trim();
		const port = String(pair.port || '').trim();
		if (!host) return say('Enter the server host or pick one from the list', 'red');
		if (!port) return say('Enter the server port', 'red');
		busy = true;
		try {
			// The event's waiting saves follow the server, including a replacement.
			const r = await post('/api/pair', { host, port, tls: !!pair.tls, password: pair.password });
			// The whole answer may also describe the retained local data.
			say(r.message, r.ok ? 'green' : 'red');
			if (r.ok) {
				pair.password = '';
				await reloadSettings();
				await pollStatus();
			}
		} finally { busy = false; }
	}

	async function doUnpair() {
		if (busy || saving) return;
		// Keep the event copy and pause delivery until a server is configured.
		const now = pending > 0 ? ` (${pending} now)` : '';
		if (
			!confirm(
				`Unpair from ${pairedName}? This client keeps its event data and works standalone. Saves waiting to reach the server${now} stay queued and resume when a server is configured again.`
			)
		)
			return;
		busy = true;
		try {
			const r = await post('/api/unpair', {});
			say(r.message, r.ok ? 'green' : 'red');
			if (r.ok) await reloadSettings();
		} finally { busy = false; }
	}

	async function retryFailed() {
		if (busy || saving) return;
		busy = true;
		const r = await post('/api/outbox/retry', {});
		busy = false;
		say(r.message, r.ok ? 'green' : 'red');
		await pollStatus();
	}

	async function discardFailed() {
		if (busy || saving) return;
		if (
			!confirm(
				`Discard the ${saves(failed)} that could not be sent? They will not be sent automatically. Their entries stay on this client and in backups.`
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
		const remote = code === 200 && s && s.mode === 'remote';
		failed = remote ? Number(s.failed) || 0 : 0;
		pending = remote ? Number(s.pending) || 0 : 0;
		connState = remote ? String(s.state || '') : '';
		return code;
	}

	// The connection state, for Pair again and the "could not be sent" line, while the page is open.
	$effect(() => {
		let stopped = false;
		let timer;
		const loop = async () => {
			await pollStatus();
			if (stopped) return;
			timer = setTimeout(loop, STATUS_POLL_MS);
		};
		loop();
		return () => {
			stopped = true;
			clearTimeout(timer);
		};
	});

	// The servers found on the network, while no server is set.
	$effect(() => {
		if (configured) return;
		let stopped = false;
		let timer;
		const loop = async () => {
			const { status: code, data: list } = await pollJSON('/api/servers');
			if (stopped) return;
			if (code === 200 && Array.isArray(list)) {
				servers = list;
				discoveryError = '';
			} else {
				servers = [];
				discoveryError = code === 0 ? API_UNREACHABLE : `TAM server discovery unavailable (HTTP ${code}). Check the client installation.`;
			}
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

<!-- The pairing form: for a first pairing, and while paired for pairing again. -->
{#snippet pairForm(label)}
	<fieldset disabled={busy || saving} class="flex flex-col gap-1">
	<div class="flex flex-row gap-1 items-center">
		<label for="pair_host">Host:</label>
		<input type="text" id="pair_host" class={iS.normal} bind:value={pair.host} />
	</div>
	<div class="flex flex-row gap-1 items-center">
		<label for="pair_port">Port:</label>
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
		<label for="pair_password">Server password:</label>
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
			disabled={busy || saving}
			onclick={doPair}>{label}</button
		>
	</div>
	</fieldset>
{/snippet}

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
		{#if configured}
			<div class="flex flex-row gap-1 items-center">
				<div>
					{#if paired}
						Paired with <span class="font-bold">{pairedName}</span>
						({serverAddress}, TLS {data.settings.remote_tls ? 'on' : 'off'})
					{:else}
						Remote server <span class="font-bold">{serverAddress}</span>
						(TLS {data.settings.remote_tls ? 'on' : 'off'}), set in the Remote Mode fields below,
						not paired{data.settings.remote_key ? '; it uses the key chosen under Auth Keys' : ''}
					{/if}
				</div>
				<button
					class="{bS.red} disabled:opacity-50 disabled:cursor-not-allowed"
					disabled={busy || saving}
					onclick={doUnpair}>Unpair</button
				>
			</div>
			<div
				id="pair_again"
				class="flex flex-col gap-1 self-start max-w-3xl {mustPairAgain
					? 'p-2 border-2 border-red-600 rounded bg-red-50'
					: ''}"
			>
				{#if connState === 'certificate'}
					<p class="{tS.red} font-bold">
						The server's certificate changed since this client paired with it. If the server was
						set up again or given a new certificate, {pairVerb.toLowerCase()} with the server password
						to trust the new one; the saves waiting stay queued and are sent once paired.
					</p>
				{:else if connState === 'unauthenticated'}
					<p class="{tS.red} font-bold">
						The server refused this client's key. {pairVerb} with the server password: the saves
						waiting stay queued and are sent once paired.
					</p>
				{:else if paired}
					<div>
						Pair again with the server password when the server refuses this client's key, its
						certificate changed, or it moved to another address:
					</div>
				{:else}
					<div>Pair with the server password to give this client a key of its own:</div>
				{/if}
				{@render pairForm(pairVerb)}
			</div>
		{:else}
			<div>Servers on this network:</div>
			{#if discoveryError}<p role="alert" class={tS.red}>{discoveryError}</p>{/if}
			{#each servers as s}
				<div class="flex flex-row gap-1 items-center">
					<div>
						<span class="font-bold">{s.name || s.host}</span>
						{s.host}:{s.port} (TLS {s.tls ? 'on' : 'off'}{s.version ? `, v${s.version}` : ''})
					</div>
					<button class={bS.gray} disabled={busy || saving} onclick={() => useServer(s)}>Use</button>
				</div>
			{:else}
				{#if !discoveryError}<div class="italic">Looking for servers on this network...</div>{/if}
			{/each}
			{@render pairForm('Pair')}
		{/if}
		{#if failed > 0}
			<p class="text-sm">Retry uses the values from the failed saves, which may be older than your current entries. It saves those values on this client and sends them after the current queue.</p>
			<div class="flex flex-row gap-1 items-center">
				<div class={tS.red}>{saves(failed)} could not be sent</div>
				<button
					class="{bS.gray} disabled:opacity-50 disabled:cursor-not-allowed"
					disabled={busy || saving}
					onclick={retryFailed}>Retry</button
				>
				<button
					class="{bS.red} disabled:opacity-50 disabled:cursor-not-allowed"
					disabled={busy || saving}
					onclick={discardFailed}>Discard</button
				>
			</div>
		{/if}
		{#if serverMsg.message}
			<p class="{tS[serverMsg.color]} break-words">{serverMsg.message}</p>
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
				disabled={!!loadError || saving || busy}
				onclick={saveSettings}>Save</button
			>
			<button
				class={bS.gray}
				disabled={saving}
				onclick={() => {
					settings = { ...savedSettings };
				}}>Cancel</button
			>
		</div>
		<div>
			<p class={tS[loadError ? 'red' : status.color]}>{loadError || status.message}</p>
			{#if unsaved}<p>Unsaved settings changes.</p>{/if}
		</div>
	</div>
</div>
