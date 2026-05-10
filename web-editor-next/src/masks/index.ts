import type { Mask, MaskDefaultContext, MaskType } from "@/masks/types";
import { generateUUID } from "@/utils/id";
import { masksRegistry } from "./registry";

export { masksRegistry } from "./registry";
export { registerDefaultMasks } from "./definitions";

export function buildDefaultMaskInstance({
	maskType,
	elementSize,
}: {
	maskType: MaskType;
	elementSize?: { width: number; height: number };
}): Mask {
	const definition = masksRegistry.get(maskType);
	const context: MaskDefaultContext = { elementSize };
	return { ...definition.buildDefault(context), id: generateUUID() } as Mask;
}
