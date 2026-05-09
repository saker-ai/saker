import { useState, useCallback } from "react";
import { Check } from "lucide-react";
import type { QuestionRequest } from "@/features/rpc/types";
import { useT } from "@/features/i18n";

interface Props {
  question: QuestionRequest;
  onRespond: (id: string, answers: Record<string, string>) => void;
}

export function QuestionCard({ question, onRespond }: Props) {
  const { t } = useT();
  // Track selections per question (keyed by question text).
  const [selections, setSelections] = useState<Record<string, string[]>>({});
  const [otherTexts, setOtherTexts] = useState<Record<string, string>>({});
  const [showOther, setShowOther] = useState<Record<string, boolean>>({});

  const toggleOption = useCallback(
    (qText: string, label: string, multi: boolean) => {
      setSelections((prev) => {
        const current = prev[qText] || [];
        if (multi) {
          const next = current.includes(label)
            ? current.filter((l) => l !== label)
            : [...current, label];
          return { ...prev, [qText]: next };
        }
        // Single select: toggle or replace.
        return { ...prev, [qText]: current[0] === label ? [] : [label] };
      });
      // Clear "Other" when selecting a predefined option in single-select.
      if (!multi) {
        setShowOther((prev) => ({ ...prev, [qText]: false }));
        setOtherTexts((prev) => ({ ...prev, [qText]: "" }));
      }
    },
    []
  );

  const toggleOther = useCallback((qText: string, multi: boolean) => {
    setShowOther((prev) => {
      const next = !prev[qText];
      if (!next) {
        setOtherTexts((p) => ({ ...p, [qText]: "" }));
      }
      return { ...prev, [qText]: next };
    });
    if (!multi) {
      setSelections((prev) => ({ ...prev, [qText]: [] }));
    }
  }, []);

  const canSubmit = question.questions.every((q) => {
    const sel = selections[q.question] || [];
    const other = showOther[q.question] && otherTexts[q.question]?.trim();
    return sel.length > 0 || other;
  });

  const handleSubmit = useCallback(() => {
    if (!canSubmit) return;
    const answers: Record<string, string> = {};
    for (const q of question.questions) {
      const sel = selections[q.question] || [];
      const other = showOther[q.question] ? otherTexts[q.question]?.trim() : "";
      if (other) {
        answers[q.question] = q.multiSelect
          ? [...sel, other].join(", ")
          : other;
      } else {
        answers[q.question] = sel.join(", ");
      }
    }
    onRespond(question.id, answers);
  }, [canSubmit, question, selections, showOther, otherTexts, onRespond]);

  return (
    <div className="question-card">
      {question.questions.map((q, qi) => {
        const selected = selections[q.question] || [];
        const isOtherActive = showOther[q.question] || false;

        return (
          <div key={qi} className="question-item">
            <div className="question-header">{q.header}</div>
            <div className="question-text">{q.question}</div>
            <div className="question-options">
              {q.options.map((opt, oi) => {
                const isSelected = selected.includes(opt.label);
                return (
                  <button
                    key={oi}
                    className={`question-option ${isSelected ? "selected" : ""}`}
                    onClick={() => toggleOption(q.question, opt.label, q.multiSelect)}
                  >
                    <span className="question-option-check">
                      {isSelected && <Check size={14} />}
                    </span>
                    <span className="question-option-content">
                      <span className="question-option-label">{opt.label}</span>
                      {opt.description && (
                        <span className="question-option-desc">{opt.description}</span>
                      )}
                    </span>
                  </button>
                );
              })}
              <button
                className={`question-option question-option-other ${isOtherActive ? "selected" : ""}`}
                onClick={() => toggleOther(q.question, q.multiSelect)}
              >
                <span className="question-option-check">
                  {isOtherActive && <Check size={14} />}
                </span>
                <span className="question-option-label">{t("question.other")}</span>
              </button>
            </div>
            {isOtherActive && (
              <input
                className="question-other-input"
                type="text"
                placeholder={t("question.otherPlaceholder")}
                value={otherTexts[q.question] || ""}
                onChange={(e) =>
                  setOtherTexts((prev) => ({ ...prev, [q.question]: e.target.value }))
                }
                onKeyDown={(e) => {
                  if (e.key === "Enter") handleSubmit();
                }}
                autoFocus
              />
            )}
          </div>
        );
      })}
      <div className="question-actions">
        <button
          className="question-submit-btn"
          onClick={handleSubmit}
          disabled={!canSubmit}
        >
          {t("question.submit")}
        </button>
      </div>
    </div>
  );
}
