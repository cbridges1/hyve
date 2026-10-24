package operator

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/yaml"
)

// Push delivery (HYVE-PROPOSAL.md, "Push delivery"): for a child whose
// fleet-agent can't reach the management cluster, hyve-controller does the
// agent's job from here, with the child's CAPI kubeconfig. Fleet still
// works out what goes where and makes a BundleDeployment per bundle per
// cluster; hyve deploys each with Fleet's own `fleet deploy` in a Job and
// writes its status as fleet-agent would, so Fleet's roll-ups (and hyve's
// UI) see push clusters like any other.

const (
	// LabelDelivery on a CAPI Cluster forces "push" or "agent"; without
	// it, a cluster is push while its agent hasn't checked in.
	LabelDelivery = "hyve.io/delivery"
	// labelPushCluster marks the Jobs and Secrets made for a Fleet
	// cluster's pushes (its name; they live in its namespace).
	labelPushCluster = "hyve.io/push-cluster"
	labelPushKind    = "hyve.io/push"
	annotPushBD      = "hyve.io/push-bundle-deployment"
	annotPushDeploy  = "hyve.io/push-deployment-id"
	annotPushChart   = "hyve.io/push-chart"

	// fleetAgentNamespace is fleet-agent's namespace on children: releases
	// are marked with it, so fleet-agent takes over hyve's pushes.
	fleetAgentNamespace = "cattle-fleet-system"
	// agentStale is how long an agent may go without checking in before
	// hyve pushes in its place.
	agentStale = 15 * time.Minute
	// pushResync is how often a push cluster is checked when nothing
	// changes: releases gone from the child are put back.
	pushResync = 10 * time.Minute
	// maxPushJobs is how many deploy Jobs run at once for one cluster.
	maxPushJobs = 2
	// pushJobTTL keeps a finished Job (and so a failure) this long; a
	// failed deploy is retried after it's gone.
	pushJobTTL = 5 * time.Minute
)

var (
	fleetClusterGVK = schema.GroupVersionKind{Group: "fleet.cattle.io", Version: "v1alpha1", Kind: "Cluster"}
	contentGVK      = schema.GroupVersionKind{Group: "fleet.cattle.io", Version: "v1alpha1", Kind: "Content"}
)

// Push deploys BundleDeployments to clusters whose agent can't reach the
// management cluster.
type Push struct {
	// Client is cached: Fleet clusters, BundleDeployments, Jobs, CAPI
	// Clusters.
	Client client.Client
	// Reader is uncached: Secrets, Pods, Contents.
	Reader client.Reader
	// Logs reads a pod's logs (a chart-packaging Job's output).
	Logs func(ctx context.Context, namespace, pod string) ([]byte, error)
	// NewChild builds a client for a child from its kubeconfig; replaced
	// in tests.
	NewChild func(kubeconfig []byte) (Child, error)
	// FleetImage runs `fleet apply` and `fleet deploy`; empty means the
	// image fleet-controller runs.
	FleetImage string
	Now        func() time.Time

	// driftAt is when each BundleDeployment with correctDrift was last
	// deployed again, to put back changes made on the child.
	driftMu sync.Mutex
	driftAt map[types.UID]time.Time
}

// driftDue reports whether bd corrects drift and is due to be deployed
// again, and if so, records that it is now.
func (r *Push) driftDue(bd *unstructured.Unstructured) bool {
	if on, _, _ := unstructured.NestedBool(bd.Object, "spec", "correctDrift", "enabled"); !on {
		return false
	}
	r.driftMu.Lock()
	defer r.driftMu.Unlock()
	if r.driftAt == nil {
		r.driftAt = map[types.UID]time.Time{}
	}
	now := r.Now()
	last, seen := r.driftAt[bd.GetUID()]
	if !seen {
		// Just deployed (or the controller restarted): wait a whole interval.
		r.driftAt[bd.GetUID()] = now
		return false
	}
	// A little early is fine: the cluster is looked at every pushResync.
	if now.Sub(last) < pushResync*9/10 {
		return false
	}
	r.driftAt[bd.GetUID()] = now
	return true
}

