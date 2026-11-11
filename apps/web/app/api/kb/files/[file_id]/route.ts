import { type NextRequest } from "next/server";

import { proxyBackend } from "../../../../_lib/backend-proxy";

export const dynamic = "force-dynamic";

interface RouteContext {
  params: Promise<{
    file_id: string;
  }>;
}

export async function GET(request: NextRequest, context: RouteContext) {
  const { file_id: fileID } = await context.params;
  return proxyBackend(`/api/kb/files/${encodeURIComponent(fileID)}`, undefined, request);
}
