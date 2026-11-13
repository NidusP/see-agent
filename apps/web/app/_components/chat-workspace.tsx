"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { ArrowDownIcon, ArrowUpIcon, SquareIcon } from "lucide-react";
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type ComponentProps,
  type FormEvent,
  type KeyboardEvent,
  type ReactElement,
  type ReactNode,
} from "react";

import {
  ApiError,
  createChatIdempotencyKey,
  createConversation,
  formatDateTime,
  formatLocator,
  getConversationDetail,
  getMessageSources,
  getRevalidation,
  listConversations,
	  renameConversation,
  revalidateMessage,
  streamMessage,
  type Citation,
  type CitationAnchor,
  type Conversation,
  type FreshnessStatus,
  type Message,
  type MessageSourcesResponse,
  type RevalidationRun,
} from "../_lib/chat-api";
import { recoverFromUnauthenticated } from "../_lib/auth-recovery";
import { cn } from "../_lib/utils";
import { Button } from "./ui/button";
import { Input } from "./ui/input";
import { Textarea } from "./ui/textarea";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";

interface ChatContextValue {
  conversations: Conversation[];
  selectedConversationId?: string;
  selectedConversation?: Conversation;
  messages: Message[];
  citations: Citation[];
  citationSources?: MessageSourcesResponse;
  citationFocus?: Message;
  citationAnchorFocus?: CitationAnchorFocus;
  citationDrawerOpen: boolean;
  revalidations: Record<string, RevalidationState>;
  failedAttempt?: FailedChatAttempt;
  pendingTurn?: PendingChatTurn;
  composer: string;
  loadingConversations: boolean;
  loadingDetail: boolean;
  loadingSources: boolean;
  creating: boolean;
  sending: boolean;
  error?: string;
  notice?: string;
  sourceError?: string;
  lastLoadedAt?: string;
  setComposer: (value: string) => void;
  createNewConversation: () => Promise<void>;
	  renameConversation: (conversationId: string, title: string) => Promise<void>;
  focusCitations: (messageId: string, citationRanks?: number[]) => void;
  previewCitationAnchor: (messageId: string, citationRanks: number[]) => void;
  focusCitationAnchor: (messageId: string, citationRanks: number[]) => void;
  closeCitationDrawer: () => void;
  revalidateAnswer: (message: Message) => Promise<void>;
  refreshRevalidation: (messageId: string) => Promise<void>;
  sendCurrentMessage: (event: FormEvent<HTMLFormElement>) => Promise<void>;
  submitComposerMessage: () => Promise<void>;
  retryLastFailedMessage: () => Promise<void>;
  abortCurrentMessage: () => void;
  applyExamplePrompt: (prompt: string) => void;
}

interface FailedChatAttempt {
  content: string;
  conversationId?: string;
  reason: string;
  failedAt: string;
}

interface PendingChatTurn {
  clientTurnID: string;
  content: string;
  phase: "preparing" | "failed";
  conversationId?: string;
  createdAt: string;
  reason?: string;
  failedAt?: string;
}

interface RevalidationState {
  run?: RevalidationRun;
  submitting?: boolean;
  refreshing?: boolean;
  error?: string;
}

interface CitationAnchorFocus {
  messageId: string;
  citationRanks: number[];
}

const ChatContext = createContext<ChatContextValue | null>(null);
const conversationAccessNotice = "会话不存在或当前账号无权访问。";
const conversationAccessNoticeStorageKey = "see-agent:conversation-access-notice";

