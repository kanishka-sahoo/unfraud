import { Suspense } from "react";
import { StageMark } from "@/components/marks";
import { api, type ModelUse } from "@/lib/api";
import { int, pct } from "@/lib/format";

export const metadata = { title: "Models" };

export default function ModelsPage() {
  return (
    <>
      <div className="page-head">
        <div>
          <h1>Models in service</h1>
          <p>Each model ships as an image tagged with a hash of its file, and every Decision records that version. Scores are checked against Python LightGBM before a model ships.</p>
        </div>
      </div>
      <Suspense fallback={<p className="skeleton">Loading models…</p>}>
        <Uses />
      </Suspense>
    </>
  );
}

async function Uses() {
  const uses = await api<ModelUse[]>("/api/models");
  if (uses.length === 0) return <p className="empty">No Decisions yet, so no models have been used.</p>;
  return (
    <table className="rows">
      <thead>
        <tr><th>Dataset</th><th>Stage</th><th>Model version</th><th className="num">Decisions</th><th className="num">Flagged</th><th>First used</th><th>Last used</th></tr>
      </thead>
      <tbody>
        {uses.map((u) => (
          <tr key={`${u.profile}-${u.stage}-${u.model_version}`}>
            <td>{u.profile}</td>
            <td><StageMark stage={u.stage} /></td>
            <td>{u.model_version}</td>
            <td className="num">{int(u.decisions)}</td>
            <td className="num">{int(u.flagged)} ({pct(u.flagged / Math.max(1, u.decisions), 2)})</td>
            <td>{new Date(u.first_scored).toISOString().slice(0, 16).replace("T", " ")}</td>
            <td>{new Date(u.last_scored).toISOString().slice(0, 16).replace("T", " ")}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
