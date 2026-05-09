"use client";

import { useState, useEffect } from "react";
import { useT } from "@/features/i18n";
import { resolveHttpBaseUrl } from "@/features/rpc/httpRpc";
import { useProjectStore } from "@/features/project/projectStore";
import {
  listKeys,
  createKey,
  deleteKey,
  rotateKey,
  type ApiKeySummary,
  type ApiKeyCreated,
  type AppInputField,
} from "./appsApi";
import { curlSnippet, jsSnippet, pythonSnippet } from "./codeSnippets";

interface Props {
  appId: string;
  inputs?: AppInputField[];
}

type SnippetTab = "curl" | "js" | "python";

function formatDate(iso: string): string {
  try {
    return new Date(iso).toLocaleString();
  } catch {
    return iso;
  }
}

function CopyButton({ text, label }: { text: string; label: string }) {
  const { t } = useT();
  const [copied, setCopied] = useState(false);

  const handleCopy = () => {
    navigator.clipboard.writeText(text).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    });
  };

  return (
    <button
      onClick={handleCopy}
      style={{
        padding: "4px 10px",
        borderRadius: 5,
        background: copied ? "var(--color-accent, #7c6af7)" : "none",
        color: copied ? "#fff" : "var(--color-accent, #7c6af7)",
        border: "1px solid var(--color-accent, #7c6af7)",
        cursor: "pointer",
        fontSize: "0.8125rem",
        fontWeight: 500,
        flexShrink: 0,
      }}
    >
      {copied ? t("apps.keysCopied") : label}
    </button>
  );
}