func setupPush(mgr manager.Manager, cs kubernetes.Interface, fleetImage string) error {
	p := &Push{
		Client: mgr.GetClient(), Reader: mgr.GetAPIReader(), NewChild: defaultPushChild, FleetImage: fleetImage, Now: time.Now,
		Logs: func(ctx context.Context, ns, pod string) ([]byte, error) {
			return cs.CoreV1().Pods(ns).GetLogs(pod, &corev1.PodLogOptions{}).DoRaw(ctx)
		},
	}
	byLabels := func(nsLabel, nameLabel string) handler.MapFunc {
		return func(_ context.Context, o client.Object) []reconcile.Request {
			l := o.GetLabels()
			ns := o.GetNamespace()
			if nsLabel != "" {
				ns = l[nsLabel]
			}
			if l[nameLabel] == "" || ns == "" {
				return nil
			}
			return []reconcile.Request{{NamespacedName: types.NamespacedName{Namespace: ns, Name: l[nameLabel]}}}
		}
	}
	sameName := handler.EnqueueRequestsFromMapFunc(func(_ context.Context, o client.Object) []reconcile.Request {
		return []reconcile.Request{{NamespacedName: client.ObjectKeyFromObject(o)}}
	})
	return ctrl.NewControllerManagedBy(mgr).Named("hyve-push").
		For(object(fleetClusterGVK), builder.WithPredicates(predicate.NewPredicateFuncs(func(o client.Object) bool {
			u, ok := o.(*unstructured.Unstructured)
			if !ok {
				return false
			}
			s, _, _ := unstructured.NestedString(u.Object, "spec", "kubeConfigSecret")
			return s != ""
		}))).
		Watches(object(bundleDeploymentGVK), handler.EnqueueRequestsFromMapFunc(byLabels("fleet.cattle.io/cluster-namespace", "fleet.cattle.io/cluster"))).
		Watches(&batchv1.Job{}, handler.EnqueueRequestsFromMapFunc(byLabels("", labelPushCluster))).
		Watches(object(clusterGVK), sameName).
		Complete(p)
}

