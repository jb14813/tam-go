<script>
	import { onDestroy, tick } from 'svelte';
	import { getJSON, errorMessage } from '$lib/client/api';
	import { prefixPage } from '$lib/client/paths';
	import HeaderBar from '$lib/client/components/HeaderBar.svelte';
	import { bAS, bS } from '$lib/client/styles';

	let currentFilter = $state('');
	let { data } = $props();
	let prefix = $derived(data.prefix);

	let pageTitle = $derived(`${prefix.prefix} Winners by Basket | TAM`);
	let filterTitle = $state('All Winners');
	let refreshed = $state(null);
	let printing = $state(false);
	let alive = true;
	onDestroy(() => { alive = false; });
	let currentReport = $derived(refreshed?.prefix === prefix.prefix ? refreshed : null);
	let allLines = $derived(currentReport ? currentReport.rows : data.reportLines);
	let reportError = $derived(currentReport?.error || '');
	let snapshotAt = $derived(currentReport ? currentReport.at : data.generatedAt);

	async function printReport() {
		if (printing) return;
		printing = true;
		const requestedPrefix = prefix.prefix;
		try {
			const rows = await getJSON(`/api/reports/bybasket/${encodeURIComponent(requestedPrefix)}`);
			if (!Array.isArray(rows)) throw new Error('Invalid report received. Refresh to try again.');
			if (!alive || requestedPrefix !== prefix.prefix) return;
			refreshed = { prefix: requestedPrefix, rows, at: new Date().toISOString(), error: '' };
			await tick();
			if (alive && requestedPrefix === prefix.prefix) window.print();
		} catch (e) {
			if (alive && requestedPrefix === prefix.prefix) refreshed = { prefix: requestedPrefix, rows: [], at: null, error: errorMessage(e) };
		} finally { printing = false; }
	}

	const headers = ['Basket ID', 'Description', 'Winning Ticket', 'Winner Name', 'Phone Number'];

	let reportLines = $derived.by(() => {
		if (currentFilter == 'CALL') {
			return allLines.filter((l) => l.pref == 'CALL');
		} else if (currentFilter == 'TEXT') {
			return allLines.filter((l) => l.pref == 'TEXT');
		} else {
			return allLines;
		}
	});
</script>

<svelte:head>
	<title>{pageTitle}</title>
</svelte:head>

<table class="w-full box-border border-separate p-1">
	<thead>
		<tr class="print:hidden">
			<td colspan="50">
				<HeaderBar>
					<div>By Basket Reports:</div>
					{#each data.prefixes as p (p.prefix)}
						<a
							href={prefixPage('/reports/bybasket/[prefix]', p.prefix)}
							class={p.prefix == prefix.prefix ? bAS[p.color] : bS[p.color]}>{p.prefix}</a
						>
					{/each}
				</HeaderBar>
				<div class="flex flex-row gap-1 py-1 justify-between">
					<div class="flex flex-row gap-1">
						<button
							class={bS[prefix.color]}
							onclick={() => {
								currentFilter = '';
								filterTitle = 'All Winners';
							}}>All Preferences</button
						>
						<button
							class={bS[prefix.color]}
							onclick={() => {
								currentFilter = 'CALL';
								filterTitle = 'Winners Preferring a CALL';
							}}>Call Preference</button
						>
						<button
							class={bS[prefix.color]}
							onclick={() => {
								currentFilter = 'TEXT';
								filterTitle = 'Winners Preferring a TEXT';
							}}>Text Preference</button
						>
					</div>
					<div class="flex flex-row gap-1">
						<button class={bS[prefix.color]} disabled={printing} onclick={printReport}>{printing ? 'Refreshing…' : 'Print'}</button>
					</div>
				</div>
			</td>
		</tr>
		<tr>
			<th colspan="50"><h1 class="text-lg text-left">{pageTitle}</h1></th>
		</tr>
		<tr>
			<th colspan="50"><h2 class="italic text-left">{filterTitle}</h2></th>
		</tr>
		<tr><td colspan="50" class="text-xs text-left">
			{#if reportError}<p role="alert" class="border border-red-700 p-2">{reportError}</p>
			{:else if snapshotAt}Report snapshot: {new Date(snapshotAt).toLocaleString()}{/if}
		</td></tr>
		<tr class="text-sm">
			{#each headers as header (header)}
				<th class="text-left border p-0.5">{header}</th>
			{/each}
		</tr>
	</thead>
	<tbody class="text-sm">
		{#each reportLines as line, idx (idx)}
			<tr class="break-inside-avoid">
				<td class="p-0.5 border">{line.b_id}</td>
				<td class="p-0.5 border">{line.description || ''}</td>
				<td class="p-0.5 border">{line.winning_ticket}</td>
				<td class="p-0.5 border">{line.last_name || ''}, {line.first_name || ''}</td>
				<td class="p-0.5 border">{line.phone_number || ''}</td>
			</tr>
		{/each}
	</tbody>
	<tfoot>
		<tr>
			<td colspan="50" class="text-center text-xs">{data.venueName}</td>
		</tr>
		<tr>
			<td colspan="50" class="text-center text-xs">&copy; 2026 Ticket Auction Manager</td>
		</tr>
	</tfoot>
</table>
