export type SourceFormat = "txt" | "md" | "json" | "pdf" | "docx" | "xlsx" | "pptx";
export type FileStatus = "uploaded" | "queued" | "running" | "succeeded" | "failed" | "deleted";
export type MessageRole = "system" | "user" | "assistant";
export type MessageStatus = "pending" | "streaming" | "completed" | "failed";
export type FinishReason = "stop" | "length" | "refusal" | "error";

export interface Conversation {
  conversation_id: string;
  owner_id: string;
  title: string;
  created_at: string;
  updated_at: string;
  config_version: string;
  retrieval_config_version: string;
  knowledge_snapshot_id: string;
  model_id: string;
}

export interface Message {
  message_id: string;
  conversation_id: string;
  role: MessageRole;
  content: string;
  status: MessageStatus;
  created_at: string;
  finish_reason?: FinishReason;
  citations_count?: number;
}

export interface SourceLocator {
  source_format: SourceFormat;
  page_no?: number;
  heading_path?: string[];
  paragraph_index?: number;
  sheet_name?: string;
  cell_range?: string;
  slide_no?: number;
  slide_title?: string;
  block_label?: string;
  json_path?: string;
}

export interface Citation {
  message_id: string;
  chunk_id: string;
  file_id: string;
  filename: string;
  rank: number;
  score: number;
  citation_label: string;
  source_locator: SourceLocator;
  snippet: string;
}

export interface KnowledgeFile {
  file_id: string;
  filename: string;
  mime_type: string;
  source_format: SourceFormat;
  status: FileStatus;
  uploaded_by: string;
  uploaded_at: string;
  object_uri: string;
  parse_version: string;
  index_version: string;
}

export interface IngestionTaskSummary {
  task_id: string;
  file_id: string;
  status: "queued" | "running" | "succeeded" | "failed";
  requested_at: string;
  updated_at: string;
  force_reindex?: boolean;
  error?: {
    code: string;
    message: string;
    retryable?: boolean;
  };
}

export interface AgentConfig {
  config_version: string;
  model_id: string;
  system_prompt_version: string;
  temperature: number;
  max_output_tokens: number;
  created_at: string;
}

export interface RetrievalConfig {
  retrieval_config_version: string;
  retrieval_strategy: "lexical_compat_v1" | "native_dense_v1";
  embedding_model: string;
  top_k: number;
  rerank_enabled: boolean;
  evidence_min_score: number;
  created_at: string;
}

export interface ConfigVersionSummary {
  version_id: string;
  kind: "agent" | "retrieval";
  created_at: string;
}

export const sourceFormats: SourceFormat[] = ["txt", "md", "json", "pdf", "docx", "xlsx", "pptx"];

export const conversations: Conversation[] = [
  {
    conversation_id: "conv_policy_rollout",
    owner_id: "user_admin",
    title: "Policy rollout FAQ",
    created_at: "2026-06-20T01:12:00Z",
    updated_at: "2026-06-20T02:28:00Z",
    config_version: "cfg_2026_06_18",
    retrieval_config_version: "ret_2026_06_19",
    knowledge_snapshot_id: "ks_2026_06_20_01",
    model_id: "qwen2.5:14b-instruct",
  },
  {
    conversation_id: "conv_expense_audit",
    owner_id: "user_admin",
    title: "Expense audit checklist",
    created_at: "2026-06-19T22:40:00Z",
    updated_at: "2026-06-20T00:04:00Z",
    config_version: "cfg_2026_06_18",
    retrieval_config_version: "ret_2026_06_19",
    knowledge_snapshot_id: "ks_2026_06_19_03",
    model_id: "qwen2.5:14b-instruct",
  },
  {
    conversation_id: "conv_vendor_onboarding",
    owner_id: "user_admin",
    title: "Vendor onboarding packet",
    created_at: "2026-06-18T06:20:00Z",
    updated_at: "2026-06-19T08:45:00Z",
    config_version: "cfg_2026_06_12",
    retrieval_config_version: "ret_2026_06_16",
    knowledge_snapshot_id: "ks_2026_06_18_02",
    model_id: "deepseek-r1:32b",
  },
];

export const defaultConversationId = conversations[0]?.conversation_id ?? "";

