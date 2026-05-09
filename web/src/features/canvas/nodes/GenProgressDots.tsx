interface GenProgressDotsProps {
  progress?: string;
  total: number;
  generating: boolean;
}

function parseProgress(progress: string | undefined): number {
  if (!progress) return 0;
  const match = progress.match(/^(\d+)\/\d+/);
  return match ? Math.min(parseInt(match[1], 10), Number.MAX_SAFE_INTEGER) : 0;
}

export function GenProgressDots({ progress, total, generating }: GenProgressDotsProps) {
  if (!generating || total <= 1) return null;
  const done = parseProgress(progress);
  const dots = Array.from({ length: total }, (_, i) => i < done);
  return (
    <div className="gen-progress-dots" role="progressbar" aria-valuenow={done} aria-valuemax={total}>
      {dots.map((filled, i) => (
        <span
          key={i}
          className={`gen-progress-dot ${filled ? "filled" : ""} ${!filled && i === done ? "pulsing" : ""}`}
        />
      ))}
      <span className="gen-progress-text">
        {done}/{total}
      </span>
    </div>
  );
}
