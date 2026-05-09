export { StorageMigration } from "./base";
export { runStorageMigrations } from "./runner";
export type { MigrationProgress } from "./runner";

export const CURRENT_PROJECT_VERSION = 28;

// `loadMigrations` is the dynamic-import entry for the heavy chain. Callers
// pass it to runStorageMigrations; the runner only invokes it after probing
// IndexedDB and finding a project that actually needs migrating, so a fresh
// install never pays the chain's bundle cost.
export function loadMigrations() {
	return import("./chain").then((m) => m.migrations);
}
