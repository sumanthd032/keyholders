"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { API_BASE } from "@/lib/sse";
import { useRegisterCommands, type Command } from "@/lib/commandRegistry";
import { KRICurve, type HistoryPoint } from "./KRICurve";

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

type OrphanedView = {
  epoch: number;
  entries: LeaderboardEntry[];
};

type Target = "maintainer" | "package";

/**
 * The observatory: reach across the whole ingested graph, not one project's lockfile. Every number
 * here is a HyperLogLog estimate, not an exact count the way the audit view's are, so every one of
 * them renders its error band directly beside it rather than looking identical to an exact value.
 */
export function ObservatoryDashboard() {
  const [data, setData] = useState<ObservatoryView | null>(null);
  const [orphaned, setOrphaned] = useState<OrphanedView | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [selected, setSelected] = useState<{ target: Target; name: string } | null>(null);
  const [history, setHistory] = useState<HistoryPoint[] | null>(null);
  const [historyError, setHistoryError] = useState<string | null>(null);

  const load = useCallback(() => {
    Promise.all([
      fetch(`${API_BASE}/api/observatory`).then((r) => {
        if (!r.ok) throw new Error(`${r.status} ${r.statusText}`);
        return r.json() as Promise<ObservatoryView>;
      }),
      fetch(`${API_BASE}/api/observatory/orphaned`).then((r) => {
        if (!r.ok) throw new Error(`${r.status} ${r.statusText}`);
        return r.json() as Promise<OrphanedView>;
      }),
    ])
      .then(([obs, orph]) => {
        setData(obs);
        setOrphaned(orph);
        setError(null);
      })
      .catch((err) => setError(String(err)));
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  const viewCommands = useMemo<Command[]>(
    () => [
      {
        id: "observatory.refresh",
        label: "refresh observatory data",
        hint: "re-read the last run's leaderboards",
        run: () => load(),
      },
    ],
    [load],
  );
  useRegisterCommands(viewCommands);

  function select(target: Target, name: string) {
    if (selected?.target === target && selected?.name === name) {
      setSelected(null);
      setHistory(null);
      setHistoryError(null);
      return;
    }
    setSelected({ target, name });
    setHistory(null);
    setHistoryError(null);
    fetch(`${API_BASE}/api/observatory/history?target=${target}&name=${encodeURIComponent(name)}`)
      .then((r) => {
        if (!r.ok) throw new Error(`${r.status} ${r.statusText}`);
        return r.json() as Promise<HistoryPoint[]>;
      })
      .then(setHistory)
      .catch((err) => setHistoryError(String(err)));
  }

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
      <Leaderboard
        title="maintainer reach leaderboard"
        entries={data.maintainers}
        onSelect={(name) => select("maintainer", name)}
        selectedName={selected?.target === "maintainer" ? selected.name : null}
      />
      {selected?.target === "maintainer" && (
        <HistoryPanel name={selected.name} history={history} error={historyError} />
      )}

      <Leaderboard
        title="package reach leaderboard"
        entries={data.packages}
        onSelect={(name) => select("package", name)}
        selectedName={selected?.target === "package" ? selected.name : null}
      />
      {selected?.target === "package" && (
        <HistoryPanel name={selected.name} history={history} error={historyError} />
      )}

      <OrphanedSection orphaned={orphaned} />
    </div>
  );
}

function HistoryPanel({
  name,
  history,
  error,
}: {
  name: string;
  history: HistoryPoint[] | null;
  error: string | null;
}) {
  return (
    <div className="border-t border-rule-1 pt-4 -mt-6">
      {error && (
        <p className="text-xs text-danger" role="alert">
          {error}
        </p>
      )}
      {!error && !history && (
        <p className="text-xs text-ramp-40" aria-live="polite">
          loading history for {name}...
        </p>
      )}
      {!error && history && <KRICurve name={name} points={history} />}
    </div>
  );
}

function OrphanedSection({ orphaned }: { orphaned: OrphanedView | null }) {
  if (!orphaned) return null;

  return (
    <div>
      <div className="flex flex-wrap items-baseline justify-between gap-2 mb-3">
        <h2 className="text-xs uppercase tracking-wide text-ramp-40">
          orphaned but load bearing: no maintainer at all
        </h2>
        <span className="tabular text-[11px] text-ramp-40">as of {day(orphaned.epoch)}</span>
      </div>
      {orphaned.entries.length === 0 ? (
        <p className="text-xs text-ramp-60">every package in this run has at least one maintainer</p>
      ) : (
        <div className="flex flex-col gap-1.5">
          {orphaned.entries.map((e) => (
            <div key={e.name} className="flex items-center gap-3">
              <span className="tabular text-xs text-danger w-56 truncate shrink-0">{e.name}</span>
              <span className="tabular text-xs text-ramp-100">
                {e.kri.toLocaleString()}
                <span className="text-ramp-40 italic"> ±{Math.round(e.kri * e.error_band)}</span>
              </span>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

function Leaderboard({
  title,
  entries,
  onSelect,
  selectedName,
}: {
  title: string;
  entries: LeaderboardEntry[];
  onSelect: (name: string) => void;
  selectedName: string | null;
}) {
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
      <div className="flex flex-wrap items-baseline justify-between gap-2 mb-3">
        <h2 className="text-xs uppercase tracking-wide text-ramp-40">{title}</h2>
        <span className="tabular text-[11px] text-ramp-40">as of {day(asOf)}</span>
      </div>
      <div className="flex flex-col gap-1.5">
        {entries.slice(0, 20).map((e) => {
          const band = Math.round(e.kri * e.error_band);
          const widthPct = (e.kri / max) * 100;
          const active = selectedName === e.name;
          return (
            <button
              key={e.name}
              onClick={() => onSelect(e.name)}
              className={`flex items-center gap-3 text-left rounded-sm -mx-1 px-1 py-0.5 ${
                active ? "bg-surface-1" : "hover:bg-surface-1/60"
              }`}
            >
              <span className="tabular text-xs text-ramp-80 w-40 truncate shrink-0">{e.name}</span>
              <div className="flex-1 h-2 bg-surface-1 rounded-sm overflow-hidden">
                <div className="h-full bg-signal" style={{ width: `${widthPct}%` }} />
              </div>
              <span className="tabular text-xs text-ramp-100 w-24 text-right shrink-0">
                {e.kri.toLocaleString()}
                <span className="text-ramp-40 italic"> ±{band}</span>
              </span>
            </button>
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
