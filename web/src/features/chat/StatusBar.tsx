import { useT } from "@/features/i18n";

type Props = {
  connected: boolean;
  turnStatus: "idle" | "running" | "waiting" | "error";
};

export function StatusBar({ connected, turnStatus }: Props) {
  const { t } = useT();
  let label = "";
  let tone = turnStatus;

  if (!connected) {
    label = t("status.disconnected");
    tone = "error";
  } else {
    switch (turnStatus) {
      case "running":
        label = t("status.thinking");
        break;
      case "waiting":
        label = t("status.waiting");
        break;
      case "error":
        label = t("status.error");
        break;
      default:
        label = t("status.ready");
    }
  }

  return (
    <div className={`status-bar tone-${tone}`}>
      <span className="status-dot" />
      <span>{label}</span>
    </div>
  );
}
