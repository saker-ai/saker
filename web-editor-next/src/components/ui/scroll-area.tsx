import { cn } from "@/utils/ui";
import * as React from "react";

const ScrollArea = React.forwardRef<
	HTMLDivElement,
	React.HTMLAttributes<HTMLDivElement>
>(({ className, children, ...props }, ref) => (
	<div
		ref={ref}
		className={cn("scrollbar-thin overflow-auto", className)}
		{...props}
	>
		{children}
	</div>
));
ScrollArea.displayName = "ScrollArea";

export { ScrollArea };