const messagesByConversation: Record<string, Message[]> = {
  conv_policy_rollout: [
    {
      message_id: "msg_001",
      conversation_id: "conv_policy_rollout",
      role: "user",
      content: "帮我总结 7 月政策发布的关键变更，以及员工最容易误解的两条。",
      status: "completed",
      created_at: "2026-06-20T02:16:00Z",
    },
    {
      message_id: "msg_002",
      conversation_id: "conv_policy_rollout",
      role: "assistant",
      content:
        "目前草案里最关键的是差旅审批从事后补录改成出发前审批，以及供应商例外采购必须补齐审批链。容易误解的两点是“紧急采购不等于免审批”和“旧会话不会自动切换到新配置快照”。",
      status: "completed",
      created_at: "2026-06-20T02:17:00Z",
      finish_reason: "stop",
      citations_count: 2,
    },
    {
      message_id: "msg_003",
      conversation_id: "conv_policy_rollout",
      role: "user",
      content: "再给我一个 30 秒口头同步版本。",
      status: "completed",
      created_at: "2026-06-20T02:20:00Z",
    },
    {
      message_id: "msg_004",
      conversation_id: "conv_policy_rollout",
      role: "assistant",
      content:
        "可以这样说：7 月版本最重要的是把审批前置，并把知识快照和配置版本绑定到新会话。前者减少补救成本，后者避免答案被新规则回写污染。",
      status: "streaming",
      created_at: "2026-06-20T02:21:00Z",
      citations_count: 1,
    },
  ],
  conv_expense_audit: [
    {
      message_id: "msg_101",
      conversation_id: "conv_expense_audit",
      role: "user",
      content: "报销审计最少要抽查哪些字段？",
      status: "completed",
      created_at: "2026-06-20T00:00:00Z",
    },
    {
      message_id: "msg_102",
      conversation_id: "conv_expense_audit",
      role: "assistant",
      content: "至少核对金额、成本中心、审批链、原始附件和供应商抬头的一致性。",
      status: "completed",
      created_at: "2026-06-20T00:01:00Z",
      finish_reason: "stop",
      citations_count: 1,
    },
  ],
  conv_vendor_onboarding: [
    {
      message_id: "msg_201",
      conversation_id: "conv_vendor_onboarding",
      role: "user",
      content: "供应商入场前的必备资料有哪些？",
      status: "completed",
      created_at: "2026-06-19T08:40:00Z",
    },
    {
      message_id: "msg_202",
      conversation_id: "conv_vendor_onboarding",
      role: "assistant",
      content: "需要营业执照、税务信息、合规承诺书和数据处理协议。",
      status: "completed",
      created_at: "2026-06-19T08:41:00Z",
      finish_reason: "stop",
      citations_count: 1,
    },
  ],
};

const citationsByMessageId: Record<string, Citation[]> = {
  msg_002: [
    {
      message_id: "msg_002",
      chunk_id: "chunk_policy_07",
      file_id: "file_policy_202607",
      filename: "policy-rollout-july.pdf",
      rank: 1,
      score: 0.94,
      citation_label: "Page 3",
      source_locator: {
        source_format: "pdf",
        page_no: 3,
      },
      snippet: "All travel requests must be approved before departure. Retroactive submission is no longer accepted except for documented outages.",
    },
    {
      message_id: "msg_002",
      chunk_id: "chunk_proc_11",
      file_id: "file_procurement_matrix",
      filename: "procurement-exceptions.xlsx",
      rank: 2,
      score: 0.88,
      citation_label: "Summary · B4:F9",
      source_locator: {
        source_format: "xlsx",
        sheet_name: "Summary",
        cell_range: "B4:F9",
      },
      snippet: "Emergency requests still require manager approval and post-event justification within one business day.",
    },
  ],
  msg_004: [
    {
      message_id: "msg_004",
      chunk_id: "chunk_spec_12",
      file_id: "file_architecture_spec",
      filename: "workspace-governance.md",
      rank: 1,
      score: 0.91,
      citation_label: "Config snapshots",
      source_locator: {
        source_format: "md",
        heading_path: ["Conversation lifecycle", "Snapshot binding"],
        block_label: "config snapshots",
      },
      snippet: "New conversations adopt the latest config versions while historical conversations keep their original knowledge snapshot and runtime settings.",
    },
  ],
  msg_102: [
    {
      message_id: "msg_102",
      chunk_id: "chunk_audit_03",
      file_id: "file_expense_controls",
      filename: "expense-audit-controls.docx",
      rank: 1,
      score: 0.9,
      citation_label: "Controls / paragraph 2",
      source_locator: {
        source_format: "docx",
        heading_path: ["Controls"],
        paragraph_index: 2,
      },
      snippet: "Audit sampling should confirm amount, account code, approver sequence, supplier identity, and attached receipts.",
    },
  ],
  msg_202: [
    {
      message_id: "msg_202",
      chunk_id: "chunk_vendor_05",
      file_id: "file_vendor_packet",
      filename: "vendor-onboarding.json",
      rank: 1,
      score: 0.87,
      citation_label: "$.required_documents",
      source_locator: {
        source_format: "json",
        json_path: "$.required_documents",
      },
      snippet: '["business_license","tax_registration","compliance_attestation","data_processing_addendum"]',
    },
  ],
};

