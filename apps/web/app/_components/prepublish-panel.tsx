"use client";

import { useState, type FormEvent } from "react";

import {
  createOnboardingCase,
  createOnboardingDataset,
  createOnboardingRun,
  freezeOnboardingDataset,
  generateOnboardingDrafts,
  getOnboardingRun,
  reviewOnboardingCase,
  type OnboardingCase,
  type OnboardingDataset,
  type OnboardingRun,
} from "../_lib/onboarding-quality-api";
import { type KnowledgeAcceptance, type KnowledgeSnapshotMember, type KnowledgeSnapshotSummary } from "../_lib/knowledge-api";
import { ApiError } from "../_lib/request-json";
import { Button } from "./ui/button";

export function PrepublishPanel({
  mode,
  snapshot,
  onActivate,
  onMakeup,
  onReload,
}: {
  mode: "publish" | "wait";
  snapshot: KnowledgeSnapshotSummary;
  onActivate?: (acknowledgeUnvalidated: boolean) => Promise<void>;
  onMakeup?: (snapshotId: string) => void;
  onReload?: () => Promise<void>;
}) {
  const acceptance = snapshot.acceptance;
  const [dataset, setDataset] = useState<OnboardingDataset>();
  const [cases, setCases] = useState<OnboardingCase[]>([]);
  const [run, setRun] = useState<OnboardingRun>();
  const [acknowledged, setAcknowledged] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const [notice, setNotice] = useState<string>();
  const [manualType, setManualType] = useState<"must_answer" | "must_refuse">("must_refuse");
  const [manualQuestion, setManualQuestion] = useState("");
  const [manualRevision, setManualRevision] = useState(snapshot.members?.[0]?.revision_id ?? "");
  const [manualLocator, setManualLocator] = useState("");
  const [manualAnswer, setManualAnswer] = useState("");
  const requiresAcknowledgement = Boolean(acceptance?.requires_unvalidated_acknowledgement);
  const acceptanceActionsId = `acceptance-actions-${snapshot.snapshot_id}`;

  const runAction = async (action: () => Promise<void>) => {
    setBusy(true);
    setError(undefined);
    try {
      await action();
    } catch (reason) {
      setError(reason instanceof ApiError ? reason.message : reason instanceof Error ? reason.message : "操作失败");
    } finally {
      setBusy(false);
    }
  };

  const draftQuestions = () => void runAction(async () => {
    const current = dataset ?? (await createOnboardingDataset({
      name: `上线验收 ${snapshot.snapshot_id}`,
      knowledge_snapshot_id: snapshot.snapshot_id,
    })).dataset;
    setDataset(current);
    try {
      const response = await generateOnboardingDrafts(current.dataset_id);
      setCases(response.cases);
      setNotice("已起草草稿题。请逐题确认后才能冻结和运行；草稿本身不是通过证据。");
    } catch (reason) {
      setCases([]);
      throw reason;
    }
  });

  const confirmCase = (item: OnboardingCase) => void runAction(async () => {
    const response = await reviewOnboardingCase(item.dataset_id, item.case_id, {
      question: item.question,
      expected_revision_ids: item.expected_revision_ids,
      expected_locator_hints: item.expected_locator_hints,
      expected_answer_points: item.expected_answer_points,
    });
    setCases((current) => current.map((entry) => entry.case_id === item.case_id ? response.case : entry));
    setNotice("题目已确认，可以进入冻结。");
  });

  const addManualCase = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!dataset) {
      setError("请先创建验收集，或使用起草按钮。");
      return;
    }
    void runAction(async () => {
      const response = await createOnboardingCase(dataset.dataset_id, manualType === "must_refuse" ? {
        case_type: "must_refuse",
        question: manualQuestion.trim(),
        review_status: "reviewed",
      } : {
        case_type: "must_answer",
        question: manualQuestion.trim(),
        expected_revision_ids: manualRevision.split(",").map((item) => item.trim()).filter(Boolean),
        expected_locator_hints: manualLocator.split(",").map((item) => item.trim()).filter(Boolean),
        expected_answer_points: manualAnswer.split(",").map((item) => item.trim()).filter(Boolean),
        review_status: "reviewed",
      });
      setCases((current) => [...current, response.case]);
      setManualQuestion("");
      setNotice("已添加手工题目。生成失败不会阻止手工出题或直接发布。");
    });
  };

  const freeze = () => void runAction(async () => {
    if (!dataset) return;
    const response = await freezeOnboardingDataset(dataset.dataset_id);
    setDataset(response.dataset);
    setCases(response.dataset.cases ?? cases);
    setNotice("问题集已冻结。");
  });

  const startRun = () => void runAction(async () => {
    if (!dataset) return;
    const response = await createOnboardingRun(dataset.dataset_id);
    setRun(response.run);
    setNotice("验收已开始。通过后即可发布已验收的知识；未通过仍可在确认后直接发布。");
  });

  const refreshRun = () => void runAction(async () => {
    if (!run) return;
    const next = (await getOnboardingRun(run.run_id)).run;
    setRun(next);
    if (next.status === "passed" || next.status === "failed") {
      await onReload?.();
    }
  });

  return (
    <section className="detail-task-card" data-testid="knowledge-prepublish-panel">
      <div className="section-heading">
        <h3>{snapshot.status === "pending_publish" ? "待发布知识" : "知识发布状态"}</h3>
        {acceptance?.display_label ? <strong data-testid="knowledge-acceptance-label">{acceptance.display_label}</strong> : null}
      </div>
      <p className="muted-copy">即将生效的快照 {snapshot.snapshot_id}。待发布快照不会切换当前问答。</p>
      <SnapshotMembers members={snapshot.members} />
      {mode === "wait" && snapshot.status === "pending_publish" ? <p data-testid="waiting-for-publish">等待系统管理员发布</p> : null}
      {error ? <p className="error-copy" role="alert">{error}</p> : null}
      {notice ? <p className="success-copy" role="status">{notice}</p> : null}
      <div id={acceptanceActionsId} className="quality-actions">
        <Button type="button" variant="outline" size="sm" onClick={draftQuestions} disabled={busy}>按新文档起草必答题和必拒题</Button>
        {dataset?.status === "draft" ? <Button type="button" variant="outline" size="sm" onClick={freeze} disabled={busy}>冻结</Button> : null}
        {dataset?.status === "ready" ? <Button type="button" size="sm" onClick={startRun} disabled={busy}>运行验收</Button> : null}
        {run && (run.status === "queued" || run.status === "running") ? <Button type="button" variant="outline" size="sm" onClick={refreshRun} disabled={busy}>刷新验收结果</Button> : null}
      </div>
      {cases.map((item) => (
        <div key={item.case_id}>
          <p>{item.case_type === "must_answer" ? "必答" : "必拒"} · {item.question}</p>
          <p className="muted-copy">{item.review_status === "reviewed" ? "已确认" : "草稿，确认后才能冻结"}</p>
          {item.review_status !== "reviewed" ? <Button type="button" size="sm" variant="outline" onClick={() => confirmCase(item)} disabled={busy}>确认这道题</Button> : null}
        </div>
      ))}
      <form onSubmit={addManualCase}>
        <label>
          <span>手工题目类型</span>
          <select value={manualType} onChange={(event) => setManualType(event.target.value as "must_answer" | "must_refuse")} disabled={busy || !dataset}>
            <option value="must_answer">必答</option>
            <option value="must_refuse">必拒</option>
          </select>
        </label>
        <label>
          <span>手工题目</span>
          <input value={manualQuestion} onChange={(event) => setManualQuestion(event.target.value)} disabled={busy || !dataset} />
        </label>
        {manualType === "must_answer" ? (
          <>
            <label><span>期望 revision</span><input value={manualRevision} onChange={(event) => setManualRevision(event.target.value)} disabled={busy || !dataset} /></label>
            <label><span>定位提示</span><input value={manualLocator} onChange={(event) => setManualLocator(event.target.value)} disabled={busy || !dataset} /></label>
            <label><span>答案要点</span><input value={manualAnswer} onChange={(event) => setManualAnswer(event.target.value)} disabled={busy || !dataset} /></label>
          </>
        ) : null}
        <Button type="submit" variant="outline" size="sm" disabled={busy || !dataset || manualQuestion.trim() === ""}>添加手工题目</Button>
      </form>
      {mode === "publish" && acceptance?.status === "failed" ? <p>发布后这份知识会立刻服务用户，但验收未通过。</p> : null}
      {mode === "publish" && requiresAcknowledgement ? (
        <div className="quality-actions">
          <Button type="button" onClick={() => document.getElementById(acceptanceActionsId)?.scrollIntoView({ block: "nearest" })}>去跑上线验收</Button>
          <label>
            <input
              type="checkbox"
              checked={acknowledged}
              onChange={(event) => setAcknowledged(event.target.checked)}
              data-testid="acknowledge-unvalidated"
            />
            我知道尚未完成上线验收，仍要发布
          </label>
          <Button type="button" variant="outline" data-testid="publish-direct" disabled={busy || !acknowledged} onClick={() => void runAction(async () => { await onActivate?.(true); })}>直接发布</Button>
        </div>
      ) : null}
      {mode === "publish" && acceptance && !requiresAcknowledgement ? (
        <Button type="button" data-testid="publish-accepted" disabled={busy} onClick={() => void runAction(async () => { await onActivate?.(false); })}>发布已验收的知识</Button>
      ) : null}
      {mode === "wait" && (acceptance?.status === "unvalidated" || acceptance?.status === "failed") && snapshot.status === "published" ? (
        <div>
          <p>这份知识已在服务用户。建议补跑上线验收；这不会阻断聊天，也不会阻断继续上传。</p>
          <Button type="button" variant="outline" data-testid="makeup-onboarding" onClick={() => onMakeup?.(snapshot.snapshot_id)}>补跑验收</Button>
        </div>
      ) : null}
    </section>
  );
}

function SnapshotMembers({ members }: { members?: KnowledgeSnapshotMember[] }) {
  if (!members || members.length === 0) {
    return null;
  }
  return (
    <ul>
      {members.map((member) => (
        <li key={member.revision_id}>{member.document_id} · {member.revision_id}</li>
      ))}
    </ul>
  );
}

export function PublishedAcceptanceReminder({
  acceptance,
  snapshotId,
  onMakeup,
}: {
  acceptance?: KnowledgeAcceptance;
  snapshotId?: string;
  onMakeup: (snapshotId: string) => void;
}) {
  if (!acceptance?.display_label || !snapshotId) {
    return null;
  }
  return (
    <div data-testid="published-acceptance-reminder">
      <strong data-testid="knowledge-acceptance-label">{acceptance.display_label}</strong>
      {acceptance.status === "unvalidated" || acceptance.status === "failed" ? (
        <>
          <p>这份知识已在服务用户。建议补跑上线验收；这不会阻断聊天，也不会阻断继续上传。</p>
          <button type="button" data-testid="makeup-onboarding" onClick={() => onMakeup(snapshotId)}>补跑验收</button>
        </>
      ) : null}
    </div>
  );
}
