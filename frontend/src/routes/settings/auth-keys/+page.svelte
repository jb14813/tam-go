<script>
	import HeaderBar from '$lib/client/components/HeaderBar.svelte';
	import { resolve } from '$app/paths';
	import { bS, iS, tS } from '$lib/client/styles';
	import { postJSON } from '$lib/client/api';
	let { data } = $props();

	const pageTitle = 'Auth Keys | TAM';

	let auth = $state({
		toggle: false,
		pwd: ''
	});

	let authKeys = $state([]);
	let newDesc = $state('');
	let curKey = $state('');
	let errorStatus = $state('');

	function maskKey(key) {
		const k = String(key ?? '');
		return '*'.repeat(Math.max(k.length - 4, 0)) + k.slice(-4);
	}

	async function getKeys() {
		let res;
		try {
			res = await fetch('/api/auth', {
				headers: { 'TAM-PWD': auth.pwd }
			});
		} catch {
			errorStatus = 'Could not reach the TAM client API';
			setTimeout(() => (errorStatus = ''), 7000);
			return;
		}
		if (res.ok) {
			auth.toggle = true;
			const resData = await res.json();
			authKeys = [...resData];
			curKey = data.authKey;
		} else {
			errorStatus = 'Invalid Password!';
			setTimeout(() => (errorStatus = ''), 7000);
		}
	}
</script>

<svelte:head>
	<title>{pageTitle}</title>
</svelte:head>

<div id="app_container" class="p-1">
	<HeaderBar>
		<a href={resolve('/settings')} class={bS.gray}>Back to Settings</a>
		<div>Other Settings:</div>
		<a href={resolve('/settings/prefixes')} class={bS.gray}>Prefixes</a>
	</HeaderBar>
	<h1 class="text-xl font-bold">{pageTitle}</h1>
	{#if !auth.toggle}
		<div class="flex flex-row gap-1 items-center py-1">
			<div>Password:</div>
			<input
				type="password"
				id="pwd_input"
				class={iS.normal}
				onkeydown={(event) => {
					if (event.key == 'Enter') {
						getKeys();
					}
				}}
				bind:value={auth.pwd}
			/>
			<button class={bS.gray} onclick={getKeys}>Login</button>
		</div>
		<div class="py-1">
			<p class={tS.red}>{errorStatus}</p>
		</div>
	{:else}
		<table class="w-full my-1">
			<thead class="text-left">
				<tr>
					<th>AuthKey</th>
					<th>Description</th>
					<th>Actions</th>
				</tr>
			</thead>
			<tbody>
				{#each authKeys as key (key.auth_key)}
					<tr>
						<td>{maskKey(key.auth_key)}</td>
						<td>{key.description}</td>
						<td
							><div class="flex flex-row gap-1 items-center">
								{#if curKey == key.auth_key}
									<div>(Cur)</div>
								{:else}
									<button
										class={bS.gray}
										onclick={async () => {
											const res = await postJSON('/api/settings', { remote_key: key.auth_key });
											if (res.ok) {
												curKey = key.auth_key;
											}
										}}>Use</button
									>
								{/if}
								<button
									class={bS.red}
									onclick={async () => {
										const res = await fetch(
											`/api/auth?key_to_del=${encodeURIComponent(key.auth_key)}`,
											{
												method: 'DELETE',
												headers: { 'TAM-PWD': auth.pwd }
											}
										);
										if (res.ok) {
											setTimeout(() => {
												const newKeys = authKeys.filter((i) => i.auth_key !== key.auth_key);
												authKeys = [...newKeys];
											}, 1);
										}
									}}>Delete</button
								>
							</div></td
						>
					</tr>
				{/each}
				<tr>
					<td>New</td>
					<td
						><input type="text" id="new_desc" class="{iS.normal} w-full" bind:value={newDesc} /></td
					>
					<td>
						<button
							class={bS.gray}
							onclick={async () => {
								if (newDesc) {
									const res = await postJSON(
										'/api/auth',
										{ description: newDesc },
										{ headers: { 'TAM-PWD': auth.pwd } }
									);
									if (res.ok) {
										newDesc = '';
										getKeys();
									}
								}
							}}>New Key</button
						>
					</td>
				</tr>
			</tbody>
		</table>
	{/if}
</div>

<style>
	table td,
	table th {
		border: solid black 1px;
		padding: 0.25rem;
	}
</style>
