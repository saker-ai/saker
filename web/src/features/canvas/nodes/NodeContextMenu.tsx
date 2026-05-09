"use client";

import { useEffect, useState, useCallback } from "react";
import { Copy, Trash2, Download, ClipboardCopy, FolderHeart, Group, Bookmark } from "lucide-react";
import { useCanvasStore } from "../store";
import { useAssetStore } from "../panels/assetStore";
import { useTemplateStore, pickTemplateData } from "../panels/templateStore";
import { useT } from "@/features/i18n";
import type { CanvasNodeData } from "../types";

interface MenuPosition {
  x: number;
  y: number;
}

interface ContextMenuData {
  nodeId: string;
  position: MenuPosition;
  mediaUrl?: string;
  content?: string;
  label?: string;
}

export function NodeContextMenu() {
  const { t } = useT();
  const [menu, setMenu] = useState<ContextMenuData | null>(null);
  const removeNode = useCanvasStore((s) => s.removeNode);

  const close = useCallback(() => setMenu(null), []);

  useEffect(() => {
    const handler = (e: Event) => {
      setMenu((e as CustomEvent).detail as ContextMenuData);
    };
    window.addEventListener("canvas-contextmenu", handler);
    window.addEventListener("click", close);
    return () => {
      window.removeEventListener("canvas-contextmenu", handler);
      window.removeEventListener("click", close);
    };
  }, [close]);

  if (!menu) return null;

  const items: { icon: React.ReactNode; label: string; onClick: () => void }[] = [];

  if (menu.content) {
    items.push({
      icon: <ClipboardCopy size={14} />,
      label: t("canvas.copyContent"),
      onClick: () => {
        navigator.clipboard.writeText(menu.content!);
        close();
      },
    });
  }

  if (menu.mediaUrl) {
    items.push({
      icon: <Download size={14} />,
      label: t("canvas.download"),
      onClick: () => {
        const a = document.createElement("a");
        a.href = menu.mediaUrl!;
        a.download = menu.label || "download";
        a.click();
        close();
      },
    });
  }

  if (menu.mediaUrl) {
    const node = useCanvasStore.getState().nodes.find((n) => n.id === menu.nodeId);
    const nodeType = node?.type || "image";
    const mediaType = (nodeType === "video" ? "video" : nodeType === "audio" ? "audio" : "image") as "image" | "video" | "audio";
    items.push({
      icon: <FolderHeart size={14} />,
      label: t("canvas.saveToLibrary"),
      onClick: () => {
        useAssetStore.getState().addAsset({
          type: mediaType,
          url: menu.mediaUrl!,
          label: menu.label || "Untitled",
        });
        close();
      },
    });
  }

  items.push({
    icon: <Copy size={14} />,
    label: t("canvas.duplicate"),
    onClick: () => {
      const store = useCanvasStore.getState();
      const node = store.nodes.find((n) => n.id === menu.nodeId);
      if (node) {
        store.addNode({
          ...node,
          id: undefined,
          position: { x: node.position.x + 30, y: node.position.y + 30 },
        });
      }
      close();
    },
  });

  // Save as template — only for generator nodes where config presets matter
  const currentNode = useCanvasStore.getState().nodes.find((n) => n.id === menu.nodeId);
  const nt = currentNode?.data.nodeType;
  if (currentNode && (nt === "imageGen" || nt === "videoGen" || nt === "voiceGen")) {
    items.push({
      icon: <Bookmark size={14} />,
      label: t("canvas.saveAsTemplate" as any),
      onClick: () => {
        const d = currentNode.data as CanvasNodeData;
        const name = window.prompt(t("canvas.templateNamePrompt" as any), d.label || nt) || "";
        if (!name.trim()) { close(); return; }
        useTemplateStore.getState().addTemplate({
          name: name.trim(),
          nodeType: nt,
          data: pickTemplateData(d),
        });
        close();
      },
    });
  }

  // Group selected nodes
  const selectedNodes = useCanvasStore.getState().nodes.filter((n) => n.selected);
  if (selectedNodes.length >= 2) {
    items.push({
      icon: <Group size={14} />,
      label: t("canvas.groupSelected"),
      onClick: () => {
        useCanvasStore.getState().groupNodes(selectedNodes.map((n) => n.id));
        close();
      },
    });
  }

  items.push({
    icon: <Trash2 size={14} />,
    label: t("canvas.delete"),
    onClick: () => {
      removeNode(menu.nodeId);
      close();
    },
  });

  return (
    <div
      className="canvas-context-menu"
      style={{ left: menu.position.x, top: menu.position.y }}
      onClick={(e) => e.stopPropagation()}
    >
      {items.map((item, i) => (
        <button key={i} className="canvas-context-menu-item" onClick={item.onClick}>
          {item.icon}
          <span>{item.label}</span>
        </button>
      ))}
    </div>
  );
}