export function ChatWorkspaceProvider({
  children,
  initialSelectedConversationId,
  showEmptyState = false,
}: {
  children: ReactNode;
  initialSelectedConversationId?: string;
  showEmptyState?: boolean;
}) {
  const router = useRouter();
  const [conversations, setConversations] = useState<Conversation[]>([]);
  const [selectedConversationId, setSelectedConversationId] = useState<string | undefined>(initialSelectedConversationId);
  const [selectedConversation, setSelectedConversation] = useState<Conversation>();
  const [messages, setMessages] = useState<Message[]>([]);
  const [citations, setCitations] = useState<Citation[]>([]);
  const [citationSources, setCitationSources] = useState<MessageSourcesResponse>();
  const [revalidations, setRevalidations] = useState<Record<string, RevalidationState>>({});
  const [focusedMessageId, setFocusedMessageId] = useState<string>();
  const [citationAnchorFocus, setCitationAnchorFocus] = useState<CitationAnchorFocus>();
  const [citationDrawerOpen, setCitationDrawerOpen] = useState(false);
  const [composer, setComposer] = useState("");
  const [loadingConversations, setLoadingConversations] = useState(true);
  const [loadingDetail, setLoadingDetail] = useState(false);
  const [loadingSources, setLoadingSources] = useState(false);
  const [creating, setCreating] = useState(false);
  const [sending, setSending] = useState(false);
  const [error, setError] = useState<string>();
  const [notice, setNotice] = useState<string>();
  const [sourceError, setSourceError] = useState<string>();
  const [failedAttempt, setFailedAttempt] = useState<FailedChatAttempt>();
  const [pendingTurn, setPendingTurn] = useState<PendingChatTurn>();
  const [lastLoadedAt, setLastLoadedAt] = useState<string>();
  const abortControllerRef = useRef<AbortController | null>(null);
  const composerRef = useRef("");
  const authRedirectRef = useRef(false);

  const updateComposer = useCallback((value: string) => {
    composerRef.current = value;
    setComposer(value);
  }, []);

  const redirectForUnauthenticated = useCallback(async (cause: unknown, draftOverride?: string) => {
    if (!isUnauthenticatedError(cause)) {
      return false;
    }
    if (authRedirectRef.current) {
      return true;
    }

    authRedirectRef.current = true;
    const nextPath = currentChatPath();
    const draft = draftOverride ?? composerRef.current;
    if (nextPath === "/chat" || nextPath.startsWith("/chat/")) {
      storeChatDraft(nextPath, draft);
    }
    await recoverFromUnauthenticated(nextPath);
    return true;
  }, []);

  useEffect(() => {
    const timer = window.setTimeout(() => {
      setSelectedConversationId(initialSelectedConversationId);
    }, 0);

    return () => window.clearTimeout(timer);
  }, [initialSelectedConversationId]);

  useEffect(() => {
    return () => {
      abortControllerRef.current?.abort();
    };
  }, []);

  useEffect(() => {
    if (window.sessionStorage.getItem(conversationAccessNoticeStorageKey) !== "1") {
      return;
    }
    window.sessionStorage.removeItem(conversationAccessNoticeStorageKey);
    setNotice(conversationAccessNotice);
  }, []);

  useEffect(() => {
    const draft = consumeChatDraft(currentChatPath());
    if (!draft) {
      return;
    }
    updateComposer(draft);
  }, [updateComposer]);

  const loadConversations = useCallback(
    async (options?: { silent?: boolean }) => {
      if (!options?.silent) {
        setLoadingConversations(true);
      }
      setError(undefined);

      try {
        const response = await listConversations();
        setConversations(response.items);
		setSelectedConversation((current) => {
			if (!current) {
				return current;
			}
			return response.items.find((conversation) => conversation.conversation_id === current.conversation_id) ?? current;
		});
        setLastLoadedAt(new Date().toISOString());
        if (
          initialSelectedConversationId &&
          !response.items.some((conversation) => conversation.conversation_id === initialSelectedConversationId)
        ) {
          setNotice(conversationAccessNotice);
          window.sessionStorage.setItem(conversationAccessNoticeStorageKey, "1");
          const fallbackConversationID = response.items[0]?.conversation_id;
          router.replace(fallbackConversationID ? `/chat/${fallbackConversationID}` : "/chat");
        }
        setSelectedConversationId((current) => {
          if (current && response.items.some((conversation) => conversation.conversation_id === current)) {
            return current;
          }
          if (showEmptyState && !initialSelectedConversationId) {
            return undefined;
          }
          return response.items[0]?.conversation_id;
        });
      } catch (fetchError) {
        if (await redirectForUnauthenticated(fetchError)) {
          return;
        }
        setError(readError(fetchError));
      } finally {
        setLoadingConversations(false);
      }
    },
    [initialSelectedConversationId, redirectForUnauthenticated, router, showEmptyState],
  );

  useEffect(() => {
    const timer = window.setTimeout(() => {
      void loadConversations();
    }, 0);

    return () => window.clearTimeout(timer);
  }, [loadConversations]);

  useEffect(() => {
    const timer = window.setTimeout(() => {
      if (!selectedConversationId) {
        setSelectedConversation(undefined);
        setMessages([]);
        setCitations([]);
        setCitationSources(undefined);
        setFocusedMessageId(undefined);
        setCitationAnchorFocus(undefined);
        setCitationDrawerOpen(false);
        return;
      }

      setLoadingDetail(true);
      setError(undefined);
      setCitationAnchorFocus(undefined);
      setCitationDrawerOpen(false);
      void getConversationDetail(selectedConversationId)
        .then((detail) => {
          const detailMessages = Array.isArray(detail.messages) ? detail.messages : [];
          setSelectedConversation(detail.conversation);
          setMessages(detailMessages);
          setFocusedMessageId((current) => {
            if (current && detailMessages.some((message) => message.message_id === current)) {
              return current;
            }
            return findDefaultCitationFocus(detailMessages)?.message_id;
          });
        })
        .catch((detailError) => {
          if (isUnauthenticatedError(detailError)) {
            void redirectForUnauthenticated(detailError);
            return;
          }
          setSelectedConversation(undefined);
          setMessages([]);
          setCitations([]);
          setCitationSources(undefined);
          setFocusedMessageId(undefined);
          setCitationAnchorFocus(undefined);
          setCitationDrawerOpen(false);
          if (detailError instanceof ApiError && detailError.status === 404) {
            setNotice(conversationAccessNotice);
            window.sessionStorage.setItem(conversationAccessNoticeStorageKey, "1");
            setError(undefined);
          } else {
            setError(readError(detailError));
          }
        })
        .finally(() => {
          setLoadingDetail(false);
        });
    }, 0);

    return () => window.clearTimeout(timer);
  }, [redirectForUnauthenticated, selectedConversationId]);

  useEffect(() => {
    setRevalidations({});
  }, [selectedConversationId]);

  const citationFocus = useMemo(() => {
    if (focusedMessageId) {
      return messages.find((message) => message.message_id === focusedMessageId);
    }
    return findDefaultCitationFocus(messages);
  }, [focusedMessageId, messages]);

  useEffect(() => {
    const timer = window.setTimeout(() => {
      const needsCitationSources = citationDrawerOpen || citationAnchorFocus !== undefined;
      if (!needsCitationSources || !selectedConversationId || !citationFocus || !canShowMessageSources(citationFocus) || (citationFocus.citations_count ?? 0) === 0) {
        setCitations([]);
        setCitationSources(undefined);
        setSourceError(undefined);
        return;
      }

      setLoadingSources(true);
      setSourceError(undefined);
      setCitations([]);
      setCitationSources(undefined);
      void getMessageSources(selectedConversationId, citationFocus.message_id)
        .then((response) => {
          setCitations(response.items);
          setCitationSources(response);
        })
        .catch((sourcesError) => {
          if (isUnauthenticatedError(sourcesError)) {
            void redirectForUnauthenticated(sourcesError);
            return;
          }
          setCitations([]);
          setCitationSources(undefined);
          setSourceError(readError(sourcesError));
        })
        .finally(() => {
          setLoadingSources(false);
        });
    }, 0);

    return () => window.clearTimeout(timer);
  }, [citationAnchorFocus, citationDrawerOpen, citationFocus, redirectForUnauthenticated, selectedConversationId]);

  const createNewConversation = useCallback(async () => {
    setCreating(true);
    setError(undefined);

    try {
      const response = await createConversation();
      setSelectedConversationId(response.conversation.conversation_id);
      setSelectedConversation(response.conversation);
      setMessages([]);
      setCitations([]);
      setCitationSources(undefined);
      setFocusedMessageId(undefined);
      setCitationAnchorFocus(undefined);
      setCitationDrawerOpen(false);
      await loadConversations({ silent: true });
      router.push(`/chat/${response.conversation.conversation_id}`);
    } catch (createError) {
      if (await redirectForUnauthenticated(createError)) {
        return;
      }
      setError(readError(createError));
    } finally {
      setCreating(false);
    }
  }, [loadConversations, redirectForUnauthenticated, router]);

	const renameConversationByID = useCallback(async (conversationId: string, title: string) => {
		try {
			const response = await renameConversation(conversationId, title);
			setConversations((current) =>
				current.map((conversation) =>
					conversation.conversation_id === conversationId ? response.conversation : conversation,
				),
			);
			setSelectedConversation((current) =>
				current?.conversation_id === conversationId ? response.conversation : current,
			);
		} catch (renameError) {
			if (await redirectForUnauthenticated(renameError)) {
				return;
			}
			throw renameError;
		}
	}, [redirectForUnauthenticated]);

  const focusCitations = useCallback((messageId: string, citationRanks?: number[]) => {
    const message = messages.find((item) => item.message_id === messageId);
    if (!message || !canShowMessageSources(message)) {
      return;
    }
    setFocusedMessageId(messageId);
    setCitationAnchorFocus(citationRanks?.length ? { messageId, citationRanks } : undefined);
    setCitationDrawerOpen(true);
  }, [messages]);

  const previewCitationAnchor = useCallback((messageId: string, citationRanks: number[]) => {
    const message = messages.find((item) => item.message_id === messageId);
    if (!message || !canShowMessageSources(message)) {
      return;
    }
    setFocusedMessageId(messageId);
    setCitationAnchorFocus({ messageId, citationRanks });
  }, [messages]);

  const focusCitationAnchor = useCallback((messageId: string, citationRanks: number[]) => {
    const message = messages.find((item) => item.message_id === messageId);
    if (!message || !canShowMessageSources(message)) {
      return;
    }
    setFocusedMessageId(messageId);
    setCitationAnchorFocus({ messageId, citationRanks });
    setCitationDrawerOpen(true);
  }, [messages]);

  const closeCitationDrawer = useCallback(() => setCitationDrawerOpen(false), []);

  useEffect(() => {
    if (!citationDrawerOpen) {
      return;
    }

    const closeOnEscape = (event: globalThis.KeyboardEvent) => {
      if (event.key === "Escape") {
        setCitationDrawerOpen(false);
      }
    };
    window.addEventListener("keydown", closeOnEscape);
    return () => window.removeEventListener("keydown", closeOnEscape);
  }, [citationDrawerOpen]);

  const revalidateAnswer = useCallback(
    async (message: Message) => {
      if (!selectedConversationId) {
        return;
      }

      setRevalidations((current) => ({
        ...current,
        [message.message_id]: {
          ...current[message.message_id],
          submitting: true,
          error: undefined,
        },
      }));

      try {
        const response = await revalidateMessage({
          conversationId: selectedConversationId,
          messageId: message.message_id,
          idempotencyKey: createChatIdempotencyKey(),
        });
        setRevalidations((current) => ({
          ...current,
          [message.message_id]: {
            run: response.revalidation,
            submitting: false,
          },
        }));
      } catch (revalidationError) {
        if (await redirectForUnauthenticated(revalidationError)) {
          return;
        }
        setRevalidations((current) => ({
          ...current,
          [message.message_id]: {
            ...current[message.message_id],
            submitting: false,
            error: readError(revalidationError),
          },
        }));
      }
    },
    [redirectForUnauthenticated, selectedConversationId],
  );

  const refreshRevalidation = useCallback(
    async (messageId: string) => {
      const state = revalidations[messageId];
      if (!selectedConversationId || !state?.run) {
        return;
      }

      setRevalidations((current) => ({
        ...current,
        [messageId]: {
          ...current[messageId],
          refreshing: true,
          error: undefined,
        },
      }));

      try {
        const response = await getRevalidation(selectedConversationId, state.run.revalidation_id);
        setRevalidations((current) => ({
          ...current,
          [messageId]: {
            run: response.revalidation,
            refreshing: false,
          },
        }));
        if (response.revalidation.status === "completed") {
          setMessages((current) =>
            current.map((message) =>
              message.message_id === messageId ? { ...message, freshness_status: "revalidated" } : message,
            ),
          );
        }
      } catch (revalidationError) {
        if (await redirectForUnauthenticated(revalidationError)) {
          return;
        }
        setRevalidations((current) => ({
          ...current,
          [messageId]: {
            ...current[messageId],
            refreshing: false,
            error: readError(revalidationError),
          },
        }));
      }
    },
    [redirectForUnauthenticated, revalidations, selectedConversationId],
  );

  const pendingRevalidationMessageIDs = useMemo(
    () =>
      Object.entries(revalidations)
        .filter(([, state]) => state.run?.status === "queued" || state.run?.status === "running")
        .map(([messageId]) => messageId),
    [revalidations],
  );

  useEffect(() => {
    if (pendingRevalidationMessageIDs.length === 0) {
      return;
    }

    const interval = window.setInterval(() => {
      for (const messageId of pendingRevalidationMessageIDs) {
        void refreshRevalidation(messageId);
      }
    }, 2500);

    return () => window.clearInterval(interval);
  }, [pendingRevalidationMessageIDs, refreshRevalidation]);

  const applyExamplePrompt = useCallback((prompt: string) => {
    updateComposer(prompt);
    setFailedAttempt(undefined);
  }, [updateComposer]);

  const sendMessageContent = useCallback(
    async (content: string, options?: { conversationId?: string }) => {
      setSending(true);
      setError(undefined);
      setSourceError(undefined);
      setFailedAttempt(undefined);

      const abortController = new AbortController();
      abortControllerRef.current = abortController;
      let activeConversationId = options?.conversationId ?? selectedConversationId;
      let createdConversation: Conversation | undefined;
      const clientTurnID = `pending-${Date.now()}-${Math.random().toString(16).slice(2)}`;
      const createdAt = new Date().toISOString();

      setPendingTurn({ clientTurnID, content, phase: "preparing", conversationId: activeConversationId, createdAt });
      updateComposer("");

      try {
        if (!activeConversationId) {
          const response = await createConversation();
          activeConversationId = response.conversation.conversation_id;
          createdConversation = response.conversation;
        }

        const streamConversationId = activeConversationId;
        await streamMessage(
          streamConversationId,
          content,
          {
            onStarted: (payload) => {
              if (createdConversation) {
                setSelectedConversation(createdConversation);
              }
              setMessages((current) =>
                appendStartedMessages(current, payload.user_message, payload.assistant_message),
              );
              setPendingTurn((current) => (current?.clientTurnID === clientTurnID ? undefined : current));
              setFocusedMessageId(undefined);
            },
            onDelta: (payload) => {
              setMessages((current) => appendMessageDelta(current, payload.message_id, payload.delta));
            },
            onCompleted: (payload) => {
              setMessages((current) =>
                replaceCompletedMessages(current, payload.user_message, payload.assistant_message),
              );
              setFocusedMessageId(payload.assistant_message.message_id);
              if (createdConversation || streamConversationId !== selectedConversationId) {
                if (createdConversation) {
                  setSelectedConversation(createdConversation);
                }
                setSelectedConversationId(streamConversationId);
                router.push(`/chat/${streamConversationId}`);
              }
            },
          },
          { signal: abortController.signal },
        );
        await loadConversations({ silent: true });
      } catch (sendError) {
        if (await redirectForUnauthenticated(sendError, content)) {
          return;
        }
        const message = readError(sendError);
        const failedAt = new Date().toISOString();
        setMessages((current) => markStreamingMessageFailed(current));
        setPendingTurn((current) =>
          current?.clientTurnID === clientTurnID
            ? { ...current, phase: "failed", conversationId: activeConversationId, reason: message, failedAt }
            : current,
        );
        if (createdConversation && activeConversationId) {
          setSelectedConversation(createdConversation);
          setSelectedConversationId(activeConversationId);
          router.push(`/chat/${activeConversationId}`);
        }
        setFailedAttempt({
          content,
          conversationId: activeConversationId,
          reason: message,
          failedAt,
        });
        setError(message);
      } finally {
        if (abortControllerRef.current === abortController) {
          abortControllerRef.current = null;
        }
        setSending(false);
      }
    },
    [loadConversations, redirectForUnauthenticated, router, selectedConversationId, updateComposer],
  );

  const submitComposerMessage = useCallback(
    async () => {
      if (sending) {
        return;
      }
      const content = composer.trim();
      if (!content) {
        return;
      }

      await sendMessageContent(content);
    },
    [composer, sendMessageContent, sending],
  );

  const sendCurrentMessage = useCallback(
    async (event: FormEvent<HTMLFormElement>) => {
      event.preventDefault();
      await submitComposerMessage();
    },
    [submitComposerMessage],
  );

  const retryLastFailedMessage = useCallback(async () => {
    if (!failedAttempt || sending) {
      return;
    }
    await sendMessageContent(failedAttempt.content, { conversationId: failedAttempt.conversationId });
  }, [failedAttempt, sendMessageContent, sending]);

  const abortCurrentMessage = useCallback(() => {
    abortControllerRef.current?.abort();
  }, []);

  const setComposerValue = useCallback(
    (value: string) => {
      updateComposer(value);
      if (failedAttempt) {
        setFailedAttempt(undefined);
      }
    },
    [failedAttempt, updateComposer],
  );

  const value = useMemo<ChatContextValue>(
    () => ({
      conversations,
      selectedConversationId,
      selectedConversation,
      messages,
      citations,
      citationSources,
      citationFocus,
      citationAnchorFocus,
      citationDrawerOpen,
      revalidations,
      failedAttempt,
      pendingTurn,
      composer,
      loadingConversations,
      loadingDetail,
      loadingSources,
      creating,
      sending,
      error,
      notice,
      sourceError,
      lastLoadedAt,
      setComposer: setComposerValue,
      createNewConversation,
		  renameConversation: renameConversationByID,
      focusCitations,
      previewCitationAnchor,
      focusCitationAnchor,
      closeCitationDrawer,
      revalidateAnswer,
      refreshRevalidation,
      sendCurrentMessage,
      submitComposerMessage,
      retryLastFailedMessage,
      abortCurrentMessage,
      applyExamplePrompt,
    }),
    [
      conversations,
      selectedConversationId,
      selectedConversation,
      messages,
      citations,
      citationSources,
      citationFocus,
      citationAnchorFocus,
      citationDrawerOpen,
      revalidations,
      failedAttempt,
      pendingTurn,
      composer,
      loadingConversations,
      loadingDetail,
      loadingSources,
      creating,
      sending,
      error,
      notice,
      sourceError,
      lastLoadedAt,
      setComposerValue,
      createNewConversation,
		  renameConversationByID,
      focusCitations,
      previewCitationAnchor,
      focusCitationAnchor,
      closeCitationDrawer,
      revalidateAnswer,
      refreshRevalidation,
      sendCurrentMessage,
      submitComposerMessage,
      retryLastFailedMessage,
      abortCurrentMessage,
      applyExamplePrompt,
    ],
  );

  return <ChatContext.Provider value={value}>{children}</ChatContext.Provider>;
}

