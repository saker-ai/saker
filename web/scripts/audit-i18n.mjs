#!/usr/bin/env node
/**
 * i18n audit script for src/features/i18n/index.tsx
 *
 * Reports:
 *   ERROR  - missing keys (t("X") called but not in dict)
 *   ERROR  - empty translations (en or zh is missing/empty)
 *   ERROR  - duplicate keys in the dict literal
 *   WARN   - unused keys (in dict but never referenced)
 *   INFO   - dynamic t(`...${var}...`) calls (listed for manual review)
 *
 * Suppression: add  // audit-ignore  anywhere on the same source line to
 * exclude that line from key extraction (both dict-side and call-site).
 *
 * Heuristic for "relevant file": only files that import from a path
 * containing "i18n" are scanned for t("...") calls.  This avoids false
 * positives from unrelated t() calls in third-party or non-i18n code.
 */

import fs from "fs";
import path from "path";
import { fileURLToPath } from "url";

// ---------------------------------------------------------------------------
// ANSI helpers
// ---------------------------------------------------------------------------
const C = {
  reset: "\x1b[0m",
  bold: "\x1b[1m",
  red: "\x1b[31m",
  yellow: "\x1b[33m",
  green: "\x1b[32m",
  cyan: "\x1b[36m",
  gray: "\x1b[90m",
};
const red = (s) => `${C.red}${s}${C.reset}`;
const yellow = (s) => `${C.yellow}${s}${C.reset}`;
const green = (s) => `${C.green}${s}${C.reset}`;
const cyan = (s) => `${C.cyan}${s}${C.reset}`;
const gray = (s) => `${C.gray}${s}${C.reset}`;
const bold = (s) => `${C.bold}${s}${C.reset}`;

// ---------------------------------------------------------------------------
// Paths
// ---------------------------------------------------------------------------
const __dirname = path.dirname(fileURLToPath(import.meta.url));
const ROOT = path.resolve(__dirname, "..");
const I18N_FILE = path.join(ROOT, "src/features/i18n/index.tsx");
const SRC_DIR = path.join(ROOT, "src");

// ---------------------------------------------------------------------------
// 1. Parse the dict from i18n/index.tsx
// ---------------------------------------------------------------------------
function parseDict(src) {
  // Extract the dict object literal between `const dict = {` and `} as const`
  const dictStart = src.indexOf("const dict = {");
  if (dictStart === -1) throw new Error("Could not find `const dict = {` in i18n file");

  // Find matching closing brace
  let depth = 0;
  let i = src.indexOf("{", dictStart);
  const start = i;
  while (i < src.length) {
    if (src[i] === "{") depth++;
    else if (src[i] === "}") {
      depth--;
      if (depth === 0) break;
    }
    i++;
  }
  const dictBody = src.slice(start + 1, i); // contents between outer { }

  /**
   * dict entries look like:
   *   "some.key": { en: "...", zh: "..." },
   * Keys may contain dots and hyphens.
   * We parse line-by-line to also detect duplicates.
   */
  const entries = new Map(); // key -> { en, zh, line }
  const duplicates = []; // { key, lines: [n, n] }
  const seenLines = new Map(); // key -> first line number (relative to dictBody)

  // Split into lines and track approximate line numbers (offset by where dict starts)
  const dictStartLine = src.slice(0, dictStart).split("\n").length;
  const lines = dictBody.split("\n");

  // Regex: match the key line  "some.key": {
  const keyLineRe = /^\s*"([^"]+)"\s*:\s*\{/;
  // Regex: match en/zh inside the entry block (single-line or next lines)
  const enRe = /\ben\s*:\s*"((?:[^"\\]|\\.)*)"/;
  const zhRe = /\bzh\s*:\s*"((?:[^"\\]|\\.)*)"/;

  let currentKey = null;
  let currentLineNo = dictStartLine + 1;
  let entryLines = [];

  function flushEntry() {
    if (!currentKey) return;
    const block = entryLines.join("\n");
    const enMatch = enRe.exec(block);
    const zhMatch = zhRe.exec(block);
    const en = enMatch ? enMatch[1] : null;
    const zh = zhMatch ? zhMatch[1] : null;
    const lineNo = seenLines.get(currentKey);

    if (entries.has(currentKey)) {
      duplicates.push({ key: currentKey, lines: [entries.get(currentKey).line, lineNo] });
    } else {
      entries.set(currentKey, { en, zh, line: lineNo });
    }
    currentKey = null;
    entryLines = [];
  }

  for (let idx = 0; idx < lines.length; idx++) {
    const line = lines[idx];
    const lineNo = dictStartLine + idx + 1;

    // Skip audit-ignore lines on the dict side
    if (line.includes("// audit-ignore")) {
      currentLineNo = lineNo + 1;
      continue;
    }

    const keyMatch = keyLineRe.exec(line);
    if (keyMatch) {
      flushEntry();
      currentKey = keyMatch[1];
      seenLines.set(currentKey, lineNo);
      entryLines = [line];
    } else if (currentKey) {
      entryLines.push(line);
      // If entry closes on this line (contains } ,), flush
      if (/\}\s*,?\s*$/.test(line.trim()) && !line.includes("{")) {
        flushEntry();
      }
    }
  }
  flushEntry();

  return { entries, duplicates };
}

