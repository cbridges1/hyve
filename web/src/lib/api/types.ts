// Field names copied verbatim from the Go CRD types in
// internal/apis/hyve/v1alpha1/*.go and the DTOs in internal/api/*.go —
// keep these in sync with that package, not with hyve-studio's old
// src/lib/api.ts, which predates the CRD-backed rewrite entirely.

export type DriverRef = { source?: string; version?: string }

export type WorkflowRef = { name?: string; source?: string; path?: string }

export type WorkflowsSpec = {
  beforeCreate?: WorkflowRef[]
  onCreate?: WorkflowRef[]
  afterCreate?: WorkflowRef[]
  onDelete?: WorkflowRef[]
  afterDelete?: WorkflowRef[]
  preReconcile?: WorkflowRef[]
}

export type HelmSpec = {
  chart?: string
  repo?: string
  version?: string
  namespace?: string
  values?: Record<string, string>
}

export type SecretKeyRef = { env: string; key?: string }
export type SecretSpec = { namespace?: string; type?: string; keys: SecretKeyRef[] }

export type ResourceRef = {
  name: string
  source?: string
  namespace?: string
  delete?: boolean
  helm?: HelmSpec
  secret?: SecretSpec
}

export type AppliedObject = { apiVersion: string; kind: string; namespace?: string; name: string }
export type AppliedResource = {
  sourceSHA256: string
  helm?: boolean
  namespace?: string
  appliedAt: string
  objects?: AppliedObject[]
}

export type RunnerSpec = { image?: string }

// ── Clusters ─────────────────────────────────────────────────────────────

export type Condition = {
  type: string
  status: 'True' | 'False' | 'Unknown'
  reason?: string
  message?: string
  lastTransitionTime?: string
  observedGeneration?: number
}

// clusterDTO (internal/api/clusters.go) — deliberately excludes
// driverOutputs/params/kubeconfig data. Don't add fields here that aren't
// actually in that response; use authContextApi for the one legitimate
// case that needs driverOutputs/params.
export type ClusterSummary = {
  name: string
  driver: string
  conditions?: Condition[]
  observedGeneration: number
  accessMethod?: string
  accessLastMinted?: string
  // metadata.deletionTimestamp != nil — DELETE only ever sets this
  // (ClusterDefinitionFinalizer keeps the object around until the
  // controller finishes OnDelete/driver-delete/AfterDelete), so a cluster
  // can sit in this state for a while.
  pendingDeletion?: boolean
  // Added for PATCH /clusters/<name>'s sake — see clusterDTO's own doc
  // comment on why this isn't the same concern as the driverOutputs/
  // kubeconfig exclusion right above.
  spec?: ClusterDefinitionSpec
  // Agent/agentStatus mirror internal/api's clusterDTO fields of the same
  // name — spec.access.agent (nil/undefined when never configured) and
  // status.agent (hyve-agent's live connectivity snapshot, written by
  // whichever hyve-api process holds this cluster's tunnel connection).
  agent?: AgentSpec
  agentStatus?: AgentStatus
}

export type AgentSpec = { enabled?: boolean; proxy?: boolean }
export type AgentStatus = {
  connected?: boolean
  lastConnectedAt?: string
  lastDisconnectedAt?: string
  version?: string
}

export type ClusterDefinitionSpec = {
  region?: string
  driver: DriverRef
  runner?: RunnerSpec
  params?: Record<string, string>
  workflows?: WorkflowsSpec
  resources?: ResourceRef[]
  delete?: boolean
  pause?: boolean
  expiresAt?: string
  dependsOn?: string[]
  access?: { method?: string; tunnel?: { provider?: string }; agent?: AgentSpec }
}

export type ClusterResources = {
  resources: ResourceRef[] | null
  appliedResources: Record<string, AppliedResource> | null
}

export type ClusterEvent = {
  type: string
  reason: string
  message: string
  count: number
  lastSeen: string
}

// Mirrors internal/api's clusterActivityDTO — GET /clusters/<name>/events'
// response shape. The only place a create/delete operation's actual output
// survives (k8sjob.Run always deletes its dispatched Job right after
// fetching logs), plus the lifecycle Events a reconcile emits. events is
// one page (see ?limit=/?offset=, ClusterDetailPage's own pagination
// state) — totalEvents is the full count, for rendering "X-Y of Z".
export type ClusterActivity = {
  events: ClusterEvent[] | null
  totalEvents: number
  lastCreateOutput?: string
  lastDeleteOutput?: string
}

export type CreateClusterFromTemplateRef = { name: string; region?: string; params?: Record<string, string> }
export type CreateClusterRequest = {
  name: string
  spec?: ClusterDefinitionSpec
  template?: CreateClusterFromTemplateRef
}

// ── Templates ────────────────────────────────────────────────────────────

export type TemplateSpec = {
  description?: string
  driver: DriverRef
  runner?: RunnerSpec
  params?: Record<string, string>
  region?: string
  workflows?: WorkflowsSpec
  resources?: ResourceRef[]
  schedule?: string
  lockParams?: boolean
}

