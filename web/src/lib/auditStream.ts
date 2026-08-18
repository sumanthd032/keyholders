import type { AuditView, Frontier } from "./types";

const API_BASE = process.env.NEXT_PUBLIC_API_BASE ?? "http://127.0.0.1:8090";

export type AuditStreamHandlers = {
  onFrontier: (f: Frontier) => void;
  onDone: (a: AuditView) => void;
  onError: (message: string) => void;
};

/**
 * Streams an audit over the API's SSE endpoint. The browser's EventSource cannot send a POST body
 * or a custom header, so this parses the text/event-stream framing by hand off a fetch response
 * body: split on blank lines for frame boundaries, then "event:"/"data:" lines within each frame.
 * This is what lets the client watch the same wavefronts the server discovers, in the order the
 * server actually discovers them, rather than waiting for one finished payload.
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

  const reader = res.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";

  for (;;) {
    const { value, done } = await reader.read();
    if (done) break;
    buffer += decoder.decode(value, { stream: true });

    let boundary: number;
    while ((boundary = buffer.indexOf("\n\n")) !== -1) {
      const frame = buffer.slice(0, boundary);
      buffer = buffer.slice(boundary + 2);
      dispatchFrame(frame, handlers);
    }
  }
}

function dispatchFrame(frame: string, handlers: AuditStreamHandlers) {
  let event = "message";
  let data = "";
  for (const line of frame.split("\n")) {
    if (line.startsWith("event: ")) event = line.slice("event: ".length);
    else if (line.startsWith("data: ")) data = line.slice("data: ".length);
  }
  if (!data) return;

  switch (event) {
    case "frontier":
      handlers.onFrontier(JSON.parse(data) as Frontier);
      break;
    case "done":
      handlers.onDone(JSON.parse(data) as AuditView);
      break;
    case "error": {
      const parsed = JSON.parse(data) as { error: string };
      handlers.onError(parsed.error);
      break;
    }
  }
}
