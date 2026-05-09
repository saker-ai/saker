"use client";

import { useState, useRef, useCallback, useMemo, useEffect } from "react";
import { toast } from "sonner";
import { useT } from "@/features/i18n";
import { usePermissions } from "@/features/project/usePermissions";
import { Send, Square, Plus, X, FileText, Film, Volume2, HelpCircle, Clock } from "lucide-react";
import type { SkillInfo } from "@/features/rpc/types";

const MAX_FILE_SIZE = 50 * 1024 * 1024; // 50 MB
const SLASH_PICKER_MAX = 8;
const RECENT_SKILLS_KEY = "saker.composer.recentSkills";
const RECENT_SKILLS_MAX = 3;

/**
 * Synthetic "skill" representing the /help shortcut. It is identified by
 * reference (not Name) so users can still register a real "help" skill if
 * they want — selection routes to skills view instead of inserting text.
 */
const HELP_SKILL: SkillInfo = {
  Name: "help",
  Description: "Open the skills catalog",
  Scope: "user",
  RelatedSkills: [],
  Keywords: ["help", "skills", "list", "catalog"],
};

function readRecentSkillNames(): string[] {
  if (typeof window === "undefined") return [];
  try {
    const raw = window.localStorage.getItem(RECENT_SKILLS_KEY);
    if (!raw) return [];
    const arr = JSON.parse(raw);
    return Array.isArray(arr) ? arr.filter((x): x is string => typeof x === "string").slice(0, RECENT_SKILLS_MAX) : [];
  } catch {
    return [];
  }
}

function writeRecentSkillNames(names: string[]) {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(RECENT_SKILLS_KEY, JSON.stringify(names.slice(0, RECENT_SKILLS_MAX)));
  } catch {
    /* ignore storage errors (private mode, quota, etc.) */
  }
}

export interface Attachment {
  id: string;
  file: File;
  name: string;
  media_type: string;
  preview?: string;    // object URL for image thumbnails
  uploading: boolean;
  progress: number;    // 0-100
  path?: string;       // server path after upload
  error?: string;
}

interface Props {
  onSend: (text: string, attachments?: Attachment[]) => void;
  onStop?: () => void;
  disabled: boolean;
  running?: boolean;
  skills?: SkillInfo[];
}

/** Resolve the upload endpoint URL based on current page. */
function resolveUploadUrl(): string {
  if (typeof window === "undefined") return "http://127.0.0.1:10112/api/upload";
  const { protocol, hostname, port } = window.location;
  if (port === "10111") return `${protocol}//${hostname}:10112/api/upload`;
  return `${protocol}//${window.location.host}/api/upload`;
}

/** Upload a file with progress tracking via XMLHttpRequest. */
function uploadFileWithProgress(
  file: File,
  onProgress: (pct: number) => void,
): Promise<Partial<Attachment>> {
  return new Promise((resolve) => {
    const xhr = new XMLHttpRequest();
    xhr.open("POST", resolveUploadUrl());

    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable) {
        onProgress(Math.round((e.loaded / e.total) * 100));
      }
    };

    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300) {
        try {
          const data = JSON.parse(xhr.responseText);
          resolve({ path: data.path, media_type: data.media_type, uploading: false, progress: 100 });
        } catch {
          resolve({ error: "Invalid server response", uploading: false, progress: 0 });
        }
      } else {
        resolve({ error: xhr.responseText || `Upload failed (${xhr.status})`, uploading: false, progress: 0 });
      }
    };

    xhr.onerror = () => {
      resolve({ error: "Network error", uploading: false, progress: 0 });
    };

    const form = new FormData();
    form.append("file", file);
    xhr.send(form);
  });
}

/**
 * Detect a slash trigger ending at `caret`.
 * Returns { start, query } when text has "/" at line start or after whitespace,
 * followed by 0+ non-space chars up to caret. Returns null otherwise.
 */
