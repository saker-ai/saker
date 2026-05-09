import { useCallback, useMemo, useState } from "react";
import type { CanvasNodeData, ManuscriptSection } from "../types";
import { useCanvasStore } from "../store";
import { renderMarkdown } from "@/features/chat/markdown";
import {
  addManuscriptSection,
  manuscriptToNodeData,
  normalizeManuscriptData,
  removeManuscriptSection,
  renameManuscriptEntity,
  updateManuscriptContent,
  updateManuscriptSection,
  type StructuredManuscript,
} from "../manuscript";

function cloneManuscript(manuscript: StructuredManuscript): StructuredManuscript {
  return structuredClone(manuscript);
}

export function useManuscriptDraft(nodeId: string, data: CanvasNodeData) {
  const updateNode = useCanvasStore((s) => s.updateNode);
  const initial = useMemo(
    () => normalizeManuscriptData(data) as CanvasNodeData & StructuredManuscript,
    [data],
  );
  const [draft, setDraft] = useState<StructuredManuscript>({
    manuscriptTitle: initial.manuscriptTitle,
    manuscriptSummary: initial.manuscriptSummary,
    manuscriptSections: initial.manuscriptSections,
    manuscriptEntities: initial.manuscriptEntities,
    manuscriptViewMode: "fullscreen",
    manuscriptEditorMode: initial.manuscriptEditorMode || "markdown",
  });
  const [past, setPast] = useState<StructuredManuscript[]>([]);
  const [future, setFuture] = useState<StructuredManuscript[]>([]);

  const persist = useCallback((next: StructuredManuscript) => {
    updateNode(nodeId, manuscriptToNodeData(data, next));
  }, [data, nodeId, updateNode]);

  const commit = useCallback((next: StructuredManuscript) => {
    setPast((items) => [...items, cloneManuscript(draft)].slice(-50));
    setFuture([]);
    setDraft(next);
    persist(next);
  }, [draft, persist]);

  const updateTitle = useCallback((title: string) => {
    commit({ ...draft, manuscriptTitle: title });
  }, [commit, draft]);

  const updateSummary = useCallback((summary: string) => {
    commit({ ...draft, manuscriptSummary: summary });
  }, [commit, draft]);

  const updateSection = useCallback((sectionId: string, patch: Partial<ManuscriptSection>) => {
    commit(updateManuscriptSection(draft, sectionId, patch));
  }, [commit, draft]);

  const addSection = useCallback((afterSectionId?: string) => {
    commit(addManuscriptSection(draft, afterSectionId));
  }, [commit, draft]);

  const removeSection = useCallback((sectionId: string) => {
    commit(removeManuscriptSection(draft, sectionId));
  }, [commit, draft]);

  const updateFullContent = useCallback((content: string) => {
    commit({
      ...updateManuscriptContent(draft, content),
      manuscriptEditorMode: "markdown",
    });
  }, [commit, draft]);

  const renameEntity = useCallback((oldLabel: string, nextLabel: string) => {
    commit(renameManuscriptEntity(draft, oldLabel, nextLabel));
  }, [commit, draft]);

  const switchEditorMode = useCallback((mode: "markdown" | "structured") => {
    commit({ ...draft, manuscriptEditorMode: mode });
  }, [commit, draft]);

  const undo = useCallback(() => {
    const previous = past[past.length - 1];
    if (!previous) return;
    setPast((items) => items.slice(0, -1));
    setFuture((items) => [cloneManuscript(draft), ...items].slice(0, 50));
    setDraft(previous);
    persist(previous);
  }, [draft, past, persist]);

  const redo = useCallback(() => {
    const next = future[0];
    if (!next) return;
    setFuture((items) => items.slice(1));
    setPast((items) => [...items, cloneManuscript(draft)].slice(-50));
    setDraft(next);
    persist(next);
  }, [draft, future, persist]);

  const fullContent = useMemo(
    () => draft.manuscriptSections.map((section) => section.text).join("\n\n"),
    [draft.manuscriptSections],
  );
  const previewHtml = useMemo(() => renderMarkdown(fullContent), [fullContent]);
  const editorMode = draft.manuscriptEditorMode || "markdown";

  return {
    draft,
    past,
    future,
    editorMode,
    fullContent,
    previewHtml,
    updateTitle,
    updateSummary,
    updateSection,
    addSection,
    removeSection,
    updateFullContent,
    renameEntity,
    switchEditorMode,
    undo,
    redo,
  };
}
