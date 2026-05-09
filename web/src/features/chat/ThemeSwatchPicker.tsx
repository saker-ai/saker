"use client";

import { Check } from "lucide-react";
import { useT } from "@/features/i18n";
import { useTheme, type Theme } from "./ThemeProvider";

interface SwatchSpec {
  id: Theme;
  i18nKey: string;
  bg: string;
  fg: string;
  accent: string;
}

const SWATCHES: SwatchSpec[] = [
  { id: "dark", i18nKey: "settings.dark", bg: "#0f0f12", fg: "#e8e8e8", accent: "#fb7185" },
  { id: "light", i18nKey: "settings.light", bg: "#fafaf7", fg: "#1a1a1a", accent: "#7c2d12" },
  { id: "warm-editorial", i18nKey: "settings.themeWarmEditorial", bg: "#f5f1ea", fg: "#1a1815", accent: "#c2410c" },
  { id: "cinema-gold", i18nKey: "settings.themeCinemaGold", bg: "#1a1614", fg: "#f0e6d2", accent: "#d4a574" },
  { id: "ink-wash", i18nKey: "settings.themeInkWash", bg: "#faf8f3", fg: "#1c1c1c", accent: "#c8392b" },
  { id: "brutalist", i18nKey: "settings.themeBrutalist", bg: "#ffffff", fg: "#000000", accent: "#00b86d" },
];

interface Props {
  onPick?: () => void;
}

/**
 * ThemeSwatchPicker — six visual color-scheme tiles. Each tile previews
 * the bg + accent of its palette so the user can pick at a glance, no
 * need to open settings.
 */
export function ThemeSwatchPicker({ onPick }: Props) {
  const { t } = useT();
  const { theme, setTheme } = useTheme();

  return (
    <div className="theme-swatch-picker" role="radiogroup" aria-label={t("settings.theme")}>
      {SWATCHES.map((s) => {
        const active = theme === s.id;
        return (
          <button
            key={s.id}
            type="button"
            role="radio"
            aria-checked={active}
            className={`theme-swatch ${active ? "theme-swatch--active" : ""}`}
            title={t(s.i18nKey as never)}
            aria-label={t(s.i18nKey as never)}
            onClick={() => {
              setTheme(s.id);
              onPick?.();
            }}
            style={{
              background: s.bg,
              color: s.fg,
              ["--swatch-accent" as string]: s.accent,
            }}
          >
            <span className="theme-swatch-dot" aria-hidden="true" />
            {active && (
              <span className="theme-swatch-check" aria-hidden="true">
                <Check size={10} strokeWidth={3} />
              </span>
            )}
          </button>
        );
      })}
    </div>
  );
}
