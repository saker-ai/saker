"use client";

import { useCallback } from "react";
import { useCopilotAction } from "@copilotkit/react-core";
import { ApprovalCard } from "@/features/chat/ApprovalCard";
import { QuestionCard } from "@/features/chat/QuestionCard";

function resolveApiBase(): string {
  if (typeof window === "undefined") return "";
  const loc = window.location;
  if (loc.port === "10111") {
    return `${loc.protocol}//${loc.hostname}:10112`;
  }
  return "";
}

async function postHitlResponse(runId: string, path: string, body: Record<string, unknown>) {
  const base = resolveApiBase();
  await fetch(`${base}/v1/agents/run/${runId}/${path}`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    credentials: "include",
    body: JSON.stringify(body),
  });
}

export function useApprovalAction() {
  useCopilotAction({
    name: "approval_request",
    description: "Tool use approval request",
    parameters: [
      { name: "approval_id", type: "string", description: "Approval ID" },
      { name: "run_id", type: "string", description: "Run ID" },
      { name: "tool_name", type: "string", description: "Tool name" },
      { name: "tool_params", type: "object" as any, description: "Tool parameters" },
      { name: "reason", type: "string", description: "Reason for approval" },
    ],
    renderAndWait: ({ args, handler }) => {
      const approval = {
        id: args.approval_id as string,
        thread_id: "",
        turn_id: args.run_id as string,
        tool_name: args.tool_name as string,
        tool_params: (args.tool_params as Record<string, unknown>) ?? {},
        reason: (args.reason as string) ?? "",
      };

      const onRespond = (id: string, decision: "allow" | "deny") => {
        postHitlResponse(args.run_id as string, "approval", {
          approval_id: id,
          decision,
        });
        handler?.({ decision });
      };

      return <ApprovalCard approval={approval} onRespond={onRespond} />;
    },
  });
}

export function useQuestionAction() {
  useCopilotAction({
    name: "question_request",
    description: "Interactive question request",
    parameters: [
      { name: "question_id", type: "string", description: "Question ID" },
      { name: "run_id", type: "string", description: "Run ID" },
      { name: "questions", type: "object[]" as any, description: "Questions" },
    ],
    renderAndWait: ({ args, handler }) => {
      const questionReq = {
        id: args.question_id as string,
        thread_id: "",
        turn_id: args.run_id as string,
        questions: ((args.questions as any[]) ?? []).map((q: any) => ({
          question: q.question ?? "",
          header: q.header ?? "",
          options: (q.options ?? []).map((o: any) => ({
            label: o.label ?? "",
            description: o.description ?? "",
          })),
          multiSelect: q.multi_select ?? false,
        })),
      };

      const onRespond = useCallback(
        (id: string, answers: Record<string, string>) => {
          postHitlResponse(args.run_id as string, "answer", {
            question_id: id,
            answers,
          });
          handler?.({ answers });
        },
        [args.run_id, handler],
      );

      return <QuestionCard question={questionReq} onRespond={onRespond} />;
    },
  });
}

export function useAguiHitlActions() {
  useApprovalAction();
  useQuestionAction();
}
