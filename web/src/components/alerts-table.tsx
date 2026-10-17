import Link from "next/link";
import { ActionBadge, Risk, StageBadge, StatusBadge } from "./marks";
import type { CaseListItem } from "@/lib/api";
import { byFeature, caseHref, eventTime, factValue, headline } from "@/lib/format";

/** The alert queue's rows: what was paid, where, by which card, and how risky. */
export function AlertsTable({ rows, compact = false }: { rows: CaseListItem[]; compact?: boolean }) {
  return (
    <div className="table-wrap">
      <table className="t">
        <thead>
          <tr>
            <th>Payment</th>
            <th>Card</th>
            <th>Fraud probability</th>
            <th>Decision</th>
            {!compact && <th>Flagged by</th>}
            <th>Time (dataset)</th>
            {!compact && <th>Status</th>}
          </tr>
        </thead>
        <tbody>
          {rows.map((c) => {
            const f = byFeature(c.payment);
            const h = headline(f);
            const mcc = f.get("mcc");
            return (
              <tr key={c.event_id} className="row">
                <td>
                  <Link href={caseHref(c.event_id)} className="stretch-link">
                    <div className="cell-main">{h.amount} <span className="dim">· {h.merchant}</span></div>
                  </Link>
                  <div className="cell-sub">
                    {[mcc?.value ? `MCC ${mcc.value}` : null, f.has("channel") ? factValue(f.get("channel")) : null, `#${c.event_id}`]
                      .filter(Boolean).join(" · ")}
                  </div>
                </td>
                <td className="mono nowrap">{c.entities.payer ?? "–"}</td>
                <td><Risk p={c.probability} /></td>
                <td><ActionBadge action={c.action} /></td>
                {!compact && <td><StageBadge stage={c.flagged_by} /></td>}
                <td className="dim nowrap">{eventTime(c.event_time)}</td>
                {!compact && <td><StatusBadge disposition={c.disposition} /></td>}
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
