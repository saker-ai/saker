"use client";

import { useEffect, useRef, useState, useCallback } from "react";

// Adaptive polling for canvas runs. Replaces the fixed 1500ms setInterval
// scheme used by AppRunner.tsx and ShareClient.tsx with a backoff that:
//   - Starts at 200ms so a fast run completes inside the first beat.
//   - Multiplies by 1.6 each tick, capped at 3000ms — slow runs stop
//     hammering the server once they've shown they are not about to finish.
//   - Re-checks every 5000ms while the tab is hidden, then snaps back to a
//     fast tick on visibilitychange so the user sees "done" immediately.
//   - Stops on terminal status (done, error, cancelled) or whenever the
//     caller flips `enabled` off.
//
// The hook is intentionally storage-free: callers own the `summary` they get
// from the fetcher; this hook only schedules the next call.

const MIN_DELAY_MS = 200;
const MAX_DELAY_MS = 3000;
const HIDDEN_DELAY_MS = 5000;
const BACKOFF_FACTOR = 1.6;

// Terminal statuses returned by canvas.RunSummary.Status — keep in sync with
// pkg/canvas/tracker.go: RunStatusDone / RunStatusFailed / RunStatusCancelled.
const TERMINAL: ReadonlyArray<string> = ["done", "error", "cancelled"];

export interface RunStatusLike {
  status: string;
}

export interface UseRunPollingOptions<T extends RunStatusLike> {
  /** Whether polling should run. Flip false to stop without unmounting. */
  enabled: boolean;
  /** Returns the latest summary; throws on transport error. */
  fetcher: () => Promise<T>;
  /** Called on every successful poll. */
  onUpdate: (summary: T) => void;
  /** Called when the fetcher rejects. The caller decides whether to surface or retry. */
  onError?: (err: unknown) => void;
  /** Called once when a terminal status is observed. */
  onTerminal?: (summary: T) => void;
}

export function useRunPolling<T extends RunStatusLike>(opts: UseRunPollingOptions<T>) {
  const { enabled, fetcher, onUpdate, onError, onTerminal } = opts;

  // Stash callbacks in refs so the polling loop doesn't restart every render
  // when the parent re-creates closures — only `enabled` should restart it.
  const fetcherRef = useRef(fetcher);
  const onUpdateRef = useRef(onUpdate);
  const onErrorRef = useRef(onError);
  const onTerminalRef = useRef(onTerminal);
  fetcherRef.current = fetcher;
  onUpdateRef.current = onUpdate;
  onErrorRef.current = onError;
  onTerminalRef.current = onTerminal;

  useEffect(() => {
    if (!enabled) return;

    let timer: ReturnType<typeof setTimeout> | null = null;
    let cancelled = false;
    let delay = MIN_DELAY_MS;

    const isHidden = (): boolean =>
      typeof document !== "undefined" && document.visibilityState === "hidden";

    const schedule = (ms: number) => {
      if (cancelled) return;
      timer = setTimeout(tick, ms);
    };

    const tick = async () => {
      if (cancelled) return;
      try {
        const summary = await fetcherRef.current();
        if (cancelled) return;
        onUpdateRef.current(summary);
        if (TERMINAL.includes(summary.status)) {
          onTerminalRef.current?.(summary);
          return;
        }
        // Not terminal yet: back off, capped, and pause harder when hidden.
        delay = Math.min(MAX_DELAY_MS, Math.ceil(delay * BACKOFF_FACTOR));
        schedule(isHidden() ? HIDDEN_DELAY_MS : delay);
      } catch (err) {
        if (cancelled) return;
        onErrorRef.current?.(err);
        // On transport error, keep polling (the tracker may still be alive)
        // but back off to avoid pinning the server when it's down.
        delay = Math.min(MAX_DELAY_MS, Math.ceil(delay * BACKOFF_FACTOR));
        schedule(isHidden() ? HIDDEN_DELAY_MS : delay);
      }
    };

    // Immediate first poll so a sub-200ms run never has to wait the floor.
    schedule(0);

    // Visibility change: snap back to a tight loop when the tab becomes
    // visible so the user doesn't stare at stale "running" state.
    const onVisibility = () => {
      if (cancelled) return;
      if (document.visibilityState === "visible") {
        if (timer !== null) clearTimeout(timer);
        delay = MIN_DELAY_MS;
        schedule(0);
      }
    };
    if (typeof document !== "undefined") {
      document.addEventListener("visibilitychange", onVisibility);
    }

    return () => {
      cancelled = true;
      if (timer !== null) clearTimeout(timer);
      if (typeof document !== "undefined") {
        document.removeEventListener("visibilitychange", onVisibility);
      }
    };
  }, [enabled]);
}

// Helper for callers that want a "running" boolean derived from the latest
// summary — saves them maintaining their own state.
export function useRunFinishedState<T extends RunStatusLike>(summary: T | null): {
  isTerminal: boolean;
  isRunning: boolean;
  isDone: boolean;
  isError: boolean;
  isCancelled: boolean;
} {
  const [, force] = useState(0);
  const rerender = useCallback(() => force((n) => n + 1), []);
  // Touch summary so eslint exhaustive-deps is satisfied (rerender is stable).
  void summary;
  void rerender;
  const status = summary?.status ?? "";
  return {
    isTerminal: TERMINAL.includes(status),
    isRunning: status === "running",
    isDone: status === "done",
    isError: status === "error",
    isCancelled: status === "cancelled",
  };
}
