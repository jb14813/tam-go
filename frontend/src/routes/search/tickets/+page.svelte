<script>
	import { bS, iS, rBS, tS } from '$lib/client/styles';
	import { saveMarked } from '$lib/client/api';
	import HeaderBar from '$lib/client/components/HeaderBar.svelte';
	import CommandBar from '$lib/client/components/CommandBar.svelte';
	import TicketSearchBar from '$lib/client/components/TicketSearchBar.svelte';

	let { data } = $props();
	let { prefix, prefixes } = $derived(data);

	let pageTitle = 'Ticket Search | TAM';

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

	let colorMap = $derived.by(() => {
		const mapData = {};
		[...prefixes].forEach((p) => (mapData[p.prefix] = p.color));
		return mapData;
	});

	let searchForm = $state({ first_name: '', last_name: '', phone_number: '' });
	let items = $state([]);
	let itemsBuffer = $derived(items.filter((i) => i.changed));
	const functions = {
		async search() {
			const searchParams = new URLSearchParams({ ...searchForm });
			const res = await fetch(`/api/search/tickets?${searchParams.toString()}`);
			if (res.ok) {
				const resData = await res.json();
				items = [...resData];
				setTimeout(() => focusIdx(0), 1);
			}
		},
		// Resolves to false when the marked rows could not be saved.
		async save() {
			const problem = await saveMarked('/api/search/tickets', itemsBuffer);
			if (problem) {
				alert(problem);
				return false;
			}
			setTimeout(() => {
				focusIdx(0);
			}, 1);
			return true;
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
				['prefix', 't_id'].forEach((key) => delete buffer[key]);
				items[nextIdx] = { ...items[nextIdx], ...buffer, changed: true };
				this.nextLine();
			} else {
				focusIdx(curIdx);
			}
		},
		dupUp() {
			if (curIdx > 0) {
				const buffer = { ...items[curIdx] };
				['prefix', 't_id'].forEach((key) => delete buffer[key]);
				items[prevIdx] = { ...items[prevIdx], ...buffer, changed: true };
				this.prevLine();
			} else {
				focusIdx(curIdx);
			}
		},
		copy() {
			if (items[curIdx]) {
				const buffer = { ...items[curIdx] };
				['prefix', 't_id'].forEach((key) => delete buffer[key]);
				window.localStorage.setItem('tam-ticket', JSON.stringify(buffer));
			}
			setTimeout(() => focusIdx(curIdx), 1);
		},
		paste() {
			if (items[curIdx]) {
				const buffer = JSON.parse(window.localStorage.getItem('tam-ticket'));
				items[curIdx] = { ...items[curIdx], ...buffer, changed: true };
			}
			setTimeout(() => focusIdx(curIdx), 1);
		}
	};
	const headers = [
		'Prefix',
		'Ticket ID',
		'First Name',
		'Last Name',
		'Phone Number',
		'Pref',
		'Save?'
	];

	$effect(() => {
		const warnOnUnload = (e) => {
			if (itemsBuffer.length > 0) e.preventDefault();
		};
		window.addEventListener('beforeunload', warnOnUnload);
		return () => {
			window.removeEventListener('beforeunload', warnOnUnload);
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
				<HeaderBar></HeaderBar>
				<h1 class="text-xl font-bold p-1">{pageTitle}</h1>
				<TicketSearchBar {prefix} {functions} bind:searchForm />
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
		{#each items as item, idx (`${item.prefix}/${item.t_id}`)}
			<tr
				class="{tS[colorMap[item.prefix]]} focus-within:font-bold {rBS[prefix.color]}"
				onfocusin={(e) => {
					changeIdx(idx);
					e.target.scrollIntoView({ block: 'center' });
				}}
			>
				<td class="p-0.5 border">{item.prefix}</td>
				<td class="p-0.5 border">{item.t_id}</td>
				<td class="p-0.5 border"
					><input
						type="text"
						class="{iS.normal} w-full"
						id="{idx}_first"
						oninput={() => (item.changed = true)}
						bind:value={item.first_name}
					/></td
				>
				<td class="p-0.5 border"
					><input
						type="text"
						class="{iS.normal} w-full"
						id="{idx}_second"
						oninput={() => (item.changed = true)}
						bind:value={item.last_name}
					/></td
				>
				<td class="p-0.5 border"
					><input
						type="text"
						class="{iS.normal} w-full"
						id="{idx}_third"
						oninput={() => (item.changed = true)}
						bind:value={item.phone_number}
					/></td
				>
				<td class="p-0.5 border"
					><button
						class={bS[prefix.color]}
						onclick={() => {
							item.pref == 'CALL' ? (item.pref = 'TEXT') : (item.pref = 'CALL');
							item.changed = true;
						}}
						onkeydown={(e) => {
							if (e.key == 't') {
								if (item.pref != 'TEXT') item.changed = true;
								item.pref = 'TEXT';
							} else if (e.key == 'c') {
								if (item.pref != 'CALL') item.changed = true;
								item.pref = 'CALL';
							}
						}}>{item.pref}</button
					></td
				>
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
