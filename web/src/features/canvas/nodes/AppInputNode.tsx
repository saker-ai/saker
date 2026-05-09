import { memo, useState, useCallback, useEffect } from "react";
import { Handle, Position, type NodeProps } from "@xyflow/react";
import { LogIn } from "lucide-react";
import type { CanvasNodeData } from "../types";
import { NodeToolbar } from "./NodeToolbar";
import { LockToggle } from "./LockToggle";
import { useCanvasStore } from "../store";
import { useT } from "@/features/i18n";
import { ToolbarDropdown } from "./ToolbarDropdown";

const FIELD_TYPES = ["text", "paragraph", "number", "select", "file"] as const;
type FieldType = (typeof FIELD_TYPES)[number];

export const AppInputNode = memo(function AppInputNode({ id, data, selected }: NodeProps) {
  const d = data as CanvasNodeData;
  const { t } = useT();
  const updateNode = useCanvasStore((s) => s.updateNode);

  const [variable, setVariable] = useState(d.appVariable ?? "input");
  const [fieldType, setFieldType] = useState<FieldType>((d.appFieldType as FieldType) ?? "text");
  const [required, setRequired] = useState(d.appRequired ?? false);
  const [defaultVal, setDefaultVal] = useState(String(d.appDefault ?? ""));
  const [optionsRaw, setOptionsRaw] = useState((d.appOptions ?? []).join(", "));
  const [min, setMin] = useState(d.appMin !== undefined ? String(d.appMin) : "");
  const [max, setMax] = useState(d.appMax !== undefined ? String(d.appMax) : "");

  useEffect(() => {
    const parsed: Partial<CanvasNodeData> = {
      appVariable: variable,
      appFieldType: fieldType,
      appRequired: required,
      appDefault: defaultVal !== "" ? defaultVal : undefined,
    };
    if (fieldType === "select") {
      parsed.appOptions = optionsRaw.split(",").map((s) => s.trim()).filter(Boolean);
    }
    if (fieldType === "number") {
      parsed.appMin = min !== "" ? Number(min) : undefined;
      parsed.appMax = max !== "" ? Number(max) : undefined;
    }
    updateNode(id, parsed);
  }, [variable, fieldType, required, defaultVal, optionsRaw, min, max, id, updateNode]);

  const handleContextMenu = useCallback((e: React.MouseEvent) => {
    e.preventDefault();
    window.dispatchEvent(
      new CustomEvent("canvas-contextmenu", {
        detail: { nodeId: id, position: { x: e.clientX, y: e.clientY }, label: d.label },
      })
    );
  }, [id, d.label]);

  const fieldTypeOptions = FIELD_TYPES.map((ft) => ({
    value: ft,
    label: t(`canvas.appFieldType.${ft}` as any) || ft,
  }));

  return (
    <div
      className={`canvas-node canvas-node-app-input ${selected ? "selected" : ""}`}
      role="article"
      aria-label={d.label || t("canvas.appInputLabel")}
      onContextMenu={handleContextMenu}
    >
      <NodeToolbar nodeId={id} selected={selected} />
      <div className="canvas-node-header">
        <div className="canvas-node-icon-wrapper">
          <LogIn size={14} />
        </div>
        <span className="canvas-node-label">{d.label || t("canvas.appInputLabel")}</span>
        <LockToggle nodeId={id} locked={d.locked} />
      </div>

      <div className="canvas-node-body nowheel">
        <div className="gen-toolbar nodrag" style={{ flexDirection: "column", gap: 6 }}>
          <div style={{ display: "flex", alignItems: "center", gap: 6 }}>
            <label style={{ fontSize: 11, opacity: 0.6, minWidth: 54 }}>
              {t("canvas.appVariable")}
            </label>
            <input
              className="gen-negative-prompt nowheel nodrag"
              style={{ flex: 1 }}
              value={variable}
              onChange={(e) => setVariable(e.target.value)}
              placeholder="variable_name"
            />
          </div>

          <div style={{ display: "flex", alignItems: "center", gap: 6 }}>
            <label style={{ fontSize: 11, opacity: 0.6, minWidth: 54 }}>
              {t("canvas.appFieldType")}
            </label>
            <ToolbarDropdown
              options={fieldTypeOptions}
              value={fieldType}
              onChange={(v) => setFieldType(v as FieldType)}
            />
          </div>

          <div style={{ display: "flex", alignItems: "center", gap: 6 }}>
            <label style={{ fontSize: 11, opacity: 0.6, minWidth: 54 }}>
              {t("canvas.appRequired")}
            </label>
            <input
              type="checkbox"
              className="nodrag"
              checked={required}
              onChange={(e) => setRequired(e.target.checked)}
            />
          </div>

          <div style={{ display: "flex", alignItems: "center", gap: 6 }}>
            <label style={{ fontSize: 11, opacity: 0.6, minWidth: 54 }}>
              {t("canvas.appDefault")}
            </label>
            <input
              className="gen-negative-prompt nowheel nodrag"
              style={{ flex: 1 }}
              value={defaultVal}
              onChange={(e) => setDefaultVal(e.target.value)}
              placeholder={t("canvas.appDefault")}
            />
          </div>

          {fieldType === "select" && (
            <div style={{ display: "flex", alignItems: "flex-start", gap: 6 }}>
              <label style={{ fontSize: 11, opacity: 0.6, minWidth: 54, paddingTop: 4 }}>
                {t("canvas.appOptions")}
              </label>
              <textarea
                className="gen-prompt nowheel nodrag"
                style={{ flex: 1, minHeight: 48 }}
                value={optionsRaw}
                onChange={(e) => setOptionsRaw(e.target.value)}
                placeholder={t("canvas.appOptions")}
                rows={2}
              />
            </div>
          )}

          {fieldType === "number" && (
            <div style={{ display: "flex", alignItems: "center", gap: 6 }}>
              <label style={{ fontSize: 11, opacity: 0.6, minWidth: 54 }}>
                {t("canvas.appMin")}
              </label>
              <input
                className="gen-negative-prompt nowheel nodrag"
                style={{ flex: 1 }}
                type="number"
                value={min}
                onChange={(e) => setMin(e.target.value)}
                placeholder={t("canvas.appMin")}
              />
              <label style={{ fontSize: 11, opacity: 0.6 }}>
                {t("canvas.appMax")}
              </label>
              <input
                className="gen-negative-prompt nowheel nodrag"
                style={{ flex: 1 }}
                type="number"
                value={max}
                onChange={(e) => setMax(e.target.value)}
                placeholder={t("canvas.appMax")}
              />
            </div>
          )}
        </div>
      </div>

      <Handle type="source" position={Position.Right} className="canvas-handle" />
    </div>
  );
});
