import type { ApprovalRequest } from "@/features/rpc/types";
import { useT } from "@/features/i18n";

interface Props {
  approval: ApprovalRequest;
  onRespond: (id: string, decision: "allow" | "deny") => void;
}

function highlightJson(json: string): string {
  return json.replace(
    /("(?:\\.|[^"\\])*")\s*:/g,
    '<span class="json-key">$1</span>:'
  ).replace(
    /:\s*("(?:\\.|[^"\\])*")/g,
    ': <span class="json-string">$1</span>'
  ).replace(
    /:\s*(\d+(?:\.\d+)?)/g,
    ': <span class="json-number">$1</span>'
  ).replace(
    /:\s*(true|false)/g,
    ': <span class="json-bool">$1</span>'
  ).replace(
    /:\s*(null)/g,
    ': <span class="json-null">$1</span>'
  );
}

export function ApprovalCard({ approval, onRespond }: Props) {
  const { t } = useT();
  return (
    <div className="approval-card">
      <div className="tool-info">
        <strong>{approval.tool_name}</strong> {t("approval.requiresApproval")}
      </div>
      {approval.reason && (
        <div className="approval-reason">{approval.reason}</div>
      )}
      {approval.tool_params &&
        Object.keys(approval.tool_params).length > 0 && (
          <div
            className="approval-params"
            dangerouslySetInnerHTML={{
              __html: highlightJson(
                JSON.stringify(approval.tool_params, null, 2)
                  .replace(/&/g, "&amp;")
                  .replace(/</g, "&lt;")
                  .replace(/>/g, "&gt;")
              ),
            }}
          />
        )}
      <div className="actions">
        <button
          className="btn-allow"
          onClick={() => onRespond(approval.id, "allow")}
        >
          {t("approval.allow")}
        </button>
        <button
          className="btn-deny"
          onClick={() => onRespond(approval.id, "deny")}
        >
          {t("approval.deny")}
        </button>
      </div>
    </div>
  );
}
