<script>
	import { resolve } from '$app/paths';
	import HeaderBar from '$lib/client/components/HeaderBar.svelte';
	import { bS, iS } from '$lib/client/styles';
	import { readDetail, API_UNREACHABLE } from '$lib/client/api';

	const pageTitle = 'Backup and Restore | TAM';

	let { data } = $props();
	let { remoteServer } = $derived(data);
	let uploadFile = $state();
	let contents = $state('');
	let results = $state('');
	let resultTimeout;

	function setResult(newResult) {
		results = newResult;
		clearTimeout(resultTimeout);
		resultTimeout = setTimeout(() => (results = ''), 10000);
	}

	$effect(() => {
		return () => {
			clearTimeout(resultTimeout);
		};
	});

	async function downloadBackupFile(target) {
		let fetch_url;
		if (target === 'local') {
			fetch_url = '/api/backuprestore/local';
		} else if (target === 'remote') {
			fetch_url = '/api/backuprestore/remote';
		}
		const now = new Date();
		const date = {
			year: now.getFullYear(),
			month: String(now.getMonth() + 1).padStart(2, '0'),
			day: String(now.getDate()).padStart(2, '0'),
			hour: String(now.getHours()).padStart(2, '0'),
			minutes: String(now.getMinutes()).padStart(2, '0')
		};
		let res;
		try {
			res = await fetch(fetch_url);
		} catch {
			setResult(`Error downloading ${target} data. ${API_UNREACHABLE}.`);
			return;
		}
		if (res.ok) {
			const backup = await res.json();
			const jsonString = JSON.stringify(backup, null, 2);
			const blob = new Blob([jsonString], { type: 'application/json' });
			const url = URL.createObjectURL(blob);

			const a = document.createElement('a');
			a.href = url;
			a.download = `TAM_${date.year}-${date.month}-${date.day} ${date.hour}-${date.minutes}.json`;
			document.body.appendChild(a);
			a.click();
			document.body.removeChild(a);
			URL.revokeObjectURL(url);
		} else {
			setResult(
				`Error downloading ${target} data. Error [${res.status}] ${await readDetail(res)}.`
			);
		}
	}

	function fileUpload(target) {
		const file = uploadFile && uploadFile[0];
		if (!file) {
			setResult('Please choose a backup file to upload first.');
			return;
		}
		const reader = new FileReader();
		reader.onload = async function (e) {
			contents = String(e.target.result);
			let res;
			try {
				res = await fetch(`/api/backuprestore/${target}`, {
					method: 'POST',
					headers: { 'Content-Type': 'application/json' },
					body: contents
				});
			} catch {
				setResult(`Error uploading file. ${API_UNREACHABLE}.`);
				return;
			}
			if (res.ok) setResult('File uploaded successfully. Check to see if your data exists.');
			else setResult(`Error uploading file. Error [${res.status}] ${await readDetail(res)}.`);
		};
		reader.onerror = function () {
			setResult('Error reading the selected file.');
		};
		reader.readAsText(file, 'utf-8');
	}

	async function pushData(target) {
		const targetStr = target.charAt(0).toUpperCase() + target.slice(1);
		let res;
		try {
			res = await fetch(`/api/backuprestore/push/${target}`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' }
			});
		} catch {
			setResult(`Error pushing ${targetStr}. ${API_UNREACHABLE}.`);
			return;
		}
		if (res.ok) {
			setResult(`${targetStr} pushed successfully.`);
		} else {
			setResult(`Error pushing ${targetStr}. Error [${res.status}] ${await readDetail(res)}.`);
		}
	}
</script>

<svelte:head>
	<title>{pageTitle}</title>
</svelte:head>

<div id="app_container" class="p-1">
	<HeaderBar>
		<a href={resolve('/settings')} class={bS.gray}>Back to Settings</a>
		<div>Other Settings</div>
		<a href={resolve('/settings/prefixes')} class={bS.gray}>Prefixes</a>
		{#if remoteServer}
			<a href={resolve('/settings/auth-keys')} class={bS.gray}>Auth Keys</a>
		{/if}
	</HeaderBar>
	<h1 class="text-xl font-bold">{pageTitle}</h1>
	<div class="my-1 p-1 border border-black rounded">
		<h2 class="text-lg font-bold">Backup File Downloads</h2>
		<div class="flex flex-row gap-1">
			<button class={bS.gray} onclick={() => downloadBackupFile('local')}>Local Data</button>
			{#if remoteServer}
				<button class={bS.gray} onclick={() => downloadBackupFile('remote')}>Remote Data</button>
			{/if}
		</div>
	</div>
	<div class="my-1 p-1 border border-black rounded">
		<h2 class="text-lg font-bold">Backup File Upload</h2>
		<div class="flex flex-row gap-1 items-center">
			<input
				type="file"
				accept=".json"
				class="{iS.normal} rounded file:bg-gray-300 file:border file:border-black file:px-2 file:py-1 file:rounded"
				bind:files={uploadFile}
			/>
			{#if !remoteServer}
				<button class={bS.gray} onclick={() => fileUpload('local')}>Upload to Local</button>
			{/if}
			{#if remoteServer}
				<button class={bS.gray} onclick={() => fileUpload('remote')}>Upload to Remote</button>
			{/if}
		</div>
	</div>
	{#if remoteServer}
		<div class="my-1 p-1 border border-black rounded">
			<h2 class="text-lg font-bold">Push Data to Server</h2>
			<div class="flex flex-row gap-1">
				<button class={bS.gray} onclick={() => pushData('prefixes')}>Push Prefixes</button>
				<button class={bS.gray} onclick={() => pushData('tickets')}>Push Tickets</button>
				<button class={bS.gray} onclick={() => pushData('baskets')}>Push Baskets</button>
			</div>
		</div>
	{/if}
	<div class="my-1 p-1 border border-black rounded">
		<span class="font-bold">Status: </span><span>{results}</span>
	</div>
</div>
