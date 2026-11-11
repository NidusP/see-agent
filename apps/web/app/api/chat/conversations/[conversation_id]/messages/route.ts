import { type NextRequest } from "next/server";

import { proxyBackend } from "../../../../../_lib/backend-proxy";

export const dynamic = "force-dynamic";

interface RouteContext {
  params: Promise<{
    conversation_id: string;
  }>;
}

export async function POST(request: NextRequest, context: RouteContext) {
  const { conversation_id: conversationID } = await context.params;
  const payload = await request.text();

  return proxyBackend(`/api/chat/conversations/${encodeURIComponent(conversationID)}/messages`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
    },
    body: payload || "{}",
  }, request);
}