// Reconcile implements reconcile.Reconciler, for a Fleet cluster.
func (r *Push) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	lg := log.FromContext(ctx)
	fc := object(fleetClusterGVK)
	if err := r.Client.Get(ctx, req.NamespacedName, fc); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}
	if fc.GetDeletionTimestamp() != nil {
		return reconcile.Result{}, nil
	}
	push, why := r.isPush(ctx, fc)
	if !push {
		// The agent may stop checking in: look again later.
		return reconcile.Result{RequeueAfter: agentStale}, nil
	}
	bdNS, _, _ := unstructured.NestedString(fc.Object, "status", "namespace")
	kcName, _, _ := unstructured.NestedString(fc.Object, "spec", "kubeConfigSecret")
	if bdNS == "" {
		return reconcile.Result{RequeueAfter: 30 * time.Second}, nil
	}
	var kc corev1.Secret
	if err := r.Reader.Get(ctx, types.NamespacedName{Namespace: fc.GetNamespace(), Name: kcName}, &kc); err != nil {
		if apierrors.IsNotFound(err) {
			return reconcile.Result{RequeueAfter: time.Minute}, nil
		}
		return reconcile.Result{}, err
	}
	child, err := r.NewChild(kc.Data["value"])
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("child client: %w", err)
	}

	bds := &unstructured.UnstructuredList{}
	bds.SetGroupVersionKind(bundleDeploymentGVK.GroupVersion().WithKind("BundleDeploymentList"))
	if err := r.Client.List(ctx, bds, client.InNamespace(bdNS)); err != nil {
		return reconcile.Result{}, err
	}
	var jobs batchv1.JobList
	if err := r.Client.List(ctx, &jobs, client.InNamespace(fc.GetNamespace()), client.MatchingLabels{labelPushCluster: fc.GetName()}); err != nil {
		return reconcile.Result{}, err
	}
	releases, err := child.ListReleases(ctx)
	if err != nil {
		// The child's API may be down for a while (or for good): try later.
		lg.Info("can't list releases on child", "cluster", req.NamespacedName, "error", err.Error())
		return reconcile.Result{RequeueAfter: time.Minute}, nil
	}

	// Releases fleet-agent would garbage-collect: marked as Fleet's, for
	// a BundleDeployment that's gone.
	want := map[string]bool{}
	for _, bd := range bds.Items {
		if bd.GetDeletionTimestamp() == nil {
			want[bd.GetName()] = true
		}
	}
	for _, rel := range releases {
		if rel.AgentNamespace == fleetAgentNamespace && !want[rel.BundleID] {
			lg.Info("uninstalling a release whose BundleDeployment is gone", "cluster", req.NamespacedName, "release", rel.Namespace+"/"+rel.Name)
			if err := child.Uninstall(ctx, rel); err != nil {
				return reconcile.Result{}, fmt.Errorf("uninstall %s/%s: %w", rel.Namespace, rel.Name, err)
			}
		}
	}

	jobsByName := map[string]*batchv1.Job{}
	// Drift corrections, by BundleDeployment: their Jobs' names aren't
	// the deployment's.
	driftJobs := map[string]*batchv1.Job{}
	active := 0
	for i := range jobs.Items {
		j := &jobs.Items[i]
		jobsByName[j.Name] = j
		if j.Labels[labelPushKind] == "drift" {
			if cur := driftJobs[j.Annotations[annotPushBD]]; cur == nil || cur.CreationTimestamp.Before(&j.CreationTimestamp) {
				driftJobs[j.Annotations[annotPushBD]] = j
			}
		}
		if (j.Labels[labelPushKind] == "deploy" || j.Labels[labelPushKind] == "drift") && !jobDone(j) {
			active++
		}
	}
	installed := map[string]Release{}
	for _, rel := range releases {
		installed[rel.BundleID] = rel
	}

	busy := false
	sort.Slice(bds.Items, func(i, j int) bool { return bds.Items[i].GetName() < bds.Items[j].GetName() })
	for i := range bds.Items {
		bd := &bds.Items[i]
		if bd.GetDeletionTimestamp() != nil {
			continue
		}
		id, _, _ := unstructured.NestedString(bd.Object, "spec", "deploymentID")
		applied, _, _ := unstructured.NestedString(bd.Object, "status", "appliedDeploymentID")
		valuesHash, _, _ := unstructured.NestedString(bd.Object, "spec", "valuesHash")
		// The UID too: a BundleDeployment made again (a resource removed
		// and added back) mustn't match the last one's finished Job.
		name := pushJobName("hyve-push", fc.GetNamespace(), fc.GetName(), bd.GetName(), string(bd.GetUID()), id, valuesHash)
		if j, ok := jobsByName[name]; ok {
			if !jobDone(j) {
				busy = true
				continue
			}
			if err := r.report(ctx, child, bd, j); err != nil {
				return reconcile.Result{}, err
			}
			continue
		}
		_, has := installed[bd.GetName()]
		kind := "deploy"
		if id == applied && has {
			if j := driftJobs[bd.GetName()]; j != nil {
				if !jobDone(j) {
					busy = true
					continue
				}
				if err := r.report(ctx, child, bd, j); err != nil {
					return reconcile.Result{}, err
				}
			}
			if !r.driftDue(bd) {
				continue
			}
			// correctDrift: deploy the same thing again, which puts back
			// what was changed on the child, as fleet-agent's drift
			// correction does.
			kind = "drift"
			name = pushJobName("hyve-drift", name, r.Now().UTC().Format(time.RFC3339))
		}
		if active >= maxPushJobs {
			busy = true
			continue
		}
		ok, err := r.deploy(ctx, fc, child, bd, name, kind)
		if err != nil {
			lg.Error(err, "push", "cluster", req.NamespacedName, "bundleDeployment", bd.GetName())
			if err := r.writeStatus(ctx, bd, false, "", err.Error()); err != nil {
				return reconcile.Result{}, err
			}
			continue
		}
		busy = true
		if ok {
			active++
		}
	}
	lg.V(1).Info("push", "cluster", req.NamespacedName, "why", why, "busy", busy)
	if busy {
		return reconcile.Result{RequeueAfter: 15 * time.Second}, nil
	}
	return reconcile.Result{RequeueAfter: pushResync}, nil
}

// isPush reports whether hyve delivers to fc, and why.
func (r *Push) isPush(ctx context.Context, fc *unstructured.Unstructured) (bool, string) {
	capi := object(clusterGVK)
	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(fc), capi); err == nil {
		switch capi.GetLabels()[LabelDelivery] {
		case "push":
			return true, "label"
		case "agent":
			return false, "label"
		}
	} else {
		// Not a CAPI cluster's (fleet-local, a cluster imported some other
		// way): leave it to its agent.
		return false, "no CAPI cluster"
	}
	seen, _, _ := unstructured.NestedString(fc.Object, "status", "agent", "lastSeen")
	if seen == "" {
		return true, "agent never checked in"
	}
	t, err := time.Parse(time.RFC3339, seen)
	if err != nil || r.Now().Sub(t) > agentStale {
		return true, "agent last checked in " + seen
	}
	return false, "agent connected"
}

