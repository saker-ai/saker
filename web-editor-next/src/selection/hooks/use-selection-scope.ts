import { useSelectionContext } from "@/selection/context";
import { type ScopeEntry, activateScope } from "@/selection/scope";
import { useEffect, useRef } from "react";

export function useSelectionScope() {
	const { selectedIds, clearSelection } = useSelectionContext();
	const hasSelectionRef = useRef(selectedIds.length > 0);
	const clearSelectionRef = useRef(clearSelection);
	const hasSelection = selectedIds.length > 0;

	hasSelectionRef.current = hasSelection;
	clearSelectionRef.current = clearSelection;

	const entryRef = useRef<ScopeEntry>({
		hasSelection: () => hasSelectionRef.current,
		clear: () => {
			clearSelectionRef.current();
		},
	});

	useEffect(() => {
		if (!hasSelection) {
			return;
		}

		return activateScope({ entry: entryRef.current });
	}, [hasSelection]);
}
