export const API_BASE = process.env.NEXT_PUBLIC_API_BASE ?? "http://127.0.0.1:8090";

/**
 * Reads a fetch response body as text/event-stream frames, calling onEvent for each one. Shared by
 * every streaming endpoint this project has: the browser's EventSource cannot send a POST body or a
 * custom header, so every stream here goes over plain fetch and gets its framing parsed by hand
 * instead, split on blank lines for frame boundaries, then "event:"/"data:" lines within each one.
 */
export async function readEventStream(
  res: Response,
  onEvent: (event: string, data: string) => void,
): Promise<void> {
  if (!res.body) return;

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
      dispatchFrame(frame, onEvent);
    }
  }
}

function dispatchFrame(frame: string, onEvent: (event: string, data: string) => void) {
  let event = "message";
  let data = "";
  for (const line of frame.split("\n")) {
    if (line.startsWith("event: ")) event = line.slice("event: ".length);
    else if (line.startsWith("data: ")) data = line.slice("data: ".length);
  }
  if (data) onEvent(event, data);
}
