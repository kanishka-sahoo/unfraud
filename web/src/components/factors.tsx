import type { Schemas } from "@/lib/api";
import { factValue, pct, sigmoid, type Fact } from "@/lib/format";

type Contributions = NonNullable<Schemas["Contributions"]>;

/** Plain-language name of a feature: its description, or the feature itself. */
const nameOf = (feature: string, facts: Map<string, Fact>) => {
  const d = facts.get(feature)?.description ?? feature.replaceAll("_", " ");
  return d.charAt(0).toUpperCase() + d.slice(1);
};

/** The strongest reasons behind the score, largest first, in words. */
export function Factors({ c, facts }: { c: Contributions; facts: Map<string, Fact> }) {
  const max = Math.max(...c.top.map((t) => Math.abs(t.shap)), 1e-9);
  return (
    <ul className="factors">
      {c.top.map((t) => {
        const f = facts.get(t.feature) ?? { feature: t.feature, description: t.feature, value: t.value };
        const up = t.shap >= 0;
        return (
          <li key={t.feature}>
            <div className="what">
              {nameOf(t.feature, facts)}
              <small>
                <span className="val">{factValue({ ...f, value: t.value })}</span>
                {f.meaning && f.meaning !== String(t.value) && <> · code {String(t.value)}</>}
                {" · "}<span className="mono">{t.feature}</span>
              </small>
            </div>
            <svg viewBox="0 0 100 8" preserveAspectRatio="none" height="8" width="100%" aria-hidden="true">
              <rect x="0" y="0" width="100" height="8" rx="4" fill="var(--surface-2)" />
              <rect x="0" y="0" width={Math.max(2, (Math.abs(t.shap) / max) * 100)} height="8" rx="4" fill={up ? "var(--raises)" : "var(--lowers)"} />
            </svg>
            <span className={`dir ${up ? "up" : "down"}`}>{up ? "Raises" : "Lowers"}</span>
          </li>
        );
      })}
    </ul>
  );
}

/**
 * The full score breakdown: contributions are additive in log-odds, so the
 * score reads like an account from the model's base rate to the case's
 * probability.
 */
export function Ledger({ c, facts }: { c: Contributions; facts: Map<string, Fact> }) {
  const after = c.top.map((_, i) => c.bias + c.top.slice(0, i + 1).reduce((s, t) => s + t.shap, 0));
  return (
    <div className="table-wrap">
      <table className="ledger">
        <caption>
          Effects are SHAP values in calibrated log-odds from model {c.model_version}; the running column is the fraud probability after each line.
        </caption>
        <thead>
          <tr><th>Factor</th><th className="num">Effect</th><th className="num">Running</th></tr>
        </thead>
        <tbody>
          <tr className="opening"><td>Base rate (the model&apos;s average payment)</td><td className="num">{c.bias.toFixed(3)}</td><td className="num">{pct(sigmoid(c.bias), 2)}</td></tr>
          {c.top.map((t, i) => (
            <tr key={t.feature}>
              <td>{nameOf(t.feature, facts)}</td>
              <td className="num">{t.shap > 0 ? "+" : ""}{t.shap.toFixed(3)}</td>
              <td className="num">{pct(sigmoid(after[i]), 2)}</td>
            </tr>
          ))}
          <tr><td>All other factors</td><td className="num">{c.other > 0 ? "+" : ""}{c.other.toFixed(3)}</td><td className="num">{pct(sigmoid(c.raw_score), 2)}</td></tr>
          <tr className="balance"><td>Fraud probability</td><td className="num">{c.raw_score.toFixed(3)}</td><td className="num">{pct(sigmoid(c.raw_score), 2)}</td></tr>
        </tbody>
      </table>
    </div>
  );
}
