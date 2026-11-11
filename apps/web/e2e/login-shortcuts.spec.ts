import { expect, test } from "@playwright/test";

const accounts = [
  { id: "user", username: "user", password: "user123", entry: "/login" },
  { id: "admin", username: "admin", password: "admin123", entry: "/admin/login" },
] as const;

for (const account of accounts) {
  test(`${account.id} 快捷登录使用默认调试账号`, async ({ page }) => {
    let payload: unknown;
    await page.route("**/api/auth/login", async (route) => {
      payload = route.request().postDataJSON();
      await route.fulfill({
        status: 200,
        headers: { "Set-Cookie": "see_agent_session=shortcut-test; Path=/" },
        json: { user: { user_id: `user_${account.id}`, username: account.username, role: account.id } },
      });
    });

    await page.goto(`${account.entry}?next=/chat`);
    await Promise.all([
      page.waitForURL(/\/chat(?:\/.*)?$/),
      page.getByTestId(`login-shortcut-${account.id}`).click(),
    ]);

    expect(payload).toEqual({ username: account.username, password: account.password });
  });
}
