package local

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/cbridges1/hyve/internal/kube"
)

// The local cluster is one K3s container that local mode runs with Docker
// alone, nothing else to install.

const (
	// NodeName is the K3s container's name; children reach the host
	// endpoint by it, through Docker's DNS.
	NodeName = "hyve-local"
	// legacyNodeName is the node k3d made, before local mode ran K3s
	// itself. Its k3d entrypoint is in the container, so Docker starts and
	// stops it the same way, and local mode keeps using it.
	legacyNodeName = "k3d-" + Name + "-server-0"

	ownerLabel = "hyve.io/local=true"
	// restartPolicy keeps the node running across Docker and machine
	// restarts, until 'hyve local stop' stops it.
	restartPolicy = "unless-stopped"
	apiPort       = "6443/tcp"
)

// nodeEntrypoint runs K3s with the DNS fix k3d makes: Docker's embedded DNS
// (127.0.0.11) only answers inside the container's own network namespace,
// so without it K3s gives pods 8.8.8.8, and they can't resolve container
// names or names only the host's resolver knows (a VPN's). This points
// the node's resolver at the network's gateway and forwards DNS there to
// Docker's resolver, for the node and its pods.
const nodeEntrypoint = `set -e
gw=$(ip -4 route | awk '/^default/ {print $3; exit}')
if grep -q '^nameserver 127.0.0.11' /etc/resolv.conf && [ -n "$gw" ]; then
  iptables-save | sed -e "s/-d 127.0.0.11/-d $gw/g" -e 's/-A OUTPUT \(.*\) -j DOCKER_OUTPUT/\0\n-A PREROUTING \1 -j DOCKER_OUTPUT/' -e "s/--to-source :53/--to-source $gw:53/g" | iptables-restore
  sed "s/127.0.0.11/$gw/" /etc/resolv.conf > /tmp/resolv.conf && cat /tmp/resolv.conf > /etc/resolv.conf
fi
exec /bin/k3s "$@"
`

// node is the local cluster's container: local mode's own, or the one an
// older version made with k3d. Empty when there's neither.
func node(ctx context.Context) string {
	for _, n := range []string{NodeName, legacyNodeName} {
		if _, err := run(ctx, "docker", "container", "inspect", n); err == nil {
			return n
		}
	}
	return ""
}

// ours reports whether the node carries local mode's label.
func ours(ctx context.Context, n string) bool {
	out, err := run(ctx, "docker", "inspect", "-f", `{{ index .Config.Labels "hyve.io/local" }}`, n)
	return err == nil && out == "true"
}

// Running reports whether the local cluster exists and its node is up.
func Running(ctx context.Context) (exists, running bool) {
	n := node(ctx)
	if n == "" {
		return false, false
	}
	out, err := run(ctx, "docker", "inspect", "-f", "{{.State.Running}}", n)
	return true, err == nil && out == "true"
}

func createNode(ctx context.Context, o Options) error {
	args := []string{"run", "-d", "--name", NodeName, "--hostname", NodeName,
		"--privileged", "--restart", restartPolicy,
		"--network", DockerNetwork,
		"--label", ownerLabel,
		"--tmpfs", "/run", "--tmpfs", "/var/run",
		// CAPD creates child-cluster nodes as containers on this Docker.
		"--volume", "/var/run/docker.sock:/var/run/docker.sock",
		// The API on a free port, the host endpoint on its own.
		"--publish", "127.0.0.1::6443",
		"--publish", fmt.Sprintf("127.0.0.1:%d:%d", o.Port, o.Port),
		"--env", "GOGC=" + o.GOGC,
		"--entrypoint", "/bin/sh",
		o.K3sImage, "-c", nodeEntrypoint, "k3s",
		"server", "--tls-san", "127.0.0.1",
	}
	for _, d := range []string{"traefik", "metrics-server", "servicelb"} {
		args = append(args, "--disable="+d)
	}
	for _, a := range o.APIServerArg {
		args = append(args, "--kube-apiserver-arg="+a)
	}
	_, err := run(ctx, "docker", args...)
	return err
}

func startNode(ctx context.Context, n string) error {
	_, err := run(ctx, "docker", "start", n)
	return err
}

