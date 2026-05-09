"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { Copy, ExternalLink } from "lucide-react";
import type { RPCClient } from "@/features/rpc/client";
import type { SkillhubDeviceLogin } from "@/features/rpc/types";
import { useT, type TKey } from "@/features/i18n";
import { useSkillhubRpc } from "./useSkillhubRpc";

interface Props {
  open: boolean;
  rpc: RPCClient | null;
  registry?: string;
  onClose: () => void;
  onSuccess?: (handle: string) => void;
}

type Phase = "init" | "waiting" | "success" | "error";

export function SkillhubLoginModal({ open, rpc, registry, onClose, onSuccess }: Props) {
  const { t } = useT();
  const api = useSkillhubRpc(rpc);
  const [phase, setPhase] = useState<Phase>("init");
  const [challenge, setChallenge] = useState<SkillhubDeviceLogin | null>(null);
  const [error, setError] = useState("");
  const [copied, setCopied] = useState(false);
  const pollTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const expiryTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const cancelledRef = useRef(false);

  const sessionIdRef = useRef<string | null>(null);

  const cleanup = useCallback(() => {
    cancelledRef.current = true;
    if (pollTimerRef.current) clearTimeout(pollTimerRef.current);
    if (expiryTimerRef.current) clearTimeout(expiryTimerRef.current);
    pollTimerRef.current = null;
    expiryTimerRef.current = null;
    // Best-effort: tell the server to drop the in-flight device flow so its
    // deviceCode can no longer be polled. We swallow errors — the session
    // would expire on its own after 10 minutes anyway.
    const sid = sessionIdRef.current;
    sessionIdRef.current = null;
    if (sid) {
      void api.loginCancel(sid).catch(() => {});
    }
  }, [api]);

  const beginPolling = useCallback((session: SkillhubDeviceLogin) => {
    const intervalMs = Math.max(1000, (session.interval || 5) * 1000);
    const tick = async () => {
      if (cancelledRef.current) return;
      try {
        const res = await api.loginPoll(session.sessionId);
        if (cancelledRef.current) return;
        if (res.status === "ok") {
          // Server already deleted the session — drop our id so cleanup
          // doesn't fire a redundant cancel.
          sessionIdRef.current = null;
          setPhase("success");
          onSuccess?.(res.handle || "");
          setTimeout(() => {
            if (!cancelledRef.current) onClose();
          }, 800);
          return;
        }
        if (res.status === "error") {
          setPhase("error");
          setError(res.error || t("skillhub.loginFailed"));
          return;
        }
        pollTimerRef.current = setTimeout(() => void tick(), intervalMs);
      } catch (e) {
        if (cancelledRef.current) return;
        setPhase("error");
        setError(e instanceof Error ? e.message : String(e));
      }
    };
    pollTimerRef.current = setTimeout(() => void tick(), intervalMs);
  }, [api, onClose, onSuccess, t]);

  const startLogin = useCallback(async () => {
    cancelledRef.current = false;
    setPhase("init");
    setError("");
    setChallenge(null);
    setCopied(false);
    if (!rpc) {
      setPhase("error");
      setError(t("skillhub.disconnected"));
      return;
    }
    try {
      const ch = await api.loginStart(registry);
      if (cancelledRef.current) return;
      sessionIdRef.current = ch.sessionId;
      setChallenge(ch);
      setPhase("waiting");
      beginPolling(ch);
      const expiryMs = Math.max(30_000, (ch.expiresIn || 600) * 1000);
      expiryTimerRef.current = setTimeout(() => {
        if (cancelledRef.current) return;
        setPhase("error");
        setError(t("skillhub.loginExpired"));
      }, expiryMs);
    } catch (e) {
      if (cancelledRef.current) return;
      setPhase("error");
      setError(e instanceof Error ? e.message : String(e));
    }
  }, [api, beginPolling, registry, rpc, t]);

  useEffect(() => {
    if (!open) {
      cleanup();
      return;
    }
    void startLogin();
    return cleanup;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape" && phase !== "success") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose, open, phase]);

  const handleCopy = useCallback(async () => {
    if (!challenge) return;
    try {
      await navigator.clipboard.writeText(challenge.userCode);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // Clipboard may be blocked; ignore.
    }
  }, [challenge]);

  const handleClose = useCallback(() => {
    if (phase === "waiting") cleanup();
    onClose();
  }, [cleanup, onClose, phase]);

  if (!open) return null;

  return (
    <div className="provider-modal-overlay" onClick={handleClose}>
      <div className="provider-modal" tabIndex={-1} onClick={(e) => e.stopPropagation()}>
        <div className="provider-modal-header">
          <span className="provider-modal-title">{t("skillhub.login")}</span>
          <div className="provider-modal-header-actions">
            <button
              className="provider-modal-close"
              onClick={handleClose}
              aria-label={t("common.close" as TKey)}
            >
              ×
            </button>
          </div>
        </div>

        <div className="provider-modal-body">
          {phase === "init" && (
            <div className="skills-detail-empty">{t("plaza.loading")}</div>
          )}

          {phase === "waiting" && challenge && (
            <>
              <div className="provider-modal-section">
                <div className="provider-modal-section-title">{t("skillhub.loginCode")}</div>
                <div className="skillhub-login-code-row">
                  <code className="skillhub-login-code">{challenge.userCode}</code>
                  <button
                    type="button"
                    className="settings-btn-cancel skillhub-login-icon-btn"
                    onClick={() => void handleCopy()}
                    aria-label={t("skillhub.loginCopyCode")}
                  >
                    <Copy size={14} />
                    <span>{copied ? t("skillhub.loginCodeCopied") : t("skillhub.loginCopyCode")}</span>
                  </button>
                </div>
              </div>

              <div className="provider-modal-section">
                <div className="settings-hint">{t("skillhub.loginInstruction")}</div>
                <a
                  href={challenge.verificationUrl}
                  target="_blank"
                  rel="noreferrer"
                  className="settings-btn-save skillhub-login-icon-btn"
                >
                  <ExternalLink size={14} />
                  <span>{t("skillhub.loginOpenUrl")}</span>
                </a>
              </div>

              <div className="settings-hint skillhub-login-pending">
                {t("skillhub.loginPending")}
              </div>
            </>
          )}

          {phase === "success" && (
            <div className="provider-modal-section skillhub-login-success">
              {t("skillhub.loginSuccess")}
            </div>
          )}

          {phase === "error" && (
            <div className="provider-modal-section">
              <div className="skills-detail-empty skills-import-error">
                {error || t("skillhub.loginFailed")}
              </div>
            </div>
          )}
        </div>

        <div className="provider-modal-footer">
          <button
            type="button"
            className="settings-btn-cancel"
            onClick={handleClose}
            disabled={phase === "success"}
          >
            {t("skillhub.loginCancel")}
          </button>
          {phase === "error" && (
            <button
              type="button"
              className="settings-btn-save"
              onClick={() => void startLogin()}
            >
              {t("chat.retry")}
            </button>
          )}
        </div>
      </div>
    </div>
  );
}
