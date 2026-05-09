import { useEffect, useRef, useState } from "react";
import { History, X } from "lucide-react";
import { useHistoryStore } from "../panels/historyStore";
import { useT } from "@/features/i18n";

interface PromptHistoryButtonProps {
  mediaType: "image" | "video" | "audio" | "text";
  disabled?: boolean;
  onSelect: (prompt: string) => void;
}

/** Small dropdown surfacing recent prompts for this mediaType from the shared history store. */
export function PromptHistoryButton({ mediaType, disabled, onSelect }: PromptHistoryButtonProps) {
  const { t } = useT();
  const entries = useHistoryStore((s) => s.entries);
  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement | null>(null);

  const recent = (() => {
    const seen = new Set<string>();
    const out: { id: string; prompt: string }[] = [];
    for (const e of entries) {
      if (e.type !== mediaType) continue;
      const p = (e.prompt || "").trim();
      if (!p || seen.has(p)) continue;
      seen.add(p);
      out.push({ id: e.id, prompt: p });
      if (out.length >= 10) break;
    }
    return out;
  })();

  useEffect(() => {
    if (!open) return;
    const handler = (e: MouseEvent) => {
      if (rootRef.current && !rootRef.current.contains(e.target as Node)) setOpen(false);
    };
    const key = (e: KeyboardEvent) => { if (e.key === "Escape") setOpen(false); };
    window.addEventListener("mousedown", handler);
    window.addEventListener("keydown", key);
    return () => {
      window.removeEventListener("mousedown", handler);
      window.removeEventListener("keydown", key);
    };
  }, [open]);

  if (recent.length === 0) return null;

  return (
    <div className="prompt-history-wrapper nodrag" ref={rootRef}>
      <button
        type="button"
        className="prompt-history-btn"
        title={t("canvas.recentPrompts" as any)}
        disabled={disabled}
        onClick={(e) => { e.stopPropagation(); setOpen((v) => !v); }}
      >
        <History size={12} />
      </button>
      {open && (
        <div className="prompt-history-menu nowheel">
          <div className="prompt-history-menu-header">
            <span>{t("canvas.recentPrompts" as any)}</span>
            <button
              type="button"
              className="prompt-history-close"
              onClick={() => setOpen(false)}
            >
              <X size={10} />
            </button>
          </div>
          {recent.map((r) => (
            <button
              key={r.id}
              type="button"
              className="prompt-history-item"
              title={r.prompt}
              onClick={() => { onSelect(r.prompt); setOpen(false); }}
            >
              <span>{r.prompt}</span>
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
