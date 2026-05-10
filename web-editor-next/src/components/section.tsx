"use client";

import { cn } from "@/utils/ui";
import { ChevronDown } from "lucide-react";
import {
	type ComponentPropsWithoutRef,
	type ReactNode,
	createContext,
	useContext,
	useState,
} from "react";

type SectionContextValue = {
	collapsible: boolean;
	open: boolean;
	toggle: () => void;
};

const SectionContext = createContext<SectionContextValue>({
	collapsible: false,
	open: true,
	toggle: () => {},
});

export interface SectionProps extends ComponentPropsWithoutRef<"div"> {
	collapsible?: boolean;
	defaultOpen?: boolean;
	showTopBorder?: boolean;
	showBottomBorder?: boolean;
	sectionKey?: string;
	children?: ReactNode;
}

export function Section({
	collapsible = false,
	defaultOpen = true,
	showTopBorder = true,
	showBottomBorder = false,
	sectionKey: _sectionKey,
	className,
	children,
	...rest
}: SectionProps) {
	const [open, setOpen] = useState(defaultOpen);
	const toggle = () => setOpen((prev) => !prev);

	return (
		<SectionContext.Provider
			value={{
				collapsible,
				open: collapsible ? open : true,
				toggle,
			}}
		>
			<div
				className={cn(
					"flex flex-col",
					showTopBorder && "border-t",
					showBottomBorder && "border-b",
					className,
				)}
				{...rest}
			>
				{children}
			</div>
		</SectionContext.Provider>
	);
}

export interface SectionHeaderProps extends ComponentPropsWithoutRef<"div"> {
	trailing?: ReactNode;
	children?: ReactNode;
}

export function SectionHeader({
	className,
	trailing,
	children,
	...rest
}: SectionHeaderProps) {
	const { collapsible, open, toggle } = useContext(SectionContext);
	return (
		<div
			className={cn(
				"flex items-center justify-between px-3 py-2",
				collapsible && "cursor-pointer select-none",
				className,
			)}
			onClick={collapsible ? toggle : undefined}
			{...rest}
		>
			<div className="flex-1">{children}</div>
			<div
				className="flex items-center gap-1"
				onClick={(event) => event.stopPropagation()}
				onKeyDown={(event) => event.stopPropagation()}
			>
				{trailing}
			</div>
			{collapsible && (
				<ChevronDown
					className={cn(
						"size-3.5 text-muted-foreground transition-transform",
						!open && "-rotate-90",
					)}
				/>
			)}
		</div>
	);
}

export interface SectionTitleProps extends ComponentPropsWithoutRef<"div"> {
	children?: ReactNode;
}

export function SectionTitle({
	className,
	children,
	...rest
}: SectionTitleProps) {
	return (
		<div
			className={cn("text-sm font-medium text-foreground", className)}
			{...rest}
		>
			{children}
		</div>
	);
}

export interface SectionContentProps extends ComponentPropsWithoutRef<"div"> {
	children?: ReactNode;
}

export function SectionContent({
	className,
	children,
	...rest
}: SectionContentProps) {
	const { open } = useContext(SectionContext);
	if (!open) return null;
	return (
		<div className={cn("flex flex-col gap-2 px-3 pb-3", className)} {...rest}>
			{children}
		</div>
	);
}

export interface SectionFieldsProps extends ComponentPropsWithoutRef<"div"> {
	children?: ReactNode;
}

export function SectionFields({
	className,
	children,
	...rest
}: SectionFieldsProps) {
	return (
		<div className={cn("flex flex-row flex-wrap gap-2", className)} {...rest}>
			{children}
		</div>
	);
}

export interface SectionFieldProps extends ComponentPropsWithoutRef<"div"> {
	label?: ReactNode;
	beforeLabel?: ReactNode;
	afterLabel?: ReactNode;
	children?: ReactNode;
}

export function SectionField({
	label,
	beforeLabel,
	afterLabel,
	className,
	children,
	...rest
}: SectionFieldProps) {
	const hasHeader = label !== undefined || beforeLabel || afterLabel;
	return (
		<div className={cn("flex flex-col gap-1", className)} {...rest}>
			{hasHeader && (
				<div className="flex items-center gap-1 text-xs text-muted-foreground">
					{beforeLabel}
					{label !== undefined && <span>{label}</span>}
					{afterLabel}
				</div>
			)}
			{children}
		</div>
	);
}