// deploy starts a Job that deploys bd; false (and no error) means it's
// waiting for its chart to be packaged first.
func (r *Push) deploy(ctx context.Context, fc *unstructured.Unstructured, child Child, bd *unstructured.Unstructured, name, kind string) (bool, error) {
	ns := fc.GetNamespace()
	image, err := r.fleetImage(ctx)
	if err != nil {
		return false, err
	}
	opts, _, _ := unstructured.NestedMap(bd.Object, "spec", "options")
	if opts == nil {
		opts = map[string]any{}
	}
	id, _, _ := unstructured.NestedString(bd.Object, "spec", "deploymentID")

	// The content: Fleet's Content object, or for a HelmOp (whose chart
	// fleet-agent downloads itself) the chart, packaged once per version.
	var content *unstructured.Unstructured
	if hco, isHelmOp, _ := unstructured.NestedMap(bd.Object, "spec", "helmChartOptions"); isHelmOp {
		// The HelmOp's repository credentials: Fleet copies its
		// helmSecretName into the BundleDeployment's namespace, where
		// fleet-agent reads it.
		var auth *corev1.Secret
		if sn, _ := hco["helmOpSecretName"].(string); sn != "" {
			auth = &corev1.Secret{}
			if err := r.Reader.Get(ctx, types.NamespacedName{Namespace: bd.GetNamespace(), Name: sn}, auth); err != nil {
				return false, fmt.Errorf("the HelmOp's credentials %s: %w", sn, err)
			}
		}
		insecure, _ := hco["helmOpInsecureSkipTLSVerify"].(bool)
		c, err := r.chartContent(ctx, ns, fc.GetName(), image, opts, auth, insecure)
		if err != nil || c == nil {
			return false, err
		}
		content = c
	} else {
		cname, _, _ := strings.Cut(id, ":")
		content = object(contentGVK)
		if err := r.Reader.Get(ctx, types.NamespacedName{Name: cname}, content); err != nil {
			return false, fmt.Errorf("content %s: %w", cname, err)
		}
	}

	// Per-cluster Helm values live in a Secret next to the
	// BundleDeployment; fleet deploy takes them in the options.
	var vals corev1.Secret
	if err := r.Reader.Get(ctx, types.NamespacedName{Namespace: bd.GetNamespace(), Name: bd.GetName()}, &vals); err == nil && len(vals.Data["values"]) > 0 {
		var v map[string]any
		if err := json.Unmarshal(vals.Data["values"], &v); err != nil {
			return false, fmt.Errorf("values: %w", err)
		}
		if err := unstructured.SetNestedField(opts, v, "helm", "values"); err != nil {
			return false, err
		}
	} else if err != nil && !apierrors.IsNotFound(err) {
		return false, err
	}

	if err := r.copyDownstream(ctx, child, bd, opts); err != nil {
		return false, err
	}

	input, err := deployInput(content, bd.GetName(), id, opts)
	if err != nil {
		return false, err
	}
	target, _ := opts["defaultNamespace"].(string)
	if target == "" {
		target = "default"
	}
	job := pushJob(name, ns, fc.GetName(), image, kind, []string{
		"fleet", "deploy", "-i", "/in/input.yaml", "-a", fleetAgentNamespace, "-n", target, "--kubeconfig", "/kubeconfig/value",
	})
	job.Annotations = map[string]string{annotPushBD: bd.GetName(), annotPushDeploy: id}
	kcName, _, _ := unstructured.NestedString(fc.Object, "spec", "kubeConfigSecret")
	job.Spec.Template.Spec.Volumes = []corev1.Volume{
		{Name: "in", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: name}}},
		{Name: "kubeconfig", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: kcName}}},
	}
	job.Spec.Template.Spec.Containers[0].VolumeMounts = []corev1.VolumeMount{
		{Name: "in", MountPath: "/in", ReadOnly: true},
		{Name: "kubeconfig", MountPath: "/kubeconfig", ReadOnly: true},
	}
	if err := r.Client.Create(ctx, job); err != nil && !apierrors.IsAlreadyExists(err) {
		return false, err
	}
	// The input goes with its Job (the pod waits for the Secret).
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: ns, Labels: map[string]string{labelPushCluster: fc.GetName(), labelPushKind: "input"},
			OwnerReferences: []metav1.OwnerReference{{APIVersion: "batch/v1", Kind: "Job", Name: job.Name, UID: job.UID}},
		},
		Data: map[string][]byte{"input.yaml": input},
	}
	if err := r.Client.Create(ctx, sec); err != nil && !apierrors.IsAlreadyExists(err) {
		return false, err
	}
	return true, nil
}

