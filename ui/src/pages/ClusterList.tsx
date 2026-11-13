// The console's cluster list: one row per cluster, its environment,
// expiry and state as pills; a row opens the cluster. The host cluster,
// local, comes first for whoever can reach it, with all environments or
// the default one: it's in none (lib/environment.ts).
import type { ReactNode } from "react";
import { Link, useLocation, useNavigate, type NavigateFunction } from "react-router-dom";
import {
  Banner,
  ErrorBanner,
  Loading,
  PageHeader,
  Pill,
  StatusPill,
} from "../components/ui";
import { request } from "../lib/api";
import { originOf } from "../lib/clusters";
import { clusterBase, LOCAL_CLUSTER } from "../lib/explorer";
import { showsLocal, useEnvironment, useEnvironmentScope } from "../lib/environment";
import { clusterStatus, expiry } from "../lib/format";
import { useLoad } from "../lib/hooks";
import { idOf, type Obj } from "../lib/kube";
import { paths } from "../lib/routes";
import { useSession } from "../lib/session";
import { ANNOTATIONS, LABELS, TYPES } from "../lib/types";

export default function ClusterList() {
  // What a release on the cluster's page said.
  const message = (useLocation().state as { message?: string } | null)
    ?.message;
  const [environment] = useEnvironment();
  useEnvironmentScope();
  const navigate = useNavigate();
  const { canExplore } = useSession();
  const showLocal = canExplore && showsLocal(environment);
  const { data, error, loading } = useLoad(
    async (r) => {
      const [clusters, version, imported] = await Promise.all([
        r.findAll(TYPES.CLUSTER, { force: true }),
        canExplore
          ? request("GET", `${clusterBase(LOCAL_CLUSTER)}/version`).catch(
              () => null,
            )
          : null,
        // A host from before imports has no such kind.
        r.findAll(TYPES.IMPORTED_CONTROL_PLANE).catch(() => [] as Obj[]),
      ]);
      const importedVersions = Object.fromEntries(
        imported.map((cp) => [
          idOf(cp),
          cp.status?.version as string | undefined,
        ]),
      );
      return {
        clusters: clusters.sort((a, b) => idOf(a).localeCompare(idOf(b))),
        localVersion: version?.gitVersion as string | undefined,
        importedVersions,
      };
    },
    [canExplore],
  );
  const clusters = data?.clusters;

  const rows = (clusters || [])
    .filter((c) => !environment || c.metadata.namespace === environment)
    .map((c) => {
      const l = c.metadata.labels || {};
      const topo = c.spec?.topology || {};
      const workers = [
        ...(topo.workers?.machineDeployments || []),
        ...(topo.workers?.machinePools || []),
      ].reduce((n: number, w: any) => n + (w.replicas || 0), 0);
      if (originOf(c) === "imported") {
        return {
          c,
          sub: [
            "imported",
            l[LABELS.TEMPLATE] ? `template ${l[LABELS.TEMPLATE]}` : "",
            data?.importedVersions[idOf(c)],
          ]
            .filter(Boolean)
            .join(" · "),
          status: clusterStatus(c),
          expires: expiry(c.metadata.annotations?.[ANNOTATIONS.EXPIRES_AT]),
        };
      }
      return {
        c,
        sub: [
          originOf(c) === "adopted" ? "adopted" : "",
          l[LABELS.TEMPLATE] ? `template ${l[LABELS.TEMPLATE]}` : "",
          topo.classRef?.name || topo.class,
          topo.version,
          `${workers} worker${workers === 1 ? "" : "s"}`,
        ]
          .filter(Boolean)
          .join(" · "),
        status: clusterStatus(c),
        expires: expiry(c.metadata.annotations?.[ANNOTATIONS.EXPIRES_AT]),
      };
    });

  return (
    <div>
      <PageHeader title="Clusters">
        <Link
          className="btn btn-secondary"
          to={paths.importCluster}
          title="Bring in a cluster hyve didn't create, without managing its lifecycle"
        >
          Import cluster
        </Link>
        <Link className="btn btn-primary" to={paths.createCluster()}>
          New cluster
        </Link>
      </PageHeader>
      {message && <Banner tone="success">{message}</Banner>}
      <ErrorBanner error={error} />
      {loading ? (
        <Loading />
      ) : (
        <div className="card-list">
          {!rows.length && !showLocal && (
            <p className="p-6 text-center text-sm text-neutral-500">
              No clusters yet.
            </p>
          )}
            {showLocal && (
              <Row to={paths.localCluster()} navigate={navigate}>
                <div className="min-w-0">
                  <div className="font-medium text-neutral-900 dark:text-neutral-100">
                    local
                  </div>
                  <div className="truncate text-xs text-neutral-500">
                    {["host cluster: hyve runs here", data?.localVersion]
                      .filter(Boolean)
                      .join(" · ")}
                  </div>
                </div>
                <div className="flex shrink-0 flex-wrap items-center gap-2 md:gap-3">
                  <Pill
                    tone="info"
                    label="Host"
                    title="hyve's management cluster"
                  />
                </div>
              </Row>
            )}
            {rows.map((r) => {
              const to = paths.cluster(
                r.c.metadata.namespace!,
                r.c.metadata.name,
              );
              return (
                <Row key={idOf(r.c)} to={to} navigate={navigate}>
                  <div className="min-w-0">
                    <div className="font-medium text-neutral-900 dark:text-neutral-100">
                      {r.c.metadata.name}
                    </div>
                    <div className="truncate text-xs text-neutral-500">
                      {r.sub}
                    </div>
                  </div>
                  <div className="flex shrink-0 flex-wrap items-center gap-2 md:gap-3">
                    <Pill tone="env" label={r.c.metadata.namespace!} />
                    <StatusPill status={r.expires} />
                    <StatusPill status={r.status} />
                  </div>
                </Row>
              );
            })}
        </div>
      )}
    </div>
  );
}

function Row({
  to,
  navigate,
  children,
}: {
  to: string;
  navigate: NavigateFunction;
  children: ReactNode;
}) {
  return (
    <div
      role="link"
      tabIndex={0}
      className="flex cursor-pointer flex-col gap-2 transition-colors hover:bg-neutral-50 md:flex-row md:items-center md:justify-between dark:hover:bg-neutral-800/50"
      onClick={() => navigate(to)}
      onKeyDown={(e) => {
        if (e.key === "Enter") navigate(to);
      }}
    >
      {children}
    </div>
  );
}
