import { type NextRequest } from "next/server";

import { proxyBackend } from "../../../../../../_lib/backend-proxy";

export const dynamic = "force-dynamic";

export async function POST(request: NextRequest, { params }: { params: Promise<{ release_id: string }> }) {
  const { release_id: releaseID } = await params;
  return proxyBackend(`/api/admin/quality/releases/${encodeURIComponent(releaseID)}/rollback`, { method: "POST", headers: { "Idempotency-Key": request.headers.get("Idempotency-Key") ?? "" } }, request);
}
