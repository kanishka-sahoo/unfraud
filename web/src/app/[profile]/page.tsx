import Link from "next/link";
import { Suspense } from "react";
import { ActionMark, StageMark } from "@/components/marks";
import { Throughput } from "@/components/throughput";
import { api, type Overview } from "@/lib/api";
import { caseHref, eventTime, int, pct, profileLabel } from "@/lib/format";

export default function OverviewPage({ params }: PageProps<"/[profile]">) {
  return (
    <Suspense fallback={<p className="skeleton">Loading overview…</p>}>
      <OverviewBody params={params} />
    </Suspense>
  );
}

async function OverviewBody({ params }: { params: Promise<{ profile: string }> }) {
  const { profile } = await params;
  const o = await api<Overview>(`/api/overview/${encodeURIComponent(profile)}`);
  const count = (stage: number, action?: number) =>
    o.decisions.filter((d) => d.stage === stage && (action === undefined || d.action === action)).reduce((s, d) => s + d.count, 0);
  const arrived = count(1);
  const flagged = count(1, 1) + count(1, 2);
  const revised = count(2);
  const revisedFlagged = count(2, 1) + count(2, 2);
  return (
    <>
      <div className="page-head">
        <div>
          <h1>{profileLabel[profile] ?? profile}</h1>
          <p>Every Event is scored on arrival; graph-svc revises it once its graph is known. Cases open for anything either stage flags.</p>
        </div>
      </div>
      <div className="now">
        <div><div className="figure">{int(arrived)}</div><div className="what">Events scored on arrival</div></div>
        <div><div className="figure">{arrived ? pct(flagged / arrived, 2) : "–"}</div><div className="what">flagged on arrival</div></div>
        <div><div className="figure">{revised ? pct(revisedFlagged / revised, 2) : "–"}</div><div className="what">flagged after graph revision</div></div>
        <div><div className="figure">{int(o.cases.open)}</div><div className="what">open cases, {int(o.cases.open_block)} blocked</div></div>
        <div><div className="figure">{int(o.summaries.done ?? 0)}</div><div className="what">summaries written</div></div>
      </div>
      <section className="section">
        <h2>Decisions per minute</h2>
        <Throughput data={o.per_minute ?? []} />
      </section>
      <section className="section">
        <h2>Newest open cases</h2>
        {o.recent_cases.length === 0 ? (
          <p className="empty">No open cases. Everything scored so far was approved, or has been reviewed.</p>
        ) : (
          <table className="rows">
            <thead>
              <tr><th>Event</th><th>Decision</th><th>Flagged by</th><th className="num">Fraud probability</th><th>Event time</th></tr>
            </thead>
            <tbody>
              {o.recent_cases.map((c) => (
                <tr key={c.event_id}>
                  <td><Link className="row-link id" href={caseHref(profile, c.event_id)}>{c.event_id}</Link></td>
                  <td><ActionMark action={c.action} /></td>
                  <td><StageMark stage={c.flagged_by} /></td>
                  <td className="num">{pct(c.probability, 1)}</td>
                  <td>{eventTime(c.event_time)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
        <p><Link href={`/${profile}/cases`}>Open the case queue</Link></p>
      </section>
    </>
  );
}
