import { expect, test } from "@playwright/test";

const baseURL = process.env.WEB_BASE_URL ?? "http://127.0.0.1:3000";

test("系统配置页的失效会话会清除 cookie 并回到登录页", async ({ page }) => {
  let logoutCalls = 0;
  await page.context().addCookies([
    { name: "see_agent_session", value: "expired-system-admin-session", url: baseURL },
    { name: "see_agent_role", value: "system_admin", url: baseURL },
  ]);

  await page.route("**/api/auth/logout", async (route) => {
    logoutCalls += 1;
    await route.fulfill({
      status: 200,
      headers: { "Set-Cookie": "see_agent_session=; Max-Age=0; Path=/" },
      json: { ok: true },
    });
  });
  await page.route("**/api/agent/config", async (route) => {
    await route.fulfill({ status: 401, json: { error: { code: "unauthenticated", message: "session expired" } } });
  });

  await page.goto("/config");

  await expect(page).toHaveURL(/\/admin\/login\?next=%2Fconfig$/);
  expect(logoutCalls).toBe(1);
});
