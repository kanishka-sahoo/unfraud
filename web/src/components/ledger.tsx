import type { Schemas } from "@/lib/api";
import { pct, sigmoid } from "@/lib/format";

type Contributions = NonNullable<Schemas["Contributions"]>;

/**
 * The risk ledger: SHAP contributions are additive in log-odds, so a score
 * reads like an account. It opens at the model's base rate, posts each
 * factor as raising or lowering risk, and closes at the case's probability.
 */
export function RiskLedger({ c, named }: { c: Contributions; named: Map<string, string> }) {
  const rows: (Contributions["top"][number] & { after: number })[] = [];
  for (let i = 0, running = c.bias; i < c.top.length; i++) {
    running += c.top[i].shap;
    rows.push({ ...c.top[i], after: sigmoid(running) });
  }
  const maxAbs = Math.max(...c.top.map((t) => Math.abs(t.shap)), Math.abs(c.other), 1e-9);
  const close = sigmoid(c.raw_score);

  return (
    <table className="ledger">
      <caption className="muted" style={{ textAlign: "left", captionSide: "bottom", paddingTop: 8, fontSize: "var(--t-xs)" }}>
        Effects are SHAP values in log-odds from model {c.model_version}. Red raises risk, blue lowers it; the running column shows the fraud probability after each line.
      </caption>
      <thead>
        <tr><th>Factor</th><th className="num">Value</th><th className="bar-cell">Effect on risk</th><th className="num">Effect</th><th className="num">Running</th></tr>
      </thead>
      <tbody>
        <tr className="opening">
          <td colSpan={3}>Base rate (the model&apos;s average case)</td><td className="num">{c.bias.toFixed(3)}</td><td className="num">{pct(sigmoid(c.bias), 2)}</td>
        </tr>
        {rows.map((r) => (
          <tr key={r.feature}>
            <td>
              <span className="feature">{r.feature}</span>
              {named.has(r.feature) ? <span className="desc">{named.get(r.feature)}</span> : <span className="masked"> masked feature</span>}
            </td>
            <td className="num">{formatValue(r.value)}</td>
            <td className="bar-cell"><Bar v={r.shap} max={maxAbs} /></td>
            <td className="num">{r.shap > 0 ? "+" : ""}{r.shap.toFixed(3)}</td>
            <td className="num">{pct(r.after, 2)}</td>
          </tr>
        ))}
        <tr>
          <td colSpan={2}>All other features</td>
          <td className="bar-cell"><Bar v={c.other} max={maxAbs} /></td>
          <td className="num">{c.other > 0 ? "+" : ""}{c.other.toFixed(3)}</td>
          <td className="num">{pct(close, 2)}</td>
        </tr>
        <tr className="balance">
          <td colSpan={3}>Fraud probability</td><td className="num">{c.raw_score.toFixed(3)}</td><td className="num">{pct(close, 2)}</td>
        </tr>
      </tbody>
    </table>
  );
}

function Bar({ v, max }: { v: number; max: number }) {
  const w = (Math.abs(v) / max) * 50;
  const left = v >= 0 ? 50 : 50 - w;
  return (
    <svg viewBox="0 0 100 10" preserveAspectRatio="none" height="10" width="100%" role="img" aria-label={`${v >= 0 ? "raises" : "lowers"} risk by ${Math.abs(v).toFixed(3)}`}>
      <line x1="50" x2="50" y1="0" y2="10" stroke="var(--rule)" strokeWidth="0.6" vectorEffect="non-scaling-stroke" />
      <rect x={left} y="1.5" width={Math.max(w, 0.4)} height="7" rx="1" fill={v >= 0 ? "var(--raises)" : "var(--lowers)"} />
    </svg>
  );
}

function formatValue(v: unknown): string {
  if (v === null || v === undefined) return "missing";
  if (typeof v === "number") return Math.abs(v) >= 100 || Number.isInteger(v) ? v.toLocaleString("en-US", { maximumFractionDigits: 2 }) : v.toFixed(4);
  return String(v);
}
