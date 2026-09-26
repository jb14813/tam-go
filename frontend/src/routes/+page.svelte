<script>
	import favicon from '$lib/assets/favicon.svg';
	import { tS, bS, bAS } from '$lib/client/styles.js';
	import { resolve } from '$app/paths';
	import { handlers } from '$lib/client/handlers';
	import hotkeys from 'hotkeys-js';
  import { onMount } from 'svelte';

	const pageTitle = 'Main Menu | TAM';

	let pageData = $state({
	  prefixes: [],
		curPrefix: "",
		venueName: "",
		adminMode: false,
		disableAttrib: false
	})

	const pColor = $derived.by(() => {
	  const curPrefix = pageData.prefixes.find(p => p.prefix === pageData.curPrefix);
		if (curPrefix) {
		  return curPrefix.color
		} else {
		  return 'gray'
		}
	})

	hotkeys('alt+a', function(){
	  pageData.adminMode = !pageData.adminMode;
	})

	onMount(async () => {
	  pageData.prefixes = await handlers.get('/api/prefixes');
		const settings = await handlers.get('/api/settings');
		pageData.venueName = settings.venue_name;
	})
</script>

<svelte:head>
	<title>{pageTitle}</title>
</svelte:head>

<div class="p-1" id="app_container">
	<div class="flex flex-row gap-1 items-center">
		<div>
			<img src={favicon} alt="TAM Logo" style="height: 4rem" />
		</div>
		<div>
			<h1 class="text-xl font-bold">{pageTitle}</h1>
			<div class="italic">{pageData.venueName}</div>
		</div>
	</div>

	<div class="flex flex-col md:flex-row md:flex-wrap gap-1 py-1">
		<div id="prefixes" class="flex flex-col gap-1 p-2 border border-black rounded">
			<h2 class="text-lg font-bold">Prefix Selection:</h2>
			{#each pageData.prefixes as prefix (prefix.prefix)}
				<button
					class={pageData.curPrefix == prefix.prefix ? bAS[prefix.color] : bS[prefix.color]}
					onclick={() => (pageData.curPrefix = prefix.prefix)}>{prefix.prefix}</button
				>
			{:else}
				<div>No Prefixes</div>
			{/each}
		</div>
		{#if pageData.curPrefix}
			<div class="flex flex-col gap-1 items-center p-1 border border-black rounded">
				<h2 class="text-lg font-bold">Forms:</h2>
				<div class="grid grid-cols-2 gap-1 p-1 text-center">
					<a href="." class={bS[pColor]}
						>Tickets</a
					>
					<a href="." class={bS[pColor]}
						>Baskets</a
					>
					<a
						href="."
						class="{bS[pColor]} col-span-2">Drawing Form</a
					>
				</div>
			</div>
			<div class="flex flex-col gap-1 items-center p-1 border border-black rounded">
				<h2 class="text-lg font-bold">Reports:</h2>
				<div class="grid grid-cols-2 gap-1 p-1 text-center">
					<a href="." class={bS[pColor]}
						>Winners By Name</a
					>
					<a href="." class={bS[pColor]}
						>Winners By Basket</a
					>
				</div>
			</div>
		{:else}
			<div class="flex flex-col gap-1 items-center justify-center p-2 border border-black rounded">
				<h2 class="text-lg font-bold">Please select a prefix to continue.</h2>
			</div>
		{/if}
		<div class="flex flex-col gap-1 items-center text-center p-1 border border-black rounded">
			<h2 class="text-lg font-bold">Prefix Independent:</h2>
			<a href="." class="{bS.gray} w-full">Ticket Counts</a>
			<a href="." class="{bS.gray} w-full">Print Sheets</a>
		</div>
	</div>

	{#if pageData.adminMode}
		<div id="admin_mode" class="py-1">
			<h2 class="text-lg font-bold">Admin Mode:</h2>
			<div class="flex flex-row gap-1">
				<a href={resolve('/settings')} class={bS.gray}>Settings</a>
				<a href="." class={bS.gray}>Search Tickets</a>
			</div>
		</div>
	{/if}

	<div id="footer">
		<div class="text-center text-xs">
			<p>&copy; 2026 Ticket Auction Manager</p>
			{#if !pageData.disableAttrib}
			<p>
				Created by Dilan Gilluly. <a
					href="https://ko-fi.com/techguydilan"
					class="text-blue-500"
					target="_blank">My Ko-Fi</a
				>.
			</p>
			{/if}
		</div>
	</div>
</div>
