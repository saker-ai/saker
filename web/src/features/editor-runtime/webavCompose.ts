import type { ProgressCallback } from "./progress";

export type ComposeInputType = "video" | "audio" | "image";

export interface ComposeInput {
  url: string;
  label?: string;
  /** Defaults to "video" for back-compat. */
  type?: ComposeInputType;
  /** Trim start, microseconds from clip start. Defaults to 0. */
  trimStartUs?: number;
  /** Trim end, microseconds from clip start. Defaults to clip duration. */
  trimEndUs?: number;
  /**
   * For image inputs: how long the still frame stays on screen, in microseconds.
   * Ignored for video/audio. Defaults to ComposeOpts.imageDurationUs (5s).
   */
  imageDurationUs?: number;
}

export interface ComposeOpts {
  /** Output canvas width; defaults to first clip's natural width. */
  width?: number;
  /** Output canvas height; defaults to first clip's natural height. */
  height?: number;
  /** Output frame rate; defaults to 30. */
  fps?: number;
  /** Output bitrate; defaults to 5_000_000. */
  bitrate?: number;
  /** Default still-frame duration for image inputs (microseconds). Default 5_000_000. */
  imageDurationUs?: number;
  /**
   * Per-visual-clip fade-in / fade-out duration in microseconds. Capped at 40%
   * of the clip's duration. `0` (default) disables transitions.
   */
  transitionUs?: number;
  /** 0..1 progress reported by Combinator.OutputProgress. */
  onProgress?: ProgressCallback;
  /** Abort the compose pipeline. Cancels stream draining and destroys Combinator. */
  signal?: AbortSignal;
}

export class ComposeUnsupportedError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "ComposeUnsupportedError";
  }
}

export class ComposeCorsError extends Error {
  readonly url: string;
  constructor(url: string, cause?: unknown) {
    super(`Cannot fetch ${url} (CORS or network error)`);
    this.name = "ComposeCorsError";
    this.url = url;
    if (cause !== undefined) (this as { cause?: unknown }).cause = cause;
  }
}

/**
 * Detect if the current browser supports the WebAV pipeline.
 * Cheap synchronous gate before user clicks the button.
 */
export function isComposeSupported(): boolean {
  if (typeof window === "undefined") return false;
  return (
    "VideoDecoder" in window &&
    "VideoEncoder" in window &&
    "OffscreenCanvas" in window
  );
}

async function applyTrim<T extends { split: (t: number) => Promise<[T, T]> }>(
  clip: T,
  startUs: number,
  endUs: number,
): Promise<T> {
  let working = clip;
  if (startUs > 0) {
    const [, tail] = await working.split(startUs);
    working = tail;
  }
  const wantDur = endUs - startUs;
  if (wantDur > 0) {
    try {
      const [head] = await working.split(wantDur);
      working = head;
    } catch {
      // If split-at-end fails (e.g. wantDur ≥ remaining), keep `working` as-is.
    }
  }
  return working;
}

async function fetchAsClipStream(
  url: string,
  signal?: AbortSignal,
): Promise<ReadableStream<Uint8Array>> {
  let res: Response;
  try {
    res = await fetch(url, { mode: "cors", credentials: "omit", signal });
  } catch (err) {
    if ((err as { name?: string })?.name === "AbortError") throw err;
    throw new ComposeCorsError(url, err);
  }
  if (!res.ok) throw new ComposeCorsError(url, new Error(`HTTP ${res.status}`));
  if (!res.body) throw new ComposeCorsError(url, new Error("response has no body"));
  return res.body;
}

/**
 * Concatenate N video URLs into a single mp4 Blob using WebAV's Combinator.
 *
 * - Sources must allow CORS reads (or be same-origin / blob/data URLs).
 * - Output dimensions default to the first clip's meta size; later clips are
 *   centered/letterboxed by the Combinator.
 * - Calls `onProgress(0..1)` while the WebAV encoder runs.
 */
