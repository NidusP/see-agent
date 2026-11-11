import { type NextRequest } from "next/server";

import { proxyBackend } from "../../../_lib/backend-proxy";

export const dynamic = "force-dynamic";

export async function GET(request: NextRequest) {
  return proxyBackend(`/api/eval/reports${request.nextUrl.search}`, undefined, request);
}

export async function POST(request: NextRequest) {
  const payload = await request.text();
  const idempotencyKey = request.headers.get("Idempotency-Key") ?? "";
  return proxyBackend("/api/eval/reports", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "Idempotency-Key": idempotencyKey,
    },
    body: payload || "{}",
  }, request);
}
