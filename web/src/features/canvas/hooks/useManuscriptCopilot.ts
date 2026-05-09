import { useCallback, useEffect, useState } from "react";

export interface CopilotProposal {
  content: string;
  scope: "selection" | "document" | "entity";
  sourceText?: string;
  selectionStart?: number;
  selectionEnd?: number;
}

export function useManuscriptCopilot(
  nodeId: string,
  fullContent: string,
  selectedText: string,
  updateFullContent: (content: string) => void,
  markdownRef: React.RefObject<HTMLTextAreaElement | null>,
) {
  const [copilotInput, setCopilotInput] = useState("");
  const [copilotScope, setCopilotScope] = useState<"selection" | "document">("document");
  const [proposal, setProposal] = useState<CopilotProposal | null>(null);

  const dispatchAiCommand = useCallback((prompt: string, meta?: Partial<CopilotProposal>) => {
    window.dispatchEvent(
      new CustomEvent("manuscript-ai-command", {
        detail: { prompt, branchNodeId: nodeId, nodeId, ...meta },
      }),
    );
  }, [nodeId]);

  const applyProposal = useCallback(() => {
    if (!proposal) return;
    if (proposal.scope === "selection" && typeof proposal.selectionStart === "number" && typeof proposal.selectionEnd === "number") {
      const next = `${fullContent.slice(0, proposal.selectionStart)}${proposal.content}${fullContent.slice(proposal.selectionEnd)}`;
      updateFullContent(next);
    } else {
      updateFullContent(proposal.content);
    }
    setProposal(null);
  }, [proposal, fullContent, updateFullContent]);

  const rejectProposal = useCallback(() => setProposal(null), []);

  const handleAiResult = useCallback((event: Event) => {
    const detail = (event as CustomEvent<{
      nodeId: string;
      scope?: "selection" | "document" | "entity";
      sourceText?: string;
      selectionStart?: number;
      selectionEnd?: number;
      content?: string;
    }>).detail;
    if (!detail || detail.nodeId !== nodeId || !detail.content?.trim()) return;
    setProposal({
      content: detail.content.trim(),
      scope: detail.scope || "document",
      sourceText: detail.sourceText,
      selectionStart: detail.selectionStart,
      selectionEnd: detail.selectionEnd,
    });
  }, [nodeId]);

  useEffect(() => {
    window.addEventListener("manuscript-ai-result", handleAiResult);
    return () => window.removeEventListener("manuscript-ai-result", handleAiResult);
  }, [handleAiResult]);

  const runCopilot = useCallback(() => {
    const trimmed = copilotInput.trim();
    if (!trimmed) return;
    const targetText = copilotScope === "selection" && selectedText ? selectedText : fullContent;
    const scopeLabel = copilotScope === "selection" && selectedText ? "当前选中文本" : "整篇灵动文稿";
    const textarea = markdownRef.current;
    dispatchAiCommand(`请基于${scopeLabel}执行这个任务：${trimmed}\n\n上下文如下：\n${targetText}`, {
      scope: copilotScope === "selection" && selectedText ? "selection" : "document",
      sourceText: targetText,
      selectionStart: textarea?.selectionStart,
      selectionEnd: textarea?.selectionEnd,
    });
    setCopilotInput("");
  }, [copilotInput, copilotScope, selectedText, fullContent, dispatchAiCommand, markdownRef]);

  return {
    copilotInput,
    setCopilotInput,
    copilotScope,
    setCopilotScope,
    proposal,
    dispatchAiCommand,
    applyProposal,
    rejectProposal,
    runCopilot,
  };
}
