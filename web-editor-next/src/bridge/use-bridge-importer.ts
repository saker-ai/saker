"use client";

import { useEditor } from "@/editor/use-editor";
import type { ProcessedMediaAsset } from "@/media/processing";
import { useRouter, useSearchParams } from "next/navigation";
import { useEffect, useRef } from "react";
import { toast } from "sonner";
import { setOriginNodeId } from "./origin";
import { type EditorAsset, readImportFromQuery } from "./protocol";

// Dynamic import keeps mediabunny + opencut-wasm out of the initial editor
// shell bundle. Bridge import is a one-time, post-mount flow triggered only
// when ?import=... is present, so paying the chunk cost here is fine and
// avoids hanging the editor shell on a stalled wasm fetch.
type ProcessFn = (args: {
	files: FileList | File[];
	onProgress?: ({ progress }: { progress: number }) => void;
}) => Promise<ProcessedMediaAsset[]>;

let processFnPromise: Promise<ProcessFn> | null = null;
async function loadProcessMediaAssets(): Promise<ProcessFn> {
	if (!processFnPromise) {
		processFnPromise = import("@/media/processing").then(
			(m) => m.processMediaAssets as ProcessFn,
		);
	}
	return processFnPromise;
}

const MIME_BY_TYPE: Record<EditorAsset["type"], string> = {
	video: "video/mp4",
	audio: "audio/mpeg",
	image: "image/png",
};

const EXT_BY_MIME: Record<string, string> = {
	"video/mp4": "mp4",
	"video/webm": "webm",
	"video/quicktime": "mov",
	"audio/mpeg": "mp3",
	"audio/wav": "wav",
	"audio/ogg": "ogg",
	"image/png": "png",
	"image/jpeg": "jpg",
	"image/webp": "webp",
};

function makeFilename({
	asset,
	contentType,
	index,
}: {
	asset: EditorAsset;
	contentType: string;
	index: number;
}): string {
	if (asset.label && asset.label.length > 0) {
		// Strip slashes the editor doesn't tolerate in display names.
		const safe = asset.label.replace(/[/\\]/g, "_");
		const ext = EXT_BY_MIME[contentType] ?? asset.type;
		// Add extension if label doesn't already have one matching the MIME.
		if (!/\.[a-zA-Z0-9]{2,5}$/.test(safe)) return `${safe}.${ext}`;
		return safe;
	}
	const ext = EXT_BY_MIME[contentType] ?? asset.type;
	return `bridge-${index + 1}.${ext}`;
}

interface FetchFailure {
	kind: "fetch";
	url: string;
	status?: number;
	cors: boolean;
	cause: unknown;
}

interface ProcessFailure {
	kind: "process";
	url: string;
	cause: unknown;
}

interface SaveFailure {
	kind: "save";
	url: string;
	cause: unknown;
}

type AssetFailure = FetchFailure | ProcessFailure | SaveFailure;

function describeFailure(failure: AssetFailure): string {
	const tail =
		failure.cause instanceof Error
			? failure.cause.message
			: String(failure.cause ?? "unknown error");
	if (failure.kind === "fetch") {
		if (failure.cors) {
			return `Cross-origin fetch blocked for ${failure.url}. The source server must send Access-Control-Allow-Origin, or the URL must be same-origin.`;
		}
		if (failure.status) {
			return `Fetch failed (HTTP ${failure.status}) for ${failure.url}`;
		}
		return `Fetch failed for ${failure.url}: ${tail}`;
	}
	if (failure.kind === "process") {
		return `Could not process media at ${failure.url}: ${tail}`;
	}
	return `Could not save ${failure.url} into the editor library: ${tail}`;
}

