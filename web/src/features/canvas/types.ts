import type { Node, Edge } from "@xyflow/react";

export type NodeStatus = "pending" | "running" | "done" | "error";

export type MediaType = "image" | "video" | "audio";

export type ManuscriptSectionType = "title" | "heading" | "paragraph" | "list" | "quote" | "note";

export interface ManuscriptEntity {
  id: string;
  label: string;
  type?: string;
  aliases?: string[];
  ranges?: Array<{ sectionId: string; start: number; end: number }>;
}

export interface ManuscriptSection {
  id: string;
  type: ManuscriptSectionType;
  text: string;
  collapsed?: boolean;
}

export type CanvasNodeType =
  | "prompt"
  | "agent"
  | "tool"
  | "skill"
  | "image"
  | "video"
  | "audio"
  | "text"
  | "composition"
  | "imageGen"
  | "voiceGen"
  | "videoGen"
  | "aiTypo"
  | "textGen"
  | "sketch"
  | "group"
  | "llm"
  | "mask"
  | "reference"
  | "export"
  | "table"
  | "appInput"
  | "appOutput";

export type RefType = "style" | "character" | "composition" | "pose";
export type ExportMode = "download" | "library" | "compose-mp4";
export type LLMMode = "refine" | "translate" | "summarize" | "custom";

export type TableColumnType = "text" | "longText" | "number" | "select";

export interface TableColumn {
  id: string;
  name: string;
  type: TableColumnType;
  options?: string[];
  width?: number;
}

export type TableCellValue = string | number | null;
export type TableRow = { id: string } & Record<string, TableCellValue>;

export interface GenHistoryEntry {
  id: string;
  mediaUrl: string;
  mediaPath?: string;
  prompt: string;
  params: Record<string, unknown>;
  createdAt: number;
  status: "done" | "error";
  error?: string;
  resultNodeIds?: string[];
}

export interface CanvasNodeData extends Record<string, unknown> {
  nodeType: CanvasNodeType;
  label: string;
  status: NodeStatus;
  content?: string;
  manuscriptTitle?: string;
  manuscriptSummary?: string;
  manuscriptSections?: ManuscriptSection[];
  manuscriptEntities?: ManuscriptEntity[];
  manuscriptViewMode?: "card" | "outline" | "read" | "fullscreen";
  manuscriptEditorMode?: "read" | "edit" | "markdown" | "structured";
  fullscreen?: boolean;
  toolName?: string;
  toolParams?: Record<string, unknown>;
  mediaType?: MediaType;
  mediaUrl?: string;
  mediaPath?: string;
  sourceUrl?: string;
  startTime?: number;
  endTime?: number;
  prompt?: string;
  generating?: boolean;
  collapsed?: boolean;
  error?: string;
  // Persisted generation settings
  engine?: string;
  size?: string;
  resolution?: string;
  aspectRatio?: string;
  cameraAngle?: string;
  duration?: number;
  negativePrompt?: string;
  voice?: string;
  language?: string;
  instructions?: string;
  genCount?: number;
  genProgress?: string;
  /** Serialized JSON of params used at last failed attempt (for change detection). */
  lastErrorParams?: string;
  taskId?: string;
  editOnCreate?: boolean;
  sketchData?: string;
  sketchBackground?: "white" | "transparent" | "black";
  sketchBgImage?: string;
  /** Turn ID linking this node to a chat turn for bidirectional navigation. */
  turnId?: string;
  /** When true, auto-layout preserves this node's position. Set on user drag. */
  pinned?: boolean;
  /** When true, node cannot be deleted and its body is read-only. Also layout-preserved. */
  locked?: boolean;
  /** Gen node: ordered history of past generations (append-only, capped). */
  generationHistory?: GenHistoryEntry[];
  /** Index into generationHistory currently highlighted; -1 = latest. */
  activeHistoryIndex?: number;
  /** Reference node: what kind of reference this media provides to downstream. */
  refType?: RefType;
  /** Reference node: influence strength 0-1 (default 1). */
  refStrength?: number;
  /** Mask node: upstream image node id this mask applies to. */
  maskFor?: string;
  /** Mask node: serialized stroke data for re-editing. */
  maskData?: string;
  /** LLM node: which compositional mode to run. */
  llmMode?: LLMMode;
  /** LLM node: target language for translate mode. */
  llmTargetLang?: string;
  /** LLM node: custom system prompt for custom mode. */
  llmCustomInstructions?: string;
  /** Export node: output target. */
  exportMode?: ExportMode;
  /** Export node: last-known execution status. */
  exportStatus?: "idle" | "running" | "done" | "error";
  /** Export node: UNIX ms timestamp of the last successful export. */
  exportedAt?: number;
  /** Sketch node: list of pose layers, each mapping keypoint name → [x, y] in 400×300 canvas coords. */
  poses?: Array<Record<string, [number, number]>>;
  /** Table node: column schema (order matters; cell keys reference column.id). */
  tableColumns?: TableColumn[];
  /** Table node: row records keyed by column.id; row.id is reserved. */
  tableRows?: TableRow[];
  /** Table node: optional title shown above the grid. */
  tableTitle?: string;
  /** Table node: when true, skip default-column bootstrap and show a
   *  loading placeholder until an agent's canvas_table_write lands.
   *  Auto-cleared by TableNode once tableColumns has any entry. */
  tablePendingExtract?: boolean;
  /** Table node: epoch ms when extractManuscriptToTable was dispatched.
   *  Persisted so a page reload doesn't reset the 60s "still loading?" timer
   *  back to zero — TableNode computes elapsed from this on mount. */
  tablePendingExtractStartedAt?: number;
  /** appInput: variable name passed to the app runner inputs map. */
  appVariable?: string;
  /** appInput: form field type. */
  appFieldType?: "text" | "paragraph" | "number" | "select" | "file";
  /** appInput: whether the field is required at run time. */
  appRequired?: boolean;
  /** appInput: default value populated when the form opens. */
  appDefault?: string | number;
  /** appInput: option list for select fields. */
  appOptions?: string[];
  /** appInput: numeric min bound when type=number. */
  appMin?: number;
  /** appInput: numeric max bound when type=number. */
  appMax?: number;
  /** appOutput: kind of media this output emits, used for the form's result rendering. */
  appOutputKind?: "image" | "video" | "audio" | "text";
}

/** Edge type constants used throughout the canvas. */
export const EdgeType = {
  FLOW: "flow",
  REFERENCE: "reference",
  CONTEXT: "context",
} as const;
export type EdgeTypeValue = (typeof EdgeType)[keyof typeof EdgeType];

export type CanvasNode = Node<CanvasNodeData>;
export type CanvasEdge = Edge;
