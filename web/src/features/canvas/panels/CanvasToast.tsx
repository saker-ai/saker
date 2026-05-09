import { useEffect, useState, useCallback } from "react";
import { CheckCircle2, AlertCircle, X } from "lucide-react";

interface Toast {
  id: number;
  type: "success" | "error";
  message: string;
}

let toastId = 0;

export function CanvasToast() {
  const [toasts, setToasts] = useState<Toast[]>([]);

  const dismiss = useCallback((id: number) => {
    setToasts((prev) => prev.filter((t) => t.id !== id));
  }, []);

  useEffect(() => {
    const handler = (e: Event) => {
      const { type, message } = (e as CustomEvent).detail as {
        type: "success" | "error";
        message: string;
      };
      const id = ++toastId;
      setToasts((prev) => [...prev.slice(-4), { id, type, message }]);
      setTimeout(() => dismiss(id), 4000);
    };
    window.addEventListener("canvas-toast", handler);
    return () => window.removeEventListener("canvas-toast", handler);
  }, [dismiss]);

  if (toasts.length === 0) return null;

  return (
    <div className="canvas-toast-container">
      {toasts.map((t) => (
        <div key={t.id} className={`canvas-toast canvas-toast-${t.type}`}>
          {t.type === "success" ? (
            <CheckCircle2 size={14} />
          ) : (
            <AlertCircle size={14} />
          )}
          <span>{t.message}</span>
          <button className="canvas-toast-close" onClick={() => dismiss(t.id)}>
            <X size={12} />
          </button>
        </div>
      ))}
    </div>
  );
}

export function showCanvasToast(type: "success" | "error", message: string) {
  window.dispatchEvent(
    new CustomEvent("canvas-toast", { detail: { type, message } })
  );
}
