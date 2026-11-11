import { expect, test } from "@playwright/test";

const initialAgentVersions = [
  { config_version: "cfg_001", model_id: "llama3.2:latest", system_prompt_version: "prompt_v1", response_style: "concise", temperature: 0.2, top_p: 1, max_output_tokens: 1024, created_at: "2026-07-18T00:00:00Z" },
  { config_version: "cfg_002", model_id: "qwen2.5:14b-instruct", system_prompt_version: "prompt_v2", response_style: "concise", temperature: 0.2, top_p: 1, max_output_tokens: 1024, created_at: "2026-07-18T01:00:00Z" },
];

const retrievalVersions = [
  { retrieval_config_version: "rcfg_001", retrieval_strategy: "lexical_compat_v1", embedding_model: "all-minilm", top_k: 3, rerank_enabled: false, evidence_min_score: 0.15, created_at: "2026-07-18T00:00:00Z" },
  { retrieval_config_version: "rcfg_002", retrieval_strategy: "native_dense_v1", embedding_model: "qwen3-embedding:0.6b", top_k: 3, rerank_enabled: false, evidence_min_score: 0.51, created_at: "2026-07-18T01:00:00Z" },
];

test("配置保存仅在字段变更后可用，版本历史显示相邻版本差异", async ({ page }) => {
  let agentVersions = [...initialAgentVersions];
  let saveRequests = 0;

  await page.route("**/api/agent/**", async (route) => {
    const request = route.request();
    const pathname = new URL(request.url()).pathname;
    if (pathname === "/api/agent/config" && request.method() === "GET") {
      await route.fulfill({ json: { config: agentVersions.at(-1) } });
      return;
    }
    if (pathname === "/api/agent/retrieval-config" && request.method() === "GET") {
      await route.fulfill({ json: { config: retrievalVersions.at(-1) } });
      return;
    }
    if (pathname === "/api/agent/config/versions") {
      await route.fulfill({ json: { items: agentVersions, total: agentVersions.length, retrieval_items: retrievalVersions, retrieval_total: retrievalVersions.length } });
      return;
    }
    if (pathname === "/api/agent/config" && request.method() === "PUT") {
      expect(request.postDataJSON()).toMatchObject({ model_id: "qwen2.5:32b-instruct" });
      saveRequests += 1;
      const next = { ...agentVersions.at(-1)!, config_version: "cfg_003", model_id: "qwen2.5:32b-instruct", created_at: "2026-07-18T02:00:00Z" };
      agentVersions = [...agentVersions, next];
      await route.fulfill({ json: { config: next } });
      return;
    }
    await route.fulfill({ status: 404, json: { error: { code: "not_found", message: "unexpected config request" } } });
  });
  await page.route("**/api/admin/evidence/**", (route) => route.fulfill({ json: { items: [] } }));
  await page.route("**/api/eval/reports**", (route) => route.fulfill({ json: { items: [] } }));
  await page.route("**/api/admin/quality/**", (route) => route.fulfill({ json: { items: [] } }));

  await page.goto("/admin/login?next=/config");
  await page.getByTestId("login-username").fill(process.env.SMOKE_ADMIN_USERNAME ?? "admin");
  await page.getByTestId("login-password").fill(process.env.SMOKE_ADMIN_PASSWORD ?? "admin123");
  await Promise.all([
    page.waitForURL(/\/config$/),
    page.getByTestId("login-submit").click(),
  ]);

  const saveButton = page.getByTestId("save-agent-config");
  await expect(saveButton).toBeDisabled();
  await page.getByTestId("open-config-details").click();
  await expect(page.getByTestId("workspace-detail-dialog")).toBeVisible();
  await expect(page.getByText("model_id：llama3.2:latest → qwen2.5:14b-instruct")).toBeVisible();
  await expect(page.getByText("system_prompt_version：prompt_v1 → prompt_v2")).toBeVisible();
  await expect(page.getByText("retrieval_strategy：lexical_compat_v1 → native_dense_v1")).toBeVisible();
  await expect(page.getByText("evidence_min_score：0.15 → 0.51")).toBeVisible();
  await expect(page.getByText("同一问题下，回答会怎样变化？")).toBeVisible();
  await expect(page.getByText("同一问题下，证据和拒答会怎样变化？")).toBeVisible();
  await expect(page.getByText(/这些数值只是说明，不是可直接复制的推荐值。/)).toBeVisible();
  await page.getByTestId("workspace-detail-close").click();
  await expect(page.getByTestId("workspace-detail-dialog")).toHaveCount(0);

  await page.getByTestId("agent-model-id").fill("qwen2.5:32b-instruct");
  await expect(saveButton).toBeEnabled();
  await saveButton.click();
  await expect.poll(() => saveRequests).toBe(1);
  await expect(page.getByText("Agent 配置候选已保存为 cfg_003。")).toBeVisible();
  await page.getByTestId("open-config-details").click();
  await expect(page.getByText("model_id：qwen2.5:14b-instruct → qwen2.5:32b-instruct")).toBeVisible();
});
