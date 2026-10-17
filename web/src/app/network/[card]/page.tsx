import Link from "next/link";
import { Suspense } from "react";
import { AskButton } from "@/components/assistant";
import { CardSearch } from "@/components/card-search";
import { NetworkView } from "@/components/network-view";
import { api, type Subgraph } from "@/lib/api";
import { merchantShort, networkHref, num, PROFILE } from "@/lib/format";
import { toNetwork } from "@/lib/network";

export async function generateMetadata({ params }: PageProps<"/network/[card]">) {
  return { title: `Network of card ${decodeURIComponent((await params).card)}` };
}

export default function CardNetwork({ params }: PageProps<"/network/[card]">) {
  return (
    <Suspense fallback={<p className="skeleton">Loading network…</p>}>
      <Body params={params} />
    </Suspense>
  );
}

async function Body({ params }: { params: Promise<{ card: string }> }) {
  const card = decodeURIComponent((await params).card);
  let graph: Subgraph | null = null;
  try {
    graph = await api<Subgraph>(`/api/graph/${PROFILE}/${encodeURIComponent(card)}?hops=2`);
  } catch {
    // graph-svc down: explained below.
  }
  const net = graph ? toNetwork(graph) : null;
  const linked = net?.merchants.filter((m) => !m.common) ?? [];
  const fraudCards = net ? [...net.cards].filter(([, v]) => v.fraud) : [];
  return (
    <>
      <div className="crumbs"><Link href="/network">Network</Link> / <span className="mono">{card}</span></div>
      <div className="head">
        <div>
          <h1>Card <span className="mono">{card}</span></h1>
          <p>Merchants this card paid in the last 30 days, and the other cards that paid them.</p>
        </div>
        <div className="row-actions">
          <AskButton question="What does this card's recent activity look like?">Ask about this card</AskButton>
          <CardSearch initial={card} />
        </div>
      </div>
      {!net && <section className="card"><p className="empty"><b>The network is unavailable</b>graph-svc is not running, so links can&apos;t be shown.</p></section>}
      {net && net.merchants.length === 0 && (
        <section className="card"><p className="empty"><b>No links in the current window</b>This card paid no merchants in the last 30 days of dataset time.</p></section>
      )}
      {net && net.merchants.length > 0 && (
        <>
          <div className="kpis">
            <div className="card kpi"><div className="label">Merchants paid</div><div className="value">{num(net.merchants.length)}</div><div className="sub">{num(linked.length)} specific enough to link</div></div>
            <div className="card kpi"><div className="label">Linked cards</div><div className="value">{num(net.cards.size)}</div><div className="sub">paid the same merchants{net.truncated ? " (first 300 nodes)" : ""}</div></div>
            <div className="card kpi"><div className="label"><span className="dot" style={{ background: "var(--high)" }} />Confirmed fraud nearby</div><div className="value">{num(fraudCards.length)}</div><div className="sub">linked cards with a released fraud label</div></div>
            <div className="card kpi"><div className="label">This card</div><div className="value" style={{ fontSize: "var(--t-xl)", marginTop: 14 }}>{net.centerFraud ? <span className="badge block">Confirmed fraud</span> : <span className="badge neutral">No confirmed fraud</span>}</div><div className="sub"><Link className="link" href={`/alerts?status=all&entity=${encodeURIComponent(`payer=${card}`)}`}>See its alerts</Link></div></div>
          </div>
          <div className="grid-2">
            <section className="card">
              <header>
                <div><h2>Link map</h2><p>Hover to trace links; click a card to centre on it</p></div>
                <div className="legend">
                  <span><span className="key" style={{ background: "var(--accent)", borderRadius: 5 }} />This card</span>
                  <span><span className="key" style={{ background: "var(--lowers)", transform: "rotate(45deg) scale(.8)" }} />Merchant</span>
                  <span><span className="key" style={{ background: "var(--text-2)", borderRadius: 5 }} />Other card</span>
                  <span><span className="key" style={{ background: "var(--high)", borderRadius: 5 }} />Confirmed fraud</span>
                  <span><span className="key" style={{ border: "1.5px solid var(--muted)", transform: "rotate(45deg) scale(.8)" }} />Too common to link</span>
                </div>
              </header>
              <NetworkView graph={graph!} />
            </section>
            <section className="card">
              <header><div><h2>Shared merchants</h2><p>Most confirmed fraud first</p></div></header>
              <table className="t">
                <thead><tr><th>Merchant</th><th className="num">Other cards</th><th className="num">Fraud</th></tr></thead>
                <tbody>
                  {net.merchants.slice(0, 25).map((m) => (
                    <tr key={m.id}>
                      <td>
                        <Link className="link mono" href={`/alerts?status=all&entity=${encodeURIComponent(`payee=${m.id}`)}`}>{merchantShort(m.id)}</Link>
                        {m.common && <div className="cell-sub">Used by 100+ cards; not linked through</div>}
                      </td>
                      <td className="num">{m.common ? "–" : num(m.cards.length)}</td>
                      <td className="num">{m.fraud > 0 ? <span className="badge block">{m.fraud}</span> : <span className="muted">0</span>}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
              {fraudCards.length > 0 && (
                <div className="body" style={{ borderTop: "1px solid var(--line)" }}>
                  <h3>Linked cards with confirmed fraud</h3>
                  {fraudCards.slice(0, 30).map(([c]) => <Link key={c} href={networkHref(c)} className="chip mono">{c}</Link>)}
                </div>
              )}
            </section>
          </div>
        </>
      )}
    </>
  );
}
