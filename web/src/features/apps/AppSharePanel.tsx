"use client";

import { useState, useEffect } from "react";
import { useT } from "@/features/i18n";
import {
  listShareTokens,
  createShareToken,
  deleteShareToken,
  type ShareTokenSummary,
  type ShareTokenCreated,
} from "./appsApi";

interface Props {
  appId: string;
}

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

export function AppSharePanel({ appId }: Props) {
  const { t } = useT();
  const [tokens, setTokens] = useState<ShareTokenSummary[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  // Create modal state
  const [showCreate, setShowCreate] = useState(false);
  const [expiresInDays, setExpiresInDays] = useState("");
  const [rateLimit, setRateLimit] = useState("");
  const [creating, setCreating] = useState(false);
  const [createError, setCreateError] = useState<string | null>(null);
  const [created, setCreated] = useState<ShareTokenCreated | null>(null);

  // Inline delete confirm
  const [confirmDeletePreview, setConfirmDeletePreview] = useState<string | null>(null);
  const [deleting, setDeleting] = useState<string | null>(null);

  const load = () => {
    setLoading(true);
    setError(null);
    listShareTokens(appId)
      .then(setTokens)
      .catch((err) => setError(err instanceof Error ? err.message : String(err)))
      .finally(() => setLoading(false));
  };

  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [appId]);

  const handleCreate = async () => {
    setCreating(true);
    setCreateError(null);
    try {
      const days = parseInt(expiresInDays, 10);
      const rate = parseInt(rateLimit, 10);
      const opts: { expiresInDays?: number; rateLimit?: number } = {};
      if (!isNaN(days) && days > 0) opts.expiresInDays = days;
      if (!isNaN(rate) && rate > 0) opts.rateLimit = rate;
      const result = await createShareToken(appId, opts);
      setCreated(result);
    } catch (err) {
      setCreateError(err instanceof Error ? err.message : String(err));
    } finally {
      setCreating(false);
    }
  };

  const handleDone = () => {
    setShowCreate(false);
    setExpiresInDays("");
    setRateLimit("");
    setCreated(null);
    setCreateError(null);
    load();
  };

  const handleDeleteClick = (tokenPreview: string) => {
    if (confirmDeletePreview !== tokenPreview) {
      setConfirmDeletePreview(tokenPreview);
      return;
    }
    setDeleting(tokenPreview);
    deleteShareToken(appId, tokenPreview)
      .then(() => {
        setTokens((prev) => prev.filter((tk) => tk.tokenPreview !== tokenPreview));
        setConfirmDeletePreview(null);
      })
      .catch((err) =>
        setError(err instanceof Error ? err.message : String(err)),
      )
      .finally(() => setDeleting(null));
  };

  const errorBoxStyle: React.CSSProperties = {
    color: "var(--color-error, #e05)",
    fontSize: "0.8125rem",
    marginBottom: 12,
    padding: "8px 12px",
    background: "rgba(220,0,80,0.08)",
    borderRadius: 6,
    border: "1px solid rgba(220,0,80,0.2)",
  };

  const inputStyle: React.CSSProperties = {
    width: "100%",
    padding: "8px 10px",
    borderRadius: 6,
    border: "1px solid var(--color-border, #333)",
    background: "var(--color-bg, #0a0a0a)",
    color: "var(--color-text, #eee)",
    fontSize: "0.875rem",
    boxSizing: "border-box",
  };

  const labelStyle: React.CSSProperties = {
    display: "block",
    fontSize: "0.8125rem",
    fontWeight: 500,
    color: "var(--color-text-secondary, #888)",
    marginBottom: 6,
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
          {t("apps.shareTitle")}
        </div>
        <button
          onClick={() => {
            setShowCreate(true);
            setCreated(null);
            setExpiresInDays("");
            setRateLimit("");
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
          {t("apps.shareCreate")}
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
            {created ? (
              /* Success state: show the full share URL */
              <>
                <div
                  style={{
                    fontWeight: 600,
                    fontSize: "1rem",
                    marginBottom: 16,
                    color: "var(--color-text, #eee)",
                  }}
                >
                  {t("apps.shareCreate")}
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
                  {t("apps.shareSaveWarning")}
                </div>
                <div
                  style={{
                    fontSize: "0.8125rem",
                    color: "var(--color-text-secondary, #888)",
                    marginBottom: 6,
                  }}
                >
                  {t("apps.shareUrl")}
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
                    {`${window.location.origin}/share/${created.token}`}
                  </code>
                  <CopyButton
                    text={`${window.location.origin}/share/${created.token}`}
                    label={t("apps.keysCopy")}
                  />
                </div>
                {created.expiresAt && (
                  <div
                    style={{
                      fontSize: "0.8125rem",
                      color: "var(--color-text-secondary, #888)",
                      marginBottom: 8,
                    }}
                  >
                    {t("apps.shareExpiresIn")}: {formatDate(created.expiresAt)}
                  </div>
                )}
                {created.rateLimit && (
                  <div
                    style={{
                      fontSize: "0.8125rem",
                      color: "var(--color-text-secondary, #888)",
                      marginBottom: 12,
                    }}
                  >
                    {t("apps.shareRateLimit")}: {created.rateLimit}
                  </div>
                )}
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
                  {t("apps.shareCreate")}
                </div>
                <div style={{ marginBottom: 14 }}>
                  <label style={labelStyle}>{t("apps.shareExpiresIn")}</label>
                  <input
                    type="number"
                    min={0}
                    value={expiresInDays}
                    onChange={(e) => setExpiresInDays(e.target.value)}
                    placeholder="0 = never"
                    style={inputStyle}
                  />
                </div>
                <div style={{ marginBottom: 14 }}>
                  <label style={labelStyle}>{t("apps.shareRateLimit")}</label>
                  <input
                    type="number"
                    min={0}
                    value={rateLimit}
                    onChange={(e) => setRateLimit(e.target.value)}
                    placeholder="0 = unlimited"
                    style={inputStyle}
                  />
                </div>
                {createError && (
                  <div style={errorBoxStyle}>{createError}</div>
                )}
                <div style={{ display: "flex", gap: 8 }}>
                  <button
                    onClick={handleCreate}
                    disabled={creating}
                    style={{
                      padding: "7px 18px",
                      borderRadius: 6,
                      background: "var(--color-accent, #7c6af7)",
                      color: "#fff",
                      border: "none",
                      cursor: creating ? "not-allowed" : "pointer",
                      opacity: creating ? 0.6 : 1,
                      fontSize: "0.875rem",
                      fontWeight: 500,
                    }}
                  >
                    {creating ? "..." : t("apps.shareCreate")}
                  </button>
                  <button
                    onClick={() => {
                      setShowCreate(false);
                      setExpiresInDays("");
                      setRateLimit("");
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

      {/* Tokens list */}
      {loading ? (
        <div
          style={{
            color: "var(--color-text-secondary, #888)",
            fontSize: "0.875rem",
          }}
        >
          Loading...
        </div>
      ) : tokens.length === 0 ? (
        <div
          style={{
            color: "var(--color-text-secondary, #888)",
            fontSize: "0.875rem",
          }}
        >
          {t("apps.shareEmpty")}
        </div>
      ) : (
        <div
          style={{
            border: "1px solid var(--color-border, #333)",
            borderRadius: 8,
            overflow: "hidden",
          }}
        >
          {tokens.map((tk, i) => (
            <div
              key={tk.tokenPreview}
              style={{
                padding: "10px 14px",
                display: "flex",
                justifyContent: "space-between",
                alignItems: "center",
                borderBottom:
                  i < tokens.length - 1
                    ? "1px solid var(--color-border, #333)"
                    : "none",
                background: "var(--color-surface, #111)",
                gap: 12,
              }}
            >
              <div style={{ flex: 1, minWidth: 0 }}>
                <div
                  style={{
                    fontFamily: "monospace",
                    fontSize: "0.8125rem",
                    color: "var(--color-text, #eee)",
                  }}
                >
                  {tk.tokenPreview}
                </div>
                <div
                  style={{
                    fontSize: "0.75rem",
                    color: "var(--color-text-secondary, #888)",
                    marginTop: 2,
                  }}
                >
                  {t("apps.shareExpiresIn")}:{" "}
                  {tk.expiresAt ? formatDate(tk.expiresAt) : t("apps.shareExpiresNever")}
                  {" · "}
                  {t("apps.shareRateLimit")}:{" "}
                  {tk.rateLimit ? String(tk.rateLimit) : t("apps.shareRateLimitNone")}
                </div>
              </div>
              <div
                style={{
                  fontSize: "0.75rem",
                  color: "var(--color-text-secondary, #888)",
                  flexShrink: 0,
                }}
              >
                {formatDate(tk.createdAt)}
              </div>
              <div
                style={{
                  display: "flex",
                  gap: 6,
                  alignItems: "center",
                  flexShrink: 0,
                }}
              >
                {confirmDeletePreview === tk.tokenPreview ? (
                  <>
                    <span
                      style={{
                        fontSize: "0.75rem",
                        color: "var(--color-error, #e05)",
                      }}
                    >
                      {t("apps.confirmDeleteShare")}
                    </span>
                    <button
                      onClick={() => handleDeleteClick(tk.tokenPreview)}
                      disabled={deleting === tk.tokenPreview}
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
                      {deleting === tk.tokenPreview ? "..." : "Delete"}
                    </button>
                    <button
                      onClick={() => setConfirmDeletePreview(null)}
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
                  <button
                    onClick={() => handleDeleteClick(tk.tokenPreview)}
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
                )}
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
