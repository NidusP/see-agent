import { type NextRequest } from "next/server";

import { proxyBackend } from "../../../../../_lib/backend-proxy";

export const dynamic = "force-dynamic";

interface RouteContext {
  params: Promise<{
    document_id: string;
  }>;
}

export async function POST(request: NextRequest, context: RouteContext) {
  const { document_id: documentID } = await context.params;
  return proxyBackend(
    `/api/kb/documents/${encodeURIComponent(documentID)}/disable`,
    {
      method: "POST",
      headers: idempotencyHeaders(request),
    },
    request,
  );
}

function idempotencyHeaders(request: NextRequest) {
  const key = request.headers.get("idempotency-key");
  return key ? { "Idempotency-Key": key } : undefined;
}
