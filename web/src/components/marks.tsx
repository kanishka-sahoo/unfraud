import { actionLabel, pct, riskBand, stageLabel } from "@/lib/format";

const cls = ["approve", "review", "block"];

/** A Decision's action: icon and word together, never colour alone. */
export function ActionBadge({ action }: { action: number }) {
  return (
    <span className={`badge ${cls[action] ?? "approve"}`}>
      <svg width="12" height="12" viewBox="0 0 14 14" aria-hidden="true" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
        {action === 2 && (<><circle cx="7" cy="7" r="5.5" /><line x1="3.2" y1="10.8" x2="10.8" y2="3.2" /></>)}
        {action === 1 && <path d="M7 1.8 L12.6 12 H1.4 Z M7 6v2.5" />}
        {action === 0 && <path d="M2.5 7.5 L5.5 10.5 L11.5 3.5" />}
      </svg>
      {actionLabel[action]}
    </span>
  );
}

/** Which scoring Stage made a Decision (ADR-0003). */
export function StageBadge({ stage }: { stage: number }) {
  return <span className={`badge ${stage === 2 ? "info" : "neutral"}`}>{stageLabel[stage] ?? `Stage ${stage}`}</span>;
}

/** Fraud probability as a labelled meter. */
export function Risk({ p }: { p: number }) {
  const band = riskBand(p);
  return (
    <span className="risk" title={`${band} risk`}>
      <span className="meter" aria-hidden="true"><span className={band} style={{ width: `${Math.max(3, p * 100)}%` }} /></span>
      <b>{pct(p, p < 0.1 ? 2 : 1)}</b>
    </span>
  );
}

export function RiskBadge({ p }: { p: number }) {
  const band = riskBand(p);
  return <span className={`badge ${band}`}>{band === "high" ? "High risk" : band === "elevated" ? "Elevated risk" : "Low risk"}</span>;
}

const dispositionLabel: Record<string, [string, string]> = {
  confirmed: ["Confirmed fraud", "block"],
  dismissed: ["Dismissed", "neutral"],
  escalated: ["Escalated", "review"],
};

export function StatusBadge({ disposition }: { disposition: string | null | undefined }) {
  if (!disposition) return <span className="badge info">Open</span>;
  const [label, c] = dispositionLabel[disposition] ?? [disposition, "neutral"];
  return <span className={`badge ${c}`}>{label}</span>;
}
