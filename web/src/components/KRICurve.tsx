"use client";

import { useMemo, useRef, useState } from "react";

export type HistoryPoint = { epoch: number; kri: number };

const WIDTH = 560;
const HEIGHT = 120;
const PAD_X = 8;
const PAD_Y = 16;

/**
 * KRI over time for one maintainer or package, read from the KRI_AT self loops a prior observatory
 * run persisted per epoch. A single series needs no legend, its own title names it; the crosshair
 * snaps to the nearest epoch since there is nothing to gain from reading a value between two
 * quarterly samples that were never actually computed.
 */
export function KRICurve({ name, points }: { name: string; points: HistoryPoint[] }) {
  const svgRef = useRef<SVGSVGElement>(null);
  const [hoverIndex, setHoverIndex] = useState<number | null>(null);

  const { path, x, y, max } = useMemo(() => {
    const innerW = WIDTH - PAD_X * 2;
    const innerH = HEIGHT - PAD_Y * 2;
    const max = Math.max(1, ...points.map((p) => p.kri));
    const n = Math.max(1, points.length - 1);

    const x = (i: number) => PAD_X + (i / n) * innerW;
    const y = (kri: number) => PAD_Y + innerH - (kri / max) * innerH;

    const path = points.map((p, i) => `${i === 0 ? "M" : "L"}${x(i)},${y(p.kri)}`).join(" ");
    return { path, x, y, max };
  }, [points]);

  if (points.length === 0) {
    return <p className="text-xs text-ramp-60">no history persisted for {name} yet</p>;
  }

  function indexFromEvent(e: React.PointerEvent<SVGSVGElement>) {
    const svg = svgRef.current;
    if (!svg) return;
    const rect = svg.getBoundingClientRect();
    const px = ((e.clientX - rect.left) / rect.width) * WIDTH;
    const innerW = WIDTH - PAD_X * 2;
    const n = Math.max(1, points.length - 1);
    const t = Math.min(1, Math.max(0, (px - PAD_X) / innerW));
    setHoverIndex(Math.round(t * n));
  }

  const hovered = hoverIndex !== null ? points[hoverIndex] : null;
  const last = points[points.length - 1];

  return (
    <div className="flex flex-col gap-1">
      <div className="flex items-baseline justify-between">
        <h3 className="text-[11px] uppercase tracking-wide text-ramp-40">{name} reach over time</h3>
        {hovered ? (
          <span className="tabular text-[11px] text-ramp-100">
            {day(hovered.epoch)} · <span className="text-signal">{hovered.kri}</span>
          </span>
        ) : (
          <span className="tabular text-[11px] text-ramp-40">latest {last.kri}</span>
        )}
      </div>
      <svg
        ref={svgRef}
        viewBox={`0 0 ${WIDTH} ${HEIGHT}`}
        className="w-full touch-none select-none"
        style={{ height: HEIGHT }}
        onPointerMove={indexFromEvent}
        onPointerLeave={() => setHoverIndex(null)}
        role="img"
        aria-label={`${name} reach across ${points.length} epochs, from ${points[0].kri} to ${last.kri}`}
      >
        <line
          x1={PAD_X}
          x2={WIDTH - PAD_X}
          y1={HEIGHT - PAD_Y}
          y2={HEIGHT - PAD_Y}
          stroke="var(--rule-1)"
          strokeWidth={1}
        />
        <path d={path} fill="none" stroke="var(--signal)" strokeWidth={2} strokeLinejoin="round" strokeLinecap="round" />
        <circle cx={x(points.length - 1)} cy={y(last.kri)} r={4} fill="var(--signal)" stroke="var(--surface-1)" strokeWidth={2} />
        {hoverIndex !== null && (
          <>
            <line
              x1={x(hoverIndex)}
              x2={x(hoverIndex)}
              y1={PAD_Y}
              y2={HEIGHT - PAD_Y}
              stroke="var(--ramp-40)"
              strokeWidth={1}
              strokeDasharray="2,2"
            />
            <circle cx={x(hoverIndex)} cy={y(points[hoverIndex].kri)} r={4} fill="var(--signal)" stroke="var(--surface-1)" strokeWidth={2} />
          </>
        )}
      </svg>
      <div className="tabular text-[11px] text-ramp-40 flex justify-between">
        <span>{day(points[0].epoch)}</span>
        <span>max {max}</span>
        <span>{day(last.epoch)}</span>
      </div>
      <details>
        <summary className="text-[11px] text-ramp-40 cursor-pointer">table</summary>
        <table className="w-full text-left text-[11px] tabular mt-1">
          <thead>
            <tr className="text-ramp-40 border-b border-rule-1">
              <th className="py-1 pr-4 font-normal">epoch</th>
              <th className="py-1 pr-4 font-normal">kri</th>
            </tr>
          </thead>
          <tbody>
            {points.map((p) => (
              <tr key={p.epoch} className="border-b border-rule-1/50">
                <td className="py-1 pr-4 text-ramp-80">{day(p.epoch)}</td>
                <td className="py-1 pr-4 text-ramp-100">{p.kri}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </details>
    </div>
  );
}

function day(t: number): string {
  return new Date(t * 1000).toISOString().slice(0, 10);
}
