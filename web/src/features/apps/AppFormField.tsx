"use client";

import type { AppInputField } from "./appsApi";
import { resolveHttpBaseUrl } from "@/features/rpc/httpRpc";

interface Props {
  field: AppInputField;
  value: unknown;
  onChange: (value: unknown) => void;
}

// uploadFile sends a file to /api/upload (if it exists) and returns the URL.
// Falls back to a data URL if the endpoint is unavailable.
async function uploadFile(file: File): Promise<string> {
  const base = resolveHttpBaseUrl();
  try {
    const form = new FormData();
    form.append("file", file);
    const res = await fetch(`${base}/api/upload`, {
      method: "POST",
      credentials: "include",
      body: form,
    });
    if (res.ok) {
      const data = await res.json();
      // The upload endpoint returns {url: "..."} or {path: "..."}
      return (data.url ?? data.path ?? "") as string;
    }
  } catch {
    // fall through
  }
  // TODO: replace with proper upload endpoint when /api/upload is available.
  // For now, return a data URL so the field is still usable.
  return URL.createObjectURL(file);
}

export function AppFormField({ field, value, onChange }: Props) {
  const labelEl = (
    <label
      style={{
        display: "block",
        fontSize: "0.8125rem",
        fontWeight: 500,
        marginBottom: 4,
        color: "var(--color-text-secondary, #888)",
      }}
    >
      {field.label}
      {field.required && (
        <span style={{ color: "var(--color-error, #e05)", marginLeft: 4 }}>*</span>
      )}
    </label>
  );

  const inputStyle: React.CSSProperties = {
    width: "100%",
    padding: "6px 10px",
    borderRadius: 6,
    border: "1px solid var(--color-border, #333)",
    background: "var(--color-input-bg, #1a1a1a)",
    color: "var(--color-text, #eee)",
    fontSize: "0.875rem",
    boxSizing: "border-box",
  };

  switch (field.type) {
    case "text":
      return (
        <div style={{ marginBottom: 12 }}>
          {labelEl}
          <input
            type="text"
            style={inputStyle}
            value={typeof value === "string" ? value : (field.default as string) ?? ""}
            onChange={(e) => onChange(e.target.value)}
            placeholder={field.label}
          />
        </div>
      );

    case "paragraph":
      return (
        <div style={{ marginBottom: 12 }}>
          {labelEl}
          <textarea
            style={{ ...inputStyle, minHeight: 80, resize: "vertical" }}
            value={typeof value === "string" ? value : (field.default as string) ?? ""}
            onChange={(e) => onChange(e.target.value)}
            placeholder={field.label}
          />
        </div>
      );

    case "number":
      return (
        <div style={{ marginBottom: 12 }}>
          {labelEl}
          <input
            type="number"
            style={inputStyle}
            value={typeof value === "number" ? value : (field.default as number) ?? ""}
            min={field.min}
            max={field.max}
            onChange={(e) => onChange(e.target.valueAsNumber)}
          />
        </div>
      );

    case "select":
      return (
        <div style={{ marginBottom: 12 }}>
          {labelEl}
          <select
            style={inputStyle}
            value={typeof value === "string" ? value : (field.default as string) ?? ""}
            onChange={(e) => onChange(e.target.value)}
          >
            {!field.required && <option value="">—</option>}
            {(field.options ?? []).map((opt) => (
              <option key={opt} value={opt}>
                {opt}
              </option>
            ))}
          </select>
        </div>
      );

    case "file":
      return (
        <div style={{ marginBottom: 12 }}>
          {labelEl}
          <input
            type="file"
            style={{ ...inputStyle, padding: "4px 6px", cursor: "pointer" }}
            onChange={async (e) => {
              const file = e.target.files?.[0];
              if (!file) return;
              const url = await uploadFile(file);
              onChange(url);
            }}
          />
          {typeof value === "string" && value && (
            <div
              style={{
                marginTop: 4,
                fontSize: "0.75rem",
                color: "var(--color-text-secondary, #888)",
                wordBreak: "break-all",
              }}
            >
              {value}
            </div>
          )}
        </div>
      );

    default:
      return null;
  }
}
