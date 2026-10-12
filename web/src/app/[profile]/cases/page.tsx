import Link from "next/link";
import { Suspense } from "react";
import { ActionMark, StageMark } from "@/components/marks";
import { api, type CaseListItem } from "@/lib/api";
import { caseHref, eventTime, pct } from "@/lib/format";

const PAGE = 50;
const filters = {
  status: [["open", "Open"], ["disposed", "Reviewed"], ["all", "All"]],
  min_action: [["1", "Review and block"], ["2", "Block only"]],
  sort: [["score", "Highest risk"], ["time", "Latest event"], ["recent", "Newest case"]],
} as const;
type Key = keyof typeof filters;
const defaults: Record<Key, string> = { status: "open", min_action: "1", sort: "score" };

export default function CasesPage({ params, searchParams }: PageProps<"/[profile]/cases">) {
  return (
    <>
      <div className="page-head">
        <div>
          <h1>Cases</h1>
          <p>Events either stage flagged. A case keeps the stricter decision and is never downgraded.</p>
        </div>
      </div>
      <Suspense fallback={<p className="skeleton">Loading cases…</p>}>
        <Queue params={params} searchParams={searchParams} />
      </Suspense>
    </>
  );
}

async function Queue({ params, searchParams }: {
  params: Promise<{ profile: string }>;
  searchParams: Promise<Record<string, string | string[] | undefined>>;
}) {
  const { profile } = await params;
  const sp = await searchParams;
  const get = (k: Key) => (typeof sp[k] === "string" ? (sp[k] as string) : defaults[k]);
  const entity = typeof sp.entity === "string" ? sp.entity : "";
  const page = Math.max(0, Number(sp.page ?? 0) || 0);
  const q = new URLSearchParams({ profile, status: get("status"), min_action: get("min_action"), sort: get("sort"),
    limit: String(PAGE + 1), offset: String(page * PAGE) });
  if (entity) q.set("entity", entity);
  const rows = await api<CaseListItem[]>(`/api/cases?${q}`);
  const more = rows.length > PAGE;

  const href = (k: string, v: string) => {
    const next = new URLSearchParams({ status: get("status"), min_action: get("min_action"), sort: get("sort") });
    if (entity) next.set("entity", entity);
    next.set(k, v);
    if (k !== "page") next.delete("page");
    return `/${profile}/cases?${next}`;
  };
  return (
    <>
      <div className="filters">
        {(Object.keys(filters) as Key[]).map((k) => (
          <div className="set" key={k}>
            <span>{{ status: "Status", min_action: "Decision", sort: "Order" }[k]}</span>
            {filters[k].map(([v, label]) => (
              <Link key={v} href={href(k, v)} aria-current={get(k) === v ? "true" : undefined}>{label}</Link>
            ))}
          </div>
        ))}
        {entity && (
          <div className="set"><span>Entity</span><span className="chip">{entity}</span><Link href={`/${profile}/cases`}>Clear</Link></div>
        )}
      </div>
      {rows.length === 0 ? (
        <p className="empty">No cases match these filters.</p>
      ) : (
        <table className="rows">
          <thead>
            <tr>
              <th>Event</th><th>Decision</th><th>Flagged by</th><th className="num">Fraud probability</th>
              <th>Rule reasons</th><th>Event time</th><th>Summary</th><th>Disposition</th>
            </tr>
          </thead>
          <tbody>
            {rows.slice(0, PAGE).map((c) => (
              <tr key={c.event_id}>
                <td><Link className="row-link id" href={caseHref(profile, c.event_id)}>{c.event_id}</Link></td>
                <td><ActionMark action={c.action} /></td>
                <td><StageMark stage={c.flagged_by} /></td>
                <td className="num">{pct(c.probability, 1)}</td>
                <td>{c.reason_codes.length ? c.reason_codes.map((r) => <span className="chip" key={r}>{r}</span>) : <span className="muted">model only</span>}</td>
                <td>{eventTime(c.event_time)}</td>
                <td className="muted">{c.summary_status === "done" ? "written" : c.summary_status}</td>
                <td>{c.disposition ?? <span className="muted">open</span>}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      <p className="filters">
        {page > 0 && <Link href={href("page", String(page - 1))}>Previous</Link>}
        {more && <Link href={href("page", String(page + 1))}>Next</Link>}
      </p>
    </>
  );
}
