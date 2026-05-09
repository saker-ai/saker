import { useRpcStore } from "@/features/rpc/rpcStore";

interface TaskResult {
  success: boolean;
  output: string;
  structured?: { media_url?: string; media_type?: string };
}

interface TaskStatus {
  id: string;
  status: "running" | "done" | "error";
  result?: TaskResult;
  error?: string;
}

/**
 * Submit a tool/run request and poll until completion.
 * Returns the same shape as the old synchronous tool/run response.
 * Pass an AbortSignal to cancel polling (e.g., on component unmount).
 */
export async function submitAndPollTask(
  toolName: string,
  params: Record<string, unknown>,
  nodeId?: string,
  pollInterval = 2000,
  signal?: AbortSignal,
): Promise<TaskResult> {
  const rpc = useRpcStore.getState().rpc;
  if (!rpc) throw new Error("RPC not connected");

  const { taskId } = await rpc.request<{ taskId: string }>("tool/run", {
    toolName,
    params,
    nodeId,
  });

  return pollTask(taskId, pollInterval, 300, signal);
}

/**
 * Poll an existing task until completion. Used for resuming after page refresh.
 * maxPolls limits total iterations to prevent infinite loops (default: 300).
 * Backoff escalates only after the first few polls to avoid slow initial response detection.
 */
export async function pollTask(
  taskId: string,
  pollInterval = 2000,
  maxPolls = 300,
  signal?: AbortSignal,
): Promise<TaskResult> {
  const rpc = useRpcStore.getState().rpc;
  if (!rpc) throw new Error("RPC not connected");

  // Use fixed interval for the first 5 polls, then exponential backoff
  const FAST_POLLS = 5;

  for (let i = 0; i < maxPolls; i++) {
    if (signal?.aborted) throw new Error("Task polling aborted");

    const status = await rpc.request<TaskStatus>("tool/task-status", { taskId });

    if (status.status === "done" && status.result) {
      return status.result;
    }
    if (status.status === "error") {
      throw new Error(status.error || "Task failed");
    }

    const delay = i < FAST_POLLS
      ? pollInterval
      : Math.min(pollInterval * Math.pow(1.5, i - FAST_POLLS), 15000);

    await new Promise<void>((resolve, reject) => {
      const timer = setTimeout(resolve, delay);
      signal?.addEventListener("abort", () => {
        clearTimeout(timer);
        reject(new Error("Task polling aborted"));
      }, { once: true });
    });
  }

  throw new Error("Task polling timed out");
}

/**
 * Fetch all active/recent tasks from the server.
 */
export async function fetchActiveTasks(): Promise<TaskStatus[]> {
  const rpc = useRpcStore.getState().rpc;
  if (!rpc) return [];

  try {
    const { tasks } = await rpc.request<{ tasks: TaskStatus[] }>("tool/active-tasks", {});
    return tasks || [];
  } catch {
    return [];
  }
}
