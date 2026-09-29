<script>
	import { untrack } from 'svelte';
	import HeaderBar from '$lib/client/components/HeaderBar.svelte';
	import { tS, bS } from '$lib/client/styles';
	import { API_UNREACHABLE, errorMessage, readDetail } from '$lib/client/api';

	let { data } = $props();
	let { prefixes } = $derived(data);
	let tableData = $state([]);
	let currentTimeout = $state();
	let lastRefreshed = $state('');
	let interval = $state('0');
	let loadError = $state('');
	let loaded = $state(false);

	// alive is cleared when the page goes away so a refresh that was in
	// flight cannot schedule the next one.
	let alive = true;
	let requestGeneration = 0;
	const loadCounts = async () => {
		const generation = ++requestGeneration;
		clearTimeout(currentTimeout);
		try {
			let res;
			try {
				res = await fetch('/api/reports/counts');
			} catch {
				throw new Error(`${API_UNREACHABLE}. Refresh to try again.`);
			}
			if (!res.ok) throw new Error(await readDetail(res));
			const resData = await res.json();
			if (!Array.isArray(resData)) throw new Error('Invalid ticket counts received. Refresh to try again.');
			if (!alive || generation !== requestGeneration) return;
			const rtnData = Object.create(null);
			prefixes.forEach((p) => (rtnData[p.prefix] = { ...p }));
			resData.filter((c) => !c.is_total).forEach((c) => (rtnData[c.prefix] = { ...rtnData[c.prefix], ...c }));
			tableData = [...Object.values(rtnData), ...resData.filter((c) => c.is_total)];
			lastRefreshed = new Date().toLocaleString();
			loadError = '';
			loaded = true;
		} catch (error) {
			if (!alive || generation !== requestGeneration) return;
			tableData = [];
			loaded = false;
			loadError = errorMessage(error);
		} finally {
			if (alive && generation === requestGeneration && interval > 0) {
				currentTimeout = setTimeout(loadCounts, interval);
			}
		}
	};

	const pageTitle = 'Ticket Counts | TAM';

	$effect(() => {
		alive = true;
		untrack(() => loadCounts());
		return () => {
			alive = false;
			requestGeneration++;
			clearTimeout(currentTimeout);
		};
	});
</script>

<svelte:head>
	<title>{pageTitle}</title>
</svelte:head>

<div id="app-container" class="p-1">
	<HeaderBar></HeaderBar>
	<h1 class="text-xl font-bold">{pageTitle}</h1>
	{#if loadError}
		<p role="alert" class="border border-red-700 bg-red-50 text-red-900 p-2 my-2">{loadError}</p>
	{:else if !loaded}
		<p>Loading ticket counts…</p>
	{/if}
	{#if loaded}
	<p class="text-xs">Report snapshot: {lastRefreshed}</p>
	<table class="border-separate box-border w-full">
		<thead>
			<tr>
				<th class="border p-0.5">Prefix</th>
				<th class="border p-0.5">Unique Buyers</th>
				<th class="border p-0.5">Total Buys</th>
			</tr>
		</thead>
		<tbody>
			{#each tableData as line (JSON.stringify([!!line.is_total, line.prefix]))}
				<tr class={tS[line.color] || ''}>
					<td class="border p-0.5">{line.prefix}</td>
					<td class="border p-0.5">{line.unique_buyers || 0}</td>
					<td class="border p-0.5">{line.total_buys || 0}</td>
				</tr>
			{/each}
		</tbody>
	</table>
	{/if}
	<div class="flex flex-row gap-1 py-1 items-center print:hidden">
		<select id="interval_select" class="border p-1" bind:value={interval}>
			<option value="0">No Interval</option>
			<option value="30000">30 sec</option>
			<option value="60000">1 Min</option>
			<option value="120000">2 Min</option>
		</select>
		<button class={bS.gray} onclick={() => loadCounts()}
			>Refresh{interval > 0 ? ` Every ${interval / 60000} Min` : ''}</button
		>
		<div>Last successful refresh: {lastRefreshed || 'None'}</div>
	</div>
</div>
