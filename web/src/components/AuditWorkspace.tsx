"use client";

import { useRef, useState } from "react";
import { streamAudit } from "@/lib/auditStream";
import type { AuditView, Frontier } from "@/lib/types";
import { UploadZone } from "./UploadZone";
import { RollCall } from "./RollCall";
import { DualCounter } from "./DualCounter";
import { Roster } from "./Roster";

type State =
  | { phase: "idle" }
  | { phase: "searching"; fileName: string; depth: number; reached: number }
  | { phase: "revealing"; audit: AuditView; runId: number }
  | { phase: "done"; audit: AuditView; runId: number }
  | { phase: "error"; message: string };

export function AuditWorkspace() {
  const [state, setState] = useState<State>({ phase: "idle" });
  const controllerRef = useRef<AbortController | null>(null);
  const runIdRef = useRef(0);

  function runAudit(file: File) {
    controllerRef.current?.abort();
    const controller = new AbortController();
    controllerRef.current = controller;
    const runId = ++runIdRef.current;

    setState({ phase: "searching", fileName: file.name, depth: 0, reached: 0 });

    streamAudit(file, controller.signal, {
      onFrontier: (f: Frontier) => {
        setState((prev) =>
          prev.phase === "searching"
            ? { ...prev, depth: f.depth, reached: prev.reached + f.urns.length }
            : prev,
        );
      },
      onDone: (audit: AuditView) => {
        setState({ phase: "revealing", audit, runId });
      },
      onError: (message: string) => {
        setState({ phase: "error", message });
      },
    }).catch((err) => {
      if (controller.signal.aborted) return;
      setState({ phase: "error", message: String(err) });
    });
  }

  if (state.phase === "idle") {
    return (
      <div className="flex-1 flex flex-col">
        <UploadZone onFile={runAudit} />
      </div>
    );
  }

  if (state.phase === "searching") {
    return (
      <div className="flex-1 flex flex-col items-center justify-center gap-2 px-8" aria-live="polite">
        <p className="tabular text-sm text-ramp-80">
          searching <span className="text-ramp-100">{state.fileName}</span>
        </p>
        <p className="tabular text-xs text-ramp-40">
          depth {state.depth}, {state.reached} versions reached so far
        </p>
      </div>
    );
  }

  if (state.phase === "error") {
    return (
      <div className="flex-1 flex flex-col items-center justify-center gap-3 px-8" role="alert">
        <p className="text-sm text-danger">{state.message}</p>
        <button
          onClick={() => setState({ phase: "idle" })}
          className="text-xs text-ramp-40 hover:text-ramp-80 underline underline-offset-2"
        >
          try another file
        </button>
      </div>
    );
  }

  const { audit, runId } = state;

  if (audit.sources_in_graph === 0) {
    return (
      <div className="flex-1 flex flex-col items-center justify-center gap-2 px-8">
        <p className="text-sm text-ramp-80">
          none of the {audit.pins} locked packages are in the graph
        </p>
        <p className="text-xs text-ramp-40">ingest the packages this project depends on first</p>
      </div>
    );
  }

  return (
    <div className="flex-1 flex flex-col overflow-y-auto">
      <div className="px-8 pt-6 flex items-baseline justify-between">
        <div>
          <h1 className="text-sm text-ramp-100">{audit.project}</h1>
          <p className="tabular text-xs text-ramp-40">
            {audit.format} · {audit.sources_in_graph} of {audit.pins} locked packages in the graph
            {audit.sources_in_graph < audit.pins ? ", lower bound" : ""}
          </p>
        </div>
        <button
          onClick={() => setState({ phase: "idle" })}
          className="text-xs text-ramp-40 hover:text-ramp-80 underline underline-offset-2"
        >
          new audit
        </button>
      </div>

      <RollCall
        key={runId}
        keyholders={audit.keyholders}
        onSettle={() => setState({ phase: "done", audit, runId })}
      />

      <DualCounter audit={audit} />
      <Roster audit={audit} />
    </div>
  );
}