// chartContent is a HelmOp's chart as a Fleet Content, from the cache
// Secret; nil while the Job packaging it runs.
func (r *Push) chartContent(ctx context.Context, ns, cluster, image string, opts map[string]any, auth *corev1.Secret, insecure bool) (*unstructured.Unstructured, error) {
	chart, _, _ := unstructured.NestedString(opts, "helm", "chart")
	repo, _, _ := unstructured.NestedString(opts, "helm", "repo")
	version, _, _ := unstructured.NestedString(opts, "helm", "version")
	if chart == "" {
		return nil, fmt.Errorf("a HelmOp without a chart")
	}
	key := pushJobName("hyve-chart", chart, repo, version)
	var cached corev1.Secret
	err := r.Reader.Get(ctx, types.NamespacedName{Namespace: ns, Name: key}, &cached)
	if err == nil {
		return contentFromSecret(&cached)
	}
	if !apierrors.IsNotFound(err) {
		return nil, err
	}

	var job batchv1.Job
	err = r.Client.Get(ctx, types.NamespacedName{Namespace: ns, Name: key}, &job)
	if apierrors.IsNotFound(err) {
		// fleet apply downloads the chart the way fleet-agent does for a
		// HelmOp, and prints the Bundle with it as resources.
		src, err := yaml.Marshal(map[string]any{"helm": map[string]any{"chart": chart, "repo": repo, "version": version}})
		if err != nil {
			return nil, err
		}
		// fleet apply builds a client for the cluster at start-up, though
		// -o - never uses it, and exits without a word if it finds no
		// config ($KUBECONFIG; its --kubeconfig flag isn't read there): it
		// gets a kubeconfig for nowhere rather than a service account token.
		flags := chartAuthFlags(auth, insecure)
		j := pushJob(key, ns, cluster, image, "chart", []string{"sh", "-c", `mkdir -p /tmp/src && printf '%s' "$FLEET_YAML" > /tmp/src/fleet.yaml && printf '%s' "$NOWHERE" > /tmp/kubeconfig && cd /tmp/src && exec fleet apply -o -` + flags + ` chart .`})
		j.Annotations = map[string]string{annotPushChart: chart + ":" + repo + ":" + version}
		j.Spec.Template.Spec.Containers[0].Env = []corev1.EnvVar{{Name: "FLEET_YAML", Value: string(src)}, {Name: "NOWHERE", Value: nowhereKubeconfig}, {Name: "KUBECONFIG", Value: "/tmp/kubeconfig"}}
		if auth != nil {
			// The credentials go in a Secret next to the Job (a pod can't
			// mount one from the BundleDeployment's namespace), owned by
			// it so they go when it does.
			j.Spec.Template.Spec.Volumes = []corev1.Volume{{Name: "auth", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: key + "-auth"}}}}
			j.Spec.Template.Spec.Containers[0].VolumeMounts = []corev1.VolumeMount{{Name: "auth", MountPath: "/auth", ReadOnly: true}}
		}
		if err := r.Client.Create(ctx, j); err != nil && !apierrors.IsAlreadyExists(err) {
			return nil, err
		}
		if auth != nil {
			sec := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: key + "-auth", Namespace: ns, Labels: map[string]string{labelPushCluster: cluster, labelPushKind: "chart-auth"},
					OwnerReferences: []metav1.OwnerReference{{APIVersion: "batch/v1", Kind: "Job", Name: j.Name, UID: j.UID}},
				},
				Data: map[string][]byte{},
			}
			for _, k := range []string{corev1.BasicAuthUsernameKey, corev1.BasicAuthPasswordKey, "cacerts"} {
				if v, ok := auth.Data[k]; ok {
					sec.Data[k] = v
				}
			}
			if err := r.Client.Create(ctx, sec); err != nil && !apierrors.IsAlreadyExists(err) {
				return nil, err
			}
		}
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	if !jobDone(&job) {
		return nil, nil
	}
	if !jobSucceeded(&job) {
		return nil, fmt.Errorf("packaging chart %s %s from %s: %s", chart, version, repo, r.jobMessage(ctx, &job))
	}
	pod, err := r.jobPod(ctx, &job)
	if err != nil {
		return nil, err
	}
	out, err := r.Logs(ctx, ns, pod.Name)
	if err != nil {
		return nil, fmt.Errorf("chart Job output: %w", err)
	}
	sec, err := chartSecret(out, key, ns, cluster)
	if err != nil {
		return nil, err
	}
	if err := r.Client.Create(ctx, sec); err != nil && !apierrors.IsAlreadyExists(err) {
		return nil, err
	}
	_ = r.Client.Delete(ctx, &job, client.PropagationPolicy(metav1.DeletePropagationBackground))
	return contentFromSecret(sec)
}

