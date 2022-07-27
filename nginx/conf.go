package nginx

import "ingress-controller/ingress"

type Path struct {
	Path     string
	PathType string
}

func (p Path) String() string {
	if p.PathType == ingress.PathTypePrefix {
		return p.Path
	}

	return p.Path + " (Exact)"
}

type LocationConfig struct {
	Path
	ProxyPass  string
	IngressRef string
}

type ServerConfig struct {
	ServerName string
	Locations  map[Path]*LocationConfig
}

type Config struct {
	WorkerProcesses   int
	WorkerConnections int
	User              string
	PidFile           string
	Prefix            string
}

type HttpConfig struct {
	LogFormat string
	AccessLog string
	Listen    int
	Servers   map[string]*ServerConfig
}
