# unfraud console

The analyst and operator web app (Next.js 16, App Router, Cache Components). It reads everything from `cases-api` (`../api/openapi.yaml`; `pnpm gen:api` regenerates `src/lib/schema.d.ts`). Browser calls go through `src/app/api/[...path]/route.ts`.

- `pnpm build` then `../scripts/console-local.sh start` runs it on http://localhost:3100 against the kind cluster.
- In the cluster it is the `web` Deployment: `make console` port-forwards it to http://localhost:3000.
- `pnpm test:e2e` runs the Playwright suite against `CONSOLE_URL` (default http://localhost:3000).
