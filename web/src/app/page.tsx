"use client";

import dynamic from "next/dynamic";
import { ThemeProvider } from "@/features/chat/ThemeProvider";
import { I18nProvider, useT } from "@/features/i18n";
import { useAuth } from "@/features/auth/useAuth";
import { useEditorBridge } from "@/features/editor-bridge";

// Lazy-load the heavy ChatApp bundle (ReactFlow + all node types + settings panels).
const ChatApp = dynamic(() => import("@/features/chat/ChatApp").then(m => ({ default: m.ChatApp })), {
  ssr: false,
  loading: () => <div className="auth-loading"><div className="auth-loading__spinner" /></div>,
});

function AppContent() {
  const auth = useAuth();
  const { t } = useT();
  useEditorBridge({ importedLabel: (t("canvas.editor.imported" as any) as string) || "Imported from editor" });

  if (auth.loading) {
    return <div className="auth-loading"><div className="auth-loading__spinner" /></div>;
  }

  return (
    <ChatApp
      authRequired={auth.required}
      authenticated={auth.authenticated}
      onLogin={auth.login}
      onLogout={auth.logout}
      authProviders={auth.providers}
      onOidcLogin={auth.oidcLogin}
    />
  );
}

export default function Home() {
  return (
    <ThemeProvider>
      <I18nProvider>
        <AppContent />
      </I18nProvider>
    </ThemeProvider>
  );
}
