import { type NextRequest } from "next/server";

import { proxyBackend } from "../../../../../_lib/backend-proxy";

export const dynamic = "force-dynamic";

export async function POST(request: NextRequest, context: { params: Promise<{ revision_id: string }> }) {
  const { revision_id: revisionID } = await context.params;
  const key = request.headers.get("Idempotency-Key") ?? request.headers.get("idempotency-key") ?? "";
  return proxyBackend(`/api/kb/revisions/${encodeURIComponent(revisionID)}/prepare-publish`, {
    method: "POST",
    headers: key ? { "Idempotency-Key": key } : undefined,
  }, request);
}
