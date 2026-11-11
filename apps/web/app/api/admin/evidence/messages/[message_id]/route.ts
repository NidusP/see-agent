import { type NextRequest } from "next/server";

import { proxyBackend } from "../../../../../_lib/backend-proxy";

export const dynamic = "force-dynamic";

export async function GET(request: NextRequest, context: { params: Promise<{ message_id: string }> }) {
  const { message_id: messageID } = await context.params;
  return proxyBackend(`/api/admin/evidence/messages/${encodeURIComponent(messageID)}`, undefined, request);
}
