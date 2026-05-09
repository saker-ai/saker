#!/usr/bin/env node
// Static server for the exported editor bundle. It mirrors the Go server's
// /editor/ mount so the editor can be checked without rebuilding the backend.
import http from "node:http";
import { createReadStream, promises as fs } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const outDir = path.resolve(__dirname, "..", "out");
const port = parseInt(process.env.PORT || "10112", 10);
const basePath = "/editor";

const mime = {
  ".css": "text/css; charset=utf-8",
  ".html": "text/html; charset=utf-8",
  ".ico": "image/x-icon",
  ".js": "application/javascript; charset=utf-8",
  ".json": "application/json",
  ".map": "application/json",
  ".png": "image/png",
  ".svg": "image/svg+xml",
  ".txt": "text/plain; charset=utf-8",
  ".webp": "image/webp",
  ".woff2": "font/woff2",
};

function safeJoin(root, urlPath) {
  const decoded = decodeURIComponent(urlPath.split("?")[0] || "/");
  const resolved = path.normalize(path.join(root, decoded));
  return resolved.startsWith(root) ? resolved : null;
}

const server = http.createServer(async (req, res) => {
  let url = req.url || "/";
  if (url === basePath || url.startsWith(`${basePath}/`) || url.startsWith(`${basePath}?`)) {
    url = url.slice(basePath.length) || "/";
  } else if (url === "/") {
    res.writeHead(302, { location: `${basePath}/` });
    res.end();
    return;
  }

  const file = safeJoin(outDir, url);
  if (!file) {
    res.writeHead(403);
    res.end("Forbidden");
    return;
  }

  try {
    let target = file;
    let stat = await fs.stat(target);
    if (stat.isDirectory()) {
      target = path.join(target, "index.html");
      stat = await fs.stat(target);
    }
    res.writeHead(200, {
      "content-length": stat.size,
      "content-type": mime[path.extname(target)] || "application/octet-stream",
    });
    createReadStream(target).pipe(res);
  } catch {
    res.writeHead(404);
    res.end("Not Found");
  }
});

server.listen(port, () => {
  console.log(`serve-export: http://localhost:${port}${basePath}/`);
});
