"use client";

import { useEffect, useRef } from "react";
import { useCopilotMessagesContext, useCopilotChat } from "@copilotkit/react-core";
import type { ThreadItem } from "@/features/rpc/types";

export function useCopilotThreadSync(threadId: string, historyItems: ThreadItem[]) {
  const { setMessages } = useCopilotMessagesContext();
  const { reset } = useCopilotChat();
  const prevThreadIdRef = useRef(threadId);

  useEffect(() => {
    if (threadId !== prevThreadIdRef.current) {
      prevThreadIdRef.current = threadId;
      reset();
    }
  }, [threadId, reset]);

  useEffect(() => {
    if (!threadId || historyItems.length === 0) return;
    const copilotMessages = historyItems
      .filter((item) => item.role === "user" || item.role === "assistant")
      .map((item) => ({
        id: item.id,
        role: item.role as "user" | "assistant",
        content: item.content || "",
      }));
    if (copilotMessages.length > 0) {
      setMessages(copilotMessages as any);
    }
  }, [threadId, historyItems, setMessages]);
}