function ChromeButton({ className, ...props }: ComponentProps<typeof Button>) {
  return (
    <Button
      className={cn("h-auto whitespace-normal font-normal shadow-none disabled:opacity-100", className)}
      {...props}
    />
  );
}

function IconTooltip({ label, children }: { label: string; children: ReactElement }) {
  return (
    <Tooltip>
      <TooltipTrigger render={children} />
      <TooltipContent>{label}</TooltipContent>
    </Tooltip>
  );
}

export function ChatNewConversationButton() {
  const { creating, createNewConversation } = useChatWorkspace();
  const label = creating ? "正在创建对话" : "新建对话";

  return (
    <IconTooltip label={label}>
      <ChromeButton
        type="button"
        className="chat-new-conversation"
        data-testid="chat-new-conversation"
        aria-label={label}
        onClick={() => void createNewConversation()}
        disabled={creating}
      >
        <span className="chat-new-conversation-icon" aria-hidden="true">+</span>
        <span className="chat-new-conversation-label">{creating ? "创建中" : "新建对话"}</span>
      </ChromeButton>
    </IconTooltip>
  );
}

export function ChatConversationList({ onConversationNavigate }: { onConversationNavigate?: () => void }) {
  const { conversations, selectedConversationId, loadingConversations, lastLoadedAt, error, renameConversation } = useChatWorkspace();
  const [editingConversationId, setEditingConversationId] = useState<string>();
  const [titleDraft, setTitleDraft] = useState("");
  const [renameError, setRenameError] = useState<string>();
  const [savingConversationId, setSavingConversationId] = useState<string>();
  const titleInputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (editingConversationId) {
      titleInputRef.current?.focus();
      titleInputRef.current?.select();
    }
  }, [editingConversationId]);

  const startRename = (conversation: Conversation) => {
    setEditingConversationId(conversation.conversation_id);
    setTitleDraft(conversation.title);
    setRenameError(undefined);
  };

  const cancelRename = () => {
    if (savingConversationId) {
      return;
    }
    setEditingConversationId(undefined);
    setRenameError(undefined);
  };

  const submitRename = async (conversationId: string) => {
    if (savingConversationId) {
      return;
    }
    setSavingConversationId(conversationId);
    setRenameError(undefined);
    try {
      await renameConversation(conversationId, titleDraft);
      setEditingConversationId(undefined);
    } catch (renameFailure) {
      setRenameError(readError(renameFailure));
    } finally {
      setSavingConversationId(undefined);
    }
  };

  return (
    <div className="conversation-pane-content">
      <div className="section-heading">
        <h2>会话</h2>
      </div>
      <div className="conversation-list">
        {loadingConversations ? <p className="loading-copy">会话加载中...</p> : null}
        {error ? <p className="inline-error">{error}</p> : null}
        {!loadingConversations && conversations.length === 0 ? (
          <section className="table-empty-state">
            <h3>还没有会话。</h3>
            <p>创建新会话，或者直接在主输入框发送第一条消息。</p>
          </section>
        ) : null}
        {conversations.map((conversation) => {
          const isActive = conversation.conversation_id === selectedConversationId;
          const isEditing = conversation.conversation_id === editingConversationId;
          const isSaving = conversation.conversation_id === savingConversationId;

          return (
            <article key={conversation.conversation_id} className={`conversation-card${isActive ? " is-active" : ""}`}>
              {isEditing ? (
                <form
                  className="conversation-title-edit"
                  onSubmit={(event) => {
                    event.preventDefault();
                    void submitRename(conversation.conversation_id);
                  }}
                >
                  <label className="sr-only" htmlFor={`conversation-title-${conversation.conversation_id}`}>重命名会话</label>
                  <Input
                    id={`conversation-title-${conversation.conversation_id}`}
                    ref={titleInputRef}
                    className="h-auto"
                    value={titleDraft}
                    onChange={(event) => setTitleDraft(event.target.value)}
                    onKeyDown={(event) => {
                      if (event.key === "Escape") {
                        event.preventDefault();
                        cancelRename();
                      }
                    }}
                    disabled={isSaving}
                    maxLength={48}
                    data-testid={`conversation-rename-input-${conversation.conversation_id}`}
                  />
                  <div className="conversation-title-edit-actions">
                    <Button type="submit" size="xs" variant="secondary" className="h-auto" disabled={isSaving} data-testid={`conversation-rename-save-${conversation.conversation_id}`}>
                      {isSaving ? "保存中" : "保存"}
                    </Button>
                    <Button type="button" size="xs" variant="ghost" className="h-auto" onClick={cancelRename} disabled={isSaving}>取消</Button>
                  </div>
                  {renameError ? <p className="conversation-title-edit-error" role="alert">{renameError}</p> : null}
                </form>
              ) : (
                <>
                  <Link
                    href={`/chat/${conversation.conversation_id}`}
                    className="conversation-card-link"
                    aria-current={isActive ? "page" : undefined}
                    onClick={onConversationNavigate}
                  >
                    <div className="conversation-card-top">
                      <h3>{conversation.title}</h3>
                      <span>{formatDateTime(conversation.updated_at)}</span>
                    </div>
                  </Link>
                  <IconTooltip label="重命名会话">
                    <ChromeButton
                      type="button"
                      className="conversation-rename-button"
                      aria-label={`重命名会话：${conversation.title}`}
                      onClick={() => startRename(conversation)}
                      data-testid={`conversation-rename-${conversation.conversation_id}`}
                    >
                      重命名
                    </ChromeButton>
                  </IconTooltip>
                </>
              )}
            </article>
          );
        })}
        {lastLoadedAt ? <p className="loading-copy">最近更新 {formatDateTime(lastLoadedAt)}</p> : null}
      </div>
    </div>
  );
}

