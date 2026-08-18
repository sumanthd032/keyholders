"use client";

import { useEffect, useMemo, useState } from "react";
import type { Keyholder } from "@/lib/types";
import { usePrefersReducedMotion } from "@/lib/useReducedMotion";

const WAVE_MS = 900;
const MAX_WAVES = 6;

/**
 * The opening moment: every keyholder the audit actually found, filling the viewport in waves
 * rather than printed as a bare number. Every cell here is a real account this run's audit found
 * really can publish to something the project depends on; clicking one scrolls to its row in the
 * roster below, which is where its proof path lives.
 *
 * Respects prefers-reduced-motion by rendering the full grid immediately: the constraint this
 * interaction is built against is that it must never be the only way to see the answer, and it must
 * never block reading the resolved count.
 *
 * The caller must remount this component (a React `key` keyed on the audit run) for a new roll
 * call: reveal state resets by remounting rather than by an effect watching keyholders change, so
 * starting a fresh reveal never needs a synchronous setState at the top of an effect body.
 */
export function RollCall({
  keyholders,
  onSettle,
}: {
  keyholders: Keyholder[];
  onSettle?: () => void;
}) {
  const reduceMotion = usePrefersReducedMotion();
  const skipAnimation = reduceMotion || keyholders.length === 0;

  const [revealed, setRevealed] = useState(() => (skipAnimation ? keyholders.length : 0));
  const [settled, setSettled] = useState(skipAnimation);

  const waves = useMemo(() => {
    const n = Math.min(MAX_WAVES, keyholders.length || 1);
    return Array.from({ length: n }, (_, i) => Math.round(((i + 1) * keyholders.length) / n));
  }, [keyholders.length]);

  // Static reveal calls onSettle once, outside the render that decided to skip, rather than from an
  // effect body's own top level: onSettle is a callback into the parent, not a subscription this
  // component owns, so it belongs after the decision is made, not synchronized every render.
  useEffect(() => {
    if (skipAnimation) onSettle?.();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [skipAnimation]);

  useEffect(() => {
    if (skipAnimation) return;
    const timers = waves.map((count, i) =>
      setTimeout(() => setRevealed(count), (i + 1) * (WAVE_MS / waves.length)),
    );
    const settle = setTimeout(() => {
      setSettled(true);
      onSettle?.();
    }, WAVE_MS + 150);
    return () => {
      timers.forEach(clearTimeout);
      clearTimeout(settle);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [keyholders.length, skipAnimation]);

  function skip() {
    setRevealed(keyholders.length);
    setSettled(true);
    onSettle?.();
  }

  if (keyholders.length === 0) return null;

  return (
    <div className="relative px-8 py-6">
      {!settled && (
        <button
          onClick={skip}
          className="absolute right-8 top-6 text-[11px] text-ramp-40 hover:text-ramp-80 underline underline-offset-2"
        >
          skip
        </button>
      )}
      <div className="flex flex-wrap gap-1.5 max-h-64 overflow-hidden">
        {keyholders.slice(0, revealed).map((k) => (
          <span
            key={k.handle}
            className="tabular text-xs px-2 py-1 bg-surface-1 border border-rule-1 text-ramp-80 rounded-sm"
            style={skipAnimation ? undefined : { animation: "rollcall-in 220ms ease-out" }}
            title={`${k.handle} — ${k.packages} package${k.packages === 1 ? "" : "s"}`}
          >
            {k.handle}
          </span>
        ))}
      </div>
      <style>{`
        @keyframes rollcall-in {
          from { opacity: 0; transform: translateY(2px); }
          to { opacity: 1; transform: translateY(0); }
        }
      `}</style>
    </div>
  );
}
