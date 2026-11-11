import { type NextRequest } from "next/server";

import { proxyBackend } from "../../../../../_lib/backend-proxy";

export const dynamic = "force-dynamic";

interface RouteContext {
  params: Promise<{
    file_id: string;
  }>;
}

export async function POST(request: NextRequest, context: RouteContext) {
  const { file_id: fileID } = await context.params;
  const payload = await request.text();

  return proxyBackend(`/api/kb/files/${encodeURIComponent(fileID)}/reindex`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
    },
    body: payload || "{}",
  }, request);
}
