const recoveryFlag = "__see_agent_auth_recovery_in_progress";
const recoveryNextPathKey = "see-agent:auth-recovery-next";

type RecoveryWindow = Window & typeof globalThis & {
  [recoveryFlag]?: boolean;
};

export async function recoverFromUnauthenticated(nextPath: string) {
  const browserWindow = window as RecoveryWindow;
  if (browserWindow[recoveryFlag] || window.sessionStorage.getItem(recoveryNextPathKey)) {
    return;
  }
  browserWindow[recoveryFlag] = true;
  window.sessionStorage.setItem(recoveryNextPathKey, nextPath);
  try {
    await fetch("/api/auth/logout", { method: "POST" });
  } finally {
    window.location.assign(`${loginEntryForPath(nextPath)}?next=${encodeURIComponent(nextPath)}`);
  }
}

export function clearAuthenticationRecovery() {
  window.sessionStorage.removeItem(recoveryNextPathKey);
}
import { loginEntryForPath } from "./login-entry";