export function ChatWorkspacePanel() {
  const {
    selectedConversation,
    messages,
    pendingTurn,
    composer,
    failedAttempt,
    loadingDetail,
    sending,
    error,
    notice,
    setComposer,
    sendCurrentMessage,
    submitComposerMessage,
    retryLastFailedMessage,
    abortCurrentMessage,
    focusCitations,
    previewCitationAnchor,
    focusCitationAnchor,
    citationDrawerOpen,
    closeCitationDrawer,
    citationSources,
    loadingSources,
    sourceError,
    revalidations,
    revalidateAnswer,
    refreshRevalidation,
    applyExamplePrompt,
  } = useChatWorkspace();
  const messageViewportRef = useRef<HTMLDivElement>(null);
  const threadEndRef = useRef<HTMLDivElement>(null);
  const composerInputRef = useRef<HTMLTextAreaElement>(null);
  const [followingLatest, setFollowingLatest] = useState(true);

  useLayoutEffect(() => {
    const input = composerInputRef.current;
    if (!input) {
      return;
    }

    input.style.height = "auto";
    const maxHeight = 200;
    const nextHeight = Math.min(input.scrollHeight, maxHeight);
    input.style.height = `${nextHeight}px`;
    input.style.overflowY = input.scrollHeight > maxHeight ? "auto" : "hidden";
  }, [composer]);

  const scrollThreadEnd = useCallback((smooth = false) => {
    window.requestAnimationFrame(() => {
      const viewport = messageViewportRef.current;
      if (!viewport) {
        return;
      }
      viewport.scrollTo({ top: viewport.scrollHeight, behavior: smooth ? "smooth" : "auto" });
    });
  }, []);

  const scrollToLatest = useCallback(() => {
    setFollowingLatest(true);
    scrollThreadEnd(true);
  }, [scrollThreadEnd]);

  useEffect(() => {
    const scrollContainer = messageViewportRef.current;
    if (!scrollContainer) {
      return;
    }

    const updateFollowingState = () => {
      const distanceFromLatest = scrollContainer.scrollHeight - scrollContainer.scrollTop - scrollContainer.clientHeight;
      setFollowingLatest(distanceFromLatest < 96);
    };
    scrollContainer.addEventListener("scroll", updateFollowingState, { passive: true });
    updateFollowingState();
    return () => scrollContainer.removeEventListener("scroll", updateFollowingState);
  }, []);

  useEffect(() => {
    if (followingLatest) {
      scrollThreadEnd();
    }
  }, [followingLatest, messages, pendingTurn, scrollThreadEnd, sending]);

  const handleComposerKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.key !== "Enter" || event.shiftKey || event.nativeEvent.isComposing || sending) {
      return;
    }
    event.preventDefault();
    void submitComposerMessage();
  };

  return (
    <div className="chat-workspace-panel">
      <div className="chat-thread-context">
        {notice ? <p className="inline-error" data-testid="conversation-access-notice" role="status">{notice}</p> : null}
        {selectedConversation ? (
          <header className="chat-thread-header">
            <h1>{selectedConversation.title}</h1>
            <span className="header-chip">知识问答优先</span>
          </header>
        ) : null}
      </div>

      <section className="chat-message-region">
        <div className="chat-message-viewport" data-testid="chat-message-viewport" ref={messageViewportRef}>
          <div className="chat-message-content">
            {!selectedConversation && messages.length === 0 && !pendingTurn ? (
              <section className="empty-state chat-empty-state">
                <h1>今天想解决什么？</h1>
                <div className="example-grid">
                  {examplePrompts.map((prompt) => (
                    <article key={prompt}>
                      <span>示例</span>
                      <p>{prompt}</p>
                      <Button type="button" variant="outline" size="sm" className="mt-3" onClick={() => applyExamplePrompt(prompt)}>
                        使用这条
                      </Button>
                    </article>
                  ))}
                </div>
              </section>
            ) : null}

            <section className="message-list" aria-label="会话消息" data-testid="chat-message-list">
              {loadingDetail ? <p className="loading-copy">消息加载中...</p> : null}
              {!loadingDetail && selectedConversation && messages.length === 0 && !pendingTurn ? (
                <section className="table-empty-state">
                  <h3>当前会话暂无消息。</h3>
                  <p>发送一条问题后，助手会基于当前已发布知识进行回答。</p>
                </section>
              ) : null}
              {messages.map((message) => (
                <ConversationMessage
                  key={message.message_id}
                  message={message}
                  revalidationState={revalidations[message.message_id]}
                  onFocusCitations={focusCitations}
                  onPreviewCitationAnchor={previewCitationAnchor}
                  onFocusCitationAnchor={focusCitationAnchor}
                  citationDrawerOpen={citationDrawerOpen}
                  onCloseCitationDrawer={closeCitationDrawer}
                  citationSources={citationSources}
                  loadingSources={loadingSources}
                  sourceError={sourceError}
                  onRevalidate={revalidateAnswer}
                  onRefreshRevalidation={refreshRevalidation}
                />
              ))}
              {pendingTurn ? <PendingTurnMessages turn={pendingTurn} /> : null}
            </section>
            <div ref={threadEndRef} aria-hidden="true" />
          </div>
        </div>
        {!followingLatest && (messages.length > 0 || pendingTurn) ? (
          <IconTooltip label="回到最新">
            <ChromeButton
              type="button"
              className="jump-to-latest"
              data-testid="jump-to-latest"
              aria-label="回到最新"
              onClick={scrollToLatest}
            >
              <ArrowDownIcon aria-hidden="true" size={20} strokeWidth={2.25} />
            </ChromeButton>
          </IconTooltip>
        ) : null}
      </section>

      <section className="composer-card">
        <form onSubmit={(event) => void sendCurrentMessage(event)}>
          <div className="composer-input-shell">
            <Textarea
              ref={composerInputRef}
              data-testid="chat-composer-input"
              className="composer-input field-sizing-fixed block h-auto min-h-16 w-full resize-none rounded-[18px] border-0 bg-transparent px-4 pt-4 pr-16 pb-4 text-base shadow-none focus-visible:border-transparent focus-visible:ring-0 md:text-base"
              rows={1}
              aria-label="输入消息"
              placeholder="向助手提问"
              value={composer}
              onChange={(event) => setComposer(event.target.value)}
              onKeyDown={handleComposerKeyDown}
            />
            {sending ? (
              <IconTooltip label="停止生成">
                <ChromeButton
                  data-testid="chat-abort-button"
                  type="button"
                  className="composer-inline-button is-abort"
                  aria-label="停止生成"
                  onClick={abortCurrentMessage}
                >
                  <SquareIcon aria-hidden="true" size={16} fill="currentColor" />
                </ChromeButton>
              </IconTooltip>
            ) : (
              <IconTooltip label="发送消息">
                <ChromeButton
                  data-testid="chat-send-button"
                  type="submit"
                  className="composer-inline-button"
                  aria-label="发送消息"
                  disabled={composer.trim().length === 0}
                >
                  <ArrowUpIcon aria-hidden="true" size={20} strokeWidth={2.5} />
                </ChromeButton>
              </IconTooltip>
            )}
          </div>
          {failedAttempt || sending || error ? (
            <div className="composer-footer">
              <div className="composer-status">
                {failedAttempt && !pendingTurn ? (
                  <div className="chat-error-state" data-testid="chat-error-state" role="status">
                    <strong>发送失败</strong>
                    <p>{failedAttempt.reason}</p>
                  </div>
                ) : sending || error ? (
                  <p className={error ? "inline-error" : undefined}>
                    {sending ? "正在生成。你可以先写下一条消息。" : error}
                  </p>
                ) : null}
              </div>
              {failedAttempt ? (
                <Button
                  data-testid="chat-retry-button"
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={() => void retryLastFailedMessage()}
                  disabled={sending}
                >
                  重试上一条
                </Button>
              ) : null}
            </div>
          ) : null}
        </form>
      </section>
    </div>
  );
}

