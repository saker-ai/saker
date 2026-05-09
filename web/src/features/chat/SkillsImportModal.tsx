"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import type { GenericTaskStatus, SkillImportPayload, SkillImportPreviewResult, SkillInfo } from "@/features/rpc/types";
import { useT, type TKey } from "@/features/i18n";

type ImportSourceType = "path" | "git" | "archive";
type ImportScope = "local" | "global";
type ConflictStrategy = "overwrite" | "skip" | "error";

interface Props {
  open: boolean;
  onClose: () => void;
  onImport: (payload: SkillImportPayload) => Promise<{ taskId: string }>;
  onPreview: (payload: SkillImportPayload) => Promise<SkillImportPreviewResult>;
  onTaskStatus: (taskId: string) => Promise<GenericTaskStatus>;
  onRefreshSkills?: () => Promise<SkillInfo[]>;
  onImported?: (skills: SkillInfo[], importedNames: string[]) => void;
}

export function SkillsImportModal({ open, onClose, onImport, onPreview, onTaskStatus, onRefreshSkills, onImported }: Props) {
  const { t } = useT();
  const [sourceType, setSourceType] = useState<ImportSourceType>("path");
  const [location, setLocation] = useState("");
  const [sourcePathsText, setSourcePathsText] = useState("");
  const [scope, setScope] = useState<ImportScope>("local");
  const [conflictStrategy, setConflictStrategy] = useState<ConflictStrategy>("overwrite");
  const [taskId, setTaskId] = useState("");
  const [taskProgress, setTaskProgress] = useState(0);
  const [taskMessage, setTaskMessage] = useState("");
  const [taskLogs, setTaskLogs] = useState<string[]>([]);
  const [taskError, setTaskError] = useState("");
  const [running, setRunning] = useState(false);
  const [preview, setPreview] = useState<SkillImportPreviewResult | null>(null);
  const [previewLoading, setPreviewLoading] = useState(false);
  const [result, setResult] = useState<GenericTaskStatus["result"] | null>(null);

  const resetState = useCallback(() => {
    setTaskId("");
    setTaskProgress(0);
    setTaskMessage("");
    setTaskLogs([]);
    setTaskError("");
    setRunning(false);
    setPreview(null);
    setPreviewLoading(false);
    setResult(null);
  }, []);

  useEffect(() => {
    if (!open) {
      resetState();
    }
  }, [open, resetState]);

  useEffect(() => {
    if (!open || running) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        onClose();
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [onClose, open, running]);

  const payload = useMemo<SkillImportPayload>(() => {
    const next: SkillImportPayload = {
      target_scope: scope,
      conflict_strategy: conflictStrategy,
    };
    if (sourceType === "path") {
      next.source_type = "path";
      next.source_path = location.trim();
    } else if (sourceType === "git") {
      next.source_type = "git";
      next.repo_url = location.trim();
    } else {
      next.source_type = "archive";
      next.archive_url = location.trim();
    }
    const extraPaths = sourcePathsText
      .split("\n")
      .map((value) => value.trim())
      .filter(Boolean);
    if (extraPaths.length > 0) {
      next.source_paths = extraPaths;
    }
    return next;
  }, [conflictStrategy, location, scope, sourcePathsText, sourceType]);

  const locationLabel = sourceType === "path"
    ? t("skills.importLocationPath")
    : sourceType === "git"
      ? t("skills.importLocationGit")
      : t("skills.importLocationArchive");

  const locationPlaceholder = sourceType === "path"
    ? t("skills.importPlaceholderPath")
    : sourceType === "git"
      ? t("skills.importPlaceholderGit")
      : t("skills.importPlaceholderArchive");

  const handlePreview = useCallback(async () => {
    setPreviewLoading(true);
    setTaskError("");
    try {
      const data = await onPreview(payload);
      setPreview(data);
    } catch (error) {
      setTaskError(error instanceof Error ? error.message : t("skills.importFailed"));
    } finally {
      setPreviewLoading(false);
    }
  }, [onPreview, payload, t]);

  const pollTask = useCallback(async (nextTaskId: string) => {
    for (;;) {
      const task = await onTaskStatus(nextTaskId);
      setTaskProgress(task.progress ?? 0);
      setTaskMessage(task.message ?? "");
      setTaskLogs(task.logs ?? []);
      if (task.status === "done") {
        setRunning(false);
        setTaskProgress(100);
        setResult(task.result ?? null);
        const imported = task.result?.importedSkills ?? [];
        if (onRefreshSkills) {
          const refreshed = await onRefreshSkills();
          if (imported.length > 0) {
            onImported?.(refreshed, imported);
          }
        }
        return;
      }
      if (task.status === "error") {
        setRunning(false);
        setTaskError(task.error ?? t("skills.importFailed"));
        setResult(task.result ?? null);
        return;
      }
      await new Promise((resolve) => setTimeout(resolve, 1200));
    }
  }, [onImported, onRefreshSkills, onTaskStatus, t]);

  const handleImport = useCallback(async () => {
    setRunning(true);
    setTaskError("");
    setTaskLogs([]);
    setTaskMessage("");
    setTaskProgress(0);
    setResult(null);
    const accepted = await onImport(payload);
    setTaskId(accepted.taskId);
    await pollTask(accepted.taskId);
  }, [onImport, payload, pollTask]);

  if (!open) return null;

  return (
    <div className="provider-modal-overlay" onClick={() => { if (!running) onClose(); }}>
      <div className="provider-modal skills-import-modal" tabIndex={-1} onClick={(event) => event.stopPropagation()}>
        <div className="provider-modal-header">
          <span className="provider-modal-title">{t("skills.import")}</span>
          <div className="provider-modal-header-actions">
            <button className="provider-modal-close" onClick={onClose} aria-label={t("common.close" as TKey)} disabled={running}>
              ×
            </button>
          </div>
        </div>
        <div className="provider-modal-body">
          <div className="provider-modal-section">
            <div className="provider-modal-section-title">{t("skills.importSourceType")}</div>
            <div className="skills-detail-tags">
              {(["path", "git", "archive"] as const).map((value) => (
                <button
                  key={value}
                  type="button"
                  className={`skills-tag skills-tag-link ${sourceType === value ? "skills-tag-active" : ""}`}
                  disabled={running}
                  onClick={() => setSourceType(value)}
                >
                  {t(`skills.importSource.${value}` as TKey)}
                </button>
              ))}
            </div>
          </div>

          <div className="provider-modal-section">
            <div className="provider-modal-section-title">{locationLabel}</div>
            <input
              className="skills-search-input"
              type="text"
              value={location}
              disabled={running}
              placeholder={locationPlaceholder}
              onChange={(event) => setLocation(event.target.value)}
            />
          </div>

          {sourceType !== "path" && (
            <div className="provider-modal-section">
              <div className="provider-modal-section-title">{t("skills.importSourcePaths")}</div>
              <textarea
                className="skills-content-pre skills-import-textarea"
                value={sourcePathsText}
                disabled={running}
                placeholder={t("skills.importSourcePathsPlaceholder")}
                onChange={(event) => setSourcePathsText(event.target.value)}
              />
            </div>
          )}

          <div className="provider-modal-section">
            <div className="provider-modal-section-title">{t("skills.importScope")}</div>
            <div className="skills-detail-tags">
              {(["local", "global"] as const).map((value) => (
                <button
                  key={value}
                  type="button"
                  className={`skills-tag skills-tag-link ${scope === value ? "skills-tag-active" : ""}`}
                  disabled={running}
                  onClick={() => setScope(value)}
                >
                  {t(`skills.importScope.${value}` as TKey)}
                </button>
              ))}
            </div>
          </div>

          <div className="provider-modal-section">
            <div className="provider-modal-section-title">{t("skills.importConflictStrategy")}</div>
            <div className="skills-detail-tags">
              {(["overwrite", "skip", "error"] as const).map((value) => (
                <button
                  key={value}
                  type="button"
                  className={`skills-tag skills-tag-link ${conflictStrategy === value ? "skills-tag-active" : ""}`}
                  disabled={running}
                  onClick={() => setConflictStrategy(value)}
                >
                  {t(`skills.importConflict.${value}` as TKey)}
                </button>
              ))}
            </div>
          </div>

          {(preview || previewLoading) && (
            <div className="provider-modal-section">
              <div className="provider-modal-section-title">{t("skills.importPreview")}</div>
              {previewLoading ? (
                <div className="skills-detail-empty">{t("common.loading" as TKey)}</div>
              ) : (
                <>
                  {preview?.items?.length ? (
                    <div className="skills-import-preview-list">
                      {preview.items.map((item) => (
                        <div key={`${item.skill_id}-${item.path}`} className="skills-import-preview-item">
                          <div className="skills-import-preview-head">
                            <span className="skills-card-name">{item.skill_id}</span>
                            <span className={`skills-import-status skills-import-status-${item.status}`}>{item.status}</span>
                          </div>
                          <div className="skills-import-preview-path">{item.path}</div>
                          {item.message ? <div className="skills-import-preview-message">{item.message}</div> : null}
                        </div>
                      ))}
                    </div>
                  ) : (
                    <div className="skills-detail-empty">{t("skills.importPreviewEmpty")}</div>
                  )}
                </>
              )}
            </div>
          )}

          {(taskId || taskError || result) && (
            <div className="provider-modal-section">
              <div className="provider-modal-section-title">{t("skills.importProgress")}</div>
              <div className="skills-analytics-stat">
                <span className="skills-analytics-stat-label">{t("skills.importTaskId")}</span>
                <span className="skills-analytics-stat-value">{taskId || "-"}</span>
              </div>
              <div className="skills-analytics-stat">
                <span className="skills-analytics-stat-label">{t("skills.importStatus")}</span>
                <span className="skills-analytics-stat-value">{taskMessage || (taskError || "-")}</span>
              </div>
              <div className="skills-analytics-stat">
                <span className="skills-analytics-stat-label">{t("skills.importProgress")}</span>
                <span className="skills-analytics-stat-value">{taskProgress}%</span>
                <div className="skills-analytics-bar">
                  <div className="skills-analytics-bar-fill" style={{ width: `${Math.max(0, Math.min(100, taskProgress))}%` }} />
                </div>
              </div>
              {result?.importedSkills && result.importedSkills.length > 0 && (
                <div className="skills-import-summary">
                  <div className="skills-detail-section-title">{t("skills.importedSkills")}</div>
                  <div className="skills-detail-tags">
                    {result.importedSkills.map((skillId) => (
                      <span key={skillId} className="skills-tag">{skillId}</span>
                    ))}
                  </div>
                </div>
              )}
              {result?.skippedSkills && result.skippedSkills.length > 0 && (
                <div className="skills-import-summary">
                  <div className="skills-detail-section-title">{t("skills.skippedSkills")}</div>
                  <div className="skills-detail-tags">
                    {result.skippedSkills.map((skillId) => (
                      <span key={skillId} className="skills-tag">{skillId}</span>
                    ))}
                  </div>
                </div>
              )}
              {taskError && <p className="skills-detail-empty skills-import-error">{taskError}</p>}
              <div className="skills-detail-section-title">{t("skills.importLogs")}</div>
              <pre className="skills-content-pre skills-import-log">{taskLogs.length > 0 ? taskLogs.join("\n") : t("skills.importLogsEmpty")}</pre>
            </div>
          )}
        </div>
        <div className="provider-modal-footer">
          <button type="button" className="settings-btn-cancel" disabled={running} onClick={onClose}>
            {result ? t("common.close" as TKey) : t("settings.cancel")}
          </button>
          <div className="skills-import-footer-actions">
            <button type="button" className="settings-btn-cancel" disabled={running || !location.trim()} onClick={() => void handlePreview()}>
              {previewLoading ? t("common.loading" as TKey) : t("skills.previewImport")}
            </button>
            <button type="button" className="settings-btn-save" disabled={running || !location.trim()} onClick={() => void handleImport()}>
              {running ? t("skills.importRunning") : t("skills.import")}
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
