import { API_BASE } from "./sse";

export type TimelineSample = {
  at: number;
  keyholder_count: number;
  union_keyholder_count: number;
};

export async function fetchTimeline(file: File, months = 24): Promise<TimelineSample[]> {
  const res = await fetch(`${API_BASE}/api/audit/timeline?months=${months}`, {
    method: "POST",
    headers: { "X-Lockfile-Name": file.name },
    body: file,
  });
  if (!res.ok) throw new Error(`timeline failed: ${res.status} ${res.statusText}`);
  const body = (await res.json()) as { samples: TimelineSample[] };
  return body.samples;
}
