import { LoginForm } from "../../_components/login-form";
import { ThemePicker } from "../../_components/theme-picker";

interface AdminLoginPageProps {
  searchParams: Promise<{
    next?: string;
  }>;
}

export default async function AdminLoginPage({ searchParams }: AdminLoginPageProps) {
  const params = await searchParams;
  const nextPath = normalizeNextPath(params.next);

  return (
    <main className="login-page">
      <section className="login-panel panel">
        <div className="brand-block">
          <p className="eyebrow">see-agent / 管理访问</p>
          <h1>进入管理工作台</h1>
          <p className="muted-copy">请使用获授权的内部管理账号登录。</p>
        </div>
        <ThemePicker />
        <LoginForm nextPath={nextPath} entry="admin" />
      </section>
    </main>
  );
}

function normalizeNextPath(next?: string) {
  if (!next || !next.startsWith("/") || next.startsWith("//") || next.startsWith("/login") || next.startsWith("/admin/login")) {
    return "/chat";
  }
  return next;
}
