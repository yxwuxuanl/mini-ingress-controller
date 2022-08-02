package secret

import (
	"fmt"
	"ingress-controller/kube"
	"net/http"
)

const (
	TypeOpaque = "Opaque"
	TypeTLS    = "kubernetes.io/tls"
)

type Secret struct {
	Metadata *kube.Metadata    `json:"metadata"`
	Data     map[string][]byte `json:"data"`
	Type     string            `json:"type"`
}

func (s *Secret) Name() string {
	return fmt.Sprintf("%s/%s", s.Metadata.Namespace, s.Metadata.Name)
}

func ReadFunc(namespace, name string) kube.ReadFunc {
	return func(r *http.Request) {
		r.URL.Path = fmt.Sprintf("/api/v1/namespaces/%s/secrets/%s", namespace, name)
	}
}

func WatchFunc(r *http.Request) {
	r.URL.Path = "/api/v1/watch/secrets"
}
