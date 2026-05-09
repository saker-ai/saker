import { useCallback, useMemo, useRef } from "react";
import { useCanvasStore } from "../store";
import { cacheCanvasMedia } from "../mediaCache";

export function useManuscriptImages(
  nodeId: string,
  fullContent: string,
  updateFullContent: (content: string) => void,
) {
  const nodes = useCanvasStore((s) => s.nodes);
  const markdownRef = useRef<HTMLTextAreaElement | null>(null);
  const imageInputRef = useRef<HTMLInputElement | null>(null);

  const selectedCanvasImages = useMemo(
    () =>
      nodes.filter((node) =>
        node.id !== nodeId &&
        node.selected === true &&
        node.type === "image" &&
        typeof node.data?.mediaUrl === "string" &&
        node.data.mediaUrl,
      ),
    [nodeId, nodes],
  );

  const insertMarkdownAtCursor = useCallback((snippet: string) => {
    const textarea = markdownRef.current;
    if (!textarea) {
      updateFullContent(`${fullContent}${fullContent ? "\n\n" : ""}${snippet}`);
      return;
    }
    const start = textarea.selectionStart ?? fullContent.length;
    const end = textarea.selectionEnd ?? fullContent.length;
    const next = `${fullContent.slice(0, start)}${snippet}${fullContent.slice(end)}`;
    updateFullContent(next);
    requestAnimationFrame(() => {
      textarea.focus();
      const cursor = start + snippet.length;
      textarea.setSelectionRange(cursor, cursor);
    });
  }, [fullContent, updateFullContent]);

  const insertImageUrl = useCallback(() => {
    const url = window.prompt("输入图片 URL");
    if (!url?.trim()) return;
    const markdown = `![image](${url.trim()})`;
    insertMarkdownAtCursor(markdown);
  }, [insertMarkdownAtCursor]);

  const handleImageFile = useCallback(async (file: File) => {
    if (!file.type.startsWith("image/")) return;
    if (file.size > 50 * 1024 * 1024) return;

    const dataUrl = await new Promise<string>((resolve, reject) => {
      const reader = new FileReader();
      reader.onload = () => resolve(reader.result as string);
      reader.onerror = reject;
      reader.readAsDataURL(file);
    });
    const stabilized = await cacheCanvasMedia(dataUrl, "image");
    const finalUrl = stabilized.mediaUrl || dataUrl;
    const alt = file.name.replace(/\.[^.]+$/, "") || "image";
    insertMarkdownAtCursor(`![${alt}](${finalUrl})`);
  }, [insertMarkdownAtCursor]);

  const insertSelectedCanvasImage = useCallback(() => {
    const image = selectedCanvasImages[0];
    if (!image) return;
    const url = String(image.data?.mediaUrl || "");
    if (!url) return;
    const label = String(image.data?.label || "canvas-image");
    insertMarkdownAtCursor(`![${label}](${url})`);
  }, [insertMarkdownAtCursor, selectedCanvasImages]);

  const handleMarkdownPaste = useCallback((event: React.ClipboardEvent<HTMLTextAreaElement>) => {
    const items = event.clipboardData.items;
    for (const item of items) {
      if (item.type.startsWith("image/")) {
        event.preventDefault();
        const file = item.getAsFile();
        if (file) void handleImageFile(file);
        return;
      }
    }
    const text = event.clipboardData.getData("text/plain").trim();
    if (!/^https?:\/\/\S+\.(png|jpe?g|gif|webp|svg)(\?\S*)?$/i.test(text)) return;
    event.preventDefault();
    insertMarkdownAtCursor(`![image](${text})`);
  }, [handleImageFile, insertMarkdownAtCursor]);

  const handleMarkdownDrop = useCallback((event: React.DragEvent<HTMLTextAreaElement>) => {
    const file = event.dataTransfer.files?.[0];
    if (!file || !file.type.startsWith("image/")) return;
    event.preventDefault();
    void handleImageFile(file);
  }, [handleImageFile]);

  return {
    markdownRef,
    imageInputRef,
    selectedCanvasImages,
    insertImageUrl,
    handleImageFile,
    insertSelectedCanvasImage,
    handleMarkdownPaste,
    handleMarkdownDrop,
  };
}
