<script>
	import { onMount } from 'svelte';
	import { prefixPage } from '$lib/client/paths';
	import { bS, bAS, iS, rBS } from '$lib/client/styles';
	import { getJSON, lookupTicket, saveMarked, saveOnLeave, unchangedRows, pageRange, errorMessage, API_UNREACHABLE } from '$lib/client/api';
	import UnsentEdits from '$lib/client/components/UnsentEdits.svelte';
	import { preserveDraft } from '$lib/client/drafts';
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
	async function showWinner(item, refresh = false) {
		const wanted = item.winning_ticket;
		const wantedPrefix = prefix.prefix;
		const request = ++lookupSequence;
		lookups.set(item, request);
		if (!refresh) item.lookupMessage = '';
		item.lookupRefresh = true;
		item.lookupPending = false;
		if (!Number.isInteger(wanted) || wanted <= 0) return;
		if (!refresh || !item.lookupMessage) item.lookupMessage = 'Lookup pending…';
		item.lookupPending = true;
		const current = () => active && lookups.get(item) === request &&
			item.winning_ticket === wanted && prefix.prefix === wantedPrefix && items.includes(item);
		try {
			const { ticket, found, source, mode } = await lookupTicket(wantedPrefix, wanted);
			if (!current()) return;
			item.lookupRefresh = mode !== 'standalone';
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
		} catch (e) {
			if (!current()) return;
			const detail = errorMessage(e);
			item.lookupMessage = detail === API_UNREACHABLE
				? 'Ticket lookup unavailable; could not reach the TAM client program. Retrying.'
				: `Ticket lookup unavailable: ${detail.replace(/[\s.]+$/, '')}. Retrying.`;
		} finally {
			if (current()) item.lookupPending = false;
		}
	}

	let pager = $state({ idFrom: 0, idTo: 0 });
	let items = $state([]);
	let itemsLength = $derived(items.length || 1);
	let itemsBuffer = $derived(items.filter((i) => i.changed));
	let loadSequence = 0;
	let loadedRange;
	function restorePager() { if (loadedRange) [pager.idFrom, pager.idTo] = loadedRange; }
	function applyDraft(row) {
		if (itemsBuffer.length) { alert('Save or cancel the current edits before using a draft.'); return false; }
		items = [{ ...row, winning_ticket: row.winning_ticket ?? undefined, changed: true }];
		loadedRange = [row.b_id, row.b_id];
		restorePager();
		showWinner(items[0]);
		return true;
	}
	const functions = {
		// Saves the marked rows, then loads the pager's range, or `range` when given.
		async getPage(range) {
			const request = ++loadSequence;
			const wanted = pageRange(range || [pager.idFrom, pager.idTo]);
			if (!wanted) { alert('Enter whole numbers for the first and last row.'); restorePager(); return; }
			// Rows that could not be saved stay on the page, with the message why.
			if (!(await this.save()) || itemsBuffer.length || request !== loadSequence) {
				if (request === loadSequence) restorePager();
				return;
			}
			const unchanged = unchangedRows(items, (line) => line.winning_ticket);
			let resData;
			try {
				resData = await getJSON(
					`/api/drawing/${encodeURIComponent(prefix.prefix)}/${wanted[0]}/${wanted[1]}`
				);
			} catch (e) {
				if (request === loadSequence) restorePager();
				alert(`Error loading rows: ${errorMessage(e)}`);
				return;
			}
			if (request !== loadSequence) return;
			if (!unchanged(items)) { restorePager(); return; }
			loadedRange = wanted;
			restorePager();
			resData.map((i) => (i.changed = false));
			items = [...resData];
			for (const item of items) showWinner(item);
			setTimeout(() => focusIdx(0));
		},
		// Resolves to false when the marked rows could not be saved.
		async save(opts = {}) {
			const invalid = itemsBuffer.find((line) => !Number.isSafeInteger(line.winning_ticket) || line.winning_ticket < 0);
			if (invalid) {
				if (!opts.keepalive) alert(`Basket ${invalid.b_id}: enter a whole winning ticket number, or 0 to clear it. Nothing was saved; all edits are still on this page.`);
				return false;
			}
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
			if (!opts.keepalive) setTimeout(() => { if (!itemsBuffer.length) focusIdx(0); }, 1);
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
			const [start, end] = loadedRange || [pager.idFrom, pager.idTo];
			const from = Math.max(0, start - itemsLength);
			this.getPage([from, from + (end - start)]);
		},
		nextPage() {
			const [start, end] = loadedRange || [pager.idFrom, pager.idTo];
			this.getPage([start + itemsLength, end + itemsLength]);
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
	$effect(() => { preserveDraft(itemsBuffer); });
	onMount(() => {
		active = true;
		const retry = setInterval(() => {
			if (document.visibilityState === 'hidden') return;
			for (const item of items) {
				// Another volunteer may correct even a buyer already found here.
				if (item.lookupRefresh && Number.isInteger(item.winning_ticket) && item.winning_ticket > 0 && !item.lookupPending) {
					showWinner(item, true);
				}
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

<UnsentEdits apply={applyDraft} />
<p class="px-2">Enter a whole winning ticket number. Enter 0 to leave a basket undrawn or clear its winner; a blank field is not saved.</p>

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
