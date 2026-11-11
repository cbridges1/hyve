// Creates a Cluster labelled with a template. hyve-controller fills in the
// rest from the template and its ClusterClass when it's created, so this
// page sends only what the user chose. Adopting ('hyve cluster adopt')
// is the same with the existing cluster's ID in the template's adopt
// variable: the provider takes that cluster over instead of creating one.
import { dump } from "js-yaml";
import { useEffect, useMemo, useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { RadioGroup, SelectField, TextField } from "../components/form";
import { BackLink, Banner, Card, ErrorBanner, Loading } from "../components/ui";
import { classOf, isoSeconds, providerOfClass } from "../lib/clusters";
import { nextRun } from "../lib/cron";
import { useEnvironment } from "../lib/environment";
import { useAction, useLoad } from "../lib/hooks";
import { idOf, newObject, save, schemaFor, type Obj } from "../lib/kube";
import { paths } from "../lib/routes";
import { ANNOTATIONS, LABELS, NEVER, TYPES } from "../lib/types";

// Mirrors the template page's value encoding.
function toText(v: unknown): string {
  if (v === null || v === undefined) return "";
  return typeof v === "string" ? v : JSON.stringify(v);
}

function fromText(s: string): unknown {
  try {
    return JSON.parse(s);
  } catch {
    return s;
  }
}

type Row = { name: string; value: string; initial: string; locked: boolean };

// A template's variables as they apply to a new cluster, or to an adopted
// one: its adopt.variables in place, without its adopt.unset (or a null), as
// templates.Effective.
function rowsFor(spec: any, adopting: boolean): Row[] {
  const locked: string[] = spec.lockedVariables || [];
  const vars: Record<string, unknown> = { ...(spec.variables || {}) };
  if (adopting) {
    for (const [k, v] of Object.entries(spec.adopt?.variables || {})) {
      if (v === null) delete vars[k];
      else vars[k] = v;
    }
    for (const k of (spec.adopt?.unset as string[] | undefined) || [])
      delete vars[k];
  }
  return Object.entries(vars).map(([n, v]) => ({
    name: n,
    value: toText(v),
    initial: toText(v),
    locked: locked.includes(n),
  }));
}

const NAME_RE = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/;

export default function CreateCluster() {
  const [query] = useSearchParams();
  const navigate = useNavigate();
  const [picked] = useEnvironment();
  // Only one provider's templates, when asked for.
  const provider = query.get("provider") || "";
  const { data, error, loading } = useLoad(
    async (r) => {
      const [templates, classes, namespaces] = await Promise.all([
        r.findAll(TYPES.TEMPLATE),
        r.findAll(TYPES.CLUSTER_CLASS),
        r.findAll(TYPES.NAMESPACE),
      ]);
      const environments = namespaces
        .filter((ns) => ns.metadata.labels?.[LABELS.ENVIRONMENT] !== undefined)
        .map((ns) => ns.metadata.name)
        .sort();
      return { templates, classes, environments };
    },
    [],
    { live: false },
  );
  const action = useAction();

  const [environment, setEnvironment] = useState("");
  const [templateId, setTemplateId] = useState("");
  const [name, setName] = useState("");
  const [version, setVersion] = useState("");
  const [workers, setWorkers] = useState("");
  const [rows, setRows] = useState<Row[]>([]);
  const [expiry, setExpiry] = useState("template");
  const [hours, setHours] = useState("8");
  const [adopting, setAdopting] = useState(query.get("adopt") === "1");
  const [adoptId, setAdoptId] = useState("");

  const templates = useMemo(() => data?.templates || [], [data]);
  const classes = useMemo(() => data?.classes || [], [data]);
  const environments = data?.environments || [];
  const template = templates.find((t) => idOf(t) === templateId);

  const pickTemplate = (id: string, adopt = adopting) => {
    setTemplateId(id);
    const spec = templates.find((t) => idOf(t) === id)?.spec || {};
    setRows(rowsFor(spec, adopt));
    setVersion("");
    setWorkers("");
    // An adopted cluster takes no schedule.
    setExpiry(spec.schedule && !adopt ? "template" : "never");
  };

  const pickMode = (adopt: boolean) => {
    setAdopting(adopt);
    const t = templates.find((x) => idOf(x) === templateId);
    if (adopt && t && !t.spec?.adopt) setTemplateId("");
    else if (t) pickTemplate(templateId, adopt);
  };

  // Once loaded: the environment, and the template the link asked for.
  useEffect(() => {
    if (!data) return;
    setEnvironment(
      data.environments.includes(picked) ? picked : data.environments[0] || "",
    );
    const q = query.get("template");
    if (q && data.templates.some((t) => idOf(t) === q)) pickTemplate(q);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [data]);

  const templateOptions = templates
    .filter(
      (t) => !provider || providerOfClass(classOf(t, classes)) === provider,
    )
    .filter((t) => !adopting || t.spec?.adopt)
    .map((t) => ({ label: idOf(t), value: idOf(t) }));

  const ref = template?.spec?.clusterClassRef || {};
  const classObj =
    template &&
    classes.find(
      (c) =>
        c.metadata.namespace ===
          (ref.namespace || template.metadata.namespace) &&
        c.metadata.name === ref.name,
    );

  const expiryOptions = (() => {
    const opts: { value: string; label: string }[] = [];
    const s = template?.spec?.schedule;
    if (s && !adopting) {
      let when = "?";
      try {
        when = nextRun(s, new Date()).toLocaleString();
      } catch {
        // An unparseable schedule: the controller says why.
      }
      opts.push({
        value: "template",
        label: `Template schedule (${s}) — expires ${when}`,
      });
    }
    opts.push({ value: "hours", label: "After a number of hours" });
    opts.push({ value: "never", label: "Never" });
    return opts;
  })();

  // The Cluster as sent: hyve-controller completes it.
  const request: Obj | null = (() => {
    const t = template;
    if (!t || !name || !environment) return null;
    // The class is set here, as the CLI does: Cluster API's defaulting
    // webhook runs before hyve's (webhooks run in name order) and refuses
    // a Cluster without one.
    const classNS = t.spec.clusterClassRef?.namespace || t.metadata.namespace;
    const topology: any = {
      classRef: {
        name: t.spec.clusterClassRef?.name,
        ...(classNS !== environment ? { namespace: classNS } : {}),
      },
    };
    const vars = rows
      .filter((r) => !r.locked && r.value !== r.initial)
      .map((r) => ({ name: r.name, value: fromText(r.value) }));
    const idVar: string | undefined = t.spec.adopt?.idVariable;
    if (adopting && idVar) {
      if (!adoptId.trim()) return null;
      vars.push({ name: idVar, value: adoptId.trim() });
    }
    if (vars.length) topology.variables = vars;
    if (version) topology.version = version;
    if (workers !== "" && classObj && !adopting) {
      const spec = classObj.spec || {};
      const type =
        t.spec.workers?.type ||
        ((spec.workers?.machinePools || []).length &&
        !(spec.workers?.machineDeployments || []).length
          ? "machinePool"
          : "machineDeployment");
      const cls =
        t.spec.workers?.class || spec.workers?.[`${type}s`]?.[0]?.class;
      if (cls)
        topology.workers = {
          [`${type}s`]: [
            {
              class: cls,
              name: type === "machinePool" ? "mp-0" : "md-0",
              replicas: Number(workers),
            },
          ],
        };
    }
    const annotations: Record<string, string> = {};
    if (expiry === "hours")
      annotations[ANNOTATIONS.EXPIRES_AT] = isoSeconds(
        new Date(Date.now() + (Number(hours) || 0) * 3600 * 1000),
      );
    else if (expiry === "never") annotations[ANNOTATIONS.EXPIRES_AT] = NEVER;
    if (adopting) annotations[ANNOTATIONS.ADOPTED] = "true";
    const s = schemaFor(TYPES.CLUSTER);
    return {
      apiVersion: s ? `${s.group}/${s.version}` : "cluster.x-k8s.io/v1beta2",
      kind: "Cluster",
      metadata: {
        name,
        namespace: environment,
        labels: {
          [LABELS.TEMPLATE]: t.metadata.name,
          [LABELS.TEMPLATE_NAMESPACE]: t.metadata.namespace!,
        },
        annotations,
      },
      spec: { topology },
    };
  })();

  // local is the host cluster's name (lib/routes.ts), as in the CLI.
  const canCreate =
    !!request && NAME_RE.test(name) && name !== "local" && !action.busy;

  const create = () =>
    action.run(async () => {
      // The webhook's refusals (a locked variable, a missing class) come
      // back here.
      await save(TYPES.CLUSTER, newObject(TYPES.CLUSTER, request!));
      navigate(paths.clusters);
    });

  return (
    <div>
      <BackLink to={paths.clusters} label="Clusters" />
      <Card>
        <h1>
          {adopting
            ? "Adopt an existing cluster"
            : "Create a cluster from a template"}
        </h1>
        {loading ? (
          <Loading />
        ) : (
          <>
            <ErrorBanner error={error} />
            {data && !environments.length && (
              <Banner tone="warning">
                No environments yet: create one with 'hyve env create NAME' (a
                namespace labelled hyve.io/environment).
              </Banner>
            )}
            {provider && !templateOptions.length && (
              <Banner tone="warning">
                No templates make {provider} clusters yet: a template whose
                ClusterClass uses {provider}'s infrastructure shows here.
              </Banner>
            )}

            <div className="mb-5">
              <RadioGroup
                value={adopting ? "adopt" : "new"}
                onChange={(v) => pickMode(v === "adopt")}
                options={[
                  { value: "new", label: "Create a new cluster" },
                  {
                    value: "adopt",
                    label:
                      "Adopt an existing cluster through the template's provider (templates that set adopt)",
                  },
                ]}
              />
            </div>
            {adopting && data && !templates.some((t) => t.spec?.adopt) && (
              <Banner tone="warning">
                No template sets adopt yet: a template's adopt names the
                ClusterClass variable its provider adopts a cluster by (e.g.
                clusterID for Civo).
              </Banner>
            )}
            <div className="mb-5 grid gap-4 md:grid-cols-3">
              <SelectField
                label="Environment"
                required
                value={environment}
                onChange={setEnvironment}
                options={environments}
                placeholder=""
              />
              <SelectField
                label="Template"
                required
                value={templateId}
                onChange={pickTemplate}
                options={templateOptions}
                placeholder=""
              />
              <TextField
                label="Cluster name"
                required
                value={name}
                onChange={setName}
              />
            </div>

            {template && (
              <>
                {template.spec.description && (
                  <p className="mb-5 text-sm">{template.spec.description}</p>
                )}
                {adopting && (
                  <div className="mb-5 md:w-1/2">
                    <TextField
                      label={`Existing cluster's ID (${template.spec.adopt?.idVariable})`}
                      required
                      value={adoptId}
                      onChange={setAdoptId}
                      hint="Its ID at the provider; the provider takes it over instead of creating a cluster. Its name needn't match."
                    />
                  </div>
                )}
                <div className="mb-5 grid gap-4 md:grid-cols-3">
                  <TextField
                    label="Kubernetes version"
                    value={version}
                    onChange={setVersion}
                    placeholder={
                      template.spec.kubernetesVersion || "ClusterClass default"
                    }
                    hint={
                      adopting
                        ? "The cluster's own version: the template's is used otherwise."
                        : undefined
                    }
                  />
                  {!adopting && (
                    <TextField
                      label="Worker replicas"
                      type="number"
                      value={workers}
                      onChange={setWorkers}
                      placeholder={String(template.spec.workers?.count ?? 1)}
                    />
                  )}
                </div>

                {rows.length > 0 && <h3>Variables</h3>}
                {rows.map((row, i) => (
                  <div key={row.name} className="mb-2.5 md:w-1/2">
                    <TextField
                      label={row.locked ? `${row.name} (locked)` : row.name}
                      disabled={row.locked}
                      value={row.value}
                      onChange={(v) =>
                        setRows(
                          rows.map((r, j) =>
                            j === i ? { ...r, value: v } : r,
                          ),
                        )
                      }
                    />
                  </div>
                ))}

                <h3>Expiry</h3>
                <div className="mb-5 flex flex-wrap items-start gap-6">
                  <RadioGroup
                    value={expiry}
                    onChange={setExpiry}
                    options={expiryOptions}
                  />
                  {expiry === "hours" && (
                    <TextField
                      className="w-32"
                      label="Hours"
                      type="number"
                      value={hours}
                      onChange={setHours}
                    />
                  )}
                </div>

                {!classObj && (
                  <Banner tone="warning">
                    ClusterClass {ref.name} not found; the cluster can't be
                    created until it exists.
                  </Banner>
                )}
                <ErrorBanner error={action.error} />

                {request && (
                  <>
                    <h3>Request</h3>
                    <p className="text-muted mb-2 text-sm">
                      hyve-controller fills in the rest from the template and
                      its ClusterClass when it's created.
                    </p>
                    <pre className="preview max-h-[360px] overflow-auto">
                      {dump(request)}
                    </pre>
                  </>
                )}

                <button
                  type="button"
                  className="btn btn-primary mt-5"
                  disabled={!canCreate}
                  onClick={create}
                >
                  {adopting ? "Adopt" : "Create"}
                </button>
              </>
            )}
          </>
        )}
      </Card>
    </div>
  );
}
