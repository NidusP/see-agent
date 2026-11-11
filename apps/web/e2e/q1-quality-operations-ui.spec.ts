import { expect, test } from "@playwright/test";

const evidence = {
  message_id: "msg_quality_answer",
  conversation_id: "conv_quality",
  request_id: "req_quality",
  config_version: "cfg_baseline",
  retrieval_config_version: "rcfg_baseline",
  knowledge_snapshot_id: "snap_quality",
  knowledge_snapshot_manifest_hash: "sha256:manifest-quality",
  model_id: "llama3.2:latest",
  generation_config: { system_prompt_version: "prompt_v2" },
  retrieval_config: { retrieval_strategy: "native_dense_v1", top_k: 3 },
  refusal: false,
  finish_reason: "stop",
  completed_at: "2026-07-17T01:02:03Z",
  intent: "knowledge_query",
  retrieval_performed: true,
  generation_performed: true,
  knowledge_used: true,
  answer_disposition: "partial_answer",
  answer_policy_version: "answer_policy_v1",
  policy_evaluation_performed: true,
  answer_generation_performed: true,
  coverage: { requested_count: 3, supported_count: 2, uncovered_description: "第三项尚未覆盖" },
  retrieval_candidates: [{ rank: 1, chunk_id: "chunk_quality", file_id: "file_quality", document_id: "document_quality", revision_id: "revision_quality", chunk_content_sha256: "sha256:chunk-quality", knowledge_snapshot_id: "snap_quality", score: 0.92, source_locator: { source_format: "md" } }],
};

const experiment = {
  experiment_id: "exp_dense_refusal",
  status: "review_ready",
  created_by: "admin",
  baseline_config_version: "cfg_baseline",
  baseline_retrieval_config_version: "rcfg_baseline",
  candidate_config_version: "cfg_qwen",
  candidate_retrieval_config_version: "rcfg_qwen",
  knowledge_snapshot_id: "snap_quality",
  knowledge_snapshot_manifest_hash: "sha256:manifest-quality",
  dataset_id: "baseline-v1",
  dataset_sha256: "sha256:dataset-quality",
  conditions: { top_k: 3, rerank_enabled: false },
  conditions_sha256: "sha256:conditions-quality",
  acceptance: { refusal_correctness: 1 },
  feedback_ids: [],
  baseline_report_id: "report_baseline",
  candidate_report_id: "report_candidate",
  created_at: "2026-07-17T00:00:00Z",
};

