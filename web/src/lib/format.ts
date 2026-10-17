// Shared display helpers (safe for server and client components).

/** The console serves one dataset (ADR-0019); the API still keys data by it. */
export const PROFILE = "tabformer";

/** Past-tense labels: a Decision has already been made. */
export const actionLabel: Record<number, string> = { 0: "Approved", 1: "In review", 2: "Blocked" };
export const stageLabel: Record<number, string> = { 1: "On arrival", 2: "Graph revision" };

export type Fact = { feature: string; description: string; value?: unknown; meaning?: string };

/** A percentage that never rounds up to certainty or down to impossibility. */
export function pct(p: number, digits = 1): string {
  const step = 10 ** -(digits + 2);
  if (p >= 1 - step / 2 && p < 1) return `>${(100 - step * 100).toFixed(digits)}%`;
  if (p > 0 && p < step / 2) return `<${(step * 100).toFixed(digits)}%`;
  return `${(p * 100).toFixed(digits)}%`;
}

/** Locale-pinned so server and browser render identical text. */
export const num = (n: number) => n.toLocaleString("en-US");

export function usd(n: number): string {
  return n.toLocaleString("en-US", { style: "currency", currency: "USD" });
}

/** Compact counts for tiles: 1.46M, 49.5K. */
export function compact(n: number): string {
  return n.toLocaleString("en-US", { notation: "compact", maximumFractionDigits: n >= 1e6 ? 2 : 1 });
}

/** Event Times are reconstructed from the dataset, so only the date and time. */
export function eventTime(iso: string): string {
  return new Date(iso).toISOString().replace("T", " ").slice(0, 16);
}

export const sigmoid = (x: number) => 1 / (1 + Math.exp(-x));

export const caseHref = (eventId: string) => `/alerts/${encodeURIComponent(eventId)}`;
export const networkHref = (card: string) => `/network/${encodeURIComponent(card)}`;

/** Risk bands for a fraud probability; colour always comes with the word. */
export function riskBand(p: number): "high" | "elevated" | "low" {
  if (p >= 0.5) return "high";
  if (p >= 0.05) return "elevated";
  return "low";
}

/** A fact's value as an analyst reads it: the meaning of a code, dollars for amounts. */
export function factValue(f: Fact | undefined): string {
  if (!f) return "–";
  const v = f.value;
  if (v === null || v === undefined) return "not recorded";
  if (f.meaning) return f.meaning;
  if (typeof v === "number") {
    if (/amount|limit|income/.test(f.feature) && !f.feature.includes("ratio")) return usd(v);
    return Number.isInteger(v) ? num(v) : v.toLocaleString("en-US", { maximumFractionDigits: 4 });
  }
  return String(v).replaceAll("_", " ");
}

export function byFeature(facts: Fact[] | undefined): Map<string, Fact> {
  return new Map((facts ?? []).map((f) => [f.feature, f]));
}

/** A short merchant name for hashed merchant ids. */
export const merchantShort = (id: string) => `Merchant …${id.replace("-", "").slice(-5)}`;

/** Payment headline: "$1,234.00 at Wholesale clubs". */
export function headline(facts: Map<string, Fact>): { amount: string; merchant: string } {
  const a = facts.get("amount_usd")?.value;
  const mcc = facts.get("mcc");
  return {
    amount: typeof a === "number" ? usd(a) : "Amount unknown",
    merchant: mcc?.meaning ?? (mcc?.value ? `MCC ${mcc.value}` : "Unknown merchant"),
  };
}
