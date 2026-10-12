import Link from "next/link";
import { redirect } from "next/navigation";
import { Suspense } from "react";
import { api } from "@/lib/api";

async function ToFirstProfile() {
  const profiles = await api<string[]>("/api/profiles");
  if (profiles.length > 0) redirect(`/${profiles[0]}`);
  return (
    <div className="empty">
      <h1>No cases yet</h1>
      <p>
        Nothing has been scored. Start a replay to stream a dataset through the engine.{" "}
        <Link href="/replay">Open replay</Link>
      </p>
    </div>
  );
}

export default function Home() {
  return (
    <Suspense fallback={<p className="skeleton">Loading…</p>}>
      <ToFirstProfile />
    </Suspense>
  );
}
