import { useCallback, useSyncExternalStore } from "react";

/** Detect if viewport is <=480px (mobile drawer breakpoint). */
export function useIsMobile() {
  const subscribe = useCallback((cb: () => void) => {
    const mq = window.matchMedia("(max-width: 480px)");
    mq.addEventListener("change", cb);
    return () => mq.removeEventListener("change", cb);
  }, []);
  const getSnapshot = useCallback(() => {
    if (typeof window === "undefined") return false;
    return window.matchMedia("(max-width: 480px)").matches;
  }, []);
  return useSyncExternalStore(subscribe, getSnapshot, () => false);
}

export type TurnStatus = "idle" | "running" | "waiting" | "error";

export const VALID_VIEWS = new Set(["chats", "skills", "tasks", "settings", "canvas", "apps"]);

export type NavView = "chats" | "skills" | "tasks" | "settings" | "canvas" | "apps";

export function parseHash(): { view: NavView; threadId: string } {
  if (typeof window === "undefined") return { view: "chats", threadId: "" };
  const raw = window.location.hash.replace("#", "");
  const [viewPart, ...rest] = raw.split("/");
  const threadId = rest.join("/");
  const view = VALID_VIEWS.has(viewPart) ? (viewPart as NavView) : "chats";
  return { view, threadId };
}

export function viewFromHash(): NavView {
  return parseHash().view;
}

export interface AuthProvider {
  name: string;
  type: "password" | "redirect";
}