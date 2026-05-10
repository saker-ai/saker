import { Button } from "@/components/ui/button";
import { cn } from "@/utils/ui";
import { KeyframeIcon } from "@hugeicons/core-free-icons";
import { HugeiconsIcon } from "@hugeicons/react";

export function KeyframeToggle({
	isActive,
	isDisabled = false,
	title,
	onToggle,
}: {
	isActive: boolean;
	isDisabled?: boolean;
	title: string;
	onToggle: () => void;
}) {
	return (
		<Button
			variant="text"
			aria-pressed={isActive}
			disabled={isDisabled}
			title={title}
			onClick={onToggle}
			className="[&>svg]:size-3.5 mb-0.5"
		>
			<HugeiconsIcon
				icon={KeyframeIcon}
				className={cn(isActive && "text-primary fill-primary")}
			/>
		</Button>
	);
}
