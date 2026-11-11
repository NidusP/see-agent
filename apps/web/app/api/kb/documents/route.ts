import { type NextRequest } from "next/server";

import { proxyBackend } from "../../../_lib/backend-proxy";

export const dynamic = "force-dynamic";

export async function GET(request: NextRequest) {
  return proxyBackend(`/api/kb/documents${request.nextUrl.search}`, undefined, request);
}

export async function POST(request: NextRequest) {
  const body = await request.formData();
  return proxyBackend(
    "/api/kb/documents",
    {
      method: "POST",
      headers: idempotencyHeaders(request),
      body,
    },
    request,
  );
}

function idempotencyHeaders(request: NextRequest) {
  const key = request.headers.get("idempotency-key");
  return key ? { "Idempotency-Key": key } : undefined;
}
