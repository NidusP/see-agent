"use client";

import { useRouter } from "next/navigation";
import { useState } from "react";
import { Button } from "./ui/button";

export function LogoutButton() {
  const router = useRouter();
  const [submitting, setSubmitting] = useState(false);

  async function handleLogout() {
    setSubmitting(true);
    try {
      await fetch("/api/auth/logout", { method: "POST" });
    } finally {
      router.push("/login");
      router.refresh();
    }
  }

  return (
    <Button type="button" variant="ghost" size="sm" className="ghost-button" onClick={() => void handleLogout()} disabled={submitting}>
      {submitting ? "退出中" : "退出"}
    </Button>
  );
}
