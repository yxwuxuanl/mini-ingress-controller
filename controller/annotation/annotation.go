package annotation

import (
	"ingress-controller/kube/ingress"
)

const (
	Prefix              = "nginx.ingress.kubernetes.io/"
	AuthSecret          = Prefix + "auth-secret"
	AuthSecretNamespace = Prefix + "auth-secret-namespace"
	EnableAccessLog     = Prefix + "enable-access-log"
	ForceSSLRedirect    = Prefix + "force-ssl-redirect"
)

func ParseAuthSecret(is *ingress.Ingress) (namespace, name string, ok bool) {
	if name = is.Metadata.Annotations[AuthSecret]; name != "" {
		if namespace = is.Metadata.Annotations[AuthSecretNamespace]; namespace == "" {
			namespace = is.Metadata.Namespace
		}
		ok = true
	}

	return
}
