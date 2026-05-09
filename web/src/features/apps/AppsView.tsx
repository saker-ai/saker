"use client";

import { useState, useEffect } from "react";
import { useAppsStore } from "./appsStore";
import { AppsList } from "./AppsList";
import { AppDetail } from "./AppDetail";

// Read the appId from window.location.hash directly to keep this component
// self-contained and avoid touching parseHash() in ChatApp which expects a
// different two-segment format (#view/threadId).
// Shape: #apps            → list
//        #apps/{appId}    → detail
function readAppIdFromHash(): string {
  if (typeof window === "undefined") return "";
  const raw = window.location.hash.replace("#", "");
  const parts = raw.split("/");
  if (parts[0] === "apps" && parts.length >= 2 && parts[1]) {
    return parts[1];
  }
  return "";
}

function setHash(hash: string) {
  if (typeof window !== "undefined") {
    window.location.hash = hash;
  }
}

export function AppsView() {
  const { refresh } = useAppsStore();
  // Track appId in state so hash changes trigger re-renders.
  const [appId, setAppId] = useState<string>(() => readAppIdFromHash());

  // Load apps on mount.
  useEffect(() => {
    refresh();
  }, [refresh]);

  // Sync appId from hash changes (back/forward buttons).
  useEffect(() => {
    const handler = () => {
      setAppId(readAppIdFromHash());
    };
    window.addEventListener("hashchange", handler);
    return () => window.removeEventListener("hashchange", handler);
  }, []);

  const handleOpen = (id: string) => {
    setHash(`apps/${id}`);
    setAppId(id);
  };

  const handleBack = () => {
    setHash("apps");
    setAppId("");
  };

  const handleDeleted = () => {
    refresh();
    setHash("apps");
    setAppId("");
  };

  return (
    <div
      className="app-content"
      style={{
        height: "100%",
        overflow: "auto",
        display: "flex",
        flexDirection: "column",
      }}
    >
      {appId ? (
        <AppDetail appId={appId} onBack={handleBack} onDeleted={handleDeleted} />
      ) : (
        <div style={{ padding: "24px 28px", flex: 1 }}>
          <AppsList onOpen={handleOpen} />
        </div>
      )}
    </div>
  );
}
