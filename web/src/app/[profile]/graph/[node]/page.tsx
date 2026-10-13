import Link from "next/link";
import { Suspense } from "react";
import { GraphView } from "@/components/graph-view";
import { api, type Subgraph } from "@/lib/api";

export const metadata = { title: "Graph" };

/** What a Node (payer) and a Link attribute (payee) are in each graph. */
const nouns: Record<string, [string, string]> = { tabformer: ["Card", "Merchant"], amlworld: ["Account", "Receiving account"] };

const explain: Record<string, string> = {
  tabformer: "Cards linked through merchants they both paid in the last 30 days. Merchants paid by more than 100 cards are too common to link.",
  amlworld: "Accounts linked through accounts they both paid in the last 7 days. Accounts paid by more than 20 others are too common to link.",
};

export default function GraphPage({ params, searchParams }: PageProps<"/[profile]/graph/[node]">) {
  return (
    <Suspense fallback={<p className="skeleton">Loading graph…</p>}>
      <GraphBody params={params} searchParams={searchParams} />
    </Suspense>
  );
}

async function GraphBody({ params, searchParams }: {
  params: Promise<{ profile: string; node: string }>;
  searchParams: Promise<Record<string, string | string[] | undefined>>;
}) {
  const { profile, node } = await params;
  const hops = Math.min(4, Math.max(1, Number((await searchParams).hops) || 2));
  const id = decodeURIComponent(node);
  let data: Subgraph | null = null;
  let error = "";
  try {
    data = await api<Subgraph>(`/api/graph/${encodeURIComponent(profile)}/${encodeURIComponent(id)}?hops=${hops}`);
  } catch {
    error = "graph-svc is not running for this dataset, so its graph can't be shown.";
  }
  const fraud = data?.nodes.filter((n) => n.fraud && n.id !== id) ?? [];
  return (
    <>
      <div className="page-head">
        <div>
          <h1>Graph around {id}</h1>
          <p>{explain[profile] ?? ""}</p>
        </div>
        <div className="filters">
          <div className="set">
            <span>Reach</span>
            {[1, 2, 3, 4].map((h) => (
              <Link key={h} href={`/${profile}/graph/${encodeURIComponent(id)}?hops=${h}`} aria-current={h === hops ? "true" : undefined}>
                {h} {h === 1 ? "hop" : "hops"}
              </Link>
            ))}
          </div>
        </div>
      </div>
      {error && <p className="empty">{error}</p>}
      {data && data.nodes.length <= 1 && <p className="empty">{id} has no links in the current window.</p>}
      {data && data.nodes.length > 1 && (
        <div className="graph-wrap">
          <GraphView profile={profile} data={data} />
          <div>
            <div className="legend" style={{ flexDirection: "column", gap: 6 }}>
              <span><span className="key" style={{ background: "#e8e4d8", height: 8, width: 8, borderRadius: 4 }} />{nouns[profile]?.[0] ?? "Payer"}</span>
              <span><span className="key" style={{ background: "#3987e5", height: 8, width: 8, borderRadius: 4 }} />{nouns[profile]?.[1] ?? "Payee"}</span>
              <span><span className="key" style={{ background: "#d03b3b", height: 8, width: 8, borderRadius: 4 }} />Confirmed fraud (released label)</span>
            </div>
            <p className="muted" style={{ fontSize: "var(--t-sm)" }}>
              {data.nodes.length} nodes, {data.edges.length} links{data.truncated ? " (truncated at 300 nodes)" : ""}. Click a node to re-centre.
            </p>
            <h2>Confirmed fraud nearby</h2>
            {fraud.length === 0 ? (
              <p className="muted">None released yet.</p>
            ) : (
              <ul>{fraud.slice(0, 20).map((n) => <li key={n.id}><Link href={`/${profile}/graph/${encodeURIComponent(n.id)}`} className="id">{n.id}</Link></li>)}</ul>
            )}
          </div>
        </div>
      )}
    </>
  );
}
