"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";

const icon = {
  dashboard: "M3 3h7v9H3zM14 3h7v5h-7zM14 12h7v9h-7zM3 16h7v5H3z",
  alerts: "M12 3l9 16H3zM12 10v4M12 17.5v.5",
  live: "M3 12h4l3-7 4 14 3-7h4",
  network: "M12 5a2 2 0 1 0 0-.01M5 19a2 2 0 1 0 0-.01M19 19a2 2 0 1 0 0-.01M12 7l-6 10M12 7l6 10M7 19h10",
  models: "M4 7l8-4 8 4-8 4zM4 12l8 4 8-4M4 17l8 4 8-4",
  bench: "M4 20V10M10 20V4M16 20v-7M22 20H2",
  replay: "M4 4v6h6M4.5 15a8 8 0 1 0 1.9-8.3L4 10",
};

const work = [
  { href: "/", label: "Dashboard", d: icon.dashboard },
  { href: "/alerts", label: "Alerts", d: icon.alerts },
  { href: "/live", label: "Live feed", d: icon.live },
  { href: "/network", label: "Network", d: icon.network },
];
const system = [
  { href: "/models", label: "Models", d: icon.models },
  { href: "/benchmarks", label: "Benchmarks", d: icon.bench },
  { href: "/replay", label: "Replay data", d: icon.replay },
];

function Item({ href, label, d, path }: { href: string; label: string; d: string; path: string }) {
  const current = href === "/" ? path === "/" : path === href || path.startsWith(href + "/");
  return (
    <Link href={href} aria-current={current ? "page" : undefined}>
      <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
        <path d={d} />
      </svg>
      {label}
    </Link>
  );
}

/** The sidebar for the current path. Render it inside Suspense, with SidebarView as the fallback. */
export function Sidebar() {
  return <SidebarView path={usePathname() ?? ""} />;
}

/** The sidebar itself; with no path (while prerendering) nothing is marked current. */
export function SidebarView({ path }: { path: string }) {
  return (
    <aside className="side">
      <div className="brand">
        <div className="logo" aria-hidden="true">u</div>
        <div>
          <b>unfraud</b>
          <small>Fraud operations</small>
        </div>
      </div>
      <nav className="nav" aria-label="Main">
        <span className="group">Investigate</span>
        {work.map((l) => <Item key={l.href} {...l} path={path} />)}
        <span className="group">System</span>
        {system.map((l) => <Item key={l.href} {...l} path={path} />)}
      </nav>
      <div className="foot">Card payments · IBM TabFormer dataset</div>
    </aside>
  );
}
