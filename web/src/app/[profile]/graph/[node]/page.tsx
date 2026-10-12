import Link from "next/link";
import { Suspense } from "react";
import { GraphView } from "@/components/graph-view";
import { api, type Subgraph } from "@/lib/api";

export const metadata = { title: "Graph" };

const explain: Record<string, string> = {
  "ieee-cis": "Cards linked through shared devices over the last 30 days. Devices used by more than 20 cards are generic fingerprints and never link.",
  elliptic: "Bitcoin payments between transactions. Links are known once a transaction's timestep closes.",
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
              <span><span className="key" style={{ background: "#e8e4d8", height: 8, width: 8, borderRadius: 4 }} />{profile === "elliptic" ? "Transaction" : "Card"}</span>
              {profile !== "elliptic" && <span><span className="key" style={{ background: "#3987e5", height: 8, width: 8, borderRadius: 4 }} />Device</span>}
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
