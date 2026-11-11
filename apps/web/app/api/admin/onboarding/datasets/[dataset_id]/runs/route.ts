import { type NextRequest } from "next/server";

import { proxyBackend } from "../../../../../../_lib/backend-proxy";

export const dynamic = "force-dynamic";

export async function POST(request: NextRequest, { params }: { params: Promise<{ dataset_id: string }> }) {
  const { dataset_id: datasetID } = await params;
  return proxyBackend(`/api/admin/onboarding/datasets/${encodeURIComponent(datasetID)}/runs`, { method: "POST", headers: { "Idempotency-Key": request.headers.get("Idempotency-Key") ?? "" } }, request);
}
