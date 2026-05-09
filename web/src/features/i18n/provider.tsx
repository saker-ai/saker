"use client";

import { useContext, useState, useEffect, type ReactNode } from "react";
import { type Locale, I18nContext } from "./context";
import { dict, type TKey } from "./dict";

const LOCALE_STORAGE_KEY = "saker-locale";

function detectLocale(): Locale {
  if (typeof window !== "undefined") {
    const saved = localStorage.getItem(LOCALE_STORAGE_KEY);
    if (saved === "en" || saved === "zh") return saved;
  }
  if (typeof navigator === "undefined") return "en";
  const lang = navigator.language || "";
  return lang.startsWith("zh") ? "zh" : "en";
}

export function I18nProvider({ children }: { children: ReactNode }) {
  const [locale, setLocaleRaw] = useState<Locale>("en");

  useEffect(() => {
    setLocaleRaw(detectLocale());
  }, []);

  const setLocale = (l: Locale) => {
    setLocaleRaw(l);
    localStorage.setItem(LOCALE_STORAGE_KEY, l);
  };

  const t = (key: TKey): string => dict[key]?.[locale] ?? key;

  return (
    <I18nContext.Provider value={{ locale, setLocale, t }}>
      {children}
    </I18nContext.Provider>
  );
}

export function useT() {
  return useContext(I18nContext);
}