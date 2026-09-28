<script>
	import { onMount } from 'svelte';
	import { prefixPage } from '$lib/client/paths';
	import { bS, bAS, iS, rBS } from '$lib/client/styles';
	import { getJSON, lookupTicket, saveMarked, saveOnLeave, errorMessage } from '$lib/client/api';
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

	let active = false;
	let lookupSequence = 0;
	const lookups = new WeakMap();
	const contact = (ticket) => {
		const name = [ticket.last_name, ticket.first_name].filter(Boolean).join(', ');
		return [name, ticket.phone_number].filter(Boolean).join(': ');
	};
	// The request identity also rejects an older retry for the same number.
	// Zero means undrawn; it must not look up ticket zero.
	async function showWinner(item) {
		const wanted = item.winning_ticket;
		const wantedPrefix = prefix.prefix;
		const request = ++lookupSequence;
		lookups.set(item, request);
		item.lookupMessage = '';
		item.lookupRetry = false;
		item.lookupPending = false;
		if (!Number.isInteger(wanted) || wanted <= 0) return;
		item.lookupMessage = 'Lookup pending…';
		item.lookupPending = true;
		const current = () => active && lookups.get(item) === request &&
			item.winning_ticket === wanted && prefix.prefix === wantedPrefix && items.includes(item);
		try {
			const { ticket, found, source, mode } = await lookupTicket(wantedPrefix, wanted);
			if (!current()) return;
			item.lookupRetry = mode !== 'standalone' && (source !== 'server' || !found);
			if (mode === 'standalone') {
				item.lookupMessage = found
					? contact(ticket) || 'Ticket found; no contact info entered'
					: 'Ticket not found on this client';
			} else if (source === 'server') {
				item.lookupMessage = found
					? contact(ticket) || 'Ticket found; no contact info entered'
					: 'Ticket not found on server; another client may still have unsent entries';
			} else {
				item.lookupMessage = found
					? `${contact(ticket) || 'Ticket found; no contact info entered'}. Local entry only; server lookup unavailable. Retrying.`
					: 'Server lookup unavailable; this client has no local entry for this ticket. Retrying.';
			}
		} catch {
			if (!current()) return;
			item.lookupMessage = 'Ticket lookup unavailable; could not reach the TAM client program. Retrying.';
			item.lookupRetry = true;
		} finally {
			if (current()) item.lookupPending = false;
		}
	}

	let pager = $state({ idFrom: 0, idTo: 0 });
	let items = $state([]);
	let itemsLength = $derived(items.length || 1);
	let itemsBuffer = $derived(items.filter((i) => i.changed));
	const functions = {
		// Saves the marked rows, then loads the pager's range, or `range` when given.
		async getPage(range) {
			// Rows that could not be saved stay on the page, with the message why.
			if (!(await this.save())) return;
			if (range) [pager.idFrom, pager.idTo] = range;
			if (pager.idFrom > pager.idTo) {
				[pager.idFrom, pager.idTo] = [pager.idTo, pager.idFrom];
			}
			if (pager.idTo - pager.idFrom > 300) {
				pager.idTo = pager.idFrom + 300;
			}
			// Numbers start at 0: a row below it could not be saved.
			if (pager.idFrom < 0) pager.idFrom = 0;
			if (pager.idTo < 0) pager.idTo = 0;
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
			for (const item of items) showWinner(item);
			setTimeout(() => focusIdx(0));
		},
		// Resolves to false when the marked rows could not be saved.
		async save(opts = {}) {
			const problem = await saveMarked('/api/drawing', itemsBuffer, {
				keepalive: !!opts.keepalive,
				// A drawing line stores its winning ticket; the winner's name
				// beside it only shows the lookup, which may answer meanwhile.
				saved: (line) => line.winning_ticket,
				payload: ({ prefix, b_id, winning_ticket }) => ({ prefix, b_id, winning_ticket })
			});
			// A save made as the page is hidden or closed shows nothing and leaves
			// the cursor where it is: the volunteer may come back to the row.
			if (problem) {
				if (!opts.keepalive) alert(problem);
				return false;
			}
			if (!opts.keepalive) setTimeout(() => focusIdx(0), 1);
			return true;
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
			// Stops at 0, keeping the page's size: 1-10 goes to 0-9.
			const from = Math.max(0, pager.idFrom - itemsLength);
			this.getPage([from, from + (pager.idTo - pager.idFrom)]);
		},
		nextPage() {
			this.getPage([pager.idFrom + itemsLength, pager.idTo + itemsLength]);
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
				items[nextIdx].winning_ticket = items[curIdx].winning_ticket;
				items[nextIdx].changed = true;
				showWinner(items[nextIdx]);
				this.nextLine();
			} else {
				focusIdx(curIdx);
			}
		},
		dupUp() {
			if (curIdx > 0) {
				items[prevIdx].winning_ticket = items[curIdx].winning_ticket;
				items[prevIdx].changed = true;
				showWinner(items[prevIdx]);
				this.prevLine();
			} else {
				focusIdx(curIdx);
			}
		},
		copy() {
			if (items[curIdx]) {
				const buffer = { winning_ticket: items[curIdx].winning_ticket };
				window.localStorage.setItem('tam-drawing', JSON.stringify(buffer));
			}
			focusIdx(curIdx);
		},
		paste() {
			if (items[curIdx]) {
				const buffer = JSON.parse(window.localStorage.getItem('tam-drawing'));
				if (buffer) {
					items[curIdx].winning_ticket = buffer.winning_ticket;
					items[curIdx].changed = true;
					showWinner(items[curIdx]);
				}
			}
			focusIdx(curIdx);
		}
	};
	const headers = ['Basket ID', 'Description', 'Winning Ticket', 'Winner', 'Save?'];

	// Marked rows are saved when the page is hidden, left or closed.
	$effect(() => saveOnLeave(() => itemsBuffer, (opts) => functions.save(opts)));
	onMount(() => {
		active = true;
		const retry = setInterval(() => {
			if (document.visibilityState === 'hidden') return;
			for (const item of items) {
				if (item.lookupRetry && !item.lookupPending) showWinner(item);
			}
		}, 5000);
		return () => {
			active = false;
			clearInterval(retry);
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
							href={prefixPage('/drawing/[prefix]', p.prefix)}
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
						aria-label="Basket {item.b_id} winning ticket"
						oninput={(event) => {
							// Svelte runs this handler before updating bind:value.
							item.winning_ticket = event.currentTarget.value === '' ? undefined : event.currentTarget.valueAsNumber;
							item.changed = true;
							showWinner(item);
						}}
						bind:value={item.winning_ticket}
					/></td
				>
				<td class="p-0.5 border">
					<div role="status" aria-label="Basket {item.b_id} winner lookup">{item.lookupMessage || ''}</div>
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
