"use client";

import { useEffect, useRef, useMemo, useCallback, useState } from "react";
import { motion, AnimatePresence } from "framer-motion";
import { User, Copy, X, ChevronDown, ArrowDown, Brain } from "lucide-react";
import type { ThreadItem, StreamEvent } from "@/features/rpc/types";
import { extractMedia } from "@/features/media/extractMedia";
import { renderMarkdown } from "./markdown";
import { useT } from "@/features/i18n";

/** Scroll to bottom with RAF throttling to avoid layout thrashing during streaming. */
function useThrottledScrollToBottom(
  bottomRef: React.RefObject<HTMLDivElement | null>,
  deps: unknown[]
) {
  const rafRef = useRef<number>(0);
  const isNearBottomRef = useRef(true);

  useEffect(() => {
    const container = bottomRef.current?.parentElement;
    if (container) {
      const threshold = 100;
      isNearBottomRef.current =
        container.scrollHeight - container.scrollTop - container.clientHeight < threshold;
    }

    if (!isNearBottomRef.current) return;

    if (rafRef.current) cancelAnimationFrame(rafRef.current);
    rafRef.current = requestAnimationFrame(() => {
      bottomRef.current?.scrollIntoView({ behavior: "smooth" });
      rafRef.current = 0;
    });

    return () => {
      if (rafRef.current) cancelAnimationFrame(rafRef.current);
    };
  }, deps); // eslint-disable-line react-hooks/exhaustive-deps
}

interface Props {
  messages: ThreadItem[];
  streamText: string;
  streaming: boolean;
  toolEvents: StreamEvent[];
  /** Currently highlighted turn ID from canvas node click. */
  highlightedTurnId?: string | null;
}

interface ToolCard {
  name: string;
  toolUseId: string;
  status: "running" | "output" | "done" | "error";
  outputs: string[];
  isError: boolean;
  media?: { type: string; url: string };
}

interface TurnGroup {
  turnId: string;
  user?: ThreadItem;
  items: ThreadItem[];
}

