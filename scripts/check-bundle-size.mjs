#!/usr/bin/env node
// Bundle-size guard: walks a Next.js static export's chunks, sums per-page
// payloads, and compares against a JSON baseline. Fails CI when any tracked
// metric exceeds the baseline by more than `--threshold` (default 10%).
//
// Usage:
//   node scripts/check-bundle-size.mjs \
//     --dir web-editor-next/out \
//     --baseline web-editor-next/bundle-size-baseline.json \
//     [--update] [--threshold 10]
//
// `--update` rewrites the baseline with the current measurements (no
// comparison performed). Run this when an intentional change is justified.
//
// Why a hand-rolled script instead of bundlesize/size-limit:
//  - Zero npm deps (pure node, ~150 LOC)
//  - Understands Next.js's per-route HTML manifests directly
//  - Same script works for web/ and web-editor-next/ without per-app config
//
// What's measured (in bytes, raw JS — gzip ratio is roughly stable):
//   total      — sum of all .js files in _next/static/chunks/
//   chunkCount — number of .js files (catches code-splitting regressions)
//   routes.<path> — sum of bytes for chunks referenced by <out>/<path>/index.html
//
// Bytes are raw because gzip varies with source positioning; raw bytes give a
// stable signal that's easier to bisect when a regression appears.

import { readFile, readdir, stat, writeFile } from "node:fs/promises";
import path from "node:path";

function parseArgs(argv) {
	const args = { dir: null, baseline: null, update: false, threshold: 10 };
	for (let i = 2; i < argv.length; i++) {
		const a = argv[i];
		if (a === "--dir") args.dir = argv[++i];
		else if (a === "--baseline") args.baseline = argv[++i];
		else if (a === "--update") args.update = true;
		else if (a === "--threshold") args.threshold = Number(argv[++i]);
		else if (a === "--help" || a === "-h") {
			console.log(
				"Usage: check-bundle-size.mjs --dir <out-dir> --baseline <file.json> [--update] [--threshold 10]",
			);
			process.exit(0);
		} else {
			console.error(`unknown arg: ${a}`);
			process.exit(2);
		}
	}
	if (!args.dir || !args.baseline) {
		console.error("--dir and --baseline are required");
		process.exit(2);
	}
	return args;
}

async function findFiles(dir, suffix) {
	const out = [];
	async function walk(d) {
		let entries;
		try {
			entries = await readdir(d, { withFileTypes: true });
		} catch {
			return;
		}
		for (const e of entries) {
			const full = path.join(d, e.name);
			if (e.isDirectory()) await walk(full);
			else if (e.isFile() && full.endsWith(suffix)) out.push(full);
		}
	}
	await walk(dir);
	return out;
}

async function measureChunks(outDir) {
	const chunksDir = path.join(outDir, "_next", "static", "chunks");
	const files = await findFiles(chunksDir, ".js");
	let total = 0;
	const sizeByRel = new Map();
	for (const f of files) {
		const s = await stat(f);
		total += s.size;
		sizeByRel.set(`/${path.relative(outDir, f).replaceAll(path.sep, "/")}`, s.size);
	}
	return { total, chunkCount: files.length, sizeByRel };
}

async function measureRoutes(outDir, sizeByRel) {
	const htmlFiles = await findFiles(outDir, "/index.html");
	const routes = {};
	for (const html of htmlFiles) {
		const route = `/${path
			.relative(outDir, path.dirname(html))
			.replaceAll(path.sep, "/")}/`.replace(/\/+/g, "/");
		const body = await readFile(html, "utf8");
		const refs = new Set();
		for (const m of body.matchAll(/_next\/static\/chunks\/[A-Za-z0-9._-]+\.js/g)) {
			refs.add(`/${m[0]}`);
		}
		let routeTotal = 0;
		for (const ref of refs) {
			routeTotal += sizeByRel.get(ref) ?? 0;
		}
		routes[route] = routeTotal;
	}
	return routes;
}

function compareNumber(label, current, baseline, thresholdPct) {
	if (baseline === undefined) {
		return { label, status: "new", current, baseline: null, deltaPct: null };
	}
	if (baseline === 0) {
		return current === 0
			? { label, status: "ok", current, baseline, deltaPct: 0 }
			: { label, status: "fail", current, baseline, deltaPct: Infinity };
	}
	const deltaPct = ((current - baseline) / baseline) * 100;
	const status = deltaPct > thresholdPct ? "fail" : deltaPct < -thresholdPct ? "shrunk" : "ok";
	return { label, status, current, baseline, deltaPct };
}

function fmtBytes(n) {
	if (n === null) return "—";
	if (n >= 1024 * 1024) return `${(n / 1024 / 1024).toFixed(2)} MB`;
	if (n >= 1024) return `${(n / 1024).toFixed(1)} KB`;
	return `${n} B`;
}

function fmt(n, label) {
	if (n === null) return "—";
	// chunkCount is a count, not a byte size; keep it unitless to avoid
	// confusing readers ("10 B" reads like a tiny chunk).
	if (label === "chunkCount") return String(n);
	return fmtBytes(n);
}

async function main() {
	const args = parseArgs(process.argv);
	const { total, chunkCount, sizeByRel } = await measureChunks(args.dir);
	const routes = await measureRoutes(args.dir, sizeByRel);
	const measurements = { total, chunkCount, routes };

	if (args.update) {
		await writeFile(args.baseline, `${JSON.stringify(measurements, null, 2)}\n`);
		console.log(`updated baseline: ${args.baseline}`);
		console.log(`  total: ${fmtBytes(total)}, chunks: ${chunkCount}, routes: ${Object.keys(routes).length}`);
		return;
	}

	let baseline;
	try {
		baseline = JSON.parse(await readFile(args.baseline, "utf8"));
	} catch (e) {
		console.error(`failed to read baseline ${args.baseline}: ${e.message}`);
		console.error("run with --update to seed a new baseline");
		process.exit(2);
	}

	const results = [
		compareNumber("total", total, baseline.total, args.threshold),
		compareNumber("chunkCount", chunkCount, baseline.chunkCount, args.threshold),
	];
	for (const route of Object.keys(routes)) {
		results.push(
			compareNumber(`routes${route}`, routes[route], baseline.routes?.[route], args.threshold),
		);
	}

	let failed = 0;
	console.log(`bundle-size check: ${args.dir}  (threshold ±${args.threshold}%)`);
	for (const r of results) {
		const arrow =
			r.status === "fail" ? "FAIL" : r.status === "shrunk" ? "SHRUNK" : r.status === "new" ? "NEW" : "ok";
		const delta = r.deltaPct === null ? "n/a" : `${r.deltaPct >= 0 ? "+" : ""}${r.deltaPct.toFixed(1)}%`;
		console.log(`  ${arrow.padEnd(7)} ${r.label.padEnd(28)} ${fmt(r.current, r.label).padStart(10)}  (baseline ${fmt(r.baseline, r.label)}, ${delta})`);
		if (r.status === "fail") failed++;
	}
	if (failed > 0) {
		console.error(`\n${failed} metric(s) exceeded threshold. If intentional, run with --update.`);
		process.exit(1);
	}
	console.log("\nall metrics within threshold.");
}

main().catch((err) => {
	console.error(err);
	process.exit(2);
});
