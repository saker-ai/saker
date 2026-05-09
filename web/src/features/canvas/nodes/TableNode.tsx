import { memo, useCallback, useMemo, useRef, useState, useEffect } from "react";
import { Handle, Position, type NodeProps } from "@xyflow/react";
import { Table as TableIcon, Plus, Trash2, Type, Hash, AlignLeft, ListChecks, Loader2, AlertTriangle, RotateCw, X as XIcon } from "lucide-react";
import type {
  CanvasNodeData,
  TableColumn,
  TableColumnType,
  TableRow,
  TableCellValue,
} from "../types";
import { LockToggle } from "./LockToggle";
import { useT } from "@/features/i18n";
import { useCanvasStore } from "../store";
import { extractManuscriptToTable } from "../extractToTable";

const PENDING_EXTRACT_TIMEOUT_MS = 60_000;

// Default schema used when a freshly added table node has no columns/rows yet.
// Three text columns matches a typical "split a script into beats" baseline:
// users can rename or change column types from the header context menu later.
const DEFAULT_COLUMNS: TableColumn[] = [
  { id: "col_1", name: "标题", type: "text" },
  { id: "col_2", name: "内容", type: "longText" },
  { id: "col_3", name: "备注", type: "text" },
];

const ID_RE = /^[A-Za-z0-9_-]+$/;

function nextID(prefix: "col" | "row", existing: { id: string }[]): string {
  const used = new Set(existing.map((x) => x.id));
  let i = existing.length + 1;
  while (used.has(`${prefix}_${i}`)) i++;
  return `${prefix}_${i}`;
}

function ColumnTypeIcon({ type }: { type: TableColumnType }) {
  switch (type) {
    case "number":
      return <Hash size={11} />;
    case "longText":
      return <AlignLeft size={11} />;
    case "select":
      return <ListChecks size={11} />;
    case "text":
    default:
      return <Type size={11} />;
  }
}

interface CellEditorProps {
  column: TableColumn;
  value: TableCellValue | undefined;
  onCommit: (next: TableCellValue) => void;
}

// CellEditor is intentionally uncontrolled at the input level (local draft
// state) so React Flow re-renders triggered by other store mutations don't
// nuke the cursor / IME composition. The store only sees the value on blur
// or Enter, which is also when commitHistory snapshots fire.
function CellEditor({ column, value, onCommit }: CellEditorProps) {
  const [draft, setDraft] = useState(() => (value == null ? "" : String(value)));

  useEffect(() => {
    setDraft(value == null ? "" : String(value));
  }, [value]);

  const commit = useCallback(
    (raw: string) => {
      let next: TableCellValue = raw;
      if (column.type === "number") {
        const trimmed = raw.trim();
        if (trimmed === "") {
          next = null;
        } else {
          const n = Number(trimmed);
          next = Number.isNaN(n) ? raw : n;
        }
      }
      if (raw === (value == null ? "" : String(value))) return;
      onCommit(next);
    },
    [column.type, value, onCommit]
  );

  if (column.type === "select") {
    const opts = column.options ?? [];
    return (
      <select
        className="canvas-table-cell-input"
        value={draft}
        onChange={(e) => {
          setDraft(e.target.value);
          onCommit(e.target.value || null);
        }}
        onClick={(e) => e.stopPropagation()}
      >
        <option value=""></option>
        {opts.map((opt) => (
          <option key={opt} value={opt}>
            {opt}
          </option>
        ))}
      </select>
    );
  }

  if (column.type === "longText") {
    return (
      <textarea
        className="canvas-table-cell-input canvas-table-cell-multi"
        rows={2}
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        onBlur={() => commit(draft)}
        onClick={(e) => e.stopPropagation()}
        onKeyDown={(e) => {
          if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
            e.preventDefault();
            (e.currentTarget as HTMLTextAreaElement).blur();
          }
        }}
      />
    );
  }

  return (
    <input
      className="canvas-table-cell-input"
      type={column.type === "number" ? "number" : "text"}
      value={draft}
      onChange={(e) => setDraft(e.target.value)}
      onBlur={() => commit(draft)}
      onClick={(e) => e.stopPropagation()}
      onKeyDown={(e) => {
        if (e.key === "Enter") {
          e.preventDefault();
          (e.currentTarget as HTMLInputElement).blur();
        }
      }}
    />
  );
}

