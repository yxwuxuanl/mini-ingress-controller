package annotation

const (
	Prefix              = "nginx.ingress.kubernetes.io/"
	AuthSecret          = Prefix + "auth-secret"
	AuthSecretNamespace = Prefix + "auth-secret-namespace"
	EnableAccessLog     = Prefix + "enable-access-log"
	RewriteTarget       = Prefix + "rewrite-target"
)