// persist gives a node made before local mode kept it running (or by k3d)
// the restart policy.
func persist(ctx context.Context, n string) error {
	_, err := run(ctx, "docker", "update", "--restart", restartPolicy, n)
	return err
}

// nodeKubeconfig is the node's admin kubeconfig, pointed at the API's port
// on this machine (Docker picks a new one each time the node starts). It
// waits for K3s to write it.
func nodeKubeconfig(ctx context.Context, n string) (string, error) {
	var raw string
	err := retry(ctx, 2*time.Minute, func() error {
		out, err := run(ctx, "docker", "exec", n, "cat", "/etc/rancher/k3s/k3s.yaml")
		raw = out
		return err
	})
	if err != nil {
		return "", fmt.Errorf("waiting for K3s's kubeconfig: %w", err)
	}
	addr, err := run(ctx, "docker", "port", n, apiPort)
	if err != nil {
		return "", err
	}
	addr = strings.SplitN(addr, "\n", 2)[0]
	addr = strings.Replace(addr, "0.0.0.0:", "127.0.0.1:", 1)
	cfg, err := clientcmd.Load([]byte(raw))
	if err != nil {
		return "", err
	}
	for _, c := range cfg.Clusters {
		c.Server = "https://" + addr
	}
	out, err := clientcmd.Write(*cfg)
	return string(out), err
}

// waitReady waits for the node to be Ready.
func waitReady(ctx context.Context, kc *kube.Client) error {
	return retry(ctx, 5*time.Minute, func() error {
		nodes, err := kc.Dynamic.Resource(schema.GroupVersionResource{Version: "v1", Resource: "nodes"}).List(ctx, metav1.ListOptions{})
		if err != nil {
			return err
		}
		for _, n := range nodes.Items {
			conds, _, _ := unstructured.NestedSlice(n.Object, "status", "conditions")
			for _, c := range conds {
				if m, _ := c.(map[string]any); m["type"] == "Ready" && m["status"] == "True" {
					return nil
				}
			}
		}
		return fmt.Errorf("the local cluster's node isn't Ready yet")
	})
}

// importImage copies a local Docker image into the node's containerd.
//
// It's imported under a tag naming the image's ID (hyve:dev becomes
// hyve:dev-1a2b3c4d5e6f), which it returns for the Deployments to use: a
// rebuilt image under the same tag would leave their spec unchanged, so
// the pods would keep running the old one.
func importImage(ctx context.Context, n, image string) (string, error) {
	id, err := run(ctx, "docker", "image", "inspect", image, "--format", "{{.Id}}")
	if err != nil {
		return "", err
	}
	ref := image
	if !strings.Contains(image, "@") {
		repo, tag := image, "latest"
		if i := strings.LastIndex(image, ":"); i > strings.LastIndex(image, "/") {
			repo, tag = image[:i], image[i+1:]
		}
		short := strings.TrimPrefix(id, "sha256:")
		if len(short) > 12 {
			short = short[:12]
		}
		ref = repo + ":" + tag + "-" + short
		if _, err := run(ctx, "docker", "tag", image, ref); err != nil {
			return "", err
		}
		// The host only needs the tag long enough to save it.
		defer run(context.WithoutCancel(ctx), "docker", "rmi", ref) //nolint:errcheck
	}
	save := exec.CommandContext(ctx, "docker", "save", ref)
	load := exec.CommandContext(ctx, "docker", "exec", "-i", n, "ctr", "-n", "k8s.io", "images", "import", "-")
	pipe, err := save.StdoutPipe()
	if err != nil {
		return "", err
	}
	load.Stdin = pipe
	var saveErr strings.Builder
	save.Stderr = &saveErr
	if err := save.Start(); err != nil {
		return "", err
	}
	out, loadErr := load.CombinedOutput()
	if err := save.Wait(); err != nil {
		return "", fmt.Errorf("docker save %s: %w: %s", ref, err, strings.TrimSpace(saveErr.String()))
	}
	if loadErr != nil {
		return "", fmt.Errorf("importing %s: %w: %s", ref, loadErr, strings.TrimSpace(string(out)))
	}
	return ref, nil
}