function ConversationMessage({
  message,
  revalidationState,
  onFocusCitations,
  onPreviewCitationAnchor,
  onFocusCitationAnchor,
  citationDrawerOpen,
  onCloseCitationDrawer,
  citationSources,
  loadingSources,
  sourceError,
  onRevalidate,
  onRefreshRevalidation,
}: {
  message: Message;
  revalidationState?: RevalidationState;
  onFocusCitations: (messageId: string, citationRanks?: number[]) => void;
  onPreviewCitationAnchor: (messageId: string, citationRanks: number[]) => void;
  onFocusCitationAnchor: (messageId: string, citationRanks: number[]) => void;
  citationDrawerOpen: boolean;
  onCloseCitationDrawer: () => void;
  citationSources?: MessageSourcesResponse;
  loadingSources: boolean;
  sourceError?: string;
  onRevalidate: (message: Message) => Promise<void>;
  onRefreshRevalidation: (messageId: string) => Promise<void>;
}) {
  const isAssistant = message.role === "assistant";
  const isStreaming = isAssistant && message.status === "streaming";
  const isCompleted = message.status === "completed";
  const isControlledIntent = Boolean(message.intent && message.intent !== "knowledge_query");
  const showMessageState = !isCompleted;
  const disposition = answerDispositionPresentation(message);
  const canShowSources = canShowMessageSources(message);
  const canShowAnswerDetails = canShowMessageKnowledgeDetails(message);
  const citedCitationRanks = isAssistant && isCompleted
    ? getCitedCitationRanks(message.content, message.citation_anchors)
    : undefined;
  const citationCount = citedCitationRanks?.length ?? message.citations_count ?? 0;

  return (
    <article
      className={`message-card${isAssistant ? " is-assistant" : " is-user"}${isStreaming ? " is-streaming" : ""}`}
      data-testid={isAssistant ? "assistant-message" : "user-message"}
      data-message-id={message.message_id}
    >
      {isCompleted && isAssistant ? (
        <div className="message-meta is-complete">
          <strong>助手</strong>
          {disposition ? <span className={`answer-disposition answer-disposition-${message.answer_disposition}`} data-testid="answer-disposition">{disposition.label}</span> : null}
        </div>
      ) : showMessageState ? (
        <div className="message-meta">
          <div>
            <strong>{isAssistant ? "助手" : "你"}</strong>
            <span>{isStreaming ? "正在生成" : formatDateTime(message.created_at)}</span>
          </div>
          <span className={`status-pill status-${message.status}`} data-testid="message-status">
            {formatMessageStatus(message.status)}
          </span>
        </div>
      ) : null}
      <p data-testid={isAssistant ? "assistant-message-content" : undefined}>
        {isStreaming && !message.content ? (
          <span className="streaming-copy">正在组织回答<span className="streaming-caret" aria-label="正在生成" /></span>
        ) : isAssistant && isCompleted ? (
          <>
            <CitationAnchorText
              content={message.content}
              anchors={message.citation_anchors}
              messageId={message.message_id}
              citationSources={citationSources}
              loadingSources={loadingSources}
              sourceError={sourceError}
              citationDrawerOpen={citationDrawerOpen}
              enabled={canShowSources}
              onPreviewAnchor={onPreviewCitationAnchor}
              onFocusAnchor={onFocusCitationAnchor}
              onCloseCitationDrawer={onCloseCitationDrawer}
            />
            {isStreaming ? <span className="streaming-caret" aria-label="正在生成" /> : null}
          </>
        ) : (
          <>
            {message.content}
            {isStreaming ? <span className="streaming-caret" aria-label="正在生成" /> : null}
          </>
        )}
      </p>
      {disposition?.coverage ? (
        <div className="answer-disposition-detail" data-testid="answer-disposition-coverage">
          <strong>已覆盖部分</strong>
          <span>本次已验证 {disposition.coverage.supported_count}/{disposition.coverage.requested_count} 项；未覆盖：{disposition.coverage.uncovered_description}</span>
        </div>
      ) : null}
      {isAssistant && isCompleted ? (
        <>
          {!isControlledIntent && canShowAnswerDetails ? (
            <details className="message-answer-details" data-testid="message-answer-details">
              <summary>回答信息</summary>
              <div className="message-answer-details-content">
              <MessageKnowledgeScope message={message} />
              <AnswerRevalidation
                message={message}
                state={revalidationState}
                onRevalidate={onRevalidate}
                onRefresh={onRefreshRevalidation}
              />
              </div>
            </details>
          ) : null}
          {canShowSources && citationCount > 0 ? (
            <div className="message-actions">
              <Button
                type="button"
                variant="ghost"
                className="citation-link h-auto w-auto whitespace-normal px-0 font-bold hover:bg-transparent"
                data-testid="assistant-citation-link"
                onClick={() => onFocusCitations(message.message_id, citedCitationRanks)}
              >
                查看 {citationCount} 条{citedCitationRanks ? "已引用来源" : "来源"}
              </Button>
            </div>
          ) : null}
        </>
      ) : null}
    </article>
  );
}

