<script>
	import { untrack } from 'svelte';
	import { resolve } from '$app/paths';
	import { bS, iS, tS } from '$lib/client/styles';
	import { postJSON, readDetail } from '$lib/client/api';
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
						setTimeout(() => window.location.reload(), 3000);
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
