"use client";

import type { ReactNode } from "react";
import type { ThreadItem } from "@/features/rpc/types";
import { useAguiHitlActions } from "./hitlActions";
import { useCopilotThreadSync } from "./useCopilotThreadSync";

export interface CopilotBridgeProps {
  threadId: string;
  historyMessages: ThreadItem[];
  children: ReactNode;
}

export function CopilotBridge({ threadId, historyMessages, children }: CopilotBridgeProps) {
  useAguiHitlActions();
  useCopilotThreadSync(threadId, historyMessages);
  return <>{children}</>;
}
