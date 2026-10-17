import Link from "next/link";
import { cache, Suspense } from "react";
import { AlertsTable } from "@/components/alerts-table";
import { Throughput } from "@/components/throughput";
import { api, listCases, type Overview } from "@/lib/api";
import { compact, num, pct, PROFILE } from "@/lib/format";

export default function Dashboard() {
  return (
    <>
      <div className="head">
        <div>
          <h1>Dashboard</h1>
          <p>Every card payment is scored the moment it arrives, then re-scored once the payment graph around it is known.</p>
        </div>
        <Link href="/alerts" className="btn">Open alert queue</Link>
      </div>
      <Suspense fallback={<p className="skeleton">Loading figures…</p>}>
        <Figures />
      </Suspense>
      <div className="grid-2">
        <section className="card">
          <header>
            <div><h2>Highest-risk open alerts</h2><p>Strictest decision first, then fraud probability</p></div>
            <Link href="/alerts" className="link">View all</Link>
          </header>
          <Suspense fallback={<p className="skeleton body">Loading alerts…</p>}>
            <TopAlerts />
          </Suspense>
        </section>
        <Suspense fallback={<section className="card"><p className="skeleton body">Loading…</p></section>}>
          <Mix />
        </Suspense>
      </div>
    </>
  );
}

/** Shared by the figures and the mix within one request. */
const overview = cache(() => api<Overview>(`/api/overview/${PROFILE}`));

function counts(o: Overview) {
  const sum = (stage: number, action?: number) =>
    o.decisions.filter((d) => d.stage === stage && (action === undefined || d.action === action)).reduce((s, d) => s + d.count, 0);
  return { scored: sum(1), review: sum(1, 1), block: sum(1, 2), revised: sum(2), revisedFlagged: sum(2, 1) + sum(2, 2) };
}

async function Figures() {
  const o = await overview();
  const c = counts(o);
  const flagged = c.review + c.block;
  const reviewed = o.cases.confirmed + o.cases.dismissed + o.cases.escalated;
  const tiles = [
    { label: "Payments scored", value: compact(c.scored), sub: `${num(c.scored)} on arrival` },
    { label: "Flag rate", value: c.scored ? pct(flagged / c.scored, 2) : "–", sub: `${compact(flagged)} sent to review or blocked`, dot: "var(--elevated)" },
    { label: "Blocked", value: compact(c.block), sub: c.scored ? `${pct(c.block / c.scored, 2)} of payments` : "", dot: "var(--high)" },
    { label: "Open alerts", value: compact(o.cases.open), sub: `${num(o.cases.open_block)} of them blocked`, dot: "var(--accent)" },
    { label: "Analyst reviews", value: num(reviewed), sub: `${num(o.cases.confirmed)} confirmed fraud · ${num(o.cases.escalated)} escalated` },
  ];
  return (
    <>
      <div className="kpis">
        {tiles.map((t) => (
          <div className="card kpi" key={t.label}>
            <div className="label">{t.dot && <span className="dot" style={{ background: t.dot }} />}{t.label}</div>
            <div className="value">{t.value}</div>
            <div className="sub">{t.sub}</div>
          </div>
        ))}
      </div>
      <section className="card" style={{ marginBottom: 16 }}>
        <header>
          <div><h2>Decisions per minute</h2><p>Last hour, by when the engine scored them</p></div>
          <div className="legend">
            <span><span className="key" style={{ background: "var(--series-1)" }} />On arrival</span>
            <span><span className="key" style={{ background: "var(--series-2)" }} />Graph revision</span>
          </div>
        </header>
        <div className="body"><Throughput data={o.per_minute ?? []} /></div>
      </section>
    </>
  );
}

async function TopAlerts() {
  const rows = await listCases({ status: "open", sort: "score", limit: 8 });
  if (rows.length === 0) return <p className="empty"><b>No open alerts</b>Everything scored so far was approved or has been reviewed.</p>;
  return <AlertsTable rows={rows} compact />;
}

async function Mix() {
  const o = await overview();
  const c = counts(o);
  const approve = c.scored - c.review - c.block;
  const parts = [
    { label: "Approved", n: approve, color: "var(--low)" },
    { label: "Sent to review", n: c.review, color: "var(--elevated)" },
    { label: "Blocked", n: c.block, color: "var(--high)" },
  ];
  const summaries = Object.entries(o.summaries ?? {}).filter(([k]) => k !== "none");
  return (
    <section className="card">
      <header><div><h2>Decision mix</h2><p>On arrival, all payments so far</p></div></header>
      <div className="body">
        <div className="mix" role="img" aria-label={parts.map((p) => `${p.label} ${num(p.n)}`).join(", ")}>
          {parts.map((p) => <span key={p.label} style={{ width: `${c.scored ? Math.max(p.n ? 0.6 : 0, (p.n / c.scored) * 100) : 0}%`, background: p.color }} />)}
        </div>
        <div className="mix-rows">
          {parts.map((p) => (
            <div key={p.label}>
              <span><span className="key" style={{ display: "inline-block", width: 10, height: 10, borderRadius: 3, background: p.color, marginRight: 8 }} />{p.label}</span>
              <span className="dim">{num(p.n)} <span className="muted">({c.scored ? pct(p.n / c.scored, 2) : "–"})</span></span>
            </div>
          ))}
        </div>
        <h3 style={{ marginTop: 22 }}>After graph revision</h3>
        <p className="dim" style={{ margin: 0, fontSize: "var(--t-sm)" }}>
          {num(c.revised)} payments re-scored; {c.revised ? pct(c.revisedFlagged / c.revised, 2) : "–"} flagged.
        </p>
        <h3 style={{ marginTop: 22 }}>Alert summaries</h3>
        <p className="dim" style={{ margin: 0, fontSize: "var(--t-sm)" }}>
          {summaries.length === 0 ? "None requested yet." : summaries.map(([k, v]) => `${num(v)} ${k === "done" ? "written" : k}`).join(" · ")}
        </p>
      </div>
    </section>
  );
}
