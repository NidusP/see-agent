import { type NextRequest } from "next/server";

import { proxyBackend } from "../../../../_lib/backend-proxy";

export const dynamic = "force-dynamic";

export async function GET(request: NextRequest, context: { params: Promise<{ report_id: string }> }) {
  const { report_id: reportID } = await context.params;
  return proxyBackend(`/api/eval/reports/${encodeURIComponent(reportID)}`, undefined, request);
}
