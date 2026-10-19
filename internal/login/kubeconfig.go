package login

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// LocalCluster is the management cluster's ID in proxy paths, as in
// Rancher.
const LocalCluster = "local"

// ProxyURL is the /k8s/clusters/<id> address of a cluster on s.
func ProxyURL(s *Server, clusterID string) string {
	return s.URL + "/k8s/clusters/" + clusterID
}

// Kubeconfig builds a kubeconfig for clusterID through s's proxy. It holds
// no secret: its credential is `<hyveBinary> token --server <name>`, an
// exec plugin returning the current session, so a copied file grants
// nothing by itself. home, when not the default, is passed to it.
func Kubeconfig(s *Server, clusterID, hyveBinary, home string) ([]byte, error) {
	cluster := &clientcmdapi.Cluster{Server: ProxyURL(s, clusterID), InsecureSkipTLSVerify: s.InsecureSkipVerify}
	if s.CACertFile != "" {
		ca, err := os.ReadFile(s.CACertFile)
		if err != nil {
			return nil, err
		}
		cluster.CertificateAuthorityData = ca
	}
	name := s.Name
	if clusterID != LocalCluster {
		name = s.Name + "-" + clusterID
	}
	cfg := clientcmdapi.NewConfig()
	cfg.Clusters[name] = cluster
	args := []string{"token", "--server", s.Name}
	if home != "" {
		args = append([]string{"--home", home}, args...)
	}
	cfg.AuthInfos[s.Name] = &clientcmdapi.AuthInfo{
		Exec: &clientcmdapi.ExecConfig{
			APIVersion:         "client.authentication.k8s.io/v1",
			Command:            hyveBinary,
			Args:               args,
			InteractiveMode:    clientcmdapi.IfAvailableExecInteractiveMode,
			ProvideClusterInfo: false,
			InstallHint:        "hyve is needed to use this kubeconfig: https://github.com/cbridges1/hyve",
		},
	}
	cfg.Contexts[name] = &clientcmdapi.Context{Cluster: name, AuthInfo: s.Name}
	cfg.CurrentContext = name
	return clientcmd.Write(*cfg)
}

// RESTConfig is a client config for clusterID through s's proxy, using the
// stored session directly.
func RESTConfig(s *Server, clusterID string) *rest.Config {
	return &rest.Config{
		Host:        ProxyURL(s, clusterID),
		BearerToken: s.Token,
		TLSClientConfig: rest.TLSClientConfig{
			CAFile:   s.CACertFile,
			Insecure: s.InsecureSkipVerify,
		},
	}
}

// ExecCredential is the client.authentication.k8s.io/v1 response an exec
// plugin prints.
func ExecCredential(token string, expiresAt time.Time) ([]byte, error) {
	status := map[string]any{"token": token}
	if !expiresAt.IsZero() {
		status["expirationTimestamp"] = expiresAt.UTC().Format(time.RFC3339)
	}
	out, err := json.Marshal(map[string]any{
		"apiVersion": "client.authentication.k8s.io/v1",
		"kind":       "ExecCredential",
		"status":     status,
	})
	if err != nil {
		return nil, fmt.Errorf("encoding the credential: %w", err)
	}
	return out, nil
}