export function AppKeysPanel({ appId, inputs = [] }: Props) {
  const { t } = useT();
  const [keys, setKeys] = useState<ApiKeySummary[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  // Create modal state
  const [showCreate, setShowCreate] = useState(false);
  const [newName, setNewName] = useState("");
  const [newExpiresInDays, setNewExpiresInDays] = useState<string>("");
  const [creating, setCreating] = useState(false);
  const [createError, setCreateError] = useState<string | null>(null);
  const [created, setCreated] = useState<ApiKeyCreated | null>(null);
  // When non-null we're showing the post-rotate modal — re-uses created state
  // for the plaintext display but the title flips to "Rotated".
  const [rotated, setRotated] = useState<ApiKeyCreated | null>(null);

  // Inline delete confirm
  const [confirmDeleteId, setConfirmDeleteId] = useState<string | null>(null);
  const [deleting, setDeleting] = useState<string | null>(null);
  // Inline rotate confirm + in-flight tracker
  const [confirmRotateId, setConfirmRotateId] = useState<string | null>(null);
  const [rotating, setRotating] = useState<string | null>(null);

  // Snippet tab
  const [snippetTab, setSnippetTab] = useState<SnippetTab>("curl");

  // Session-only plaintext key for snippets (cleared on remount)
  const [sessionKey, setSessionKey] = useState<string | undefined>(undefined);

  const load = () => {
    setLoading(true);
    setError(null);
    listKeys(appId)
      .then(setKeys)
      .catch((err) => setError(err instanceof Error ? err.message : String(err)))
      .finally(() => setLoading(false));
  };

  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [appId]);

  const handleCreate = async () => {
    if (!newName.trim()) return;
    setCreating(true);
    setCreateError(null);
    try {
      const days = newExpiresInDays.trim()
        ? Math.max(0, Math.floor(Number(newExpiresInDays)))
        : undefined;
      const result = await createKey(
        appId,
        newName.trim(),
        days && days > 0 ? { expiresInDays: days } : undefined,
      );
      setCreated(result);
      setSessionKey(result.apiKey);
    } catch (err) {
      setCreateError(err instanceof Error ? err.message : String(err));
    } finally {
      setCreating(false);
    }
  };

  const handleDone = () => {
    setShowCreate(false);
    setNewName("");
    setNewExpiresInDays("");
    setCreated(null);
    setRotated(null);
    setCreateError(null);
    load();
  };

  const handleDeleteClick = (keyId: string) => {
    if (confirmDeleteId !== keyId) {
      setConfirmDeleteId(keyId);
      return;
    }
    setDeleting(keyId);
    deleteKey(appId, keyId)
      .then(() => {
        setKeys((prev) => prev.filter((k) => k.id !== keyId));
        setConfirmDeleteId(null);
      })
      .catch((err) =>
        setError(err instanceof Error ? err.message : String(err)),
      )
      .finally(() => setDeleting(null));
  };

  const handleRotateClick = (keyId: string) => {
    if (confirmRotateId !== keyId) {
      setConfirmRotateId(keyId);
      return;
    }
    setRotating(keyId);
    rotateKey(appId, keyId)
      .then((result) => {
        setRotated(result);
        setSessionKey(result.apiKey);
        setShowCreate(true); // reuse the modal shell to show the new plaintext
        setConfirmRotateId(null);
      })
      .catch((err) =>
        setError(err instanceof Error ? err.message : String(err)),
      )
      .finally(() => setRotating(null));
  };

  const snippetCtx = {
    baseUrl: resolveHttpBaseUrl() || window.location.origin,
    appId,
    projectId: useProjectStore.getState().currentProjectId ?? undefined,
    apiKey: sessionKey,
    apiKeyPlaceholder: t("apps.codeSnippetApiKeyHint"),
    inputs,
  };

  const currentSnippet =
    snippetTab === "curl"
      ? curlSnippet(snippetCtx)
      : snippetTab === "js"
        ? jsSnippet(snippetCtx)
        : pythonSnippet(snippetCtx);

  const sectionTitleStyle: React.CSSProperties = {
    fontSize: "0.8125rem",
    fontWeight: 600,
    color: "var(--color-text-secondary, #888)",
    textTransform: "uppercase",
    letterSpacing: "0.05em",
    marginBottom: 10,
  };

  const snippetTabBtnStyle = (active: boolean): React.CSSProperties => ({
    padding: "5px 12px",
    border: "none",
    background: active ? "var(--color-accent, #7c6af7)" : "none",
    color: active ? "#fff" : "var(--color-text-secondary, #888)",
    borderRadius: 4,
    cursor: "pointer",
    fontSize: "0.8125rem",
    fontWeight: active ? 600 : 400,
  });

  const errorBoxStyle: React.CSSProperties = {
    color: "var(--color-error, #e05)",
    fontSize: "0.8125rem",
    marginBottom: 12,
    padding: "8px 12px",
    background: "rgba(220,0,80,0.08)",
    borderRadius: 6,
    border: "1px solid rgba(220,0,80,0.2)",
  };

  return (
    <div>
      {/* Header */}
      <div
        style={{
          display: "flex",
          justifyContent: "space-between",
          alignItems: "center",
          marginBottom: 20,
        }}
      >
        <div
          style={{
            fontWeight: 600,
            fontSize: "1rem",
            color: "var(--color-text, #eee)",
          }}
        >
          {t("apps.keysTitle")}
        </div>
        <button
          onClick={() => {
            setShowCreate(true);
            setCreated(null);
            setNewName("");
            setCreateError(null);
          }}
          style={{
            padding: "6px 14px",
            borderRadius: 6,
            background: "var(--color-accent, #7c6af7)",
            color: "#fff",
            border: "none",
            cursor: "pointer",
            fontSize: "0.875rem",
            fontWeight: 500,
          }}
        >
          {t("apps.keysCreate")}
        </button>
      </div>

      {/* Error */}
      {error && <div style={errorBoxStyle}>{error}</div>}

      {/* Create modal */}
      {showCreate && (
        <div
          style={{
            position: "fixed",
            inset: 0,
            background: "rgba(0,0,0,0.6)",
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            zIndex: 1000,
          }}
        >
          <div
            style={{
              background: "var(--color-surface, #111)",
              border: "1px solid var(--color-border, #333)",
              borderRadius: 10,
              padding: 24,
              width: 440,
              maxWidth: "90vw",
            }}
          >
            {created || rotated ? (
              /* Success state: show plaintext key (works for both create and rotate). */
              <>
                <div
                  style={{
                    fontWeight: 600,
                    fontSize: "1rem",
                    marginBottom: 16,
                    color: "var(--color-text, #eee)",
                  }}
                >
                  {rotated ? t("apps.keysRotate") : t("apps.keysCreate")}
                </div>
                <div
                  style={{
                    padding: "10px 12px",
                    background: "rgba(220,0,80,0.08)",
                    border: "1px solid rgba(220,0,80,0.25)",
                    borderRadius: 6,
                    fontSize: "0.8125rem",
                    color: "var(--color-error, #e05)",
                    marginBottom: 12,
                    fontWeight: 500,
                  }}
                >
                  {rotated ? t("apps.keysRotated") : t("apps.keysSaveWarning")}
                </div>
                <div style={{ marginBottom: 6, fontSize: "0.8125rem", color: "var(--color-text-secondary, #888)" }}>
                  {t("apps.keysName")}:{" "}
                  <strong style={{ color: "var(--color-text, #eee)" }}>
                    {(rotated ?? created)?.name}
                  </strong>
                </div>
                <div
                  style={{
                    display: "flex",
                    alignItems: "center",
                    gap: 8,
                    marginBottom: 20,
                    background: "var(--color-bg, #0a0a0a)",
                    border: "1px solid var(--color-border, #333)",
                    borderRadius: 6,
                    padding: "8px 10px",
                  }}
                >
                  <code
                    style={{
                      flex: 1,
                      fontFamily: "monospace",
                      fontSize: "0.8125rem",
                      color: "var(--color-text, #eee)",
                      wordBreak: "break-all",
                    }}
                  >
                    {(rotated ?? created)?.apiKey}
                  </code>
                  <CopyButton
                    text={(rotated ?? created)?.apiKey ?? ""}
                    label={t("apps.keysCopy")}
                  />
                </div>
                <button
                  onClick={handleDone}
                  style={{
                    padding: "7px 18px",
                    borderRadius: 6,
                    background: "var(--color-accent, #7c6af7)",
                    color: "#fff",
                    border: "none",
                    cursor: "pointer",
                    fontSize: "0.875rem",
                    fontWeight: 500,
                  }}
                >
                  Done
                </button>
              </>
            ) : (
              /* Create form */
              <>
                <div
                  style={{
                    fontWeight: 600,
                    fontSize: "1rem",
                    marginBottom: 16,
                    color: "var(--color-text, #eee)",
                  }}
                >
                  {t("apps.keysCreate")}
                </div>
                <div style={{ marginBottom: 14 }}>
                  <label
                    style={{
                      display: "block",
                      fontSize: "0.8125rem",
                      fontWeight: 500,
                      color: "var(--color-text-secondary, #888)",
                      marginBottom: 6,
                    }}
                  >
                    {t("apps.keysName")}
                  </label>
                  <input
                    type="text"
                    value={newName}
                    onChange={(e) => setNewName(e.target.value)}
                    onKeyDown={(e) => {
                      if (e.key === "Enter") handleCreate();
                    }}
                    placeholder={t("apps.keysName")}
                    style={{
                      width: "100%",
                      padding: "8px 10px",
                      borderRadius: 6,
                      border: "1px solid var(--color-border, #333)",
                      background: "var(--color-bg, #0a0a0a)",
                      color: "var(--color-text, #eee)",
                      fontSize: "0.875rem",
                      boxSizing: "border-box",
                    }}
                  />
                </div>
                <div style={{ marginBottom: 14 }}>
                  <label
                    style={{
                      display: "block",
                      fontSize: "0.8125rem",
                      fontWeight: 500,
                      color: "var(--color-text-secondary, #888)",
                      marginBottom: 6,
                    }}
                  >
                    {t("apps.keysExpiresInDays")}
                  </label>
                  <input
                    type="number"
                    min={0}
                    value={newExpiresInDays}
                    onChange={(e) => setNewExpiresInDays(e.target.value)}
                    placeholder={t("apps.keysExpiresNever")}
                    style={{
                      width: "100%",
                      padding: "8px 10px",
                      borderRadius: 6,
                      border: "1px solid var(--color-border, #333)",
                      background: "var(--color-bg, #0a0a0a)",
                      color: "var(--color-text, #eee)",
                      fontSize: "0.875rem",
                      boxSizing: "border-box",
                    }}
                  />
                </div>
                {createError && <div style={errorBoxStyle}>{createError}</div>}
                <div style={{ display: "flex", gap: 8 }}>
                  <button
                    onClick={handleCreate}
                    disabled={creating || !newName.trim()}
                    style={{
                      padding: "7px 18px",
                      borderRadius: 6,
                      background: "var(--color-accent, #7c6af7)",
                      color: "#fff",
                      border: "none",
                      cursor:
                        creating || !newName.trim() ? "not-allowed" : "pointer",
                      opacity: creating || !newName.trim() ? 0.6 : 1,
                      fontSize: "0.875rem",
                      fontWeight: 500,
                    }}
                  >
                    {creating ? "..." : t("apps.keysCreate")}
                  </button>
                  <button
                    onClick={() => {
                      setShowCreate(false);
                      setNewName("");
                      setCreateError(null);
                    }}
                    style={{
                      padding: "7px 14px",
                      borderRadius: 6,
                      background: "none",
                      color: "var(--color-text-secondary, #888)",
                      border: "1px solid var(--color-border, #333)",
                      cursor: "pointer",
                      fontSize: "0.875rem",
                    }}
                  >
                    Cancel
                  </button>
                </div>
              </>
            )}
          </div>
        </div>
      )}

      {/* Keys list */}
      {loading ? (
        <div
          style={{
            color: "var(--color-text-secondary, #888)",
            fontSize: "0.875rem",
          }}
        >
          Loading...
        </div>
      ) : keys.length === 0 ? (
        <div
          style={{
            color: "var(--color-text-secondary, #888)",
            fontSize: "0.875rem",
            marginBottom: 24,
          }}
        >
          {t("apps.keysEmpty")}
        </div>
      ) : (
        <div
          style={{
            border: "1px solid var(--color-border, #333)",
            borderRadius: 8,
            overflow: "hidden",
            marginBottom: 28,
          }}
        >
          {keys.map((key, i) => (
            <div
              key={key.id}
              style={{
                padding: "10px 14px",
                display: "flex",
                justifyContent: "space-between",
                alignItems: "center",
                borderBottom:
                  i < keys.length - 1
                    ? "1px solid var(--color-border, #333)"
                    : "none",
                background: "var(--color-surface, #111)",
                gap: 12,
              }}
            >
              <div style={{ flex: 1, minWidth: 0 }}>
                <div
                  style={{
                    fontWeight: 500,
                    fontSize: "0.875rem",
                    color: "var(--color-text, #eee)",
                  }}
                >
                  {key.name}
                </div>
                <div
                  style={{
                    fontSize: "0.75rem",
                    color: "var(--color-text-secondary, #888)",
                    fontFamily: "monospace",
                    marginTop: 2,
                  }}
                >
                  {key.prefix}
                </div>
              </div>
              <div
                style={{
                  fontSize: "0.75rem",
                  color: "var(--color-text-secondary, #888)",
                  flexShrink: 0,
                  textAlign: "right",
                }}
              >
                <div>{formatDate(key.createdAt)}</div>
                <div>
                  {t("apps.keysLastUsed")}:{" "}
                  {key.lastUsedAt
                    ? formatDate(key.lastUsedAt)
                    : t("apps.keysNeverUsed")}
                </div>
                <div>
                  {t("apps.keysExpiresAt")}:{" "}
                  {key.expiresAt
                    ? formatDate(key.expiresAt)
                    : t("apps.keysExpiresNever")}
                </div>
              </div>
              <div
                style={{
                  display: "flex",
                  gap: 6,
                  alignItems: "center",
                  flexShrink: 0,
                }}
              >
                {confirmDeleteId === key.id ? (
                  <>
                    <span
                      style={{
                        fontSize: "0.75rem",
                        color: "var(--color-error, #e05)",
                      }}
                    >
                      {t("apps.confirmDeleteKey")}
                    </span>
                    <button
                      onClick={() => handleDeleteClick(key.id)}
                      disabled={deleting === key.id}
                      style={{
                        padding: "3px 10px",
                        borderRadius: 4,
                        background: "var(--color-error, #e05)",
                        color: "#fff",
                        border: "none",
                        cursor: "pointer",
                        fontSize: "0.75rem",
                      }}
                    >
                      {deleting === key.id ? "..." : "Delete"}
                    </button>
                    <button
                      onClick={() => setConfirmDeleteId(null)}
                      style={{
                        padding: "3px 8px",
                        borderRadius: 4,
                        background: "none",
                        color: "var(--color-text-secondary, #888)",
                        border: "1px solid var(--color-border, #333)",
                        cursor: "pointer",
                        fontSize: "0.75rem",
                      }}
                    >
                      Cancel
                    </button>
                  </>
                ) : confirmRotateId === key.id ? (
                  <>
                    <span
                      style={{
                        fontSize: "0.75rem",
                        color: "var(--color-accent, #7c6af7)",
                      }}
                    >
                      {t("apps.keysConfirmRotate")}
                    </span>
                    <button
                      onClick={() => handleRotateClick(key.id)}
                      disabled={rotating === key.id}
                      style={{
                        padding: "3px 10px",
                        borderRadius: 4,
                        background: "var(--color-accent, #7c6af7)",
                        color: "#fff",
                        border: "none",
                        cursor: "pointer",
                        fontSize: "0.75rem",
                      }}
                    >
                      {rotating === key.id ? "..." : t("apps.keysRotate")}
                    </button>
                    <button
                      onClick={() => setConfirmRotateId(null)}
                      style={{
                        padding: "3px 8px",
                        borderRadius: 4,
                        background: "none",
                        color: "var(--color-text-secondary, #888)",
                        border: "1px solid var(--color-border, #333)",
                        cursor: "pointer",
                        fontSize: "0.75rem",
                      }}
                    >
                      Cancel
                    </button>
                  </>
                ) : (
                  <>
                    <button
                      onClick={() => handleRotateClick(key.id)}
                      style={{
                        padding: "3px 10px",
                        borderRadius: 4,
                        background: "none",
                        color: "var(--color-accent, #7c6af7)",
                        border: "1px solid var(--color-accent, #7c6af7)",
                        cursor: "pointer",
                        fontSize: "0.75rem",
                      }}
                    >
                      {t("apps.keysRotate")}
                    </button>
                    <button
                      onClick={() => handleDeleteClick(key.id)}
                      style={{
                        padding: "3px 10px",
                        borderRadius: 4,
                        background: "none",
                        color: "var(--color-text-secondary, #888)",
                        border: "1px solid var(--color-border, #333)",
                        cursor: "pointer",
                        fontSize: "0.75rem",
                      }}
                    >
                      Delete
                    </button>
                  </>
                )}
              </div>
            </div>
          ))}
        </div>
      )}

      {/* Use your key section */}
      <div>
        <div style={sectionTitleStyle}>{t("apps.keysUseTitle")}</div>
        {!sessionKey && (
          <div
            style={{
              fontSize: "0.8125rem",
              color: "var(--color-text-secondary, #888)",
              marginBottom: 10,
            }}
          >
            Create a key above to fill in the snippets automatically.
          </div>
        )}
        {/* Snippet tab bar */}
        <div
          style={{
            display: "flex",
            gap: 4,
            marginBottom: 8,
            background: "var(--color-bg, #0a0a0a)",
            borderRadius: 6,
            padding: 3,
            width: "fit-content",
          }}
        >
          <button
            style={snippetTabBtnStyle(snippetTab === "curl")}
            onClick={() => setSnippetTab("curl")}
          >
            {t("apps.keysSnippetCurl")}
          </button>
          <button
            style={snippetTabBtnStyle(snippetTab === "js")}
            onClick={() => setSnippetTab("js")}
          >
            {t("apps.keysSnippetJs")}
          </button>
          <button
            style={snippetTabBtnStyle(snippetTab === "python")}
            onClick={() => setSnippetTab("python")}
          >
            {t("apps.keysSnippetPython")}
          </button>
        </div>
        <div style={{ position: "relative" }}>
          <pre
            style={{
              background: "var(--color-bg, #0a0a0a)",
              border: "1px solid var(--color-border, #333)",
              borderRadius: 6,
              padding: "12px 14px",
              paddingRight: 80,
              fontSize: "0.8125rem",
              fontFamily: "monospace",
              color: "var(--color-text, #eee)",
              margin: 0,
              overflow: "auto",
              whiteSpace: "pre",
            }}
          >
            {currentSnippet}
          </pre>
          <div style={{ position: "absolute", top: 8, right: 8 }}>
            <CopyButton text={currentSnippet} label={t("apps.keysCopy")} />
          </div>
        </div>
      </div>
    </div>
  );
}