function CitationAnchorText({
  content,
  anchors,
  messageId,
  citationSources,
  loadingSources,
  sourceError,
  citationDrawerOpen,
  enabled,
  onPreviewAnchor,
  onFocusAnchor,
  onCloseCitationDrawer,
}: {
  content: string;
  anchors?: CitationAnchor[];
  messageId: string;
  citationSources?: MessageSourcesResponse;
  loadingSources: boolean;
  sourceError?: string;
  citationDrawerOpen: boolean;
  enabled: boolean;
  onPreviewAnchor: (messageId: string, citationRanks: number[]) => void;
  onFocusAnchor: (messageId: string, citationRanks: number[]) => void;
  onCloseCitationDrawer: () => void;
}) {
  const [previewKey, setPreviewKey] = useState<string>();
  if (!enabled) {
    return <>{content}</>;
  }
  const runes = Array.from(content);
  const validAnchors = getValidCitationAnchors(runes, anchors);
  const fragments: ReactNode[] = [];
  let cursor = 0;

  validAnchors.forEach((anchor) => {
    if (cursor < anchor.start_rune) {
      fragments.push(runes.slice(cursor, anchor.start_rune).join(""));
    }

    const key = `${anchor.start_rune}-${anchor.end_rune}-${anchor.citation_ranks.join("-")}`;
    const previewVisible = previewKey === key;
    fragments.push(
      <CitationFootprint
        key={key}
        content={runes.slice(anchor.start_rune, anchor.end_rune).join("")}
        messageId={messageId}
        citationRanks={anchor.citation_ranks}
        citationSources={citationSources}
        loadingSources={loadingSources}
        sourceError={sourceError}
        onPreview={() => {
          setPreviewKey(key);
          onPreviewAnchor(messageId, anchor.citation_ranks);
        }}
        onDismissPreview={() => setPreviewKey((current) => (current === key ? undefined : current))}
        onOpen={() => {
          setPreviewKey(key);
          onFocusAnchor(messageId, anchor.citation_ranks);
        }}
        onEscape={() => {
          if (citationDrawerOpen) {
            onCloseCitationDrawer();
          } else {
            setPreviewKey(undefined);
          }
        }}
        previewVisible={previewVisible}
      />,
    );
    cursor = anchor.end_rune;
  });

  if (cursor < runes.length) {
    fragments.push(runes.slice(cursor).join(""));
  }

  return <>{fragments}</>;
}

function CitationFootprint({
  content,
  messageId,
  citationRanks,
  citationSources,
  loadingSources,
  sourceError,
  onPreview,
  onDismissPreview,
  onOpen,
  onEscape,
  previewVisible,
}: {
  content: string;
  messageId: string;
  citationRanks: number[];
  citationSources?: MessageSourcesResponse;
  loadingSources: boolean;
  sourceError?: string;
  onPreview: () => void;
  onDismissPreview: () => void;
  onOpen: () => void;
  onEscape: () => void;
  previewVisible: boolean;
}) {
  return (
    <span className="citation-footprint">
      <ChromeButton
        type="button"
        className="citation-footprint-trigger select-text"
        data-testid="citation-anchor"
        data-citation-ranks={citationRanks.join(",")}
        aria-label="查看这段结论的依据"
        aria-expanded={previewVisible}
        onMouseEnter={onPreview}
        onFocus={onPreview}
        onMouseLeave={onDismissPreview}
        onBlur={onDismissPreview}
        onKeyDown={(event) => {
          if (event.key === "Escape") {
            event.preventDefault();
            onEscape();
          }
        }}
        onClick={onOpen}
      >
        {content}
      </ChromeButton>
      {previewVisible ? (
        <CitationAnchorPreview
          messageId={messageId}
          citationRanks={citationRanks}
          citationSources={citationSources}
          loadingSources={loadingSources}
          sourceError={sourceError}
        />
      ) : null}
    </span>
  );
}

function CitationAnchorPreview({
  messageId,
  citationRanks,
  citationSources,
  loadingSources,
  sourceError,
}: {
  messageId: string;
  citationRanks: number[];
  citationSources?: MessageSourcesResponse;
  loadingSources: boolean;
  sourceError?: string;
}) {
  const sourcesReady = citationSources?.message_id === messageId;
  const items = sourcesReady ? citationSources.items.filter((citation) => citationRanks.includes(citation.rank)) : [];
  const pending = !sourceError && (loadingSources || !sourcesReady);

  return (
    <span className="citation-anchor-preview" data-testid="citation-anchor-preview" role="tooltip">
      {pending ? <span className="citation-anchor-loading">正在加载依据...</span> : null}
      {sourceError ? <span className="citation-anchor-error">依据暂不可用</span> : null}
      {sourcesReady && !loadingSources && !sourceError
        ? items.map((citation) => (
            <span key={`${citation.rank}-${citation.chunk_id}`} className="citation-anchor-source">
              <strong>{citation.filename}</strong>
              <span>{formatLocator(citation.source_locator)}</span>
              <em>{citation.snippet}</em>
            </span>
          ))
        : null}
    </span>
  );
}

function getValidCitationAnchors(runes: string[], anchors: CitationAnchor[] | undefined) {
  const sorted = (anchors ?? [])
    .filter(
      (anchor) =>
        Number.isInteger(anchor.start_rune) &&
        Number.isInteger(anchor.end_rune) &&
        anchor.start_rune >= 0 &&
        anchor.end_rune > anchor.start_rune &&
        anchor.end_rune <= runes.length &&
        anchor.citation_ranks.length > 0 &&
        anchor.citation_ranks.every((rank) => Number.isInteger(rank) && rank > 0),
    )
    .slice()
    .sort((left, right) => left.start_rune - right.start_rune || left.end_rune - right.end_rune);

  const nonOverlapping: CitationAnchor[] = [];
  for (const anchor of sorted) {
    const previous = nonOverlapping[nonOverlapping.length - 1];
    if (previous && anchor.start_rune < previous.end_rune) {
      continue;
    }
    nonOverlapping.push(anchor);
  }
  return nonOverlapping;
}

function getCitedCitationRanks(content: string, anchors: CitationAnchor[] | undefined) {
  const ranks = new Set<number>();
  for (const anchor of getValidCitationAnchors(Array.from(content), anchors)) {
    for (const rank of anchor.citation_ranks) {
      ranks.add(rank);
    }
  }
  return ranks.size > 0 ? Array.from(ranks).sort((left, right) => left - right) : undefined;
}

function PendingTurnMessages({ turn }: { turn: PendingChatTurn }) {
  const failed = turn.phase === "failed";

  return (
    <>
      <article className="message-card is-user is-pending" data-testid="pending-user-message">
        <div className="message-meta">
          <div>
            <strong>你</strong>
            <span>{formatDateTime(turn.createdAt)}</span>
          </div>
          <span className="status-pill status-pending">{failed ? "未发送" : "发送中"}</span>
        </div>
        <p>{turn.content}</p>
      </article>
      <article className={`message-card is-assistant is-pending${failed ? " is-failed" : " is-streaming"}`} data-testid="pending-assistant-message">
        <div className="message-meta">
          <div>
            <strong>助手</strong>
            <span>{failed ? "回答未开始" : "正在处理请求"}</span>
          </div>
          <span className={`status-pill ${failed ? "status-failed" : "status-streaming"}`}>{failed ? "未完成" : "准备中"}</span>
        </div>
        {failed ? (
          <div className="pending-turn-error" data-testid="pending-turn-error" role="alert">
            <strong>{formatPendingTurnFailureTitle(turn.reason)}</strong>
            <p>{turn.reason ?? "管理员需要完成知识上传、索引并发布后，助手才能开始回答。"}</p>
          </div>
        ) : (
          <p className="streaming-copy">正在判断并处理请求<span className="streaming-caret" aria-label="正在处理" /></p>
        )}
      </article>
    </>
  );
}

