<script>
	import { bS, iS } from '$lib/client/styles';
	import HeaderBar from '$lib/client/components/HeaderBar.svelte';

	const pageTitle = 'Print Ticket Sheets | TAM';

	let inputs = $state({ start: 0, end: 0, perPage: 20, ticketType: '' });

	let items = $state([]);

	function runSheet() {
		items = [];
		for (let i = inputs.start; i <= inputs.end; i++) items = [...items, i];
	}

	function resetSheet() {
		items = [];
	}
</script>

<svelte:head>
	<title>{pageTitle}</title>
</svelte:head>

<table class="p-1 border-separate box-border w-full text-left">
	<thead class="sticky top-0">
		<tr class="print:hidden">
			<td colspan="50">
				<HeaderBar />
			</td>
		</tr>
		<tr class="print:hidden">
			<td colspan="50">
				<h1 class="text-lg font-bold">{pageTitle}</h1>
			</td>
		</tr>
		<tr class="print:hidden">
			<td colspan="50">
				<div class="flex flex-row gap-1 py-1 items-center">
					<div>Range:</div>
					<input type="number" id="range_start" bind:value={inputs.start} class={iS.normal} />
					<div class="px-2">-</div>
					<input type="number" id="range_end" bind:value={inputs.end} class={iS.normal} />
					<div>Per Page:</div>
					<input type="number" id="per_page" bind:value={inputs.perPage} class={iS.normal} />
					<div>Ticket Type:</div>
					<input type="text" id="ticket_type" bind:value={inputs.ticketType} class={iS.normal} />
					<button class={bS.gray} onclick={runSheet}>Create Sheets</button>
					<button class={bS.gray} onclick={resetSheet}>Reset Sheet</button>
				</div>
			</td>
		</tr>
		{#if inputs.ticketType}
			<tr>
				<td colspan="50"><h2 class="font-bold">{inputs.ticketType} Ticket Sales</h2></td>
			</tr>
		{/if}
		<tr>
			<th class="p-0.5 border" style="width: 15%">Ticket #</th>
			<th class="p-0.5 border" style="width: 40%">Name</th>
			<th class="p-0.5 border" style="width: 35%">Phone Number</th>
			<th class="p-0.5 border" style="width: 10%">Text?</th>
		</tr>
	</thead>
	<tbody>
		{#each items as i, idx (i)}
			<tr
				class="h-10 break-inside-avoid{(idx + 1) % inputs.perPage === 0 ? ' break-after-page' : ''}"
			>
				<td class="text-lg font-bold p-0.5 border">{i}</td>
				<td class="border"></td>
				<td class="border"></td>
				<td class="border"></td>
			</tr>
		{:else}
			<tr>
				<td colspan="50" class="p-0.5 border"
					>No rows created. Please use the form above to create rows.</td
				>
			</tr>
		{/each}
	</tbody>
</table>
