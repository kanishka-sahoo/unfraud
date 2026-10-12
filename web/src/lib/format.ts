// Shared display helpers (safe for server and client components).

export const actions = ["approve", "review", "block"] as const;
export type ActionName = (typeof actions)[number];

/** Past-tense labels: a Decision has already been made. */
export const actionLabel: Record<number, string> = { 0: "Approved", 1: "Sent to review", 2: "Blocked" };
export const stageLabel: Record<number, string> = { 1: "On arrival", 2: "Graph revision" };

export const profileLabel: Record<string, string> = {
  "ieee-cis": "IEEE-CIS card payments",
  elliptic: "Elliptic Bitcoin",
  paysim: "PaySim mobile money",
};

/** A percentage that never rounds up to certainty or down to impossibility. */
export function pct(p: number, digits = 1): string {
  const step = 10 ** -(digits + 2);
  if (p >= 1 - step / 2 && p < 1) return `>${(100 - step * 100).toFixed(digits)}%`;
  if (p > 0 && p < step / 2) return `<${(step * 100).toFixed(digits)}%`;
  return `${(p * 100).toFixed(digits)}%`;
}

export function int(n: number): string {
  return n.toLocaleString("en-US");
}

/** Locale-pinned so server and browser render identical text. */
export const num = (n: number) => n.toLocaleString("en-US");

/** Event Times are reconstructed from datasets, so only the date and time. */
export function eventTime(iso: string): string {
  return new Date(iso).toISOString().replace("T", " ").slice(0, 16);
}

export function ago(iso: string, now = Date.now()): string {
  const s = Math.max(0, Math.round((now - new Date(iso).getTime()) / 1000));
  if (s < 60) return `${s}s ago`;
  if (s < 3600) return `${Math.round(s / 60)}m ago`;
  if (s < 86400) return `${Math.round(s / 3600)}h ago`;
  return `${Math.round(s / 86400)}d ago`;
}

export const sigmoid = (x: number) => 1 / (1 + Math.exp(-x));

export function caseHref(profile: string, eventId: string): string {
  return `/${profile}/cases/${encodeURIComponent(eventId)}`;
}
