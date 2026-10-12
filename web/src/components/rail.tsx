"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";

const perProfile = [
  { href: "", label: "Overview" },
  { href: "/live", label: "Live decisions" },
  { href: "/cases", label: "Cases" },
];
const shared = [
  { href: "/models", label: "Models" },
  { href: "/benchmarks", label: "Benchmarks" },
  { href: "/replay", label: "Replay" },
];

/** The rail in the prerendered shell, before the path is known. */
export function RailShell() {
  return (
    <aside className="rail">
      <Brand />
      <nav aria-label="Main">
        <span className="group">System</span>
        {shared.map((l) => <Link key={l.href} href={l.href}>{l.label}</Link>)}
      </nav>
    </aside>
  );
}

function Brand() {
  return (
    <div className="brand">
      unfraud
      <small>fraud detection console</small>
    </div>
  );
}

export function Rail({ profiles }: { profiles: string[] }) {
  const path = usePathname();
  const first = path.split("/")[1];
  const profile = profiles.includes(first) ? first : profiles[0];
  const current = (href: string) => {
    if (href === `/${profile}`) return path === href;
    return path === href || path.startsWith(href + "/");
  };
  return (
    <aside className="rail">
      <Brand />
      {profiles.length > 0 && (
        <div className="profiles" aria-label="Dataset">
          {profiles.map((p) => (
            <Link key={p} href={`/${p}`} aria-current={p === profile ? "true" : undefined}>
              {p}
            </Link>
          ))}
        </div>
      )}
      <nav aria-label="Main">
        {profile &&
          perProfile.map((l) => (
            <Link key={l.href} href={`/${profile}${l.href}`} aria-current={current(`/${profile}${l.href}`) ? "page" : undefined}>
              {l.label}
            </Link>
          ))}
        <span className="group">System</span>
        {shared.map((l) => (
          <Link key={l.href} href={l.href} aria-current={current(l.href) ? "page" : undefined}>
            {l.label}
          </Link>
        ))}
      </nav>
    </aside>
  );
}