export const TableNode = memo(function TableNode({ id, data, selected }: NodeProps) {
  const d = data as CanvasNodeData;
  const { t } = useT();
  const updateNode = useCanvasStore((s) => s.updateNode);
  const commitHistory = useCanvasStore((s) => s.commitHistory);
  const initRef = useRef(false);

  const columns = useMemo<TableColumn[]>(() => d.tableColumns ?? [], [d.tableColumns]);
  const rows = useMemo<TableRow[]>(() => d.tableRows ?? [], [d.tableRows]);

  // First-render bootstrap: a freshly created table node has no schema yet.
  // We seed the default columns once and write back so the persisted JSON
  // stays consistent with what the user sees.
  // Exception: when tablePendingExtract is true, the node was just created by
  // the manuscript "拆成多维表格" flow and we're awaiting an agent write —
  // skip seeding so the loading placeholder shows instead of stale defaults.
  useEffect(() => {
    if (initRef.current) return;
    initRef.current = true;
    if (d.tablePendingExtract) return;
    if (!d.tableColumns || d.tableColumns.length === 0) {
      updateNode(id, {
        tableColumns: DEFAULT_COLUMNS,
        tableRows: rows.length > 0 ? rows : [],
      });
    }
  }, [id, d.tableColumns, d.tablePendingExtract, rows.length, updateNode, rows]);

  // Once the agent's canvas_table_write lands (columns become non-empty),
  // clear the pending flag so subsequent edits behave normally.
  useEffect(() => {
    if (d.tablePendingExtract && columns.length > 0) {
      updateNode(id, { tablePendingExtract: false, tablePendingExtractStartedAt: undefined, status: "done" });
    }
  }, [id, d.tablePendingExtract, columns.length, updateNode]);

  // Track elapsed time + timeout for the extract spinner. Anchor the elapsed
  // calculation on the persisted `tablePendingExtractStartedAt` timestamp so a
  // page reload during an already-stuck extract goes straight to the error
  // state instead of pretending a fresh request just kicked off.
  const startedAt = d.tablePendingExtractStartedAt ?? 0;
  const [extractElapsedMs, setExtractElapsedMs] = useState(() =>
    startedAt > 0 ? Math.max(0, Date.now() - startedAt) : 0,
  );
  const [extractTimedOut, setExtractTimedOut] = useState(() =>
    startedAt > 0 && Date.now() - startedAt >= PENDING_EXTRACT_TIMEOUT_MS,
  );
  useEffect(() => {
    if (!d.tablePendingExtract || columns.length > 0) {
      setExtractElapsedMs(0);
      setExtractTimedOut(false);
      return;
    }
    const anchor = startedAt > 0 ? startedAt : Date.now();
    const update = () => {
      const elapsed = Math.max(0, Date.now() - anchor);
      setExtractElapsedMs(elapsed);
      if (elapsed >= PENDING_EXTRACT_TIMEOUT_MS) setExtractTimedOut(true);
    };
    update();
    const tick = setInterval(update, 500);
    return () => clearInterval(tick);
  }, [d.tablePendingExtract, columns.length, startedAt]);

  const retryExtract = useCallback(() => {
    const state = useCanvasStore.getState();
    const incoming = state.edges.find((edge) => edge.target === id);
    if (!incoming) {
      setExtractTimedOut(false);
      setExtractElapsedMs(0);
      return;
    }
    const source = state.nodes.find((node) => node.id === incoming.source);
    const sourceData = (source?.data ?? {}) as CanvasNodeData;
    const fullContent = (sourceData.content || "").trim();
    if (!fullContent) {
      setExtractTimedOut(false);
      setExtractElapsedMs(0);
      return;
    }
    setExtractTimedOut(false);
    setExtractElapsedMs(0);
    extractManuscriptToTable({
      nodeId: incoming.source,
      manuscriptTitle: sourceData.manuscriptTitle,
      fullContent,
    });
  }, [id]);

  const cancelExtract = useCallback(() => {
    setExtractTimedOut(false);
    setExtractElapsedMs(0);
    updateNode(id, {
      tablePendingExtract: false,
      tablePendingExtractStartedAt: undefined,
      tableColumns: DEFAULT_COLUMNS,
      status: "done",
    });
  }, [id, updateNode]);

  const setCell = useCallback(
    (rowID: string, colID: string, value: TableCellValue) => {
      const nextRows = rows.map((r) =>
        r.id === rowID ? ({ ...r, [colID]: value } as TableRow) : r
      );
      updateNode(id, { tableRows: nextRows });
    },
    [rows, updateNode, id]
  );

  const addColumn = useCallback(() => {
    commitHistory();
    const nextID_ = nextID("col", columns);
    const next: TableColumn = { id: nextID_, name: `列 ${columns.length + 1}`, type: "text" };
    updateNode(id, { tableColumns: [...columns, next] });
  }, [columns, updateNode, commitHistory, id]);

  const renameColumn = useCallback(
    (colID: string, name: string) => {
      const nextCols = columns.map((c) => (c.id === colID ? { ...c, name } : c));
      updateNode(id, { tableColumns: nextCols });
    },
    [columns, updateNode, id]
  );

  const cycleColumnType = useCallback(
    (colID: string) => {
      commitHistory();
      const order: TableColumnType[] = ["text", "longText", "number", "select"];
      const nextCols = columns.map((c) => {
        if (c.id !== colID) return c;
        const idx = Math.max(0, order.indexOf(c.type));
        const nextType = order[(idx + 1) % order.length];
        const out: TableColumn = { ...c, type: nextType };
        if (nextType === "select" && (!c.options || c.options.length === 0)) {
          out.options = ["选项 A", "选项 B"];
        }
        return out;
      });
      updateNode(id, { tableColumns: nextCols });
    },
    [columns, updateNode, commitHistory, id]
  );

  const deleteColumn = useCallback(
    (colID: string) => {
      if (columns.length <= 1) return;
      commitHistory();
      const nextCols = columns.filter((c) => c.id !== colID);
      const nextRows = rows.map((r) => {
        const out = { ...r };
        delete (out as Record<string, unknown>)[colID];
        return out as TableRow;
      });
      updateNode(id, { tableColumns: nextCols, tableRows: nextRows });
    },
    [columns, rows, updateNode, commitHistory, id]
  );

  const addRow = useCallback(() => {
    commitHistory();
    const newRow: TableRow = { id: nextID("row", rows) };
    updateNode(id, { tableRows: [...rows, newRow] });
  }, [rows, updateNode, commitHistory, id]);

  const deleteRow = useCallback(
    (rowID: string) => {
      commitHistory();
      updateNode(id, { tableRows: rows.filter((r) => r.id !== rowID) });
    },
    [rows, updateNode, commitHistory, id]
  );

  const setTitle = useCallback(
    (next: string) => {
      updateNode(id, { tableTitle: next });
    },
    [updateNode, id]
  );

  const handleContextMenu = useCallback(
    (e: React.MouseEvent) => {
      e.preventDefault();
      window.dispatchEvent(
        new CustomEvent("canvas-contextmenu", {
          detail: { nodeId: id, position: { x: e.clientX, y: e.clientY }, label: d.label },
        })
      );
    },
    [id, d.label]
  );

  return (
    <div
      className={`canvas-node canvas-node-table ${selected ? "selected" : ""}`}
      onContextMenu={handleContextMenu}
      role="article"
      aria-label={d.label || "Table"}
      style={{ minWidth: 360 }}
    >
      <div className="canvas-node-header">
        <div className="canvas-node-icon-wrapper">
          <TableIcon size={14} />
        </div>
        <input
          className="canvas-table-title"
          value={d.tableTitle ?? d.label ?? ""}
          placeholder={t("canvas.table" as any) || "Table"}
          onChange={(e) => setTitle(e.target.value)}
          onClick={(e) => e.stopPropagation()}
        />
        <LockToggle nodeId={id} locked={d.locked} />
      </div>

      <div className="canvas-node-body canvas-table-body">
        {d.tablePendingExtract && columns.length === 0 ? (
          extractTimedOut ? (
            <div className="canvas-table-loading canvas-table-loading-error">
              <AlertTriangle size={18} className="canvas-table-loading-error-icon" />
              <span>智能拆解超时（{Math.floor(extractElapsedMs / 1000)}s 仍无返回）</span>
              <small>可能是模型未调用 canvas_table_write，或 Copilot 链路异常。</small>
              <div className="canvas-table-loading-actions">
                <button
                  type="button"
                  className="canvas-table-loading-retry"
                  onClick={(e) => { e.stopPropagation(); retryExtract(); }}
                  title="重新派发拆表请求"
                >
                  <RotateCw size={13} />
                  重试
                </button>
                <button
                  type="button"
                  className="canvas-table-loading-cancel"
                  onClick={(e) => { e.stopPropagation(); cancelExtract(); }}
                  title="改成空白表格继续手动编辑"
                >
                  <XIcon size={13} />
                  取消，手动编辑
                </button>
              </div>
            </div>
          ) : (
            <div className="canvas-table-loading">
              <Loader2 size={18} className="canvas-table-loading-spin" />
              <span>正在智能拆解文稿… {Math.floor(extractElapsedMs / 1000)}s</span>
              <small>稍后表格列与内容会自动填入</small>
            </div>
          )
        ) : (
        <>
        <div className="canvas-table-grid">
          <div className="canvas-table-row canvas-table-head">
            {columns.map((col) => (
              <div key={col.id} className="canvas-table-cell canvas-table-headcell">
                <button
                  className="canvas-table-typebtn"
                  onClick={(e) => {
                    e.stopPropagation();
                    cycleColumnType(col.id);
                  }}
                  title={col.type}
                >
                  <ColumnTypeIcon type={col.type} />
                </button>
                <input
                  className="canvas-table-colname"
                  value={col.name}
                  onChange={(e) => renameColumn(col.id, e.target.value)}
                  onClick={(e) => e.stopPropagation()}
                />
                {columns.length > 1 && (
                  <button
                    className="canvas-table-delcol"
                    onClick={(e) => {
                      e.stopPropagation();
                      deleteColumn(col.id);
                    }}
                    title={t("canvas.tableDeleteCol" as any) || "Delete column"}
                  >
                    <Trash2 size={11} />
                  </button>
                )}
              </div>
            ))}
            <button
              className="canvas-table-addcol"
              onClick={(e) => {
                e.stopPropagation();
                addColumn();
              }}
              title={t("canvas.tableAddCol" as any) || "Add column"}
            >
              <Plus size={12} />
            </button>
          </div>

          {rows.length === 0 && (
            <div className="canvas-table-empty">
              {t("canvas.tableEmpty" as any) || "No rows yet"}
            </div>
          )}

          {rows.map((row) => (
            <div key={row.id} className="canvas-table-row">
              {columns.map((col) => (
                <div key={col.id} className="canvas-table-cell">
                  <CellEditor
                    column={col}
                    value={(row as Record<string, TableCellValue | undefined>)[col.id]}
                    onCommit={(v) => setCell(row.id, col.id, v)}
                  />
                </div>
              ))}
              <button
                className="canvas-table-delrow"
                onClick={(e) => {
                  e.stopPropagation();
                  deleteRow(row.id);
                }}
                title={t("canvas.tableDeleteRow" as any) || "Delete row"}
              >
                <Trash2 size={11} />
              </button>
            </div>
          ))}
        </div>

        <button
          className="canvas-table-addrow"
          onClick={(e) => {
            e.stopPropagation();
            addRow();
          }}
        >
          <Plus size={12} /> {t("canvas.tableAddRow" as any) || "Add row"}
        </button>
        </>
        )}
      </div>

      <Handle type="target" position={Position.Left} className="canvas-handle" />
      <Handle type="source" position={Position.Right} className="canvas-handle" />
    </div>
  );
});

// Surface ID validator for tests / future agent-side helpers; matches the
// canvasIDPattern used server-side in pkg/tool/builtin/canvas.go.
export const __TableNode_ID_RE = ID_RE;
