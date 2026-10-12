// Forwards browser requests (live stream, dispositions, replays, graph) to
// cases-api, so the browser only ever talks to this origin.
const base = () => process.env.CASES_API_URL ?? "http://localhost:8080";

async function forward(req: Request, path: string[]): Promise<Response> {
  const url = new URL(req.url);
  const target = `${base()}/api/${path.map(encodeURIComponent).join("/")}${url.search}`;
  const res = await fetch(target, {
    method: req.method,
    headers: { "content-type": req.headers.get("content-type") ?? "application/json" },
    body: req.method === "GET" ? undefined : await req.text(),
    signal: req.signal,
    cache: "no-store",
  });
  const headers = new Headers();
  for (const h of ["content-type", "cache-control"]) {
    const v = res.headers.get(h);
    if (v) headers.set(h, v);
  }
  return new Response(res.body, { status: res.status, headers });
}

export async function GET(req: Request, ctx: RouteContext<"/api/[...path]">) {
  return forward(req, (await ctx.params).path);
}

export async function POST(req: Request, ctx: RouteContext<"/api/[...path]">) {
  return forward(req, (await ctx.params).path);
}
