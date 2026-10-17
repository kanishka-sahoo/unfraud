import Link from "next/link";
import { Suspense } from "react";
import { AlertsTable } from "@/components/alerts-table";
import { listCases } from "@/lib/api";

export const metadata = { title: "Alerts" };

const PAGE = 50;
const status = [["open", "Open"], ["disposed", "Reviewed"], ["all", "All"]] as const;
const decision = [["1", "Review + block"], ["2", "Blocked only"]] as const;
const sort = [["score", "Highest risk"], ["time", "Latest payment"], ["recent", "Newest alert"]] as const;
const defaults = { status: "open", min_action: "1", sort: "score" };
type Key = keyof typeof defaults;

export default function AlertsPage({ searchParams }: PageProps<"/alerts">) {
  return (
    <>
      <div className="head">
        <div>
          <h1>Alerts</h1>
          <p>Payments the engine sent to review or blocked. An alert keeps the strictest decision either stage made.</p>
        </div>
      </div>
      <Suspense fallback={<p className="skeleton">Loading alerts…</p>}>
        <Queue searchParams={searchParams} />
      </Suspense>
    </>
  );
}

async function Queue({ searchParams }: { searchParams: Promise<Record<string, string | string[] | undefined>> }) {
  const sp = await searchParams;
  const get = (k: Key) => (typeof sp[k] === "string" ? (sp[k] as string) : defaults[k]);
  const entity = typeof sp.entity === "string" ? sp.entity : "";
  const page = Math.max(0, Number(sp.page ?? 0) || 0);
  const rows = await listCases({
    status: get("status"), min_action: get("min_action"), sort: get("sort"), entity, limit: PAGE + 1, offset: page * PAGE,
  });
  const more = rows.length > PAGE;
  const href = (k: string, v: string) => {
    const next = new URLSearchParams({ status: get("status"), min_action: get("min_action"), sort: get("sort") });
    if (entity) next.set("entity", entity);
    next.set(k, v);
    if (k !== "page") next.delete("page");
    return `/alerts?${next}`;
  };
  const [entityType, entityId] = entity.split("=");
  return (
    <section className="card">
      <div className="toolbar">
        <nav className="tabs" aria-label="Status">
          {status.map(([v, l]) => <Link key={v} href={href("status", v)} aria-current={get("status") === v ? "true" : undefined}>{l}</Link>)}
        </nav>
        <div className="filters">
          <nav className="tabs" aria-label="Decision">
            {decision.map(([v, l]) => <Link key={v} href={href("min_action", v)} aria-current={get("min_action") === v ? "true" : undefined}>{l}</Link>)}
          </nav>
          <nav className="tabs" aria-label="Order">
            {sort.map(([v, l]) => <Link key={v} href={href("sort", v)} aria-current={get("sort") === v ? "true" : undefined}>{l}</Link>)}
          </nav>
        </div>
      </div>
      {entity && (
        <div className="toolbar">
          <span className="filters">
            <span>Showing alerts for {({ payer: "card", payee: "merchant", customer: "cardholder" } as Record<string, string>)[entityType] ?? entityType}</span>
            <span className="chip mono">{entityId}</span>
            <Link href="/alerts" className="link">Clear</Link>
          </span>
        </div>
      )}
      {rows.length === 0 ? (
        <p className="empty"><b>No alerts match these filters</b>Try another status or decision.</p>
      ) : (
        <AlertsTable rows={rows.slice(0, PAGE)} />
      )}
      <div className="pager">
        <span>Page {page + 1}</span>
        <span style={{ display: "flex", gap: 8 }}>
          {page > 0 && <Link href={href("page", String(page - 1))}>Previous</Link>}
          {more && <Link href={href("page", String(page + 1))}>Next</Link>}
        </span>
      </div>
    </section>
  );
}
