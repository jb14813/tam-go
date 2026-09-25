<script>
	import { onMount } from 'svelte';
	import HeaderBar from '$lib/client/components/HeaderBar.svelte';
	import { bS, bAS, iS } from '$lib/client/styles';
	import { resolve } from '$app/paths';
    import { handlers } from '$lib/client/handlers';

	const pageTitle = 'Prefixes | TAM';

	let prefixes = $state([]);
	let editPrefix = $state({ prefix: '', color: 'white', weight: 1 });

	onMount(async () => {
		prefixes = await handlers.get('/api/prefixes');
		const form_prefix = document.getElementById('form_prefix');
		if (form_prefix) form_prefix.select();
	});
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
			<input type="text" id="form_prefix" class={iS.normal} bind:value={editPrefix.prefix} />
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
			<input type="number" id="form_weight" class={iS.normal} bind:value={editPrefix.weight} />
		</div>
		<div class="flex flex-col gap-1">
			<div>Actions</div>
			<button
				class={bS[editPrefix.color]}
				onclick={async () => {
					if (editPrefix.prefix) {
						const req = await fetch('/api/prefixes', {
							method: 'POST',
							headers: { 'Content-Type': 'application/json' },
							body: JSON.stringify([editPrefix])
						});
						if (req.ok) window.location.reload();
					}
					editPrefix.prefix = '';
					const form_prefix = document.getElementById('form_prefix');
					if (form_prefix) form_prefix.select();
				}}>Add/Change</button
			>
		</div>
	</div>
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
							<button
								class={bS[prefix.color]}
								onclick={async () => {
									const res = await fetch(`/api/prefixes?p=${prefix.prefix}`, {
										method: 'DELETE'
									});
									if (res.ok) window.location.reload();
								}}>Delete</button
							>
						</div>
					</td>
				</tr>
			{/each}
		</tbody>
	</table>
</div>
