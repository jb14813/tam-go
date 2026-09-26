<script>
	import { resolve } from '$app/paths';
	import { pollJSON, saves } from '../api';

	// The connection state of a client in remote mode, from GET /api/status.
	// Nothing is rendered in standalone mode, when the client cannot be reached
	// or when it does not have the route yet (an older client answers 404).
	let status = $state(null);

	const POLL_MS = 3000;

	$effect(() => {
		let stopped = false;
		let timer;
		const poll = async () => {
			const { status: code, data } = await pollJSON('/api/status');
			if (stopped) return;
			status = code === 200 && data && data.mode === 'remote' ? data : null;
			// An older client has no status route: no point asking again this page load.
			if (code === 404) return;
			timer = setTimeout(poll, POLL_MS);
		};
		poll();
		return () => {
			stopped = true;
			clearTimeout(timer);
		};
	});

	const colors = {
		green: 'bg-green-200 text-green-900 border-green-600',
		amber: 'bg-amber-200 text-amber-900 border-amber-600',
		red: 'bg-red-200 text-red-900 border-red-600',
		gray: 'bg-gray-200 text-gray-900 border-gray-600'
	};

	const waiting = (n) => (n > 0 ? `, ${saves(n)} waiting` : '');

	let view = $derived.by(() => {
		if (!status) return null;
		const pending = Number(status.pending) || 0;
		switch (status.state) {
			case 'connected':
				return {
					color: 'green',
					text: `Connected to ${status.server_name || status.server || 'server'}`,
					settings: false
				};
			case 'reconnecting':
				return { color: 'amber', text: `Reconnecting${waiting(pending)}`, settings: false };
			case 'offline':
				return { color: 'red', text: `Offline${waiting(pending)}`, settings: false };
			case 'unauthenticated':
				return { color: 'red', text: "The server rejected this laptop's key, open", settings: true };
			default:
				return { color: 'gray', text: String(status.state || 'Unknown state'), settings: false };
		}
	});

	let failed = $derived((status && Number(status.failed)) || 0);
</script>

{#if view}
	<div
		id="status_bar"
		role="status"
		class="w-full truncate border-b px-2 py-0.5 text-sm {colors[view.color]}"
	>
		<span>
			{view.text}
			{#if view.settings}<a href={resolve('/settings')} class="font-bold underline">Settings</a>{/if}
		</span>
		{#if failed > 0}
			<span class="font-bold">
				&middot; {saves(failed)} could not be sent, open
				<a href={resolve('/settings')} class="underline">Settings</a>
			</span>
		{/if}
	</div>
{/if}
