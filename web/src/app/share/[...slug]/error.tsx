"use client";

import { useEffect } from "react";

export default function Error({
  error,
  reset,
}: {
  error: Error & { digest?: string };
  reset: () => void;
}) {
  useEffect(() => {
    console.error("Share page error:", error);
  }, [error]);

  return (
    <div style={{
      display: "flex",
      flexDirection: "column",
      alignItems: "center",
      justifyContent: "center",
      minHeight: "100vh",
      padding: "2rem",
      fontFamily: "system-ui, sans-serif",
    }}>
      <h1 style={{ fontSize: "1.5rem", marginBottom: "0.5rem" }}>Something went wrong</h1>
      <p style={{ color: "#666", marginBottom: "1rem", maxWidth: "400px" }}>
        The shared content failed to load. Please try again or refresh the page.
      </p>
      <details style={{ marginBottom: "1rem", maxWidth: "600px", width: "100%" }}>
        <summary style={{ cursor: "pointer", color: "#888" }}>Error details</summary>
        <pre style={{
          marginTop: "0.5rem",
          padding: "1rem",
          background: "#f5f5f5",
          borderRadius: "4px",
          overflow: "auto",
          fontSize: "0.875rem",
        }}>
          {error.message}
          {error.digest && `\nDigest: ${error.digest}`}
        </pre>
      </details>
      <button
        onClick={reset}
        style={{
          padding: "0.5rem 1rem",
          background: "#0070f3",
          color: "white",
          border: "none",
          borderRadius: "4px",
          cursor: "pointer",
          fontSize: "0.875rem",
        }}
      >
        Try again
      </button>
    </div>
  );
}