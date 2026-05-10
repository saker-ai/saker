import { TICKS_PER_SECOND, mediaTime, mediaTimeFromSeconds } from "@/wasm";

export const DEFAULT_NEW_ELEMENT_DURATION = mediaTime({
	ticks: 5 * TICKS_PER_SECOND,
});

export function toElementDurationTicks({
	seconds,
}: {
	seconds: number | null | undefined;
}) {
	if (seconds == null) {
		return DEFAULT_NEW_ELEMENT_DURATION;
	}

	return mediaTimeFromSeconds({ seconds });
}
