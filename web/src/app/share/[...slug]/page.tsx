// Server Component — no "use client".
// Catch-all route handles /share/{token} for any token value.
// With output: "export" we generate one shell page; the token is read
// from window.location client-side after hydration.

import type { Metadata } from "next";
import { ShareApp } from "./ShareClient";

// Tokens are unguessable secrets — search engines must not index share URLs.
export const metadata: Metadata = {
  robots: { index: false, follow: false },
};

export function generateStaticParams() {
  // Produce a single shell page that covers all token values at runtime.
  return [{ slug: ["_"] }];
}

export default function ShareTokenPage() {
  return <ShareApp />;
}
