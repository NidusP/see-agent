import { NextResponse, type NextRequest } from "next/server";
import { loginEntryForPath } from "./app/_lib/login-entry";

const SESSION_COOKIE = "see_agent_session";
const ROLE_COOKIE = "see_agent_role";

export function proxy(request: NextRequest) {
  const { pathname, search } = request.nextUrl;
  if (shouldBypass(pathname)) {
    return NextResponse.next();
  }

  const hasSession = Boolean(request.cookies.get(SESSION_COOKIE)?.value);
  if (isLoginPath(pathname)) {
    if (!hasSession) {
      return NextResponse.next();
    }
    return NextResponse.redirect(new URL(readSafeNextPath(request) ?? "/chat", request.url));
  }

  if (!isWorkspacePath(pathname)) {
    return NextResponse.next();
  }

  if (!hasSession) {
    const loginURL = new URL(loginEntryForPath(pathname), request.url);
    loginURL.searchParams.set("next", `${pathname}${search}`);
    return NextResponse.redirect(loginURL);
  }

  const role = request.cookies.get(ROLE_COOKIE)?.value;
  if (isSystemAdmin(role) && isSystemConfigurationOnlyPath(pathname)) {
    return NextResponse.redirect(new URL("/config", request.url));
  }
  if (isSystemAdminPath(pathname) && !isSystemAdmin(role)) {
    const chatURL = new URL("/chat", request.url);
    chatURL.searchParams.set("notice", "system_admin_required");
    return NextResponse.redirect(chatURL);
  }
  if (isKnowledgeAdminPath(pathname) && !isKnowledgeAdmin(role)) {
    const chatURL = new URL("/chat", request.url);
    chatURL.searchParams.set("notice", "knowledge_admin_required");
    return NextResponse.redirect(chatURL);
  }

  return NextResponse.next();
}

function isSystemConfigurationOnlyPath(pathname: string) {
  return pathname === "/chat" || pathname.startsWith("/chat/") || pathname === "/knowledge" || pathname.startsWith("/knowledge/");
}

function isWorkspacePath(pathname: string) {
  return pathname === "/" || pathname === "/chat" || pathname.startsWith("/chat/") || pathname === "/knowledge" || pathname.startsWith("/knowledge/") || pathname === "/config" || pathname.startsWith("/config/");
}

function isLoginPath(pathname: string) {
  return pathname === "/login" || pathname === "/admin/login";
}

export const config = {
  matcher: ["/((?!_next/static|_next/image|favicon.ico).*)"],
};

function shouldBypass(pathname: string) {
  return pathname.startsWith("/api/") || pathname.includes(".");
}

function isKnowledgeAdmin(role: string | undefined) {
  return role === "knowledge_admin" || isSystemAdmin(role);
}

function isSystemAdmin(role: string | undefined) {
  return role === "system_admin" || role === "admin";
}

function isKnowledgeAdminPath(pathname: string) {
  return pathname === "/knowledge" || pathname.startsWith("/knowledge/");
}

function isSystemAdminPath(pathname: string) {
  return pathname === "/config" || pathname.startsWith("/config/");
}

function readSafeNextPath(request: NextRequest) {
  const nextPath = request.nextUrl.searchParams.get("next");
  if (!nextPath || !nextPath.startsWith("/") || nextPath.startsWith("//") || nextPath.startsWith("/login") || nextPath.startsWith("/admin/login")) {
    return undefined;
  }
  return nextPath;
}
