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
  access?: { method?: string; tunnel?: { provider?: string } }
}

export type ClusterResources = {
  resources: ResourceRef[] | null
  appliedResources: Record<string, AppliedResource> | null
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

// ── Access methods (internal/api/accessmethods.go's accessMethodDTO) ────
// Read-only from this console — admins own writes via kubectl apply
// directly, same stance HyveConfig already takes.

export type AccessMethodSpec = {
  driver?: DriverRef
  inlineAuth?: string
  requiredEnv?: string[]
  serverURL: string
  runner?: RunnerSpec
}
export type AccessMethod = { name: string; spec: AccessMethodSpec; requiredEnv?: string[] }
export type MintAccessMethodRequest = {
  clusterName: string
  accessMethodClusterID: string
  credentialEnv?: Record<string, string>
}
export type MintAccessMethodResponse = { kubeconfig: string }

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

// ── Environments (internal/api/environments.go) — superadmin-only, one
// per tenant namespace (see HYVE-MULTI-TENANCY-PLAN.md's "Phase 2").

export type Environment = { name: string; namespace: string }
