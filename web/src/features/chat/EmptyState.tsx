"use client";

import { useState, useEffect, useMemo } from "react";
import { motion } from "framer-motion";
import {
  Eye,
  Sparkles,
  Volume2,
  Film,
} from "lucide-react";
import { resolveWsUrl } from "@/features/rpc/client";
import type { SkillInfo } from "@/features/rpc/types";
import { ParticleBackground } from "./ParticleBackground";
import { useT } from "@/features/i18n";

/** Empty state when no thread is selected — decorative header with example cards. */
export function EmptyState({
  connected,
  skills,
  onExampleClick,
}: {
  connected: boolean;
  skills?: SkillInfo[];
  onExampleClick?: (text: string) => void;
}) {
  const { t } = useT();
  const [wsDisplay, setWsDisplay] = useState("");
  useEffect(() => {
    setWsDisplay(resolveWsUrl().replace("ws://", "").replace("wss://", "").replace("/ws", ""));
  }, []);

  const examples = useMemo(() => [
    { text: t("starter.analyzeImage"), icon: <Eye size={18} /> },
    { text: t("starter.generateImage"), icon: <Sparkles size={18} /> },
    { text: t("starter.textToSpeech"), icon: <Volume2 size={18} /> },
    { text: t("starter.analyzeVideo"), icon: <Film size={18} /> },
  ], [t]);

  return (
    <>
    <ParticleBackground />
    <div className="empty-state-v3">
      <motion.div
        className="hero-section"
        initial={{ opacity: 0, y: 12 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ duration: 0.5, delay: 0 }}
      >
        <h1 className="hero-title">{t("empty.heroTitle")}<span className="hero-brand">Saker</span></h1>
        <p className="hero-subtitle">{t("empty.heroSubtitle")}</p>
      </motion.div>

      <motion.div
        className="home-examples"
        initial={{ opacity: 0, y: 12 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ duration: 0.5, delay: 0.1 }}
      >
        {examples.map((ex, i) => (
          <button key={i} className="home-example-card" onClick={() => onExampleClick?.(ex.text)}>
            <div className="home-example-icon">{ex.icon}</div>
            <div className="home-example-text">{ex.text}</div>
          </button>
        ))}
      </motion.div>

      {!connected && wsDisplay && (
        <p className="empty-hint empty-hint--danger">
          {t("empty.disconnectedFrom")} {wsDisplay}
        </p>
      )}
    </div>
    </>
  );
}
