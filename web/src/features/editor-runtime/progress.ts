export type ComposeStatus = "idle" | "running" | "done" | "error";

export interface ComposeProgress {
  status: ComposeStatus;
  /** 0..1 progress reported by Combinator. */
  pct: number;
  message?: string;
}

export type ProgressCallback = (p: number) => void;
