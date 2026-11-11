import { type NextRequest } from "next/server";

import { proxyBackend } from "../../../../../../../_lib/backend-proxy";

export const dynamic = "force-dynamic";

interface RouteContext {
  params: Promise<{
    conversation_id: string;
    message_id: string;
  }>;
}

export async function POST(request: NextRequest, context: RouteContext) {
  const { conversation_id: conversationID, message_id: messageID } = await context.params;
  const payload = await request.text();
  const idempotencyKey = request.headers.get("idempotency-key");

  return proxyBackend(
    `/api/chat/conversations/${encodeURIComponent(conversationID)}/messages/${encodeURIComponent(messageID)}/revalidate`,
    {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        ...(idempotencyKey ? { "Idempotency-Key": idempotencyKey } : {}),
      },
      body: payload || "{}",
    },
    request,
  );
}
