"use client";

import { useRouter } from "next/navigation";
import { useEffect, useState, useTransition } from "react";
import { AskButton } from "@/components/assistant";
import type { components } from "@/lib/schema";

type Summary = components["schemas"]["Summary"];

const waiting: Record<string, string> = {
  none: "No summary yet. Write one to get the story of this alert, what the card's history, the merchant and the network add, and what to check next.",
  pending: "Queued for writing…",
  running: "Being written now…",
  failed: "The summary did not pass the grounding check twice, so it is hidden.",
  skipped: "No summary: the alert had no score breakdown when it was queued.",
};

/** The alert's written summary, and a way to ask for a fresh one. */
export function SummaryCard({ path, summary, status }: { path: string; summary: Summary; status: string }) {
  const router = useRouter();
  const [pending, start] = useTransition();
  const [error, setError] = useState("");
  const inFlight = status === "pending" || status === "running";

  // Poll while a summary is being written, for up to two minutes.
  useEffect(() => {
    if (!inFlight) return;
    const t = setInterval(() => router.refresh(), 2500);
    const stop = setTimeout(() => clearInterval(t), 120_000);
    return () => { clearInterval(t); clearTimeout(stop); };
  }, [inFlight, router]);

  function rewrite() {
    setError("");
    start(async () => {
      const res = await fetch(`/api/cases/${path}/summary`, { method: "POST" });
      if (!res.ok) setError(`Not queued: ${(await res.text()).trim() || res.statusText}`);
      else router.refresh();
    });
  }

  const m = summary?.meta;
  const llm = m && m.driver !== "stub";
  const done = status === "done" && summary?.summary;
  return (
    <section className="card ai">
      <header>
        <div>
          <h2>Summary</h2>
          <p>{done ? (llm ? `Written by ${m?.model ?? "an LLM"}` : "Written from a template (no LLM)") : "Written when you ask for it"}</p>
        </div>
        <div className="row-actions">
          <AskButton>Ask the assistant</AskButton>
          <button className={done ? "ghost" : "primary"} onClick={rewrite} disabled={pending || inFlight}>
            {inFlight ? "Writing…" : done ? "Rewrite" : "Write summary"}
          </button>
        </div>
      </header>
      <div className="body">
        {done ? (
          <>
            <p className="text">{summary!.summary}</p>
            {summary!.key_points && summary!.key_points.length > 0 && <ul>{summary!.key_points.map((k, i) => <li key={i}>{k}</li>)}</ul>}
            {summary!.context && summary!.context.length > 0 && (
              <><h3>Wider context</h3><ul>{summary!.context.map((k, i) => <li key={i}>{k}</li>)}</ul></>
            )}
            {summary!.checks && summary!.checks.length > 0 && (
              <><h3>What to check next</h3><ul className="checks">{summary!.checks.map((k, i) => <li key={i}>{k}</li>)}</ul></>
            )}
            <div className="foot">
              <span className="check">
                <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true"><path d="M2 6.5 L5 9.5 L10 3" fill="none" stroke="var(--low)" strokeWidth="2" strokeLinecap="round" /></svg>
                Every number and identifier checked against the alert record
              </span>
              <span>
                {[m?.latency_ms ? `${(m.latency_ms / 1000).toFixed(1)} s` : "", m?.cost_usd ? `$${m.cost_usd.toFixed(4)}` : "",
                  summary!.attempts && summary!.attempts > 1 ? "second attempt" : ""].filter(Boolean).join(" · ")}
              </span>
            </div>
          </>
        ) : (
          <p className="dim" style={{ margin: 0 }}>{waiting[status] ?? "No summary yet."}</p>
        )}
        {error && <p className="error" role="alert">{error}</p>}
      </div>
    </section>
  );
}
