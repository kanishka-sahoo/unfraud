import { Suspense } from "react";
import { ReplayForm } from "@/components/replay-form";
import { api, type Replay } from "@/lib/api";

export const metadata = { title: "Replay" };

export default function ReplayPage() {
  return (
    <>
      <div className="page-head">
        <div>
          <h1>Replay a dataset</h1>
          <p>Streams a public dataset into the engine in Event Time order, with fraud labels released after the dataset&apos;s label delay.</p>
        </div>
      </div>
      <ReplayForm />
      <section className="section">
        <h2>Recent replays</h2>
        <Suspense fallback={<p className="skeleton">Loading replays…</p>}>
          <Recent />
        </Suspense>
      </section>
    </>
  );
}

async function Recent() {
  const rs = await api<Replay[]>("/api/replays");
  if (rs.length === 0) return <p className="empty">No replays in the last day.</p>;
  return (
    <table className="rows">
      <thead><tr><th>Job</th><th>Dataset</th><th className="num">Speed</th><th>Status</th><th>Started</th><th>Finished</th></tr></thead>
      <tbody>
        {rs.map((r) => (
          <tr key={r.name}>
            <td className="id">{r.name}</td>
            <td>{r.profile}</td>
            <td className="num">{r.speed === 0 ? "max" : `${r.speed}×`}</td>
            <td>{r.status}</td>
            <td>{new Date(r.started_at).toISOString().slice(0, 19).replace("T", " ")}</td>
            <td>{r.ended_at ? new Date(r.ended_at).toISOString().slice(0, 19).replace("T", " ") : "–"}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
