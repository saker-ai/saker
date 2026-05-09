"use client";

import { useEffect, useRef } from "react";

/**
 * LiveRegion renders a visually-hidden aria-live region that announces
 * dynamic content changes to screen readers.
 *
 * @param message - the text to announce
 * @param politeness - "polite" (wait until idle) or "assertive" (interrupt)
 */
export function LiveRegion({ message, politeness = "polite" }: { message: string; politeness?: "polite" | "assertive" }) {
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    // Clear then set message so screen readers detect the change.
    if (ref.current) {
      ref.current.textContent = "";
      requestAnimationFrame(() => {
        if (ref.current) ref.current.textContent = message;
      });
    }
  }, [message]);

  return (
    <div
      ref={ref}
      role="status"
      aria-live={politeness}
      aria-atomic="true"
      className="sr-only"
      style={{ position: "absolute", width: "1px", height: "1px", overflow: "hidden", clip: "rect(0,0,0,0)" }}
    />
  );
}