// Shared helper used by both ManuscriptEditorOverlay (right-side button) and
// AITypoNode (NodeToolbar action). Creates an empty table node next to the
// source manuscript node, wires a flow edge, and dispatches the standard
// `manuscript-ai-command` event so ChatApp's existing pipeline drives the
// agent through canvas_table_write(replace, node_id=<new table id>).
//
// The new table node carries `tablePendingExtract: true` so TableNode can
// show a "loading" placeholder instead of seeding default columns. The flag
// is cleared by TableNode the first time real columns arrive.

import { useCanvasStore } from "./store";

interface ExtractOptions {
  nodeId: string;
  manuscriptTitle?: string;
  /** Full manuscript text (sections joined with \n\n, or raw markdown). */
  fullContent: string;
}

const EMPTY_RESULT = null;

export function extractManuscriptToTable(opts: ExtractOptions): string | null {
  const text = (opts.fullContent || "").trim();
  if (!text) return EMPTY_RESULT;

  const state = useCanvasStore.getState();
  const source = state.nodes.find((n) => n.id === opts.nodeId);
  if (!source) return EMPTY_RESULT;

  const width = source.measured?.width || 280;
  state.commitHistory();

  const tableTitle = (opts.manuscriptTitle || "文稿").trim() || "文稿";
  const tableId = state.addNode({
    type: "table",
    position: { x: source.position.x + width + 80, y: source.position.y },
    data: {
      nodeType: "table",
      label: `${tableTitle} · 表格`,
      status: "running",
      tableTitle: `${tableTitle} 拆解`,
      tableColumns: [],
      tableRows: [],
      // Tells TableNode to skip the default-column bootstrap and show a
      // loading placeholder until the agent's canvas_table_write lands.
      tablePendingExtract: true,
      // Persist when the extract was kicked off so a page reload doesn't
      // restart the 60s "still loading?" timer from zero — the user would
      // otherwise see the spinner re-appear with no actual request in flight.
      tablePendingExtractStartedAt: Date.now(),
    },
  });
  state.addEdge({
    id: `edge-${opts.nodeId}-${tableId}`,
    source: opts.nodeId,
    target: tableId,
    type: "flow",
  });

  const promptText = [
    `请把下方灵动文稿智能拆解成一张多维表格，并写入新建的表格节点 \`${tableId}\`。`,
    `要求：`,
    `1. 使用 canvas_table_write 工具，operation=replace，一次性写入 columns 和 rows。`,
    `2. 如果文稿很短（少于一段，或本身缺乏结构性内容），就只生成 1 列 + 1 行（列名 "内容"，type=longText）即可，不要为了凑列数硬拆。`,
    `3. 否则自行设计 3-6 个有意义的列。例如：分镜稿用 场景/动作/台词/镜头；文章用 标题/要点/受众/CTA；任务清单用 步骤/责任人/状态。列 id 用 col_1..colN；列 type 在 text/longText/number/select 里挑最合适的。`,
    `4. 行 id 用 row_1..rowM；每一行对应一段 / 一个分镜 / 一个章节；单格内容控制在 200 字符以内。`,
    `5. 写完后用一两句话总结你做了哪些列、多少行，不要把整张表格再贴一遍。`,
    ``,
    `文稿全文：`,
    text,
  ].join("\n");

  window.dispatchEvent(
    new CustomEvent("manuscript-ai-command", {
      detail: {
        prompt: promptText,
        branchNodeId: opts.nodeId,
        nodeId: opts.nodeId,
        scope: "document",
        sourceText: text,
        // If turn/send fails (network error, server reject, etc.), the chat
        // handler should remove this newly-created empty table node so the
        // user doesn't see a leftover orphan + dangling edge on the canvas.
        cleanupTableNodeId: tableId,
      },
    }),
  );

  return tableId;
}
