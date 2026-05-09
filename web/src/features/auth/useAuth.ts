"use client";

import { useState, useEffect, useCallback, useRef } from "react";

interface AuthProvider {
  name: string;
  type: "password" | "redirect";
}

interface AuthState {
  loading: boolean;
  required: boolean;
  authenticated: boolean;
  providers: AuthProvider[];
}

function resolveApiBase(): string {
  if (typeof window === "undefined") return "http://127.0.0.1:10112";
  const { protocol, hostname, port } = window.location;
  if (port === "10111") return `${protocol}//${hostname}:10112`;
  return "";
}

export function useAuth() {
  const [state, setState] = useState<AuthState>({
    loading: true,
    required: false,
    authenticated: false,
    providers: [],
  });
  const retryRef = useRef<ReturnType<typeof setTimeout>>(undefined);
  const retryCountRef = useRef(0);

  const checkStatus = useCallback(async () => {
    try {
      const base = resolveApiBase();
      const res = await fetch(`${base}/api/auth/status`, { credentials: "include" });
      if (!res.ok) throw new Error("status check failed");
      const data = await res.json();
      retryCountRef.current = 0;

      // Fetch available auth providers.
      let providers: AuthProvider[] = [];
      try {
        const pRes = await fetch(`${base}/api/auth/providers`, { credentials: "include" });
        if (pRes.ok) {
          const pData = await pRes.json();
          providers = pData.providers || [];
        }
      } catch { /* ignore */ }

      setState({
        loading: false,
        required: data.required,
        authenticated: data.authenticated,
        providers,
      });
    } catch {
      // Server unreachable — retry with backoff (max 5s), treat as no-auth after 5 retries
      const count = retryCountRef.current++;
      if (count < 5) {
        const delay = Math.min(1000 * Math.pow(1.5, count), 5000);
        retryRef.current = setTimeout(() => { checkStatus(); }, delay);
        return;
      }
      setState({ loading: false, required: false, authenticated: true, providers: [] });
    }
  }, []);

  useEffect(() => {
    checkStatus();
    return () => { clearTimeout(retryRef.current); };
  }, [checkStatus]);

  const login = useCallback(async (username: string, password: string): Promise<string | null> => {
    try {
      const base = resolveApiBase();
      const res = await fetch(`${base}/api/auth/login`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "include",
        body: JSON.stringify({ username, password }),
      });
      if (!res.ok) {
        const data = await res.json().catch(() => ({ error: "login failed" }));
        return data.error || "login failed";
      }
      setState((s) => ({ ...s, authenticated: true }));
      return null;
    } catch {
      return "network error";
    }
  }, []);

  const logout = useCallback(async () => {
    const base = resolveApiBase();
    await fetch(`${base}/api/auth/logout`, {
      method: "POST",
      credentials: "include",
    }).catch(() => {});
    setState((s) => ({ ...s, authenticated: false }));
  }, []);

  const oidcLogin = useCallback(() => {
    const base = resolveApiBase();
    window.location.href = `${base}/api/auth/oidc/login`;
  }, []);

  return { ...state, login, logout, oidcLogin };
}
