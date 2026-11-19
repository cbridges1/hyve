// A cluster, the console's way: its fields and conditions in cards, with
// scale (control plane and each worker pool, or the class's node count
// when it has no pools: Civo's), variables, expiry, a kubeconfig, YAML
// and delete: what 'hyve cluster' does. What's deployed on it, and why, is
// its Deployments tab, .../<name>/deployments.
// An imported cluster has no size or version to change, and deleting it
// detaches it.
import { lazy, Suspense, useEffect, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { ClusterDeployments } from "../components/ClusterDeployments";
import { ClusterRemove, type RemoveMode } from "../components/ClusterRemove";
import {
  BackLink,
  Card,
  ErrorBanner,
  Field,
  Loading,
  Pill,
  StatusPill,
  StickyHeader,
  Tabs,
} from "../components/ui";
import {
  EXTEND_HOURS,
  isoSeconds,
  NODE_COUNT_VARIABLE,
  originOf,
  setExpiry,
  variableText,
  variableValue,
} from "../lib/clusters";
import { clusterStatus, download, expiry, kubeconfig } from "../lib/format";
import { useAction, useLoad } from "../lib/hooks";
import { kube, type Obj, patch, remove, save } from "../lib/kube";
import { paths } from "../lib/routes";
import { ResourceMonitor } from "../components/ResourceMonitor";
import { openShell } from "../lib/kubectlShell";
import { ANNOTATIONS, LABELS, NEVER, TYPES } from "../lib/types";

// The explorer (and xterm.js) load when the tab is opened.
const Explorer = lazy(() =>
  import("../components/explorer/Explorer").then((m) => ({
    default: m.Explorer,
  })),
);

type Form = {
  controlPlane: string;
  sizes: string[];
  nodes: string;
  expireAt: string;
};

function poolsOf(c: Obj): any[] {
  const topo = c.spec?.topology || {};
  return [
    ...(topo.workers?.machineDeployments || []),
    ...(topo.workers?.machinePools || []),
  ];
}

// The node-count variable, for a cluster whose class has no worker groups.
function nodeCountOf(c: Obj): { value: unknown } | undefined {
  if (poolsOf(c).length) return undefined;
  return (c.spec?.topology?.variables || []).find(
    (v: any) => v.name === NODE_COUNT_VARIABLE,
  );
}

function formOf(c: Obj): Form {
  const at = Date.parse(c.metadata.annotations?.[ANNOTATIONS.EXPIRES_AT] || "");
  return {
    controlPlane: String(c.spec?.topology?.controlPlane?.replicas ?? ""),
    sizes: poolsOf(c).map((w) => String(w.replicas ?? "")),
    nodes: variableText(nodeCountOf(c)?.value),
    // datetime-local wants local time, without seconds' zone.
    expireAt: Number.isNaN(at)
      ? ""
      : new Date(at - new Date().getTimezoneOffset() * 60000)
          .toISOString()
          .slice(0, 16),
  };
}

const sameForm = (a: Form, b: Form) =>
  a.controlPlane === b.controlPlane &&
  a.nodes === b.nodes &&
  a.expireAt === b.expireAt &&
  a.sizes.length === b.sizes.length &&
  a.sizes.every((v, i) => v === b.sizes[i]);

export default function ClusterDetail() {
  const { namespace = "", name = "", tab } = useParams();
  const navigate = useNavigate();
  const {
    data: value,
    error,
    loading,
    setData,
  } = useLoad((r) => r.find(TYPES.CLUSTER, namespace, name), [namespace, name]);
  // An imported cluster's control plane: its endpoint, version and reach.
  const imported = !!value && originOf(value) === "imported";
  const { data: importedCP } = useLoad(
    (r) =>
      imported
        ? r
            .find(TYPES.IMPORTED_CONTROL_PLANE, namespace, name)
            .catch(() => null)
        : Promise.resolve(null),
    [namespace, name, imported],
  );
  const action = useAction();
  const [form, setForm] = useState<Form>({
    controlPlane: "",
    sizes: [],
    nodes: "",
    expireAt: "",
  });
  const [pristine, setPristine] = useState<Form>(form);
  const [upgradeTo, setUpgradeTo] = useState("");
  const [removing, setRemoving] = useState<RemoveMode | null>(null);
  // The variables being edited, as text; null when not editing.
  const [vars, setVars] = useState<{ name: string; value: string }[] | null>(
    null,
  );

  const reset = (c: Obj) => {
    const f = formOf(c);
    setForm(f);
    setPristine(f);
  };

  // A change on the cluster: show it, but keep inputs the user has edited.
  const rv = value?.metadata.resourceVersion;
  useEffect(() => {
    if (value && sameForm(form, pristine)) reset(value);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [rv]);

  if (loading) return <Loading />;
  if (!value)
    return (
      <div>
        <BackLink to={paths.clusters} label="Clusters" />
        <ErrorBanner error={error} />
      </div>
    );

  const m = value.metadata;
  const l = m.labels || {};
  const topo = value.spec?.topology || {};
  const pools = poolsOf(value);
  const nodeCount = nodeCountOf(value);
  const expires = m.annotations?.[ANNOTATIONS.EXPIRES_AT] || "";
  const never = !expires || expires === NEVER;

  // Runs a change, then shows the cluster as it is now (also after a
  // failure).
  const act = (fn: () => Promise<unknown>) =>
    action.run(async () => {
      try {
        await fn();
      } finally {
        const fresh = await kube
          .find(TYPES.CLUSTER, namespace, name)
          .catch(() => null);
        if (fresh) {
          setData(fresh);
          reset(fresh);
        }
      }
    });

  const extend = () => {
    const cur = Date.parse(expires);
    return act(() =>
      setExpiry(
        value,
        isoSeconds(
          new Date(
            Math.max(Date.now(), Number.isNaN(cur) ? 0 : cur) +
              EXTEND_HOURS * 3600 * 1000,
          ),
        ),
      ),
    );
  };

  const scale = () =>
    act(async () => {
      const o: Obj = JSON.parse(JSON.stringify(value));
      const t = o.spec.topology;
      if (form.controlPlane !== "" && t.controlPlane)
        t.controlPlane.replicas = Number(form.controlPlane);
      poolsOf(o).forEach((w, i) => {
        if (form.sizes[i] !== "" && form.sizes[i] !== undefined)
          w.replicas = Number(form.sizes[i]);
      });
      const n = nodeCountOf(o);
      if (n && form.nodes !== "") n.value = Number(form.nodes);
      await save(TYPES.CLUSTER, o);
    });

  // The webhook refuses a variable the template locks.
  const saveVariables = () =>
    act(async () => {
      const o: Obj = JSON.parse(JSON.stringify(value));
      o.spec.topology.variables = (vars || []).map((v) => ({
        name: v.name,
        value: variableValue(v.value),
      }));
      await save(TYPES.CLUSTER, o);
      setVars(null);
    });

  const setExpiryAt = () => {
    const t = new Date(form.expireAt);
    if (Number.isNaN(t.getTime())) {
      action.setError("Pick a date and time");
      return;
    }
    return act(() => setExpiry(value, isoSeconds(t)));
  };

  // Cluster API rolls the control plane to the new version, then the
  // workers ('hyve cluster upgrade').
  const upgrade = () => {
    const v = upgradeTo.trim();
    if (!v) return;
    return act(async () => {
      await patch(TYPES.CLUSTER, namespace, name, {
        spec: { topology: { version: v.startsWith("v") ? v : `v${v}` } },
      });
      setUpgradeTo("");
    });
  };

  const downloadKubeconfig = () =>
    action.run(async () =>
      download(
        `${m.namespace}-${m.name}.kubeconfig`,
        await kubeconfig(`${m.namespace}.${m.name}`),
      ),
    );

  const del = async () => {
    await remove(TYPES.CLUSTER, value);
    navigate(paths.clusters);
  };

  const sizeChanged =
    (topo.controlPlane &&
      form.controlPlane !== "" &&
      Number(form.controlPlane) !== topo.controlPlane.replicas) ||
    pools.some(
      (w, i) =>
        form.sizes[i] !== "" &&
        form.sizes[i] !== undefined &&
        Number(form.sizes[i]) !== w.replicas,
    ) ||
    (!!nodeCount &&
      form.nodes !== "" &&
      Number(form.nodes) !== nodeCount.value);

  const origin = originOf(value);
  const cpEndpoint = importedCP?.spec?.controlPlaneEndpoint;
  const fields: [string, string][] = imported
    ? [
        ["Environment", m.namespace || ""],
        [
          "Origin",
          "Imported: hyve doesn't create, scale, upgrade or delete it",
        ],
        [
          "Template",
          l[LABELS.TEMPLATE]
            ? `${l[LABELS.TEMPLATE_NAMESPACE] || m.namespace}/${l[LABELS.TEMPLATE]} (its deployments)`
            : "—",
        ],
        [
          "API server",
          cpEndpoint?.host ? `${cpEndpoint.host}:${cpEndpoint.port}` : "—",
        ],
        ["Kubernetes", importedCP?.status?.version || "—"],
        ["Phase", value.status?.phase || "—"],
        ["Expires", never ? "Never" : new Date(expires).toLocaleString()],
        ["Created by", m.annotations?.[ANNOTATIONS.CREATOR] || "—"],
      ]
    : [
        ["Environment", m.namespace || ""],
        ...(origin === "adopted"
          ? ([
              ["Origin", "Adopted: an existing cluster its provider took over"],
            ] as [string, string][])
          : []),
        [
          "Template",
          l[LABELS.TEMPLATE]
            ? `${l[LABELS.TEMPLATE_NAMESPACE] || m.namespace}/${l[LABELS.TEMPLATE]}`
            : "—",
        ],
        [
          "ClusterClass",
          topo.classRef
            ? `${topo.classRef.namespace || m.namespace}/${topo.classRef.name}`
            : topo.class || "—",
        ],
        ["Kubernetes", topo.version || "—"],
        ["Control plane", String(topo.controlPlane?.replicas ?? "—")],
        [
          "Workers",
          // No replicas (an adopted cluster's pool): it keeps the nodes it has.
          pools
            .map((w) =>
              w.replicas === undefined
                ? `${w.class} (its own count)`
                : `${w.replicas} × ${w.class}`,
            )
            .join(", ") || "—",
        ],
        ["Phase", value.status?.phase || "—"],
        ["Expires", never ? "Never" : new Date(expires).toLocaleString()],
        ["Created by", m.annotations?.[ANNOTATIONS.CREATOR] || "—"],
      ];
  const variables: [string, string][] = (topo.variables || []).map((x: any) => [
    x.name,
    JSON.stringify(x.value),
  ]);
  const conditions = [
    ...(value.status?.conditions || []),
    ...(importedCP?.status?.conditions || []).map((c: any) => ({
      ...c,
      type: `Reach: ${c.type}`,
    })),
  ].filter(
    (c: any) =>
      c.status !== "True" &&
      c.message &&
      c.type !== "Paused" &&
      c.type !== "Deleting",
  );
  const tabs = [
    { label: "Overview", to: paths.cluster(namespace, name), active: !tab },
    {
      label: "Deployments",
      to: paths.cluster(namespace, name, "deployments"),
      active: tab === "deployments",
    },
    {
      label: "Explorer",
      to: paths.cluster(namespace, name, "explorer"),
      active: tab === "explorer",
    },
  ];

  return (
    <div className="space-y-4">
      <StickyHeader>
        <div>
          <BackLink to={paths.clusters} label="Clusters" />
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div className="flex min-w-0 flex-wrap items-center gap-3">
              <h1 className="mb-0 truncate">{m.name}</h1>
              <Pill tone="env" label={m.namespace || ""} />
              <StatusPill status={expiry(expires)} />
              <StatusPill status={clusterStatus(value)} />
            </div>
            <div className="flex flex-wrap items-center gap-2">
              <button
                type="button"
                className="btn btn-secondary"
                title="kubectl, helm and k9s in your browser, as you, on this cluster"
                onClick={() =>
                  openShell({
                    cluster: `${namespace}.${name}`,
                    label: `${namespace}/${name}`,
                  })
                }
              >
                Kubectl shell
              </button>
              <button
                type="button"
                className="btn btn-secondary"
                title="A kubeconfig that runs 'hyve token' for your CLI session; it holds no secret"
                onClick={downloadKubeconfig}
              >
                Kubeconfig
              </button>
              <Link
                className="btn btn-secondary"
                to={paths.yaml(TYPES.CLUSTER, m.namespace, m.name)}
              >
                YAML
              </Link>
              {!m.deletionTimestamp &&
                (imported ? (
                  <button
                    type="button"
                    className="btn border-red-300 text-red-600 hover:bg-red-50 dark:border-red-800 dark:text-red-400 dark:hover:bg-red-950/40"
                    disabled={action.busy}
                    onClick={() => setRemoving("detach")}
                  >
                    Detach
                  </button>
                ) : (
                  <>
                    <button
                      type="button"
                      className="btn btn-secondary"
                      title="Stop managing it with hyve and leave it running"
                      disabled={action.busy}
                      onClick={() => setRemoving("release")}
                    >
                      Release
                    </button>
                    <button
                      type="button"
                      className="btn border-red-300 text-red-600 hover:bg-red-50 dark:border-red-800 dark:text-red-400 dark:hover:bg-red-950/40"
                      disabled={action.busy}
                      onClick={() => setRemoving("delete")}
                    >
                      Delete
                    </button>
                  </>
                ))}
            </div>
          </div>
        </div>
        <Tabs tabs={tabs} className="" />
      </StickyHeader>
      <ErrorBanner error={error} />
      <ErrorBanner error={action.error} />
      {removing && (
        <ClusterRemove
          cluster={value}
          mode={removing}
          onClose={() => setRemoving(null)}
          onDelete={del}
          onDone={(message) =>
            navigate(paths.clusters, { state: { message } })
          }
        />
      )}

      {tab === "deployments" ? (
        <Card>
          <ClusterDeployments cluster={value} />
        </Card>
      ) : tab === "explorer" ? (
        <Suspense fallback={<Loading />}>
          <Explorer clusterId={`${namespace}.${name}`} />
        </Suspense>
      ) : (
        <>
          {value.status?.phase === "Provisioned" && (
            <ResourceMonitor clusterId={`${namespace}.${name}`} />
          )}
          <div className="grid gap-4 lg:grid-cols-3">
            <div className="lg:col-span-2">
              <Card title="Details">
                {fields.map(([k, v]) => (
                  <Field key={k} label={k}>
                    {v}
                  </Field>
                ))}
                <Field label="CLI">
                  <code>
                    hyve cluster kubeconfig {m.name} -e {m.namespace}
                  </code>
                </Field>
              </Card>
            </div>
            <div className="space-y-4">
              {!imported && (
                <>
                  <Card title="Size">
                    <form
                      className="space-y-3"
                      onSubmit={(e) => {
                        e.preventDefault();
                        scale();
                      }}
                    >
                      {topo.controlPlane && (
                        <label className="flex items-center justify-between gap-3 text-sm">
                          <span>Control plane</span>
                          <input
                            className="w-24!"
                            type="number"
                            min={1}
                            aria-label="Control-plane replicas"
                            value={form.controlPlane}
                            onChange={(e) =>
                              setForm({ ...form, controlPlane: e.target.value })
                            }
                          />
                        </label>
                      )}
                      {pools.map((w, i) => (
                        <label
                          key={w.name}
                          className="flex items-center justify-between gap-3 text-sm"
                        >
                          <span className="truncate">
                            Workers: {w.name}{" "}
                            <span className="text-neutral-500">
                              ({w.class})
                            </span>
                          </span>
                          <input
                            className="w-24!"
                            type="number"
                            min={0}
                            placeholder="its own"
                            aria-label={`${w.name} replicas`}
                            value={form.sizes[i] ?? ""}
                            onChange={(e) =>
                              setForm({
                                ...form,
                                sizes: form.sizes.map((s, j) =>
                                  j === i ? e.target.value : s,
                                ),
                              })
                            }
                          />
                        </label>
                      ))}
                      {nodeCount && (
                        <label className="flex items-center justify-between gap-3 text-sm">
                          <span>Nodes</span>
                          <input
                            className="w-24!"
                            type="number"
                            min={1}
                            aria-label="Nodes"
                            value={form.nodes}
                            onChange={(e) =>
                              setForm({ ...form, nodes: e.target.value })
                            }
                          />
                        </label>
                      )}
                      <button
                        type="submit"
                        className="btn btn-primary w-full"
                        disabled={action.busy || !sizeChanged}
                      >
                        Scale
                      </button>
                    </form>
                  </Card>
                  <Card title="Kubernetes version">
                    <p className="mb-3 text-sm">
                      {topo.version || "—"}
                      {value.status?.observedGeneration !== undefined &&
                        value.metadata.generation !==
                          value.status?.observedGeneration && (
                          <span className="text-muted"> (changing)</span>
                        )}
                    </p>
                    <form
                      className="flex gap-2"
                      onSubmit={(e) => {
                        e.preventDefault();
                        upgrade();
                      }}
                    >
                      <input
                        placeholder="e.g. v1.35.2"
                        aria-label="Upgrade to"
                        value={upgradeTo}
                        onChange={(e) => setUpgradeTo(e.target.value)}
                      />
                      <button
                        type="submit"
                        className="btn btn-secondary"
                        disabled={
                          action.busy ||
                          !upgradeTo.trim() ||
                          upgradeTo.trim() === topo.version
                        }
                      >
                        Upgrade
                      </button>
                    </form>
                    <p className="hint">
                      One minor version at a time; the control plane goes first,
                      then the workers.
                    </p>
                  </Card>
                </>
              )}
              <Card title="Expiry">
                <p className="mb-3 text-sm">
                  {never
                    ? "Never expires."
                    : `Deleted at ${new Date(expires).toLocaleString()}.`}
                </p>
                <form
                  className="mb-3 flex gap-2"
                  onSubmit={(e) => {
                    e.preventDefault();
                    setExpiryAt();
                  }}
                >
                  <input
                    type="datetime-local"
                    aria-label="Expires at"
                    value={form.expireAt}
                    onChange={(e) =>
                      setForm({ ...form, expireAt: e.target.value })
                    }
                  />
                  <button
                    type="submit"
                    className="btn btn-secondary"
                    disabled={action.busy || !form.expireAt}
                  >
                    Set
                  </button>
                </form>
                <div className="flex flex-wrap gap-2">
                  <button
                    type="button"
                    className="btn btn-secondary btn-sm"
                    disabled={action.busy}
                    onClick={extend}
                  >
                    {never ? "Expire in 8 hours" : "Extend 8 hours"}
                  </button>
                  {!never && (
                    <button
                      type="button"
                      className="btn btn-tertiary btn-sm"
                      disabled={action.busy}
                      onClick={() => act(() => setExpiry(value, NEVER))}
                    >
                      Never expire
                    </button>
                  )}
                </div>
              </Card>
              {variables.length > 0 && (
                <Card
                  title="Variables"
                  action={
                    !imported &&
                    !vars && (
                      <button
                        type="button"
                        className="btn btn-tertiary btn-sm"
                        onClick={() =>
                          setVars(
                            (topo.variables || []).map((x: any) => ({
                              name: x.name,
                              value: variableText(x.value),
                            })),
                          )
                        }
                      >
                        Edit
                      </button>
                    )
                  }
                >
                  {vars ? (
                    <form
                      className="space-y-3"
                      onSubmit={(e) => {
                        e.preventDefault();
                        saveVariables();
                      }}
                    >
                      {vars.map((v, i) => (
                        <label key={v.name} className="block text-sm">
                          <span>{v.name}</span>
                          <input
                            className="mt-1 w-full"
                            value={v.value}
                            onChange={(e) =>
                              setVars(
                                vars.map((x, j) =>
                                  j === i ? { ...x, value: e.target.value } : x,
                                ),
                              )
                            }
                          />
                        </label>
                      ))}
                      <p className="hint">
                        Its provider decides what reaches a running cluster:
                        Civo resizes for nodeCount, but reads nodeSize only
                        when it creates the cluster.
                      </p>
                      <div className="flex gap-2">
                        <button
                          type="submit"
                          className="btn btn-primary"
                          disabled={action.busy}
                        >
                          Save
                        </button>
                        <button
                          type="button"
                          className="btn btn-secondary"
                          onClick={() => setVars(null)}
                        >
                          Cancel
                        </button>
                      </div>
                    </form>
                  ) : (
                    variables.map(([k, v]) => (
                      <Field key={k} label={k}>
                        <code>{v}</code>
                      </Field>
                    ))
                  )}
                </Card>
              )}
            </div>
          </div>

          {conditions.length > 0 && (
            <Card title="Conditions">
              {conditions.map((c: any) => (
                <Field key={c.type} label={c.type}>
                  {c.message}
                </Field>
              ))}
            </Card>
          )}
        </>
      )}
    </div>
  );
}