async function fetchAssetAsFile({
	asset,
	index,
	signal,
}: {
	asset: EditorAsset;
	index: number;
	signal: AbortSignal;
}): Promise<File> {
	console.log("[bridge-importer] fetch start", {
		url: asset.url,
		type: asset.type,
	});
	let res: Response;
	try {
		// Same-origin URLs (relative or matching window.origin) succeed without
		// CORS; cross-origin URLs require Access-Control-Allow-Origin on the
		// source. We catch and re-throw with a CORS hint so the toast can
		// explain it instead of saying "Failed to fetch".
		res = await fetch(asset.url, {
			mode: "cors",
			signal,
			credentials: "same-origin",
		});
	} catch (cause) {
		// Browser obscures CORS rejection as a generic TypeError. Heuristic:
		// if URL parses to a different origin than the page, treat as CORS.
		let cors = false;
		try {
			const u = new URL(asset.url, window.location.href);
			cors = u.origin !== window.location.origin;
		} catch {
			/* relative URL — same origin */
		}
		const failure: FetchFailure = {
			kind: "fetch",
			url: asset.url,
			cors,
			cause,
		};
		throw failure;
	}
	if (!res.ok) {
		const failure: FetchFailure = {
			kind: "fetch",
			url: asset.url,
			status: res.status,
			cors: false,
			cause: new Error(`HTTP ${res.status} ${res.statusText}`),
		};
		throw failure;
	}
	const blob = await res.blob();
	const contentType =
		blob.type || res.headers.get("content-type") || MIME_BY_TYPE[asset.type];
	const name = makeFilename({ asset, contentType, index });
	console.log("[bridge-importer] fetch ok", {
		url: asset.url,
		size: blob.size,
		contentType,
		name,
	});
	return new File([blob], name, { type: contentType });
}

function withTimeout<T>(p: Promise<T>, ms: number, label: string): Promise<T> {
	return new Promise((resolve, reject) => {
		const timer = setTimeout(
			() => reject(new Error(`${label} timed out after ${ms}ms`)),
			ms,
		);
		p.then(
			(v) => {
				clearTimeout(timer);
				resolve(v);
			},
			(err) => {
				clearTimeout(timer);
				reject(err);
			},
		);
	});
}

function isAssetFailure(value: unknown): value is AssetFailure {
	return (
		typeof value === "object" &&
		value !== null &&
		"kind" in value &&
		"url" in value &&
		(["fetch", "process", "save"] as const).includes(
			(value as { kind: AssetFailure["kind"] }).kind,
		)
	);
}

/**
 * Read `?import=...` from the URL on mount, download each asset and inject
 * it into the active project's media library. Strips the query param so
 * a refresh doesn't re-import. The originNodeId is stashed in
 * sessionStorage for the export pipeline to echo back to the canvas.
 *
 * Each asset is processed independently — partial failures surface per-asset
 * toasts with actionable error descriptions so the user knows whether the
 * problem was CORS, a missing file, or a downstream processing issue.
 */
