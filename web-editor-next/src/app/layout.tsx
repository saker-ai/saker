import { ThemeProvider } from "next-themes";
import "./globals.css";
import localFont from "next/font/local";
import { Toaster } from "../components/ui/sonner";
import { TooltipProvider } from "../components/ui/tooltip";
import { ThemeBridge } from "../theme/theme-bridge";
import { baseMetaData } from "./metadata";

// Fonts live under src/fonts/ rather than public/fonts/ — Next.js's
// next/font/local pipeline copies the source into _next/static/media/ with
// a content hash, but if the file ALSO sits under public/ Next blindly
// publishes a second copy at /fonts/. Moving them out of public/ removes
// the duplicate (~344KB) without changing what the browser actually loads.
const inter = localFont({
	src: "../fonts/InterVariable.woff2",
	variable: "--font-sans",
	display: "swap",
	weight: "100 900",
});

const jetbrains = localFont({
	src: "../fonts/JetBrainsMono-Variable.ttf",
	variable: "--font-mono",
	display: "swap",
	weight: "100 800",
});

export const metadata = baseMetaData;

const SAKER_THEMES = [
	"system",
	"light",
	"dark",
	"warm-editorial",
	"cinema-gold",
	"ink-wash",
	"brutalist",
];

// Inline boot script — runs synchronously before React hydrates so the
// initial paint already has the right data-theme + .dark class on <html>.
// Mirrors src/theme/theme-bridge.tsx's THEME_SCHEME and reads the same
// localStorage key as next-themes (storageKey="saker-theme"). Without this
// there's a one-frame flash where :root's default-dark palette renders before
// next-themes hydrates and switches to the user's actual theme.
const THEME_BOOT_SCRIPT = `
(function () {
  try {
    var STORAGE_KEY = "saker-theme";
    var DARK_SET = { dark: 1, "cinema-gold": 1 };
    var LIGHT_SET = { light: 1, "warm-editorial": 1, "ink-wash": 1, brutalist: 1 };
    var raw = localStorage.getItem(STORAGE_KEY) || "dark";
    var theme = raw;
    if (theme === "system") {
      theme = matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
    }
    var root = document.documentElement;
    root.setAttribute("data-theme", theme);
    if (DARK_SET[theme]) root.classList.add("dark");
    else if (LIGHT_SET[theme]) root.classList.remove("dark");
  } catch (e) {
    document.documentElement.setAttribute("data-theme", "dark");
    document.documentElement.classList.add("dark");
  }
})();
`.trim();

export default function RootLayout({
	children,
}: Readonly<{
	children: React.ReactNode;
}>) {
	return (
		<html lang="en" suppressHydrationWarning>
			<head>
				<script
					// biome-ignore lint/security/noDangerouslySetInnerHtml: synchronous boot script must run before React hydration
					dangerouslySetInnerHTML={{ __html: THEME_BOOT_SCRIPT }}
				/>
			</head>
			<body
				className={`${inter.variable} ${jetbrains.variable} font-sans antialiased`}
			>
				<ThemeProvider
					attribute="data-theme"
					defaultTheme="dark"
					themes={SAKER_THEMES}
					storageKey="saker-theme"
					disableTransitionOnChange={true}
				>
					<ThemeBridge />
					<TooltipProvider>
						<Toaster />
						{children}
					</TooltipProvider>
				</ThemeProvider>
			</body>
		</html>
	);
}
