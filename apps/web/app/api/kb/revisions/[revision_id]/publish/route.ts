import { type NextRequest } from "next/server";

import { proxyBackend } from "../../../../../_lib/backend-proxy";

export const dynamic = "force-dynamic";

interface RouteContext {
  params: Promise<{
    revision_id: string;
  }>;
}

export async function POST(request: NextRequest, context: RouteContext) {
  const { revision_id: revisionID } = await context.params;
  const key = request.headers.get("Idempotency-Key") ?? request.headers.get("idempotency-key") ?? "";
  return proxyBackend(
    `/api/kb/revisions/${encodeURIComponent(revisionID)}/publish`,
    {
      method: "POST",
      headers: {
        "Content-Type": request.headers.get("content-type") ?? "application/json",
        ...(key ? { "Idempotency-Key": key } : {}),
      },
      body: (await request.text()) || "{}",
    },
    request,
  );
}
