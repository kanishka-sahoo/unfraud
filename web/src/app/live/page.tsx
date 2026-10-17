import { LiveStream } from "@/components/live";

export const metadata = { title: "Live feed" };

export default function LivePage() {
  return (
    <>
      <div className="head">
        <div>
          <h1>Live feed</h1>
          <p>Decisions as the scorer and graph-svc make them. Latency runs from the payment entering Kafka to its decision.</p>
        </div>
      </div>
      <LiveStream />
    </>
  );
}