export function MessageStream({
  messages,
  streamText,
  streaming,
  toolEvents,
  highlightedTurnId,
}: Props) {
  const { t } = useT();
  const bottomRef = useRef<HTMLDivElement>(null);
  const containerRef = useRef<HTMLDivElement>(null);
  const [lightboxUrl, setLightboxUrl] = useState<string | null>(null);
  const [showScrollBottom, setShowScrollBottom] = useState(false);

  useThrottledScrollToBottom(bottomRef, [messages, streamText, toolEvents]);

  // Handle scroll events to show/hide "Scroll to bottom" button
  useEffect(() => {
    const container = containerRef.current?.parentElement;
    if (!container) return;

    const handleScroll = () => {
      const isAtBottom = container.scrollHeight - container.scrollTop - container.clientHeight < 200;
      setShowScrollBottom(!isAtBottom);
    };

    container.addEventListener("scroll", handleScroll);
    return () => container.removeEventListener("scroll", handleScroll);
  }, []);

  const scrollToBottom = () => {
    bottomRef.current?.scrollIntoView({ behavior: "smooth" });
  };

  const handleClick = useCallback((e: React.MouseEvent) => {
    const target = e.target as HTMLElement;

    // Handle image clicks in message content — open lightbox
    if (target.tagName === "IMG" && target.closest(".message-content")) {
      const src = (target as HTMLImageElement).src;
      if (src) {
        e.preventDefault();
        setLightboxUrl(src);
        return;
      }
    }

    if (!target.classList.contains("copy-btn")) return;
    const wrapper = target.closest(".code-block-wrapper");
    if (!wrapper) return;
    const code = wrapper.querySelector("code");
    if (!code) return;
    navigator.clipboard.writeText(code.textContent || "").then(() => {
      target.textContent = t("message.copied");
      setTimeout(() => {
        target.textContent = t("message.copy");
      }, 2000);
    });
  }, [t]);

  useEffect(() => {
    if (!lightboxUrl) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setLightboxUrl(null);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [lightboxUrl]);

  const turnGroups = useMemo(() => {
    const turns = new Map<string, TurnGroup>();
    const standalone: ThreadItem[] = [];
    const turnOrder: string[] = [];

    for (const m of messages) {
      if (!m.turn_id) {
        standalone.push(m);
        continue;
      }
      if (!turns.has(m.turn_id)) {
        turns.set(m.turn_id, { turnId: m.turn_id, items: [] });
        turnOrder.push(m.turn_id);
      }
      const group = turns.get(m.turn_id)!;
      if (m.role === "user") group.user = m;
      else group.items.push(m);
    }
    return { standalone, turns, turnOrder };
  }, [messages]);

  const toolCards = useMemo(() => {
    const cards: ToolCard[] = [];
    for (const evt of toolEvents) {
      const name = evt.name || "tool";
      const tid = evt.tool_use_id || "";

      if (evt.type === "tool_execution_start") {
        cards.push({
          name,
          toolUseId: tid,
          status: "running",
          outputs: [],
          isError: false,
        });
      } else if (evt.type === "tool_execution_output") {
        const card = cards.find((c) => c.toolUseId === tid) || cards[cards.length - 1];
        if (card) {
          const text =
            typeof evt.output === "string"
              ? evt.output
              : JSON.stringify(evt.output);
          card.outputs.push(text);
          card.status = "output";
          if (evt.is_error) card.isError = true;
        }
      } else if (evt.type === "tool_execution_result") {
        const card = cards.find((c) => c.toolUseId === tid) || cards[cards.length - 1];
        if (card) {
          card.status = evt.is_error ? "error" : "done";
          if (evt.is_error) card.isError = true;
          if (!evt.is_error) {
            const media = extractMedia(evt);
            if (media) card.media = media;
          }
        }
      }
    }
    return cards;
  }, [toolEvents]);

  const onImageClick = useCallback((url: string) => setLightboxUrl(url), []);

  return (
    <div className="messages-inner" ref={containerRef} onClick={handleClick}>
      {turnGroups.standalone.map((m) => (
        <MessageBubble key={m.id} item={m} onImageClick={onImageClick} />
      ))}

      {turnGroups.turnOrder.map((turnId) => {
        const group = turnGroups.turns.get(turnId)!;
        const runs: { role: "assistant" | "tool"; items: ThreadItem[] }[] = [];
        for (const item of group.items) {
          const role = item.role === "tool" ? "tool" : "assistant";
          const last = runs[runs.length - 1];
          if (last && last.role === role) {
            last.items.push(item);
          } else {
            runs.push({ role, items: [item] });
          }
        }
        const isHighlighted = highlightedTurnId === turnId;
        return (
          <div
            key={turnId}
            className={`turn-group${isHighlighted ? " turn-highlighted" : ""}`}
            data-turn-id={turnId}
          >
            {group.user && (
              <MessageBubble item={group.user} onImageClick={onImageClick} />
            )}
            {runs.map((run, i) =>
              run.role === "assistant" ? (
                run.items.map((m) => (
                  <MessageBubble key={m.id} item={m} onImageClick={onImageClick} />
                ))
              ) : (
                <ToolGroupCollapse key={`tools-${i}`} tools={run.items} onImageClick={onImageClick} />
              )
            )}
          </div>
        );
      })}

      {toolCards.map((card, i) => {
        const joined = card.outputs.join("\n");
        const lineCount = joined ? joined.split("\n").length : 0;
        return (
          <motion.div
            initial={{ opacity: 0, height: 0 }}
            animate={{ opacity: 1, height: "auto" }}
            key={`${card.toolUseId}-${i}`}
            className={`tool-card ${card.isError ? "tool-error" : ""}`}
          >
            <div className="tool-card-header">
              <span className={`tool-status-icon status-${card.status}`} />
              <span className="tool-name">{card.name}</span>
              <span className="tool-status-label">
                {card.status === "running"
                  ? t("message.running")
                  : card.status === "error"
                    ? t("message.failed")
                    : card.status === "done"
                      ? t("message.done")
                      : ""}
              </span>
            </div>
            {card.outputs.length > 0 && (
              <details className="tool-output-details">
                <summary>{t("message.output")} ({lineCount} {lineCount === 1 ? t("message.line") : t("message.lines")})</summary>
                <pre className="tool-output">
                  {joined.slice(0, 2000)}
                  {joined.length > 2000 ? "\n..." : ""}
                </pre>
              </details>
            )}
            {card.media && (
              <MediaPreview type={card.media.type} url={card.media.url} onImageClick={onImageClick} />
            )}
          </motion.div>
        );
      })}

      {streaming && streamText && (
        <motion.div
          initial={{ opacity: 0, y: 10 }}
          animate={{ opacity: 1, y: 0 }}
          className="message assistant"
        >
          <div className="message-role">
            <div className="assistant-avatar">
              <SakerAvatar size={18} />
            </div>
          </div>
          <div className="message-content">
            <pre className="stream-pre">{streamText}<span className="streaming-cursor" /></pre>
          </div>
        </motion.div>
      )}

      <div ref={bottomRef} />

      <AnimatePresence>
        {showScrollBottom && (
          <motion.button
            initial={{ opacity: 0, scale: 0.8, y: 20 }}
            animate={{ opacity: 1, scale: 1, y: 0 }}
            exit={{ opacity: 0, scale: 0.8, y: 20 }}
            className="scroll-bottom-btn"
            onClick={scrollToBottom}
            aria-label={t("message.scrollToBottom")}
          >
            <ArrowDown size={18} />
          </motion.button>
        )}
      </AnimatePresence>

      {lightboxUrl && (
        <div className="lightbox-overlay" onClick={() => setLightboxUrl(null)}>
          <button
            className="lightbox-close"
            onClick={(e) => { e.stopPropagation(); setLightboxUrl(null); }}
          >
            <X size={20} />
          </button>
          <img
            src={lightboxUrl}
            alt={t("message.fullSizePreview")}
            className="lightbox-img"
            onClick={(e) => e.stopPropagation()}
          />
        </div>
      )}
    </div>
  );
}

function ToolGroupCollapse({
  tools,
  onImageClick,
}: {
  tools: ThreadItem[];
  onImageClick: (url: string) => void;
}) {
  const { t } = useT();
  const seenArtifactTypes = new Set<string>();
  const mediaItems: ThreadItem[] = [];
  const textOnly: ThreadItem[] = [];
  for (const tl of tools) {
    const arts = tl.artifacts?.filter((a) => {
      if (seenArtifactTypes.has(a.type)) return false;
      seenArtifactTypes.add(a.type);
      return true;
    });
    if (arts && arts.length > 0) {
      mediaItems.push({ ...tl, artifacts: arts });
    } else {
      textOnly.push(tl);
    }
  }
  const hasMedia = mediaItems.length > 0;

  return (
    <div className="tool-group">
      {mediaItems.map((item) => (
        <ToolItemCard key={item.id} item={item} onImageClick={onImageClick} />
      ))}
      {textOnly.length > 0 && (
        <details className="tool-group-details">
          <summary className="tool-group-summary">
            <ChevronDown size={12} />
            {textOnly.length} {textOnly.length === 1 ? t("message.toolCall") : t("message.toolCalls")}
            {hasMedia ? "" : ` ${t("message.completed")}`}
          </summary>
          <div className="tool-group-items">
            {textOnly.map((item) => (
              <ToolItemCard key={item.id} item={item} onImageClick={onImageClick} />
            ))}
          </div>
        </details>
      )}
    </div>
  );
}

function parseToolContent(content: string): { name: string; output: string } {
  const match = content.match(/^\[([^\]]+)\]\s*([\s\S]*)$/);
  if (match) {
    return { name: match[1], output: match[2] };
  }
  return { name: "Tool", output: content };
}

