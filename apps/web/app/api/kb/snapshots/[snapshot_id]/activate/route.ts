import { type NextRequest } from "next/server";

import { proxyBackend } from "../../../../../_lib/backend-proxy";

export const dynamic = "force-dynamic";

export async function POST(request: NextRequest, context: { params: Promise<{ snapshot_id: string }> }) {
  const { snapshot_id: snapshotID } = await context.params;
  const key = request.headers.get("Idempotency-Key") ?? request.headers.get("idempotency-key") ?? "";
  return proxyBackend(`/api/kb/snapshots/${encodeURIComponent(snapshotID)}/activate`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      ...(key ? { "Idempotency-Key": key } : {}),
    },
    body: (await request.text()) || "{}",
  }, request);
}
