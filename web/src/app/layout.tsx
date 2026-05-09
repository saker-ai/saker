import type { Metadata } from "next";
import localFont from "next/font/local";
import { Toaster } from "sonner";
import { ErrorBoundary } from "@/components/ErrorBoundary";
import "./globals.css";

const inter = localFont({
  src: "../../public/fonts/InterVariable.woff2",
  variable: "--font-sans",
  display: "swap",
  weight: "100 900",
});

const jetbrains = localFont({
  src: "../../public/fonts/JetBrainsMono-Variable.ttf",
  variable: "--font-mono",
  display: "swap",
  weight: "100 800",
});

export const metadata: Metadata = {
  title: "Saker",
  description: "Saker AI Agent",
  icons: {
    icon: "/favicon.svg",
  },
};

export default function RootLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <html lang="en">
      <body className={`${inter.variable} ${jetbrains.variable}`}>
        <ErrorBoundary>
          <a href="#main-content" className="skip-link">
            Skip to main content
          </a>
          {children}
        </ErrorBoundary>
        <Toaster position="bottom-right" richColors />
      </body>
    </html>
  );
}
