import type { Metadata } from "next";
import { Schibsted_Grotesk, Spectral } from "next/font/google";
import { Suspense } from "react";
import { Rail, RailShell } from "@/components/rail";
import { api } from "@/lib/api";
import "./globals.css";

const ui = Schibsted_Grotesk({ subsets: ["latin"], variable: "--font-schibsted" });
const memo = Spectral({ subsets: ["latin"], weight: ["400", "500"], style: ["normal", "italic"], variable: "--font-spectral" });

export const metadata: Metadata = {
  title: { default: "unfraud", template: "%s · unfraud" },
  description: "Real-time fraud detection console",
};

async function ProfiledRail() {
  const profiles = await api<string[]>("/api/profiles").catch(() => [] as string[]);
  return <Rail profiles={profiles} />;
}

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en" className={`${ui.variable} ${memo.variable}`}>
      <body>
        <div className="shell">
          <Suspense fallback={<RailShell />}>
            <ProfiledRail />
          </Suspense>
          <main className="main">{children}</main>
        </div>
      </body>
    </html>
  );
}
