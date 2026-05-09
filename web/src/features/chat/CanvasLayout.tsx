"use client";

import { X, MessageCircle } from "lucide-react";
import type { Thread, ThreadItem, ApprovalRequest, QuestionRequest, StreamEvent, SkillInfo } from "@/features/rpc/types";
import { CanvasView } from "@/features/canvas/CanvasView";
import { ThreadPanel } from "./ThreadPanel";
import { StatusBar } from "./StatusBar";
import { Composer, type Attachment } from "./Composer";
import { ChatStream } from "./ChatStream";
import { StarterState } from "./StarterState";
import { useT } from "@/features/i18n";
import type { TurnStatus } from "./chatUtils";

export interface CanvasLayoutProps {
  sortedThreads: Thread[];
  activeThreadId: string;
  activeThread: Thread | undefined;
  switchThread: (id: string) => Promise<void>;
  createThread: () => Promise<void>;
  deleteThread: (id: string) => void;
  panelCollapsed: boolean;
  wsHealthy: boolean;
  canvasChatOpen: boolean;
  setCanvasChatOpen: (open: boolean) => void;
  canvasHasNodes: boolean;
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

export function CanvasLayout({
  sortedThreads,
  activeThreadId,
  activeThread,
  switchThread,
  createThread,
  deleteThread,
  panelCollapsed,
  wsHealthy,
  canvasChatOpen,
  setCanvasChatOpen,
  canvasHasNodes,
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
}: CanvasLayoutProps) {
  const { t } = useT();

  return (
    <>
      <ThreadPanel
        threads={sortedThreads}
        activeThreadId={activeThreadId}
        onSelectThread={switchThread}
        onCreateThread={createThread}
        onDeleteThread={deleteThread}
        collapsed={panelCollapsed}
        connected={wsHealthy}
      />
      <div className={`canvas-layout${panelCollapsed ? "" : " panel-expanded"}`}>
        {/* Canvas area — shrinks when drawer opens */}
        <div className="canvas-area">
          <CanvasView />
          {!canvasChatOpen && !canvasHasNodes && (
            <div className="composer-area floating-composer">
              <StatusBar connected={wsHealthy} turnStatus={turnStatus} />
              <Composer
                onSend={activeThreadId ? sendMessage : sendWithAutoCreate}
                onStop={cancelTurn}
                disabled={!wsHealthy || turnStatus === "running"}
                running={turnStatus === "running"}
                skills={skills}
              />
            </div>
          )}
          {/* Floating chat ball */}
          {!canvasChatOpen && (
            <button
              className="canvas-chat-fab"
              onClick={() => { setCanvasChatOpen(true); requestAnimationFrame(() => window.dispatchEvent(new Event("resize"))); }}
              aria-label={t("chat.openChat")}
            >
              <MessageCircle size={22} strokeWidth={1.75} />
            </button>
          )}
        </div>

        {/* Right-side chat drawer — same level as canvas */}
        {canvasChatOpen && (
          <div className="canvas-chat-drawer">
            <div className="canvas-chat-drawer-header">
              <h4 className="canvas-chat-drawer-title">
                {activeThread?.title || t("nav.chats")}
              </h4>
              <button
                className="canvas-chat-drawer-close"
                onClick={() => { setCanvasChatOpen(false); requestAnimationFrame(() => window.dispatchEvent(new Event("resize"))); }}
                aria-label={t("chat.closeChat")}
              >
                <X size={18} strokeWidth={2} />
              </button>
            </div>
            <div className="canvas-chat-drawer-messages">
              {!activeThreadId ? (
                <div className="canvas-chat-drawer-empty">
                  <p>{t("chat.selectOrCreate")}</p>
                </div>
              ) : messages.length === 0 && !streamText && turnStatus === "idle" ? (
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

            <div className="composer-area floating-composer">
              <Composer
                onSend={activeThreadId ? sendMessage : sendWithAutoCreate}
                onStop={cancelTurn}
                disabled={!wsHealthy || turnStatus === "running"}
                running={turnStatus === "running"}
                skills={skills}
              />
            </div>
          </div>
        )}
      </div>
    </>
  );
}