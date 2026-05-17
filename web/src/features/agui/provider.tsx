"use client";

import { useMemo, type ReactNode } from "react";
import { CopilotKit } from "@copilotkit/react-core";

function resolveRuntimeUrl(): string {
  if (typeof window === "undefined") return "/v1/agents/run";
  const loc = window.location;
  if (loc.port === "10111") {
    return `${loc.protocol}//${loc.hostname}:10112/v1/agents/run`;
  }
  return "/v1/agents/run";
}

export interface SakerCopilotProviderProps {
  children: ReactNode;
}

export function SakerCopilotProvider({ children }: SakerCopilotProviderProps) {
  const runtimeUrl = useMemo(() => resolveRuntimeUrl(), []);

  return (
    <CopilotKit
      runtimeUrl={runtimeUrl}
      credentials="include"
    >
      {children}
    </CopilotKit>
  );
}
