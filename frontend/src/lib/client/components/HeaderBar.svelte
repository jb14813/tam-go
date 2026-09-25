<script>
	import { resolve } from '$app/paths';
	import { bS } from '../styles';

	let { children } = $props();
</script>

<div id="header_bar" class="py-1 flex flex-row gap-1 items-center">
	<a href={resolve('/')} class="{bS.gray} font-bold">Main Menu</a>
	{#if children}
		{@render children()}
	{/if}
</div>
