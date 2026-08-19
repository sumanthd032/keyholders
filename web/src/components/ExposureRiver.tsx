"use client";

import { useMemo, useRef, useState } from "react";
import type { TimelineSample } from "@/lib/timeline";

const WIDTH = 900;
const HEIGHT = 160;
const PAD = 24;

/**
 * Time is the primary canvas here, not a filter in a corner: the project is a line, keyholders are
 * a band alongside it, and the band's thickness at any instant is exposure at that instant, not a
 * single present-tense count. Scrubbing is the primary interaction, not the chart itself: dragging
 * re-queries the audit at the selected instant and everything downstream re-resolves, which is what
 * `onScrub` triggers in the parent rather than this component owning any query itself.
 *
 * Coexistence and union are drawn as two edges of the same band, the identical dual-counter argument
 * made as a shape instead of two numbers: the gap between them, visible the whole time, is phantom
 * reach at every instant along the way, not only at the instant currently selected.
 */
export function ExposureRiver({
  samples,
  selectedAt,
  onScrub,
}: {
  samples: TimelineSample[];
  selectedAt: number | null;
  onScrub: (at: number) => void;
}) {
  const svgRef = useRef<SVGSVGElement>(null);
  const [dragging, setDragging] = useState(false);

  const { unionPath, coexistPath, maxCount, xAt, atX } = useMemo(() => {
    const max = Math.max(1, ...samples.map((s) => s.union_keyholder_count));
    const innerW = WIDTH - PAD * 2;
    const innerH = HEIGHT - PAD * 2;
    const n = Math.max(1, samples.length - 1);

    const x = (i: number) => PAD + (i / n) * innerW;
    const y = (count: number) => PAD + innerH - (count / max) * innerH;

    const union = samples.map((s, i) => `${i === 0 ? "M" : "L"}${x(i)},${y(s.union_keyholder_count)}`).join(" ");
    const coexist = samples
      .map((s, i) => `${i === 0 ? "M" : "L"}${x(i)},${y(s.keyholder_count)}`)
      .join(" ");

    const xAt = (at: number) => {
      const first = samples[0]?.at ?? at;
      const last = samples[samples.length - 1]?.at ?? at;
      const t = last === first ? 0 : (at - first) / (last - first);
      return PAD + Math.min(1, Math.max(0, t)) * innerW;
    };
    const atX = (px: number) => {
      const first = samples[0]?.at ?? 0;
      const last = samples[samples.length - 1]?.at ?? 0;
      const t = Math.min(1, Math.max(0, (px - PAD) / innerW));
      return Math.round(first + t * (last - first));
    };

    return { unionPath: union, coexistPath: coexist, maxCount: max, xAt, atX };
  }, [samples]);

  function scrubFromEvent(e: React.PointerEvent<SVGSVGElement>) {
    const svg = svgRef.current;
    if (!svg) return;
    const rect = svg.getBoundingClientRect();
    const px = ((e.clientX - rect.left) / rect.width) * WIDTH;
    onScrub(atX(px));
  }

  if (samples.length === 0) return null;

  const cursorX = selectedAt !== null ? xAt(selectedAt) : null;

  return (
    <div className="px-8 py-6">
      <h2 className="text-xs uppercase tracking-wide text-ramp-40 mb-1">exposure over time</h2>
      <p className="text-[11px] text-ramp-40 mb-3">
        drag to re-resolve exposure as of that instant · <span className="text-signal">cyan</span>{" "}
        line is coexistence, <span className="text-ramp-60">grey</span> is union, the gap between
        them is phantom reach at that instant
      </p>
      <svg
        ref={svgRef}
        viewBox={`0 0 ${WIDTH} ${HEIGHT}`}
        className="w-full touch-none select-none"
        style={{ height: HEIGHT, cursor: "ew-resize" }}
        onPointerDown={(e) => {
          setDragging(true);
          (e.target as Element).setPointerCapture(e.pointerId);
          scrubFromEvent(e);
        }}
        onPointerMove={(e) => {
          if (dragging) scrubFromEvent(e);
        }}
        onPointerUp={() => setDragging(false)}
        role="slider"
        aria-label="scrub exposure over time"
        aria-valuemin={samples[0]?.at}
        aria-valuemax={samples[samples.length - 1]?.at}
        aria-valuenow={selectedAt ?? undefined}
      >
        <path d={unionPath} fill="none" stroke="var(--ramp-40)" strokeWidth={1.5} />
        <path d={coexistPath} fill="none" stroke="var(--signal)" strokeWidth={2} />
        {cursorX !== null && (
          <line x1={cursorX} x2={cursorX} y1={PAD} y2={HEIGHT - PAD} stroke="var(--signal)" strokeWidth={1} strokeDasharray="2,2" />
        )}
      </svg>
      <div className="tabular text-[11px] text-ramp-40 flex justify-between mt-1">
        <span>{day(samples[0]?.at)}</span>
        <span>max {maxCount}</span>
        <span>{day(samples[samples.length - 1]?.at)}</span>
      </div>
    </div>
  );
}

function day(t?: number): string {
  if (!t) return "-";
  return new Date(t * 1000).toISOString().slice(0, 10);
}
