import { type NextRequest } from "next/server";

import { proxyBackend } from "../../../../../../_lib/backend-proxy";

export const dynamic = "force-dynamic";

export async function POST(request: NextRequest, { params }: { params: Promise<{ experiment_id: string }> }) {
  const { experiment_id: experimentID } = await params;
  return proxyBackend(`/api/admin/quality/experiments/${encodeURIComponent(experimentID)}/publish`, { method: "POST", headers: { "Idempotency-Key": request.headers.get("Idempotency-Key") ?? "" } }, request);
}
