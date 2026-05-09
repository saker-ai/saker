import { useCallback, useMemo, useState } from "react";
import { createPortal } from "react-dom";
import { Plus, Trash2, X, RotateCcw, RotateCw, Minimize2, Eye, Pencil, FileCode, Blocks, ImagePlus, Link2, Upload, Sparkles, Table as TableIcon } from "lucide-react";
import DOMPurify from "dompurify";
import type { CanvasNodeData, ManuscriptSectionType } from "../types";
import { useManuscriptDraft } from "../hooks/useManuscriptDraft";
import { useManuscriptImages } from "../hooks/useManuscriptImages";
import { useManuscriptCopilot } from "../hooks/useManuscriptCopilot";
import { extractManuscriptToTable } from "../extractToTable";

interface ManuscriptEditorOverlayProps {
  nodeId: string;
  data: CanvasNodeData;
  onClose: () => void;
}

interface SelectionAction {
  label: string;
  prompt: (text: string) => string;
}

const SELECTION_ACTIONS: SelectionAction[] = [
  { label: "扩写", prompt: (text) => `请扩写下面这段灵动文稿内容，并保持原意：\n\n${text}` },
  { label: "精简", prompt: (text) => `请精简下面这段灵动文稿内容，保留核心画面和情绪：\n\n${text}` },
  { label: "更电影感", prompt: (text) => `请把下面这段内容改写得更有电影感、更适合镜头表达：\n\n${text}` },
  { label: "生成提示词", prompt: (text) => `请把下面这段文稿转换成适合图像生成的高质量 prompt：\n\n${text}` },
];

const COPILOT_PROMPTS = [
  "把这篇文稿改得更有电影感",
  "把全文提炼成图像生成提示词",
  "找出最适合拆成节点的段落",
  "给我一个更商业化版本",
] as const;

const SECTION_TYPES: Array<{ value: ManuscriptSectionType; label: string }> = [
  { value: "heading", label: "标题" },
  { value: "paragraph", label: "正文" },
  { value: "quote", label: "引用" },
  { value: "list", label: "列表" },
  { value: "note", label: "注释" },
];

