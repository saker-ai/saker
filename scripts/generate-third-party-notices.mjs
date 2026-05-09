#!/usr/bin/env node
import fs from "node:fs";
import path from "node:path";

const root = process.cwd();
const apps = [
  ["web", "Main web workspace"],
  ["web-editor-next", "Browser video editor"],
];
const licenseOverrides = new Map([
  ["@webav/av-cliper", "MIT"],
  ["@webav/internal-utils", "MIT"],
  ["opfs-tools", "MIT"],
]);

function readJSON(file) {
  return JSON.parse(fs.readFileSync(path.join(root, file), "utf8"));
}

function packageNameFromLockPath(lockPath) {
  return lockPath.replace(/^node_modules\//, "");
}

function collectApp(appDir, label) {
  const pkg = readJSON(`${appDir}/package.json`);
  const lock = readJSON(`${appDir}/package-lock.json`);
  const declared = new Set([
    ...Object.keys(pkg.dependencies || {}),
    ...Object.keys(pkg.devDependencies || {}),
  ]);
  const direct = [];
  const notable = [];

  for (const [lockPath, meta] of Object.entries(lock.packages || {})) {
    if (!lockPath.startsWith("node_modules/")) continue;
    const name = meta.name || packageNameFromLockPath(lockPath);
    const row = {
      name,
      version: meta.version || "",
      license: meta.license || licenseOverrides.get(name) || "UNKNOWN",
    };
    if (declared.has(name)) direct.push(row);
    if (isNotableLicense(row.license)) notable.push(row);
  }

  direct.sort(compareRows);
  notable.sort(compareRows);
  return { appDir, label, direct, notable };
}

function isNotableLicense(license) {
  return /LGPL|GPL|AGPL|CC-BY|CC0|BlueOak|UNKNOWN/i.test(license);
}

function compareRows(a, b) {
  return a.name.localeCompare(b.name);
}

function table(rows) {
  if (rows.length === 0) return "None found.\n";
  return [
    "| Package | Version | License |",
    "| --- | --- | --- |",
    ...rows.map((row) => `| \`${row.name}\` | ${row.version} | ${row.license} |`),
    "",
  ].join("\n");
}

const sections = apps.map(([dir, label]) => collectApp(dir, label));
const generatedAt = new Date().toISOString().slice(0, 10);

let out = `# Third-Party Notices

Generated from \`package-lock.json\` metadata on ${generatedAt}.

This inventory is a release aid, not a substitute for a full legal review. The
root project license is MIT; third-party dependencies keep their own licenses.

## Local In-Tree Dependencies

| Component | Path | License |
| --- | --- | --- |
| aigo | \`godeps/aigo\` | MIT |
| OpenCut-derived editor code | \`web-editor-next\` | MIT |
| flag icon assets | \`web-editor-next/public/flags\` | MIT |

## Direct npm Dependencies

`;

for (const section of sections) {
  out += `### ${section.label} (\`${section.appDir}\`)\n\n`;
  out += table(section.direct);
  out += "\n";
}

out += `## Notable Transitive npm Licenses

These are licenses that are not in the common MIT/Apache/BSD/ISC/MPL permissive
set and should stay visible in release notes and audits.

`;

for (const section of sections) {
  out += `### ${section.label} (\`${section.appDir}\`)\n\n`;
  out += table(section.notable);
  out += "\n";
}

fs.mkdirSync(path.join(root, "docs"), { recursive: true });
fs.writeFileSync(path.join(root, "docs/third-party-notices.md"), out);
console.log("wrote docs/third-party-notices.md");
