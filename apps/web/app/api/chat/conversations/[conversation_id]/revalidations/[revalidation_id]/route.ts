import { type NextRequest } from "next/server";

import { proxyBackend } from "../../../../../../_lib/backend-proxy";

export const dynamic = "force-dynamic";

interface RouteContext {
  params: Promise<{
    conversation_id: string;
    revalidation_id: string;
  }>;
}

export async function GET(request: NextRequest, context: RouteContext) {
  const { conversation_id: conversationID, revalidation_id: revalidationID } = await context.params;
  return proxyBackend(
    `/api/chat/conversations/${encodeURIComponent(conversationID)}/revalidations/${encodeURIComponent(revalidationID)}`,
    undefined,
    request,
  );
}
