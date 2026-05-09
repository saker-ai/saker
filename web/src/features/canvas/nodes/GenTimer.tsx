import { useState, useEffect } from "react";
import { Clock, Timer } from "lucide-react";

function formatDuration(ms: number): string {
  const seconds = Math.floor(ms / 1000);
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  const remaining = seconds % 60;
  return `${minutes}m ${remaining}s`;
}

interface GenTimerProps {
  generating?: boolean;
  startTime?: number;
  endTime?: number;
}

export function GenTimer({ generating, startTime, endTime }: GenTimerProps) {
  const [now, setNow] = useState(Date.now());

  useEffect(() => {
    if (!generating) return;
    const interval = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(interval);
  }, [generating]);

  if (generating && startTime) {
    const elapsed = now - startTime;
    return (
      <div className="gen-timer gen-timer-running">
        <Timer size={11} />
        <span>{formatDuration(elapsed)}</span>
      </div>
    );
  }

  if (!generating && startTime && endTime && endTime > startTime) {
    const duration = endTime - startTime;
    return (
      <div className="gen-timer gen-timer-done">
        <Clock size={11} />
        <span>{formatDuration(duration)}</span>
      </div>
    );
  }

  return null;
}