// chartAuthFlags are `fleet apply`'s flags for a HelmOp's repository
// credentials (mounted at /auth), as fleet-agent would use them.
func chartAuthFlags(auth *corev1.Secret, insecure bool) string {
	var f []string
	if auth != nil {
		_, user := auth.Data[corev1.BasicAuthUsernameKey]
		_, pass := auth.Data[corev1.BasicAuthPasswordKey]
		if user && pass {
			f = append(f, `--username "$(cat /auth/username)"`, "--password-file /auth/password")
		}
		if _, ok := auth.Data["cacerts"]; ok {
			f = append(f, "--cacerts-file /auth/cacerts")
		}
		if len(f) > 0 {
			// fleet apply only uses credentials for repos matching this.
			f = append(f, "--helm-repo-url-regex '.*'")
		}
		if v, _ := strconv.ParseBool(string(auth.Data["insecureSkipVerify"])); v {
			insecure = true
		}
		if v, _ := strconv.ParseBool(string(auth.Data["basicHTTP"])); v {
			f = append(f, "--helm-basic-http")
		}
	}
	if insecure {
		f = append(f, "--helm-insecure-skip-tls")
	}
	if len(f) == 0 {
		return ""
	}
	return " " + strings.Join(f, " ")
}

// chartSecret turns `fleet apply -o -` output (a Bundle carrying the chart
// as resources) into a cache Secret holding the gzipped manifest and its
// checksum, as a Fleet Content holds them.
func chartSecret(out []byte, name, ns, cluster string) (*corev1.Secret, error) {
	var b struct {
		Spec struct {
			Resources []map[string]any `json:"resources"`
		} `json:"spec"`
	}
	if err := yaml.Unmarshal(out, &b); err != nil {
		return nil, fmt.Errorf("chart Job output: %w", err)
	}
	if len(b.Spec.Resources) == 0 {
		return nil, fmt.Errorf("chart Job output has no chart files")
	}
	data, err := json.Marshal(map[string]any{"resources": b.Spec.Resources})
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	var gz bytes.Buffer
	w := gzip.NewWriter(&gz)
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Labels: map[string]string{labelPushCluster: cluster, labelPushKind: "chart"}},
		Data:       map[string][]byte{"content": gz.Bytes(), "sha256sum": []byte(hex.EncodeToString(sum[:]))},
	}, nil
}

// nowhereKubeconfig points at no cluster.
const nowhereKubeconfig = `apiVersion: v1
kind: Config
clusters: [{name: none, cluster: {server: "https://127.0.0.1:1"}}]
users: [{name: none, user: {}}]
contexts: [{name: none, context: {cluster: none, user: none}}]
current-context: none
`

func contentFromSecret(s *corev1.Secret) (*unstructured.Unstructured, error) {
	if len(s.Data["content"]) == 0 {
		return nil, fmt.Errorf("chart cache %s is empty", s.Name)
	}
	c := object(contentGVK)
	c.SetName(s.Name)
	c.Object["content"] = base64.StdEncoding.EncodeToString(s.Data["content"])
	c.Object["sha256sum"] = string(s.Data["sha256sum"])
	return c, nil
}

// deployInput is fleet deploy's -i file: the Content and a
// BundleDeployment with the options to deploy.
func deployInput(content *unstructured.Unstructured, bdName, id string, opts map[string]any) ([]byte, error) {
	c := map[string]any{
		"apiVersion": "fleet.cattle.io/v1alpha1", "kind": "Content",
		"metadata": map[string]any{"name": content.GetName()},
		"content":  content.Object["content"], "sha256sum": content.Object["sha256sum"],
	}
	bd := map[string]any{
		"apiVersion": "fleet.cattle.io/v1alpha1", "kind": "BundleDeployment",
		"metadata": map[string]any{"name": bdName},
		"spec":     map[string]any{"deploymentID": id, "options": opts},
	}
	a, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(bd)
	if err != nil {
		return nil, err
	}
	return append(append(a, []byte("\n---\n")...), append(b, '\n')...), nil
}

