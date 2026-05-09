"use client";

import { useMemo } from "react";
import {
  Image,
  Sparkles,
  Mic,
  Video,
} from "lucide-react";
import { useT } from "@/features/i18n";

/** Starter prompts when a thread is selected but empty. */
export function StarterState({ onSend }: { onSend: (text: string) => void }) {
  const { t } = useT();
  const prompts = useMemo(() => [
    { text: t("starter.analyzeImage"), icon: <Image size={14} /> },
    { text: t("starter.generateImage"), icon: <Sparkles size={14} /> },
    { text: t("starter.textToSpeech"), icon: <Mic size={14} /> },
    { text: t("starter.analyzeVideo"), icon: <Video size={14} /> },
  ], [t]);
  return (
    <div className="starter-state-v3">
      <div className="starter-prompts">
        {prompts.map((p) => (
          <button
            key={p.text}
            className="gemini-pill-chip"
            onClick={() => onSend(p.text)}
          >
            {p.icon}
            {p.text}
          </button>
        ))}
      </div>
    </div>
  );
}