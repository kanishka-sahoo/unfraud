import type { Metadata } from "next";
import { Geist, Geist_Mono } from "next/font/google";
import { Suspense } from "react";
import { AssistantShell } from "@/components/assistant";
import { Sidebar, SidebarView } from "@/components/sidebar";
import "./globals.css";

const ui = Geist({ subsets: ["latin"], variable: "--font-geist" });
const mono = Geist_Mono({ subsets: ["latin"], variable: "--font-geist-mono" });

export const metadata: Metadata = {
  title: { default: "unfraud", template: "%s · unfraud" },
  description: "Real-time card fraud operations console",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en" className={`${ui.variable} ${mono.variable}`}>
      <body>
        <AssistantShell sidebar={<Suspense fallback={<SidebarView path="" />}><Sidebar /></Suspense>}>
          {children}
        </AssistantShell>
      </body>
    </html>
  );
}