// copyDownstream copies the Secrets and ConfigMaps a bundle names in
// downstreamResources to the child, as fleet-agent does: Fleet has already
// copied them into the BundleDeployment's namespace.
func (r *Push) copyDownstream(ctx context.Context, child Child, bd *unstructured.Unstructured, opts map[string]any) error {
	list, _, _ := unstructured.NestedSlice(opts, "downstreamResources")
	if len(list) == 0 {
		return nil
	}
	target, _ := opts["targetNamespace"].(string)
	if target == "" {
		target, _ = opts["defaultNamespace"].(string)
	}
	if target == "" {
		target = "default"
	}
	for _, e := range list {
		m, _ := e.(map[string]any)
		kind, _ := m["kind"].(string)
		name, _ := m["name"].(string)
		var data map[string]any
		var typ string
		switch kind {
		case "Secret":
			var s corev1.Secret
			if err := r.Reader.Get(ctx, types.NamespacedName{Namespace: bd.GetNamespace(), Name: name}, &s); err != nil {
				return fmt.Errorf("downstream Secret %s: %w", name, err)
			}
			d := map[string]any{}
			for k, v := range s.Data {
				d[k] = base64.StdEncoding.EncodeToString(v)
			}
			data, typ = d, string(s.Type)
		case "ConfigMap":
			var c corev1.ConfigMap
			if err := r.Reader.Get(ctx, types.NamespacedName{Namespace: bd.GetNamespace(), Name: name}, &c); err != nil {
				return fmt.Errorf("downstream ConfigMap %s: %w", name, err)
			}
			d := map[string]any{}
			for k, v := range c.Data {
				d[k] = v
			}
			data = d
		default:
			continue
		}
		o := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "v1", "kind": kind,
			"metadata": map[string]any{"name": name, "namespace": target},
			"data":     data,
		}}
		if typ != "" {
			o.Object["type"] = typ
		}
		ns := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": target}}}
		if err := child.ApplyObject(ctx, ns); err != nil {
			return err
		}
		if err := child.ApplyObject(ctx, o); err != nil {
			return err
		}
	}
	return nil
}

// report writes a finished deploy Job's result to its BundleDeployment,
// unless that's been done.
func (r *Push) report(ctx context.Context, child Child, bd *unstructured.Unstructured, j *batchv1.Job) error {
	id := j.Annotations[annotPushDeploy]
	if jobSucceeded(j) {
		applied, _, _ := unstructured.NestedString(bd.Object, "status", "appliedDeploymentID")
		if applied == id {
			return nil
		}
		release := ""
		if rels, err := child.ListReleases(ctx); err == nil {
			for _, rel := range rels {
				if rel.BundleID == bd.GetName() {
					release = fmt.Sprintf("%s/%s:%d", rel.Namespace, rel.Name, rel.Version)
				}
			}
		}
		return r.writeStatus(ctx, bd, true, release, "")
	}
	msg := r.jobMessage(ctx, j)
	cur := deployedMessage(bd)
	if cur == msg {
		return nil
	}
	return r.writeStatus(ctx, bd, false, "", msg)
}

// writeStatus records a deploy on bd's status as fleet-agent does: on
// success the applied deployment, ready and the conditions Fleet rolls up;
// on failure, Deployed false with why.
func (r *Push) writeStatus(ctx context.Context, bd *unstructured.Unstructured, ok bool, release, msg string) error {
	now := r.Now().UTC().Format(time.RFC3339)
	cond := func(t string, status bool, m string) map[string]any {
		s := "False"
		if status {
			s = "True"
		}
		c := map[string]any{"type": t, "status": s, "lastUpdateTime": now}
		if m != "" {
			c["message"] = m
		}
		return c
	}
	patch := map[string]any{}
	if ok {
		id, _, _ := unstructured.NestedString(bd.Object, "spec", "deploymentID")
		patch = map[string]any{
			"appliedDeploymentID": id, "ready": true, "nonModified": true,
			"conditions": []any{cond("Installed", true, ""), cond("Deployed", true, ""), cond("Ready", true, ""), cond("Monitored", true, "")},
		}
		if release != "" {
			patch["release"] = release
		}
	} else {
		patch = map[string]any{
			"ready":      false,
			"conditions": []any{cond("Installed", false, msg), cond("Deployed", false, msg), cond("Ready", false, msg)},
		}
	}
	raw, err := json.Marshal(map[string]any{"status": patch})
	if err != nil {
		return err
	}
	u := object(bundleDeploymentGVK)
	u.SetNamespace(bd.GetNamespace())
	u.SetName(bd.GetName())
	return r.Client.Status().Patch(ctx, u, client.RawPatch(types.MergePatchType, raw))
}

