import { expect, test } from "@playwright/test";

test("生态主题是默认值，选择会在当前浏览器恢复", async ({ page }) => {
  await page.goto("/login");

  const picker = page.getByTestId("theme-picker");
  await expect(picker).toContainText("绿野共生");
  await expect(page.locator("html")).toHaveAttribute("data-theme", "verdant-habitat");
  const verdantScrollbarThumb = await page.locator("html").evaluate((element) => getComputedStyle(element).getPropertyValue("--scrollbar-thumb").trim());

  await picker.click();
  await page.getByRole("option", { name: "静纸工作室" }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "quiet-paper");
  const quietScrollbarThumb = await page.locator("html").evaluate((element) => getComputedStyle(element).getPropertyValue("--scrollbar-thumb").trim());
  expect(quietScrollbarThumb).not.toBe(verdantScrollbarThumb);
  await page.reload();

  await expect(picker).toContainText("静纸工作室");
  await expect(page.locator("html")).toHaveAttribute("data-theme", "quiet-paper");
});

test("损坏的主题偏好安全回退到绿野共生", async ({ page }) => {
  await page.addInitScript(() => window.localStorage.setItem("see-agent:theme", "untrusted-theme"));
  await page.goto("/login");

  await expect(page.getByTestId("theme-picker")).toContainText("绿野共生");
  await expect(page.locator("html")).toHaveAttribute("data-theme", "verdant-habitat");
});
