import { actionLabel, stageLabel } from "@/lib/format";

/** A Decision's action: icon and word together, never colour alone. */
export function ActionMark({ action, short = false }: { action: number; short?: boolean }) {
  const cls = ["approve", "review", "block"][action] ?? "approve";
  const label = short ? ["Approve", "Review", "Block"][action] : actionLabel[action];
  return (
    <span className={`action ${cls}`}>
      <svg width="14" height="14" viewBox="0 0 14 14" aria-hidden="true">
        {action === 2 && (
          <>
            <circle cx="7" cy="7" r="6" fill="none" stroke="var(--block)" strokeWidth="2" />
            <line x1="3" y1="11" x2="11" y2="3" stroke="var(--block)" strokeWidth="2" />
          </>
        )}
        {action === 1 && <path d="M7 1.5 L13 12.5 H1 Z" fill="none" stroke="var(--review)" strokeWidth="2" strokeLinejoin="round" />}
        {action === 0 && <path d="M2.5 7.5 L5.5 10.5 L11.5 3.5" fill="none" stroke="var(--approve)" strokeWidth="2" strokeLinecap="round" />}
      </svg>
      {label}
    </span>
  );
}

/** Which scoring Stage made a Decision (ADR-0003). */
export function StageMark({ stage }: { stage: number }) {
  return (
    <span className={`stage ${stage === 2 ? "revised" : "provisional"}`}>
      <span className="dot" aria-hidden="true" />
      {stageLabel[stage] ?? `Stage ${stage}`}
    </span>
  );
}
