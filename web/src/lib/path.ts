import { API_BASE } from "./sse";
import type { PathView } from "./types";

/**
 * Proves reachability from what the project locked to one package a handle holds. Defaults to a
 * single named package, mirroring the CLI path command's unflagged case: proving every package a
 * keyholder holds costs one graph call each, which is tens of seconds for a large portfolio, so the
 * API only computes that many when asked for explicitly.
 */
export async function fetchPath(
  file: File,
  handle: string,
  opts: { package?: string; at?: number | null } = {},
): Promise<PathView> {
  const params = new URLSearchParams({ handle });
  if (opts.package) params.set("package", opts.package);
  if (opts.at) params.set("at", new Date(opts.at * 1000).toISOString().slice(0, 10));

  const res = await fetch(`${API_BASE}/api/audit/path?${params.toString()}`, {
    method: "POST",
    headers: { "X-Lockfile-Name": file.name },
    body: file,
  });
  if (!res.ok) throw new Error(`path failed: ${res.status} ${res.statusText}`);
  return (await res.json()) as PathView;
}
