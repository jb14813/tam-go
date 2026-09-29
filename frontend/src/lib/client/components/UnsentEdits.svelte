<script>
	import { onMount } from 'svelte';
	import { previousDrafts, draftProblem, preserveDraft, removeDraftRow } from '../drafts';
	import { errorMessage, readDetail } from '../api';
	let { apply } = $props();
	let drafts = $state([]);
	let problem = $state('');
	let review = $state(false);
	let current = $state({});
	let reviewSequence = 0;
	let draftSnapshot = '';
	let mounted = false;
	const fields = { prefix: 'Prefix', t_id: 'Ticket', b_id: 'Basket', first_name: 'First name', last_name: 'Last name', phone_number: 'Phone', pref: 'Contact preference', description: 'Description', donors: 'Donors', winning_ticket: 'Winning ticket' };
	const display = (value) => value == null || value === '' ? '(blank)' : String(value);
	const kindOf = (row) => 't_id' in row ? 'tickets' : 'winning_ticket' in row ? 'drawing' : 'baskets';
	const rowKey = (draft, row) => JSON.stringify([draft.key, kindOf(row), row.prefix, row.t_id ?? row.b_id]);
	function update() {
		const next = previousDrafts();
		const snapshot = JSON.stringify(next.map(({ key, rows }) => ({ key, rows })));
		if (snapshot !== draftSnapshot) {
			// Keep completed comparisons only for exactly the same record and
			// draft value. Removing a row must not shift another row's answer.
			++reviewSequence;
			const remaining = new Map(next.flatMap((draft) => draft.rows.map((row) => [rowKey(draft, row), JSON.stringify(row)])));
			current = Object.fromEntries(Object.entries(current).filter(([id, result]) => !result.pending && remaining.get(id) === result.draft));
			draftSnapshot = snapshot;
		}
		drafts = next;
		problem = draftProblem();
	}
	onMount(() => {
		mounted = true;
		update();
		window.addEventListener('tam-drafts', update);
		window.addEventListener('storage', update);
		return () => {
			mounted = false;
			++reviewSequence;
			window.removeEventListener('tam-drafts', update);
			window.removeEventListener('storage', update);
		};
	});
	async function check() {
		const request = ++reviewSequence;
		review = true;
		current = {};
		for (const draft of drafts) {
			for (const row of draft.rows) {
				const id = rowKey(draft, row);
				const value = JSON.stringify(row);
				const valid = () => mounted && review && request === reviewSequence;
				if (!valid()) return;
				current[id] = { pending: true, draft: value };
				try {
					const res = await fetch(`/api/${kindOf(row)}/${encodeURIComponent(row.prefix)}/${row.t_id ?? row.b_id}`);
					if (!res.ok) throw new Error(await readDetail(res));
					const saved = await res.json();
					if (!valid()) return;
					current[id] = { row: saved, draft: value, source: res.headers.get('X-TAM-Source') === 'server' ? 'Shared server' : 'This client (shared server values may be unavailable)' };
				} catch (e) {
					if (!valid()) return;
					current[id] = { error: errorMessage(e), draft: value };
				}
			}
		}
	}
	function useRow(draft, index) {
		const row = { ...draft.rows[index] };
		if (!apply(row)) return;
		++reviewSequence;
		// Transfer only this explicitly selected row after its new browser copy
		// exists. The old copy stays available if storage is unavailable.
		if (preserveDraft([{ ...row, changed: true }])) removeDraftRow(draft.key, index, row);
		current = {};
		review = false;
	}
</script>

{#if problem}<p role="alert" class="m-2 border border-red-700 bg-red-50 p-2">{problem}</p>{/if}
{#if drafts.length}
	<section aria-label="Unsent edits" class="m-2 border border-amber-700 bg-amber-50 p-3">
		<p class="font-bold">Unsent edits are available from an earlier visit.</p>
		<p>These browser copies are not confirmed saves. Compare them with current values before using or discarding them.</p>
		<button class="border bg-white px-2 py-1 my-2" onclick={check}>Review unsent edits</button>
		{#if review}
			{#each drafts as draft (draft.key)}
				{#each draft.rows as row, index (rowKey(draft, row))}
					{@const checked = current[rowKey(draft, row)]}
					<div class="border border-amber-700 p-2 my-2">
						<p>Saved in this browser: {new Date(draft.savedAt).toLocaleString()}</p>
						<table class="w-full text-left"><thead><tr><th>Field</th><th>Unsent edit</th><th>Current value</th></tr></thead>
							<tbody>{#each Object.entries(row) as [field, value]}<tr><th>{fields[field] || field}</th><td>{display(value)}</td><td>{checked?.row ? display(checked.row[field]) : '—'}</td></tr>{/each}</tbody>
						</table>
						<p>{checked?.error || checked?.source || (checked?.pending ? 'Checking current values…' : 'Review current values again.')}</p>
						<p>Use draft values loads this row for editing. Save or leave the page to send it.</p>
						<button class="border bg-white px-2 py-1 mr-2" disabled={!checked?.row} onclick={() => useRow(draft, index)}>Use draft values</button>
						<button class="border bg-white px-2 py-1" onclick={() => { if (confirm('Discard this unsent browser copy?')) removeDraftRow(draft.key, index, row); }}>Discard this draft</button>
					</div>
				{/each}
			{/each}
		{/if}
	</section>
{/if}
