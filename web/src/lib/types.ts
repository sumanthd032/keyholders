// Mirrors internal/query/view.go's JSON shapes exactly. Kept as one file so a change on the Go side
// has one obvious place to update on this side, rather than scattered inline shapes per component.

export type Fraction = {
  count: number;
  of: number;
  known: boolean;
};

export type RiskTerm = {
  name: string;
  value: number;
  weight: number;
  known: boolean;
  contribution: number;
  detail: string;
};

export type Keyholder = {
  handle: string;
  packages: number;
  holds: string[];
  since?: number;
  risk: number;
  risk_terms: RiskTerm[];
  solo: Fraction;
  no_provenance: Fraction;
  install_script: Fraction;
  last_publish?: number;
  last_release?: number;
};

export type Cut = {
  handle: string;
  controls: number;
  packages_lost: number;
  versions_lost: number;
  downstream_lost: number;
  orphaned: string[] | null;
  irreplaceable: boolean;
};

export type Weights = {
  reach: number;
  staleness: number;
  solo: number;
  no_provenance: number;
  install_script: number;
};

export type AuditView = {
  project: string;
  format: string;
  pins: number;
  sources_in_graph: number;
  keyholders: Keyholder[];
  keyholder_count: number;
  union_keyholder_count: number;
  phantom_keyholder_count: number;
  packages_reached: number;
  union_packages_reached: number;
  versions_reached: number;
  union_versions_reached: number;
  cuts: Cut[];
  weights: Weights;
  depth: number;
  truncated: boolean;
  kappa: number;
  elapsed_ms: number;
};

export type Frontier = {
  depth: number;
  urns: string[];
};

/** package name from a package or version URN: "pkg:npm/left-pad@1.0.0" -> "left-pad". */
export function packageNameOf(urn: string): string {
  const withoutScheme = urn.replace(/^pkg:[^/]+\//, "");
  const at = withoutScheme.lastIndexOf("@");
  // A scoped name, "@scope/name@1.0.0", has an earlier "@" for the scope itself; only split on
  // the one that comes after the "/" separating scope from name.
  const slash = withoutScheme.indexOf("/");
  if (at > slash) {
    return withoutScheme.slice(0, at);
  }
  return withoutScheme;
}
