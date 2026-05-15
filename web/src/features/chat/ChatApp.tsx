"use client";

import { useState, useEffect, useCallback, useRef, useMemo } from "react";
import { flushSync } from "react-dom";
import { toast } from "sonner";
import {
  PanelLeftClose,
  PanelLeftOpen,
  Menu,
  X,
} from "lucide-react";
import { RPCClient, resolveWsUrl } from "@/features/rpc/client";
import { httpRequest, setHTTPProjectIdProvider } from "@/features/rpc/httpRpc";
import { useRpcStore } from "@/features/rpc/rpcStore";
import type {
  Thread,
  ThreadItem,
  ApprovalRequest,
  QuestionRequest,
  StreamEvent,
  SkillInfo,
  SkillContentResult,
  GenericTaskStatus,
  SkillImportPreviewResult,
  SkillStats,
  ServerSettings,
  SandboxConfig,
  StorageConfig,
  AigoConfig,
  FailoverConfig,
} from "@/features/rpc/types";
import { LoginPage } from "@/features/auth/LoginPage";
import { IconNav, type NavView } from "./IconNav";
import { Composer, type Attachment } from "./Composer";
import { SkillsPage } from "@/features/skills";
import { SettingsPanel } from "./SettingsPanel";
import { useCanvasBridge } from "@/features/canvas/useCanvasBridge";
import { useCanvasStore, saveToServer } from "@/features/canvas/store";
import { useT } from "@/features/i18n";
import { TasksView } from "@/features/tasks/TasksView";
import { AppsView } from "@/features/apps/AppsView";
import { useProjectStore, projectIdProvider } from "@/features/project/projectStore";
import { TopBar } from "./TopBar";
import { CanvasLayout } from "./CanvasLayout";
import { ChatMainView } from "./ChatMainView";
import { useIsMobile, parseHash, type TurnStatus, type AuthProvider } from "./chatUtils";
import { SakerCopilotProvider } from "@/features/agui/provider";
import { CopilotBridge } from "@/features/agui/CopilotBridge";


interface ChatAppProps {
  authRequired?: boolean;
  authenticated?: boolean;
  onLogin?: (username: string, password: string) => Promise<string | null>;
  onLogout?: () => Promise<void> | void;
  authProviders?: AuthProvider[];
  onOidcLogin?: () => void;
}

