export type SessionRole = "user" | "knowledge_admin" | "system_admin" | "admin";

export function isKnowledgeAdmin(role?: SessionRole) {
  return role === "knowledge_admin" || isSystemAdmin(role);
}

export function isSystemAdmin(role?: SessionRole) {
  return role === "system_admin" || role === "admin";
}

export function sessionRoleLabel(role?: SessionRole) {
  if (isSystemAdmin(role)) return "系统管理员";
  if (role === "knowledge_admin") return "知识库管理员";
  return "普通用户";
}
