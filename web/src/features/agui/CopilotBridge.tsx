"use client";

import type { ReactNode } from "react";
import { useAguiHitlActions } from "./hitlActions";
import { useCopilotThreadSync } from "./useCopilotThreadSync";

export function CopilotBridge({ threadId, children }: { threadId: string; children: ReactNode }) {
  useAguiHitlActions();
  useCopilotThreadSync(threadId);
  return <>{children}</>;
}
