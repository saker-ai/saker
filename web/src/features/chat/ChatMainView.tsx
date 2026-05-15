"use client";

import { useCallback } from "react";
import type { Thread, SkillInfo } from "@/features/rpc/types";
import { ThreadPanel } from "./ThreadPanel";
import { EmptyState } from "./EmptyState";
import { useT } from "@/features/i18n";
import { CopilotChat } from "@copilotkit/react-ui";
import "@copilotkit/react-ui/styles.css";
import "./copilot-theme.css";

export interface ChatMainViewProps {
  isMobile: boolean;
  mobileDrawerOpen: boolean;
  setMobileDrawerOpen: (open: boolean) => void;
  sortedThreads: Thread[];
  activeThreadId: string;
  switchThread: (id: string) => Promise<void>;
  createThread: () => Promise<void>;
  deleteThread: (id: string) => void;
  panelCollapsed: boolean;
  wsHealthy: boolean;
  onAutoCreateThread: (title: string) => Promise<void>;
  skills: SkillInfo[];
}

function generateTitle(text: string): string {
  const firstSentence = text.split(/[。.!?！？\n]/)[0].trim();
  if (firstSentence.length > 0 && firstSentence.length <= 40) {
    return firstSentence;
  }
  if (text.length <= 40) return text;
  const truncated = text.slice(0, 40);
  const lastSpace = truncated.lastIndexOf(" ");
  return (lastSpace > 20 ? truncated.slice(0, lastSpace) : truncated) + "...";
}

export function ChatMainView({
  isMobile,
  mobileDrawerOpen,
  setMobileDrawerOpen,
  sortedThreads,
  activeThreadId,
  switchThread,
  createThread,
  deleteThread,
  panelCollapsed,
  wsHealthy,
  onAutoCreateThread,
  skills,
}: ChatMainViewProps) {
  const { t } = useT();

  const handleSubmitMessage = useCallback(
    async (text: string) => {
      if (!activeThreadId) {
        await onAutoCreateThread(generateTitle(text));
      }
    },
    [activeThreadId, onAutoCreateThread],
  );

  return (
    <>
      {isMobile && mobileDrawerOpen && (
        <div
          className="thread-panel-overlay"
          onClick={() => setMobileDrawerOpen(false)}
        />
      )}
      <ThreadPanel
        threads={sortedThreads}
        activeThreadId={activeThreadId}
        onSelectThread={(id) => {
          switchThread(id);
          if (isMobile) setMobileDrawerOpen(false);
        }}
        onCreateThread={() => {
          createThread();
          if (isMobile) setMobileDrawerOpen(false);
        }}
        onDeleteThread={deleteThread}
        collapsed={isMobile ? !mobileDrawerOpen : panelCollapsed}
        connected={wsHealthy}
        mobileDrawer={isMobile}
        mobileOpen={mobileDrawerOpen}
      />
      <div className="main" id="main-content">
        {!wsHealthy && (
          <div className="connection-status" role="alert">
            {t("chat.disconnected")}
          </div>
        )}

        {!activeThreadId && (
          <div className="messages">
            <EmptyState
              connected={wsHealthy}
              skills={skills}
            />
          </div>
        )}
        <CopilotChat
          className={`saker-copilot-chat${activeThreadId ? " saker-copilot-chat--active" : ""}`}
          onSubmitMessage={handleSubmitMessage}
          labels={{
            title: "",
            placeholder: t("composer.placeholder"),
            initial: "",
          }}
          icons={{
            sendIcon: undefined,
            activityIcon: undefined,
          }}
        />
      </div>
    </>
  );
}
