import Link from "next/link";
import { Fragment, type ReactNode } from "react";

/**
 * Renders an assistant answer: paragraphs, "- " lists and **bold**, with
 * alert and card IDs linked and numbers nothing looked up marked. Only this
 * small subset is rendered, so model output can't inject markup.
 */
export function Answer({ text, links, unverified }: { text: string; links: Record<string, string>; unverified: string[] }) {
  const blocks = text.split(/\n{2,}/).map((b) => b.trim()).filter(Boolean);
  return (
    <div className="answer">
      {blocks.map((b, i) => {
        const lines = b.split("\n");
        if (lines.every((l) => /^\s*[-*•] /.test(l))) {
          return <ul key={i}>{lines.map((l, j) => <li key={j}>{inline(l.replace(/^\s*[-*•] /, ""), links, unverified)}</li>)}</ul>;
        }
        // A paragraph followed by a list without a blank line between.
        const k = lines.findIndex((l) => /^\s*[-*•] /.test(l));
        if (k > 0 && lines.slice(k).every((l) => /^\s*[-*•] /.test(l))) {
          return (
            <Fragment key={i}>
              <p>{inline(lines.slice(0, k).join(" "), links, unverified)}</p>
              <ul>{lines.slice(k).map((l, j) => <li key={j}>{inline(l.replace(/^\s*[-*•] /, ""), links, unverified)}</li>)}</ul>
            </Fragment>
          );
        }
        return <p key={i}>{inline(lines.join(" "), links, unverified)}</p>;
      })}
    </div>
  );
}

const escape = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");

function inline(text: string, links: Record<string, string>, unverified: string[]): ReactNode[] {
  const out: ReactNode[] = [];
  text.split(/\*\*(.+?)\*\*/g).forEach((part, i) => {
    const nodes = tokens(part, links, unverified, i);
    out.push(i % 2 ? <strong key={`b${i}`}>{nodes}</strong> : <Fragment key={`t${i}`}>{nodes}</Fragment>);
  });
  return out;
}

function tokens(text: string, links: Record<string, string>, unverified: string[], k: number): ReactNode[] {
  const keys = [...new Set([...Object.keys(links), ...unverified])].filter((s) => s.length > 1).sort((a, b) => b.length - a.length);
  if (keys.length === 0) return [text];
  // IDs and numbers only match whole, not inside a longer number or word.
  const re = new RegExp(`(?<![\\w.,-])(${keys.map(escape).join("|")})(?![\\w-]|[.,]\\d)`, "g");
  const out: ReactNode[] = [];
  let last = 0;
  for (const m of text.matchAll(re)) {
    const tok = m[1];
    out.push(text.slice(last, m.index));
    if (unverified.includes(tok)) {
      out.push(
        <mark key={`${k}-${m.index}`} className="unverified" tabIndex={0}
          title="Not verified: this number or ID doesn't appear in anything the assistant looked up. Check it before relying on it.">
          {tok}
        </mark>,
      );
    } else {
      out.push(<Link key={`${k}-${m.index}`} className="link" href={links[tok]}>{tok}</Link>);
    }
    last = (m.index ?? 0) + tok.length;
  }
  out.push(text.slice(last));
  return out;
}
