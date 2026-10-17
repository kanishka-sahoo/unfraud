import { Suspense } from "react";
import { ReplayForm } from "@/components/replay-form";
import { api, type Replay } from "@/lib/api";

export const metadata = { title: "Replay" };

export default function ReplayPage() {
  return (
    <>
      <div className="head">
        <div>
          <h1>Replay data</h1>
          <p>Streams the TabFormer card payments into the engine in dataset-time order. Fraud labels are released 30 days after each payment, as chargebacks would be.</p>
        </div>
      </div>
      <section className="card" style={{ marginBottom: 16 }}>
        <header><div><h2>Start a replay</h2></div></header>
        <div className="body"><ReplayForm /></div>
      </section>
      <section className="card">
        <header><div><h2>Recent replays</h2></div></header>
        <Suspense fallback={<p className="skeleton body">Loading replays…</p>}>
          <Recent />
        </Suspense>
      </section>
    </>
  );
}

async function Recent() {
  const rs = await api<Replay[]>("/api/replays");
  if (rs.length === 0) return <p className="empty"><b>No replays in the last day</b></p>;
  return (
    <table className="t">
      <thead><tr><th>Job</th><th className="num">Speed</th><th>Status</th><th>Started</th><th>Finished</th></tr></thead>
      <tbody>
        {rs.map((r) => (
          <tr key={r.name}>
            <td className="mono">{r.name}</td>
            <td className="num">{r.speed === 0 ? "max" : `${r.speed}×`}</td>
            <td><span className={`badge ${r.status === "failed" ? "block" : r.status === "running" ? "info" : "approve"}`}>{r.status}</span></td>
            <td>{new Date(r.started_at).toISOString().slice(0, 19).replace("T", " ")}</td>
            <td>{r.ended_at ? new Date(r.ended_at).toISOString().slice(0, 19).replace("T", " ") : "–"}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
