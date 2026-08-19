"use client";

import { useEffect, useState } from "react";
import { API_BASE } from "@/lib/sse";

type LeaderboardEntry = {
  name: string;
  kri: number;
  kri_at: number;
  error_band: number;
};

type ObservatoryView = {
  maintainers: LeaderboardEntry[];
  packages: LeaderboardEntry[];
};

/**
 * The observatory: reach across the whole ingested graph, not one project's lockfile. Every number
 * here is a HyperLogLog estimate, not an exact count the way the audit view's are, so every one of
 * them renders its error band directly beside it rather than looking identical to an exact value.
 * The orphaned-but-load-bearing view and a KRI curve over multiple epochs are not served by the API
 * yet; this renders what is actually available rather than a placeholder standing in for them.
 */
export function ObservatoryDashboard() {
  const [data, setData] = useState<ObservatoryView | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    fetch(`${API_BASE}/api/observatory`)
      .then((r) => {
        if (!r.ok) throw new Error(`${r.status} ${r.statusText}`);
        return r.json();
      })
      .then(setData)
      .catch((err) => setError(String(err)));
  }, []);

  if (error) {
    return (
      <div className="flex-1 flex items-center justify-center px-8" role="alert">
        <p className="text-sm text-danger">{error}</p>
      </div>
    );
  }

  if (!data) {
    return (
      <div className="flex-1 flex items-center justify-center px-8" aria-live="polite">
        <p className="text-sm text-ramp-40">loading the last observatory run&apos;s results</p>
      </div>
    );
  }

  return (
    <div className="flex-1 overflow-y-auto px-8 py-6 flex flex-col gap-10">
      <Leaderboard title="maintainer reach leaderboard" entries={data.maintainers} />
      <Leaderboard title="package reach leaderboard" entries={data.packages} />
      <p className="text-[11px] text-ramp-40 max-w-2xl">
        orphaned-but-load-bearing packages and a KRI curve across multiple epochs are not served by
        this view yet: the first needs a schema addition this Cypher subset&apos;s lack of a
        negated-pattern query cannot answer directly, and the second needs every epoch&apos;s value
        persisted rather than only the most recent.
      </p>
    </div>
  );
}

function Leaderboard({ title, entries }: { title: string; entries: LeaderboardEntry[] }) {
  if (entries.length === 0) {
    return (
      <div>
        <h2 className="text-xs uppercase tracking-wide text-ramp-40 mb-3">{title}</h2>
        <p className="text-xs text-ramp-60">no observatory run has written results yet</p>
      </div>
    );
  }

  const max = Math.max(...entries.map((e) => e.kri));
  const asOf = entries[0]?.kri_at;

  return (
    <div>
      <div className="flex items-baseline justify-between mb-3">
        <h2 className="text-xs uppercase tracking-wide text-ramp-40">{title}</h2>
        <span className="tabular text-[11px] text-ramp-40">as of {day(asOf)}</span>
      </div>
      <div className="flex flex-col gap-1.5">
        {entries.slice(0, 20).map((e) => {
          const band = Math.round(e.kri * e.error_band);
          const widthPct = (e.kri / max) * 100;
          return (
            <div key={e.name} className="flex items-center gap-3">
              <span className="tabular text-xs text-ramp-80 w-40 truncate shrink-0">{e.name}</span>
              <div className="flex-1 h-2 bg-surface-1 rounded-sm overflow-hidden">
                <div className="h-full bg-signal" style={{ width: `${widthPct}%` }} />
              </div>
              <span className="tabular text-xs text-ramp-100 w-24 text-right shrink-0">
                {e.kri.toLocaleString()}
                <span className="text-ramp-40 italic"> ±{band}</span>
              </span>
            </div>
          );
        })}
      </div>
    </div>
  );
}

function day(t?: number): string {
  if (!t) return "-";
  return new Date(t * 1000).toISOString().slice(0, 10);
}
