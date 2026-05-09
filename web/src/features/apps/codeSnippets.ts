import type { AppInputField } from "./appsApi";

export interface SnippetContext {
  baseUrl: string;
  appId: string;
  projectId?: string;
  apiKey?: string;
  inputs: AppInputField[];
  /** Placeholder shown when no apiKey is provided. Caller should pass an
   *  i18n-translated string like t("apps.codeSnippetApiKeyHint"). Defaults
   *  to the English angle-bracket form for back-compat. */
  apiKeyPlaceholder?: string;
}

function buildRunUrl(ctx: SnippetContext): string {
  const base = ctx.baseUrl.replace(/\/$/, "");
  if (ctx.projectId) {
    return `${base}/api/apps/${ctx.projectId}/${ctx.appId}/run`;
  }
  return `${base}/api/apps/${ctx.appId}/run`;
}

function inputPlaceholder(field: AppInputField): unknown {
  switch (field.type) {
    case "number":
      return 0;
    case "select":
      return field.options?.[0] ?? "";
    default:
      return "example";
  }
}

function buildInputsObject(inputs: AppInputField[]): Record<string, unknown> {
  const obj: Record<string, unknown> = {};
  for (const field of inputs) {
    obj[field.variable] = inputPlaceholder(field);
  }
  return obj;
}

export function curlSnippet(ctx: SnippetContext): string {
  const url = buildRunUrl(ctx);
  const key = ctx.apiKey ?? ctx.apiKeyPlaceholder ?? "<your-api-key>";
  const body = JSON.stringify({ inputs: buildInputsObject(ctx.inputs) });
  return [
    `curl -X POST '${url}' \\`,
    `  -H 'Authorization: Bearer ${key}' \\`,
    `  -H 'Content-Type: application/json' \\`,
    `  -d '${body}'`,
  ].join("\n");
}

export function jsSnippet(ctx: SnippetContext): string {
  const url = buildRunUrl(ctx);
  const key = ctx.apiKey ?? ctx.apiKeyPlaceholder ?? "<your-api-key>";
  const inputs = buildInputsObject(ctx.inputs);
  const bodyLiteral = JSON.stringify({ inputs }, null, 2)
    .split("\n")
    .map((line, i) => (i === 0 ? line : "  " + line))
    .join("\n");
  return [
    `const res = await fetch('${url}', {`,
    `  method: 'POST',`,
    `  headers: {`,
    `    'Authorization': 'Bearer ${key}',`,
    `    'Content-Type': 'application/json'`,
    `  },`,
    `  body: JSON.stringify(${bodyLiteral})`,
    `});`,
    `const { runId } = await res.json();`,
  ].join("\n");
}

export function pythonSnippet(ctx: SnippetContext): string {
  const url = buildRunUrl(ctx);
  const key = ctx.apiKey ?? ctx.apiKeyPlaceholder ?? "<your-api-key>";
  const body = JSON.stringify({ inputs: buildInputsObject(ctx.inputs) });
  return [
    `import requests`,
    `resp = requests.post(`,
    `    '${url}',`,
    `    headers={'Authorization': 'Bearer ${key}', 'Content-Type': 'application/json'},`,
    `    json=${body}`,
    `)`,
    `print(resp.json())`,
  ].join("\n");
}
