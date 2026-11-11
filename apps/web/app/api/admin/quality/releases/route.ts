import { type NextRequest } from "next/server";

import { proxyBackend } from "../../../../_lib/backend-proxy";

export const dynamic = "force-dynamic";

export async function GET(request: NextRequest) {
  return proxyBackend(`/api/admin/quality/releases${request.nextUrl.search}`, undefined, request);
}
