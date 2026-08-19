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

export type GraphNode = {
  package: string;
  coexistent: boolean;
};

export type GraphEdge = {
  from: string;
  to: string;
  coexistent: boolean;
};

export type Graph = {
  nodes: GraphNode[];
  edges: GraphEdge[];
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
  graph: Graph;
};

export type Frontier = {
  depth: number;
  urns: string[];
};

export type RecordedProject = {
  name: string;
  locked_at: number;
};

export type AdvisoryInfo = {
  id: string;
  summary: string;
  severity: string;
};

export type DependencyRef = { name: string; range: string };
export type DependencyChange = { name: string; from: string; to: string };
export type DependencyDiff = {
  added: DependencyRef[];
  removed: DependencyRef[];
  changed: DependencyChange[];
};
export type Introduction = {
  package: string;
  first_affected: string;
  published_at: number;
  previous?: string;
  diff: DependencyDiff;
};

export type ExposedProject = { project: string; locked_at: number };
export type ResolvedLiveExposure = {
  project: string;
  locked_at: number;
  dependent: string;
  valid_from: number;
  valid_to: number;
};
export type SharedMaintainer = { maintainer: string; package: string };
export type TyposquatNeighbor = {
  name: string;
  direction: string;
  distance: number;
  popularity_ratio: number;
};

export type IncidentView = {
  package: string;
  version: string;
  advisories: AdvisoryInfo[];
  introductions: Introduction[];
  exposed: ExposedProject[];
  resolved_while_live: ResolvedLiveExposure[];
  shared_maintainers: SharedMaintainer[];
  typosquats: TyposquatNeighbor[];
};

export type Hop = {
  from: string;
  to: string;
  range: string;
  valid_from: number;
  valid_to: number;
};

export type Chain = {
  package: string;
  found: boolean;
  valid_from?: number;
  valid_to?: number;
  hops?: Hop[];
};

export type PathView = {
  handle: string;
  holds: string[];
  chains: Chain[];
};

export type IncidentTraversalDone = {
  audit: AuditView;
  target: string;
  reached: boolean;
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

/** package URN from a version URN: "pkg:npm/left-pad@1.0.0" -> "pkg:npm/left-pad". Keeps the scheme
 * and ecosystem prefix, unlike packageNameOf, since this is used to match graph node ids rather than
 * to display a label. */
export function packageURNOf(versionURN: string): string {
  const at = versionURN.lastIndexOf("@");
  const slash = versionURN.indexOf("/");
  return at > slash ? versionURN.slice(0, at) : versionURN;
}
