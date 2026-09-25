# TAM web app

The SvelteKit single-page app served by `tam-client` under `/web`. It talks to the client's JSON API at `/api` on the same origin.

- `pnpm install --frozen-lockfile` once, then `pnpm build` writes the static build into `../cmd/tam-client/dist`, which `tam-client` embeds at compile time (`go build ./cmd/tam-client` must run after the web build; `../build.sh client` does both).
- `pnpm dev` starts Vite on http://localhost:5173/web/ for working on the pages; `vite.config.js` proxies `/api` to a `tam-client` running on `localhost:3080`, so start that first (`../run.sh client` or the binary).
- The app is a pure client-side SPA (`ssr = false`, `prerender = false`, fallback `index.html`, base path `/web`). Every link is a full page load (`data-sveltekit-reload` in `app.html`), which is what lets the forms save pending rows when you leave a page.

Pages live in `src/routes`, shared pieces in `src/lib/client` (`api.js` for fetch helpers, `styles.js` for the Tailwind class maps, `components/` for the header, pager, command and search bars).