export function CitationWorkspaceDrawer() {
  const { citationDrawerOpen, closeCitationDrawer } = useChatWorkspace();

  if (!citationDrawerOpen) {
    return null;
  }

  return (
    <>
      <ChromeButton
        type="button"
        className="citation-drawer-scrim"
        aria-label="关闭来源详情"
        onClick={closeCitationDrawer}
      />
      <aside
        className="citation-drawer"
        id="citation-panel"
        data-testid="citation-panel"
        data-layout-region="workspace-inspector"
        role="dialog"
        aria-label="来源详情"
      >
        <CitationWorkspacePanel />
      </aside>
    </>
  );
}

export function CitationWorkspacePanel() {
  const {
    selectedConversation,
    citationFocus,
    citationAnchorFocus,
    citations,
    citationSources,
    loadingSources,
    sourceError,
    closeCitationDrawer,
  } = useChatWorkspace();
  let citationRanks: number[] | undefined;
  if (citationAnchorFocus && citationAnchorFocus.messageId === citationFocus?.message_id) {
    citationRanks = citationAnchorFocus.citationRanks;
  }
  const visibleCitations = citationRanks ? citations.filter((citation) => citationRanks.includes(citation.rank)) : citations;

  return (
    <div className="content-stack detail-stack">
      <header className="citation-drawer-header">
        <div>
          <p className="eyebrow">回答依据</p>
          <h2>来源引用</h2>
        </div>
        <IconTooltip label="关闭来源详情">
          <ChromeButton
            type="button"
            className="icon-button"
            data-testid="citation-drawer-close"
            aria-label="关闭来源详情"
            onClick={closeCitationDrawer}
          >
            <span aria-hidden="true">x</span>
          </ChromeButton>
        </IconTooltip>
      </header>

      {!selectedConversation ? (
        <section className="detail-empty-state">
          <h3>等待聊天上下文。</h3>
          <p>选择包含引用的回答后，在这里查看它的知识依据。</p>
        </section>
      ) : (
        <>
          <section className="detail-summary">
            <h3>{selectedConversation.title}</h3>
            <p>{citationAnchorFocus ? "当前结论的依据" : citationFocus ? citationFocus.message_id : "暂无引用焦点"}</p>
          </section>

          <KnowledgeProvenancePanel message={citationFocus} sources={citationSources} />

          {loadingSources ? <p className="loading-copy">来源加载中...</p> : null}
          {sourceError ? <p className="inline-error">{sourceError}</p> : null}

          {!loadingSources && visibleCitations.length === 0 ? (
            <section className="detail-empty-state">
              <h3>当前消息暂无引用。</h3>
              <p>如果 assistant message 带有 citations，这里会按 rank 展示 citation 卡片。</p>
            </section>
          ) : (
            <div className="citation-list">
              {visibleCitations
                .slice()
                .sort((left, right) => left.rank - right.rank)
                .map((citation) => (
                  <article key={citation.chunk_id} className="citation-card">
                    <div className="citation-card-top">
                      <div>
                        <strong>{citation.filename}</strong>
                        <p>{citation.citation_label}</p>
                      </div>
                      <span>#{citation.rank}</span>
                    </div>
                    <p className="locator-copy">{formatLocator(citation.source_locator)}</p>
                    <blockquote>{citation.snippet}</blockquote>
                    <dl className="citation-provenance" data-testid="citation-provenance">
                      <div>
                        <dt>document</dt>
                        <dd>{citation.document_id ?? "未知（旧兼容数据）"}</dd>
                      </div>
                      <div>
                        <dt>revision</dt>
                        <dd data-testid="citation-revision">{citation.revision_id ?? "未知（旧兼容数据）"}</dd>
                      </div>
                      <div>
                        <dt>chunk hash</dt>
                        <dd>{citation.chunk_content_sha256 ?? "未知（旧兼容数据）"}</dd>
                      </div>
                    </dl>
                    <div className="citation-footer">
                      <span>{citation.file_id}</span>
                      <span>score {citation.score.toFixed(2)}</span>
                    </div>
                  </article>
                ))}
            </div>
          )}
        </>
      )}
    </div>
  );
}

function MessageKnowledgeScope({ message }: { message: Message }) {
  const scope = getKnowledgeScope(message);

  return (
    <div className="message-knowledge-scope" data-testid="message-knowledge-scope">
      <span>回答知识版本</span>
      <strong title={scope.snapshotID ?? undefined}>{scope.snapshotID ?? "未知（旧兼容数据）"}</strong>
      <FreshnessBadge freshness={scope.freshness} />
    </div>
  );
}

function AnswerRevalidation({
  message,
  state,
  onRevalidate,
  onRefresh,
}: {
  message: Message;
  state?: RevalidationState;
  onRevalidate: (message: Message) => Promise<void>;
  onRefresh: (messageId: string) => Promise<void>;
}) {
  if (!isRevalidationEligible(message)) {
    return null;
  }

  const run = state?.run;
  const isPending = run?.status === "queued" || run?.status === "running";

  return (
    <section className="answer-revalidation" data-testid="answer-revalidation">
      <div className="answer-revalidation-header">
        <h4>历史回答核验</h4>
        {run ? <span className={`status-badge status-${run.status}`}>{formatRevalidationStatus(run.status)}</span> : null}
      </div>
      {!run ? (
        <Button
          type="button"
          variant="outline"
          size="sm"
          data-testid="message-revalidate-action"
          onClick={() => void onRevalidate(message)}
          disabled={state?.submitting}
        >
          {state?.submitting ? "提交中" : "使用最新知识核验"}
        </Button>
      ) : (
        <>
          <div className="answer-revalidation-meta">
            <span>最新快照 {run.latest_snapshot_id}</span>
            <span>关联更新 {run.related_update_event_ids.length} 个</span>
            <span>请求于 {formatDateTime(run.requested_at)}</span>
          </div>
          {isPending ? (
            <Button
              type="button"
              variant="outline"
              size="sm"
              data-testid="message-revalidation-refresh"
              onClick={() => void onRefresh(message.message_id)}
              disabled={state?.refreshing}
            >
              {state?.refreshing ? "刷新中" : "刷新核验状态"}
            </Button>
          ) : null}
          {run.status === "failed" ? <p className="inline-error">{run.error?.message ?? "核验未能完成，请稍后重试。"}</p> : null}
          {run.status === "completed" && run.result ? <RevalidationResultView result={run.result} /> : null}
        </>
      )}
      {state?.error ? <p className="inline-error" role="alert">{state.error}</p> : null}
    </section>
  );
}

function RevalidationResultView({ result }: { result: NonNullable<RevalidationRun["result"]> }) {
  return (
    <section className="revalidation-result" data-testid="revalidation-result">
      <div className="answer-revalidation-header">
        <h4>最新核验结果</h4>
        <span className={`revalidation-outcome outcome-${result.outcome}`}>{formatRevalidationOutcome(result.outcome)}</span>
      </div>
      <p data-testid="revalidation-result-content">{result.content}</p>
      <dl>
        <div><dt>snapshot</dt><dd>{result.knowledge_snapshot_id}</dd></div>
        <div><dt>manifest</dt><dd>{result.knowledge_snapshot_manifest_hash}</dd></div>
        <div><dt>completed_at</dt><dd>{formatDateTime(result.completed_at)}</dd></div>
      </dl>
      {result.citations.length > 0 ? (
        <div className="revalidation-citation-list" data-testid="revalidation-citations">
          {result.citations.map((citation) => (
            <article key={citation.chunk_id}>
              <strong>{citation.filename}</strong>
              <span>{citation.revision_id ?? "未知 revision"}</span>
              <span>{formatLocator(citation.source_locator)}</span>
            </article>
          ))}
        </div>
      ) : null}
    </section>
  );
}

