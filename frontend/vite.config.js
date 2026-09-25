import tailwindcss from '@tailwindcss/vite';
import adapter from '@sveltejs/adapter-static';
import { sveltekit } from '@sveltejs/kit/vite';
import { defineConfig } from 'vite';

export default defineConfig({
	// `pnpm dev` only: forward the API to a running tam-client.
	server: {
		proxy: { '/api': 'http://localhost:3080' }
	},
	plugins: [
		tailwindcss(),
		sveltekit({
			compilerOptions: {
				// Force runes mode for the project, except for libraries. Can be removed in svelte 6.
				runes: ({ filename }) =>
					filename.split(/[/\\]/).includes('node_modules') ? undefined : true
			},
			adapter: adapter({
				pages: '../cmd/tam-client/dist',
				assets: '../cmd/tam-client/dist',
				fallback: 'index.html',
				precompress: false,
				strict: false
			}),
			paths: { base: '/web', relative: false }
		})
	]
});
