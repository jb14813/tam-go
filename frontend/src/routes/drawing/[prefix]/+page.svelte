<script>
	import { resolve } from '$app/paths';
	import { bS, bAS, iS, rBS } from '$lib/client/styles';
	import { getJSON, postJSON, errorMessage } from '$lib/client/api';
	import HeaderBar from '$lib/client/components/HeaderBar.svelte';
	import PagerBar from '$lib/client/components/PagerBar.svelte';
	import CommandBar from '$lib/client/components/CommandBar.svelte';

	let { data } = $props();
	let { prefix, prefixes } = $derived(data);

	let pageTitle = $derived(`${prefix.prefix} Drawing Form | TAM`);

	let curIdx = $state(0),
		nextIdx = $derived(curIdx + 1),
		prevIdx = $derived(curIdx - 1);
	const changeIdx = (idx) => {
		curIdx = idx;
	};
	const focusIdx = (idx) => {
		curIdx = idx;
		const elemIdx = document.getElementById(`${idx}_first`);
		if (elemIdx) {
			elemIdx.select();
		}
	};

	let pager = $state({ idFrom: 0, idTo: 0 });
	let items = $state([]);
	let itemsLength = $derived(items.length || 1);
	let itemsBuffer = $derived(items.filter((i) => i.changed));
	const functions = {
		async getPage() {
			this.save();
			if (pager.idFrom > pager.idTo) {
				[pager.idFrom, pager.idTo] = [pager.idTo, pager.idFrom];
			}
			if (pager.idTo - pager.idFrom > 300) {
				pager.idTo = pager.idFrom + 300;
			}
			let resData;
			try {
				resData = await getJSON(
					`/api/drawing/${encodeURIComponent(prefix.prefix)}/${pager.idFrom}/${pager.idTo}`
				);
			} catch (e) {
				alert(`Error loading rows: ${errorMessage(e)}`);
				return;
			}
			resData.map((i) => (i.changed = false));
			items = [...resData];
			setTimeout(() => focusIdx(0));
		},
		async save() {
			if (itemsBuffer.length > 0) {
				const res = await postJSON('/api/drawing', itemsBuffer);
				if (res.ok) {
					itemsBuffer.forEach((i) => (i.changed = false));
				} else {
					alert('Error saving items.');
				}
			}
			setTimeout(() => {
				focusIdx(0);
			}, 1);
		},
		cancel() {
			if (itemsBuffer.length > 0) {
				itemsBuffer.forEach((i) => (i.changed = false));
				this.getPage();
			}
		},
		pagerFromUpdate() {
			pager.idTo = pager.idFrom + (itemsLength - 1);
		},
		prevPage() {
			((pager.idFrom -= itemsLength), (pager.idTo -= itemsLength));
			this.getPage();
		},
		nextPage() {
			((pager.idFrom += itemsLength), (pager.idTo += itemsLength));
			this.getPage();
		},
		nextLine() {
			if (items[nextIdx]) {
				setTimeout(() => {
					focusIdx(nextIdx);
				}, 1);
			} else {
				setTimeout(() => {
					focusIdx(curIdx);
				}, 1);
			}
		},
		prevLine() {
			if (curIdx > 0) {
				setTimeout(() => {
					focusIdx(prevIdx);
				}, 1);
			} else {
				setTimeout(() => {
					focusIdx(curIdx);
				}, 1);
			}
		},
		dupDown() {
			if (items[nextIdx]) {
				const buffer = { ...items[curIdx] };
				['prefix', 'b_id'].forEach((key) => delete buffer[key]);
				items[nextIdx] = { ...items[nextIdx], ...buffer, changed: true };
				this.nextLine();
			} else {
				focusIdx(curIdx);
			}
		},
		dupUp() {
			if (curIdx > 0) {
				const buffer = { ...items[curIdx] };
				['prefix', 'b_id'].forEach((key) => delete buffer[key]);
				items[prevIdx] = { ...items[prevIdx], ...buffer, changed: true };
				this.prevLine();
			} else {
				focusIdx(curIdx);
			}
		},
		copy() {
			if (items[curIdx]) {
				const buffer = { ...items[curIdx] };
				['prefix', 'b_id'].forEach((key) => delete buffer[key]);
				window.localStorage.setItem('tam-drawing', JSON.stringify(buffer));
			}
			focusIdx(curIdx);
		},
		paste() {
			if (items[curIdx]) {
				const buffer = JSON.parse(window.localStorage.getItem('tam-drawing'));
				items[curIdx] = { ...items[curIdx], ...buffer, changed: true };
			}
			focusIdx(curIdx);
		}
	};
	const headers = ['Basket ID', 'Description', 'Winning Ticket', 'Winner', 'Save?'];

	$effect(() => {
		const saveOnUnload = () => {
			if (itemsBuffer.length > 0) functions.save();
		};
		window.addEventListener('beforeunload', saveOnUnload);
		return () => {
			window.removeEventListener('beforeunload', saveOnUnload);
		};
	});
