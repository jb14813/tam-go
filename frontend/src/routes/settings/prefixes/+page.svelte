<script>
	import { untrack } from 'svelte';
	import HeaderBar from '$lib/client/components/HeaderBar.svelte';
	import { bS, bAS, iS, tS } from '$lib/client/styles';
	import { postJSON, readDetail } from '$lib/client/api';
	import { resolve } from '$app/paths';

	let { data } = $props();

	const pageTitle = 'Prefixes | TAM';

	// Reconcile acknowledged changes without replacing newer form input.
	let prefixes = $state(untrack(() => [...data.prefixes]));
	let editPrefix = $state({ prefix: '', color: 'white', weight: 1 });
	let status = $state('');
	let saving = $state(false);
	let statusColor = $state('red');

	// Put the cursor back at the name only when no newer input would be interrupted.
	const selectPrefixInput = () => {
		const form_prefix = document.getElementById('form_prefix');
		if (form_prefix) {
			form_prefix.focus();
			form_prefix.select();
		}
	};

	async function addChange() {
		if (saving) return;
		statusColor = 'red';
		const submitted = { ...editPrefix };
		const entered = String(editPrefix.prefix ?? '');
		// An existing prefix is an identity, including any restored whitespace.
		const name = prefixes.some((p) => p.prefix === entered) ? entered : entered.trim();
		if (!name) {
			status = 'Prefix name cannot be empty.';
			selectPrefixInput();
			return;
		}
		if (!Number.isSafeInteger(submitted.weight) || submitted.weight < 0) {
			status = 'Weight must be a whole number from 0 to 9007199254740991.';
			return;
		}
		const body = [
			{
				prefix: name,
				color: submitted.color,
				weight: submitted.weight
			}
		];
		let res;
		saving = true;
		try {
			res = await postJSON('/api/prefixes', body);
			if (res.ok) {
				const index = prefixes.findIndex((p) => p.prefix === name);
				if (index < 0) prefixes.push(body[0]);
				else prefixes[index] = body[0];
				const unchanged = Object.keys(submitted).every((key) => editPrefix[key] === submitted[key]);
				if (unchanged) {
					editPrefix = { prefix: '', color: 'white', weight: 1 };
					selectPrefixInput();
				}
				status = unchanged ? 'Prefix saved.' : 'Prefix saved. Newer edits still need saving.';
				statusColor = 'green';
			} else status = await readDetail(res);
		} catch {
			status = 'Could not reach the TAM client API';
		} finally { saving = false; }
	}

	async function deletePrefix(prefix) {
		if (saving) return;
		statusColor = 'red';
		let res;
		saving = true;
		try {
			res = await fetch(`/api/prefixes?p=${encodeURIComponent(prefix.prefix)}`, {
				method: 'DELETE'
			});
			if (res.ok) {
				prefixes = prefixes.filter((p) => p.prefix !== prefix.prefix);
				status = 'Prefix deleted.';
				statusColor = 'green';
			} else status = await readDetail(res);
		} catch {
			status = 'Could not reach the TAM client API';
		} finally { saving = false; }
	}
</script>

<svelte:head>
	<title>{pageTitle}</title>
</svelte:head>

<div id="app_container" class="p-1">
	<HeaderBar>
		<a href={resolve('/settings')} class={bS.gray}>Back to Settings</a>
	</HeaderBar>
	<h1 class="text-xl font-bold">{pageTitle}</h1>
	<div class="flex flex-row gap-1 py-1 items-center">
		<div class="flex flex-col gap-1">
			<div>Prefix</div>
			<!-- The cursor belongs in the first field of this form; see selectPrefixInput. -->
			<!-- svelte-ignore a11y_autofocus -->
			<input type="text" id="form_prefix" class={iS.normal} bind:value={editPrefix.prefix} autofocus />
		</div>
		<div class="flex flex-col gap-1">
			<div>Color</div>
			<select id="form_color" class={iS.normal} bind:value={editPrefix.color}>
				<option value="white">White</option>
				<option value="blue">Blue</option>
				<option value="yellow">Yellow</option>
				<option value="green">Green</option>
				<option value="orange">Orange</option>
				<option value="purple">Purple</option>
				<option value="red">Red</option>
			</select>
		</div>
		<div class="flex flex-col gap-1">
			<div>Weight</div>
			<input
				type="number"
				id="form_weight"
				step="1"
				min="0"
				class={iS.normal}
				bind:value={editPrefix.weight}
			/>
		</div>
		<div class="flex flex-col gap-1">
			<div>Actions</div>
			<button class={bS[editPrefix.color]} disabled={saving} onclick={addChange}>Add/Change</button>
		</div>
	</div>
	{#if status}
		<div class="py-1">
			<p role="status" class={tS[statusColor]}>{status}</p>
		</div>
	{/if}
	<div class="flex flex-row gap-1 py-1 items-center">
		<div>Colors:</div>
		<button
			class={editPrefix.color == 'white' ? bAS.white : bS.white}
			onclick={() => (editPrefix.color = 'white')}>White</button
		>
		<button
			class={editPrefix.color == 'blue' ? bAS.blue : bS.blue}
			onclick={() => (editPrefix.color = 'blue')}>Blue</button
		>
		<button
			class={editPrefix.color == 'yellow' ? bAS.yellow : bS.yellow}
			onclick={() => (editPrefix.color = 'yellow')}>Yellow</button
		>
		<button
			class={editPrefix.color == 'green' ? bAS.green : bS.green}
			onclick={() => (editPrefix.color = 'green')}>Green</button
		>
		<button
			class={editPrefix.color == 'orange' ? bAS.orange : bS.orange}
			onclick={() => (editPrefix.color = 'orange')}>Orange</button
		>
		<button
			class={editPrefix.color == 'purple' ? bAS.purple : bS.purple}
			onclick={() => (editPrefix.color = 'purple')}>Purple</button
		>
		<button
			class={editPrefix.color == 'red' ? bAS.red : bS.red}
			onclick={() => (editPrefix.color = 'red')}>Red</button
		>
	</div>
	<table class="w-full border-separate">
		<thead class="text-left">
			<tr>
				<th class="border p-0.5">Prefix</th>
				<th class="border p-0.5">Color</th>
				<th class="border p-0.5">Weight</th>
				<th class="border p-0.5">Actions</th>
			</tr>
		</thead>
		<tbody>
			{#each prefixes as prefix (prefix.prefix)}
				<tr>
					<td class="border p-0.5">{prefix.prefix}</td>
					<td class="border p-0.5"
						>{prefix.color.charAt(0).toUpperCase() + prefix.color.slice(1)}</td
					>
					<td class="border p-0.5">{prefix.weight}</td>
					<td class="border p-0.5">
						<div class="flex flex-row gap-1 items-center">
							<button class={bS[prefix.color]} onclick={() => (editPrefix = { ...prefix })}
								>Edit</button
							>
							<button class={bS[prefix.color]} disabled={saving} onclick={() => deletePrefix(prefix)}>Delete</button>
						</div>
					</td>
				</tr>
			{/each}
		</tbody>
	</table>
</div>
