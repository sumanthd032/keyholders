"use client";

import { useMemo, useState } from "react";
import {
  GraphCanvas as ReagraphCanvas,
  type GraphEdge as ReagraphEdge,
  type GraphNode as ReagraphNode,
  type Theme,
} from "reagraph";
import type { Graph } from "@/lib/types";
import { packageNameOf } from "@/lib/types";

// Matches the CSS token values in globals.css. Reagraph's theme is consumed by three.js material
// props, not CSS, so the values are duplicated here rather than read from a custom property; see
// the doc comment on GraphCanvas for why that duplication is accepted rather than solved.
const theme: Theme = {
  canvas: { background: "#0a0c10", fog: null },
  node: {
    fill: "#767c89",
    activeFill: "#3fd9ff",
    opacity: 0.9,
    selectedOpacity: 1,
    inactiveOpacity: 0.2,
    label: { color: "#c7cad1", activeColor: "#f2f3f5" },
  },
  ring: { fill: "#3fd9ff", activeFill: "#3fd9ff" },
  edge: {
    fill: "#454952",
    activeFill: "#3fd9ff",
    opacity: 0.5,
    selectedOpacity: 1,
    inactiveOpacity: 0.1,
    label: { color: "#9aa0ab", activeColor: "#f2f3f5" },
  },
  arrow: { fill: "#454952", activeFill: "#3fd9ff" },
  lasso: { background: "rgba(63, 217, 255, 0.1)", border: "1px solid #3fd9ff" },
};

/**
 * The reach graph, package granularity, rendered in WebGL rather than SVG or canvas 2D: the
 * interface design's own stated reason is that neither survives tens of thousands of nodes, and
 * this project's audits can reach that scale on a deep dependency tree. Node color carries the same
 * coexistence-versus-phantom argument the dual counter makes in numbers: signal cyan is genuinely
 * reachable, danger red reached only in the union graph, never at any one instant. Colour is not the
 * only carrier here, every node is also labelled by name, so the distinction survives colour
 * blindness the same way the rest of the token system is built to.
 *
 * Theme colors are duplicated from globals.css rather than read from the CSS custom properties:
 * reagraph consumes them as three.js material props at the React prop layer, not as CSS, so there is
 * no live binding to read from without piping color strings through JavaScript instead of CSS in the
 * first place. A theme drift check is the honest mitigation, not a shared source, until reagraph
 * offers a way to theme from CSS variables directly.
 */
export function GraphCanvas({
  graph,
  onSelectPackage,
  highlighted,
}: {
  graph: Graph;
  onSelectPackage?: (pkg: string) => void;
  /**
   * Externally controlled highlight set, for Cut a head: the packages a removal analysis found
   * would be lost. Takes over from click-driven selection while set, since the two mean different
   * things, one node the user picked versus every node one removal would cost, and showing both at
   * once would blur that distinction rather than clarify it.
   */
  highlighted?: string[];
}) {
  const [selected, setSelected] = useState<string | null>(null);
  const activeSelections = highlighted ?? (selected ? [selected] : []);

  const nodes: ReagraphNode[] = useMemo(
    () =>
      graph.nodes.map((n) => ({
        id: n.package,
        label: packageNameOf(n.package),
        fill: n.coexistent ? "#3fd9ff" : "#ff4d6d",
      })),
    [graph.nodes],
  );

  const edges: ReagraphEdge[] = useMemo(
    () =>
      graph.edges.map((e, i) => ({
        id: `${e.from}->${e.to}-${i}`,
        source: e.from,
        target: e.to,
      })),
    [graph.edges],
  );

  if (nodes.length === 0) return null;

  return (
    <div className="px-8 py-6">
      <h2 className="text-xs uppercase tracking-wide text-ramp-40 mb-1">reach graph</h2>
      <p className="text-[11px] text-ramp-40 mb-3">
        <span className="text-signal">cyan</span> coexists at some instant ·{" "}
        <span className="text-danger">red</span> is phantom, union graph only, never actually
        coexisted
      </p>
      <div className="border border-rule-1 rounded-sm relative" style={{ height: 420 }}>
        <ReagraphCanvas
          nodes={nodes}
          edges={edges}
          theme={theme}
          layoutType="forceDirected2d"
          selections={activeSelections}
          onNodeClick={(node) => {
            setSelected(node.id);
            onSelectPackage?.(node.id);
          }}
          onCanvasClick={() => setSelected(null)}
        />
      </div>
    </div>
  );
}
