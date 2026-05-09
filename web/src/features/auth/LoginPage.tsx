"use client";

import { useState, type FormEvent } from "react";
import { motion } from "framer-motion";
import { useT } from "@/features/i18n";

interface AuthProvider {
  name: string;
  type: "password" | "redirect";
}

interface Props {
  onLogin: (username: string, password: string) => Promise<string | null>;
  providers?: AuthProvider[];
  onOidcLogin?: () => void;
}

export function LoginPage({ onLogin, providers = [], onOidcLogin }: Props) {
  const { t } = useT();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setLoading(true);
    try {
      const err = await onLogin(username, password);
      if (err) setError(err);
    } finally {
      setLoading(false);
    }
  }

  return (
    <div className="auth-page">
      <motion.form
        className="auth-card"
        onSubmit={handleSubmit}
        initial={{ opacity: 0, scale: 0.96, y: 8 }}
        animate={{ opacity: 1, scale: 1, y: 0 }}
        transition={{ duration: 0.35, ease: "easeOut" }}
      >
        <div className="auth-card__header">
          <svg width="40" height="40" viewBox="0 0 128 128" aria-hidden="true">
            <defs>
              <linearGradient id="ag" x1="0%" y1="0%" x2="100%" y2="100%">
                <stop offset="0%" stopColor="#22c55e" />
                <stop offset="100%" stopColor="#4ade80" />
              </linearGradient>
            </defs>
            <rect width="128" height="128" rx="24" fill="var(--bg-secondary, #090a0b)" />
            <rect x="40" y="28" width="10" height="10" rx="2" fill="#22c55e" opacity="0.6" />
            <rect x="50" y="24" width="10" height="14" rx="2" fill="#22c55e" />
            <rect x="60" y="22" width="10" height="16" rx="2" fill="#22c55e" />
            <rect x="70" y="24" width="10" height="14" rx="2" fill="#22c55e" />
            <rect x="80" y="28" width="10" height="10" rx="2" fill="#22c55e" opacity="0.6" />
            <rect x="34" y="50" width="14" height="14" rx="3" fill="#22c55e" />
            <rect x="82" y="50" width="14" height="14" rx="3" fill="#22c55e" />
            <rect x="50" y="80" width="30" height="5" rx="2" fill="#22c55e" />
          </svg>
          <h1 className="auth-card__title">Saker</h1>
        </div>

        {error && (
          <div className="auth-card__error" role="alert">
            {error}
          </div>
        )}

        <label className="auth-card__field">
          <span className="auth-card__label">{t("auth.username")}</span>
          <input
            className="auth-card__input"
            type="text"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            autoComplete="username"
            autoFocus
            required
          />
        </label>

        <label className="auth-card__field">
          <span className="auth-card__label">{t("auth.password")}</span>
          <input
            className="auth-card__input"
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            autoComplete="current-password"
            required
          />
        </label>

        <button className="auth-card__submit" type="submit" disabled={loading}>
          {loading ? t("auth.loggingIn") : t("auth.login")}
        </button>

        {providers.some((p) => p.type === "redirect") && onOidcLogin && (
          <>
            <div className="auth-card__divider">
              <span>{t("auth.or")}</span>
            </div>
            <button
              className="auth-card__submit auth-card__submit--oidc"
              type="button"
              onClick={onOidcLogin}
            >
              {t("auth.ssoLogin")}
            </button>
          </>
        )}
      </motion.form>
    </div>
  );
}
