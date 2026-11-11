import { type NextRequest } from "next/server";

import { proxyBackend } from "../../../../_lib/backend-proxy";

export const dynamic = "force-dynamic";

export async function GET(request: NextRequest) {
  return proxyBackend(`/api/admin/onboarding/datasets${request.nextUrl.search}`, undefined, request);
}

export async function POST(request: NextRequest) {
  return proxyBackend("/api/admin/onboarding/datasets", { method: "POST", headers: { "Content-Type": "application/json", "Idempotency-Key": request.headers.get("Idempotency-Key") ?? "" }, body: (await request.text()) || "{}" }, request);
}