</script>

<svelte:head>
	<title>{pageTitle}</title>
</svelte:head>

<table class="w-full box-border border-separate p-1">
	<thead class="sticky top-1 bg-white">
		<tr>
			<td colspan="50">
				<HeaderBar>
					<div>Drawing Forms:</div>
					{#each prefixes as p (p.prefix)}
						<a
							href={resolve('/drawing/[prefix]', { prefix: p.prefix })}
							class={prefix.prefix == p.prefix ? bAS[p.color] : bS[p.color]}>{p.prefix}</a
						>
					{/each}
				</HeaderBar>
				<h1 class="text-xl font-bold p-1">{pageTitle}</h1>
				<PagerBar {prefix} {functions} bind:pager />
				<CommandBar {prefix} {functions} /></td
			>
		</tr>
		<tr>
			{#each headers as header (header)}
				<th class="border text-left p-0.5">{header}</th>
			{/each}
		</tr>
	</thead>
	<tbody>
		{#each items as item, idx (item.b_id)}
			<tr
				class="focus-within:font-bold {rBS[prefix.color]}"
				onfocusin={(e) => {
					changeIdx(idx);
					e.target.scrollIntoView({ block: 'center' });
				}}
			>
				<td class="p-0.5 border">{item.b_id}</td>
				<td class="p-0.5 border">{item.description}</td>
				<td class="p-0.5 border"
					><input
						type="number"
						class="{iS.normal} w-full"
						id="{idx}_first"
						oninput={async () => {
							item.changed = true;
							const res = await fetch(
								`/api/tickets/${encodeURIComponent(prefix.prefix)}/${item.winning_ticket}`
							);
							if (res.ok) {
								const ticket = await res.json();
								[item.last_name, item.first_name, item.phone_number] = [
									ticket.last_name || '',
									ticket.first_name || '',
									ticket.phone_number || ''
								];
							} else {
								// A cleared or invalid ticket number has no winner.
								[item.last_name, item.first_name, item.phone_number] = ['', '', ''];
							}
						}}
						bind:value={item.winning_ticket}
					/></td
				>
				<td class="p-0.5 border">
					{item.last_name || ''}, {item.first_name || ''}: {item.phone_number || ''}
				</td>
				<td class="p-0.5 border"
					><button
						class={bS[prefix.color]}
						tabindex="-1"
						onclick={() => {
							item.changed ? (item.changed = false) : (item.changed = true);
						}}>{item.changed ? 'Yes' : 'No'}</button
					></td
				>
			</tr>
		{:else}
			<tr>
				<td class="p-0.5 border text-center" colspan="50">
					No rows loaded. Please use the pager at the top to put in the first, then last number on
					the sheet, click Go, and that should load in the sheet.
				</td>
			</tr>
		{/each}
	</tbody>
</table>
