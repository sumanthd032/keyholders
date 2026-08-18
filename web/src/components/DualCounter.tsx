import type { AuditView } from "@/lib/types";

function percent(part: number, whole: number): string {
  if (whole === 0) return "0%";
  return `${((100 * part) / whole).toFixed(1)}%`;
}

/**
 * Union count and coexistence count side by side, permanently, with the gap between them labelled.
 * The gap is the argument the whole product makes, so it stays on screen rather than living in a
 * methodology note a reader has to go find.
 */
export function DualCounter({ audit }: { audit: AuditView }) {
  const phantomVersions = audit.union_versions_reached - audit.versions_reached;
  const phantomPackages = audit.union_packages_reached - audit.packages_reached;

  return (
    <div className="border-t border-b border-rule-1 px-8 py-4 flex flex-wrap items-baseline gap-x-8 gap-y-2">
      <Metric label="coexistence" value={audit.keyholder_count} accent="signal" />
      <Metric label="union graph" value={audit.union_keyholder_count} accent="ramp-100" />
      <Metric
        label="phantom"
        value={audit.phantom_keyholder_count}
        accent="danger"
        detail={`${percent(phantomVersions, audit.union_versions_reached)} of versions, ${phantomPackages} packages never coexisted`}
      />
    </div>
  );
}

function Metric({
  label,
  value,
  accent,
  detail,
}: {
  label: string;
  value: number;
  accent: "signal" | "danger" | "ramp-100";
  detail?: string;
}) {
  const color =
    accent === "signal" ? "text-signal" : accent === "danger" ? "text-danger" : "text-ramp-100";
  return (
    <div className="flex flex-col">
      <span className="text-[11px] uppercase tracking-wide text-ramp-40">{label}</span>
      <span className={`tabular text-2xl font-medium ${color}`}>{value.toLocaleString()}</span>
      {detail ? <span className="text-[11px] text-ramp-60">{detail}</span> : null}
    </div>
  );
}
