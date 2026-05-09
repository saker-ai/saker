"use client";

import { createContext, useContext, useState, useEffect, useCallback } from "react";

export type Theme =
  | "system"
  | "dark"
  | "light"
  | "warm-editorial"
  | "cinema-gold"
  | "ink-wash"
  | "brutalist";

export type ResolvedTheme = Exclude<Theme, "system">;

// Each non-system theme has an inherent color scheme — used to mirror
// dark/light affordances (scrollbars, ambient glow, etc.) and to map
// "system" onto a sensible default.
export const THEME_SCHEME: Record<ResolvedTheme, "dark" | "light"> = {
  dark: "dark",
  light: "light",
  "warm-editorial": "light",
  "cinema-gold": "dark",
  "ink-wash": "light",
  brutalist: "light",
};

interface ThemeContextValue {
  theme: Theme;
  resolved: ResolvedTheme;
  setTheme: (t: Theme) => void;
  toggle: () => void;
}

const ThemeContext = createContext<ThemeContextValue>({
  theme: "system",
  resolved: "dark",
  setTheme: () => {},
  toggle: () => {},
});

export const useTheme = () => useContext(ThemeContext);

function getSystemTheme(): "dark" | "light" {
  if (typeof window === "undefined") return "dark";
  return window.matchMedia("(prefers-color-scheme: light)").matches ? "light" : "dark";
}

function resolve(theme: Theme): ResolvedTheme {
  return theme === "system" ? getSystemTheme() : theme;
}

export function ThemeProvider({ children }: { children: React.ReactNode }) {
  // Use fixed defaults to avoid SSR/client hydration mismatch (#418).
  // Actual theme is synced in useEffect after mount.
  const [theme, setThemeRaw] = useState<Theme>("system");
  const [resolved, setResolved] = useState<ResolvedTheme>("dark");

  const applyTheme = useCallback((t: Theme) => {
    const r = resolve(t);
    setResolved(r);
    document.documentElement.setAttribute("data-theme", r);
    if (t === "system") {
      document.documentElement.removeAttribute("data-theme-manual");
    } else {
      document.documentElement.setAttribute("data-theme-manual", t);
    }
  }, []);

  const setTheme = useCallback((t: Theme) => {
    setThemeRaw(t);
    localStorage.setItem("saker-theme", t);
    applyTheme(t);
  }, [applyTheme]);

  // Backwards-compat toggle: cycles between dark and light Gemini themes.
  // For non-Gemini palettes the toggle keeps the current theme — users
  // pick palettes explicitly via the swatch picker.
  const toggle = useCallback(() => {
    const scheme = THEME_SCHEME[resolved];
    const next: Theme = scheme === "dark" ? "light" : "dark";
    setTheme(next);
  }, [resolved, setTheme]);

  // Sync theme from localStorage after mount (avoids SSR mismatch).
  useEffect(() => {
    const saved = localStorage.getItem("saker-theme") as Theme | null;
    const valid: Theme[] = [
      "system",
      "dark",
      "light",
      "warm-editorial",
      "cinema-gold",
      "ink-wash",
      "brutalist",
    ];
    const initial: Theme = saved && valid.includes(saved) ? saved : "dark";
    setThemeRaw(initial);
    applyTheme(initial);
  }, [applyTheme]);

  // Listen for system theme changes when in "system" mode
  useEffect(() => {
    if (theme !== "system") return;
    const mq = window.matchMedia("(prefers-color-scheme: light)");
    const handler = () => applyTheme("system");
    mq.addEventListener("change", handler);
    return () => mq.removeEventListener("change", handler);
  }, [theme, applyTheme]);

  return (
    <ThemeContext.Provider value={{ theme, resolved, setTheme, toggle }}>
      {children}
    </ThemeContext.Provider>
  );
}
