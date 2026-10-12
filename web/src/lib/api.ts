import "server-only";
import type { components } from "./schema";

export type Schemas = components["schemas"];
export type Overview = Schemas["Overview"];
export type CaseListItem = Schemas["CaseListItem"];
export type CaseDetail = Schemas["CaseDetail"];
export type Subgraph = Schemas["Subgraph"];
export type ModelUse = Schemas["ModelUse"];
export type Benchmark = Schemas["Benchmark"];
export type Replay = Schemas["Replay"];

/** cases-api inside the cluster; the browser goes through /api (route.ts). */
function base(): string {
  return process.env.CASES_API_URL ?? "http://localhost:8080";
}

export class NotFound extends Error {}

/** GET a cases-api path from a server component. Always fresh. */
export async function api<T>(path: string): Promise<T> {
  const res = await fetch(base() + path, { cache: "no-store" });
  if (res.status === 404) throw new NotFound(path);
  if (!res.ok) throw new Error(`cases-api ${path}: ${res.status} ${await res.text()}`);
  return (await res.json()) as T;
}
