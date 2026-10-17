import type { Metadata } from "next";
import Link from "next/link";
import { notFound } from "next/navigation";
import { Suspense } from "react";
import { Disposition } from "@/components/disposition";
import { Factors, Ledger } from "@/components/factors";
import { ActionBadge, RiskBadge, StageBadge, StatusBadge } from "@/components/marks";
import { SummaryCard } from "@/components/summary-card";
import { api, NotFound, type CaseDetail } from "@/lib/api";
import { byFeature, eventTime, factValue, headline, merchantShort, networkHref, pct, PROFILE, riskBand, type Fact } from "@/lib/format";

export async function generateMetadata({ params }: PageProps<"/alerts/[id]">): Promise<Metadata> {
  const { id } = await params;
  return { title: `Alert ${decodeURIComponent(id)}` };
}

export default function AlertPage({ params }: PageProps<"/alerts/[id]">) {
  return (
    <Suspense fallback={<p className="skeleton">Loading alert…</p>}>
      <AlertBody params={params} />
    </Suspense>
  );
}

/** Payment facts, then the card and cardholder behind it. */
const paymentFields = ["amount_usd", "mcc", "channel", "entry_mode", "merchant_state", "errors", "hour", "weekday"];
const cardFields = ["card_brand", "card_type", "card_has_chip", "credit_limit", "customer_age", "yearly_income", "fico_score"];
const fieldLabel: Record<string, string> = {
  amount_usd: "Amount", mcc: "Merchant category", channel: "Channel", entry_mode: "Entry mode", merchant_state: "Merchant location",
  errors: "Authorization errors", hour: "Hour of day", weekday: "Day of week", card_brand: "Card network", card_type: "Card type",
  card_has_chip: "Chip card", credit_limit: "Credit limit", customer_age: "Cardholder age", yearly_income: "Yearly income", fico_score: "FICO score",
};
const weekdays = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"];

function show(f: Fact | undefined, feature: string): string {
  if (!f) return "–";
  const v = f.value;
  if (feature === "weekday" && typeof v === "number") return weekdays[v] ?? String(v);
  if (feature === "card_has_chip" && typeof v === "number") return v ? "Yes" : "No";
  if (feature === "errors" && (v === null || v === undefined)) return "None";
  if (feature === "merchant_state" && (v === null || v === undefined)) return "Online (no location)";
  if (feature === "hour" && typeof v === "number") return `${String(v).padStart(2, "0")}:00`;
  return factValue(f);
}

