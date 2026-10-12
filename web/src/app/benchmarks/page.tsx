import { marked } from "marked";
import { Suspense } from "react";
import { LineChart, type Series } from "@/components/line-chart";
import { api, type Benchmark } from "@/lib/api";

export const metadata = { title: "Benchmarks" };

const order = ["ieee-cis-lightgbm", "elliptic-lightgbm", "summaries-stub"];

export default function BenchmarksPage() {
  return (
    <>
      <div className="page-head">
        <div>
          <h1>Benchmarks</h1>
          <p>Every number here is regenerated from public datasets by the commands in the repository; nothing is typed by hand.</p>
        </div>
      </div>
      <Suspense fallback={<p className="skeleton">Loading results…</p>}>
        <Results />
      </Suspense>
    </>
  );
}

async function Results() {
  const all = await api<Benchmark[]>("/api/benchmarks");
  all.sort((a, b) => (order.indexOf(a.name) + 99) % 99 - (order.indexOf(b.name) + 99) % 99);
  return (
    <>
      {all.map((b) => (
        <section key={b.name} className="section md" id={b.name}>
          {b.name === "elliptic-lightgbm" && <EllipticF1 data={b.data} />}
          <div dangerouslySetInnerHTML={{ __html: marked.parse(b.markdown, { async: false }) }} />
        </section>
      ))}
    </>
  );
}

type PerGroup = Record<string, { f1: number }>;
const models: [string, string, string][] = [
  ["local", "Provisional (local)", "var(--series-1)"],
  ["flow", "Revised (flow graph)", "var(--series-2)"],
  ["dataset_agg", "Dataset aggregates", "var(--series-3)"],
];

function EllipticF1({ data }: { data: unknown }) {
  const runs = data as Record<string, { test_f1?: { per_group_at_val_best?: PerGroup } }> | null;
  if (!runs) return null;
  const series: Series[] = models
    .filter(([k]) => runs[k]?.test_f1?.per_group_at_val_best)
    .map(([k, name, color]) => ({
      name, color,
      points: Object.entries(runs[k].test_f1!.per_group_at_val_best!).map(([ts, v]) => ({ x: Number(ts), y: v.f1 })).sort((a, b) => a.x - b.x),
    }));
  return (
    <>
      <h2>Elliptic illicit F1 by test timestep</h2>
      <p>The collapse from timestep 43 is the dark-market shutdown Weber et al. documented; no model recovers from it.</p>
      <LineChart series={series} xLabel="timestep" label="Illicit F1 per test timestep for three Elliptic models" />
    </>
  );
}
