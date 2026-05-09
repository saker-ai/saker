import { ChevronDown } from "lucide-react";
import { useT } from "@/features/i18n";

interface EngineSelectorProps {
  engines: string[];
  value: string;
  onChange: (v: string) => void;
  disabled?: boolean;
}

export function EngineSelector({ engines, value, onChange, disabled }: EngineSelectorProps) {
  const { t } = useT();

  const getShortName = (name: string) => {
    if (!name) return "";
    const parts = name.split("/");
    return parts[parts.length - 1];
  };

  if (engines.length === 0) return null;

  if (engines.length === 1) {
    return (
      <div className="gen-param-group">
        <span className="gen-param-label">{t("canvas.engine")}</span>
        <span className="gen-engine-badge" title={engines[0]}>{getShortName(engines[0])}</span>
      </div>
    );
  }

  return (
    <div className="gen-param-group">
      <span className="gen-param-label">{t("canvas.engine")}</span>
      <div className="gen-select-wrapper engine-select-fixed">
        <select
          className="gen-select nodrag"
          value={value}
          onChange={(e) => onChange(e.target.value)}
          onClick={(e) => e.stopPropagation()}
          onMouseDown={(e) => e.stopPropagation()}
          disabled={disabled}
          title={value}
        >
          {engines.map((e) => (
            <option key={e} value={e}>
              {e}
            </option>
          ))}
        </select>
        {/* Visual overlay for the short name so the select still works but looks cleaner */}
        <div className="gen-select-display-val">
          {getShortName(value)}
        </div>
        <ChevronDown size={12} className="gen-select-icon" />
      </div>
    </div>
  );
}
