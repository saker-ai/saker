import { useState, useEffect } from "react";
import { useRpcStore } from "@/features/rpc/rpcStore";

export interface ToolSchemaInfo {
  engines: string[];
  sizes?: string[];
  resolutions?: string[];
  aspectRatios?: string[];
  cameraAngles?: string[];
  voices?: string[];
  languages?: string[];
}

/**
 * Shared hook to load tool schema (engine list and optional enum fields) from the server.
 * If engine is provided, it fetches engine-specific capabilities.
 * Used by ImageNode, VideoNode, ImageGenNode, VideoGenNode, VoiceGenNode.
 */
export function useToolSchema(toolName: string, engine?: string) {
  const [schema, setSchema] = useState<ToolSchemaInfo | null>(null);
  const [defaultEngine, setDefaultEngine] = useState("");

  useEffect(() => {
    const rpc = useRpcStore.getState().rpc;
    if (!rpc) return;
    rpc.request<{
      name: string;
      schema: { properties?: Record<string, { enum?: string[] }> };
      engines?: string[];
    }>("tool/schema", { toolName, engine })
      .then((res) => {
        const engines = res.engines || [];
        const props = res.schema?.properties || {};
        const sizes = props.size?.enum || [];
        const resolutions = props.resolution?.enum || [];
        const aspectRatios = props.aspect_ratio?.enum || [];
        const cameraAngles = props.camera_angle?.enum || [];
        const voices = props.voice?.enum || [];
        const languages = props.language?.enum || [];
        setSchema({ engines, sizes, resolutions, aspectRatios, cameraAngles, voices, languages });
        if (engines.length > 0 && !defaultEngine) {
          setDefaultEngine(engines[0]);
        }
      })
      .catch(() => setSchema({ engines: [] }));
  }, [toolName, engine, defaultEngine]);

  return { schema, defaultEngine };
}
