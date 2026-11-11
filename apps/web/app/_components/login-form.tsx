"use client";

import { useRouter } from "next/navigation";
import { useState, type FormEvent } from "react";
import { clearAuthenticationRecovery } from "../_lib/auth-recovery";
import { Button } from "./ui/button";
import { Input } from "./ui/input";

interface LoginFormProps {
  nextPath: string;
  entry: "user" | "admin";
  adminLoginPath?: string;
}

const developmentAccounts = {
  user: [{ id: "user", label: "普通用户", username: "user", password: "user123" }],
  admin: [
    { id: "knowledge-admin", label: "知识库管理员", username: "knowledge_admin", password: "knowledge_admin123" },
    { id: "admin", label: "系统管理员", username: "admin", password: "admin123" },
  ],
} as const;

export function LoginForm({ nextPath, entry, adminLoginPath }: LoginFormProps) {
  const router = useRouter();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string>();
  const [submitting, setSubmitting] = useState(false);

  async function submitCredentials(loginUsername: string, loginPassword: string) {
    setSubmitting(true);
    setError(undefined);

    try {
      const response = await fetch("/api/auth/login", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
        },
        body: JSON.stringify({ username: loginUsername, password: loginPassword }),
      });
      if (!response.ok) {
        setError(response.status === 401 ? "用户名或密码不正确。" : "登录失败，请稍后重试。");
        return;
      }
      clearAuthenticationRecovery();
      router.push(nextPath);
      router.refresh();
    } catch {
      setError("无法连接登录服务，请确认 Backend 已启动。");
    } finally {
      setSubmitting(false);
    }
  }

  function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    void submitCredentials(username, password);
  }

  function loginWithDevelopmentAccount(account: (typeof developmentAccounts)["user"][number] | (typeof developmentAccounts)["admin"][number]) {
    setUsername(account.username);
    setPassword(account.password);
    void submitCredentials(account.username, account.password);
  }

  return (
    <form className="login-form" onSubmit={handleSubmit}>
      <label>
        <span>用户名</span>
        <Input
          data-testid="login-username"
          autoComplete="username"
          value={username}
          onChange={(event) => setUsername(event.target.value)}
          placeholder="输入内部账号"
          disabled={submitting}
        />
      </label>
      <label>
        <span>密码</span>
        <Input
          data-testid="login-password"
          autoComplete="current-password"
          type="password"
          value={password}
          onChange={(event) => setPassword(event.target.value)}
          placeholder="输入密码"
          disabled={submitting}
        />
      </label>
      {process.env.NODE_ENV === "development" ? (
        <div className="login-shortcuts" aria-label="开发快捷登录">
          <span>开发快捷登录</span>
          <div className="login-shortcut-actions">
            {developmentAccounts[entry].map((account) => (
              <Button
                data-testid={`login-shortcut-${account.id}`}
                type="button"
                variant="outline"
                className="login-shortcut-button"
                key={account.id}
                disabled={submitting}
                onClick={() => loginWithDevelopmentAccount(account)}
              >
                {account.label}
              </Button>
            ))}
          </div>
        </div>
      ) : null}
      {entry === "user" && adminLoginPath ? (
        <div className="login-admin-entry">
          <span>需要管理知识库？</span>
          <Button
            data-testid="login-admin-entry"
            type="button"
            variant="link"
            className="login-admin-entry-button"
            onClick={() => router.push(adminLoginPath)}
            disabled={submitting}
          >
            管理员登录
          </Button>
        </div>
      ) : null}
      {error ? <p className="inline-error">{error}</p> : null}
      <Button data-testid="login-submit" type="submit" disabled={submitting || username.trim() === "" || password === ""}>
        {submitting ? "登录中..." : "登录"}
      </Button>
    </form>
  );
}
