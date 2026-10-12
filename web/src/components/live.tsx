"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import { ActionMark, StageMark } from "./marks";
import { caseHref, eventTime, pct } from "@/lib/format";
import type { components } from "@/lib/schema";

type Live = components["schemas"]["LiveDecision"];

/** The newest Decisions from both Stages, as they are made. */
export function LiveStream({ profile }: { profile: string }) {
  const [rows, setRows] = useState<(Live & { key: number })[]>([]);
  const [open, setOpen] = useState(false);
  const [skipped, setSkipped] = useState(0);

  useEffect(() => {
    const es = new EventSource(`/api/stream/${encodeURIComponent(profile)}`);
    let n = 0;
    es.onopen = () => setOpen(true);
    es.onerror = () => setOpen(false);
    es.onmessage = (m) => {
      const d = JSON.parse(m.data) as Live;
      setSkipped((s) => s + d.skipped);
      setRows((r) => [{ ...d, key: n++ }, ...r].slice(0, 60));
    };
    return () => es.close();
  }, [profile]);

  return (
    <>
      <p className={`stream-live ${open ? "" : "off"}`} role="status">
        <span className="pulse" aria-hidden="true" />
        {open ? "Connected. Showing up to 20 decisions a second" : "Connecting to the decision stream…"}
        {skipped > 0 && `; ${skipped.toLocaleString("en-US")} more were made than shown`}
      </p>
      {rows.length === 0 ? (
        <p className="empty">Waiting for decisions. Start a replay to stream a dataset through the engine.</p>
      ) : (
        <table className="rows">
          <thead>
            <tr><th>Event</th><th>Stage</th><th>Decision</th><th className="num">Fraud probability</th><th>Rule reasons</th><th>Event time</th><th className="num">Scoring latency</th></tr>
          </thead>
          <tbody>
            {rows.map((r) => (
              <tr key={r.key}>
                <td>{r.action > 0 ? <Link className="row-link id" href={caseHref(profile, r.event_id)}>{r.event_id}</Link> : <span className="id">{r.event_id}</span>}</td>
                <td><StageMark stage={r.stage} /></td>
                <td><ActionMark action={r.action} short /></td>
                <td className="num">{pct(r.probability, 1)}</td>
                <td>{(r.reason_codes ?? []).map((c) => <span className="chip" key={c}>{c}</span>)}</td>
                <td>{eventTime(r.event_time)}</td>
                <td className="num">{r.latency_ms.toLocaleString("en-US")} ms</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </>
  );
}
