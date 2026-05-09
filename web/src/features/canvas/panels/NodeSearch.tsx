import { useState, useCallback, useRef, useEffect, useMemo } from "react";
import { Search, X, ChevronUp, ChevronDown } from "lucide-react";
import { useCanvasStore } from "../store";
import { useReactFlow } from "@xyflow/react";
import { useT } from "@/features/i18n";

export function NodeSearch() {
  const [open, setOpen] = useState(false);
  const { t } = useT();

  // Ctrl/Cmd+F to open
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key === "f") {
        const target = e.target as HTMLElement;
        if (target.tagName === "INPUT" || target.tagName === "TEXTAREA") return;
        e.preventDefault();
        setOpen(true);
      }
    };
    window.addEventListener("keydown", handler);
    return () => window.removeEventListener("keydown", handler);
  }, []);

  if (!open) return null;

  return <NodeSearchInner open={open} setOpen={setOpen} />;
}

function NodeSearchInner({ open, setOpen }: { open: boolean; setOpen: (v: boolean) => void }) {
  const { t } = useT();
  const [query, setQuery] = useState("");
  const [matchIdx, setMatchIdx] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);
  const nodes = useCanvasStore((s) => s.nodes);
  const setNodes = useCanvasStore((s) => s.setNodes);
  const { setCenter } = useReactFlow();

  const matches = useMemo(() => query.trim()
    ? nodes.filter((n) => {
        const label = (n.data.label || "").toLowerCase();
        const type = (n.type || "").toLowerCase();
        const q = query.toLowerCase();
        return label.includes(q) || type.includes(q);
      })
    : [], [nodes, query]);

  const focusMatch = useCallback(
    (idx: number) => {
      if (matches.length === 0) return;
      const safeIdx = ((idx % matches.length) + matches.length) % matches.length;
      setMatchIdx(safeIdx);
      const node = matches[safeIdx];
      setCenter(node.position.x + 140, node.position.y + 80, {
        zoom: 1,
        duration: 300,
      });
      // highlight matched node
      setNodes(
        nodes.map((n) => ({ ...n, selected: n.id === node.id }))
      );
    },
    [matches, nodes, setCenter, setNodes]
  );

  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if (e.key === "Escape" && open) {
        setOpen(false);
        setQuery("");
      }
    };
    window.addEventListener("keydown", handler);
    setTimeout(() => inputRef.current?.focus(), 50);
    return () => window.removeEventListener("keydown", handler);
  }, [open, setOpen]);

  return (
    <div className="canvas-search-bar">
      <Search size={14} className="canvas-search-icon" />
      <input
        ref={inputRef}
        className="canvas-search-input"
        placeholder={t("canvas.searchNodes")}
        value={query}
        onChange={(e) => {
          setQuery(e.target.value);
          setMatchIdx(0);
        }}
        onKeyDown={(e) => {
          if (e.key === "Enter") {
            e.preventDefault();
            focusMatch(e.shiftKey ? matchIdx - 1 : matchIdx + 1);
          }
        }}
      />
      {matches.length > 0 && (
        <span className="canvas-search-count">
          {matchIdx + 1}/{matches.length}
        </span>
      )}
      {matches.length > 0 && (
        <>
          <button className="canvas-search-nav" onClick={() => focusMatch(matchIdx - 1)}>
            <ChevronUp size={14} />
          </button>
          <button className="canvas-search-nav" onClick={() => focusMatch(matchIdx + 1)}>
            <ChevronDown size={14} />
          </button>
        </>
      )}
      <button
        className="canvas-search-close"
        onClick={() => {
          setOpen(false);
          setQuery("");
        }}
      >
        <X size={14} />
      </button>
    </div>
  );
}
