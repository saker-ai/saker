// Stores the canvas-side origin node id between import-on-mount and
// export-on-finish. Persisted in sessionStorage so it survives soft-reloads
// (e.g. fast refresh during dev) within the same tab.

const KEY = "saker:editor:originNodeId";

export function setOriginNodeId(id: string | undefined): void {
	if (typeof window === "undefined" || !window.sessionStorage) return;
	if (id) {
		try {
			window.sessionStorage.setItem(KEY, id);
		} catch {
			/* ignore */
		}
	} else {
		try {
			window.sessionStorage.removeItem(KEY);
		} catch {
			/* ignore */
		}
	}
}

export function getOriginNodeId(): string | undefined {
	if (typeof window === "undefined" || !window.sessionStorage) return undefined;
	try {
		return window.sessionStorage.getItem(KEY) ?? undefined;
	} catch {
		return undefined;
	}
}

export function clearOriginNodeId(): void {
	setOriginNodeId(undefined);
}
