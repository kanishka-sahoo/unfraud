"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import { ActionBadge, Risk, StageBadge } from "./marks";
import { caseHref, eventTime, num, PROFILE } from "@/lib/format";
import type { components } from "@/lib/schema";

type Live = components["schemas"]["LiveDecision"];
type Filter = "all" | "flagged";

/** The newest Decisions from both Stages, as they are made. */
export function LiveStream() {
  const [rows, setRows] = useState<(Live & { key: number })[]>([]);
  const [open, setOpen] = useState(false);
  const [skipped, setSkipped] = useState(0);
  const [filter, setFilter] = useState<Filter>("all");
  const [paused, setPaused] = useState(false);

  useEffect(() => {
    if (paused) return;
    const es = new EventSource(`/api/stream/${PROFILE}`);
    let n = Date.now();
    es.onopen = () => setOpen(true);
    es.onerror = () => setOpen(false);
    es.onmessage = (m) => {
      const d = JSON.parse(m.data) as Live;
      setSkipped((s) => s + d.skipped);
      setRows((r) => [{ ...d, key: n++ }, ...r].slice(0, 100));
    };
    return () => { es.close(); setOpen(false); };
  }, [paused]);

  const shown = filter === "flagged" ? rows.filter((r) => r.action > 0) : rows;
  return (
    <section className="card">
      <div className="toolbar">
        <span className={`live-dot ${open ? "" : "off"}`} role="status">
          <i aria-hidden="true" />
          {paused ? "Paused" : open ? "Connected. Showing up to 20 decisions a second" : "Connecting to the decision stream…"}
          {skipped > 0 && <span className="muted">· {num(skipped)} more made than shown</span>}
        </span>
        <span className="filters">
          <nav className="tabs" aria-label="Show">
            <a href="#" onClick={(e) => { e.preventDefault(); setFilter("all"); }} aria-current={filter === "all" ? "true" : undefined}>All</a>
            <a href="#" onClick={(e) => { e.preventDefault(); setFilter("flagged"); }} aria-current={filter === "flagged" ? "true" : undefined}>Flagged only</a>
          </nav>
          <button onClick={() => setPaused((p) => !p)}>{paused ? "Resume" : "Pause"}</button>
        </span>
      </div>
      {shown.length === 0 ? (
        <p className="empty"><b>Waiting for decisions</b>Replay data to stream payments through the engine.</p>
      ) : (
        <div className="table-wrap">
          <table className="t">
            <thead>
              <tr><th>Payment</th><th>Stage</th><th>Decision</th><th>Fraud probability</th><th>Rules</th><th>Time (dataset)</th><th className="num">Latency</th></tr>
            </thead>
            <tbody>
              {shown.map((r, i) => (
                <tr key={r.key} className={i === 0 && !paused ? "fresh" : undefined}>
                  <td className="mono">{r.action > 0 ? <Link className="link" href={caseHref(r.event_id)}>#{r.event_id}</Link> : <>#{r.event_id}</>}</td>
                  <td><StageBadge stage={r.stage} /></td>
                  <td><ActionBadge action={r.action} /></td>
                  <td><Risk p={r.probability} /></td>
                  <td>{(r.reason_codes ?? []).map((c) => <span className="chip mono" key={c}>{c}</span>)}</td>
                  <td className="dim">{eventTime(r.event_time)}</td>
                  <td className="num dim">{num(r.latency_ms)} ms</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}
