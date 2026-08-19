"use client";

import { useState } from "react";
import { fetchPath } from "@/lib/path";
import { packageNameOf, type Chain } from "@/lib/types";

type State =
  | { phase: "idle" }
  | { phase: "loading" }
  | { phase: "done"; chain: Chain }
  | { phase: "error"; message: string };

/**
 * The proof behind one keyholder's reach: the concrete chain from what the project locked to the
 * reached version they can publish to, exactly what algo.MSpaths returned. This is evidence rather
 * than a claim, checkable against the registry by hand, which is the whole reason the roster can be
 * trusted instead of taken on faith.
 */
export function PathInspector({
  file,
  handle,
  packages,
  at,
}: {
  file: File;
  handle: string;
  packages: string[];
  at: number | null;
}) {
  const [selected, setSelected] = useState(packages[0] ?? "");
  const [state, setState] = useState<State>({ phase: "idle" });

  function prove(pkg: string) {
    setSelected(pkg);
    setState({ phase: "loading" });
    fetchPath(file, handle, { package: pkg, at }).then(
      (view) => {
        const chain = view.chains[0];
        if (!chain) {
          setState({ phase: "error", message: "the server returned no chain for this package" });
          return;
        }
        setState({ phase: "done", chain });
      },
      (err) => setState({ phase: "error", message: String(err) }),
    );
  }

  return (
    <div className="px-4 py-3 bg-surface-1 border border-rule-1 rounded-sm text-xs flex flex-col gap-2">
      <div className="flex items-center gap-2 flex-wrap">
        <span className="text-ramp-40">prove reach to</span>
        <select
          value={selected}
          onChange={(e) => setSelected(e.target.value)}
          className="bg-transparent border border-rule-1 rounded-sm px-1.5 py-0.5 text-ramp-100"
        >
          {packages.map((p) => (
            <option key={p} value={p} className="bg-surface-1">
              {packageNameOf(p)}
            </option>
          ))}
        </select>
        <button
          onClick={() => prove(selected)}
          disabled={state.phase === "loading" || !selected}
          className="text-signal hover:text-ramp-100 underline underline-offset-2 disabled:text-ramp-40 disabled:no-underline"
        >
          show proof
        </button>
      </div>

      {state.phase === "loading" && (
        <p className="text-ramp-40" aria-live="polite">
          searching for a coexistence path...
        </p>
      )}

      {state.phase === "error" && (
        <p className="text-danger" role="alert">
          {state.message}
        </p>
      )}

      {state.phase === "done" && !state.chain.found && (
        <p className="text-ramp-60">no coexistence path found within the depth bound</p>
      )}

      {state.phase === "done" && state.chain.found && (
        <div className="flex flex-col gap-1">
          <p className="text-ramp-40">
            every edge below held from {day(state.chain.valid_from)} to {openOrNow(state.chain.valid_to)}
          </p>
          <ol className="tabular flex flex-col gap-0.5">
            {chainNodes(state.chain).map((node, i) => (
              <li key={`${node}-${i}`} className="text-ramp-100">
                {i === 0 ? (
                  packageNameOf(node)
                ) : (
                  <>
                    <span className="text-ramp-40">{"-> "}</span>
                    {packageNameOf(node)}
                    <span className="text-ramp-40"> (declared {state.chain.hops?.[i - 1]?.range})</span>
                  </>
                )}
              </li>
            ))}
          </ol>
        </div>
      )}
    </div>
  );
}

function chainNodes(chain: Chain): string[] {
  if (!chain.hops || chain.hops.length === 0) return [];
  return [chain.hops[0].from, ...chain.hops.map((h) => h.to)];
}

function day(t?: number): string {
  if (!t) return "-";
  return new Date(t * 1000).toISOString().slice(0, 10);
}

// graph.OpenInterval on the Go side: a valid_to this far out means "still valid", mirrored here
// since the API sends the raw sentinel rather than a resolved boolean.
const OPEN_INTERVAL = 4102444800;

function openOrNow(t?: number): string {
  if (!t) return "-";
  return t >= OPEN_INTERVAL ? "now" : day(t);
}
