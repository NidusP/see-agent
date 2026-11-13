"use client";

import Link from "next/link";
import { Dialog } from "@base-ui/react/dialog";
import { X } from "lucide-react";
import { useEffect, useState, type ComponentProps, type ReactElement, type ReactNode } from "react";

import {
  ChatConversationList,
  ChatNewConversationButton,
  ChatWorkspacePanel,
  ChatWorkspaceProvider,
  CitationWorkspaceDrawer,
} from "./chat-workspace";
import { ConfigDetailWorkspace, ConfigWorkspace, ConfigWorkspaceProvider } from "./config-workspace";
import { KnowledgeDetailWorkspace, KnowledgeWorkspace, KnowledgeWorkspaceProvider } from "./knowledge-workspace";
import { LogoutButton } from "./logout-button";
import { ThemePicker } from "./theme-picker";
import { Button } from "./ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";
import { cn } from "../_lib/utils";
import { isKnowledgeAdmin, isSystemAdmin, sessionRoleLabel, type SessionRole } from "../_lib/role";

type WorkspaceSection = "chat" | "knowledge" | "config";

const sectionLinks: Array<{ href: string; label: string; section: WorkspaceSection; requiredRole?: "knowledge_admin" | "system_admin" }> = [
  { href: "/chat", label: "对话", section: "chat" },
  { href: "/knowledge", label: "知识库", section: "knowledge", requiredRole: "knowledge_admin" },
  { href: "/config", label: "配置", section: "config", requiredRole: "system_admin" },
];

const sidebarStorageKey = "see-agent:chat-sidebar-collapsed";

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

interface WorkspaceShellProps {
  activeSection: WorkspaceSection;
  selectedConversationId?: string;
  showChatEmptyState?: boolean;
  currentRole?: SessionRole;
}

export function WorkspaceShell({
  activeSection,
  selectedConversationId,
  showChatEmptyState = false,
  currentRole,
}: WorkspaceShellProps) {
  if (isSystemAdmin(currentRole)) {
    return <SystemConfigurationShell currentRole={currentRole} />;
  }

  return (
    <ChatWorkspaceProvider initialSelectedConversationId={selectedConversationId} showEmptyState={showChatEmptyState}>
      <ChatFirstWorkspace activeSection={activeSection} currentRole={currentRole} />
    </ChatWorkspaceProvider>
  );
}

function SystemConfigurationShell({ currentRole }: { currentRole?: SessionRole }) {
  const [detailsOpen, setDetailsOpen] = useState(false);
  return (
    <main className="viewport-workspace-shell system-config-shell" data-testid="system-config-shell">
      <header className="system-config-topbar">
        <Link href="/config" className="system-config-brand" aria-label="系统配置首页">
          <span className="chat-sidebar-mark" aria-hidden="true">SA</span>
          <span><strong>智能问答</strong><small data-testid="workspace-current-section">系统配置</small></span>
        </Link>
        <div className="chat-topbar-actions">
          <ThemePicker compact />
          <span className="chat-topbar-role">{sessionRoleLabel(currentRole)}</span>
          <LogoutButton />
        </div>
      </header>
      <ConfigWorkspaceProvider>
        <section className="system-config-layout" data-testid="system-config-layout">
          <section className="app-main">
            <ConfigWorkspace onOpenDetails={() => setDetailsOpen(true)} />
          </section>
        </section>
        <WorkspaceDetailDialog open={detailsOpen} title="配置详情" onClose={() => setDetailsOpen(false)}>
          <ConfigDetailWorkspace />
        </WorkspaceDetailDialog>
      </ConfigWorkspaceProvider>
    </main>
  );
}

