import { API_BASE, readEventStream } from "./sse";
import type { Frontier, IncidentTraversalDone } from "./types";

export type IncidentStreamHandlers = {
  onFrontier: (f: Frontier) => void;
  onDone: (result: IncidentTraversalDone) => void;
  onError: (message: string) => void;
};

/**
 * Streams one recorded project's search toward a compromised version live: the blast radius expands
 * hop by hop rather than appearing finished, backed by the same query.Options.OnFrontier mechanism
 * the audit stream uses.
 */
export async function streamIncidentTraversal(
  packageName: string,
  version: string,
  project: string,
  signal: AbortSignal,
  handlers: IncidentStreamHandlers,
): Promise<void> {
  const url = `${API_BASE}/api/incident/${encodeURIComponent(packageName)}/${encodeURIComponent(version)}/stream?project=${encodeURIComponent(project)}`;
  const res = await fetch(url, { signal });

  if (!res.ok || !res.body) {
    handlers.onError(`traversal failed: ${res.status} ${res.statusText}`);
    return;
  }

  await readEventStream(res, (event, data) => {
    switch (event) {
      case "frontier":
        handlers.onFrontier(JSON.parse(data) as Frontier);
        break;
      case "done":
        handlers.onDone(JSON.parse(data) as IncidentTraversalDone);
        break;
      case "error":
        handlers.onError((JSON.parse(data) as { error: string }).error);
        break;
    }
  });
}
