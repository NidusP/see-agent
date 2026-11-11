import { expect, test, type Page } from "@playwright/test";

const adminUsername = process.env.SMOKE_ADMIN_USERNAME ?? "admin";
const adminPassword = process.env.SMOKE_ADMIN_PASSWORD ?? "admin123";

async function login(page: Page) {
  await page.goto("/admin/login?next=/config");
  await page.getByTestId("login-username").fill(adminUsername);
  await page.getByTestId("login-password").fill(adminPassword);
  await Promise.all([
    page.waitForURL(/\/config$/),
    page.getByTestId("login-submit").click(),
  ]);
}

test("管理员可按 revision 查询回答，并追溯知识、配置、delivery 与 eval evidence", async ({ page }) => {
  const summary = {
    message_id: "msg_k7_answer",
    conversation_id: "conv_k7",
    request_id: "req_k7",
    config_version: "cfg_k7",
    retrieval_config_version: "rcfg_k7",
    knowledge_snapshot_id: "snap_k7",
    model_id: "llama3.2:latest",
    generation_config: { system_prompt_version: "prompt_v1", response_style: "concise", temperature: 0.2, top_p: 1, max_output_tokens: 1024 },
    retrieval_config: { retrieval_strategy: "native_dense_v1", embedding_model: "qwen3-embedding:0.6b", top_k: 6, rerank_enabled: true, evidence_min_score: 0.15 },
    usage: { prompt_tokens: 30, completion_tokens: 12, total_tokens: 42 },
    refusal: false,
    finish_reason: "stop",
    completed_at: "2026-07-15T01:02:03Z",
  };

  await page.route("**/api/agent/**", async (route) => {
    const pathname = new URL(route.request().url()).pathname;
    if (pathname === "/api/agent/config") {
      await route.fulfill({ json: { config: { config_version: "cfg_k7", model_id: "llama3.2:latest", system_prompt_version: "prompt_v1", response_style: "concise", temperature: 0.2, top_p: 1, max_output_tokens: 1024, created_at: "2026-07-15T00:00:00Z" } } });
      return;
    }
    if (pathname === "/api/agent/retrieval-config") {
      await route.fulfill({ json: { config: { retrieval_config_version: "rcfg_k7", retrieval_strategy: "native_dense_v1", embedding_model: "qwen3-embedding:0.6b", top_k: 6, rerank_enabled: true, evidence_min_score: 0.15, created_at: "2026-07-15T00:00:00Z" } } });
      return;
    }
    if (pathname === "/api/agent/config/versions") {
      await route.fulfill({ json: { items: [{ config_version: "cfg_k7", model_id: "llama3.2:latest", system_prompt_version: "prompt_v1", response_style: "concise", temperature: 0.2, top_p: 1, max_output_tokens: 1024, created_at: "2026-07-15T00:00:00Z" }], total: 1 } });
      return;
    }
    await route.fulfill({ status: 404, json: { error: { code: "not_found", message: "unexpected config request" } } });
  });

  await page.route("**/api/admin/evidence/**", async (route) => {
    const url = new URL(route.request().url());
    if (url.pathname === "/api/admin/evidence/executions") {
      if (url.searchParams.get("revision_id")) {
        expect(url.searchParams.get("revision_id")).toBe("rev_policy_v2");
      }
      await route.fulfill({ json: { items: [summary] } });
      return;
    }
    if (url.pathname === `/api/admin/evidence/messages/${summary.message_id}`) {
      await route.fulfill({ json: { evidence: { ...summary, knowledge_snapshot_manifest_hash: "manifest_k7", freshness_status: "current", citations: [{ rank: 1, filename: "expense-policy-v2.md", document_id: "doc_policy", revision_id: "rev_policy_v2", chunk_id: "chunk_policy_v2", score: 0.98, citation_label: "差旅审批" }] } } });
      return;
    }
    if (url.pathname === "/api/admin/evidence/deliveries") {
      await route.fulfill({ json: { items: [{ delivery_id: "delivery_k7", topic: "knowledge.revision.index_requested", document_id: "doc_policy", revision_id: "rev_policy_v2", status: "delivered", attempt_count: 1, available_at: "2026-07-15T00:00:00Z", completed_at: "2026-07-15T00:00:03Z", created_at: "2026-07-15T00:00:00Z", updated_at: "2026-07-15T00:00:03Z" }] } });
      return;
    }
    await route.fulfill({ status: 404, json: { error: { code: "not_found", message: "unexpected evidence request" } } });
  });

  await page.route("**/api/eval/reports**", async (route) => {
    await route.fulfill({ json: { items: [{ report_id: "eval_k7", dataset_id: "baseline-v1", model_id: "llama3.2:latest", config_version: "cfg_k7", retrieval_config_version: "rcfg_k7", generation_config: { temperature: 0.2 }, metrics: { citation_accuracy: 0.97 }, completed_at: "2026-07-15T01:10:00Z", created_at: "2026-07-15T01:10:01Z" }] } });
  });

  await login(page);
  await page.getByTestId("open-config-details").click();
  await expect(page.getByTestId("workspace-detail-dialog")).toBeVisible();
  const promptVersionSelect = page.getByTestId("agent-system-prompt-version");
  await expect(promptVersionSelect).toContainText("prompt_v1");
  await expect(page.getByTestId("retrieval-strategy")).toContainText("原生语义向量（native_dense_v1）");
  await page.getByTestId("workspace-detail-close").click();
  await expect(page.getByTestId("workspace-detail-dialog")).toHaveCount(0);
  await promptVersionSelect.click();
  const promptMenu = page.locator('[data-slot="select-content"]');
  await expect(promptMenu).toBeVisible();
  await page.waitForTimeout(150);
  const [triggerBox, menuBox] = await Promise.all([promptVersionSelect.boundingBox(), promptMenu.boundingBox()]);
  expect(triggerBox).not.toBeNull();
  expect(menuBox).not.toBeNull();
  expect(menuBox!.y).toBeGreaterThanOrEqual(triggerBox!.y + triggerBox!.height - 1);
  expect(menuBox!.y - (triggerBox!.y + triggerBox!.height)).toBeLessThanOrEqual(3);
  await page.keyboard.press("Escape");
  await expect(page.getByText(/负责生成表达的模型.*不会补齐未上传/)).toBeVisible();
  await expect(page.getByText(/拒答的最低证据边界.*边缘匹配更早拒答/)).toBeVisible();
  await expect(page.getByText("保存只创建候选版本，不能跳过报告 gate、审批或发布。")).toBeVisible();
  const workspace = page.getByTestId("evidence-query-workspace");
  await expect(workspace).toContainText("msg_k7_answer");
  await workspace.getByPlaceholder("rev_...").fill("rev_policy_v2");
  await workspace.getByRole("button", { name: "查询证据" }).click();
  await workspace.getByRole("button", { name: /msg_k7_answer/ }).click();

  const detail = page.getByTestId("execution-evidence-detail");
  await expect(detail).toContainText("manifest_k7");
  await expect(detail).toContainText("rev_policy_v2");
  await expect(detail).toContainText("cfg_k7");
  await expect(detail).toContainText("rcfg_k7");
  await expect(page.getByTestId("delivery-evidence-list")).toContainText("delivery_k7");
  await expect(page.getByTestId("eval-evidence-list")).toContainText("eval_k7");
});
