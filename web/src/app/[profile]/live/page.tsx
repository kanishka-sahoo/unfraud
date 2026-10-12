import { Suspense } from "react";
import { LiveStream } from "@/components/live";

export const metadata = { title: "Live decisions" };

export default function LivePage({ params }: PageProps<"/[profile]/live">) {
  return (
    <>
      <div className="page-head">
        <div>
          <h1>Live decisions</h1>
          <p>Decisions as the scorer and graph-svc make them. Scoring latency runs from the Event entering Kafka to its Decision.</p>
        </div>
      </div>
      <Suspense fallback={<p className="skeleton">Connecting…</p>}>
        <Stream params={params} />
      </Suspense>
    </>
  );
}

async function Stream({ params }: { params: Promise<{ profile: string }> }) {
  const { profile } = await params;
  return <LiveStream profile={profile} />;
}
