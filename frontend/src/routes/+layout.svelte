<script>
	import './layout.css';
	import favicon from '$lib/assets/favicon.svg';
	import { base } from '$app/paths';
	import StatusBar from '$lib/client/components/StatusBar.svelte';

	let { children } = $props();
</script>

<svelte:head>
	<link rel="icon" href={favicon} />
	<link rel="manifest" href="{base}/manifest.json" />
</svelte:head>
<StatusBar />
{@render children()}
