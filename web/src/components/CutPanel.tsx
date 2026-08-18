"use client";

import type { Cut } from "@/lib/types";

/**
 * Cut a head: select an account and see what its removal actually costs, rather than assuming every
 * widely trusted publisher matters equally. Most turn out to change nothing, because another path
 * covers the same package; a few take dependents down with them, Beyond above zero, which is the
 * distinction Controls alone cannot make. Every number shown here is exactly what the server already
 * computed for the removal analysis; nothing about "what the new total would be" is fabricated
 * client side; if the server did not compute it, it is not shown as though it had.
 */
export function CutPanel({
  cuts,
  selected,
  onSelect,
}: {
  cuts: Cut[];
  selected: string | null;
  onSelect: (handle: string | null) => void;
}) {
  const ranked = [...cuts].sort((a, b) => b.packages_lost - a.packages_lost);
  const active = ranked.find((c) => c.handle === selected) ?? null;

  return (
    <div className="px-8 py-6">
      <h2 className="text-xs uppercase tracking-wide text-ramp-40 mb-3">
        cut a head — what removing one account actually costs
      </h2>
      <div className="flex flex-wrap gap-1.5 mb-4">
        {ranked.map((c) => (
          <button
            key={c.handle}
            onClick={() => onSelect(selected === c.handle ? null : c.handle)}
            className={`tabular text-xs px-2 py-1 border rounded-sm ${
              selected === c.handle
                ? "border-signal text-signal bg-surface-1"
                : c.irreplaceable
                  ? "border-danger-dim text-danger bg-surface-1"
                  : "border-rule-1 text-ramp-60 hover:border-ramp-40"
            }`}
          >
            {c.handle}
          </button>
        ))}
      </div>

      {active ? (
        <div className="tabular text-xs text-ramp-80 border-t border-rule-1 pt-4 flex flex-col gap-1">
          <p>
            removing <span className="text-ramp-100">{active.handle}</span> costs{" "}
            <span className="text-signal">{active.packages_lost}</span> packages (
            {active.versions_lost} versions)
          </p>
          <p>
            <span className="text-ramp-100">{active.controls}</span> directly controlled,{" "}
            <span className={active.downstream_lost > 0 ? "text-danger" : "text-ramp-60"}>
              {active.downstream_lost}
            </span>{" "}
            lost downstream through no package this account holds itself
          </p>
          {active.orphaned && active.orphaned.length > 0 && (
            <p className="text-ramp-60">
              downstream: {active.orphaned.map((p) => p.replace(/^pkg:npm\//, "")).join(", ")}
            </p>
          )}
          <p className={active.irreplaceable ? "text-danger" : "text-positive"}>
            {active.irreplaceable
              ? "irreplaceable: no other path covers what this account reaches"
              : "not irreplaceable: other paths cover this account's reach"}
          </p>
        </div>
      ) : (
        <p className="text-xs text-ramp-40">select an account to see what its removal costs</p>
      )}
    </div>
  );
}
