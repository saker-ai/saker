import { Image, Video, Music, Pencil, Sparkles, Brush, X, Brain, Layers, Link2, Download, Table, LogIn, LogOut } from "lucide-react";
import { useT } from "@/features/i18n";
import type { CanvasNodeType } from "../types";

export type QuickAddType = CanvasNodeType | "imageEdit" | "videoEdit";

interface QuickAddMenuProps {
  onSelect: (type: QuickAddType) => void;
  onClose: () => void;
  style?: React.CSSProperties;
  className?: string;
}

const ITEMS: Array<{ type: QuickAddType; icon: React.ReactNode; labelKey: string }> = [
  // --- Creation / Generation ---
  { type: "sketch", icon: <Brush size={16} />, labelKey: "canvas.sketch" },
  { type: "textGen", icon: <Sparkles size={16} />, labelKey: "canvas.textGen" },
  { type: "imageGen", icon: <Image size={16} />, labelKey: "canvas.imageGen" },
  { type: "voiceGen", icon: <Music size={16} />, labelKey: "canvas.audioGen" },
  { type: "videoGen", icon: <Video size={16} />, labelKey: "canvas.videoGen" },
  
  // --- Editing / Enhancement ---
  { type: "imageEdit", icon: <Pencil size={16} />, labelKey: "canvas.imageEditNode" },
  { type: "videoEdit", icon: <Pencil size={16} />, labelKey: "canvas.videoEditNode" },

  // --- Advanced workflow nodes ---
  { type: "llm", icon: <Brain size={16} />, labelKey: "canvas.llm" },
  { type: "mask", icon: <Layers size={16} />, labelKey: "canvas.mask" },
  { type: "reference", icon: <Link2 size={16} />, labelKey: "canvas.reference" },
  { type: "export", icon: <Download size={16} />, labelKey: "canvas.export" },

  // --- Structured data ---
  { type: "table", icon: <Table size={16} />, labelKey: "canvas.table" },

  // --- App I/O ---
  { type: "appInput", icon: <LogIn size={16} />, labelKey: "canvas.appInputLabel" },
  { type: "appOutput", icon: <LogOut size={16} />, labelKey: "canvas.appOutputLabel" },
];

export function QuickAddMenu({ onSelect, onClose, style, className }: QuickAddMenuProps) {
  const { t } = useT();
  return (
    <div className={className} style={style}>
      <div className="canvas-add-menu-header">
        <span>{t("canvas.addNode")}</span>
        <button className="canvas-add-menu-close" onClick={onClose}>
          <X size={14} />
        </button>
      </div>
      {ITEMS.map((item) => (
        <button
          key={item.type}
          className="canvas-add-menu-item"
          onClick={() => onSelect(item.type)}
        >
          {item.icon}
          <span>{t(item.labelKey as any)}</span>
        </button>
      ))}
    </div>
  );
}