function ChatFirstWorkspace({ activeSection, currentRole }: Omit<WorkspaceShellProps, "selectedConversationId" | "showChatEmptyState">) {
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false);
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const [workspaceMenuOpen, setWorkspaceMenuOpen] = useState(false);
  const [sidebarPreferenceLoaded, setSidebarPreferenceLoaded] = useState(false);
  const [detailsOpen, setDetailsOpen] = useState(false);
  const canManageKnowledge = isKnowledgeAdmin(currentRole);
  const canManageSystem = isSystemAdmin(currentRole);
  const visibleSectionLinks = sectionLinks.filter((link) => !link.requiredRole || (link.requiredRole === "knowledge_admin" ? canManageKnowledge : canManageSystem));
  const activeSectionLabel = sectionLinks.find((link) => link.section === activeSection)?.label ?? "对话";

  useEffect(() => {
    const timer = window.setTimeout(() => {
      setSidebarCollapsed(window.localStorage.getItem(sidebarStorageKey) === "true");
      setSidebarPreferenceLoaded(true);
    }, 0);
    return () => window.clearTimeout(timer);
  }, []);

  useEffect(() => {
    if (sidebarPreferenceLoaded) {
      window.localStorage.setItem(sidebarStorageKey, String(sidebarCollapsed));
    }
  }, [sidebarCollapsed, sidebarPreferenceLoaded]);

  useEffect(() => {
    const closeTransientNavigation = (event: KeyboardEvent) => {
      if (event.key !== "Escape") {
        return;
      }
      setWorkspaceMenuOpen(false);
      setSidebarOpen(false);
    };
    window.addEventListener("keydown", closeTransientNavigation);
    return () => window.removeEventListener("keydown", closeTransientNavigation);
  }, []);

  const closeMobileSidebar = () => setSidebarOpen(false);
  const toggleSidebar = () => setSidebarCollapsed((current) => !current);

  return (
    <main
      className={`viewport-workspace-shell chat-first-shell${sidebarCollapsed ? " is-sidebar-collapsed" : ""}${sidebarOpen ? " is-sidebar-open" : ""}`}
      data-testid="chat-first-shell"
    >
      <aside className="chat-sidebar" aria-label="会话历史" data-testid="conversation-context-pane">
        <div className="chat-sidebar-top">
          <Link href="/chat" className="chat-sidebar-brand" aria-label="智能问答首页" onClick={closeMobileSidebar}>
            <span className="chat-sidebar-mark" aria-hidden="true">SA</span>
            <span className="chat-sidebar-brand-copy">智能问答</span>
          </Link>
          <IconTooltip label={sidebarCollapsed ? "展开会话侧栏" : "收起会话侧栏"}>
            <ChromeButton
              type="button"
              className="icon-button chat-sidebar-collapse"
              data-testid="chat-sidebar-toggle"
              aria-label={sidebarCollapsed ? "展开会话侧栏" : "收起会话侧栏"}
              onClick={toggleSidebar}
            >
              <span aria-hidden="true">{sidebarCollapsed ? ">" : "<"}</span>
            </ChromeButton>
          </IconTooltip>
          <IconTooltip label="关闭会话侧栏">
            <ChromeButton
              type="button"
              className="icon-button chat-sidebar-mobile-close"
              data-testid="chat-sidebar-mobile-close"
              aria-label="关闭会话侧栏"
              onClick={closeMobileSidebar}
            >
              <span aria-hidden="true">×</span>
            </ChromeButton>
          </IconTooltip>
        </div>

        <div className="chat-sidebar-new">
          <ChatNewConversationButton />
        </div>

        <div className="chat-sidebar-conversations">
          <ChatConversationList onConversationNavigate={closeMobileSidebar} />
        </div>

        <div className="chat-sidebar-footer">
          <span className="role-label">{sessionRoleLabel(currentRole)}</span>
          <LogoutButton />
        </div>
      </aside>

      <ChromeButton
        type="button"
        className="chat-sidebar-scrim"
        aria-label="关闭会话侧栏"
        onClick={closeMobileSidebar}
      />

      <section className="chat-first-frame" data-testid="chat-workspace-frame">
        <header className="chat-topbar">
          <div className="chat-topbar-leading">
            <IconTooltip label="打开会话侧栏">
              <ChromeButton
                type="button"
                className="icon-button chat-mobile-sidebar-toggle"
                data-testid="chat-sidebar-mobile-toggle"
                aria-label="打开会话侧栏"
                aria-expanded={sidebarOpen}
                onClick={() => setSidebarOpen(true)}
              >
                <span aria-hidden="true">=</span>
              </ChromeButton>
            </IconTooltip>
            {canManageKnowledge ? (
              <div className="workspace-switcher">
                <ChromeButton
                  type="button"
                  className="workspace-switcher-trigger"
                  data-testid="workspace-switcher"
                  aria-haspopup="menu"
                  aria-expanded={workspaceMenuOpen}
                  onClick={() => setWorkspaceMenuOpen((current) => !current)}
                >
                  <span data-testid="workspace-current-section">{activeSectionLabel}</span> <span aria-hidden="true">v</span>
                </ChromeButton>
                {workspaceMenuOpen ? (
                  <nav className="workspace-switcher-menu" aria-label="管理工作区" data-testid="workspace-switcher-menu">
                    {visibleSectionLinks.map((link) => (
                      <Link
                        key={link.section}
                        href={link.href}
                        className={activeSection === link.section ? "is-active" : undefined}
                        aria-current={activeSection === link.section ? "page" : undefined}
                        onClick={() => {
                          setWorkspaceMenuOpen(false);
                          closeMobileSidebar();
                        }}
                      >
                        {link.label}
                      </Link>
                    ))}
                  </nav>
                ) : null}
              </div>
            ) : (
              <span className="workspace-product-name" data-testid="workspace-current-section">{activeSectionLabel}</span>
            )}
          </div>
          <div className="chat-topbar-actions">
            <ThemePicker compact />
            <span className="chat-topbar-role">{canManageSystem ? "系统管理工作区" : canManageKnowledge ? "知识库管理工作区" : "普通用户"}</span>
          </div>
        </header>

        {activeSection === "knowledge" ? (
          <KnowledgeWorkspaceProvider currentRole={currentRole}>
            <section className="management-layout" data-testid="management-layout">
              <section className="app-main">
                <KnowledgeWorkspace onOpenDetails={() => setDetailsOpen(true)} />
              </section>
            </section>
            <WorkspaceDetailDialog open={detailsOpen} title="知识库详情" onClose={() => setDetailsOpen(false)}>
              <KnowledgeDetailWorkspace />
            </WorkspaceDetailDialog>
          </KnowledgeWorkspaceProvider>
        ) : activeSection === "config" ? (
          <ConfigWorkspaceProvider>
            <section className="management-layout" data-testid="management-layout">
              <section className="app-main">
                <ConfigWorkspace onOpenDetails={() => setDetailsOpen(true)} />
              </section>
            </section>
            <WorkspaceDetailDialog open={detailsOpen} title="配置详情" onClose={() => setDetailsOpen(false)}>
              <ConfigDetailWorkspace />
            </WorkspaceDetailDialog>
          </ConfigWorkspaceProvider>
        ) : (
          <section className="chat-first-main chat-main" data-testid="chat-main">
            <ChatWorkspacePanel />
          </section>
        )}
      </section>

      {activeSection === "chat" ? <CitationWorkspaceDrawer /> : null}
    </main>
  );
}

function WorkspaceDetailDialog({ open, title, onClose, children }: { open: boolean; title: string; onClose: () => void; children: ReactNode }) {
  return (
    <Dialog.Root open={open} onOpenChange={(nextOpen) => { if (!nextOpen) onClose(); }}>
      <Dialog.Portal>
        <Dialog.Backdrop className="workspace-detail-scrim" />
        <Dialog.Popup className="workspace-detail-dialog" data-testid="workspace-detail-dialog">
          <header className="workspace-detail-dialog-header">
            <Dialog.Title>{title}</Dialog.Title>
            <Dialog.Close
              data-testid="workspace-detail-close"
              aria-label="关闭详情"
              render={<Button variant="ghost" size="icon" className="icon-button h-auto" />}
            >
              <X size={18} aria-hidden="true" />
            </Dialog.Close>
          </header>
          <div className="workspace-detail-dialog-content">{children}</div>
        </Dialog.Popup>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
