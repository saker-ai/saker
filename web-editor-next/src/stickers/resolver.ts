import type { StickerResolveOptions } from "@/stickers/types";
import { registerDefaultStickerProviders } from "./providers";
import { stickersRegistry } from "./registry";
import { parseStickerId } from "./sticker-id";

export function resolveStickerId({
	stickerId,
	options,
}: {
	stickerId: string;
	options?: StickerResolveOptions;
}): string {
	registerDefaultStickerProviders();

	const parsedStickerId = parseStickerId({ stickerId });
	return stickersRegistry.get(parsedStickerId.providerId).resolveUrl({
		stickerId,
		options,
	});
}
