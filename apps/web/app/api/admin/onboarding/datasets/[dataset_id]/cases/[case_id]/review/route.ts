import { type NextRequest } from "next/server";

import { proxyBackend } from "../../../../../../../../_lib/backend-proxy";

export const dynamic = "force-dynamic";

export async function POST(request: NextRequest, context: { params: Promise<{ dataset_id: string; case_id: string }> }) {
  const { dataset_id: datasetID, case_id: caseID } = await context.params;
  return proxyBackend(`/api/admin/onboarding/datasets/${encodeURIComponent(datasetID)}/cases/${encodeURIComponent(caseID)}/review`, {
    method: "POST",
    headers: { "Content-Type": "application/json", "Idempotency-Key": request.headers.get("Idempotency-Key") ?? "" },
    body: (await request.text()) || "{}",
  }, request);
}