func deployedMessage(bd *unstructured.Unstructured) string {
	conds, _, _ := unstructured.NestedSlice(bd.Object, "status", "conditions")
	for _, c := range conds {
		m, _ := c.(map[string]any)
		if m["type"] == "Deployed" && m["status"] == "False" {
			s, _ := m["message"].(string)
			return s
		}
	}
	return ""
}

// fleetImage is the image fleet-controller runs, unless set.
func (r *Push) fleetImage(ctx context.Context) (string, error) {
	if r.FleetImage != "" {
		return r.FleetImage, nil
	}
	for _, ns := range []string{"cattle-fleet-system", "cattle-fleet-local-system", "fleet-system"} {
		var d unstructured.Unstructured
		d.SetGroupVersionKind(schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"})
		if err := r.Reader.Get(ctx, types.NamespacedName{Namespace: ns, Name: "fleet-controller"}, &d); err != nil {
			continue
		}
		cs, _, _ := unstructured.NestedSlice(d.Object, "spec", "template", "spec", "containers")
		if len(cs) > 0 {
			if img, _ := cs[0].(map[string]any)["image"].(string); img != "" {
				r.FleetImage = img
				return img, nil
			}
		}
	}
	return "", fmt.Errorf("can't find fleet-controller's image (set --fleet-image)")
}

func pushJob(name, ns, cluster, image, kind string, command []string) *batchv1.Job {
	labels := map[string]string{labelPushCluster: cluster, labelPushKind: kind}
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Labels: labels},
		Spec: batchv1.JobSpec{
			BackoffLimit:            ptr.To[int32](0),
			ActiveDeadlineSeconds:   ptr.To[int64](600),
			TTLSecondsAfterFinished: ptr.To(int32(pushJobTTL.Seconds())),
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					RestartPolicy:                corev1.RestartPolicyNever,
					AutomountServiceAccountToken: ptr.To(false),
					Containers: []corev1.Container{{
						Name: kind, Image: image, ImagePullPolicy: corev1.PullIfNotPresent, Command: command,
						TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
						SecurityContext: &corev1.SecurityContext{
							AllowPrivilegeEscalation: ptr.To(false),
							Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
						},
					}},
				},
			},
		},
	}
}

// pushJobName is prefix plus a hash of parts: stable for the same inputs,
// so a deploy isn't started twice, and short enough for a name.
func pushJobName(prefix string, parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return prefix + "-" + hex.EncodeToString(h[:])[:16]
}

func jobDone(j *batchv1.Job) bool {
	for _, c := range j.Status.Conditions {
		if (c.Type == batchv1.JobComplete || c.Type == batchv1.JobFailed) && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func jobSucceeded(j *batchv1.Job) bool {
	for _, c := range j.Status.Conditions {
		if c.Type == batchv1.JobComplete && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func (r *Push) jobPod(ctx context.Context, j *batchv1.Job) (*corev1.Pod, error) {
	var pods corev1.PodList
	if err := r.Reader.List(ctx, &pods, client.InNamespace(j.Namespace), client.MatchingLabels{"job-name": j.Name}); err != nil {
		return nil, err
	}
	if len(pods.Items) == 0 {
		return nil, fmt.Errorf("job %s has no pod", j.Name)
	}
	return &pods.Items[len(pods.Items)-1], nil
}

// jobMessage is why a Job failed: its container's termination message
// (the end of its output), or the Job's own condition.
func (r *Push) jobMessage(ctx context.Context, j *batchv1.Job) string {
	if pod, err := r.jobPod(ctx, j); err == nil {
		for _, s := range pod.Status.ContainerStatuses {
			if t := s.State.Terminated; t != nil && t.Message != "" {
				return lastLines(t.Message, 6)
			}
		}
	}
	for _, c := range j.Status.Conditions {
		if c.Type == batchv1.JobFailed && c.Message != "" {
			return c.Message
		}
	}
	return "the push Job failed"
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
