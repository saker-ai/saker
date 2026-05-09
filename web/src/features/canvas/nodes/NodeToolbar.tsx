import { Download, Maximize2, Copy, Trash2, Eye, CopyPlus } from "lucide-react";
import { useCanvasStore } from "../store";

interface ToolbarAction {
  icon: React.ReactNode;
  label: string;
  onClick: () => void;
}

interface NodeToolbarProps {
  nodeId: string;
  selected?: boolean;
  actions?: ToolbarAction[];
}

function downloadUrl(url: string, filename: string) {
  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  a.click();
}

function copyToClipboard(text: string) {
  navigator.clipboard.writeText(text);
}

export function NodeToolbar({ nodeId, selected, actions = [] }: NodeToolbarProps) {
  const removeNode = useCanvasStore((s) => s.removeNode);
  const addNode = useCanvasStore((s) => s.addNode);

  if (!selected) return null;

  const duplicateNode = () => {
    const node = useCanvasStore.getState().nodes.find((n) => n.id === nodeId);
    if (!node) return;
    addNode({
      type: node.type,
      position: { x: node.position.x + 40, y: node.position.y + 40 },
      data: { ...node.data },
    });
  };

  const allActions: ToolbarAction[] = [
    ...actions,
    {
      icon: <CopyPlus size={13} />,
      label: "Duplicate",
      onClick: duplicateNode,
    },
    {
      icon: <Trash2 size={13} />,
      label: "Delete",
      onClick: () => removeNode(nodeId),
    },
  ];

  return (
    <div className="canvas-node-toolbar" onClick={(e) => e.stopPropagation()}>
      {allActions.map((action) => (
        <button
          key={action.label}
          className="canvas-node-toolbar-btn"
          title={action.label}
          onClick={(e) => {
            e.stopPropagation();
            action.onClick();
          }}
        >
          {action.icon}
        </button>
      ))}
    </div>
  );
}

/** Pre-built action sets for common node types */
export function getMediaActions(mediaUrl?: string, label?: string, type: "image" | "video" = "image") {
  const actions: ToolbarAction[] = [];
  if (mediaUrl) {
    actions.push({
      icon: <Download size={13} />,
      label: "Download",
      onClick: () => downloadUrl(mediaUrl, label || "download"),
    });
    actions.push({
      icon: <Maximize2 size={13} />,
      label: "Fullscreen",
      onClick: () => {
        window.dispatchEvent(
          new CustomEvent("canvas-preview", { detail: { url: mediaUrl, type, label } })
        );
      },
    });
  }
  return actions;
}

export function getTextActions(content?: string) {
  const actions: ToolbarAction[] = [];
  if (content) {
    actions.push({
      icon: <Copy size={13} />,
      label: "Copy",
      onClick: () => copyToClipboard(content),
    });
  }
  return actions;
}

export function getDetailActions(content?: string) {
  const actions: ToolbarAction[] = [];
  if (content) {
    actions.push({
      icon: <Eye size={13} />,
      label: "Detail",
      onClick: () => {
        window.dispatchEvent(
          new CustomEvent("canvas-preview", { detail: { text: content, type: "text" } })
        );
      },
    });
    actions.push({
      icon: <Copy size={13} />,
      label: "Copy",
      onClick: () => copyToClipboard(content),
    });
  }
  return actions;
}
