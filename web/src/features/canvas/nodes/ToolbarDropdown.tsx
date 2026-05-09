import React, { useState, useRef, useEffect, useCallback, type ReactNode } from "react";
import { ChevronDown } from "lucide-react";

export interface DropdownOption {
  value: string;
  label: string;
}

interface ToolbarDropdownProps {
  icon?: ReactNode;
  options: DropdownOption[];
  value: string;
  onChange: (value: string) => void;
  disabled?: boolean;
}

export function ToolbarDropdown({ icon, options, value, onChange, disabled }: ToolbarDropdownProps) {
  const [open, setOpen] = useState(false);
  const [focusIndex, setFocusIndex] = useState(-1);
  const [dropBelow, setDropBelow] = useState(true);
  const ref = useRef<HTMLDivElement>(null);

  const selected = options.find((o) => o.value === value);

  const getShortName = (name: string) => {
    if (!name) return "";
    const parts = name.split("/");
    return parts[parts.length - 1];
  };

  const handleToggle = useCallback(
    (e?: React.MouseEvent | React.KeyboardEvent) => {
      if (e) e.stopPropagation();
      if (disabled) return;

      setOpen((prev) => {
        if (!prev) {
          const idx = options.findIndex((o) => o.value === value);
          setFocusIndex(idx >= 0 ? idx : 0);
          if (ref.current) {
            const rect = ref.current.getBoundingClientRect();
            // Prefer downward if there is space (>200px), otherwise detect where space is larger
            const spaceBelow = window.innerHeight - rect.bottom;
            setDropBelow(spaceBelow > 200 || spaceBelow > rect.top);
          }
        }
        return !prev;
      });
    },
    [disabled, options, value]
  );

  const handleSelect = useCallback(
    (v: string, e?: React.MouseEvent | React.KeyboardEvent) => {
      if (e) e.stopPropagation();
      onChange(v);
      setOpen(false);
    },
    [onChange]
  );

  // Keyboard navigation
  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      if (!open) {
        if (e.key === "Enter" || e.key === " " || e.key === "ArrowDown") {
          e.preventDefault();
          handleToggle(e);
        }
        return;
      }

      switch (e.key) {
        case "Escape":
          e.preventDefault();
          setOpen(false);
          break;
        case "ArrowDown":
          e.preventDefault();
          setFocusIndex((i) => (i + 1) % options.length);
          break;
        case "ArrowUp":
          e.preventDefault();
          setFocusIndex((i) => (i - 1 + options.length) % options.length);
          break;
        case "Enter":
          e.preventDefault();
          if (focusIndex >= 0 && focusIndex < options.length) {
            handleSelect(options[focusIndex].value, e);
          }
          break;
      }
    },
    [open, focusIndex, options, handleToggle, handleSelect]
  );

  // Close on outside click
  useEffect(() => {
    if (!open) return;
    const handler = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("mousedown", handler, true);
    return () => document.removeEventListener("mousedown", handler, true);
  }, [open]);

  return (
    <div
      className={`tbd-root nodrag ${disabled ? "tbd-disabled" : ""}`}
      ref={ref}
      tabIndex={0}
      onKeyDown={handleKeyDown}
    >
      <button className="tbd-trigger" onClick={handleToggle} disabled={disabled} type="button">
        {icon && <span className="tbd-icon">{icon}</span>}
        <span className="tbd-label" title={selected?.label ?? value}>
          {getShortName(selected?.label ?? value)}
        </span>
        <ChevronDown size={10} className={`tbd-chevron ${open ? "tbd-chevron-open" : ""}`} />
      </button>
      {open && (
        <div className={`tbd-menu nowheel ${dropBelow ? "tbd-menu-below" : ""}`}>
          {options.map((opt, i) => (
            <button
              key={opt.value}
              className={`tbd-item ${opt.value === value ? "tbd-item-active" : ""} ${
                i === focusIndex ? "tbd-item-focus" : ""
              }`}
              onClick={(e) => handleSelect(opt.value, e)}
              type="button"
            >
              {opt.label}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