export function useBridgeImporter(): void {
	const editor = useEditor();
	const activeProject = useEditor((e) => e.project.getActiveOrNull());
	const router = useRouter();
	const searchParams = useSearchParams();
	const consumedRef = useRef(false);

	useEffect(() => {
		if (consumedRef.current) return;
		if (!activeProject) return;
		const search = searchParams.toString();
		if (!search.includes("import=")) return;

		const payload = readImportFromQuery(`?${search}`);
		if (!payload || payload.assets.length === 0) {
			consumedRef.current = true;
			const next = new URLSearchParams(search);
			next.delete("import");
			const qs = next.toString();
			router.replace(qs ? `?${qs}` : "?");
			return;
		}

		consumedRef.current = true;
		setOriginNodeId(payload.originNodeId);

		const controller = new AbortController();
		const projectId = activeProject.metadata.id;
		const total = payload.assets.length;
		const loadingId = toast.loading(
			total === 1
				? "Importing 1 asset from canvas..."
				: `Importing ${total} assets from canvas...`,
		);

		const failures: AssetFailure[] = [];
		const importedNames: string[] = [];

		(async () => {
			let processMediaAssets: ProcessFn;
			try {
				processMediaAssets = await withTimeout(
					loadProcessMediaAssets(),
					15_000,
					"load media processor",
				);
			} catch (err) {
				console.error("[bridge-importer] processor load failed", err);
				toast.dismiss(loadingId);
				toast.error("Bridge import failed", {
					description:
						err instanceof Error
							? `Could not load media processor: ${err.message}`
							: "Could not load media processor",
					duration: 12_000,
				});
				const next = new URLSearchParams(search);
				next.delete("import");
				const qs = next.toString();
				router.replace(qs ? `?${qs}` : "?");
				return;
			}

			// Process assets concurrently with a small pool. Three feels right:
			// the bottleneck is usually mediabunny decode (CPU-bound) rather
			// than network, and going wider just thrashes the main thread.
			// Each worker walks the next index off a shared cursor until done.
			const CONCURRENCY = 3;
			let cursor = 0;
			const processOne = async (i: number) => {
				if (controller.signal.aborted) return;
				const asset = payload.assets[i];
				let file: File;
				try {
					file = await withTimeout(
						fetchAssetAsFile({
							asset,
							index: i,
							signal: controller.signal,
						}),
						30_000,
						`fetch ${asset.url}`,
					);
				} catch (err) {
					console.error("[bridge-importer] fetch failed", asset.url, err);
					if (controller.signal.aborted) return;
					if (isAssetFailure(err)) failures.push(err);
					else
						failures.push({
							kind: "fetch",
							url: asset.url,
							cors: false,
							cause: err,
						});
					return;
				}

				let processed: ProcessedMediaAsset[];
				try {
					console.log(
						"[bridge-importer] process start",
						file.name,
						file.size,
						file.type,
					);
					processed = await withTimeout(
						processMediaAssets({ files: [file] }),
						60_000,
						`process ${file.name}`,
					);
					console.log("[bridge-importer] process ok", processed.length);
				} catch (err) {
					console.error("[bridge-importer] process failed", asset.url, err);
					failures.push({ kind: "process", url: asset.url, cause: err });
					return;
				}
				if (processed.length === 0) {
					console.warn(
						"[bridge-importer] process returned 0 assets — likely rejected by media-utils",
					);
					failures.push({
						kind: "process",
						url: asset.url,
						cause: new Error(
							"asset was rejected (unsupported type or out of storage)",
						),
					});
					return;
				}

				try {
					for (const a of processed) {
						console.log("[bridge-importer] save start", a.name);
						const saved = await withTimeout(
							editor.media.addMediaAsset({ projectId, asset: a }),
							30_000,
							`save ${a.name}`,
						);
						console.log("[bridge-importer] save result", a.name, !!saved);
						if (saved) importedNames.push(a.name);
					}
				} catch (err) {
					console.error("[bridge-importer] save failed", asset.url, err);
					failures.push({ kind: "save", url: asset.url, cause: err });
				}
			};

			const worker = async () => {
				while (!controller.signal.aborted) {
					const i = cursor++;
					if (i >= payload.assets.length) return;
					await processOne(i);
				}
			};

			try {
				await Promise.all(
					Array.from(
						{ length: Math.min(CONCURRENCY, payload.assets.length) },
						() => worker(),
					),
				);

				toast.dismiss(loadingId);
				if (importedNames.length === total && failures.length === 0) {
					toast.success(
						total === 1
							? `${importedNames[0]} imported`
							: `${total} assets imported from canvas`,
					);
				} else if (importedNames.length > 0) {
					toast.warning(
						`Imported ${importedNames.length}/${total} assets — ${failures.length} failed`,
						{
							description: failures.map(describeFailure).join("\n\n"),
							duration: 12_000,
						},
					);
				} else {
					toast.error(
						total === 1 ? "Bridge import failed" : "All bridge imports failed",
						{
							description: failures.map(describeFailure).join("\n\n"),
							duration: 12_000,
						},
					);
				}
			} catch (err) {
				if (controller.signal.aborted) return;
				toast.dismiss(loadingId);
				toast.error("Bridge import crashed", {
					description: err instanceof Error ? err.message : String(err),
					duration: 12_000,
				});
				console.error("[bridge-importer] unexpected", err);
			} finally {
				const next = new URLSearchParams(search);
				next.delete("import");
				const qs = next.toString();
				router.replace(qs ? `?${qs}` : "?");
			}
		})();

		return () => {
			controller.abort();
			toast.dismiss(loadingId);
		};
	}, [editor, activeProject, router, searchParams]);
}
