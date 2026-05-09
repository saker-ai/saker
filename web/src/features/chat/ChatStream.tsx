import type { ThreadItem, ApprovalRequest, QuestionRequest, StreamEvent } from "@/features/rpc/types";
import { MessageStream } from "./MessageStream";
import { ApprovalCard } from "./ApprovalCard";
import { QuestionCard } from "./QuestionCard";

/** Renders the active turn: assistant stream, tool cards, then any pending
 * approval/question prompts. Used by both the main view and the canvas chat
 * drawer — keeping it here avoids drifting two near-identical JSX blocks. */
export function ChatStream({
  messages,
  streamText,
  streaming,
  toolEvents,
  highlightedTurnId,
  approvals,
  questions,
  onApproval,
  onQuestionRespond,
}: {
  messages: ThreadItem[];
  streamText: string;
  streaming: boolean;
  toolEvents: StreamEvent[];
  highlightedTurnId: string | null;
  approvals: ApprovalRequest[];
  questions: QuestionRequest[];
  onApproval: (id: string, decision: "allow" | "deny") => void;
  onQuestionRespond: (id: string, answers: Record<string, string>) => void;
}) {
  return (
    <>
      <MessageStream
        messages={messages}
        streamText={streamText}
        streaming={streaming}
        toolEvents={toolEvents}
        highlightedTurnId={highlightedTurnId}
      />
      {approvals.map((a) => (
        <ApprovalCard key={a.id} approval={a} onRespond={onApproval} />
      ))}
      {questions.map((q) => (
        <QuestionCard key={q.id} question={q} onRespond={onQuestionRespond} />
      ))}
    </>
  );
}