<script>
	import { untrack } from 'svelte';
	import HeaderBar from '$lib/client/components/HeaderBar.svelte';
	import { tS, bS } from '$lib/client/styles';

	let { data } = $props();
	let { prefixes } = $derived(data);
	let tableData = $state([]);
	let currentTimeout = $state();
	let lastRefreshed = $state('');
	let interval = $state('0');

	const loadCounts = async () => {
		const rtnData = {};
		let res;
		try {
			res = await fetch('/api/reports/counts');
		} catch {
			return;
		}
		if (res.ok) {
			prefixes.forEach((p) => (rtnData[p.prefix] = { ...p }));
			const resData = await res.json();
			resData.forEach((c) => (rtnData[c.prefix] = { ...rtnData[c.prefix], ...c }));
			tableData = [...Object.values(rtnData)];
			const now = new Date();
			lastRefreshed = now.toLocaleString();
			clearTimeout(currentTimeout);
			if (interval > 0) {
				currentTimeout = setTimeout(loadCounts, interval);
			}
		}
	};

	const pageTitle = 'Ticket Counts | TAM';

	$effect(() => {
		untrack(() => loadCounts());
		return () => {
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
	<table class="border-separate box-border w-full">
		<thead>
			<tr>
				<th class="border p-0.5">Prefix</th>
				<th class="border p-0.5">Unique Buyers</th>
				<th class="border p-0.5">Total Buys</th>
			</tr>
		</thead>
		<tbody>
			{#each tableData as line (line.prefix)}
				<tr class={tS[line.color] || ''}>
					<td class="border p-0.5">{line.prefix}</td>
					<td class="border p-0.5">{line.unique_buyers || 0}</td>
					<td class="border p-0.5">{line.total_buys || 0}</td>
				</tr>
			{/each}
		</tbody>
	</table>
	<div class="flex flex-row gap-1 py-1 items-center">
		<select id="interval_select" class="border p-1" bind:value={interval}>
			<option value="0">No Interval</option>
			<option value="30000">30 sec</option>
			<option value="60000">1 Min</option>
			<option value="120000">2 Min</option>
		</select>
		<button class={bS.gray} onclick={() => loadCounts()}
			>Refresh{interval > 0 ? ` Every ${interval / 60000} Min` : ''}</button
		>
		<div>Last refreshed: {lastRefreshed}</div>
	</div>
</div>