export async function composeToMp4Blob(
  inputs: ComposeInput[],
  opts: ComposeOpts = {},
): Promise<Blob> {
  if (inputs.length === 0) throw new Error("composeToMp4Blob: no inputs");

  if (!isComposeSupported()) {
    throw new ComposeUnsupportedError(
      "Browser lacks WebCodecs / OffscreenCanvas — please use Chrome or Edge 113+",
    );
  }

  const { Combinator, MP4Clip, AudioClip, ImgClip, OffscreenSprite } = await import(
    "@webav/av-cliper"
  );

  const supported = await Combinator.isSupported({
    width: opts.width,
    height: opts.height,
    bitrate: opts.bitrate,
  });
  if (!supported) {
    throw new ComposeUnsupportedError(
      "Combinator.isSupported returned false — codec or hardware unsupported",
    );
  }

  const imgDefaultDur = opts.imageDurationUs ?? 5_000_000;

  type PreparedClip =
    | { kind: "video"; clip: InstanceType<typeof MP4Clip>; durationUs: number }
    | { kind: "image"; clip: InstanceType<typeof ImgClip>; durationUs: number }
    | { kind: "audio"; clip: InstanceType<typeof AudioClip>; durationUs: number };

  // Prepare every clip in parallel — fetch + WebAV-init are both I/O bound,
  // doing them sequentially turns N round-trips into N×latency. Keep input
  // order via index-preserving map. Errors are collected (allSettled) so a
  // single bad CORS source doesn't mask other failures.
  type Prep = PreparedClip | { kind: "skip" };
  const prepResults = await Promise.allSettled(
    inputs.map<Promise<Prep>>(async (input) => {
      if (opts.signal?.aborted) throw new DOMException("Aborted", "AbortError");
      const inferredType =
        input.type ?? (/\.(mp3|wav|m4a|aac|ogg|flac)(\?|$)/i.test(input.url)
          ? "audio"
          : /\.(png|jpe?g|gif|webp|bmp|avif)(\?|$)/i.test(input.url)
            ? "image"
            : "video");
      const body = await fetchAsClipStream(input.url, opts.signal);

      if (inferredType === "video") {
        let clip = new MP4Clip(body);
        await clip.ready;
        const naturalDur = clip.meta.duration;
        const start = Math.max(0, Math.min(input.trimStartUs ?? 0, naturalDur));
        const end = Math.max(start, Math.min(input.trimEndUs ?? naturalDur, naturalDur));
        if (start > 0 || end < naturalDur) clip = await applyTrim(clip, start, end);
        return { kind: "video", clip, durationUs: end - start };
      }
      if (inferredType === "image") {
        const blob = await new Response(body).blob();
        const bitmap = await createImageBitmap(blob);
        const clip = new ImgClip(bitmap);
        await clip.ready;
        return { kind: "image", clip, durationUs: input.imageDurationUs ?? imgDefaultDur };
      }
      const clip = new AudioClip(body);
      await clip.ready;
      const naturalDur = clip.meta.duration;
      const start = Math.max(0, Math.min(input.trimStartUs ?? 0, naturalDur));
      const end = Math.max(start, Math.min(input.trimEndUs ?? naturalDur, naturalDur));
      return { kind: "audio", clip, durationUs: end - start };
    }),
  );

  const visualClips: PreparedClip[] = [];
  const audioClips: PreparedClip[] = [];
  const failures: Array<{ url: string; error: unknown }> = [];

  prepResults.forEach((r, i) => {
    const input = inputs[i]!;
    if (r.status === "rejected") {
      failures.push({ url: input.url, error: r.reason });
      return;
    }
    const item = r.value;
    if (item.kind === "skip") return;
    if (item.kind === "video" || item.kind === "image") {
      visualClips.push(item);
    } else {
      audioClips.push(item);
    }
  });

  // Derive default canvas size from the first video clip (TS-friendly: no closure-mutated let).
  const firstVideo = visualClips.find((c) => c.kind === "video");
  const firstVideoMeta =
    firstVideo?.kind === "video"
      ? { width: firstVideo.clip.meta.width, height: firstVideo.clip.meta.height }
      : null;

  if (failures.length > 0 && visualClips.length === 0 && audioClips.length === 0) {
    // Surface the first CORS-typed error (most actionable) but include all
    // failed urls in the message so the user sees the full picture.
    const firstCors = failures.find((f) => f.error instanceof ComposeCorsError);
    const all = failures.map((f) => f.url).join(", ");
    if (firstCors) {
      throw new ComposeCorsError(all, (firstCors.error as ComposeCorsError).cause);
    }
    throw failures[0]!.error;
  }
  if (failures.length > 0) {
    // Partial failure: log so the developer sees it, but proceed with the
    // clips we did manage to prepare.
    console.warn(
      `[webavCompose] ${failures.length}/${inputs.length} inputs failed:`,
      failures.map((f) => ({ url: f.url, error: (f.error as Error)?.message })),
    );
  }

  const width = opts.width ?? firstVideoMeta?.width ?? 1280;
  const height = opts.height ?? firstVideoMeta?.height ?? 720;
  const fps = opts.fps ?? 30;
  const bitrate = opts.bitrate ?? 5_000_000;

  const com = new Combinator({ width, height, fps, bitrate });

  // Visual track: video + image, sequential
  const transitionUs = Math.max(0, opts.transitionUs ?? 0);
  let visualOffsetUs = 0;
  for (const item of visualClips) {
    if (opts.signal?.aborted) {
      com.destroy();
      throw new DOMException("Aborted", "AbortError");
    }
    const sprite = new OffscreenSprite(item.clip);
    sprite.time = { offset: visualOffsetUs, duration: item.durationUs };
    if (transitionUs > 0 && item.durationUs > 0) {
      const fade = Math.min(transitionUs, Math.floor(item.durationUs * 0.4));
      if (fade > 0) {
        const inPct = ((fade / item.durationUs) * 100).toFixed(2);
        const outPct = (100 - (fade / item.durationUs) * 100).toFixed(2);
        sprite.setAnimation(
          {
            "0%": { opacity: 0 },
            [`${inPct}%`]: { opacity: 1 },
            [`${outPct}%`]: { opacity: 1 },
            "100%": { opacity: 0 },
          },
          { duration: item.durationUs, iterCount: 1 },
        );
      }
    }
    await com.addSprite(sprite);
    visualOffsetUs += item.durationUs;
  }
  // Audio track: layered, sequential starting at 0
  let audioOffsetUs = 0;
  for (const item of audioClips) {
    if (opts.signal?.aborted) {
      com.destroy();
      throw new DOMException("Aborted", "AbortError");
    }
    const sprite = new OffscreenSprite(item.clip);
    sprite.time = { offset: audioOffsetUs, duration: item.durationUs };
    await com.addSprite(sprite, { main: false });
    audioOffsetUs += item.durationUs;
  }
  if (visualClips.length === 0 && audioClips.length === 0) {
    com.destroy();
    throw new Error("composeToMp4Blob: no usable clips after type inference");
  }

  let unbindProgress: (() => void) | null = null;
  if (opts.onProgress) {
    unbindProgress = com.on("OutputProgress", (p) => {
      try {
        opts.onProgress?.(p);
      } catch {
        // user callback errors must not break the pipeline
      }
    });
  }

  let abortListener: (() => void) | null = null;
  let aborted = false;
  if (opts.signal) {
    abortListener = () => {
      aborted = true;
      try {
        com.destroy();
      } catch {
        /* ignore */
      }
    };
    opts.signal.addEventListener("abort", abortListener, { once: true });
  }

  try {
    const stream = com.output();
    const reader = stream.getReader();
    const chunks: Uint8Array[] = [];
    let total = 0;
    while (true) {
      const { value, done } = await reader.read();
      if (done) break;
      if (value) {
        chunks.push(value);
        total += value.byteLength;
      }
      if (opts.signal?.aborted || aborted) {
        try {
          await reader.cancel();
        } catch {
          /* ignore */
        }
        throw new DOMException("Aborted", "AbortError");
      }
    }

    const merged = new Uint8Array(total);
    let cursor = 0;
    for (const chunk of chunks) {
      merged.set(chunk, cursor);
      cursor += chunk.byteLength;
    }
    return new Blob([merged], { type: "video/mp4" });
  } finally {
    unbindProgress?.();
    if (opts.signal && abortListener) {
      opts.signal.removeEventListener("abort", abortListener);
    }
    try {
      com.destroy();
    } catch {
      /* ignore */
    }
  }
}

/**
 * Convenience wrapper: compose then trigger a browser download.
 */
export async function composeAndDownload(
  inputs: ComposeInput[],
  filename: string,
  opts?: ComposeOpts,
): Promise<Blob> {
  const blob = await composeToMp4Blob(inputs, opts);
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = filename.endsWith(".mp4") ? filename : `${filename}.mp4`;
  document.body.appendChild(a);
  a.click();
  document.body.removeChild(a);
  setTimeout(() => URL.revokeObjectURL(url), 1500);
  return blob;
}
