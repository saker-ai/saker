import type { CanvasNodeData, ManuscriptEntity, ManuscriptSection } from "./types";

export interface StructuredManuscript {
  manuscriptTitle: string;
  manuscriptSummary?: string;
  manuscriptSections: ManuscriptSection[];
  manuscriptEntities: ManuscriptEntity[];
  manuscriptViewMode: "card" | "outline" | "read" | "fullscreen";
  manuscriptEditorMode: "read" | "edit" | "markdown" | "structured";
}

const ENTITY_RE = /\[(.+?)\]/g;
type ManuscriptEditorMode = StructuredManuscript["manuscriptEditorMode"];

function makeId(prefix: string, index: number) {
  return `${prefix}_${index}`;
}

function splitContentIntoSections(content: string): ManuscriptSection[] {
  const parts = content
    .split(/\n{2,}/)
    .map((part) => part.trim())
    .filter(Boolean);

  const source = parts.length > 0 ? parts : [content.trim()].filter(Boolean);
  if (source.length === 0) {
    return [{ id: "section_0", type: "paragraph", text: "" }];
  }

  return source.map((text, index) => ({
    id: makeId("section", index),
    type: "paragraph",
    text,
  }));
}

export function detectManuscriptEditorMode(_content: string): ManuscriptEditorMode {
  // Currently all manuscripts use markdown mode. When structured mode is
  // implemented, this function should inspect content to pick the right mode.
  return "markdown";
}

export function getManuscriptText(manuscript: Pick<StructuredManuscript, "manuscriptSections">) {
  return manuscript.manuscriptSections.map((section) => section.text).join("\n\n");
}

export function extractManuscriptEntities(manuscript: Pick<StructuredManuscript, "manuscriptSections">): ManuscriptEntity[] {
  const entityByLabel = new Map<string, ManuscriptEntity>();

  for (const section of manuscript.manuscriptSections) {
    ENTITY_RE.lastIndex = 0;
    let match: RegExpExecArray | null;
    while ((match = ENTITY_RE.exec(section.text)) !== null) {
      const label = match[1].trim();
      if (!label) continue;
      const existing = entityByLabel.get(label);
      const range = { sectionId: section.id, start: match.index, end: match.index + match[0].length };
      if (existing) {
        existing.ranges = [...(existing.ranges ?? []), range];
      } else {
        entityByLabel.set(label, {
          id: `entity_${entityByLabel.size}`,
          label,
          ranges: [range],
        });
      }
    }
  }

  return [...entityByLabel.values()];
}

export function createDefaultManuscript(title: string, content: string): StructuredManuscript {
  const manuscript: StructuredManuscript = {
    manuscriptTitle: title || "灵感文稿",
    manuscriptSections: splitContentIntoSections(content),
    manuscriptEntities: [],
    manuscriptViewMode: "card",
    manuscriptEditorMode: detectManuscriptEditorMode(content),
  };
  return {
    ...manuscript,
    manuscriptEntities: extractManuscriptEntities(manuscript),
  };
}

export function normalizeManuscriptData(data: CanvasNodeData): CanvasNodeData {
  const existingSections = data.manuscriptSections?.length ? data.manuscriptSections : null;
  const manuscript = existingSections
    ? {
        manuscriptTitle: data.manuscriptTitle || data.label || "灵感文稿",
        manuscriptSummary: data.manuscriptSummary,
        manuscriptSections: existingSections,
        manuscriptEntities: data.manuscriptEntities?.length
          ? data.manuscriptEntities
          : extractManuscriptEntities({ manuscriptSections: existingSections }),
        manuscriptViewMode: data.manuscriptViewMode || "card",
        manuscriptEditorMode: data.manuscriptEditorMode || detectManuscriptEditorMode(getManuscriptText({ manuscriptSections: existingSections })),
      }
    : createDefaultManuscript(data.manuscriptTitle || data.label || "灵感文稿", data.content || "");

  const content = getManuscriptText(manuscript);
  return {
    ...data,
    ...manuscript,
    label: data.label || manuscript.manuscriptTitle,
    content,
  };
}

export function updateManuscriptContent(manuscript: StructuredManuscript, content: string): StructuredManuscript {
  const updated: StructuredManuscript = {
    ...manuscript,
    manuscriptSections: splitContentIntoSections(content),
  };
  return {
    ...updated,
    manuscriptEntities: extractManuscriptEntities(updated),
  };
}

export function updateManuscriptSection(
  manuscript: StructuredManuscript,
  sectionId: string,
  patch: Partial<ManuscriptSection>,
): StructuredManuscript {
  const updated: StructuredManuscript = {
    ...manuscript,
    manuscriptSections: manuscript.manuscriptSections.map((section) =>
      section.id === sectionId ? { ...section, ...patch } : section
    ),
  };
  return {
    ...updated,
    manuscriptEntities: extractManuscriptEntities(updated),
  };
}

export function addManuscriptSection(manuscript: StructuredManuscript, afterSectionId?: string): StructuredManuscript {
  const nextSection: ManuscriptSection = {
    id: `section_${Date.now()}`,
    type: "paragraph",
    text: "",
  };
  const index = afterSectionId
    ? manuscript.manuscriptSections.findIndex((section) => section.id === afterSectionId)
    : manuscript.manuscriptSections.length - 1;
  const insertAt = index >= 0 ? index + 1 : manuscript.manuscriptSections.length;
  return {
    ...manuscript,
    manuscriptSections: [
      ...manuscript.manuscriptSections.slice(0, insertAt),
      nextSection,
      ...manuscript.manuscriptSections.slice(insertAt),
    ],
  };
}

export function removeManuscriptSection(manuscript: StructuredManuscript, sectionId: string): StructuredManuscript {
  const sections = manuscript.manuscriptSections.filter((section) => section.id !== sectionId);
  const updated: StructuredManuscript = {
    ...manuscript,
    manuscriptSections: sections.length > 0 ? sections : [{ id: "section_0", type: "paragraph", text: "" }],
  };
  return {
    ...updated,
    manuscriptEntities: extractManuscriptEntities(updated),
  };
}

export function renameManuscriptEntity(
  manuscript: StructuredManuscript,
  oldLabel: string,
  newLabel: string,
): StructuredManuscript {
  const trimmed = newLabel.trim();
  if (!trimmed) return manuscript;
  const token = `[${oldLabel}]`;
  const replacement = `[${trimmed}]`;
  const updated: StructuredManuscript = {
    ...manuscript,
    manuscriptSections: manuscript.manuscriptSections.map((section) => ({
      ...section,
      text: section.text.split(token).join(replacement),
    })),
  };
  return {
    ...updated,
    manuscriptEntities: extractManuscriptEntities(updated),
  };
}

export function manuscriptToNodeData(data: CanvasNodeData, manuscript: StructuredManuscript): Partial<CanvasNodeData> {
  const content = getManuscriptText(manuscript);
  return {
    manuscriptTitle: manuscript.manuscriptTitle,
    manuscriptSummary: manuscript.manuscriptSummary,
    manuscriptSections: manuscript.manuscriptSections,
    manuscriptEntities: extractManuscriptEntities(manuscript),
    manuscriptViewMode: manuscript.manuscriptViewMode,
    manuscriptEditorMode: manuscript.manuscriptEditorMode,
    label: manuscript.manuscriptTitle || data.label,
    content,
  };
}
