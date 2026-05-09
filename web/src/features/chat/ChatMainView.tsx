"use client";

import type { Thread, ThreadItem, ApprovalRequest, QuestionRequest, StreamEvent, SkillInfo } from "@/features/rpc/types";
import { ThreadPanel } from "./ThreadPanel";
import { StatusBar } from "./StatusBar";
import { Composer, type Attachment } from "./Composer";
import { ChatStream } from "./ChatStream";
import { EmptyState } from "./EmptyState";
import { StarterState } from "./StarterState";
import { useT } from "@/features/i18n";
import type { TurnStatus } from "./chatUtils";

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
  messages: ThreadItem[];
  streamText: string;
  turnStatus: TurnStatus;
  toolEvents: StreamEvent[];
  highlightedTurnId: string | null;
  approvals: ApprovalRequest[];
  questions: QuestionRequest[];
  onApproval: (id: string, decision: "allow" | "deny") => void;
  onQuestionRespond: (id: string, answers: Record<string, string>) => void;
  sendMessage: (text: string, attachments?: Attachment[]) => void;
  sendWithAutoCreate: (text: string, attachments?: Attachment[]) => void;
  cancelTurn: () => void;
  skills: SkillInfo[];
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
  messages,
  streamText,
  turnStatus,
  toolEvents,
  highlightedTurnId,
  approvals,
  questions,
  onApproval,
  onQuestionRespond,
  sendMessage,
  sendWithAutoCreate,
  cancelTurn,
  skills,
}: ChatMainViewProps) {
  const { t } = useT();

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

        <div
          className={`messages${
            activeThreadId &&
            !(messages.length === 0 && !streamText && turnStatus === "idle")
              ? " messages--threaded"
              : ""
          }`}
        >
          {!activeThreadId ? (
            <EmptyState
              connected={wsHealthy}
              onSend={sendWithAutoCreate}
              skills={skills}
            />
          ) : messages.length === 0 &&
            !streamText &&
            turnStatus === "idle" ? (
            <StarterState onSend={sendMessage} />
          ) : (
            <ChatStream
              messages={messages}
              streamText={streamText}
              streaming={turnStatus === "running"}
              toolEvents={toolEvents}
              highlightedTurnId={highlightedTurnId}
              approvals={approvals}
              questions={questions}
              onApproval={onApproval}
              onQuestionRespond={onQuestionRespond}
            />
          )}
        </div>

        {activeThreadId && (
          <div className="composer-area floating-composer">
            <StatusBar connected={wsHealthy} turnStatus={turnStatus} />
            <Composer
              onSend={sendMessage}
              onStop={cancelTurn}
              disabled={!wsHealthy || turnStatus === "running"}
              running={turnStatus === "running"}
              skills={skills}
            />
          </div>
        )}
      </div>
    </>
  );
}