export function ManuscriptEditorOverlay({ nodeId, data, onClose }: ManuscriptEditorOverlayProps) {
  const {
    draft, past, future, editorMode, fullContent, previewHtml,
    updateTitle, updateSummary, updateSection, addSection, removeSection,
    updateFullContent, renameEntity, switchEditorMode, undo, redo,
  } = useManuscriptDraft(nodeId, data);

  const {
    markdownRef, imageInputRef, selectedCanvasImages,
    insertImageUrl, handleImageFile, insertSelectedCanvasImage,
    handleMarkdownPaste, handleMarkdownDrop,
  } = useManuscriptImages(nodeId, fullContent, updateFullContent);

  const [previewMode, setPreviewMode] = useState(true);
  const [selectedText, setSelectedText] = useState("");
  const [activeEntityId, setActiveEntityId] = useState<string | null>(null);

  const {
    copilotInput, setCopilotInput, copilotScope, setCopilotScope,
    proposal, dispatchAiCommand, applyProposal, rejectProposal, runCopilot,
  } = useManuscriptCopilot(nodeId, fullContent, selectedText, updateFullContent, markdownRef);

  const extractToTable = useCallback(() => {
    extractManuscriptToTable({
      nodeId,
      manuscriptTitle: draft.manuscriptTitle,
      fullContent,
    });
  }, [nodeId, draft.manuscriptTitle, fullContent]);

  const handleSelectionChange = useCallback(() => {
    const textarea = markdownRef.current;
    if (!textarea) return;
    setSelectedText(textarea.value.slice(textarea.selectionStart, textarea.selectionEnd).trim());
  }, [markdownRef]);

  const activeEntity = useMemo(
    () => draft.manuscriptEntities.find((entity) => entity.id === activeEntityId) || null,
    [activeEntityId, draft.manuscriptEntities],
  );

  const editor = (
    <div className="manuscript-editor-overlay" role="dialog" aria-modal="true" aria-label="灵感文稿全屏编辑器">
      <div className="manuscript-editor-shell">
        <header className="manuscript-editor-header">
          <div>
            <div className="manuscript-editor-eyebrow">Dynamic Manuscript</div>
            <input
              className="manuscript-editor-title"
              value={draft.manuscriptTitle}
              onChange={(event) => updateTitle(event.target.value)}
              placeholder="灵感文稿标题"
            />
          </div>
          <div className="manuscript-editor-actions">
            <span className="manuscript-editor-save-state">已自动保存</span>
            <button type="button" onClick={undo} disabled={past.length === 0} title="撤销">
              <RotateCcw size={16} />
            </button>
            <button type="button" onClick={redo} disabled={future.length === 0} title="重做">
              <RotateCw size={16} />
            </button>
            <button type="button" onClick={() => setPreviewMode((value) => !value)} title={previewMode ? "进入编辑" : "返回查看"}>
              {previewMode ? <Pencil size={16} /> : <Eye size={16} />}
            </button>
            <button
              type="button"
              className={editorMode === "markdown" ? "active" : ""}
              onClick={() => switchEditorMode("markdown")}
              title="Markdown 编辑"
            >
              <FileCode size={16} />
            </button>
            <button
              type="button"
              className={editorMode === "structured" ? "active" : ""}
              onClick={() => switchEditorMode("structured")}
              title="结构化编辑"
            >
              <Blocks size={16} />
            </button>
            <button type="button" onClick={onClose} title="关闭">
              <X size={16} />
            </button>
          </div>
        </header>

        <div className="manuscript-editor-grid">
          <main className="manuscript-editor-main">
            {previewMode ? (
              <div className="manuscript-preview message-content" dangerouslySetInnerHTML={{ __html: DOMPurify.sanitize(previewHtml) }} />
            ) : editorMode === "markdown" ? (
              <div className="manuscript-markdown-editor">
                <div className="manuscript-markdown-toolbar">
                  <input
                    ref={imageInputRef}
                    type="file"
                    accept="image/*"
                    className="manuscript-file-input"
                    onChange={(event) => {
                      const file = event.target.files?.[0];
                      if (file) void handleImageFile(file);
                      event.target.value = "";
                    }}
                  />
                  <button type="button" onClick={() => imageInputRef.current?.click()} title="上传本地图片">
                    <Upload size={15} />
                    上传本地图片
                  </button>
                  <button type="button" onClick={insertImageUrl} title="插入图片 URL">
                    <Link2 size={15} />
                    插入图片 URL
                  </button>
                  <button
                    type="button"
                    onClick={insertSelectedCanvasImage}
                    disabled={selectedCanvasImages.length === 0}
                    title="插入已选画布图片"
                  >
                    <ImagePlus size={15} />
                    插入已选图片
                  </button>
                </div>
                {selectedText && (
                  <div className="manuscript-ai-toolbar">
                    <span><Sparkles size={14} /> 选中文本 AI 命令</span>
                    {SELECTION_ACTIONS.map((action) => (
                      <button
                        key={action.label}
                        type="button"
                        onClick={() => {
                          const textarea = markdownRef.current;
                          dispatchAiCommand(action.prompt(selectedText), {
                            scope: "selection",
                            sourceText: selectedText,
                            selectionStart: textarea?.selectionStart,
                            selectionEnd: textarea?.selectionEnd,
                          });
                        }}
                      >
                        {action.label}
                      </button>
                    ))}
                  </div>
                )}
                <textarea
                  className="manuscript-editor-summary"
                  value={draft.manuscriptSummary || ""}
                  onChange={(event) => updateSummary(event.target.value)}
                  placeholder="摘要、创作目标或使用说明..."
                  rows={2}
                />
                <textarea
                  ref={markdownRef}
                  className="manuscript-markdown-source"
                  value={fullContent}
                  onChange={(event) => updateFullContent(event.target.value)}
                  onSelect={handleSelectionChange}
                  onKeyUp={handleSelectionChange}
                  onMouseUp={handleSelectionChange}
                  onPaste={handleMarkdownPaste}
                  onDragOver={(event) => {
                    if (event.dataTransfer.types.includes("Files")) event.preventDefault();
                  }}
                  onDrop={handleMarkdownDrop}
                  rows={20}
                  placeholder="直接输入 Markdown。支持标题、列表、引用、代码块、图片和链接；也支持拖拽或粘贴图片。"
                />
              </div>
            ) : (
              <>
                <textarea
                  className="manuscript-editor-summary"
                  value={draft.manuscriptSummary || ""}
                  onChange={(event) => updateSummary(event.target.value)}
                  placeholder="摘要、创作目标或使用说明..."
                  rows={2}
                />

                <div className="manuscript-section-list">
                  {draft.manuscriptSections.map((section) => (
                    <article key={section.id} id={section.id} className={`manuscript-section-editor section-${section.type}`}>
                      <div className="manuscript-section-toolbar">
                        <select
                          value={section.type}
                          onChange={(event) => updateSection(section.id, { type: event.target.value as ManuscriptSectionType })}
                        >
                          {SECTION_TYPES.map((type) => (
                            <option key={type.value} value={type.value}>{type.label}</option>
                          ))}
                        </select>
                        <button type="button" onClick={() => updateSection(section.id, { collapsed: !section.collapsed })}>
                          {section.collapsed ? "展开" : "折叠"}
                        </button>
                        <button type="button" onClick={() => addSection(section.id)} title="在后面新增段落">
                          <Plus size={14} />
                        </button>
                        <button type="button" onClick={() => removeSection(section.id)} title="删除段落">
                          <Trash2 size={14} />
                        </button>
                      </div>
                      {!section.collapsed && (
                        <textarea
                          value={section.text}
                          onChange={(event) => updateSection(section.id, { text: event.target.value })}
                          placeholder="写下灵感、场景、人物、镜头或 [实体]..."
                          rows={section.type === "heading" ? 2 : 5}
                        />
                      )}
                    </article>
                  ))}
                </div>

                <button type="button" className="manuscript-add-section" onClick={() => addSection()}>
                  <Plus size={16} />
                  新增段落
                </button>
              </>
            )}
          </main>

          <aside className="manuscript-editor-inspector">
            <section>
              <h3>实体</h3>
              <div className="manuscript-entity-list">
                {draft.manuscriptEntities.length === 0 ? (
                  <p className="manuscript-muted">使用 [实体名] 标记文稿实体。</p>
                ) : draft.manuscriptEntities.map((entity) => (
                  <label
                    key={entity.id}
                    className={`manuscript-entity-row ${activeEntityId === entity.id ? "active" : ""}`}
                    onClick={() => setActiveEntityId(entity.id)}
                  >
                    <span>{entity.ranges?.length ?? 0}</span>
                    <input
                      value={entity.label}
                      onChange={(event) => renameEntity(entity.label, event.target.value)}
                    />
                  </label>
                ))}
              </div>
              {activeEntity && (
                <div className="manuscript-entity-actions">
                  <strong>{activeEntity.label}</strong>
                  <button type="button" onClick={() => dispatchAiCommand(`请扩展实体「${activeEntity.label}」的描述，并补充更丰富的视觉细节。`, { scope: "entity", sourceText: activeEntity.label })}>
                    扩展实体
                  </button>
                  <button type="button" onClick={() => dispatchAiCommand(`请为实体「${activeEntity.label}」生成一段适合图像生成的 prompt。`, { scope: "entity", sourceText: activeEntity.label })}>
                    生成配图提示词
                  </button>
                  <button type="button" onClick={() => dispatchAiCommand(`请基于实体「${activeEntity.label}」生成一个独立的画面或镜头节点建议。`, { scope: "entity", sourceText: activeEntity.label })}>
                    生成节点建议
                  </button>
                </div>
              )}
            </section>
            <section>
              <h3>能力提示</h3>
              <ul>
                <li>用 `[实体]` 标记可连接对象。</li>
                <li>实体重命名会同步替换正文 token。</li>
                <li>全屏默认先进入查看模式。</li>
                <li>右上角可切换"查看 / Markdown / 结构化"。</li>
              </ul>
            </section>
            <section>
              <h3>智能拆解</h3>
              <div className="manuscript-smart-actions">
                <button
                  type="button"
                  onClick={extractToTable}
                  disabled={!fullContent.trim()}
                  title="新建一个多维表格节点，并让 Copilot 把全文按合适的列自动拆解写入"
                >
                  <TableIcon size={15} />
                  拆成多维表格
                </button>
              </div>
            </section>
            <section>
              <h3>Copilot</h3>
              <div className="manuscript-copilot">
                <div className="manuscript-copilot-scope">
                  <button
                    type="button"
                    className={copilotScope === "document" ? "active" : ""}
                    onClick={() => setCopilotScope("document")}
                  >
                    全文
                  </button>
                  <button
                    type="button"
                    className={copilotScope === "selection" ? "active" : ""}
                    onClick={() => setCopilotScope("selection")}
                    disabled={!selectedText}
                  >
                    选区
                  </button>
                </div>
                <textarea
                  value={copilotInput}
                  onChange={(event) => setCopilotInput(event.target.value)}
                  rows={5}
                  placeholder={selectedText ? "例如：把这段改成更像电影预告片" : "例如：把全文提炼成可生成图片的版本"}
                />
                <div className="manuscript-copilot-presets">
                  {COPILOT_PROMPTS.map((prompt) => (
                    <button key={prompt} type="button" onClick={() => setCopilotInput(prompt)}>
                      {prompt}
                    </button>
                  ))}
                </div>
                <button type="button" className="manuscript-copilot-run" onClick={runCopilot} disabled={!copilotInput.trim()}>
                  <Sparkles size={15} />
                  发送给 Copilot
                </button>
                {proposal && (
                  <div className="manuscript-copilot-proposal">
                    <div className="manuscript-copilot-proposal-header">
                      <strong>AI 提案</strong>
                      <span>{proposal.scope === "selection" ? "作用于选区" : proposal.scope === "entity" ? "作用于实体" : "作用于全文"}</span>
                    </div>
                    {proposal.sourceText && (
                      <div className="manuscript-copilot-source">
                        <small>原始内容</small>
                        <pre>{proposal.sourceText}</pre>
                      </div>
                    )}
                    <div className="manuscript-copilot-suggestion">
                      <small>建议结果</small>
                      <pre>{proposal.content}</pre>
                    </div>
                    <div className="manuscript-copilot-proposal-actions">
                      <button type="button" onClick={applyProposal}>接受</button>
                      <button type="button" onClick={rejectProposal}>拒绝</button>
                    </div>
                  </div>
                )}
              </div>
            </section>
          </aside>
        </div>
      </div>
    </div>
  );

  return createPortal(editor, document.body);
}
