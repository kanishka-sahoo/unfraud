import Link from "next/link";
import { Suspense } from "react";
import { CardSearch } from "@/components/card-search";
import { Risk } from "@/components/marks";
import { listCases } from "@/lib/api";
import { byFeature, headline, networkHref } from "@/lib/format";

export const metadata = { title: "Network" };

export default function NetworkHome() {
  return (
    <>
      <div className="head">
        <div>
          <h1>Network</h1>
          <p>Cards are linked when they pay the same merchant within 30 days. Fraud often clusters: a card that shares quiet merchants with confirmed-fraud cards deserves a closer look.</p>
        </div>
        <CardSearch />
      </div>
      <section className="card">
        <header><div><h2>Cards behind the riskiest open alerts</h2><p>Start an investigation from one of these</p></div></header>
        <Suspense fallback={<p className="skeleton body">Loading…</p>}>
          <Suggestions />
        </Suspense>
      </section>
    </>
  );
}

async function Suggestions() {
  const rows = await listCases({ status: "open", min_action: "2", sort: "score", limit: 40 });
  const seen = new Set<string>();
  const picks = rows.filter((r) => r.entities.payer && !seen.has(r.entities.payer) && seen.add(r.entities.payer)).slice(0, 12);
  if (picks.length === 0) return <p className="empty"><b>No open alerts</b>Search for a card above.</p>;
  return (
    <table className="t">
      <thead><tr><th>Card</th><th>Latest alert</th><th>Fraud probability</th></tr></thead>
      <tbody>
        {picks.map((r) => {
          const h = headline(byFeature(r.payment));
          return (
            <tr key={r.event_id} className="row">
              <td><Link href={networkHref(r.entities.payer)} className="stretch-link mono cell-main">{r.entities.payer}</Link></td>
              <td>{h.amount} <span className="dim">at {h.merchant}</span></td>
              <td><Risk p={r.probability} /></td>
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}
