import { useTranslation } from "react-i18next";
import { collapse, diffLines } from "../diff";

// FileDiff shows the changed file line by line against the RUNNING state —
// what gets judged is the change that accepting produces.
export function FileDiff({ file, before, after }: { file: string; before: string; after: string }) {
  const { t } = useTranslation();
  const chunks = collapse(diffLines(before, after));
  return (
    <div className="diff mb-2">
      <div className="diff-head mono">
        {file}
        {before === "" && <span className="muted"> · {t("improvements.newFile")}</span>}
      </div>
      <pre className="diff-body">
        {chunks.map((c, i) =>
          c.kind === "skip" ? (
            <span key={i} className="diff-skip">
              {t("improvements.skipped", { count: c.skipped })}
            </span>
          ) : (
            <span key={i} className={`diff-line ${c.kind}`}>
              {c.kind === "add" ? "+" : c.kind === "del" ? "−" : " "} {c.text}
            </span>
          ),
        )}
      </pre>
    </div>
  );
}