export function ChatApp({ authRequired, authenticated, onLogin, onLogout, authProviders, onOidcLogin }: ChatAppProps) {
  const { t } = useT();
  const rpcRef = useRef<RPCClient | null>(null);
  const switchThreadRef = useRef<((id: string) => Promise<void>) | null>(null);
  // Three states replace the old `connected`:
  //   bootstrapped         — HTTP boot finished, main UI can render
  //   wsConnected          — WebSocket is currently OPEN (used to disable
  //                          composer / show banner when a streaming session
  //                          loses its connection mid-turn)
  //   wsHasBeenConnectedRef — WS has opened at least once this session;
  //                          banner stays hidden until the user has actually
  //                          relied on streaming (avoids alarming idle users)
  const [bootstrapped, setBootstrapped] = useState(false);
  const [wsConnected, setWsConnected] = useState(false);
  const wsHasBeenConnectedRef = useRef(false);
  // Derived "looks fine" gate — true at idle (never connected) and while
  // currently connected. Mirrors the old `connected || !hasConnectedRef.current`
  // pattern that several child components rely on.
  const wsHealthy = wsConnected || !wsHasBeenConnectedRef.current;
  const [showLogin, setShowLogin] = useState(false);
  const [threads, setThreads] = useState<Thread[]>([]);
  const [activeThreadId, setActiveThreadId] = useState("");
  const [messages, setMessages] = useState<ThreadItem[]>([]);
  const [streamText, setStreamText] = useState("");
  const [turnStatus, setTurnStatus] = useState<TurnStatus>("idle");
  const [activeTurnId, setActiveTurnId] = useState("");
  const [approvals, setApprovals] = useState<ApprovalRequest[]>([]);
  const [questions, setQuestions] = useState<QuestionRequest[]>([]);
  const [toolEvents, setToolEvents] = useState<StreamEvent[]>([]);
  const lastPromptRef = useRef("");
  const manuscriptCommandsRef = useRef(new Map<string, {
    nodeId: string;
    scope?: "selection" | "document" | "entity";
    sourceText?: string;
    selectionStart?: number;
    selectionEnd?: number;
  }>());

  const isMobile = useIsMobile();
  const [mobileDrawerOpen, setMobileDrawerOpen] = useState(false);

  // Navigation & panel state — synced with URL hash.
  // Initialize with default to avoid SSR/client hydration mismatch (#418).
  const [activeView, setActiveViewRaw] = useState<NavView>("chats");
  const [hydrated, setHydrated] = useState(false);

  const setActiveView = useCallback((view: NavView, threadId?: string) => {
    setActiveViewRaw(view);
    const tid = threadId ?? "";
    if (view === "chats" && !tid) {
      window.location.hash = "";
    } else if (tid) {
      window.location.hash = `${view}/${tid}`;
    } else {
      window.location.hash = view;
    }
  }, []);

  // Sync view from URL hash after hydration (avoids SSR mismatch).
  useEffect(() => {
    const { view, threadId } = parseHash();
    setActiveViewRaw(view);
    setHydrated(true);
    if (threadId) {
      switchThreadRef.current?.(threadId);
    }
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  // Respond to browser back/forward — restore view and thread
  useEffect(() => {
    const onHashChange = () => {
      const { view, threadId } = parseHash();
      setActiveViewRaw(view);
      if (threadId) {
        switchThreadRef.current?.(threadId);
      }
    };
    window.addEventListener("hashchange", onHashChange);
    return () => window.removeEventListener("hashchange", onHashChange);
  }, []);
  const [skills, setSkills] = useState<SkillInfo[]>([]);
  const [settings, setSettings] = useState<ServerSettings | null>(null);
  const [registeredTools, setRegisteredTools] = useState<{ name: string; description: string; category: string }[]>([]);
  const [embedBackends, setEmbedBackends] = useState<{ name: string; env_key: string; available: boolean }[]>([]);
  const [currentUser, setCurrentUser] = useState<{ username: string; role: string }>({ username: "", role: "admin" });
  const [panelCollapsed, setPanelCollapsed] = useState(true);
  const [canvasChatOpen, setCanvasChatOpen] = useState(false);
  const canvasNodes = useCanvasStore((s) => s.nodes);
  const canvasHasNodes = canvasNodes.length > 0;
  const highlightedTurnId = useCanvasStore((s) => s.highlightedTurnId);

  // Scroll chat to highlighted turn when canvas node is clicked.
  useEffect(() => {
    if (!highlightedTurnId) return;
    const el = document.querySelector(`[data-turn-id="${highlightedTurnId}"]`);
    if (el) el.scrollIntoView({ behavior: "smooth", block: "center" });
  }, [highlightedTurnId]);

  // (thread panel state is shared across chats and canvas views)

  // Canvas bridge — always enabled when a thread is active (supports offline & background updates)
  const canvasEnabled = activeView === "canvas";
  const { addPrompt: canvasAddPrompt, resetCanvas, setTurnId: canvasSetTurnId } = useCanvasBridge(
    rpcRef.current,
    canvasEnabled,
    activeThreadId || undefined
  );

  // Initialize RPC client.
  useEffect(() => {
    const rpc = new RPCClient(resolveWsUrl());
    rpcRef.current = rpc;
    useRpcStore.getState().setRpc(rpc);
    // Auto-inject projectId on every non-skip RPC for BOTH transports.
    // Bound once at boot; stays in effect across WS reconnects and HTTP
    // bootstrap calls.
    rpc.setProjectIdProvider(projectIdProvider);
    setHTTPProjectIdProvider(projectIdProvider);

    rpc.on("_connected", () => {
      // First WS open this session — flip the ref so the disconnected banner
      // can fire on subsequent drops. Bootstrap data is already loaded over
      // HTTP; this handler only manages streaming-availability state now.
      wsHasBeenConnectedRef.current = true;
      setWsConnected(true);
    });

    rpc.on("_disconnected", () => setWsConnected(false));

    rpc.on("thread/item", (params) => {
      const item = params as ThreadItem;
      setMessages((prev) => {
        if (prev.some((m) => m.id === item.id)) return prev;
        return [...prev, item];
      });
      if (item.role === "assistant" && item.turn_id) {
        const command = manuscriptCommandsRef.current.get(item.turn_id);
        if (command) {
          window.dispatchEvent(
            new CustomEvent("manuscript-ai-result", {
              detail: {
                nodeId: command.nodeId,
                turnId: item.turn_id,
                scope: command.scope,
                sourceText: command.sourceText,
                selectionStart: command.selectionStart,
                selectionEnd: command.selectionEnd,
                content: item.content,
              },
            })
          );
          manuscriptCommandsRef.current.delete(item.turn_id);
        }
      }
    });

    rpc.on("thread/item_updated", (params) => {
      const updated = params as ThreadItem;
      setMessages((prev) => prev.map((m) => m.id === updated.id ? updated : m));
    });

    rpc.on("stream/event", (params) => {
      const evt = params as StreamEvent;
      if (evt.delta?.text) {
        setStreamText((prev) => prev + evt.delta!.text!);
      }
      if (
        evt.type === "tool_execution_start" ||
        evt.type === "tool_execution_output" ||
        evt.type === "tool_execution_result"
      ) {
        setToolEvents((prev) => [...prev, evt]);
      }
      if (evt.type === "tool_execution_start") {
        setTurnStatus("running");
      }
    });

    rpc.on("turn/finished", () => {
      setTurnStatus("idle");
      // Delay clearing streamText and toolEvents so the persisted messages
      // render first, avoiding a visible flash between streaming and persisted content.
      requestAnimationFrame(() => {
        requestAnimationFrame(() => {
          setStreamText("");
          setToolEvents([]);
        });
      });
      setActiveTurnId("");
    });

    rpc.on("turn/error", (params) => {
      setTurnStatus("error");
      setStreamText("");
      setToolEvents([]);
      const err = params as { turnId: string; error: string };
      toast.error(err.error, {
        duration: 10000,
      });
      setTurnStatus("idle");
    });

    rpc.on("approval/request", (params) => {
      const req = params as ApprovalRequest;
      setApprovals((prev) => [...prev, req]);
      setTurnStatus("waiting");
    });

    rpc.on("question/request", (params) => {
      const req = params as QuestionRequest;
      setQuestions((prev) => [...prev, req]);
      setTurnStatus("waiting");
    });

    rpc.on("approval/timeout", (params) => {
      const { approvalId } = params as { approvalId: string };
      setApprovals((prev) => prev.filter((a) => a.id !== approvalId));
      setTurnStatus("running");
    });

    rpc.on("question/timeout", (params) => {
      const { questionId } = params as { questionId: string };
      setQuestions((prev) => prev.filter((q) => q.id !== questionId));
      setTurnStatus("running");
    });

    // Don't connect on mount — defer until authenticated or auth not required.
    return () => rpc.disconnect();
  }, []);

  // HTTP bootstrap. Replaces the WS-on-mount path: 6 boot RPCs run over the
  // /api/rpc/* adapter so opening the page never establishes a WebSocket.
  // The WS only opens later when the user submits a turn or subscribes to a
  // thread (rpc.request → ensureConnected → connect).
  useEffect(() => {
    if (authRequired && !authenticated) return;
    if (bootstrapped) return;
    let cancelled = false;
    (async () => {
      // initialize is fire-and-forget for HTTP — the response carries a
      // throwaway clientId we don't need (each HTTP request gets its own).
      // Kept for parity / metrics so the server still sees the boot ping.
      httpRequest<{ clientId: string }>("initialize").catch(() => {});

      // Projects must land before the scope-bound calls so injectProjectId
      // has a value to inject. Failure is non-fatal in legacy single-project
      // mode (server with no project store returns an empty list).
      try {
        await useProjectStore.getState().refresh();
      } catch {
        // ignore — store records the error itself
      }
      if (cancelled) return;

      // The remaining 4 are independent — fan them out concurrently.
      const [threadsRes, userRes, skillsRes, settingsRes] = await Promise.all([
        httpRequest<{ threads: Thread[] }>("thread/list").catch(() => ({ threads: [] as Thread[] })),
        httpRequest<{ username: string; role: string }>("user/me").catch(() => ({ username: "", role: "admin" })),
        httpRequest<{ skills: SkillInfo[] }>("skill/list").catch(() => ({ skills: [] as SkillInfo[] })),
        httpRequest<{ settings: ServerSettings; tools?: { name: string; description: string; category: string }[]; embedBackends?: { name: string; env_key: string; available: boolean }[] }>("settings/get").catch(() => ({ settings: null as unknown as ServerSettings, tools: [] as { name: string; description: string; category: string }[], embedBackends: [] as { name: string; env_key: string; available: boolean }[] })),
      ]);
      if (cancelled) return;

      const list = threadsRes.threads || [];
      setThreads(list);
      const { threadId } = parseHash();
      if (threadId && list.some((t) => t.id === threadId)) {
        // Subscribe lazily — switchThread calls rpc.request("thread/subscribe")
        // which triggers ensureConnected. Intentional: a deep-link visitor
        // gets streaming as soon as they land on a real thread.
        switchThreadRef.current?.(threadId);
      }

      setCurrentUser({ username: userRes.username || "", role: userRes.role || "admin" });
      setSkills(skillsRes.skills || []);
      setSettings(settingsRes.settings || null);
      setRegisteredTools(settingsRes.tools || []);
      setEmbedBackends(settingsRes.embedBackends || []);
      setBootstrapped(true);
    })();
    return () => {
      cancelled = true;
    };
  }, [authRequired, authenticated, bootstrapped]);

  // React to project switches: clear scope-bound state and reload thread/skill
  // /settings under the new project. The very first project pick (after the
  // initial refresh on _connected) is treated as the baseline and skipped —
  // we don't want to clobber the thread list that was just loaded.
  const lastProjectIdRef = useRef<string | null>(null);
  // Mirror activeThreadId into a ref so the project-switch subscriber (which
  // only re-binds when resetCanvas changes) can read the latest value when
  // sending thread/unsubscribe for the previous project.
  const activeThreadIdRef = useRef("");
  useEffect(() => {
    activeThreadIdRef.current = activeThreadId;
  }, [activeThreadId]);
  useEffect(() => {
    return useProjectStore.subscribe((state) => {
      const next = state.currentProjectId;
      if (next === lastProjectIdRef.current) return;
      const prev = lastProjectIdRef.current;
      lastProjectIdRef.current = next;
      // Skip the boot-time transition from null → first project.
      if (prev === null) return;
      const rpc = rpcRef.current;
      if (!rpc) return;

      // Tell the server we're done with the previous project's active thread
      // before we drop the local state. Pass projectId explicitly because the
      // auto-injector now points at the new project. Only do this when the
      // WS is actually open — if the user never sent a turn, there's nothing
      // to unsubscribe and we don't want to spuriously open a connection
      // just to clean up state that doesn't exist on the server.
      const prevThreadId = activeThreadIdRef.current;
      if (prev && prevThreadId && rpc.connected) {
        rpc
          .request("thread/unsubscribe", { threadId: prevThreadId, projectId: prev })
          .catch(() => {});
      }

      // Drop URL hash thread reference so we don't try to re-subscribe a
      // thread that belongs to the previous project.
      if (typeof window !== "undefined") {
        const { view } = parseHash();
        window.location.hash = view;
      }
      // Clear in-flight conversation state.
      setMessages([]);
      setStreamText("");
      setToolEvents([]);
      setApprovals([]);
      setQuestions([]);
      setActiveThreadId("");
      setTurnStatus("idle");
      // Drop the old canvas (next thread will load its own).
      resetCanvas();

      // Reload scope-bound resources under the new project over HTTP — same
      // rules as boot. Independent so failures shouldn't block the others.
      httpRequest<{ threads: Thread[] }>("thread/list")
        .then((r) => setThreads(r.threads || []))
        .catch(() => setThreads([]));
      httpRequest<{ skills: SkillInfo[] }>("skill/list")
        .then((r) => setSkills(r.skills || []))
        .catch(() => {});
      httpRequest<{ settings: ServerSettings; tools?: { name: string; description: string; category: string }[]; embedBackends?: { name: string; env_key: string; available: boolean }[] }>("settings/get")
        .then((r) => {
          setSettings(r.settings || null);
          setRegisteredTools(r.tools || []);
          setEmbedBackends(r.embedBackends || []);
        })
        .catch(() => {});
    });
  }, [resetCanvas]);

  // Poll project/list every 30s once the app is bootstrapped so newly accepted
  // invites or project deletions surface in the dropdown without a manual
  // refresh. Goes over HTTP — no longer triggers a WebSocket reconnect when
  // the user is idle. Skipped while the tab is hidden to avoid background
  // traffic.
  useEffect(() => {
    if (!bootstrapped) return;
    const tick = () => {
      if (typeof document !== "undefined" && document.hidden) return;
      useProjectStore.getState().refresh().catch(() => {});
    };
    const id = window.setInterval(tick, 30_000);
    return () => window.clearInterval(id);
  }, [bootstrapped]);

  /** Check auth before interactive actions. Returns true if OK to proceed. */
  const requireAuth = useCallback(() => {
    if (authRequired && !authenticated) {
      setShowLogin(true);
      return false;
    }
    return true;
  }, [authRequired, authenticated]);

  const switchThread = useCallback(
    async (threadId: string) => {
      if (!requireAuth()) return;
      const rpc = rpcRef.current;
      if (!rpc) return;

      // Detect reconnect: same thread being re-subscribed after server restart.
      const isReconnect = threadId === activeThreadId;

      if (activeThreadId && !isReconnect) {
        rpc
          .request("thread/unsubscribe", { threadId: activeThreadId })
          .catch(() => {});
      }

      setActiveThreadId(threadId);
      // Sync thread ID into URL hash (preserve current view).
      const currentView = parseHash().view;
      const viewForHash = currentView === "skills" || currentView === "settings" ? "chats" : currentView;
      window.location.hash = `${viewForHash}/${threadId}`;
      if (viewForHash !== currentView) {
        setActiveViewRaw(viewForHash);
      }
      setMessages([]);
      setStreamText("");
      setTurnStatus("idle");
      setToolEvents([]);
      setApprovals([]);
      setQuestions([]);

      // Only reset canvas when actually switching threads, not on reconnect.
      // On reconnect the canvas already has correct data; resetting would
      // clear it and the bridge effect won't reload (same threadId).
      if (!isReconnect) {
        // Save current thread's canvas before clearing — resetCanvas() sets
        // intentionalClear which would cause the bridge effect's deferred
        // saveToServer to overwrite the previous thread's data with empty state.
        if (activeThreadId) {
          saveToServer(rpc, activeThreadId).catch(() => {});
        }
        resetCanvas();
      }

      try {
        const result = await rpc.request<{ items: ThreadItem[] }>(
          "thread/subscribe",
          { threadId }
        );
        setMessages(result.items || []);
      } catch (e) {
        console.error("subscribe error:", e);
      }
    },
    [requireAuth, activeThreadId, resetCanvas]
  );
  switchThreadRef.current = switchThread;

  const createThread = useCallback(async () => {
    if (!requireAuth()) return;
    try {
      const thread = await httpRequest<Thread>("thread/create", {
        title: "New Chat",
      });
      setThreads((prev) => [...prev, thread]);
      await switchThread(thread.id);
    } catch (e) {
      console.error("create thread error:", e);
    }
  }, [requireAuth, switchThread]);

  const doSend = useCallback(
    async (threadId: string, text: string, attachments?: Attachment[]) => {
      const rpc = rpcRef.current;
      if (!rpc) return;
      setTurnStatus("running");
      setStreamText("");
      setToolEvents([]); // Clear previous turn's tool cards
      lastPromptRef.current = text;

      // Check for pending branch node (set by canvas branch button).
      const branchNodeId = useCanvasStore.getState().pendingBranchNodeId;
      if (branchNodeId) {
        useCanvasStore.getState().setPendingBranchNodeId(null);
      }
      canvasAddPrompt(text, branchNodeId || undefined);

      try {
        const params: Record<string, unknown> = { threadId, text };
        if (attachments && attachments.length > 0) {
          params.attachments = attachments.map(a => ({
            path: a.path,
            name: a.name,
            media_type: a.media_type,
          }));
        }
        // Flush canvas before turn/send so any just-created node (added via
        // QuickAdd or context menu just before the user typed) is on disk
        // when the agent's tools fetch canvas state. Mirror the manuscript
        // pre-flush — without this, regular chat turns can hit the same
        // "no canvas data" race that bit thread bc72d587.
        await saveToServer(rpc, threadId).catch((err) => {
          console.warn("canvas pre-flush failed:", err);
        });
        const res = await rpc.request<{ turnId: string }>("turn/send", params);
        const turnId = res.turnId || "";
        setActiveTurnId(turnId);
        // Link turnId back to canvas nodes for bidirectional navigation.
        if (turnId) {
          canvasSetTurnId(turnId);
        }
      } catch (e) {
        console.error("send error:", e);
        setTurnStatus("error");
        toast.error(String(e));
      }
    },
    [canvasAddPrompt, canvasSetTurnId]
  );

  useEffect(() => {
    const handler = (event: Event) => {
      const detail = (event as CustomEvent<{
        prompt?: string;
        branchNodeId?: string;
        nodeId?: string;
        scope?: "selection" | "document" | "entity";
        sourceText?: string;
        selectionStart?: number;
        selectionEnd?: number;
      }>).detail;
      const prompt = detail?.prompt?.trim();
      if (!prompt || !activeThreadId || turnStatus === "running") return;
      if (detail?.branchNodeId) {
        useCanvasStore.getState().setPendingBranchNodeId(detail.branchNodeId);
      }
      const rpc = rpcRef.current;
      if (!rpc) return;
      setTurnStatus("running");
      setStreamText("");
      setToolEvents([]);
      lastPromptRef.current = prompt;
      canvasAddPrompt(prompt, detail?.branchNodeId || undefined);
      // Manuscript AI commands often spawn fresh nodes (e.g. extractToTable creates
      // an empty table node and immediately asks the agent to write into it).
      // Flush canvas to the server BEFORE turn/send so the agent's first
      // canvas_get_node / canvas_table_write call actually sees the new node
      // instead of racing with the autosave debounce.
      void saveToServer(rpc, activeThreadId)
        .catch((err) => console.warn("canvas pre-flush failed:", err))
        .then(() => rpc.request<{ turnId: string }>("turn/send", { threadId: activeThreadId, text: prompt }))
        .then((res) => {
          if (!res) return;
          const turnId = res.turnId || "";
          setActiveTurnId(turnId);
          if (turnId) {
            canvasSetTurnId(turnId);
            if (detail?.nodeId) {
              manuscriptCommandsRef.current.set(turnId, {
                nodeId: detail.nodeId,
                scope: detail.scope,
                sourceText: detail.sourceText,
                selectionStart: detail.selectionStart,
                selectionEnd: detail.selectionEnd,
              });
            }
          }
        })
        .catch((e) => {
          console.error("send error:", e);
          setTurnStatus("error");
          toast.error(String(e));
          // Roll back any speculative canvas node the dispatcher created so
          // a failed send doesn't leave an empty table + dangling edge behind.
          const cleanupId = (detail as { cleanupTableNodeId?: string } | undefined)?.cleanupTableNodeId;
          if (cleanupId) useCanvasStore.getState().removeNode(cleanupId);
        });
    };
    window.addEventListener("manuscript-ai-command", handler);
    return () => window.removeEventListener("manuscript-ai-command", handler);
  }, [activeThreadId, turnStatus, canvasAddPrompt, canvasSetTurnId]);

  /** Generate a short title from user input (first sentence or first 40 chars). */
  const generateTitle = useCallback((text: string): string => {
    // Use first sentence if short enough
    const firstSentence = text.split(/[。.!?！？\n]/)[0].trim();
    if (firstSentence.length > 0 && firstSentence.length <= 40) {
      return firstSentence;
    }
    // Otherwise truncate at word boundary
    if (text.length <= 40) return text;
    const truncated = text.slice(0, 40);
    const lastSpace = truncated.lastIndexOf(" ");
    return (lastSpace > 20 ? truncated.slice(0, lastSpace) : truncated) + "...";
  }, []);

  const handleAutoCreateThread = useCallback(async (title: string) => {
    if (!requireAuth()) return;
    try {
      const thread = await httpRequest<Thread>("thread/create", { title });
      flushSync(() => {
        setThreads((prev) => [...prev, thread]);
        setActiveThreadId(thread.id);
      });
      window.location.hash = `chats/${thread.id}`;
    } catch (e) {
      console.error("auto-create thread error:", e);
    }
  }, [requireAuth]);

  /** Update thread title on the server and locally. */
  const updateThreadTitle = useCallback(
    (threadId: string, title: string) => {
      setThreads((prev) =>
        prev.map((t) => (t.id === threadId ? { ...t, title } : t))
      );
      httpRequest("thread/update", { threadId, title }).catch(() => {});
    },
    []
  );

  /** Delete a thread on the server and locally. */
  const deleteThread = useCallback(
    (threadId: string) => {
      setThreads((prev) => prev.filter((t) => t.id !== threadId));
      if (activeThreadId === threadId) {
        setActiveThreadId("");
        setMessages([]);
      }
      httpRequest("thread/delete", { threadId }).catch(() => {});
    },
    [activeThreadId]
  );

  const sendMessage = useCallback(
    async (text: string, attachments?: Attachment[]) => {
      if (!requireAuth()) return;
      if (!activeThreadId || turnStatus === "running") return;
      // Auto-update title if still default
      const thread = threads.find((t) => t.id === activeThreadId);
      if (thread && (thread.title === "New Chat" || thread.title === "New Thread")) {
        updateThreadTitle(activeThreadId, generateTitle(text));
      }
      await doSend(activeThreadId, text, attachments);
    },
    [requireAuth, activeThreadId, turnStatus, doSend, threads, generateTitle, updateThreadTitle]
  );

  const sendWithAutoCreate = useCallback(
    async (text: string, attachments?: Attachment[]) => {
      if (!requireAuth()) return;
      const title = generateTitle(text);
      if (!activeThreadId) {
        try {
          const thread = await httpRequest<Thread>("thread/create", { title });
          setThreads((prev) => [...prev, thread]);
          await switchThread(thread.id);
          await doSend(thread.id, text, attachments);
        } catch (e) {
          console.error("create thread error:", e);
        }
      } else {
        // Auto-update title if still default
        const thread = threads.find((t) => t.id === activeThreadId);
        if (thread && (thread.title === "New Chat" || thread.title === "New Thread")) {
          updateThreadTitle(activeThreadId, title);
        }
        await sendMessage(text, attachments);
      }
    },
    [requireAuth, activeThreadId, switchThread, sendMessage, doSend, threads, generateTitle, updateThreadTitle]
  );

  const handleApproval = useCallback(
    async (approvalId: string, decision: "allow" | "deny") => {
      const rpc = rpcRef.current;
      if (!rpc) return;
      try {
        await rpc.request("approval/respond", { approvalId, decision });
        setApprovals((prev) => prev.filter((a) => a.id !== approvalId));
        if (decision === "allow") {
          setTurnStatus("running");
        } else {
          setTurnStatus("idle");
        }
      } catch (e) {
        console.error("approval error:", e);
      }
    },
    []
  );

  const handleQuestionRespond = useCallback(
    async (questionId: string, answers: Record<string, string>) => {
      const rpc = rpcRef.current;
      if (!rpc) return;
      try {
        await rpc.request("question/respond", { questionId, answers });
        setQuestions((prev) => prev.filter((q) => q.id !== questionId));
        setTurnStatus("running");
      } catch (e) {
        console.error("question respond error:", e);
      }
    },
    []
  );

  const cancelTurn = useCallback(async () => {
    const rpc = rpcRef.current;
    if (!rpc || !activeTurnId) return;
    try {
      await rpc.request("turn/cancel", { turnId: activeTurnId });
      setTurnStatus("idle");
      setStreamText("");
      setToolEvents([]);
      setActiveTurnId("");
    } catch (e) {
      console.error("cancel error:", e);
    }
  }, [activeTurnId]);

  const refreshSettings = useCallback(async () => {
    const rpc = rpcRef.current;
    if (!rpc) return;
    try {
      const r = await rpc.request<{ settings: ServerSettings; tools?: { name: string; description: string; category: string }[]; embedBackends?: { name: string; env_key: string; available: boolean }[] }>("settings/get");
      setSettings(r.settings || null);
      setRegisteredTools(r.tools || []);
      setEmbedBackends(r.embedBackends || []);
    } catch (e) {
      console.error("refresh settings error:", e);
    }
  }, []);

  const updateAigoSettings = useCallback(
    async (aigo: AigoConfig) => {
      const rpc = rpcRef.current;
      if (!rpc) return;
      await rpc.request("settings/update", { aigo } as Record<string, unknown>);
      await refreshSettings();
    },
    [refreshSettings]
  );

  const updateSandboxSettings = useCallback(
    async (sandbox: SandboxConfig) => {
      const rpc = rpcRef.current;
      if (!rpc) return;
      await rpc.request("settings/update", { sandbox } as Record<string, unknown>);
      await refreshSettings();
    },
    [refreshSettings]
  );

  const updateStorageSettings = useCallback(
    async (storage: StorageConfig) => {
      const rpc = rpcRef.current;
      if (!rpc) return;
      await rpc.request("settings/update", { storage } as Record<string, unknown>);
      await refreshSettings();
    },
    [refreshSettings]
  );

  const updateFailoverSettings = useCallback(
    async (failover: FailoverConfig) => {
      const rpc = rpcRef.current;
      if (!rpc) return;
      await rpc.request("settings/update", { failover } as Record<string, unknown>);
      await refreshSettings();
    },
    [refreshSettings]
  );

  const updateAuthSettings = useCallback(
    async (username: string, password: string) => {
      const rpc = rpcRef.current;
      if (!rpc) return;
      await rpc.request("auth/update", { username, password } as Record<string, unknown>);
      await refreshSettings();
    },
    [refreshSettings]
  );

  const createUser = useCallback(
    async (username: string, password: string) => {
      const rpc = rpcRef.current;
      if (!rpc) return;
      await rpc.request("user/create", { username, password } as Record<string, unknown>);
      await refreshSettings();
    },
    [refreshSettings]
  );

  const deleteUser = useCallback(
    async (username: string) => {
      const rpc = rpcRef.current;
      if (!rpc) return;
      await rpc.request("user/delete", { username } as Record<string, unknown>);
      await refreshSettings();
    },
    [refreshSettings]
  );

  const deleteAuthSettings = useCallback(
    async () => {
      const rpc = rpcRef.current;
      if (!rpc) return;
      await rpc.request("auth/delete", {} as Record<string, unknown>);
      await refreshSettings();
    },
    [refreshSettings]
  );

  const sortedThreads = useMemo(
    () =>
      [...threads].sort(
        (a, b) =>
          new Date(b.updated_at).getTime() - new Date(a.updated_at).getTime()
      ),
    [threads]
  );

  const activeThread = useMemo(
    () => threads.find((t) => t.id === activeThreadId),
    [threads, activeThreadId]
  );

  const togglePanel = useCallback(() => {
    if (isMobile && activeView !== "canvas") {
      setMobileDrawerOpen((v) => !v);
    } else {
      setPanelCollapsed((v) => !v);
    }
  }, [isMobile, activeView]);

  // Global ⌘\ / Ctrl+\ shortcut to toggle the conversation panel.
  // Skips when focus is in a text field, contenteditable, or while a modifier
  // combo other than the bare Cmd/Ctrl is held — so it doesn't fight Composer
  // shortcuts or the browser's own ⌘⇧\.
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if (e.key !== "\\") return;
      const mod = e.metaKey || e.ctrlKey;
      if (!mod || e.shiftKey || e.altKey) return;
      const tgt = e.target as HTMLElement | null;
      if (tgt) {
        const tag = tgt.tagName;
        if (tag === "INPUT" || tag === "TEXTAREA" || tgt.isContentEditable) return;
      }
      e.preventDefault();
      togglePanel();
    };
    window.addEventListener("keydown", handler);
    return () => window.removeEventListener("keydown", handler);
  }, [togglePanel]);

  const renderThreadPanelToggle = useCallback(() => {
    const isPanelOpen = isMobile && activeView !== "canvas"
      ? mobileDrawerOpen
      : !panelCollapsed;

    const ariaLabel = isPanelOpen
      ? t("thread.collapsePanel")
      : isMobile
        ? t("chat.openChatList")
        : t("thread.expandPanel");

    // Show platform-appropriate shortcut hint in title (mac→⌘\, others→Ctrl+\).
    const isMac = typeof navigator !== "undefined" && /Mac|iPod|iPhone|iPad/.test(navigator.platform);
    const shortcut = isMobile ? "" : isMac ? "⌘\\" : "Ctrl+\\";
    const title = shortcut ? `${ariaLabel} (${shortcut})` : ariaLabel;

    const Icon = isMobile
      ? (isPanelOpen ? X : Menu)
      : (isPanelOpen ? PanelLeftClose : PanelLeftOpen);

    return (
      <button
        className={`thread-panel-toggle-btn${isPanelOpen ? " thread-panel-toggle-btn--open" : ""}`}
        onClick={togglePanel}
        aria-label={ariaLabel}
        aria-expanded={isPanelOpen}
        aria-controls="thread-panel"
        title={title}
      >
        <Icon size={isMobile ? 18 : 16} />
      </button>
    );
  }, [isMobile, activeView, mobileDrawerOpen, panelCollapsed, t, togglePanel]);

  return (
    <SakerCopilotProvider threadId={activeThreadId}>
    <CopilotBridge threadId={activeThreadId} historyMessages={messages}>
    <div className="app">
      <TopBar
        username={currentUser.username}
        role={currentUser.role}
        onLogout={onLogout}
        leftSlot={
          activeView === "chats" || activeView === "canvas" ? (
            <>
              {renderThreadPanelToggle()}
              {activeView === "canvas" && (
                <input
                  className="topbar-thread-title"
                  value={activeThread?.title || ""}
                  placeholder={t("nav.canvas")}
                  aria-label={t("nav.canvas")}
                  onChange={(e) => {
                    if (activeThreadId) updateThreadTitle(activeThreadId, e.target.value);
                  }}
                  onKeyDown={(e) => { if (e.key === "Enter") (e.target as HTMLInputElement).blur(); }}
                />
              )}
            </>
          ) : undefined
        }
      />
      <IconNav
        active={activeView}
        onChange={(view) => { if (view !== "chats" && !requireAuth()) return; setActiveView(view); }}
        visible={activeView !== "chats" && activeView !== "canvas" || panelCollapsed}
        showLoginBtn={authRequired && !authenticated}
        onLoginClick={() => setShowLogin(true)}
      />

      {activeView === "canvas" ? (
        <CanvasLayout
          sortedThreads={sortedThreads}
          activeThreadId={activeThreadId}
          activeThread={activeThread}
          switchThread={switchThread}
          createThread={createThread}
          deleteThread={deleteThread}
          panelCollapsed={panelCollapsed}
          wsHealthy={wsHealthy}
          canvasChatOpen={canvasChatOpen}
          setCanvasChatOpen={setCanvasChatOpen}
          canvasHasNodes={canvasHasNodes}
          messages={messages}
          streamText={streamText}
          turnStatus={turnStatus}
          toolEvents={toolEvents}
          highlightedTurnId={highlightedTurnId}
          approvals={approvals}
          questions={questions}
          onApproval={handleApproval}
          onQuestionRespond={handleQuestionRespond}
          sendMessage={sendMessage}
          sendWithAutoCreate={sendWithAutoCreate}
          cancelTurn={cancelTurn}
          skills={skills}
        />
      ) : activeView === "apps" ? (
        <AppsView />
      ) : activeView === "tasks" ? (
        <TasksView rpc={rpcRef.current} connected={wsConnected} />
      ) : activeView === "skills" ? (
        <div className="app-content">
          <SkillsPage
            rpc={rpcRef.current}
            skills={skills}
            disabledSkills={settings?.disabledSkills ?? []}
            onToggleSkill={async (name, disabled) => {
              const current = settings?.disabledSkills ?? [];
              const updated = disabled
                ? [...current, name]
                : current.filter(n => n.toLowerCase() !== name.toLowerCase());
              await rpcRef.current?.request("settings/update", { disabledSkills: updated });
              setSettings(s => s ? { ...s, disabledSkills: updated } : s);
            }}
            onRemove={async (name) => {
              await rpcRef.current?.request("skill/remove", { name });
              const r = await rpcRef.current?.request<{ skills: SkillInfo[] }>("skill/list");
              setSkills(r?.skills || []);
            }}
            onPromote={async (name) => {
              await rpcRef.current?.request("skill/promote", { name });
              const r = await rpcRef.current?.request<{ skills: SkillInfo[] }>("skill/list");
              setSkills(r?.skills || []);
            }}
            onLoadContent={async (name) => {
              const r = await rpcRef.current?.request<SkillContentResult>("skill/content", { name });
              return r!;
            }}
            onLoadAnalytics={async () => {
              const r = await rpcRef.current?.request<Record<string, SkillStats>>("skill/analytics");
              return r ?? null;
            }}
            onImport={async (payload) => {
              const r = await rpcRef.current?.request<{ taskId: string }>("skill/import", payload as unknown as Record<string, unknown>);
              return r ?? { taskId: "" };
            }}
            onPreviewImport={async (payload) => {
              const r = await rpcRef.current?.request<SkillImportPreviewResult>("skill/import-preview", payload as unknown as Record<string, unknown>);
              return r ?? { items: [] };
            }}
            onTaskStatus={async (taskId) => {
              const r = await rpcRef.current?.request<GenericTaskStatus>("tool/task-status", { taskId });
              return r!;
            }}
            onRefreshSkills={async () => {
              const r = await rpcRef.current?.request<{ skills: SkillInfo[] }>("skill/list");
              setSkills(r?.skills || []);
              return r?.skills || [];
            }}
          />
        </div>
      ) : activeView === "settings" ? (
        <div className="app-content">
          <div className="page-container page-container-settings">
            <h1 className="page-title">{t("nav.settings")}</h1>
            <SettingsPanel
              settings={settings}
              connected={wsHealthy}
              registeredTools={registeredTools}
              embedBackends={embedBackends}
              isAdmin={currentUser.role === "admin"}
              onUpdateAigo={updateAigoSettings}
              onUpdateFailover={updateFailoverSettings}
              onUpdateSandbox={updateSandboxSettings}
              onUpdateStorage={updateStorageSettings}
              onUpdateAuth={updateAuthSettings}
              onDeleteAuth={deleteAuthSettings}
              onCreateUser={createUser}
              onDeleteUser={deleteUser}
              rpc={rpcRef.current}
            />
          </div>
        </div>
      ) : (
        <ChatMainView
          isMobile={isMobile}
          mobileDrawerOpen={mobileDrawerOpen}
          setMobileDrawerOpen={setMobileDrawerOpen}
          sortedThreads={sortedThreads}
          activeThreadId={activeThreadId}
          switchThread={switchThread}
          createThread={createThread}
          deleteThread={deleteThread}
          panelCollapsed={panelCollapsed}
          wsHealthy={wsHealthy}
          onAutoCreateThread={handleAutoCreateThread}
          skills={skills}
        />
      )}
      {showLogin && onLogin && (
        <div className="auth-overlay">
          <LoginPage
            onLogin={async (u, p) => {
              const err = await onLogin(u, p);
              if (!err) setShowLogin(false);
              return err;
            }}
            providers={authProviders || []}
            onOidcLogin={onOidcLogin}
          />
        </div>
      )}
    </div>
    </CopilotBridge>
    </SakerCopilotProvider>
  );
}