"use client";

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ChangeEvent,
  type FormEvent,
  type ReactNode,
} from "react";

import {
  activateKnowledgeSnapshot,
  createIdempotencyKey,
  deleteKnowledgeDocument,
  disableKnowledgeDocument,
  formatDateTime,
  getKnowledgeDocument,
  getKnowledgeRetirementReadiness,
  getKnowledgeSnapshot,
  listKnowledgeDocuments,
  listLegacyKnowledgeFiles,
  listKnowledgeUpdates,
  uploadKnowledgeDocument,
  uploadKnowledgeRevision,
  ApiError as KnowledgeApiError,
  type KnowledgeDocumentDetailResponse,
  type KnowledgeDocumentListItem,
  type KnowledgeDocumentStatus,
  type KnowledgeRetirementReadiness,
  type KnowledgeRevision,
  type KnowledgeSnapshot,
  type KnowledgeUpdateEvent,
  type LegacyKnowledgeFile,
  type RevisionDeliverySummary,
} from "../_lib/knowledge-api";
import { ApiError as RequestApiError } from "../_lib/request-json";
import { recoverFromUnauthenticated } from "../_lib/auth-recovery";
import {
  createOnboardingCase,
  createOnboardingDataset,
  createOnboardingRun,
  freezeOnboardingDataset,
  getOnboardingDataset,
  getOnboardingRun,
  listOnboardingDatasets,
  type OnboardingDataset,
  type OnboardingRun,
} from "../_lib/onboarding-quality-api";
import { PrepublishPanel, PublishedAcceptanceReminder } from "./prepublish-panel";
import { isSystemAdmin, type SessionRole } from "../_lib/role";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "./ui/alert-dialog";
import { Button } from "./ui/button";
import { Input } from "./ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "./ui/select";
import { Textarea } from "./ui/textarea";

type EmptyFilter = "";
type KnowledgeAction = "create" | "replace" | "publish" | "disable" | "delete";

interface KnowledgeConfirmRequest {
  title: string;
  description: string;
  confirmLabel: string;
  destructive?: boolean;
  run: () => Promise<void>;
}

interface KnowledgeFilters {
  status: KnowledgeDocumentStatus | EmptyFilter;
}

interface KnowledgeContextValue {
  documents: KnowledgeDocumentListItem[];
  total: number;
  filters: KnowledgeFilters;
  selectedDocumentId?: string;
  selectedDetail?: KnowledgeDocumentDetailResponse;
  retirementReadiness?: KnowledgeRetirementReadiness;
  legacyFiles: LegacyKnowledgeFile[];
  updates: KnowledgeUpdateEvent[];
  updatesLoading: boolean;
  updatesError?: string;
  selectedSnapshot?: KnowledgeSnapshot;
  snapshotLoading: boolean;
  snapshotError?: string;
  loading: boolean;
  refreshing: boolean;
  detailLoading: boolean;
  action?: KnowledgeAction;
  error?: string;
  message?: string;
  lastLoadedAt?: string;
  initialFileName?: string;
  replacementFileName?: string;
  displayName: string;
  initialEffectiveAt: string;
  replacementEffectiveAt: string;
  canPublish: boolean;
  setFilter: (key: keyof KnowledgeFilters, value: string) => void;
  setSelectedDocumentId: (documentId: string) => void;
  setDisplayName: (value: string) => void;
  setInitialEffectiveAt: (value: string) => void;
  setReplacementEffectiveAt: (value: string) => void;
  setInitialFile: (event: ChangeEvent<HTMLInputElement>) => void;
  setReplacementFile: (event: ChangeEvent<HTMLInputElement>) => void;
  uploadInitialRevision: (event: FormEvent<HTMLFormElement>) => Promise<void>;
  uploadReplacementRevision: (event: FormEvent<HTMLFormElement>) => Promise<void>;
  refresh: () => Promise<void>;
  makeupSnapshotId?: string;
  requestMakeup: (snapshotId: string) => void;
  disableDocument: () => Promise<void>;
  deleteDocument: () => Promise<void>;
  selectSnapshot: (snapshotId: string) => Promise<void>;
  handleUnauthenticated: (error: unknown) => boolean;
}

const KnowledgeContext = createContext<KnowledgeContextValue | null>(null);
const documentStatuses: KnowledgeDocumentStatus[] = ["active", "disabled", "deleted"];