// ---------------------------------------------------------------------------
// 2. Walk src/ for .ts/.tsx files
// ---------------------------------------------------------------------------
function walkSrc(dir) {
  const results = [];
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      results.push(...walkSrc(full));
    } else if (entry.isFile() && /\.(tsx?|mts)$/.test(entry.name)) {
      results.push(full);
    }
  }
  return results;
}

// ---------------------------------------------------------------------------
// 3. Check if a file imports from i18n
// ---------------------------------------------------------------------------
function importsI18n(src) {
  return /from\s+['"][^'"]*i18n[^'"]*['"]/.test(src);
}

// ---------------------------------------------------------------------------
// 4. Extract t("...") calls from source (skip comments, skip template literals
//    with interpolation)
// ---------------------------------------------------------------------------
function extractTCalls(src, filePath) {
  const staticKeys = new Set();
  const dynamicCalls = [];

  const lines = src.split("\n");
  for (let lineIdx = 0; lineIdx < lines.length; lineIdx++) {
    const line = lines[lineIdx];
    const lineNo = lineIdx + 1;

    // Skip lines with audit-ignore
    if (line.includes("// audit-ignore")) continue;

    // Strip single-line comments (naive but good enough for low false-positive rate)
    // Only strip // comments that aren't inside a string
    const noComment = line.replace(/(?<!['":`])\/\/.*$/, "");

    // Find all t("..."), t('...'), t(`...`) patterns
    // We use a generator-style loop over regex matches
    const callRe = /\bt\s*\(\s*(`[^`]*`|"[^"]*"|'[^']*')\s*(?:as\s+\w+)?\s*\)/g;
    let m;
    while ((m = callRe.exec(noComment)) !== null) {
      const raw = m[1];
      if (raw.startsWith("`")) {
        // Template literal
        const inner = raw.slice(1, -1);
        if (inner.includes("${")) {
          // Dynamic key — record for DYNAMIC section
          dynamicCalls.push({
            expr: raw,
            file: path.relative(ROOT, filePath),
            line: lineNo,
          });
        } else {
          // Static template literal (no interpolation)
          staticKeys.add(inner);
        }
      } else {
        // Regular string
        const inner = raw.slice(1, -1);
        staticKeys.add(inner);
      }
    }
  }

  return { staticKeys, dynamicCalls };
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------
function main() {
  let errors = 0;
  let warnings = 0;

  // --- Parse dict ---
  const i18nSrc = fs.readFileSync(I18N_FILE, "utf8");
  const { entries: dictEntries, duplicates } = parseDict(i18nSrc);

  // --- Walk files ---
  const allFiles = walkSrc(SRC_DIR);
  const relevantFiles = allFiles.filter((f) => {
    if (f === I18N_FILE) return false;
    const src = fs.readFileSync(f, "utf8");
    return importsI18n(src);
  });

  // --- Collect all static refs and dynamic calls ---
  const allStaticRefs = new Set(); // union of all t("key") across files
  const allDynamic = []; // { expr, file, line }

  for (const f of relevantFiles) {
    const src = fs.readFileSync(f, "utf8");
    const { staticKeys, dynamicCalls } = extractTCalls(src, f);
    for (const k of staticKeys) allStaticRefs.add(k);
    allDynamic.push(...dynamicCalls);
  }

  // --- Compute categories ---
  const missingKeys = [...allStaticRefs].filter((k) => !dictEntries.has(k)).sort();

  const emptyTranslations = [];
  for (const [key, val] of dictEntries) {
    const enEmpty = !val.en || val.en.trim() === "";
    const zhEmpty = !val.zh || val.zh.trim() === "";
    if (enEmpty || zhEmpty) {
      emptyTranslations.push({ key, missingEn: enEmpty, missingZh: zhEmpty, line: val.line });
    }
  }

  const unusedKeys = [...dictEntries.keys()]
    .filter((k) => !allStaticRefs.has(k))
    .sort();

  // ---------------------------------------------------------------------------
  // Output
  // ---------------------------------------------------------------------------
  const section = (title, color) =>
    console.log(`\n${color}${bold("━━ " + title + " ━━")}${C.reset}`);

  console.log(bold(cyan("\n[i18n-audit] Scanning…")));
  console.log(gray(`  Dict file : ${path.relative(ROOT, I18N_FILE)}`));
  console.log(gray(`  Dict keys : ${dictEntries.size}`));
  console.log(gray(`  Scanned   : ${relevantFiles.length} files (importing i18n)`));
  console.log(gray(`  Refs found: ${allStaticRefs.size} unique static keys`));

  // --- Duplicates ---
  if (duplicates.length > 0) {
    section(`DUPLICATE KEYS (${duplicates.length})`, C.red);
    for (const d of duplicates) {
      console.log(red(`  ERROR  duplicate key "${d.key}" first at line ${d.lines[0]}, again at line ${d.lines[1]}`));
      errors++;
    }
  }

  // --- Missing keys ---
  if (missingKeys.length > 0) {
    section(`MISSING KEYS — referenced but not in dict (${missingKeys.length})`, C.red);
    for (const k of missingKeys) {
      console.log(red(`  ERROR  "${k}" is used in code but not defined in dict`));
      errors++;
    }
  }

  // --- Empty translations ---
  if (emptyTranslations.length > 0) {
    section(`EMPTY TRANSLATIONS (${emptyTranslations.length})`, C.red);
    for (const e of emptyTranslations) {
      const missing = [e.missingEn && "en", e.missingZh && "zh"].filter(Boolean).join(", ");
      console.log(red(`  ERROR  "${e.key}" missing ${missing} (dict line ~${e.line})`));
      errors++;
    }
  }

  // --- Unused keys ---
  if (unusedKeys.length > 0) {
    section(`UNUSED KEYS — in dict but never referenced (${unusedKeys.length})`, C.yellow);
    for (const k of unusedKeys) {
      console.log(yellow(`  WARN   "${k}"`));
      warnings++;
    }
  }

  // --- Dynamic calls ---
  if (allDynamic.length > 0) {
    section(`DYNAMIC t(\`...\${var}...\`) CALLS — verify prefix families manually (${allDynamic.length})`, C.cyan);
    for (const d of allDynamic) {
      console.log(cyan(`  DYNAMIC  ${d.expr}  ${gray(`${d.file}:${d.line}`)}`));
    }
  }

  // --- Summary ---
  console.log("");
  if (errors === 0 && warnings === 0) {
    console.log(green(bold("[i18n-audit] PASS")) + gray(" — no issues found"));
  } else if (errors === 0) {
    console.log(
      yellow(bold(`[i18n-audit] PASS`)) +
        gray(` — 0 errors, ${warnings} warning${warnings !== 1 ? "s" : ""}`)
    );
  } else {
    console.log(
      red(bold(`[i18n-audit] FAIL: ${errors} error${errors !== 1 ? "s" : ""}, ${warnings} warning${warnings !== 1 ? "s" : ""}`))
    );
  }
  console.log("");

  process.exit(errors > 0 ? 1 : 0);
}

main();
