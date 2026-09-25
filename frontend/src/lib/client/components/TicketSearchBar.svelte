<script>
	import hotkeys from 'hotkeys-js';
	import { bS, iS } from '../styles';

	let { prefix, functions, searchForm = $bindable() } = $props();

	$effect(() => {
		hotkeys.filter = () => {
			return true;
		};
		const selectFirstName = (e) => {
			e.preventDefault();
			const first_name = document.getElementById('search_first_name');
			if (first_name) first_name.select();
		};
		const selectLastName = (e) => {
			e.preventDefault();
			const last_name = document.getElementById('search_last_name');
			if (last_name) last_name.select();
		};
		const selectPhoneNumber = (e) => {
			e.preventDefault();
			const phone_number = document.getElementById('search_phone_number');
			if (phone_number) phone_number.select();
		};
		hotkeys('alt+q', selectFirstName);
		hotkeys('alt+w', selectLastName);
		hotkeys('alt+e', selectPhoneNumber);
		return () => {
			hotkeys.unbind('alt+q', selectFirstName);
			hotkeys.unbind('alt+w', selectLastName);
			hotkeys.unbind('alt+e', selectPhoneNumber);
		};
	});
</script>

<div class="flex flex-row justify-between gap-1 p-1">
	<div class="flex flex-row gap-1 items-center">
		<div>First Name:</div>
		<input
			type="text"
			id="search_first_name"
			class={iS.normal}
			bind:value={searchForm.first_name}
			onclick={(e) => e.target.select()}
		/>
		<div>Last Name:</div>
		<input
			type="text"
			id="search_last_name"
			class={iS.normal}
			bind:value={searchForm.last_name}
			onclick={(e) => e.target.select()}
		/>
		<div>Phone Number:</div>
		<input
			type="text"
			id="search_phone_number"
			class={iS.normal}
			bind:value={searchForm.phone_number}
			onclick={(e) => e.target.select()}
		/>
		{#if functions.search}
			<button class={bS[prefix.color]} onclick={() => functions.search()}>Search</button>
		{/if}
	</div>
</div>