export const knowledgeFiles: KnowledgeFile[] = [
  {
    file_id: "file_policy_202607",
    filename: "policy-rollout-july.pdf",
    mime_type: "application/pdf",
    source_format: "pdf",
    status: "succeeded",
    uploaded_by: "ops-admin",
    uploaded_at: "2026-06-20T01:32:00Z",
    object_uri: "s3://see-agent/policy-rollout-july.pdf",
    parse_version: "parse_v1",
    index_version: "index_v1",
  },
  {
    file_id: "file_procurement_matrix",
    filename: "procurement-exceptions.xlsx",
    mime_type: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
    source_format: "xlsx",
    status: "running",
    uploaded_by: "ops-admin",
    uploaded_at: "2026-06-20T01:48:00Z",
    object_uri: "s3://see-agent/procurement-exceptions.xlsx",
    parse_version: "parse_v1",
    index_version: "index_v1",
  },
  {
    file_id: "file_vendor_packet",
    filename: "vendor-onboarding.json",
    mime_type: "application/json",
    source_format: "json",
    status: "failed",
    uploaded_by: "risk-reviewer",
    uploaded_at: "2026-06-19T09:14:00Z",
    object_uri: "s3://see-agent/vendor-onboarding.json",
    parse_version: "parse_v1",
    index_version: "index_v1",
  },
];

const latestTaskByFileId: Record<string, IngestionTaskSummary> = {
  file_policy_202607: {
    task_id: "task_7001",
    file_id: "file_policy_202607",
    status: "succeeded",
    requested_at: "2026-06-20T01:32:00Z",
    updated_at: "2026-06-20T01:35:00Z",
  },
  file_procurement_matrix: {
    task_id: "task_7002",
    file_id: "file_procurement_matrix",
    status: "running",
    requested_at: "2026-06-20T01:48:00Z",
    updated_at: "2026-06-20T02:24:00Z",
    force_reindex: false,
  },
  file_vendor_packet: {
    task_id: "task_7003",
    file_id: "file_vendor_packet",
    status: "failed",
    requested_at: "2026-06-19T09:14:00Z",
    updated_at: "2026-06-19T09:15:00Z",
    force_reindex: true,
    error: {
      code: "json_path_unsupported",
      message: "The current parser registry does not yet expose stable JSON path extraction.",
      retryable: true,
    },
  },
};

export const agentConfig: AgentConfig = {
  config_version: "cfg_2026_06_18",
  model_id: "qwen3:14b",
  system_prompt_version: "prompt_v7",
  temperature: 0,
  max_output_tokens: 1024,
  created_at: "2026-06-18T05:30:00Z",
};

export const retrievalConfig: RetrievalConfig = {
  retrieval_config_version: "ret_2026_06_19",
  retrieval_strategy: "native_dense_v1",
  embedding_model: "qwen3-embedding:0.6b",
  top_k: 3,
  rerank_enabled: false,
  evidence_min_score: 0.51,
  created_at: "2026-06-19T07:00:00Z",
};

export const configVersions: ConfigVersionSummary[] = [
  {
    version_id: "cfg_2026_06_18",
    kind: "agent",
    created_at: "2026-06-18T05:30:00Z",
  },
  {
    version_id: "ret_2026_06_19",
    kind: "retrieval",
    created_at: "2026-06-19T07:00:00Z",
  },
  {
    version_id: "cfg_2026_06_12",
    kind: "agent",
    created_at: "2026-06-12T03:15:00Z",
  },
];

export function getConversation(conversationId: string) {
  return conversations.find((conversation) => conversation.conversation_id === conversationId);
}

export function getMessages(conversationId: string) {
  return messagesByConversation[conversationId] ?? [];
}

export function getCitations(messageId: string) {
  return citationsByMessageId[messageId] ?? [];
}

export function getKnowledgeTask(fileId: string) {
  return latestTaskByFileId[fileId];
}

export function getConversationCitationFocus(messages: Message[]) {
  return [...messages]
    .reverse()
    .find((message) => message.role === "assistant" && (message.citations_count ?? 0) > 0);
}

export function formatDateTime(iso: string) {
  return new Intl.DateTimeFormat("zh-CN", {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
    timeZone: "UTC",
  }).format(new Date(iso));
}

export function formatLocator(sourceLocator: SourceLocator) {
  switch (sourceLocator.source_format) {
    case "pdf":
      return sourceLocator.page_no ? `Page ${sourceLocator.page_no}` : "PDF excerpt";
    case "docx":
      if (sourceLocator.heading_path?.length) {
        return sourceLocator.heading_path.join(" / ");
      }

      return sourceLocator.paragraph_index !== undefined
        ? `paragraph ${sourceLocator.paragraph_index}`
        : "DOCX excerpt";
    case "xlsx":
      return [sourceLocator.sheet_name, sourceLocator.cell_range].filter(Boolean).join(" · ") || "Sheet excerpt";
    case "pptx":
      return [sourceLocator.slide_no ? `Slide ${sourceLocator.slide_no}` : undefined, sourceLocator.slide_title]
        .filter(Boolean)
        .join(" · ") || "Slide excerpt";
    case "json":
      return sourceLocator.json_path ?? "JSON excerpt";
    case "txt":
    case "md":
      return sourceLocator.block_label ?? sourceLocator.heading_path?.join(" / ") ?? "Text excerpt";
    default:
      return "Source excerpt";
  }
}
