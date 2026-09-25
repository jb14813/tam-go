<script>
	import { bS } from '../styles';
	import hotkeys from 'hotkeys-js';

	let { prefix, functions } = $props();

	$effect(() => {
		hotkeys.filter = () => {
			return true;
		};
		const nextLine = (e) => {
			e.preventDefault();
			if (functions.nextLine) functions.nextLine();
		};
		const prevLine = (e) => {
			e.preventDefault();
			if (functions.prevLine) functions.prevLine();
		};
		const dupDown = (e) => {
			e.preventDefault();
			if (functions.dupDown) functions.dupDown();
		};
		const dupUp = (e) => {
			e.preventDefault();
			if (functions.dupUp) functions.dupUp();
		};
		const copy = (e) => {
			e.preventDefault();
			if (functions.copy) functions.copy();
		};
		const paste = (e) => {
			e.preventDefault();
			if (functions.paste) functions.paste();
		};
		const save = (e) => {
			e.preventDefault();
			if (functions.save) functions.save();
		};
		hotkeys('alt+l', nextLine);
		hotkeys('alt+o', prevLine);
		hotkeys('alt+j', dupDown);
		hotkeys('alt+u', dupUp);
		hotkeys('alt+c', copy);
		hotkeys('alt+v', paste);
		hotkeys('alt+s', save);
		return () => {
			hotkeys.unbind('alt+l', nextLine);
			hotkeys.unbind('alt+o', prevLine);
			hotkeys.unbind('alt+j', dupDown);
			hotkeys.unbind('alt+u', dupUp);
			hotkeys.unbind('alt+c', copy);
			hotkeys.unbind('alt+v', paste);
			hotkeys.unbind('alt+s', save);
		};
	});
</script>

<div class="flex flex-row justify-between gap-1 py-1">
	<div class="flex flex-row gap-1">
		{#if functions.dupDown}
			<button class={bS[prefix.color]} title="Alt + J" onclick={() => functions.dupDown()}
				>Duplicate Down</button
			>
		{/if}
		{#if functions.dupUp}
			<button class={bS[prefix.color]} title="Alt + U" onclick={() => functions.dupUp()}
				>Duplicate Up</button
			>
		{/if}
		{#if functions.nextLine}
			<button class={bS[prefix.color]} title="Alt + L" onclick={() => functions.nextLine()}
				>Next Line</button
			>
		{/if}
		{#if functions.prevLine}
			<button class={bS[prefix.color]} title="Alt + O" onclick={() => functions.prevLine()}
				>Previous Line</button
			>
		{/if}
		{#if functions.copy}
			<button class={bS[prefix.color]} title="Alt + C" onclick={() => functions.copy()}>Copy</button
			>
		{/if}
		{#if functions.paste}
			<button class={bS[prefix.color]} title="Alt + V" onclick={() => functions.paste()}
				>Paste</button
			>
		{/if}
	</div>
	<div class="flex flex-row gap-1">
		{#if functions.save}
			<button class={bS[prefix.color]} title="Alt + S" onclick={() => functions.save()}
				>Save Marked</button
			>
		{/if}
		{#if functions.cancel}
			<button
				class={bS[prefix.color]}
				title="Alt + S"
				onclick={() => {
					const yes_no = confirm(
						'This will cancel all pending changes. Are you sure you want to cancel?'
					);
					if (yes_no) {
						functions.cancel();
					}
				}}>Cancel Marked</button
			>
		{/if}
	</div>
</div>