function detectSlashTrigger(value: string, caret: number): { start: number; query: string } | null {
  if (caret <= 0) return null;
  // Walk back from caret: stop at whitespace; if we hit "/", check the char before it.
  let i = caret - 1;
  while (i >= 0) {
    const ch = value[i];
    if (ch === " " || ch === "\n" || ch === "\t") return null;
    if (ch === "/") {
      const prev = i === 0 ? "" : value[i - 1];
      if (i === 0 || prev === " " || prev === "\n" || prev === "\t") {
        return { start: i, query: value.slice(i + 1, caret) };
      }
      return null;
    }
    i--;
  }
  return null;
}

export function Composer({ onSend, onStop, disabled, running, skills }: Props) {
  const { t } = useT();
  const perms = usePermissions();
  const readOnly = !perms.canEdit;
  const inputDisabled = disabled || readOnly;
  const [text, setText] = useState("");
  const [attachments, setAttachments] = useState<Attachment[]>([]);
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const nextAttachIdRef = useRef(0);

  // Slash picker state
  const [slashOpen, setSlashOpen] = useState(false);
  const [slashStart, setSlashStart] = useState(0);
  const [slashQuery, setSlashQuery] = useState("");
  const [slashIndex, setSlashIndex] = useState(0);
  const [recentSkillNames, setRecentSkillNames] = useState<string[]>(() => readRecentSkillNames());

  // Items shown in the slash picker. When the query is empty we surface the
  // /help shortcut and recently-used skills first; once the user types we
  // fall back to plain ranked filtering against the full skill list.
  const filteredSkills = useMemo(() => {
    if (!slashOpen) return [];
    const list = skills ?? [];
    const q = slashQuery.toLowerCase();

    if (!q) {
      const seen = new Set<string>();
      const out: SkillInfo[] = [];
      // /help always pinned at top of empty query
      out.push(HELP_SKILL);
      seen.add(HELP_SKILL.Name);
      for (const name of recentSkillNames) {
        const found = list.find(s => s.Name === name);
        if (found && !seen.has(found.Name)) {
          out.push(found);
          seen.add(found.Name);
        }
      }
      for (const s of list) {
        if (out.length >= SLASH_PICKER_MAX) break;
        if (!seen.has(s.Name)) {
          out.push(s);
          seen.add(s.Name);
        }
      }
      return out.slice(0, SLASH_PICKER_MAX);
    }

    const matches: { skill: SkillInfo; rank: number }[] = [];
    // Make /help findable by typing "/h" or "/help"
    const helpName = HELP_SKILL.Name.toLowerCase();
    if (helpName.startsWith(q) || helpName.includes(q)) {
      matches.push({ skill: HELP_SKILL, rank: helpName.startsWith(q) ? 0 : 1 });
    }
    for (const s of list) {
      const name = (s.Name || "").toLowerCase();
      let rank = -1;
      if (name.startsWith(q)) rank = 0;
      else if (name.includes(q)) rank = 1;
      else if ((s.Keywords || []).some(k => k.toLowerCase().includes(q))) rank = 2;
      else if ((s.Description || "").toLowerCase().includes(q)) rank = 3;
      if (rank >= 0) matches.push({ skill: s, rank });
    }
    matches.sort((a, b) => a.rank - b.rank || a.skill.Name.localeCompare(b.skill.Name));
    return matches.slice(0, SLASH_PICKER_MAX).map(m => m.skill);
  }, [slashOpen, slashQuery, skills, recentSkillNames]);

  // Reset highlighted item when filtered list changes
  useEffect(() => {
    setSlashIndex(0);
  }, [slashQuery, slashOpen]);

  const closeSlash = useCallback(() => {
    setSlashOpen(false);
    setSlashQuery("");
    setSlashIndex(0);
  }, []);

  const insertSlashSelection = useCallback((skill: SkillInfo) => {
    const ta = textareaRef.current;
    if (!ta) return;
    const before = text.slice(0, slashStart);
    const after = text.slice(ta.selectionStart);
    const inserted = `/${skill.Name} `;
    const next = before + inserted + after;
    setText(next);
    closeSlash();
    requestAnimationFrame(() => {
      const t2 = textareaRef.current;
      if (!t2) return;
      const pos = before.length + inserted.length;
      t2.focus();
      t2.setSelectionRange(pos, pos);
      t2.style.height = "auto";
      t2.style.height = Math.min(t2.scrollHeight, 200) + "px";
    });
  }, [text, slashStart, closeSlash]);

  const pushRecentSkill = useCallback((name: string) => {
    setRecentSkillNames(prev => {
      const next = [name, ...prev.filter(n => n !== name)].slice(0, RECENT_SKILLS_MAX);
      writeRecentSkillNames(next);
      return next;
    });
  }, []);

  /**
   * Routes a slash-picker selection. The synthetic /help item is identified by
   * reference (HELP_SKILL) so a real "help" skill, if registered, still
   * inserts as text. /help instead navigates to the skills catalog view.
   */
  const handleSelectSkill = useCallback((skill: SkillInfo) => {
    if (skill === HELP_SKILL) {
      closeSlash();
      if (typeof window !== "undefined") {
        window.location.hash = "skills";
      }
      return;
    }
    pushRecentSkill(skill.Name);
    insertSlashSelection(skill);
  }, [closeSlash, pushRecentSkill, insertSlashSelection]);

  const updateSlashFromCaret = useCallback((value: string, caret: number) => {
    if (!skills || skills.length === 0) {
      if (slashOpen) closeSlash();
      return;
    }
    const trig = detectSlashTrigger(value, caret);
    if (trig) {
      setSlashOpen(true);
      setSlashStart(trig.start);
      setSlashQuery(trig.query);
    } else if (slashOpen) {
      closeSlash();
    }
  }, [skills, slashOpen, closeSlash]);

  const addFiles = useCallback((files: FileList | File[]) => {
    const newAttachments: Attachment[] = [];
    for (const file of Array.from(files)) {
      // Size pre-check
      if (file.size > MAX_FILE_SIZE) {
        toast.error(`${file.name}: ${t("composer.fileTooLarge")}`);
        continue;
      }

      const id = `att-${++nextAttachIdRef.current}`;
      const att: Attachment = {
        id,
        file,
        name: file.name,
        media_type: file.type || "application/octet-stream",
        uploading: true,
        progress: 0,
      };
      // Generate preview for images
      if (file.type.startsWith("image/")) {
        att.preview = URL.createObjectURL(file);
      }
      newAttachments.push(att);
    }
    if (newAttachments.length === 0) return;
    setAttachments(prev => [...prev, ...newAttachments]);

    // Upload each file with progress
    for (const att of newAttachments) {
      uploadFileWithProgress(att.file, (pct) => {
        setAttachments(prev =>
          prev.map(a => a.id === att.id ? { ...a, progress: pct } : a)
        );
      }).then(result => {
        if (result.error) {
          toast.error(`${att.name}: ${t("composer.uploadFailed")}`);
        }
        setAttachments(prev =>
          prev.map(a => a.id === att.id ? { ...a, ...result } : a)
        );
      });
    }
  }, [t]);

  const removeAttachment = useCallback((id: string) => {
    setAttachments(prev => {
      const att = prev.find(a => a.id === id);
      if (att?.preview) URL.revokeObjectURL(att.preview);
      return prev.filter(a => a.id !== id);
    });
  }, []);

  const handleSend = useCallback(() => {
    const trimmed = text.trim();
    const readyAttachments = attachments.filter(a => a.path && !a.uploading && !a.error);
    if ((!trimmed && readyAttachments.length === 0) || inputDisabled) return;
    onSend(trimmed || " ", readyAttachments.length > 0 ? readyAttachments : undefined);
    setText("");
    setAttachments([]);
    closeSlash();
    if (textareaRef.current) {
      textareaRef.current.style.height = "auto";
    }
  }, [text, attachments, inputDisabled, onSend, closeSlash]);

  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
      // Slash picker keyboard handling takes priority
      if (slashOpen && filteredSkills.length > 0) {
        if (e.key === "ArrowDown") {
          e.preventDefault();
          setSlashIndex(i => (i + 1) % filteredSkills.length);
          return;
        }
        if (e.key === "ArrowUp") {
          e.preventDefault();
          setSlashIndex(i => (i - 1 + filteredSkills.length) % filteredSkills.length);
          return;
        }
        if (e.key === "Enter" || e.key === "Tab") {
          e.preventDefault();
          const pick = filteredSkills[slashIndex];
          if (pick) handleSelectSkill(pick);
          return;
        }
        if (e.key === "Escape") {
          e.preventDefault();
          closeSlash();
          return;
        }
      }
      if (e.key === "Enter" && !e.shiftKey) {
        e.preventDefault();
        handleSend();
      }
    },
    [slashOpen, filteredSkills, slashIndex, handleSelectSkill, closeSlash, handleSend]
  );

  const handleInput = useCallback(
    (e: React.ChangeEvent<HTMLTextAreaElement>) => {
      const value = e.target.value;
      setText(value);
      const ta = e.target;
      ta.style.height = "auto";
      ta.style.height = Math.min(ta.scrollHeight, 200) + "px";
      updateSlashFromCaret(value, ta.selectionStart ?? value.length);
    },
    [updateSlashFromCaret]
  );

  const handleSelect = useCallback(
    (e: React.SyntheticEvent<HTMLTextAreaElement>) => {
      const ta = e.currentTarget;
      updateSlashFromCaret(ta.value, ta.selectionStart ?? ta.value.length);
    },
    [updateSlashFromCaret]
  );

  const handleBlur = useCallback(() => {
    // Delay so click on picker item still registers
    window.setTimeout(() => closeSlash(), 120);
  }, [closeSlash]);

  const handleFileChange = useCallback((e: React.ChangeEvent<HTMLInputElement>) => {
    if (e.target.files && e.target.files.length > 0) {
      addFiles(e.target.files);
      e.target.value = "";
    }
  }, [addFiles]);

  const handleDrop = useCallback((e: React.DragEvent) => {
    e.preventDefault();
    e.stopPropagation();
    if (e.dataTransfer.files.length > 0) {
      addFiles(e.dataTransfer.files);
    }
  }, [addFiles]);

  const handleDragOver = useCallback((e: React.DragEvent) => {
    e.preventDefault();
    e.stopPropagation();
  }, []);

  // Ctrl+V / Cmd+V paste image from clipboard
  const handlePaste = useCallback((e: React.ClipboardEvent) => {
    const items = e.clipboardData?.items;
    if (!items) return;
    const imageFiles: File[] = [];
    for (const item of Array.from(items)) {
      if (item.type.startsWith("image/")) {
        const file = item.getAsFile();
        if (file) {
          // Give pasted images a descriptive name
          const ext = file.type.split("/")[1] || "png";
          const named = new File([file], `pasted-image-${Date.now()}.${ext}`, { type: file.type });
          imageFiles.push(named);
        }
      }
    }
    if (imageFiles.length > 0) {
      e.preventDefault();
      addFiles(imageFiles);
    }
  }, [addFiles]);

  const anyUploading = attachments.some(a => a.uploading);

  const renderAttachmentIcon = (att: Attachment) => {
    if (att.media_type.startsWith("video/")) return <Film size={16} />;
    if (att.media_type.startsWith("audio/")) return <Volume2 size={16} />;
    return <FileText size={16} />;
  };

  return (
    <div className="gemini-composer-container">
      <div
        className="gemini-composer-pill"
        onDrop={handleDrop}
        onDragOver={handleDragOver}
      >
        {/* Attachment previews */}
        {attachments.length > 0 && (
          <div className="composer-attachments">
            {attachments.map(att => (
              <div key={att.id} className={`attachment-chip ${att.error ? "attachment-error" : ""} ${att.uploading ? "attachment-uploading" : ""}`}>
                {att.preview ? (
                  <img src={att.preview} alt={att.name} className="attachment-thumb" />
                ) : (
                  <span className="attachment-icon">{renderAttachmentIcon(att)}</span>
                )}
                <span className="attachment-name">{att.name}</span>
                {att.uploading && (
                  <span className="attachment-progress">{att.progress}%</span>
                )}
                <button
                  className="attachment-remove"
                  onClick={() => removeAttachment(att.id)}
                  aria-label={t("composer.removeFile")}
                >
                  <X size={14} />
                </button>
                {/* Upload progress bar */}
                {att.uploading && (
                  <div className="attachment-progress-bar">
                    <div className="attachment-progress-fill" style={{ width: `${att.progress}%` }} />
                  </div>
                )}
              </div>
            ))}
          </div>
        )}

        <div className="gemini-input-row">
          {/* "+" button */}
          {!readOnly && (
            <button
              className="gemini-attach-btn"
              onClick={() => fileInputRef.current?.click()}
              disabled={inputDisabled}
              aria-label={t("composer.addFiles")}
            >
              <Plus size={20} />
            </button>
          )}
          <input
            ref={fileInputRef}
            type="file"
            multiple
            accept="image/*,video/*,audio/*,.pdf"
            onChange={handleFileChange}
            style={{ display: "none" }}
          />

          <textarea
            ref={textareaRef}
            className="gemini-textarea"
            value={text}
            onChange={handleInput}
            onSelect={handleSelect}
            onKeyDown={handleKeyDown}
            onPaste={handlePaste}
            onBlur={handleBlur}
            placeholder={readOnly ? t("composer.viewerReadOnly") : t("composer.askSaker")}
            aria-label={t("composer.send")}
            disabled={inputDisabled}
            rows={1}
          />

          <div className="gemini-right-actions">
            <div className="send-btn-wrapper">
              {running ? (
                <button className="gemini-stop-btn" onClick={onStop} aria-label={t("composer.stop")}>
                  <Square size={16} fill="currentColor" strokeWidth={0} />
                </button>
              ) : !readOnly ? (
                <button
                  className="gemini-send-btn"
                  onClick={handleSend}
                  disabled={inputDisabled || anyUploading}
                  aria-label={t("composer.send")}
                >
                  <Send size={18} />
                </button>
              ) : null}
            </div>
          </div>
        </div>

        {/* Slash command picker */}
        {slashOpen && filteredSkills.length > 0 && (
          <div className="slash-picker" role="listbox" aria-label={t("composer.slashHint")}>
            <div className="slash-picker__header">{t("composer.slashHint")}</div>
            <ul className="slash-picker__list">
              {filteredSkills.map((s, i) => {
                const isHelp = s === HELP_SKILL;
                const isRecent = !isHelp && !slashQuery && recentSkillNames.includes(s.Name);
                return (
                  <li
                    key={s.Name}
                    className={`slash-item${i === slashIndex ? " slash-item--active" : ""}`}
                    role="option"
                    aria-selected={i === slashIndex}
                    onMouseDown={(e) => { e.preventDefault(); handleSelectSkill(s); }}
                    onMouseEnter={() => setSlashIndex(i)}
                  >
                    <span className="slash-item__name">
                      {isHelp ? <HelpCircle size={12} aria-hidden="true" /> :
                        isRecent ? <Clock size={12} aria-hidden="true" /> : null}
                      /{s.Name}
                    </span>
                    {s.Description && (
                      <span className="slash-item__desc">{s.Description}</span>
                    )}
                    {isHelp ? (
                      <span className="slash-item__scope slash-item__scope--help">help</span>
                    ) : s.Scope ? (
                      <span className={`slash-item__scope slash-item__scope--${s.Scope}`}>{s.Scope}</span>
                    ) : null}
                  </li>
                );
              })}
            </ul>
          </div>
        )}
      </div>
      <div className="gemini-disclaimer">
        {t("composer.disclaimer")}
      </div>
    </div>
  );
}
