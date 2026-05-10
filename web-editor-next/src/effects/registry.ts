import type { EffectDefinition } from "@/effects/types";
import { DefinitionRegistry } from "@/params/registry";

export class EffectsRegistry extends DefinitionRegistry<
	string,
	EffectDefinition
> {
	constructor() {
		super("effect");
	}
}

export const effectsRegistry = new EffectsRegistry();
