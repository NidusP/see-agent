import { LoginForm } from "../_components/login-form";
import { ThemePicker } from "../_components/theme-picker";

interface LoginPageProps {
  searchParams: Promise<{
    next?: string;
  }>;
}

export default async function LoginPage({ searchParams }: LoginPageProps) {
  const params = await searchParams;
  const nextPath = normalizeNextPath(params.next);

  return (
    <main className="login-page">
      <section className="login-panel panel">
        <div className="brand-block">
          <p className="eyebrow">see-agent / 内部访问</p>
          <h1>进入单智能体工作台</h1>
          <p className="muted-copy">请使用您的内部账号登录。</p>
        </div>
        <ThemePicker />
        <LoginForm nextPath={nextPath} entry="user" adminLoginPath="/admin/login?next=%2Fknowledge" />
      </section>
    </main>
  );
}

function normalizeNextPath(next?: string) {
  if (!next || !next.startsWith("/") || next.startsWith("//") || next.startsWith("/login")) {
    return "/chat";
  }
  return next;
}