/** Try to extract a media reference from tool output text (images, videos, audio). */
function extractMediaFromText(text: string): { type: string; url: string } | null {
  if (!text) return null;
  const urlMatch = text.match(
    /https?:\/\/[^\s"']+\.(png|jpe?g|gif|webp|svg|mp4|webm|mp3|wav|ogg)(\?[^\s"']*)?/i
  );
  if (urlMatch) {
    const ext = urlMatch[1].toLowerCase();
    const t = /^(mp4|webm)$/.test(ext) ? "video" : /^(mp3|wav|ogg)$/.test(ext) ? "audio" : "image";
    return { type: t, url: urlMatch[0] };
  }
  const pathMatch = text.match(
    /\/[\w/._-]+\.(png|jpe?g|gif|webp|mp4|webm|mp3|wav|ogg)\b/i
  );
  if (pathMatch) {
    const ext = pathMatch[1].toLowerCase();
    const t = /^(mp4|webm)$/.test(ext) ? "video" : /^(mp3|wav|ogg)$/.test(ext) ? "audio" : "image";
    return { type: t, url: `/api/files${pathMatch[0]}` };
  }
  const relMatch = text.match(
    /(?:^|[\s"'=])([\w.][\w./_-]*\/[\w./_-]*\.(png|jpe?g|gif|webp|mp4|webm|mp3|wav|ogg))(?:\s|$|["'])/i
  );
  if (relMatch) {
    const ext = relMatch[2].toLowerCase();
    const t = /^(mp4|webm)$/.test(ext) ? "video" : /^(mp3|wav|ogg)$/.test(ext) ? "audio" : "image";
    return { type: t, url: `/api/files/${relMatch[1]}` };
  }
  return null;
}

function ToolItemCard({
  item,
  onImageClick,
}: {
  item: ThreadItem;
  onImageClick: (url: string) => void;
}) {
  const { t } = useT();

  if (!item.content && item.artifacts?.length) {
    return (
      <div className="message tool">
        {item.artifacts.map((a, i) => (
          <MediaPreview key={i} type={a.type} url={a.url} onImageClick={onImageClick} />
        ))}
      </div>
    );
  }

  const toolName = item.tool_name || parseToolContent(item.content).name;
  const output = item.tool_name
    ? item.content
    : parseToolContent(item.content).output;
  const hasArtifacts = item.artifacts && item.artifacts.length > 0;
  const lineCount = output ? output.split("\n").length : 0;
  const inferredMedia = !hasArtifacts ? extractMediaFromText(output) : null;

  return (
    <div className="tool-card">
      <div className="tool-card-header">
        <span className="tool-status-icon status-done" />
        <span className="tool-name">{toolName}</span>
        <span className="tool-status-label">{t("message.done")}</span>
      </div>
      {output && output.trim() && (
        <details className="tool-output-details">
          <summary>{t("message.output")} ({lineCount} {lineCount === 1 ? t("message.line") : t("message.lines")})</summary>
          <pre className="tool-output">
            {output.slice(0, 2000)}
            {output.length > 2000 ? "\n..." : ""}
          </pre>
        </details>
      )}
      {hasArtifacts &&
        item.artifacts!.map((a, i) => (
          <MediaPreview key={i} type={a.type} url={a.url} onImageClick={onImageClick} />
        ))}
      {inferredMedia && (
        <MediaPreview type={inferredMedia.type} url={inferredMedia.url} onImageClick={onImageClick} />
      )}
    </div>
  );
}

function MessageBubble({
  item,
  onImageClick,
}: {
  item: ThreadItem;
  onImageClick: (url: string) => void;
}) {
  const { t } = useT();
  const html = useMemo(() => renderMarkdown(item.content), [item.content]);
  const contentRef = useRef<HTMLDivElement>(null);
  const [collapsed, setCollapsed] = useState(false);
  const [isLong, setIsLong] = useState(false);

  useEffect(() => {
    if (item.role === "assistant" && contentRef.current) {
      setIsLong(contentRef.current.scrollHeight > 600);
    }
  }, [html, item.role]);

  const copyMessage = useCallback(() => {
    navigator.clipboard.writeText(item.content).then(() => {
      // visual feedback
    });
  }, [item.content]);

  // Handle Reasoning/Thinking Block (Custom detection for Saker)
  const isReasoning = item.role === "assistant" && item.content.includes("<thought>");
  const displayContent = isReasoning 
    ? item.content.replace(/<thought>([\s\S]*?)<\/thought>/g, "")
    : item.content;
  const thoughtText = isReasoning 
    ? item.content.match(/<thought>([\s\S]*?)<\/thought>/)?.[1]
    : null;

  return (
    <div
      className={`message ${item.role}`}
    >
      <div className="message-role">
        {item.role === "user" ? (
          <User size={16} />
        ) : (
          <div className="assistant-avatar">
            <SakerAvatar size={20} />
          </div>
        )}
      </div>

      <div className="message-body">
        {thoughtText && (
          <details className="thought-block-details">
            <summary className="thought-block-summary">
              <Brain size={14} style={{ color: 'var(--accent)' }} />
              <span>{t("message.thoughtProcess")}</span>
            </summary>
            <div className="thought-block-content">
              {thoughtText}
            </div>
          </details>
        )}

        <div
          ref={contentRef}
          className={`message-content${isLong && collapsed ? " content-collapsed" : ""}`}
          dangerouslySetInnerHTML={{ __html: renderMarkdown(displayContent) }}
        />
        
        {isLong && (
          <button
            className="content-toggle-btn"
            onClick={() => setCollapsed(!collapsed)}
          >
            {collapsed ? t("message.showMore") : t("message.showLess")}
          </button>
        )}
        
        {item.role === "assistant" && (
          <div className="message-actions">
            <button className="icon-btn-sm" onClick={copyMessage} title={t("message.copyMessage")}>
              <Copy size={14} />
            </button>
          </div>
        )}

        {item.artifacts?.map((a, i) => (
          <MediaPreview key={i} type={a.type} url={a.url} onImageClick={onImageClick} />
        ))}
      </div>
    </div>
  );
}

function MediaPreview({
  type,
  url,
  onImageClick,
}: {
  type: string;
  url: string;
  onImageClick?: (url: string) => void;
}) {
  const { t } = useT();
  const [loaded, setLoaded] = useState(false);
  const [error, setError] = useState(false);

  // Timeout fallback: if image hasn't loaded or errored within 8s, treat as error
  useEffect(() => {
    if (type !== "image" || loaded || error) return;
    const timer = setTimeout(() => {
      setError(true);
    }, 8000);
    return () => clearTimeout(timer);
  }, [type, loaded, error]);

  if (type === "image") {
    return (
      <div className="tool-media">
        {!loaded && !error && <div className="media-skeleton" />}
        {error ? (
          <div className="media-error">{t("message.imageFailedToLoad")}</div>
        ) : (
          <img
            src={url}
            alt={t("message.generatedImage")}
            onLoad={() => setLoaded(true)}
            onError={() => setError(true)}
            onClick={() => onImageClick?.(url)}
            style={{
              display: loaded ? "block" : "none",
              cursor: onImageClick ? "zoom-in" : undefined,
            }}
          />
        )}
      </div>
    );
  }
  if (type === "video") {
    return (
      <div className="tool-media">
        <video src={url} controls preload="metadata" />
      </div>
    );
  }
  if (type === "audio") {
    return (
      <div className="tool-media">
        <audio src={url} controls preload="metadata" />
      </div>
    );
  }
  return null;
}

/** Mini pixel-art Saker face avatar */
function SakerAvatar({ size = 20 }: { size?: number }) {
  return (
    <svg viewBox="0 0 128 128" width={size} height={size}>
      {/* Hair */}
      <rect x="20" y="8" width="16" height="16" rx="3" fill="currentColor" opacity="0.4"/>
      <rect x="36" y="4" width="16" height="22" rx="3" fill="currentColor" opacity="0.7"/>
      <rect x="56" y="0" width="16" height="26" rx="3" fill="currentColor"/>
      <rect x="76" y="4" width="16" height="22" rx="3" fill="currentColor" opacity="0.7"/>
      <rect x="92" y="8" width="16" height="16" rx="3" fill="currentColor" opacity="0.4"/>
      {/* Left eye */}
      <rect x="10" y="38" width="24" height="24" rx="5" fill="currentColor" opacity="0.8"/>
      <rect x="16" y="44" width="12" height="12" rx="12" fill="var(--bg, #1a1a1e)"/>
      <circle cx="22" cy="50" r="3.5" fill="currentColor"/>
      {/* Right eye */}
      <rect x="94" y="38" width="24" height="24" rx="5" fill="currentColor" opacity="0.8"/>
      <rect x="100" y="44" width="12" height="12" rx="12" fill="var(--bg, #1a1a1e)"/>
      <circle cx="106" cy="50" r="3.5" fill="currentColor"/>
      {/* Mouth */}
      <rect x="34" y="84" width="54" height="10" rx="3" fill="currentColor" opacity="0.8"/>
    </svg>
  );
}