export function KnowledgeWorkspaceProvider({ children, currentRole }: { children: ReactNode; currentRole?: SessionRole }) {
  const initialFileInputRef = useRef<HTMLInputElement | null>(null);
  const replacementFileInputRef = useRef<HTMLInputElement | null>(null);
  const [documents, setDocuments] = useState<KnowledgeDocumentListItem[]>([]);
  const [total, setTotal] = useState(0);
  const [filters, setFilters] = useState<KnowledgeFilters>({ status: "" });
  const [selectedDocumentId, setSelectedDocumentId] = useState<string>();
  const [selectedDetail, setSelectedDetail] = useState<KnowledgeDocumentDetailResponse>();
  const [retirementReadiness, setRetirementReadiness] = useState<KnowledgeRetirementReadiness>();
  const [legacyFiles, setLegacyFiles] = useState<LegacyKnowledgeFile[]>([]);
  const [updates, setUpdates] = useState<KnowledgeUpdateEvent[]>([]);
  const [updatesLoading, setUpdatesLoading] = useState(true);
  const [updatesError, setUpdatesError] = useState<string>();
  const [selectedSnapshot, setSelectedSnapshot] = useState<KnowledgeSnapshot>();
  const [snapshotLoading, setSnapshotLoading] = useState(false);
  const [snapshotError, setSnapshotError] = useState<string>();
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [detailLoading, setDetailLoading] = useState(false);
  const [action, setAction] = useState<KnowledgeAction>();
  const [error, setError] = useState<string>();
  const [message, setMessage] = useState<string>();
  const [lastLoadedAt, setLastLoadedAt] = useState<string>();
  const [initialFileName, setInitialFileName] = useState<string>();
  const [replacementFileName, setReplacementFileName] = useState<string>();
  const [displayName, setDisplayName] = useState("");
  const [initialEffectiveAt, setInitialEffectiveAt] = useState("");
  const [replacementEffectiveAt, setReplacementEffectiveAt] = useState("");
  const [makeupSnapshotId, setMakeupSnapshotId] = useState<string>();
  const canPublish = isSystemAdmin(currentRole);
  const [confirmRequest, setConfirmRequest] = useState<KnowledgeConfirmRequest>();
  const requestMakeup = useCallback((snapshotId: string) => {
    setMakeupSnapshotId(snapshotId);
    document.getElementById("onboarding-quality-workspace")?.scrollIntoView({ block: "nearest" });
  }, []);

  const recoverUnauthenticated = useCallback(async () => {
    await recoverFromUnauthenticated(currentKnowledgePath());
  }, []);

  const handleUnauthenticated = useCallback((requestError: unknown) => {
    if (!isUnauthenticatedError(requestError)) {
      return false;
    }
    void recoverUnauthenticated();
    return true;
  }, [recoverUnauthenticated]);

  const loadDocuments = useCallback(
    async (options?: { silent?: boolean }) => {
      if (options?.silent) {
        setRefreshing(true);
      } else {
        setLoading(true);
      }
      setError(undefined);

      try {
        const response = await listKnowledgeDocuments({ status: filters.status });
        setDocuments(response.items);
        setTotal(response.total);
        setLastLoadedAt(new Date().toISOString());
        setSelectedDocumentId((current) => {
          if (current && response.items.some((document) => document.document_id === current)) {
            return current;
          }
          return response.items[0]?.document_id;
        });
      } catch (fetchError) {
        if (handleUnauthenticated(fetchError)) {
          return;
        }
        setDocuments([]);
        setTotal(0);
        setSelectedDetail(undefined);
        setError(readError(fetchError));
      } finally {
        setLoading(false);
        setRefreshing(false);
      }
    },
    [filters.status, handleUnauthenticated],
  );

  const loadDetail = useCallback(async (documentId: string, options?: { silent?: boolean }) => {
    if (!options?.silent) {
      setDetailLoading(true);
    }

    try {
      const [detail, readiness] = await Promise.all([
        getKnowledgeDocument(documentId),
        canPublish ? getKnowledgeRetirementReadiness(documentId).then((response) => response.readiness) : Promise.resolve(undefined),
      ]);
      setSelectedDetail(detail);
      setRetirementReadiness(readiness);
    } catch (fetchError) {
      if (handleUnauthenticated(fetchError)) {
        return;
      }
      setSelectedDetail(undefined);
      setRetirementReadiness(undefined);
      setError(readError(fetchError));
    } finally {
      setDetailLoading(false);
    }
  }, [canPublish, handleUnauthenticated]);

  const loadLegacyFiles = useCallback(async () => {
    try {
      const response = await listLegacyKnowledgeFiles();
      setLegacyFiles(response.items);
    } catch (fetchError) {
      if (handleUnauthenticated(fetchError)) {
        return;
      }
      // Legacy records are a read-only compatibility aid and must not block lifecycle work.
      setLegacyFiles([]);
    }
  }, [handleUnauthenticated]);

  const loadUpdates = useCallback(async () => {
    setUpdatesLoading(true);
    setUpdatesError(undefined);
    try {
      const response = await listKnowledgeUpdates({ limit: 50 });
      setUpdates(response.items);
    } catch (fetchError) {
      if (handleUnauthenticated(fetchError)) {
        return;
      }
      setUpdates([]);
      setUpdatesError(readError(fetchError));
    } finally {
      setUpdatesLoading(false);
    }
  }, [handleUnauthenticated]);

  const selectSnapshot = useCallback(async (snapshotId: string) => {
    setSnapshotLoading(true);
    setSnapshotError(undefined);
    setSelectedSnapshot(undefined);
    try {
      const response = await getKnowledgeSnapshot(snapshotId);
      setSelectedSnapshot(response.snapshot);
    } catch (fetchError) {
      if (handleUnauthenticated(fetchError)) {
        return;
      }
      setSnapshotError(readError(fetchError));
    } finally {
      setSnapshotLoading(false);
    }
  }, [handleUnauthenticated]);

  const refresh = useCallback(async () => {
    await Promise.all([loadDocuments({ silent: true }), loadLegacyFiles(), loadUpdates()]);
    if (selectedDocumentId) {
      await loadDetail(selectedDocumentId, { silent: true });
    }
  }, [loadDetail, loadDocuments, loadLegacyFiles, loadUpdates, selectedDocumentId]);

  const reloadDocument = useCallback(
    async (documentId: string) => {
      await Promise.all([loadDocuments({ silent: true }), loadLegacyFiles(), loadUpdates()]);
      setSelectedDocumentId(documentId);
      await loadDetail(documentId, { silent: true });
    },
    [loadDetail, loadDocuments, loadLegacyFiles, loadUpdates],
  );

  useEffect(() => {
    const timer = window.setTimeout(() => {
      void loadDocuments();
      void loadLegacyFiles();
      void loadUpdates();
    }, 0);

    return () => window.clearTimeout(timer);
  }, [loadDocuments, loadLegacyFiles, loadUpdates]);

  useEffect(() => {
    const timer = window.setTimeout(() => {
      if (!selectedDocumentId) {
        setSelectedDetail(undefined);
        setRetirementReadiness(undefined);
        return;
      }
      void loadDetail(selectedDocumentId);
    }, 0);

    return () => window.clearTimeout(timer);
  }, [loadDetail, selectedDocumentId]);

  const needsPolling = useMemo(
    () =>
      documents.some((document) => isRevisionProcessing(document.current_revision)) ||
      selectedDetail?.revisions.some(
        ({ delivery, revision }) => isRevisionProcessing(revision) || isDeliveryProcessing(delivery),
      ) === true ||
      updates.some((update) => isFreshnessScanProcessing(update.freshness_scan?.status)),
    [documents, selectedDetail?.revisions, updates],
  );

  useEffect(() => {
    if (!needsPolling) {
      return;
    }

    const interval = window.setInterval(() => {
      void refresh();
    }, 2500);

    return () => window.clearInterval(interval);
  }, [needsPolling, refresh]);

  const setFilter = useCallback((key: keyof KnowledgeFilters, value: string) => {
    if (key === "status") {
      setFilters({ status: value as KnowledgeFilters["status"] });
    }
  }, []);

  const setInitialFile = useCallback((event: ChangeEvent<HTMLInputElement>) => {
    setInitialFileName(event.target.files?.[0]?.name);
    setError(undefined);
    setMessage(undefined);
  }, []);

  const setReplacementFile = useCallback((event: ChangeEvent<HTMLInputElement>) => {
    setReplacementFileName(event.target.files?.[0]?.name);
    setError(undefined);
    setMessage(undefined);
  }, []);

  const uploadInitialRevision = useCallback(
    async (event: FormEvent<HTMLFormElement>) => {
      event.preventDefault();
      const file = initialFileInputRef.current?.files?.[0];
      if (!file) {
        setError("请选择要创建为初始 revision 的文件。");
        return;
      }

      setAction("create");
      setError(undefined);
      setMessage(undefined);
      try {
        const response = await uploadKnowledgeDocument({
          file,
          displayName: displayName.trim() || undefined,
          effectiveAt: toIsoDateTime(initialEffectiveAt),
          idempotencyKey: createIdempotencyKey(),
        });
        if (initialFileInputRef.current) {
          initialFileInputRef.current.value = "";
        }
        setInitialFileName(undefined);
        setDisplayName("");
        setInitialEffectiveAt("");
        setMessage(`已创建文档并接受 revision ${response.revision.revision_id}。`);
        await reloadDocument(response.document.document_id);
      } catch (uploadError) {
        if (handleUnauthenticated(uploadError)) {
          return;
        }
        setError(readError(uploadError));
      } finally {
        setAction(undefined);
      }
    },
    [displayName, handleUnauthenticated, initialEffectiveAt, reloadDocument],
  );

  const uploadReplacementRevision = useCallback(
    async (event: FormEvent<HTMLFormElement>) => {
      event.preventDefault();
      const documentId = selectedDetail?.document.document_id;
      const file = replacementFileInputRef.current?.files?.[0];
      if (!documentId || !file) {
        setError("请选择用于替换的 revision 文件。");
        return;
      }

      setAction("replace");
      setError(undefined);
      setMessage(undefined);
      try {
        const response = await uploadKnowledgeRevision({
          documentId,
          file,
          effectiveAt: toIsoDateTime(replacementEffectiveAt),
          idempotencyKey: createIdempotencyKey(),
        });
        if (replacementFileInputRef.current) {
          replacementFileInputRef.current.value = "";
        }
        setReplacementFileName(undefined);
        setReplacementEffectiveAt("");
        setMessage(`已接受 replacement revision ${response.revision.revision_id}。`);
        await reloadDocument(response.document.document_id);
      } catch (uploadError) {
        if (handleUnauthenticated(uploadError)) {
          return;
        }
        setError(readError(uploadError));
      } finally {
        setAction(undefined);
      }
    },
    [handleUnauthenticated, reloadDocument, replacementEffectiveAt, selectedDetail?.document.document_id],
  );

  const disableDocument = useCallback(() => {
    const document = selectedDetail?.document;
    if (!document) {
      return Promise.resolve();
    }
    setConfirmRequest({
      title: "确认停用",
      description: `确认停用文档 ${document.display_name} 吗？`,
      confirmLabel: "停用文档",
      destructive: true,
      run: async () => {
        setAction("disable");
        setError(undefined);
        setMessage(undefined);
        try {
          const response = await disableKnowledgeDocument(document.document_id, createIdempotencyKey());
          setMessage(response.snapshot ? `已更新快照 ${response.snapshot.snapshot_id}。` : "文档已停用。");
          await reloadDocument(response.document.document_id);
        } catch (disableError) {
          if (handleUnauthenticated(disableError)) {
            return;
          }
          setError(readError(disableError));
        } finally {
          setAction(undefined);
        }
      },
    });
    return Promise.resolve();
  }, [handleUnauthenticated, reloadDocument, selectedDetail?.document]);

  const deleteDocument = useCallback(() => {
    const document = selectedDetail?.document;
    if (!document) {
      return Promise.resolve();
    }
    setConfirmRequest({
      title: "确认删除",
      description: `确认删除文档 ${document.display_name} 吗？历史证据会保留。`,
      confirmLabel: "删除文档",
      destructive: true,
      run: async () => {
        setAction("delete");
        setError(undefined);
        setMessage(undefined);
        try {
          const response = await deleteKnowledgeDocument(document.document_id, createIdempotencyKey());
          setMessage(`文档 ${response.document.display_name} 已删除。`);
          await reloadDocument(response.document.document_id);
        } catch (deleteError) {
          if (handleUnauthenticated(deleteError)) {
            return;
          }
          setError(readError(deleteError));
        } finally {
          setAction(undefined);
        }
      },
    });
    return Promise.resolve();
  }, [handleUnauthenticated, reloadDocument, selectedDetail?.document]);

  const value = useMemo<KnowledgeContextValue>(
    () => ({
      documents,
      total,
      filters,
      selectedDocumentId,
      selectedDetail,
      retirementReadiness,
      legacyFiles,
      updates,
      updatesLoading,
      updatesError,
      selectedSnapshot,
      snapshotLoading,
      snapshotError,
      loading,
      refreshing,
      detailLoading,
      action,
      error,
      message,
      lastLoadedAt,
      initialFileName,
      replacementFileName,
      displayName,
      initialEffectiveAt,
      replacementEffectiveAt,
      canPublish,
      setFilter,
      setSelectedDocumentId,
      setDisplayName,
      setInitialEffectiveAt,
      setReplacementEffectiveAt,
      setInitialFile,
      setReplacementFile,
      uploadInitialRevision,
      uploadReplacementRevision,
      refresh,
      makeupSnapshotId,
      requestMakeup,
      disableDocument,
      deleteDocument,
      selectSnapshot,
      handleUnauthenticated,
    }),
    [
      action,
      deleteDocument,
      detailLoading,
      disableDocument,
      displayName,
      documents,
      error,
      filters,
      initialEffectiveAt,
      initialFileName,
      lastLoadedAt,
      legacyFiles,
      loading,
      message,
      makeupSnapshotId,
      requestMakeup,
      refresh,
      refreshing,
      replacementEffectiveAt,
      canPublish,
      replacementFileName,
      selectedDetail,
      retirementReadiness,
      selectedDocumentId,
      selectedSnapshot,
      selectSnapshot,
      setFilter,
      setInitialFile,
      setReplacementFile,
      total,
      uploadInitialRevision,
      uploadReplacementRevision,
      updates,
      updatesError,
      updatesLoading,
      snapshotError,
      snapshotLoading,
      handleUnauthenticated,
    ],
  );

  return (
    <KnowledgeContext.Provider value={value}>
      <Input
        ref={initialFileInputRef}
        id="knowledge-document-file-input"
        className="visually-hidden"
        type="file"
        accept=".txt,.md,.json,.pdf,.docx,.xlsx,.pptx"
        onChange={setInitialFile}
      />
      <Input
        ref={replacementFileInputRef}
        id="knowledge-revision-file-input"
        className="visually-hidden"
        type="file"
        accept=".txt,.md,.json,.pdf,.docx,.xlsx,.pptx"
        onChange={setReplacementFile}
      />
      <AlertDialog open={Boolean(confirmRequest)} onOpenChange={(open) => { if (!open) setConfirmRequest(undefined); }}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{confirmRequest?.title}</AlertDialogTitle>
            <AlertDialogDescription>{confirmRequest?.description}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>取消</AlertDialogCancel>
            <AlertDialogAction
              variant={confirmRequest?.destructive ? "destructive" : "default"}
              onClick={() => {
                const run = confirmRequest?.run;
                setConfirmRequest(undefined);
                if (run) {
                  void run();
                }
              }}
            >
              {confirmRequest?.confirmLabel ?? "确认"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
      {children}
    </KnowledgeContext.Provider>
  );
}

function FilePickerButton({ htmlFor, children }: { htmlFor: string; children: ReactNode }) {
  return (
    <Button
      variant="outline"
      size="sm"
      className="h-auto"
      nativeButton={false}
      render={<label htmlFor={htmlFor} />}
    >
      {children}
    </Button>
  );
}

export function KnowledgeWorkspace({ onOpenDetails }: { onOpenDetails?: () => void } = {}) {
  const knowledge = useKnowledgeContext();

  return (
    <div className="content-stack" data-testid="knowledge-lifecycle-workspace">
      <header className="page-header">
        <div>
          <p className="eyebrow">知识库工作区</p>
          <h2>文档与版本生命周期</h2>
        </div>
        <div className="header-chip-row">
          <Button type="button" variant="outline" size="sm" onClick={knowledge.refresh} disabled={knowledge.refreshing || knowledge.loading}>
            {knowledge.refreshing ? "刷新中" : "刷新"}
          </Button>
          <span className="header-chip">{knowledge.total} 个文档</span>
          {onOpenDetails ? <Button type="button" variant="outline" size="sm" data-testid="open-knowledge-details" onClick={onOpenDetails} disabled={!knowledge.selectedDocumentId}>查看详情</Button> : null}
        </div>
      </header>

      <form className="upload-zone lifecycle-upload-zone" onSubmit={knowledge.uploadInitialRevision}>
        <div className="upload-copy">
          <h3>创建文档</h3>
          <p>{knowledge.initialFileName ? `初始 revision: ${knowledge.initialFileName}` : "选择初始内容后创建逻辑文档"}</p>
          {knowledge.error ? <p className="error-copy" role="alert">{knowledge.error}</p> : null}
          {knowledge.message ? <p className="success-copy" role="status">{knowledge.message}</p> : null}
        </div>
        <div className="upload-actions lifecycle-form-fields">
          <label>
            <span>显示名称</span>
            <Input
              value={knowledge.displayName}
              onChange={(event) => knowledge.setDisplayName(event.target.value)}
              placeholder="默认使用文件名"
              maxLength={240}
            />
          </label>
          <label>
            <span>生效时间</span>
            <Input
              type="datetime-local"
              value={knowledge.initialEffectiveAt}
              onChange={(event) => knowledge.setInitialEffectiveAt(event.target.value)}
            />
          </label>
          <div className="button-row">
            <FilePickerButton htmlFor="knowledge-document-file-input">选择文件</FilePickerButton>
            <Button type="submit" data-testid="knowledge-create-submit" disabled={knowledge.action === "create" || !knowledge.initialFileName}>
              {knowledge.action === "create" ? "创建中" : "创建文档"}
            </Button>
          </div>
        </div>
      </form>

      <section className="filter-card lifecycle-filter-card">
        <label>
          <span>文档状态</span>
          <Select
            value={knowledge.filters.status || "all"}
            onValueChange={(value) => knowledge.setFilter("status", !value || value === "all" ? "" : value)}
          >
            <SelectTrigger className="w-full min-w-36" aria-label="文档状态">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">全部</SelectItem>
              {documentStatuses.map((status) => (
                <SelectItem key={status} value={status}>
                  {status}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </label>
        <p>{knowledge.lastLoadedAt ? `最近刷新 ${formatDateTime(knowledge.lastLoadedAt)}` : "等待首次刷新"}</p>
      </section>

      <section className="table-card">
        <div className="section-heading">
          <h3>逻辑文档</h3>
          <span>{knowledge.loading ? "加载中" : `${knowledge.documents.length} 条可见`}</span>
        </div>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>文档</th>
                <th>状态</th>
                <th>当前 revision</th>
                <th>最近更新</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              {knowledge.documents.map((document) => (
                <tr key={document.document_id} className={document.document_id === knowledge.selectedDocumentId ? "is-selected-row" : undefined}>
                  <td>
                    <strong>{document.display_name}</strong>
                    <span>{document.document_id}</span>
                  </td>
                  <td><StatusBadge status={document.status} /></td>
                  <td>
                    {document.current_revision ? (
                      <>
                        <strong>{document.current_revision.filename}</strong>
                        <span><StatusBadge status={document.current_revision.status} /></span>
                        {document.acceptance?.display_label ? <span data-testid="knowledge-acceptance-label">{document.acceptance.display_label}</span> : null}
                      </>
                    ) : "-"}
                  </td>
                  <td>{formatDateTime(document.updated_at)}</td>
                  <td>
                    <div className="table-actions">
                      <Button type="button" variant="outline" size="sm" onClick={() => { knowledge.setSelectedDocumentId(document.document_id); onOpenDetails?.(); }}>
                        查看版本
                      </Button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        {!knowledge.loading && knowledge.documents.length === 0 ? (
          <div className="table-empty-state">
            <h3>暂无逻辑文档。</h3>
            <p>创建第一个文档后，初始 revision 会进入投影队列。</p>
          </div>
        ) : null}
      </section>

      <KnowledgeUpdateCalendar
        updates={knowledge.updates}
        loading={knowledge.updatesLoading}
        error={knowledge.updatesError}
        onSelectSnapshot={(snapshotId) => { onOpenDetails?.(); return knowledge.selectSnapshot(snapshotId); }}
      />

      <OnboardingQualityWorkspace />

      <LegacyCompatibilityView files={knowledge.legacyFiles} />
    </div>
  );
}

function OnboardingQualityWorkspace() {
  const knowledge = useKnowledgeContext();
  const { handleUnauthenticated } = knowledge;
  const [datasets, setDatasets] = useState<OnboardingDataset[]>([]);
  const [selected, setSelected] = useState<OnboardingDataset>();
  const [lastRun, setLastRun] = useState<OnboardingRun>();
  const [name, setName] = useState("");
  const [snapshotID, setSnapshotID] = useState("");
  const [appliedMakeupId, setAppliedMakeupId] = useState<string | undefined>();
  if (knowledge.makeupSnapshotId && knowledge.makeupSnapshotId !== appliedMakeupId) {
    setAppliedMakeupId(knowledge.makeupSnapshotId);
    setSnapshotID(knowledge.makeupSnapshotId);
  }
  const [caseType, setCaseType] = useState<"must_answer" | "must_refuse">("must_answer");
  const [question, setQuestion] = useState("");
  const [revisionIDs, setRevisionIDs] = useState("");
  const [locatorHints, setLocatorHints] = useState("");
  const [answerPoints, setAnswerPoints] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string>();
  const [message, setMessage] = useState<string>();

  const reportRequestError = useCallback((reason: unknown) => {
    if (!handleUnauthenticated(reason)) {
      setError(readError(reason));
    }
  }, [handleUnauthenticated]);

  const refreshList = useCallback(async () => {
    const response = await listOnboardingDatasets();
    setDatasets(response.items);
  }, []);
  useEffect(() => {
    const timer = window.setTimeout(() => { void refreshList().catch(reportRequestError); }, 0);
    return () => window.clearTimeout(timer);
  }, [refreshList, reportRequestError]);
  const select = async (datasetID: string) => {
    setLoading(true); setError(undefined);
    try { setSelected((await getOnboardingDataset(datasetID)).dataset); setLastRun(undefined); } catch (reason) { reportRequestError(reason); } finally { setLoading(false); }
  };
  const createDataset = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault(); setLoading(true); setError(undefined); setMessage(undefined);
    try {
      const response = await createOnboardingDataset({ name: requiredText(name, "名称"), knowledge_snapshot_id: requiredText(snapshotID, "已发布 snapshot id") });
      setSelected(response.dataset); setName(""); setSnapshotID(""); await refreshList(); setMessage("已创建草稿。请录入经业务审核的真值用例。");
    } catch (reason) { reportRequestError(reason); } finally { setLoading(false); }
  };
  const createCase = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault(); if (!selected) return; setLoading(true); setError(undefined); setMessage(undefined);
    try {
      await createOnboardingCase(selected.dataset_id, caseType === "must_refuse" ? { case_type: caseType, question: requiredText(question, "问题"), review_status: "reviewed" } : { case_type: caseType, question: requiredText(question, "问题"), expected_revision_ids: requiredList(revisionIDs, "期望 revision"), expected_locator_hints: requiredList(locatorHints, "定位提示"), expected_answer_points: requiredList(answerPoints, "答案要点"), review_status: "reviewed" });
      setQuestion(""); setRevisionIDs(""); setLocatorHints(""); setAnswerPoints(""); await select(selected.dataset_id); setMessage("已添加 reviewed 真值用例。");
    } catch (reason) { reportRequestError(reason); } finally { setLoading(false); }
  };
  const freeze = async () => {
    if (!selected) return; setLoading(true); setError(undefined);
    try { const response = await freezeOnboardingDataset(selected.dataset_id); setSelected(response.dataset); await refreshList(); setMessage("数据集已冻结，题目和真值不再可改动。"); } catch (reason) { reportRequestError(reason); } finally { setLoading(false); }
  };
  const run = async () => {
    if (!selected) return; setLoading(true); setError(undefined);
    try { const response = await createOnboardingRun(selected.dataset_id); setLastRun(response.run); setMessage("已排入固定 snapshot 与固定配置的受控评测队列。"); } catch (reason) { reportRequestError(reason); } finally { setLoading(false); }
  };
  const refreshRun = async () => { if (!lastRun) return; setLoading(true); try { setLastRun((await getOnboardingRun(lastRun.run_id)).run); } catch (reason) { reportRequestError(reason); } finally { setLoading(false); } };

  return <section className="quality-operations" id="onboarding-quality-workspace" data-testid="onboarding-quality-workspace">
    <div className="section-heading"><div><p className="eyebrow">上线质量门</p><h3>新知识验收</h3></div><span>上传成功不等于回答正确</span></div>
    <p className="muted-copy">可对待发布或已发布 snapshot 录入并审核真值问题。系统将固定 snapshot、模型与检索配置实际运行；只有全通过才可作为退役证据。验收是发布的推荐项，不会自动发布配置，也不会阻断聊天或继续上传。</p>
    {error ? <p className="error-copy" role="alert">{error}</p> : null}{message ? <p className="success-copy" role="status">{message}</p> : null}
    <div className="quality-grid">
      <form className="config-form quality-form" onSubmit={createDataset}>
        <h4>1. 创建验收集</h4>
        <label>
          <span>名称</span>
          <Input value={name} onChange={(event) => setName(event.target.value)} placeholder="例如：客户政策 2026-07" />
        </label>
        <label>
          <span>待发布或已发布 snapshot id</span>
          <Input value={snapshotID} onChange={(event) => setSnapshotID(event.target.value)} placeholder="从更新日历或待发布快照复制" />
        </label>
        <Button type="submit" disabled={loading}>创建草稿</Button>
      </form>
      <div className="version-list quality-list">
        <h4>验收集</h4>
        {datasets.map((dataset) => (
          <Button
            key={dataset.dataset_id}
            type="button"
            variant="ghost"
            className="evidence-list-row h-auto whitespace-normal"
            aria-pressed={selected?.dataset_id === dataset.dataset_id}
            onClick={() => void select(dataset.dataset_id)}
          >
            <span><strong>{dataset.name}</strong><small>{dataset.dataset_id} · {dataset.knowledge_snapshot_id}</small></span>
            <StatusBadge status={dataset.status} />
          </Button>
        ))}
        {datasets.length === 0 ? <p className="muted-copy">尚未创建。</p> : null}
      </div>
    </div>
    {selected ? (
      <div className="quality-grid">
        <form className="config-form quality-form" onSubmit={createCase}>
          <h4>2. 审核真值用例</h4>
          <p className="muted-copy">当前 {selected.status} · {selected.cases?.length ?? 0} 题</p>
          <label>
            <span>类型</span>
            <Select
              value={caseType}
              disabled={selected.status !== "draft" || loading}
              onValueChange={(value) => setCaseType((value ?? "must_answer") as "must_answer" | "must_refuse")}
            >
              <SelectTrigger className="w-full" aria-label="类型">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="must_answer">必须回答并引用</SelectItem>
                <SelectItem value="must_refuse">必须拒答</SelectItem>
              </SelectContent>
            </Select>
          </label>
          <label>
            <span>问题</span>
            <Textarea value={question} disabled={selected.status !== "draft" || loading} onChange={(event) => setQuestion(event.target.value)} />
          </label>
          {caseType === "must_answer" ? (
            <>
              <label>
                <span>期望 revision（逗号分隔）</span>
                <Input value={revisionIDs} disabled={selected.status !== "draft" || loading} onChange={(event) => setRevisionIDs(event.target.value)} />
              </label>
              <label>
                <span>定位提示（逗号分隔）</span>
                <Input value={locatorHints} disabled={selected.status !== "draft" || loading} onChange={(event) => setLocatorHints(event.target.value)} />
              </label>
              <label>
                <span>答案要点（逗号分隔）</span>
                <Input value={answerPoints} disabled={selected.status !== "draft" || loading} onChange={(event) => setAnswerPoints(event.target.value)} />
              </label>
            </>
          ) : null}
          <Button type="submit" disabled={selected.status !== "draft" || loading}>添加 reviewed 用例</Button>
        </form>
        <div className="quality-experiment-detail">
          <h4>3. 冻结并运行</h4>
          <dl className="config-list">
            <div><dt>snapshot</dt><dd>{selected.knowledge_snapshot_id}</dd></div>
            <div><dt>manifest</dt><dd>{selected.knowledge_snapshot_manifest_hash}</dd></div>
            <div><dt>case hash</dt><dd>{selected.cases_sha256 || "尚未冻结"}</dd></div>
          </dl>
          <div className="quality-actions">
            {selected.status === "draft" ? (
              <Button type="button" onClick={() => void freeze()} disabled={loading}>冻结真值集</Button>
            ) : (
              <Button type="button" onClick={() => void run()} disabled={loading}>运行质量门</Button>
            )}
          </div>
          {lastRun ? (
            <div className="detail-task-card">
              <div className="section-heading"><strong>{lastRun.run_id}</strong><StatusBadge status={lastRun.status} /></div>
              <p>配置：{lastRun.config_version} / {lastRun.retrieval_config_version}</p>
              {lastRun.status === "queued" || lastRun.status === "running" ? (
                <Button type="button" variant="outline" size="sm" onClick={() => void refreshRun()} disabled={loading}>刷新结果</Button>
              ) : null}
              {lastRun.error ? <p className="error-copy">{lastRun.error.code}: {lastRun.error.message}</p> : null}
              {lastRun.results?.map((result) => (
                <div key={result.case_id}>
                  <p>{result.passed ? "通过" : "失败"} · {result.case_id}{result.failure_code ? ` · ${result.failure_code}` : ""}</p>
                  <p>{result.observed_answer}</p>
                  <small>revision：{String(result.expected_revision_hit)} · locator：{String(result.expected_locator_hit)} · 要点覆盖：{result.answer_point_hit_rate ?? "-"}</small>
                  {result.observed_citations?.map((citation, index) => (
                    <small key={`${citation.revision_id}-${index}`}>引用：{citation.revision_id ?? "-"} · {citation.citation_label ?? citation.snippet ?? "-"}</small>
                  ))}
                </div>
              ))}
            </div>
          ) : null}
        </div>
      </div>
    ) : null}
  </section>;
}

export function KnowledgeDetailWorkspace() {
  const knowledge = useKnowledgeContext();
  const detail = knowledge.selectedDetail?.document.document_id === knowledge.selectedDocumentId ? knowledge.selectedDetail : undefined;
  const document = detail?.document;
  const hasInflightRevision = detail?.revisions.some(({ revision }) => isRevisionProcessing(revision)) ?? false;

  return (
    <div className="content-stack detail-stack" data-testid="knowledge-document-detail">
      <header className="page-header compact">
        <div>
          <p className="eyebrow">知识库详情</p>
          <h2>文档与 revisions</h2>
        </div>
      </header>

      <KnowledgeSnapshotDetail
        snapshot={knowledge.selectedSnapshot}
        loading={knowledge.snapshotLoading}
        error={knowledge.snapshotError}
      />

      {document ? (
        <>
          <section className="detail-summary">
            <h3>{document.display_name}</h3>
            <p>{document.document_id}</p>
          </section>
          <dl className="config-list">
            <div>
              <dt>document status</dt>
              <dd><StatusBadge status={document.status} /></dd>
            </div>
            <div>
              <dt>owner_id</dt>
              <dd>{document.owner_id}</dd>
            </div>
            <div>
              <dt>active_snapshot_id</dt>
              <dd>{detail.active_snapshot_id ?? "-"}</dd>
            </div>
            <div>
              <dt>updated_at</dt>
              <dd>{formatDateTime(document.updated_at)}</dd>
            </div>
          </dl>

          {document.status === "active" ? (
            <form className="detail-task-card lifecycle-revision-form" onSubmit={knowledge.uploadReplacementRevision}>
              <div className="section-heading">
                <h3>创建 replacement revision</h3>
                <span>{knowledge.replacementFileName ?? "未选择文件"}</span>
              </div>
              <label>
                <span>生效时间</span>
                <Input
                  type="datetime-local"
                  value={knowledge.replacementEffectiveAt}
                  onChange={(event) => knowledge.setReplacementEffectiveAt(event.target.value)}
                />
              </label>
              <div className="button-row">
                <FilePickerButton htmlFor="knowledge-revision-file-input">选择 replacement 文件</FilePickerButton>
                <Button type="submit" disabled={knowledge.action === "replace" || !knowledge.replacementFileName}>
                  {knowledge.action === "replace" ? "提交中" : "提交 revision"}
                </Button>
              </div>
            </form>
          ) : null}

          <section className="table-card lifecycle-revision-table">
            <div className="section-heading">
              <h3>Immutable revisions</h3>
              <span>{knowledge.detailLoading ? "刷新中" : `${detail.revisions.length} 个 revision`}</span>
            </div>
            <div className="table-wrap">
              <table>
                <thead>
                  <tr>
                    <th>revision</th>
                    <th>投影状态</th>
                    <th>版本事实</th>
                    <th>快照</th>
                    <th>操作</th>
                  </tr>
                </thead>
                <tbody>
                  {detail.revisions.map(({ revision, delivery, in_active_snapshot }) => (
                    <tr key={revision.revision_id}>
                      <td>
                        <strong>{revision.filename}</strong>
                        <span>{revision.revision_id}</span>
                      </td>
                      <td><RevisionDeliveryFacts revision={revision} delivery={delivery} /></td>
                      <td><RevisionFacts revision={revision} /></td>
                      <td>{in_active_snapshot ? "当前快照成员" : "-"}</td>
                      <td>
                        {revision.status === "ready" && document.status === "active" ? <span className="muted-copy" data-testid="waiting-for-publish">等待系统管理员发布</span> : "-"}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </section>

          <PublishedAcceptanceReminder acceptance={detail.acceptance} snapshotId={detail.active_snapshot_id} onMakeup={knowledge.requestMakeup} />
          {detail.pending_snapshot ? (
            <PrepublishPanel
              mode={knowledge.canPublish ? "publish" : "wait"}
              snapshot={detail.pending_snapshot}
              onMakeup={knowledge.requestMakeup}
              onReload={() => knowledge.refresh()}
              onActivate={async (acknowledgeUnvalidated) => {
                if (!detail.pending_snapshot) return;
                await activateKnowledgeSnapshot(detail.pending_snapshot.snapshot_id, createIdempotencyKey(), acknowledgeUnvalidated);
                await knowledge.refresh();
              }}
            />
          ) : null}

          <section className="detail-task-card lifecycle-danger-zone">
            <div className="section-heading">
              <h3>文档状态操作</h3>
              <StatusBadge status={document.status} />
            </div>
            <p className="muted-copy">系统管理员必须先发布替代知识，并对包含旧知识与新知识的当前快照取得 passing onboarding run，才可停用。停用后还需在不含旧知识的 new-only 快照再次通过，才可删除；删除不会改写历史回答或引用。</p>
            {knowledge.canPublish && knowledge.retirementReadiness ? (
              <RetirementReadinessPanel readiness={knowledge.retirementReadiness} />
            ) : null}
            {!knowledge.canPublish ? <p className="muted-copy">停用与删除须在通过新知识验收后，由系统管理员在系统配置台审批。</p> : null}
            {document.status === "active" && knowledge.canPublish ? (
              <Button
                type="button"
                variant="destructive"
                onClick={knowledge.disableDocument}
                disabled={knowledge.action === "disable" || hasInflightRevision || knowledge.retirementReadiness?.stage !== "ready_to_disable"}
              >
                {knowledge.action === "disable" ? "停用中" : "停用文档"}
              </Button>
            ) : null}
            {document.status === "active" && hasInflightRevision ? <p className="muted-copy">当前有 revision 正在投影，完成前不可停用。</p> : null}
            {document.status === "disabled" && knowledge.canPublish ? (
              <Button type="button" variant="destructive" onClick={knowledge.deleteDocument} disabled={knowledge.action === "delete" || knowledge.retirementReadiness?.stage !== "ready_to_delete"}>
                {knowledge.action === "delete" ? "删除中" : "删除文档"}
              </Button>
            ) : null}
            {document.status === "deleted" ? <p className="muted-copy">文档已删除，历史 evidence 保留在服务端。</p> : null}
          </section>
        </>
      ) : !knowledge.selectedSnapshot && !knowledge.snapshotLoading ? (
        <section className="detail-empty-state">
          <h3>暂无文档详情。</h3>
          <p>{knowledge.error ? "文档服务暂不可用。" : "从左侧选择一个逻辑文档查看其 immutable revisions。"}</p>
        </section>
      ) : null}
    </div>
  );
}

function RetirementReadinessPanel({ readiness }: { readiness: KnowledgeRetirementReadiness }) {
  const ready = readiness.stage !== "blocked";
  const action = readiness.stage === "ready_to_disable" ? "可停用" : readiness.stage === "ready_to_delete" ? "可删除" : "尚不可退役";
  return <div className="detail-task-card">
    <div className="section-heading"><strong>退役安全门</strong><StatusBadge status={ready ? "passed" : "blocked"} /></div>
    <p>{action} · 当前快照 {readiness.active_snapshot_id ?? "-"}</p>
    {readiness.passing_onboarding_run_id ? <p className="muted-copy">通过证据：{readiness.passing_onboarding_run_id}</p> : null}
    {readiness.blockers.length > 0 ? <ul className="muted-copy">{readiness.blockers.map((blocker) => <li key={blocker}>{blocker}</li>)}</ul> : null}
  </div>;
}

function KnowledgeUpdateCalendar({
  updates,
  loading,
  error,
  onSelectSnapshot,
}: {
  updates: KnowledgeUpdateEvent[];
  loading: boolean;
  error?: string;
  onSelectSnapshot: (snapshotId: string) => Promise<void>;
}) {
  return (
    <section className="table-card knowledge-update-calendar" data-testid="knowledge-update-calendar">
      <div className="section-heading">
        <h3>知识更新日历</h3>
        <span>{loading ? "加载中" : `${updates.length} 个事件`}</span>
      </div>
      {error ? <p className="inline-error" role="alert">{error}</p> : null}
      <div className="table-wrap">
        <table>
          <thead>
            <tr>
              <th>发生时间</th>
              <th>事件</th>
              <th>文档 / revision</th>
              <th>快照</th>
              <th>时效扫描</th>
            </tr>
          </thead>
          <tbody>
            {updates.map((update) => (
              <tr key={update.event_id}>
                <td>{formatDateTime(update.occurred_at)}</td>
                <td><StatusBadge status={update.event_type} /></td>
                <td>
                  <div className="lifecycle-cell-stack lifecycle-fact-stack">
                    <span>{update.document_id ?? "-"}</span>
                    {update.revision_id ? <span>{update.revision_id}</span> : null}
                  </div>
                </td>
                <td>
                  {update.snapshot_id ? (
                    <Button
                      type="button"
                      variant="link"
                      className="table-link-button h-auto whitespace-normal px-0"
                      data-testid="knowledge-update-snapshot"
                      onClick={() => void onSelectSnapshot(update.snapshot_id!)}
                    >
                      {update.snapshot_id}
                      {update.acceptance?.display_label ? <span data-testid="knowledge-acceptance-label">{update.acceptance.display_label}</span> : null}
                    </Button>
                  ) : "-"}
                </td>
                <td><FreshnessScanFacts event={update} /></td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {!loading && updates.length === 0 && !error ? (
        <div className="table-empty-state">
          <h3>暂无知识更新事件。</h3>
          <p>发布、停用或删除影响可见知识后，会在这里显示时效扫描进度。</p>
        </div>
      ) : null}
    </section>
  );
}

function FreshnessScanFacts({ event }: { event: KnowledgeUpdateEvent }) {
  const scan = event.freshness_scan;
  if (!scan) {
    return <span className="muted-copy">无时效扫描</span>;
  }

  return (
    <div className="lifecycle-cell-stack lifecycle-fact-stack" data-testid="knowledge-freshness-scan">
      <StatusBadge status={scan.status} />
      <span>候选 {scan.candidate_count} · 标记 {scan.marked_count}</span>
      <span>尝试 {scan.attempt_count} · 更新 {formatDateTime(scan.updated_at)}</span>
      {scan.last_error ? <span className="error-copy">{scan.last_error}</span> : null}
    </div>
  );
}

function KnowledgeSnapshotDetail({
  snapshot,
  loading,
  error,
}: {
  snapshot?: KnowledgeSnapshot;
  loading: boolean;
  error?: string;
}) {
  if (!snapshot && !loading && !error) {
    return null;
  }

  return (
    <section className="detail-task-card knowledge-snapshot-detail" data-testid="knowledge-snapshot-detail">
      <div className="section-heading">
        <h3>Immutable snapshot</h3>
        {snapshot ? <StatusBadge status={snapshot.status} /> : <span>{loading ? "加载中" : "不可用"}</span>}
      </div>
      {error ? <p className="inline-error" role="alert">{error}</p> : null}
      {snapshot ? (
        <>
          <dl className="config-list">
            <div><dt>snapshot_id</dt><dd>{snapshot.snapshot_id}</dd></div>
            <div><dt>manifest</dt><dd>{snapshot.manifest_sha256}</dd></div>
            <div><dt>published_at</dt><dd>{formatDateTime(snapshot.published_at)}</dd></div>
            {snapshot.acceptance?.display_label ? <div><dt>验收状态</dt><dd data-testid="knowledge-acceptance-label">{snapshot.acceptance.display_label}</dd></div> : null}
          </dl>
          <details>
            <summary>查看成员 ({snapshot.members.length})</summary>
            <div className="snapshot-member-list">
              {snapshot.members.map((member) => (
                <div key={member.revision_id}>
                  <strong>{member.document_id}</strong>
                  <span>revision: {member.revision_id}</span>
                  <span>checksum: {member.checksum_sha256}</span>
                </div>
              ))}
            </div>
          </details>
        </>
      ) : null}
    </section>
  );
}

function LegacyCompatibilityView({ files }: { files: LegacyKnowledgeFile[] }) {
  return (
    <section className="table-card legacy-compatibility" data-testid="knowledge-legacy-compatibility">
      <div className="section-heading">
        <h3>Legacy 文件兼容记录</h3>
        <span>只读 · {files.length} 条</span>
      </div>
      <details>
        <summary>查看 legacy file records</summary>
        {files.length > 0 ? (
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>文件</th>
                  <th>格式</th>
                  <th>状态</th>
                  <th>上传时间</th>
                </tr>
              </thead>
              <tbody>
                {files.map((file) => (
                  <tr key={file.file_id}>
                    <td><strong>{file.filename}</strong><span>{file.file_id}</span></td>
                    <td>{file.source_format}</td>
                    <td><StatusBadge status={file.status} /></td>
                    <td>{formatDateTime(file.uploaded_at)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : <p className="muted-copy">暂无 legacy file records。</p>}
      </details>
    </section>
  );
}

function RevisionDeliveryFacts({ revision, delivery }: { revision: KnowledgeRevision; delivery?: RevisionDeliverySummary }) {
  return (
    <div className="lifecycle-cell-stack">
      <StatusBadge status={revision.status} />
      {delivery ? <span>delivery: <StatusBadge status={delivery.status} /></span> : <span>delivery: -</span>}
      {delivery ? <span>attempts: {delivery.attempt_count}</span> : null}
      {delivery?.last_error ? <span className="error-copy">{delivery.last_error}</span> : null}
    </div>
  );
}

function RevisionFacts({ revision }: { revision: KnowledgeRevision }) {
  return (
    <div className="lifecycle-cell-stack lifecycle-fact-stack">
      <span>{revision.source_format} · {formatDateTime(revision.created_at)}</span>
      <span>parse: {revision.parse_version}</span>
      <span>index: {revision.index_version}</span>
      {revision.embedding_model ? <span>embedding: {revision.embedding_model}</span> : null}
      {revision.effective_at ? <span>effective: {formatDateTime(revision.effective_at)}</span> : null}
      {revision.chunk_manifest_sha256 ? <span>manifest: {revision.chunk_manifest_sha256}</span> : null}
      <span>checksum: {revision.checksum_sha256}</span>
    </div>
  );
}

function StatusBadge({ status }: { status: string }) {
  return <span className={`status-badge status-${status.replaceAll("_", "-")}`}>{status}</span>;
}

function isRevisionProcessing(revision?: KnowledgeRevision) {
  return revision?.status === "uploaded" || revision?.status === "queued" || revision?.status === "indexing";
}

function isDeliveryProcessing(delivery?: RevisionDeliverySummary) {
  return delivery?.status === "pending" || delivery?.status === "streamed" || delivery?.status === "indexing" || delivery?.status === "retry";
}

function isFreshnessScanProcessing(status?: string) {
  return status === "queued" || status === "running" || status === "retry";
}

function toIsoDateTime(value: string) {
  if (!value) {
    return undefined;
  }
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? undefined : date.toISOString();
}

function useKnowledgeContext() {
  const context = useContext(KnowledgeContext);
  if (!context) {
    throw new Error("Knowledge workspace must be rendered inside KnowledgeWorkspaceProvider");
  }
  return context;
}

function readError(error: unknown) {
  return error instanceof Error ? error.message : "知识库请求失败。";
}

function isUnauthenticatedError(error: unknown) {
  return (
    (error instanceof KnowledgeApiError || error instanceof RequestApiError)
    && error.code === "unauthenticated"
  );
}

function currentKnowledgePath() {
  return `${window.location.pathname}${window.location.search}`;
}

function requiredText(value: string, label: string) {
  const result = value.trim();
  if (!result) {
    throw new Error(`${label}为必填项。`);
  }
  return result;
}

function requiredList(value: string, label: string) {
  const result = value.split(",").map((item) => item.trim()).filter(Boolean);
  if (result.length === 0) {
    throw new Error(`${label}至少需要一项。`);
  }
  return result;
}
