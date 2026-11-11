import { type NextRequest } from "next/server";

import { proxyBackend } from "../../../_lib/backend-proxy";

export const dynamic = "force-dynamic";

export async function POST(request: NextRequest) {
  return proxyBackend(
    "/api/auth/logout",
    {
      method: "POST",
      body: "{}",
    },
    request,
  );
}
