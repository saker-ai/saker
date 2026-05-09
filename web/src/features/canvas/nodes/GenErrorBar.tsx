import { useState } from "react";
import { AlertCircle, RotateCcw, ChevronDown, ChevronUp, AlertTriangle } from "lucide-react";
import { useT } from "@/features/i18n";

interface GenErrorBarProps {
  error?: string;
  onRetry: () => void;
  /** If true, params have changed since this error was recorded. */
  paramsChanged?: boolean;
}

const PREVIEW_LEN = 100;

export function GenErrorBar({ error, onRetry, paramsChanged }: GenErrorBarProps) {
  const { t } = useT();
  const [expanded, setExpanded] = useState(false);
  if (!error) return null;

  const text = typeof error === "string" ? error : t("canvas.error");
  const isLong = text.length > PREVIEW_LEN;
  const display = expanded || !isLong ? text : text.slice(0, PREVIEW_LEN) + "…";

  return (
    <div className={`gen-error ${expanded ? "expanded" : ""}`}>
      <AlertCircle size={12} />
      <span className="gen-error-text" title={isLong && !expanded ? text : undefined}>
        {display}
      </span>
      {paramsChanged && (
        <span className="gen-error-changed" title={t("canvas.paramsChangedHint" as any)}>
          <AlertTriangle size={10} />
          {t("canvas.paramsChanged" as any)}
        </span>
      )}
      {isLong && (
        <button
          className="gen-error-toggle"
          onClick={() => setExpanded((v) => !v)}
          title={expanded ? t("canvas.collapse" as any) : t("canvas.expand" as any)}
        >
          {expanded ? <ChevronUp size={12} /> : <ChevronDown size={12} />}
        </button>
      )}
      <button className="gen-retry-btn" onClick={onRetry} title={t("canvas.retry")}>
        <RotateCcw size={12} />
      </button>
    </div>
  );
}
