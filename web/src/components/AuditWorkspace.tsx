"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { fetchAudit, streamAudit } from "@/lib/auditStream";
import { fetchTimeline, type TimelineSample } from "@/lib/timeline";
import { useRegisterCommands, type Command } from "@/lib/commandRegistry";
import type { AuditView, Frontier } from "@/lib/types";
import { UploadZone } from "./UploadZone";
import { RollCall } from "./RollCall";
import { DualCounter } from "./DualCounter";
import { ExposureRiver } from "./ExposureRiver";
import { GraphCanvas } from "./GraphCanvas";
import { CutPanel } from "./CutPanel";
import { Roster } from "./Roster";

type State =
  | { phase: "idle" }
  | { phase: "searching"; fileName: string; depth: number; reached: number }
  | { phase: "revealing"; audit: AuditView; runId: number }
  | { phase: "done"; audit: AuditView; runId: number }
  | { phase: "error"; message: string };

export function AuditWorkspace() {
  const [state, setState] = useState<State>({ phase: "idle" });
  const [cutHandle, setCutHandle] = useState<string | null>(null);
  const [timeline, setTimeline] = useState<TimelineSample[]>([]);
  const [selectedAt, setSelectedAt] = useState<number | null>(null);
  const [file, setFile] = useState<File | null>(null);
  const controllerRef = useRef<AbortController | null>(null);
  const runIdRef = useRef(0);
  const fileRef = useRef<File | null>(null);

  function runAudit(file: File) {
    controllerRef.current?.abort();
    const controller = new AbortController();
    controllerRef.current = controller;
    const runId = ++runIdRef.current;

    fileRef.current = file;
    setFile(file);
    setCutHandle(null);
    setTimeline([]);
    setSelectedAt(null);
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

  // Fetched once the search settles, keyed on runId rather than on every audit swap: scrubbing
  // replaces state.audit repeatedly without changing runId, and the timeline itself does not change
  // just because the selected instant did.
  useEffect(() => {
    if (state.phase !== "done" || !fileRef.current) return;
    const file = fileRef.current;
    let cancelled = false;
    fetchTimeline(file).then((samples) => {
      if (!cancelled) setTimeline(samples);
    });
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [state.phase === "done" ? state.runId : null]);

  function scrubTo(at: number) {
    const file = fileRef.current;
    if (!file || (state.phase !== "done" && state.phase !== "revealing")) return;
    setSelectedAt(at);
    const runId = state.runId;
    fetchAudit(file, at).then(
      (audit) => setState({ phase: "done", audit, runId }),
      (err) => setState({ phase: "error", message: String(err) }),
    );
  }

  // Registered for as long as this view is mounted, not only while a given phase is active: the
  // command list itself changes with the phase (there is nothing to skip past before a search
  // reveals anything), which useRegisterCommands picks up on every call since it replaces its
  // registration whenever the array identity changes.
  const viewCommands = useMemo(() => {
    const cmds: Command[] = [];
    if (state.phase !== "idle") {
      cmds.push({
        id: "audit.new",
        label: "new audit",
        hint: "start over with another lockfile",
        run: () => setState({ phase: "idle" }),
      });
    }
    if (state.phase === "revealing") {
      const { audit, runId } = state;
      cmds.push({
        id: "audit.skip-roll-call",
        label: "skip roll call",
        hint: "reveal the roster immediately",
        run: () => setState({ phase: "done", audit, runId }),
      });
    }
    return cmds;
  }, [state]);
  useRegisterCommands(viewCommands);

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
      <div className="px-8 pt-6 flex flex-wrap items-baseline justify-between gap-2">
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
      {state.phase === "done" && timeline.length === 0 && (
        <p className="px-8 py-2 text-[11px] text-ramp-40" aria-live="polite">
          loading exposure over time...
        </p>
      )}
      {timeline.length > 0 && (
        <ExposureRiver samples={timeline} selectedAt={selectedAt} onScrub={scrubTo} />
      )}
      <GraphCanvas
        graph={audit.graph}
        highlighted={cutPackages(audit, cutHandle)}
        onSelectPackage={(pkg) => {
          const holder = audit.keyholders.find((k) => k.holds.includes(pkg));
          if (holder) {
            document
              .getElementById(`keyholder-${holder.handle}`)
              ?.scrollIntoView({ block: "center", behavior: "smooth" });
          }
        }}
      />
      <CutPanel cuts={audit.cuts} selected={cutHandle} onSelect={setCutHandle} />
      <Roster audit={audit} file={file} at={selectedAt} />
    </div>
  );
}

/** Every package a removal analysis found would be lost: what the account holds directly, plus
 * whatever the server found orphaned downstream. Returns undefined rather than [] when nothing is
 * selected, so GraphCanvas falls back to its own click-driven selection instead of highlighting an
 * empty set. */
function cutPackages(audit: AuditView, handle: string | null): string[] | undefined {
  if (!handle) return undefined;
  const cut = audit.cuts.find((c) => c.handle === handle);
  const holder = audit.keyholders.find((k) => k.handle === handle);
  if (!cut || !holder) return undefined;
  return [...holder.holds, ...(cut.orphaned ?? [])];
}
