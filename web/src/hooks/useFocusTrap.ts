"use client";

import { useEffect, useRef, useCallback } from "react";

/**
 * useFocusTrap confines keyboard focus within a container element.
 * Useful for modal dialogs and dropdown menus where Tab should not
 * escape to background content.
 *
 * @param active - whether the trap is currently engaged
 * @returns ref to attach to the container element
 */
export function useFocusTrap<T extends HTMLElement = HTMLElement>(active: boolean) {
  const containerRef = useRef<T>(null);

  const handleKeyDown = useCallback((e: KeyboardEvent) => {
    if (e.key !== "Tab" || !containerRef.current) return;

    const focusable = containerRef.current.querySelectorAll<HTMLElement>(
      'a[href], button:not([disabled]), textarea, input, select, [tabindex]:not([tabindex="-1"])'
    );
    if (focusable.length === 0) return;

    const first = focusable[0];
    const last = focusable[focusable.length - 1];

    if (e.shiftKey) {
      if (document.activeElement === first) {
        e.preventDefault();
        last.focus();
      }
    } else {
      if (document.activeElement === last) {
        e.preventDefault();
        first.focus();
      }
    }
  }, []);

  useEffect(() => {
    if (!active) return;

    document.addEventListener("keydown", handleKeyDown);

    // Auto-focus the first focusable element when trap activates.
    if (containerRef.current) {
      const first = containerRef.current.querySelector<HTMLElement>(
        'a[href], button:not([disabled]), textarea, input, select, [tabindex]:not([tabindex="-1"])'
      );
      if (first) first.focus();
    }

    return () => document.removeEventListener("keydown", handleKeyDown);
  }, [active, handleKeyDown]);

  return containerRef;
}