function KnowledgeProvenancePanel({
  message,
  sources,
}: {
  message?: Message;
  sources?: MessageSourcesResponse;
}) {
  const scope = getKnowledgeScope(message, sources);

  return (
    <section className="knowledge-provenance" data-testid="knowledge-provenance">
      <div className="knowledge-provenance-header">
        <h3>回答知识版本</h3>
        <FreshnessBadge freshness={scope.freshness} />
      </div>
      <dl>
        <div>
          <dt>snapshot</dt>
          <dd>{scope.snapshotID ?? "未知（旧兼容数据）"}</dd>
        </div>
        <div>
          <dt>manifest</dt>
          <dd>{scope.manifestHash ?? "未知（旧兼容数据）"}</dd>
        </div>
        <div>
          <dt>answered_at</dt>
          <dd>{scope.answeredAt ? formatDateTime(scope.answeredAt) : "未知（旧兼容数据）"}</dd>
        </div>
      </dl>
    </section>
  );
}

function FreshnessBadge({ freshness }: { freshness: FreshnessStatus }) {
  return (
    <span className={`freshness-badge freshness-${freshness}`} data-testid="knowledge-freshness">
      知识状态：{formatFreshness(freshness)}
    </span>
  );
}

function getKnowledgeScope(message?: Message, sources?: MessageSourcesResponse) {
  const snapshotID = message?.knowledge_snapshot_id;
  const hasMessageScope = Boolean(snapshotID);

  return {
    snapshotID,
    manifestHash: hasMessageScope
      ? message?.knowledge_snapshot_manifest_hash ?? sources?.knowledge_snapshot_manifest_hash
      : undefined,
    answeredAt: message?.answered_at,
    freshness: hasMessageScope ? message?.freshness_status ?? sources?.freshness_status ?? "unknown" : "unknown",
  } satisfies {
    snapshotID?: string;
    manifestHash?: string;
    answeredAt?: string;
    freshness: FreshnessStatus;
  };
}

function formatFreshness(freshness: FreshnessStatus) {
  switch (freshness) {
    case "current":
      return "当前";
    case "possibly_stale":
      return "可能过期";
    case "revalidated":
      return "已核验";
    case "unknown":
      return "未知";
  }
}

function formatMessageStatus(status: Message["status"]) {
  switch (status) {
    case "pending":
      return "等待发送";
    case "streaming":
      return "生成中";
    case "completed":
      return "已完成";
    case "failed":
      return "未完成";
  }
}

function formatPendingTurnFailureTitle(reason?: string) {
  if (reason?.includes("还没有已发布的知识版本")) {
    return "当前没有已发布的知识版本。";
  }
  return "本次回答未完成。";
}

function formatRevalidationStatus(status: RevalidationRun["status"]) {
  switch (status) {
    case "queued":
      return "已排队";
    case "running":
      return "核验中";
    case "completed":
      return "已完成";
    case "failed":
      return "失败";
  }
}

function formatRevalidationOutcome(outcome: NonNullable<RevalidationRun["outcome"]>) {
  switch (outcome) {
    case "still_valid":
      return "仍然有效";
    case "needs_update":
      return "需要更新";
    case "insufficient_evidence":
      return "证据不足";
  }
}

function isRevalidationEligible(message: Message) {
  return (
    message.role === "assistant" &&
    message.status === "completed" &&
    Boolean(message.knowledge_snapshot_id) &&
    Boolean(message.knowledge_snapshot_manifest_hash)
  );
}

function useChatWorkspace() {
  const context = useContext(ChatContext);
  if (!context) {
    throw new Error("Chat workspace components must be rendered inside ChatWorkspaceProvider.");
  }
  return context;
}

function findDefaultCitationFocus(messages: Message[] | null | undefined) {
  if (!messages) {
    return undefined;
  }

  return messages
    .slice()
    .reverse()
    .find((message) => canShowMessageSources(message) && (message.citations_count ?? 0) > 0);
}

function canShowMessageSources(message: Message) {
  if (message.role !== "assistant" || message.status !== "completed") {
    return false;
  }
  if (message.intent && message.intent !== "knowledge_query") {
    return false;
  }
  return !["clarification_required", "insufficient_evidence", "controlled_response"].includes(message.answer_disposition ?? "");
}

function canShowMessageKnowledgeDetails(message: Message) {
  return canShowMessageSources(message) || !message.answer_disposition;
}

function answerDispositionPresentation(message: Message) {
  switch (message.answer_disposition) {
    case "clarification_required":
      return { label: "需要补充范围" };
    case "insufficient_evidence":
      return { label: "当前知识不足" };
    case "partial_answer":
      return { label: "已覆盖部分", coverage: message.coverage };
    default:
      return undefined;
  }
}

function appendStartedMessages(current: Message[], userMessage: Message, assistantMessage: Message) {
  const existingIDs = new Set([userMessage.message_id, assistantMessage.message_id]);
  return [...current.filter((message) => !existingIDs.has(message.message_id)), userMessage, assistantMessage];
}

function appendMessageDelta(current: Message[], messageId: string, delta: string) {
  return current.map((message) => {
    if (message.message_id !== messageId) {
      return message;
    }
    return {
      ...message,
      content: `${message.content}${delta}`,
      status: "streaming" as const,
    };
  });
}

function replaceCompletedMessages(current: Message[], userMessage: Message, assistantMessage: Message) {
  let next = replaceOrAppendMessage(current, userMessage);
  next = replaceOrAppendMessage(next, assistantMessage);
  return next;
}

function replaceOrAppendMessage(current: Message[], nextMessage: Message) {
  if (!current.some((message) => message.message_id === nextMessage.message_id)) {
    return [...current, nextMessage];
  }
  return current.map((message) => (message.message_id === nextMessage.message_id ? nextMessage : message));
}

function markStreamingMessageFailed(current: Message[]) {
  return current.map((message) => {
    if (message.status !== "streaming") {
      return message;
    }
    return {
      ...message,
      status: "failed" as const,
      finish_reason: "error" as const,
    };
  });
}

const chatDraftStoragePrefix = "see-agent:chat-draft:";

function isUnauthenticatedError(error: unknown) {
  return error instanceof ApiError && error.code === "unauthenticated";
}

function currentChatPath() {
  const path = `${window.location.pathname}${window.location.search}`;
  return path.startsWith("/") && !path.startsWith("//") && !path.startsWith("/login") ? path : "/chat";
}

function storeChatDraft(path: string, draft: string) {
  if (!draft) {
    return;
  }
  window.sessionStorage.setItem(`${chatDraftStoragePrefix}${path}`, draft);
}

function consumeChatDraft(path: string) {
  const key = `${chatDraftStoragePrefix}${path}`;
  const draft = window.sessionStorage.getItem(key);
  window.sessionStorage.removeItem(key);
  return draft;
}

function readError(error: unknown) {
  if (error instanceof ApiError) {
    if (error.code === "runtime_unavailable") {
      return "Runtime 暂不可用，本次消息没有被伪装成成功回复。";
    }
    if (error.code === "knowledge_snapshot_unavailable") {
      return "还没有已发布的知识版本。管理员完成上传、索引和发布后即可重试。";
    }
    if (error.code === "snapshot_scope_violation") {
      return "知识范围校验未通过，本次回答未完成，请稍后重试。";
    }
    if (error.code === "runtime_citation_anchor_violation") {
      return "回答依据校验未通过，本次回答未完成，请稍后重试。";
    }
    if (error.code === "request_aborted") {
      return "已停止生成，可重试上一条。";
    }
    if (error.code === "backend_unavailable") {
      return "Backend 暂不可用，请稍后重试。";
    }
    return error.message;
  }
  if (
    (error instanceof DOMException && error.name === "AbortError") ||
    (error instanceof Error && error.name === "AbortError")
  ) {
    return "已停止生成，可重试上一条。";
  }
  return "聊天请求失败，请检查网络或服务状态后重试。";
}

const examplePrompts = [
  "员工手册里报销提交期限是多少天？",
  "总结上传知识中最影响一线执行的三条规则。",
  "找出当前知识库里关于审批链的直接证据。",
];
