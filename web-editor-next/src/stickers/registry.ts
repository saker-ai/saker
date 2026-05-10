import { DefinitionRegistry } from "@/params/registry";
import type { StickerProvider } from "@/stickers/types";

export class StickersRegistry extends DefinitionRegistry<
	string,
	StickerProvider
> {
	constructor() {
		super("sticker provider");
	}
}

export const stickersRegistry = new StickersRegistry();
