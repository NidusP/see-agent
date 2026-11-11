export function loginEntryForPath(pathname: string) {
  return pathname === "/knowledge" || pathname.startsWith("/knowledge/") || pathname === "/config" || pathname.startsWith("/config/")
    ? "/admin/login"
    : "/login";
}
