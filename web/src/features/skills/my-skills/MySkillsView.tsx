"use client";

import { useMemo, useState } from "react";
import type {
  GenericTaskStatus,
  SkillContentResult,
  SkillImportPayload,
  SkillImportPreviewResult,
  SkillInfo,
  SkillStats,
} from "@/features/rpc/types";
import { useT, type TKey } from "@/features/i18n";
import { SkillsCatalog } from "@/features/chat/SkillsCatalog";

type Filter = "all" | "repo" | "subscribed" | "learned";

interface Props {
  skills: SkillInfo[];
  disabledSkills?: string[];
  onRemove?: (name: string) => Promise<void>;
  onPromote?: (name: string) => Promise<void>;
  onToggleSkill?: (name: string, disabled: boolean) => Promise<void>;
  onLoadContent?: (name: string) => Promise<SkillContentResult>;
  onLoadAnalytics?: () => Promise<Record<string, SkillStats> | null>;
  onSelectRelated?: (name: string) => void;
  onImport?: (payload: SkillImportPayload) => Promise<{ taskId: string }>;
  onPreviewImport?: (payload: SkillImportPayload) => Promise<SkillImportPreviewResult>;
  onTaskStatus?: (taskId: string) => Promise<GenericTaskStatus>;
  onRefreshSkills?: () => Promise<SkillInfo[]>;
}

const FILTERS: Array<{ id: Filter; labelKey: TKey }> = [
  { id: "all", labelKey: "skills.filter.all" },
  { id: "repo", labelKey: "skills.filter.repo" },
  { id: "subscribed", labelKey: "skills.filter.subscribed" },
  { id: "learned", labelKey: "skills.filter.learned" },
];

function predicate(filter: Filter, skill: SkillInfo): boolean {
  const scope = (skill.Scope || "").toLowerCase();
  switch (filter) {
    case "all":
      return true;
    case "repo":
      return scope === "" || scope === "repo" || scope === "user" || scope === "custom";
    case "subscribed":
      return scope === "subscribed";
    case "learned":
      return scope === "learned";
  }
}

function countFor(filter: Filter, skills: SkillInfo[]): number {
  return skills.filter((s) => predicate(filter, s)).length;
}

export function MySkillsView(props: Props) {
  const { t } = useT();
  const [filter, setFilter] = useState<Filter>("all");

  const filteredSkills = useMemo(
    () => props.skills.filter((s) => predicate(filter, s)),
    [filter, props.skills]
  );

  // Render the scope filter as a compact dropdown so the catalog toolbar stays
  // a single-row strip (search + sort + import live to the right of this).
  const filterSlot = (
    <select
      className="skills-sort-select my-skills-filter-select"
      value={filter}
      onChange={(e) => setFilter(e.target.value as Filter)}
      aria-label={t("skills.filter.label")}
    >
      {FILTERS.map((f) => (
        <option key={f.id} value={f.id}>
          {t(f.labelKey)} ({countFor(f.id, props.skills)})
        </option>
      ))}
    </select>
  );

  return (
    <div className="my-skills-view">
      <SkillsCatalog
        {...props}
        skills={filteredSkills}
        toolbarLeftSlot={filterSlot}
      />
    </div>
  );
}
