import { marked } from "marked";
import DOMPurify from "dompurify";

// Custom renderer: wrap code blocks with copy button
const renderer = new marked.Renderer();
renderer.code = function ({ text, lang }: { text: string; lang?: string }) {
  const normalizedLang = normalizeCodeLanguage(lang);
  const langClass = normalizedLang ? ` class="language-${normalizedLang}"` : "";
  const langLabel = normalizedLang ? `<span class="code-lang">${escapeHtml(normalizedLang)}</span>` : "";
  return `<div class="code-block-wrapper">${langLabel}<button type="button" class="copy-btn" title="Copy">Copy</button><pre><code${langClass}>${escapeHtml(text)}</code></pre></div>`;
};

marked.setOptions({
  breaks: true,
  gfm: true,
  renderer,
});

/**
 * Render markdown text to sanitized HTML.
 */
export function renderMarkdown(text: string): string {
  if (!text) return "";
  try {
    const raw = marked.parse(text, { async: false }) as string;
    return sanitizeHtml(raw);
  } catch {
    return escapeHtml(text).replace(/\n/g, "<br/>");
  }
}

/**
 * Render externally sourced markdown (e.g. skillhub registry READMEs)
 * with stricter sanitization that does not allow button or class attributes,
 * preventing UI injection from untrusted content authors.
 */
export function renderExternalMarkdown(text: string): string {
  if (!text) return "";
  try {
    const raw = marked.parse(text, { async: false }) as string;
    return sanitizeExternalHtml(raw);
  } catch {
    return escapeHtml(text).replace(/\n/g, "<br/>");
  }
}

function sanitizeHtml(raw: string): string {
  try {
    return DOMPurify.sanitize(raw, {
      ADD_ATTR: ["class", "title"],
      ADD_TAGS: ["button"],
    });
  } catch {
    return escapeHtml(raw).replace(/\n/g, "<br/>");
  }
}

// Stricter sanitization for externally sourced content (skillhub registry,
// third-party READMEs). Does not allow button or class attributes to prevent
// UI injection / phishing vectors from untrusted authors.
export function sanitizeExternalHtml(raw: string): string {
  try {
    return DOMPurify.sanitize(raw);
  } catch {
    return escapeHtml(raw).replace(/\n/g, "<br/>");
  }
}

function normalizeCodeLanguage(lang?: string): string {
  if (!lang) return "";
  return lang
    .trim()
    .toLowerCase()
    .replace(/[^\w#+.-]/g, "");
}

function escapeHtml(text: string): string {
  return text
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}
