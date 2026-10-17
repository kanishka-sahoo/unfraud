import { marked } from "marked";
import { Suspense } from "react";
import { api, type Benchmark } from "@/lib/api";

export const metadata = { title: "Benchmarks" };

const order = ["tabformer-lightgbm", "summaries-stub"];

export default function BenchmarksPage() {
  return (
    <>
      <div className="head">
        <div>
          <h1>Benchmarks</h1>
          <p>Every number here is regenerated from a public dataset by the commands in the repository; nothing is typed by hand.</p>
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
        <section key={b.name} className="card md" id={b.name} style={{ marginBottom: 16 }}>
          <div className="body" dangerouslySetInnerHTML={{ __html: marked.parse(b.markdown, { async: false }) }} />
        </section>
      ))}
    </>
  );
}