test("管理员在配置工作台从执行证据记录反馈，并查看候选实验与报告对照", async ({ page }) => {

  let feedbackPosted = false;
  await page.route("**/api/agent/**", async (route) => {
    const pathname = new URL(route.request().url()).pathname;
    if (pathname === "/api/agent/config") {
      await route.fulfill({ json: { config: { config_version: "cfg_baseline", model_id: "llama3.2:latest", system_prompt_version: "prompt_v2", response_style: "concise", temperature: 0.2, top_p: 1, max_output_tokens: 1024, created_at: "2026-07-17T00:00:00Z" } } });
      return;
    }
    if (pathname === "/api/agent/retrieval-config") {
      await route.fulfill({ json: { config: { retrieval_config_version: "rcfg_baseline", retrieval_strategy: "native_dense_v1", embedding_model: "all-minilm", top_k: 3, rerank_enabled: false, evidence_min_score: 0.15, created_at: "2026-07-17T00:00:00Z" } } });
      return;
    }
    await route.fulfill({ json: { items: [], total: 0 } });
  });
  await page.route("**/api/admin/evidence/**", async (route) => {
    const pathname = new URL(route.request().url()).pathname;
    if (pathname === "/api/admin/evidence/executions") {
      await route.fulfill({ json: { items: [evidence] } });
      return;
    }
    if (pathname === `/api/admin/evidence/messages/${evidence.message_id}`) {
      await route.fulfill({ json: { evidence: { ...evidence, citations: [] } } });
      return;
    }
    await route.fulfill({ json: { items: [] } });
  });
  await page.route("**/api/eval/reports**", async (route) => {
    const reportID = new URL(route.request().url()).pathname.split("/").at(-1);
    if (reportID === "report_baseline" || reportID === "report_candidate") {
      await route.fulfill({ json: { report: { report_id: reportID, dataset_id: "baseline-v1", model_id: reportID === "report_baseline" ? "all-minilm" : "qwen3-embedding:0.6b", config_version: "cfg_baseline", retrieval_config_version: "rcfg_baseline", generation_config: {}, metrics: { refusal_correctness: reportID === "report_baseline" ? 0 : 1 }, completed_at: "2026-07-17T00:00:00Z", report_payload: {}, created_at: "2026-07-17T00:00:01Z" } } });
      return;
    }
    await route.fulfill({ json: { items: [] } });
  });
  await page.route("**/api/admin/quality/**", async (route) => {
    const request = route.request();
    const pathname = new URL(request.url()).pathname;
    if (pathname === "/api/admin/quality/feedback" && request.method() === "POST") {
      expect(request.headers()["idempotency-key"]).toBeTruthy();
      expect(request.postDataJSON()).toMatchObject({ message_id: evidence.message_id, kind: "should_clarify", comment: "问题缺少适用地点" });
      feedbackPosted = true;
      await route.fulfill({ status: 201, json: { feedback: { feedback_id: "feedback_refuse", message_id: evidence.message_id, conversation_id: evidence.conversation_id, actor_id: "admin", kind: "should_refuse", created_at: "2026-07-17T01:03:00Z" } } });
      return;
    }
    if (pathname === "/api/admin/quality/feedback") {
      await route.fulfill({ json: { items: [] } });
      return;
    }
    if (pathname === "/api/admin/quality/experiments") {
      await route.fulfill({ json: { items: [experiment] } });
      return;
    }
    if (pathname === `/api/admin/quality/experiments/${experiment.experiment_id}`) {
      await route.fulfill({ json: { experiment } });
      return;
    }
    if (pathname === "/api/admin/quality/releases") {
      await route.fulfill({ json: { items: [] } });
      return;
    }
    await route.fulfill({ status: 404, json: { error: { code: "not_found", message: "unexpected quality request" } } });
  });

  await page.goto("/admin/login?next=/config");
  await page.getByTestId("login-username").fill(process.env.SMOKE_ADMIN_USERNAME ?? "admin");
  await page.getByTestId("login-password").fill(process.env.SMOKE_ADMIN_PASSWORD ?? "admin123");
  await Promise.all([
    page.waitForURL(/\/config$/),
    page.getByTestId("login-submit").click(),
  ]);
  const quality = page.getByTestId("quality-operations-workspace");
  await expect(quality).toContainText("发布安全边界");
  await expect(quality).toContainText("不会自动调参或改变线上");
  await expect(page.getByTestId("evidence-coverage-detail")).toContainText("2/3");
  await expect(page.getByTestId("retrieval-candidate-diagnostics")).toContainText("候选 #1");
  await quality.getByTestId("feedback-kind").click();
  await page.getByRole("option", { name: "本应澄清" }).click();
  await expect(quality.getByText("期望引用 rank（可选，逗号分隔）")).toHaveCount(0);
  await expect(quality.getByText("不应澄清", { exact: true })).toHaveCount(0);
  await quality.getByLabel("说明（必填）").fill("问题缺少适用地点");
  await quality.getByRole("button", { name: "追加不可变反馈" }).click();
  await expect.poll(() => feedbackPosted).toBe(true);
  await expect(quality).toContainText("反馈已追加到所选回答的证据链");

  await quality.getByRole("button", { name: /exp_dense_refusal/ }).click();
  const detail = page.getByTestId("quality-experiment-detail");
  await expect(detail).toContainText("Baseline report");
  await expect(detail).toContainText("Candidate report");
  await expect(detail).toContainText("qwen3-embedding:0.6b");
  await expect(detail.getByRole("button", { name: "发布到未来新会话" })).toBeDisabled();
});
