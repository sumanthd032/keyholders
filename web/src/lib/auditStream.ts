import { API_BASE, readEventStream } from "./sse";
import type { AuditView, Frontier } from "./types";

export type AuditStreamHandlers = {
  onFrontier: (f: Frontier) => void;
  onDone: (a: AuditView) => void;
  onError: (message: string) => void;
};

/**
 * Runs the identical lockfile through the plain, non-streaming audit endpoint. Used for the Exposure
 * River's scrubbing, which re-resolves the whole view at a dragged instant: replaying the full
 * streaming search, and with it the Roll Call reveal, on every drag tick would turn a scrub into
 * repeated opening moments instead of a smooth re-query, so scrubbing intentionally does not go
 * through streamAudit.
 */
export async function fetchAudit(file: File, at?: number): Promise<AuditView> {
  const params = new URLSearchParams();
  if (at) params.set("at", new Date(at * 1000).toISOString().slice(0, 10));
  const qs = params.toString();

  const res = await fetch(`${API_BASE}/api/audit${qs ? `?${qs}` : ""}`, {
    method: "POST",
    headers: { "X-Lockfile-Name": file.name },
    body: file,
  });
  if (!res.ok) throw new Error(`audit failed: ${res.status} ${res.statusText}`);
  return (await res.json()) as AuditView;
}

/**
 * Streams an audit over the API's SSE endpoint. This is what lets the client watch the same
 * wavefronts the server discovers, in the order the server actually discovers them, rather than
 * waiting for one finished payload.
 */
export async function streamAudit(
  file: File,
  signal: AbortSignal,
  handlers: AuditStreamHandlers,
): Promise<void> {
  const res = await fetch(`${API_BASE}/api/audit/stream`, {
    method: "POST",
    headers: { "X-Lockfile-Name": file.name },
    body: file,
    signal,
  });

  if (!res.ok || !res.body) {
    handlers.onError(`audit failed: ${res.status} ${res.statusText}`);
    return;
  }

  await readEventStream(res, (event, data) => {
    switch (event) {
      case "frontier":
        handlers.onFrontier(JSON.parse(data) as Frontier);
        break;
      case "done":
        handlers.onDone(JSON.parse(data) as AuditView);
        break;
      case "error":
        handlers.onError((JSON.parse(data) as { error: string }).error);
        break;
    }
  });
}
