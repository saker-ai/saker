import { registerCanceller } from "@/editor/cancel-interaction";
import { useEditor } from "@/editor/use-editor";
import type { TimelineElement } from "@/timeline";
import {
	type KeyframeDragConfig,
	KeyframeDragController,
	type KeyframeDragState,
} from "@/timeline/controllers/keyframe-drag-controller";
import type { MediaTime } from "@/wasm";
import { useEffect, useReducer, useRef } from "react";
import { useKeyframeSelection } from "./use-keyframe-selection";

export type { KeyframeDragState };

export function useKeyframeDrag({
	zoomLevel,
	element,
	displayedStartTime,
}: {
	zoomLevel: number;
	element: TimelineElement;
	displayedStartTime: MediaTime;
}) {
	const editor = useEditor();
	const {
		selectedKeyframes,
		isKeyframeSelected,
		setKeyframeSelection,
		toggleKeyframeSelection,
		selectKeyframeRange,
	} = useKeyframeSelection();

	const config: KeyframeDragConfig = {
		zoomLevel,
		element,
		displayedStartTime,
		getFps: () => editor.project.getActive()?.settings.fps ?? null,
		selectedKeyframes,
		isKeyframeSelected,
		setKeyframeSelection,
		toggleKeyframeSelection,
		selectKeyframeRange,
		executeCommand: (command) => editor.command.execute({ command }),
		seek: ({ time }) => editor.playback.seek({ time }),
		getTotalDuration: () => editor.timeline.getTotalDuration(),
	};

	const configRef = useRef<KeyframeDragConfig>(config);
	configRef.current = config;

	const controllerRef = useRef<KeyframeDragController | null>(null);
	if (!controllerRef.current) {
		controllerRef.current = new KeyframeDragController({ configRef });
	}
	const controller = controllerRef.current;

	const [, rerender] = useReducer((n: number) => n + 1, 0);
	useEffect(() => controller.subscribe(rerender), [controller]);

	useEffect(() => {
		if (!controller.isActive) return;
		return registerCanceller({ fn: () => controller.cancel() });
	}, [controller.isActive, controller]);

	useEffect(() => () => controller.destroy(), [controller]);

	return {
		keyframeDragState: controller.keyframeDragState,
		handleKeyframeMouseDown: controller.onKeyframeMouseDown,
		handleKeyframeClick: controller.onKeyframeClick,
		getVisualOffsetPx: controller.getVisualOffsetPx,
	};
}
