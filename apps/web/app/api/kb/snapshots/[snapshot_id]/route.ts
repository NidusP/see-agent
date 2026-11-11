import { type NextRequest } from "next/server";

import { proxyBackend } from "../../../../_lib/backend-proxy";

export const dynamic = "force-dynamic";

interface RouteContext {
  params: Promise<{
    snapshot_id: string;
  }>;
}

export async function GET(request: NextRequest, context: RouteContext) {
  const { snapshot_id: snapshotID } = await context.params;
  return proxyBackend(`/api/kb/snapshots/${encodeURIComponent(snapshotID)}`, undefined, request);
}
