"use client";

import { useEffect } from "react";
import { useTheme } from "next-themes";

// Themes whose color scheme is dark. Mirrors web/src/features/chat/
// ThemeProvider.tsx THEME_SCHEME and the inline boot script in app/layout.tsx.
const DARK_THEMES = new Set(["dark", "cinema-gold"]);

/**
 * Bridges next-themes' data-theme attribute onto the .dark utility class so
 * Tailwind's `@custom-variant dark (&:where(.dark, .dark *))` keeps working.
 *
 * next-themes is configured with attribute="data-theme" only (see layout.tsx),
 * because writing `class="cinema-gold"` would clobber the .dark utility.
 *
 * The first paint is handled by the synchronous inline boot script in
 * layout.tsx — this hook just keeps .dark in sync on subsequent theme changes
 * driven by user toggles or storage events.
 */
export function ThemeBridge() {
	const { resolvedTheme } = useTheme();

	useEffect(() => {
		if (!resolvedTheme) return;
		const isDark = DARK_THEMES.has(resolvedTheme);
		document.documentElement.classList.toggle("dark", isDark);
	}, [resolvedTheme]);

	return null;
}