async function AlertBody({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  const eventId = decodeURIComponent(id);
  let d: CaseDetail;
  try {
    d = await api<CaseDetail>(`/api/cases/${PROFILE}/${encodeURIComponent(eventId)}`);
  } catch (e) {
    if (e instanceof NotFound) notFound();
    throw e;
  }
  const c = d.case;
  const facts = byFeature(d.facts);
  const h = headline(facts);
  const band = riskBand(c.probability);
  const card = c.entities.payer;
  const path = `${PROFILE}/${encodeURIComponent(eventId)}`;
  const entityAlerts = (type: string, v: string) => `/alerts?status=all&entity=${encodeURIComponent(`${type}=${v}`)}`;

  return (
    <>
      <div className="crumbs"><Link href="/alerts">Alerts</Link> / <span className="mono">#{eventId}</span></div>
      <div className="case-top">
        <div>
          <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
            <ActionBadge action={c.action} />
            <RiskBadge p={c.probability} />
            <StatusBadge disposition={c.disposition} />
          </div>
          <h1>{h.amount} <span>at {h.merchant}</span></h1>
          <div className="meta">
            {card && <span>Card <span className="mono">{card}</span></span>}
            {facts.has("channel") && <span>{show(facts.get("channel"), "channel")}</span>}
            {facts.get("merchant_state")?.value != null && <span>{show(facts.get("merchant_state"), "merchant_state")}</span>}
            <span>{eventTime(c.event_time)} (dataset time)</span>
            <span>Flagged by <StageBadge stage={c.flagged_by} /></span>
          </div>
        </div>
        <div className="card score">
          <div className={`v ${band}`}>{pct(c.probability, 1)}</div>
          <small>fraud probability · model {c.model_version}</small>
        </div>
      </div>

      <div className="grid-2">
        <div className="stack">
          <SummaryCard path={path} summary={c.summary} status={c.summary_status} />

          <section className="card">
            <header><div><h2>Why it was flagged</h2><p>The factors that moved the score most, strongest first</p></div></header>
            {c.reason_codes.length > 0 && (
              <div className="body" style={{ borderBottom: "1px solid var(--line)" }}>
                <h3>Rules that fired</h3>
                {c.reason_codes.map((r) => <span className="chip mono" key={r}>{r}</span>)}
              </div>
            )}
            {c.contributions ? (
              <>
                <Factors c={c.contributions} facts={facts} />
                <details className="more">
                  <summary>Show the full score breakdown</summary>
                  <Ledger c={c.contributions} facts={facts} />
                </details>
              </>
            ) : (
              <p className="empty">The score breakdown is still being computed. Refresh in a moment.</p>
            )}
          </section>

          <section className="card">
            <header><div><h2>Payment details</h2><p>As the engine saw them when it scored the payment</p></div></header>
            <div className="body stack">
              <dl className="kv">
                {paymentFields.filter((f) => facts.has(f)).map((f) => (
                  <div key={f}>
                    <dt>{fieldLabel[f]}</dt>
                    <dd>{show(facts.get(f), f)}{f === "mcc" && <small>MCC {String(facts.get(f)?.value)}</small>}</dd>
                  </div>
                ))}
                <div><dt>Merchant</dt><dd className="mono">{c.entities.payee ? merchantShort(c.entities.payee) : "–"}</dd></div>
              </dl>
              <h3 style={{ margin: "8px 0 0" }}>Card and cardholder</h3>
              <dl className="kv">
                {cardFields.filter((f) => facts.has(f)).map((f) => (
                  <div key={f}><dt>{fieldLabel[f]}</dt><dd>{show(facts.get(f), f)}</dd></div>
                ))}
              </dl>
              {d.facts.length === 0 && <p className="muted" style={{ margin: 0 }}>No payment details were stored for this alert.</p>}
            </div>
          </section>
        </div>

        <div className="stack">
          <section className="card">
            <header><div><h2>Your decision</h2></div></header>
            <div className="body">
              <Disposition path={path} current={c.disposition} note={c.disposition_note} />
            </div>
          </section>

          <section className="card">
            <header><div><h2>Investigate</h2></div></header>
            <div className="body">
              <dl className="list">
                {card && (<><dt>Card</dt><dd><span className="mono">{card}</span> · <Link className="link" href={entityAlerts("payer", card)}>alerts</Link> · <Link className="link" href={networkHref(card)}>network</Link></dd></>)}
                {c.entities.customer && (<><dt>Cardholder</dt><dd><span className="mono">{c.entities.customer}</span> · <Link className="link" href={entityAlerts("customer", c.entities.customer)}>alerts</Link></dd></>)}
                {c.entities.payee && (<><dt>Merchant</dt><dd><span className="mono">{merchantShort(c.entities.payee)}</span> · <Link className="link" href={entityAlerts("payee", c.entities.payee)}>alerts</Link></dd></>)}
              </dl>
            </div>
          </section>

          <section className="card">
            <header><div><h2>Decision history</h2></div></header>
            <div className="body">
              <ol className="timeline">
                {d.decisions.map((x) => (
                  <li key={x.stage} className={x.stage === 2 ? "revised" : "provisional"}>
                    <span className="node" aria-hidden="true" />
                    <div>
                      <div className="when">{x.stage === 2 ? "Graph revision" : "On arrival"} · {new Date(x.scored_at).toISOString().slice(11, 19)} UTC</div>
                      <div style={{ display: "flex", gap: 8, alignItems: "center", margin: "4px 0" }}><ActionBadge action={x.action} /> <span className="dim">{pct(x.probability, 1)}</span></div>
                      <div className="when">
                        model {x.model_version}
                        {x.rule_action > x.model_action && " · escalated by rules"}
                      </div>
                    </div>
                  </li>
                ))}
              </ol>
            </div>
          </section>
        </div>
      </div>
    </>
  );
}