export type Template = { name: string; spec: TemplateSpec }
export type CreateTemplateRequest = { name: string; spec: TemplateSpec }
export type RenderTemplateRequest = { region?: string; params?: Record<string, string> }

// ── Workflows ────────────────────────────────────────────────────────────

export type WorkflowInput = { name: string; description?: string; default?: string }
export type WorkflowStep = {
  name: string
  description?: string
  if?: string
  command?: string
  script?: string
  action?: string
  with?: Record<string, string>
  env?: Record<string, string>
  workingDir?: string
  timeout?: string
  continueOnError?: boolean
  container?: string
}
export type WorkflowJob = {
  name: string
  description?: string
  if?: string
  dependsOn?: string[]
  cluster?: string
  env?: Record<string, string>
  steps: WorkflowStep[]
  timeout?: string
  retry?: { maxAttempts: number; delay?: string }
  container?: string
}
export type WorkflowSpec = {
  description?: string
  inputs?: WorkflowInput[]
  requirements?: { tools?: { name: string; version?: string; description?: string }[]; secrets?: { name: string; provider?: string; required: boolean; description?: string }[] }
  preFlight?: { cluster?: string }
  triggers?: { type: string; config?: Record<string, string> }[]
  jobs: WorkflowJob[]
  env?: Record<string, string>
  runtime?: string
  secretsFrom?: { cluster: string; namespace: string; secretRef: string; keys: { key: string; env?: string }[] }[]
}

export type RefStatus = {
  source: string
  resolved: boolean
  rawVersion?: string
  resolvedVersion?: string // workflows only
  sha256?: string
  error?: string
}

export type Workflow = { name: string; spec?: WorkflowSpec; refStatus?: RefStatus }
export type CreateWorkflowRequest = { name: string; spec: WorkflowSpec }

// ── Resources ────────────────────────────────────────────────────────────

export type ResourceSpec = { manifest: string }
export type ResourceItem = { name: string; spec?: ResourceSpec; refStatus?: RefStatus }
export type CreateResourceRequest = { name: string; spec: ResourceSpec }

// ── Modules ──────────────────────────────────────────────────────────────

export type ModuleSpec = { source?: string; version?: string }
export type ModuleStatus = { resolved?: boolean; sha256?: string; resolvedAt?: string; error?: string }
export type Module = { name: string; spec: ModuleSpec; status: ModuleStatus }

// ── Auth context (client-side auth handshake) ───────────────────────────

export type AuthContext = {
  driverSource: string
  driverVersion: string
  region?: string
  params?: Record<string, string>
  driverOutputs?: Record<string, string>
  authFileName: string
  authFileContent: string
  tools?: { name: string; description?: string }[]
}

// ── Workflow runs (internal/api/workflowruns.go) — cluster mode's `hyve
// workflow run` execution surface. No list endpoint exists (single-name
// lookup only), so this console only ever trigger-and-polls one at a time.

export type CreateWorkflowRunRequest = {
  workflow?: string
  source?: string
  path?: string
  cluster: string
  params?: Record<string, string>
}
export type WorkflowRunStatus = {
  phase: string
  message?: string
  output?: string
  startedAt?: string
  completedAt?: string
}

// ── Organizations (internal/api/organizations.go) — superadmin-only, one
// per tenant namespace. Business record lives in hyve-api's own Postgres/
// SQLite datastore (internal/orgdb), not a Kubernetes CRD — see
// HYVE-ORGANIZATION-MODEL-PROPOSAL.md (nexus-config/docs).

export type Organization = {
  name: string
  namespace: string
  plan?: string
  // reconcilingCluster is the ReconcilingCluster.Name this organization's
  // own resources currently live on (Milestone 6) — omitted/empty means
  // the control plane's own home cluster, never a raw id.
  reconcilingCluster?: string
  // migrating reflects an in-flight PATCH /organizations/{name} move —
  // every request against this organization's own resource types gets
  // 423 until it completes.
  migrating?: boolean
}

// An organization's own named sub-scope (Milestone 3 — see
// HYVE-ORGANIZATION-MODEL-PROPOSAL.md's "What moves to Postgres" section).
// Every organization gets a `default` environment automatically at
// creation time; this type is for the ones after that first one.
export type OrganizationEnvironment = { name: string }

// A physical cluster hyve-controller can reconcile organizations'
// infrastructure against (Milestone 6 — internal/orgdb.ReconcilingCluster).
// Deliberately excludes the kubeconfig itself: the server never echoes it
// back once registered (internal/api/reconcilingclusters.go).
export type ReconcilingCluster = {
  name: string
  reachable?: boolean
  lastCheckedAt?: string
  lastError?: string
}

// ── HyveConfig (internal/api/config.go) — superadmin-only, one singleton
// per install (GET/PATCH /config). Mirrors hyveConfigDTO field-for-field;
// exists distinguishes "no HyveConfig object yet" (the common starting
// state) from "one exists with every field at its zero value."
export type ImageInstall = { image: string; install: string }
export type HyveConfig = {
  exists: boolean
  strictResourceDelete: boolean
  defaultWorkflowImage?: string
  defaultModuleImage?: string
  defaultAgentImage?: string
  imageInstalls?: ImageInstall[]
  imagePullSecrets?: string[]
}
