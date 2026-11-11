import { type NextRequest } from "next/server";

import { proxyBackend } from "../../../../../_lib/backend-proxy";

export const dynamic = "force-dynamic";

export async function GET(request: NextRequest, { params }: { params: Promise<{ run_id: string }> }) {
  const { run_id: runID } = await params;
  return proxyBackend(`/api/admin/onboarding/runs/${encodeURIComponent(runID)}`, undefined, request);
}
