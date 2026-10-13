import type { Metadata } from "next";
import Link from "next/link";
import { notFound } from "next/navigation";
import { Suspense } from "react";
import { Disposition } from "@/components/disposition";
import { RiskLedger } from "@/components/ledger";
import { ActionMark, StageMark } from "@/components/marks";
import { api, NotFound, type CaseDetail, type Schemas } from "@/lib/api";
import { eventTime, pct } from "@/lib/format";

/** The Entity type each Profile's graph is built on. */
const graphNode: Record<string, string> = { tabformer: "payer", amlworld: "payer" };

export async function generateMetadata({ params }: PageProps<"/[profile]/cases/[id]">): Promise<Metadata> {
  const { id } = await params;
  return { title: `Case ${decodeURIComponent(id)}` };
}

export default function CasePage({ params }: PageProps<"/[profile]/cases/[id]">) {
  return (
    <Suspense fallback={<p className="skeleton">Loading case…</p>}>
      <CaseBody params={params} />
    </Suspense>
  );
}

async function CaseBody({ params }: { params: Promise<{ profile: string; id: string }> }) {
  const { profile, id } = await params;
  const eventId = decodeURIComponent(id);
  let d: CaseDetail;
  try {
    d = await api<CaseDetail>(`/api/cases/${encodeURIComponent(profile)}/${encodeURIComponent(eventId)}`);
  } catch (e) {
    if (e instanceof NotFound) notFound();
    throw e;
  }
  const c = d.case;
  const summary = c.summary;
  const named = new Map((summary?.packet?.model_factors ?? []).map((f) => [f.feature, f.description]));
  const node = c.entities[graphNode[profile] ?? ""];

  return (
    <>
      <p className="muted"><Link href={`/${profile}/cases`}>Cases</Link> / {eventId}</p>
      <div className="case-head">
        <div>
          <h1 style={{ fontSize: "var(--t-2xl)", margin: "0 0 8px", letterSpacing: "-0.02em" }}><ActionMark action={c.action} /></h1>
          <div className="muted">
            Event {eventId} at {eventTime(c.event_time)} (dataset time) · flagged by <StageMark stage={c.flagged_by} />
          </div>
          {c.reason_codes.length > 0 && <div style={{ marginTop: 10 }}>{c.reason_codes.map((r) => <span className="chip" key={r}>{r}</span>)}</div>}
        </div>
        <div className="prob">{pct(c.probability, 1)}<small>fraud probability</small></div>
      </div>

      <div className="case-grid">
        <div>
          <h2>Analyst summary</h2>
          <Memo summary={summary} status={c.summary_status} />
          <h2 className="section">Why the model scored it this way</h2>
          {c.contributions ? (
            <RiskLedger c={c.contributions} named={named} />
          ) : (
            <p className="empty">Contributions are being computed. Refresh in a moment.</p>
          )}
        </div>
        <div className="side">
          <h2>Decisions</h2>
          <ol className="timeline">
            {d.decisions.map((x) => (
              <li key={x.stage} className={x.stage === 2 ? "revised" : "provisional"}>
                <span className="node" aria-hidden="true" />
                <div>
                  <div className="muted" style={{ fontSize: "var(--t-sm)" }}>{x.stage === 2 ? "Graph revision" : "On arrival"}</div>
                  <div><ActionMark action={x.action} /> at {pct(x.probability, 1)}</div>
                  <div className="muted" style={{ fontSize: "var(--t-xs)" }}>
                    model {x.model_version}
                    {x.rule_action > x.model_action && " · escalated by rules"}
                    {" · "}scored {new Date(x.scored_at).toISOString().slice(11, 19)} UTC
                  </div>
                </div>
              </li>
            ))}
          </ol>
          <h2>Entities</h2>
          <dl className="facts">
            {Object.entries(c.entities).map(([k, v]) => (
              <Entity key={k} profile={profile} type={k} id={v} />
            ))}
          </dl>
          {node && <p><Link href={`/${profile}/graph/${encodeURIComponent(node)}`}>Explore its graph</Link></p>}
          <h2>Your decision</h2>
          <Disposition path={`${encodeURIComponent(profile)}/${encodeURIComponent(eventId)}`} current={c.disposition} note={c.disposition_note} />
        </div>
      </div>
    </>
  );
}

function Entity({ profile, type, id }: { profile: string; type: string; id: string }) {
  return (
    <>
      <dt>{type}</dt>
      <dd><Link href={`/${profile}/cases?status=all&entity=${encodeURIComponent(`${type}=${id}`)}`}>{id}</Link></dd>
    </>
  );
}

function Memo({ summary, status }: { summary: Schemas["Summary"]; status: string }) {
  if (status !== "done" || !summary?.summary) {
    const why: Record<string, string> = {
      pending: "Waiting in the summary queue.",
      running: "Being written now.",
      failed: "The summary did not pass the grounding check twice, so it is not shown.",
      skipped: "No summary: the case had no contributions when it was queued.",
    };
    return <p className="empty">{why[status] ?? "No summary."}</p>;
  }
  const m = summary.meta;
  return (
    <div>
      <div className="memo">
        <p>{summary.summary}</p>
        {summary.key_points && summary.key_points.length > 0 && (
          <ul>{summary.key_points.map((k, i) => <li key={i}>{k}</li>)}</ul>
        )}
      </div>
      <p className="memo-meta">
        <span className="grounded">
          <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true"><path d="M2 6.5 L5 9.5 L10 3" fill="none" stroke="var(--approve)" strokeWidth="2" strokeLinecap="round" /></svg>
          Every number and identifier checked against the case record
        </span>
        <br />
        Written by {m?.driver === "stub" ? "the template writer (no LLM)" : `${m?.model} via ${m?.provider || "OpenRouter"}`}
        {m?.latency_ms ? `, ${m.latency_ms} ms` : ""}
        {m?.cost_usd ? `, $${m.cost_usd.toFixed(5)}` : ""}
        {summary.attempts && summary.attempts > 1 ? ", on the second attempt" : ""}
      </p>
    </div>
  );
}
