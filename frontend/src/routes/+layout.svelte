<script>
	import './layout.css';
	import { base } from '$app/paths';
	import StatusBar from '$lib/client/components/StatusBar.svelte';

	let { children } = $props();
</script>

<svelte:head>
	<link rel="icon" href="{base}/favicon.ico" />
	<link rel="manifest" href="{base}/manifest.json" />
</svelte:head>
<StatusBar />
{@render children()}
