// Imports a cluster hyve didn't create, as 'hyve cluster import' does: the
// host reaches it with the kubeconfig given, gives itself a ServiceAccount
// there, and makes the Cluster API objects that point at it
// (internal/hostapi/importcluster.go). hyve never changes the cluster's
// lifecycle; deleting it detaches it.
import { useEffect, useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import { SelectField, TextArea, TextField } from "../components/form";
import { BackLink, Banner, Card, ErrorBanner, Loading } from "../components/ui";
import { useEnvironment } from "../lib/environment";
import { useAction, useLoad } from "../lib/hooks";
import { host, idOf } from "../lib/kube";
import { paths } from "../lib/routes";
import { LABELS, TYPES } from "../lib/types";

const NAME_RE = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/;

export default function ImportCluster() {
  const navigate = useNavigate();
  const [picked] = useEnvironment();
  const { data, error, loading } = useLoad(
    async (r) => {
      const [templates, namespaces] = await Promise.all([
        r.findAll(TYPES.TEMPLATE),
        r.findAll(TYPES.NAMESPACE),
      ]);
      const environments = namespaces
        .filter((ns) => ns.metadata.labels?.[LABELS.ENVIRONMENT] !== undefined)
        .map((ns) => ns.metadata.name)
        .sort();
      return { templates, environments };
    },
    [],
    { live: false },
  );
  const action = useAction();
  const [environment, setEnvironment] = useState("");
  const [name, setName] = useState("");
  const [kubeconfig, setKubeconfig] = useState("");
  const [context, setContext] = useState("");
  const [apiServer, setApiServer] = useState("");
  const [templateId, setTemplateId] = useState("");

  useEffect(() => {
    if (data)
      setEnvironment(
        data.environments.includes(picked)
          ? picked
          : data.environments[0] || "",
      );
  }, [data, picked]);

  const templates = useMemo(() => data?.templates || [], [data]);
  const template = templates.find((t) => idOf(t) === templateId);
  const canImport =
    !!environment &&
    NAME_RE.test(name) &&
    name !== "local" &&
    !!kubeconfig.trim() &&
    !action.busy;

  const loadFile = (f: File | undefined) => {
    if (f) f.text().then(setKubeconfig, (e) => action.setError(String(e)));
  };

  const submit = () =>
    action.run(async () => {
      await host("clusters/import", {
        name,
        environment,
        kubeconfig,
        context: context.trim() || undefined,
        apiServer: apiServer.trim() || undefined,
        template: template?.metadata.name,
        templateNamespace: template?.metadata.namespace,
      });
      navigate(paths.cluster(environment, name));
    });

  return (
    <div>
      <BackLink to={paths.clusters} label="Clusters" />
      <Card>
        <h1>Import a cluster</h1>
        {loading ? (
          <Loading />
        ) : (
          <>
            <ErrorBanner error={error} />
            <p className="mb-5 text-sm">
              Brings in a cluster hyve didn't create (EKS, GKE, AKS, on-prem,
              kind...). hyve gives itself a ServiceAccount on it
              (kube-system/hyve-import, cluster-admin), and Fleet, access,
              resources and the Explorer then work as for any cluster. hyve
              never creates, scales, upgrades or deletes it: deleting it here
              detaches it.
            </p>
            <div className="mb-5 grid gap-4 md:grid-cols-3">
              <SelectField
                label="Environment"
                required
                value={environment}
                onChange={setEnvironment}
                options={data?.environments || []}
                placeholder=""
              />
              <TextField
                label="Cluster name"
                required
                value={name}
                onChange={setName}
                hint="Its name in hyve; it needn't match the cluster's own."
              />
              <SelectField
                label="Template"
                value={templateId}
                onChange={setTemplateId}
                options={[
                  { label: "None", value: "" },
                  ...templates.map((t) => ({ label: idOf(t), value: idOf(t) })),
                ]}
                hint="Gives it the template's resources (not its variables, version or expiry)."
              />
            </div>
            <TextArea
              className="mb-2"
              label="Kubeconfig"
              required
              mono
              rows={10}
              value={kubeconfig}
              onChange={setKubeconfig}
              hint="One that holds a token or certificate: the host uses it once, to make hyve's ServiceAccount. One that runs a command (aws, gcloud) needs the CLI: hyve cluster import."
            />
            <input
              className="mb-5 text-sm"
              type="file"
              aria-label="Load a kubeconfig file"
              onChange={(e) => loadFile(e.target.files?.[0])}
            />
            <div className="mb-5 grid gap-4 md:grid-cols-2">
              <TextField
                label="Context"
                value={context}
                onChange={setContext}
                placeholder="the kubeconfig's current context"
              />
              <TextField
                label="API server as the host reaches it"
                value={apiServer}
                onChange={setApiServer}
                placeholder="the kubeconfig's"
                hint="When hyve's host reaches the API server at another address than the kubeconfig's."
              />
            </div>
            {data && !data.environments.length && (
              <Banner tone="warning">
                No environments yet: create one first.
              </Banner>
            )}
            <ErrorBanner error={action.error} />
            <button
              type="button"
              className="btn btn-primary"
              disabled={!canImport}
              onClick={submit}
            >
              {action.busy ? "Importing…" : "Import"}
            </button>
          </>
        )}
      </Card>
    </div>
  );
}
