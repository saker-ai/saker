import { useState, useRef, useCallback, useEffect, type ReactNode, type InputHTMLAttributes } from "react";
import { Eye, EyeOff, CheckCircle, AlertCircle } from "lucide-react";
import { useT } from "@/features/i18n";

// --- Shared layout components ---

export function Section({ title, id, children }: { title: string; id?: string; children?: ReactNode }) {
  return (
    <div className="settings-card" id={id} data-section={id}>
      <div className="settings-card-title"><span>{title}</span></div>
      {children}
    </div>
  );
}

export function Row({ label, value }: { label: string; value: string }) {
  return (
    <div className="settings-row">
      <span className="settings-label">{label}</span>
      <span className="settings-value">{value}</span>
    </div>
  );
}

// --- Utility functions ---

const CATEGORY_ORDER = ["builtin", "aigo"];

export function groupTools(
  tools: { name: string; description: string; category: string }[],
  categoryLabel: (cat: string) => string,
): [string, { name: string; description: string; category: string }[]][] {
  const groups = new Map<string, typeof tools>();
  for (const tool of tools) {
    const key = tool.category || "builtin";
    if (!groups.has(key)) groups.set(key, []);
    groups.get(key)!.push(tool);
  }
  const sorted = [...groups.entries()].sort(([a], [b]) => {
    const ai = CATEGORY_ORDER.indexOf(a);
    const bi = CATEGORY_ORDER.indexOf(b);
    const aOrder = ai >= 0 ? ai : a.startsWith("mcp") ? 100 : 50;
    const bOrder = bi >= 0 ? bi : b.startsWith("mcp") ? 100 : 50;
    return aOrder - bOrder || a.localeCompare(b);
  });
  return sorted.map(([key, items]) => [categoryLabel(key), items]);
}

export function truncateDesc(desc: string): string {
  if (!desc) return "";
  const dot = desc.indexOf(". ");
  const newline = desc.indexOf("\n");
  let end = desc.length;
  if (dot > 0 && dot < end) end = dot + 1;
  if (newline > 0 && newline < end) end = newline;
  const result = desc.slice(0, Math.min(end, 120));
  return result.length < desc.length ? result + (result.endsWith(".") ? "" : "...") : result;
}

export function maskKey(key: string): string {
  if (key.startsWith("${") && key.endsWith("}")) return key;
  if (key.length <= 8) return "****";
  return key.slice(0, 4) + "****" + key.slice(-4);
}

// --- Toast ---

interface ToastMsg {
  text: string;
  type: "success" | "error";
}

export function useToast() {
  const [msg, setMsg] = useState<ToastMsg | null>(null);
  const timerRef = useRef<ReturnType<typeof setTimeout>>(undefined);

  const showToast = useCallback((text: string, type: "success" | "error" = "success") => {
    clearTimeout(timerRef.current);
    setMsg({ text, type });
    timerRef.current = setTimeout(() => setMsg(null), 3000);
  }, []);

  return { toast: msg, showToast };
}

export function Toast({ msg }: { msg: ToastMsg | null }) {
  if (!msg) return null;
  return (
    <div className={`settings-toast settings-toast-${msg.type}`} role="status" aria-live="polite">
      {msg.type === "success" ? <CheckCircle size={14} /> : <AlertCircle size={14} />}
      {msg.text}
    </div>
  );
}

// --- PasswordInput ---

interface PasswordInputProps extends Omit<InputHTMLAttributes<HTMLInputElement>, "type"> {
  value: string;
  onChange: (e: React.ChangeEvent<HTMLInputElement>) => void;
}

export function PasswordInput({ value, onChange, ...props }: PasswordInputProps) {
  const [visible, setVisible] = useState(false);
  const { t } = useT();

  return (
    <div className="settings-password-wrap">
      <input
        className="settings-input"
        type={visible ? "text" : "password"}
        value={value}
        onChange={onChange}
        {...props}
      />
      <button
        type="button"
        className="settings-password-toggle"
        onClick={() => setVisible(!visible)}
        aria-label={visible ? t("settings.hidePassword") : t("settings.showPassword")}
      >
        {visible ? <EyeOff size={16} /> : <Eye size={16} />}
      </button>
    </div>
  );
}

// --- ConfirmDialog ---

interface ConfirmDialogProps {
  open: boolean;
  title: string;
  message: string;
  confirmLabel?: string;
  cancelLabel?: string;
  danger?: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}

export function ConfirmDialog({ open, title, message, confirmLabel, cancelLabel, danger, onConfirm, onCancel }: ConfirmDialogProps) {
  const { t } = useT();
  const confirmRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    if (open) confirmRef.current?.focus();
  }, [open]);

  useEffect(() => {
    if (!open) return;
    const handler = (e: KeyboardEvent) => {
      if (e.key === "Escape") onCancel();
    };
    document.addEventListener("keydown", handler);
    return () => document.removeEventListener("keydown", handler);
  }, [open, onCancel]);

  if (!open) return null;

  return (
    <div className="confirm-overlay" onClick={onCancel} role="presentation">
      <div
        className="confirm-dialog"
        role="alertdialog"
        aria-modal="true"
        aria-labelledby="confirm-title"
        aria-describedby="confirm-message"
        onClick={(e) => e.stopPropagation()}
      >
        <p id="confirm-title" className="confirm-title">{title}</p>
        <p id="confirm-message" className="confirm-message">{message}</p>
        <div className="confirm-actions">
          <button className="persona-btn" onClick={onCancel} type="button">
            {cancelLabel || t("channels.cancel")}
          </button>
          <button
            ref={confirmRef}
            className={`persona-btn ${danger ? "persona-btn-danger" : "persona-btn-primary"}`}
            onClick={onConfirm}
            type="button"
          >
            {confirmLabel || t("channels.confirm")}
          </button>
        </div>
      </div>
    </div>
  );
}

// --- Collapsible Subsection ---

export function CollapsibleSub({
  title,
  icon,
  defaultOpen = true,
  children,
}: {
  title: string;
  icon?: ReactNode;
  defaultOpen?: boolean;
  children: ReactNode;
}) {
  const [open, setOpen] = useState(defaultOpen);

  return (
    <div className="settings-subsection">
      <button
        type="button"
        className="settings-subtitle-icon settings-collapsible-toggle"
        onClick={() => setOpen(!open)}
        aria-expanded={open}
      >
        {icon} {title}
        <span className={`settings-chevron ${open ? "open" : ""}`}>&#9662;</span>
      </button>
      {open && children}
    </div>
  );
}
