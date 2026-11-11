import { expect, test } from "@playwright/test";

test("404 页面使用主题前景色并提供返回工作台入口", async ({ page }) => {
  await page.goto("/missing-route");

  await expect(page.locator("html")).toHaveAttribute("data-theme", "verdant-habitat");
  await expect(page.getByRole("heading", { name: "页面未找到" })).toBeVisible();
  await expect(page.getByRole("link", { name: "返回工作台" })).toHaveAttribute("href", "/chat");

  const colors = await page.locator(".not-found-card").evaluate((element) => {
    const styles = getComputedStyle(element);
    return { background: styles.backgroundColor, color: styles.color };
  });
  expect(colors.background).not.toBe("rgba(0, 0, 0, 0)");
  expect(colors.color).not.toBe("rgb(255, 255, 255)");
});

test("404 页面在静纸主题中仍使用主题前景与表面色", async ({ page }) => {
  await page.addInitScript(() => window.localStorage.setItem("see-agent:theme", "quiet-paper"));
  await page.goto("/missing-route");

  await expect(page.locator("html")).toHaveAttribute("data-theme", "quiet-paper");
  const colors = await page.locator(".not-found-card").evaluate((element) => {
    const styles = getComputedStyle(element);
    return { background: styles.backgroundColor, color: styles.color };
  });
  expect(colors.background).toBe("rgb(255, 255, 255)");
  expect(colors.color).toBe("rgb(32, 33, 35)");
});
