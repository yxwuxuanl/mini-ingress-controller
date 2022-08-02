package nginx

import (
	"bytes"
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"ingress-controller/kube/ingress"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"syscall"
	"text/template"
	"time"
)

const MainLogFormat = `'$remote_addr - $remote_user [$time_local] "$request" '
'$status $body_bytes_sent "$http_referer" '
'"$http_user_agent" "$http_x_forwarded_for"'`

var Prefix = flag.String("ngx.prefix", "/etc/nginx", "")

//go:embed templates/nginx.gotpl
var _nginxTpl string

//go:embed templates/http.gotpl
var _httpTpl string

var (
	nginxTpl *template.Template
	httpTpl  *template.Template
)

var noNgx = os.Getenv("NO_NGINX") == "1"

func init() {
	var err error

	funcMap := template.FuncMap{
		"now": func() string {
			return time.Now().Format(time.RFC3339)
		},
	}

	if nginxTpl, err = template.New("nginx.nginx").Funcs(funcMap).Parse(_nginxTpl); err != nil {
		panic(err)
	}

	if httpTpl, err = template.New("nginx.http").Funcs(funcMap).Parse(_httpTpl); err != nil {
		panic(err)
	}
}

type Nginx struct {
	mainConf *Main
	httpConf *Http
	cmd      *exec.Cmd
	stopCh   chan struct{}
}

func (ngx *Nginx) AddLocation(host string, loc *Location, tlsConf *TLSConf) error {
	if host == "" {
		host = "_"

		if loc.Path.Path == "/" {
			return errors.New("nginx: definition is not allowed")
		}

		tlsConf = nil
	}

	var server *Server

	if tlsConf != nil {
		server = ngx.httpConf.SSLServers[host]

		if server != nil {
			if server.SSL.Key != tlsConf.Key || server.SSL.Cert != tlsConf.Cert {
				return fmt.Errorf("nginx: ssl certificate conflict, host=%s", host)
			}
		} else {
			server = &Server{
				ServerName: host,
				Locations:  map[string]*Location{},
				SSL:        tlsConf,
			}

			ngx.httpConf.SSLServers[host] = server
		}
	} else {
		server = ngx.httpConf.Servers[host]

		if server == nil {
			server = &Server{
				ServerName: host,
				Locations:  map[string]*Location{},
			}

			ngx.httpConf.Servers[host] = server
		}
	}

	locations := server.Locations

	if loc, ok := locations[loc.Path.String()]; ok {
		return fmt.Errorf("nginx: duplicated location %s", loc.Path)
	}

	locations[loc.Path.String()] = loc
	log.Printf("nginx: add location %s, server_name=%s", loc.Path.String(), host)
	return nil
}

func (ngx *Nginx) DeleteLocation(host string, isRef string) {
	if host == "" {
		host = "_"
	}

	doDelete := func(s *Server) (deleteServer bool) {
		if s == nil {
			return false
		}

		var locNum int

		for path, loc := range s.Locations {
			if loc.IngressRef == isRef {
				delete(s.Locations, path)
				log.Printf("nginx: delete location %s, server_name=%s", path, s.ServerName)
			} else {
				locNum++
			}
		}

		return locNum == 0
	}

	if doDelete(ngx.httpConf.Servers[host]) {
		delete(ngx.httpConf.Servers, host)
	}

	if doDelete(ngx.httpConf.SSLServers[host]) {
		delete(ngx.httpConf.SSLServers, host)
	}
}

func (ngx *Nginx) BuildHttpConfig() error {
	var buf bytes.Buffer

	if err := httpTpl.Execute(&buf, ngx.httpConf); err != nil {
		return err
	}

	return ioutil.WriteFile(*Prefix+"/http.conf", buf.Bytes(), 0777)
}

func (ngx *Nginx) BuildMainConfig() error {
	var buf bytes.Buffer

	if err := nginxTpl.Execute(&buf, ngx.mainConf); err != nil {
		return err
	}

	return ioutil.WriteFile(*Prefix+"/nginx.conf", buf.Bytes(), 0777)
}

func (ngx *Nginx) Reload() {
	if noNgx {
		return
	}

	if err := ngx.cmd.Process.Signal(syscall.SIGHUP); err != nil {
		log.Printf("nginx: reload error: %s", err)
	}
}

func (ngx *Nginx) Run() error {
	if noNgx {
		return nil
	}

	cmd := exec.Command("nginx", "-p", *Prefix)

	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout

	ngx.cmd = cmd

	if err := cmd.Run(); err != nil {
		return err
	}

	ngx.stopCh <- struct{}{}
	return nil
}

func (ngx *Nginx) Shutdown() {
	if noNgx {
		return
	}

	if err := ngx.cmd.Process.Signal(syscall.SIGQUIT); err != nil {
		log.Printf("nginx: shutdown error: %s", err)
	}

	<-ngx.stopCh
}

func New(mainConf *Main, httpConf *Http) *Nginx {
	healthz := &Location{
		Path: Path{
			Path:     "/_/healthz",
			PathType: ingress.PathTypeExact,
		},
		Return: &ReturnConf{
			Status:     "ok",
			StatusCode: 200,
		},
		DisableAccessLog: true,
	}

	dumpConfig := &Location{
		Path: Path{
			Path:  "/_/dump-config/(nginx|http)",
			Regex: true,
		},
		Directives: []Directive{
			{
				"alias",
				fmt.Sprintf("%s/$1.conf", *Prefix),
			},
		},
	}

	stub := &Location{
		Path: Path{
			Path:     "/_/stub_status",
			PathType: ingress.PathTypeExact,
		},
		DisableAccessLog: true,
		Directives:       []Directive{{"stub_status"}},
	}

	locations := map[string]*Location{
		healthz.Path.String():    healthz,
		dumpConfig.Path.String(): dumpConfig,
		stub.Path.String():       stub,
	}

	httpConf.Servers = map[string]*Server{
		"_": {
			ServerName: "_",
			Locations:  locations,
		},
	}

	httpConf.SSLServers = map[string]*Server{}

	return &Nginx{
		mainConf: mainConf,
		httpConf: httpConf,
		stopCh:   make(chan struct{}),
	}
}
