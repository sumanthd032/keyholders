"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { API_BASE } from "@/lib/sse";
import { streamIncidentTraversal } from "@/lib/incidentStream";
import { useRegisterCommands, type Command } from "@/lib/commandRegistry";
import { packageURNOf } from "@/lib/types";
import type {
  Frontier,
  Graph,
  IncidentTraversalDone,
  IncidentView,
  RecordedProject,
} from "@/lib/types";
import { GraphCanvas } from "./GraphCanvas";
import { DualCounter } from "./DualCounter";

type TraversalState =
  | { phase: "idle" }
  | { phase: "watching"; graph: Graph; depth: number }
  | { phase: "done"; result: IncidentTraversalDone }
  | { phase: "error"; message: string };

/**
 * The incident view's live traversal: watch one recorded project's search expand hop by hop toward
 * a compromised version, the animation being the actual computation rather than a replay of one, the
 * same honesty constraint the audit view's streaming search already holds itself to.
 */
export function IncidentWorkspace() {
  const [target, setTarget] = useState("");
  const [projects, setProjects] = useState<RecordedProject[]>([]);
  const [project, setProject] = useState("");
  const [report, setReport] = useState<IncidentView | null>(null);
  const [reportError, setReportError] = useState<string | null>(null);
  const [reportLoading, setReportLoading] = useState(false);
  const [traversal, setTraversal] = useState<TraversalState>({ phase: "idle" });
  const controllerRef = useRef<AbortController | null>(null);

  useEffect(() => {
    fetch(`${API_BASE}/api/projects`)
      .then((r) => r.json())
      .then((ps: RecordedProject[]) => {
        setProjects(ps);
        if (ps.length > 0) setProject(ps[0].name);
      })
      .catch(() => setProjects([]));
  }, []);

  function parseTarget(): [string, string] | null {
    const at = target.lastIndexOf("@");
    if (at <= 0) return null;
    return [target.slice(0, at), target.slice(at + 1)];
  }

  async function loadReport(name: string, version: string) {
    setReport(null);
    setReportError(null);
    setReportLoading(true);
    try {
      const res = await fetch(`${API_BASE}/api/incident/${name}/${version}`);
      if (!res.ok) throw new Error(`${res.status} ${res.statusText}`);
      setReport((await res.json()) as IncidentView);
    } catch (err) {
      setReportError(String(err));
    } finally {
      setReportLoading(false);
    }
  }

  function watch() {
    const parsed = parseTarget();
    if (!parsed || !project) return;
    const [name, version] = parsed;

    loadReport(name, version);

    controllerRef.current?.abort();
    const controller = new AbortController();
    controllerRef.current = controller;

    setTraversal({ phase: "watching", graph: { nodes: [], edges: [] }, depth: 0 });

    const seen = new Map<string, boolean>();
    streamIncidentTraversal(name, version, project, controller.signal, {
      onFrontier: (f: Frontier) => {
        for (const urn of f.urns) seen.set(packageURNOf(urn), true);
        setTraversal((prev) =>
          prev.phase === "watching"
            ? {
                phase: "watching",
                depth: f.depth,
                graph: {
                  nodes: [...seen.keys()].map((pkg) => ({ package: pkg, coexistent: true })),
                  edges: prev.graph.edges,
                },
              }
            : prev,
        );
      },
      onDone: (result: IncidentTraversalDone) => setTraversal({ phase: "done", result }),
      onError: (message: string) => setTraversal({ phase: "error", message }),
    }).catch((err) => {
      if (controller.signal.aborted) return;
      setTraversal({ phase: "error", message: String(err) });
    });
  }

  const canWatch = Boolean(parseTarget() && project);
  const viewCommands = useMemo(() => {
    if (!canWatch) return [];
    const cmds: Command[] = [
      {
        id: "incident.watch",
        label: "watch live traversal",
        hint: `${target} against ${project}`,
        run: () => watch(),
      },
    ];
    return cmds;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [canWatch, target, project]);
  useRegisterCommands(viewCommands);

  return (
    <div className="flex-1 flex flex-col overflow-y-auto">
      <div className="px-8 py-6 flex flex-wrap items-end gap-4 border-b border-rule-1">
        <label className="flex flex-col gap-1">
          <span className="text-[11px] uppercase tracking-wide text-ramp-40">package@version</span>
          <input
            value={target}
            onChange={(e) => setTarget(e.target.value)}
            placeholder="minimist@1.2.0"
            className="tabular bg-surface-1 border border-rule-1 rounded-sm px-2 py-1 text-sm text-ramp-100 focus:border-signal outline-none"
          />
        </label>
        <label className="flex flex-col gap-1">
          <span className="text-[11px] uppercase tracking-wide text-ramp-40">
            recorded project to watch
          </span>
          <select
            value={project}
            onChange={(e) => setProject(e.target.value)}
            className="tabular bg-surface-1 border border-rule-1 rounded-sm px-2 py-1 text-sm text-ramp-100"
          >
            {projects.length === 0 && <option value="">no recorded projects</option>}
            {projects.map((p) => (
              <option key={p.name} value={p.name}>
                {p.name}
              </option>
            ))}
          </select>
        </label>
        <button
          onClick={watch}
          disabled={!parseTarget() || !project}
          className="text-xs px-3 py-1.5 border border-signal text-signal rounded-sm hover:bg-surface-1 disabled:opacity-30 disabled:cursor-not-allowed"
        >
          watch live
        </button>
      </div>

      {traversal.phase === "idle" && !report && (
        <p className="px-8 py-6 text-xs text-ramp-40">
          type a package@version and pick a recorded project to see its blast radius
        </p>
      )}

      {traversal.phase === "watching" && (
        <div className="px-8 py-4" aria-live="polite">
          <p className="tabular text-xs text-ramp-40 mb-3">
            depth {traversal.depth}, {traversal.graph.nodes.length} packages reached so far
          </p>
          <GraphCanvas graph={traversal.graph} />
        </div>
      )}

      {traversal.phase === "done" && (
        <div className="px-8 py-4 flex flex-col gap-4">
          <p
            className={`text-sm ${traversal.result.reached ? "text-danger" : "text-positive"}`}
            role="status"
          >
            {traversal.result.reached
              ? `${project} resolves to the compromised version`
              : `${project} does not reach the compromised version`}
          </p>
          <DualCounter audit={traversal.result.audit} />
          <GraphCanvas graph={traversal.result.audit.graph} />
        </div>
      )}

      {traversal.phase === "error" && (
        <p className="px-8 py-4 text-sm text-danger" role="alert">
          {traversal.message}
        </p>
      )}

      <div className="px-8 py-6 flex flex-col gap-8">
        {reportLoading && (
          <p className="text-xs text-ramp-40" aria-live="polite">
            loading blast radius report...
          </p>
        )}
        {reportError && (
          <p className="text-sm text-danger" role="alert">
            {reportError}
          </p>
        )}
        {report && (
          <>
            <Section title="advisories">
              {report.advisories.length === 0 ? (
                <Empty text="none found affecting this exact version" />
              ) : (
                <Table
                  columns={["id", "severity", "summary"]}
                  rows={report.advisories.map((a) => [a.id, a.severity, a.summary])}
                />
              )}
            </Section>

            <Section title="introduced">
              {report.introductions.length === 0 ? (
                <Empty text="no bisection available" />
              ) : (
                <Table
                  columns={["package", "first affected", "published"]}
                  rows={report.introductions.map((i) => [
                    i.package,
                    i.first_affected,
                    day(i.published_at),
                  ])}
                />
              )}
            </Section>

            <Section title="transitively exposed">
              {report.exposed.length === 0 ? (
                <Empty text="none of the recorded projects reach this version" />
              ) : (
                <Table
                  columns={["project", "locked at"]}
                  rows={report.exposed.map((e) => [e.project, day(e.locked_at)])}
                />
              )}
            </Section>

            <Section title="resolved the compromised version while it was live">
              {report.resolved_while_live.length === 0 ? (
                <Empty text="no recorded project locked a dependent whose resolution was live at the time" />
              ) : (
                <Table
                  columns={["project", "locked at", "through"]}
                  rows={report.resolved_while_live.map((l) => [
                    l.project,
                    day(l.locked_at),
                    l.dependent,
                  ])}
                />
              )}
            </Section>

            <Section title="shares a maintainer with">
              {report.shared_maintainers.length === 0 ? (
                <Empty text="no other package shares a maintainer with this one" />
              ) : (
                <Table
                  columns={["package", "through"]}
                  rows={report.shared_maintainers
                    .slice(0, 20)
                    .map((s) => [s.package, s.maintainer])}
                />
              )}
            </Section>

            <Section title="likely typosquats nearby">
              {report.typosquats.length === 0 ? (
                <Empty text="none materialized against this package" />
              ) : (
                <Table
                  columns={["name", "relation", "distance"]}
                  rows={report.typosquats.map((t) => [t.name, t.direction, String(t.distance)])}
                />
              )}
            </Section>
          </>
        )}
      </div>
    </div>
  );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div>
      <h2 className="text-xs uppercase tracking-wide text-ramp-40 mb-3">{title}</h2>
      {children}
    </div>
  );
}

function Empty({ text }: { text: string }) {
  return <p className="text-xs text-ramp-60">{text}</p>;
}

function Table({ columns, rows }: { columns: string[]; rows: string[][] }) {
  return (
    <div className="overflow-x-auto">
      <table className="w-full text-left text-xs tabular">
        <thead>
          <tr className="text-ramp-40 border-b border-rule-1">
            {columns.map((c) => (
              <th key={c} className="py-2 pr-4 font-normal">
                {c}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((row, i) => (
            <tr key={i} className="border-b border-rule-1/50">
              {row.map((cell, j) => (
                <td key={j} className="py-2 pr-4 text-ramp-80">
                  {cell}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function day(t: number): string {
  if (!t) return "-";
  return new Date(t * 1000).toISOString().slice(0, 10);
}
