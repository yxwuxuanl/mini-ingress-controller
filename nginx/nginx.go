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

func (ngx *Nginx) AddLocation(host string, loc *Location, ssl *SSLConf) error {
	if host == "" {
		host = "_"

		if loc.Path.Path == "/" {
			return errors.New("nginx: definition is not allowed")
		}
	}

	server, ok := ngx.httpConf.Servers[host]

	if !ok {
		server = &Server{
			ServerName: host,
			Locations:  map[string]*Location{},
		}

		ngx.httpConf.Servers[host] = server
	}

	locations := server.Locations

	if loc, ok := locations[loc.Path.String()]; ok {
		return fmt.Errorf("nginx: duplicated location %s", loc.Path)
	}

	locations[loc.Path.String()] = loc
	return nil
}

func (ngx *Nginx) DeleteLocation(host string, isRef string) {
	if host == "" {
		host = "_"
	}

	server, ok := ngx.httpConf.Servers[host]

	if !ok {
		return
	}

	var locNum int

	for path, loc := range server.Locations {
		if loc.IngressRef == isRef {
			delete(server.Locations, path)
		} else {
			locNum++
		}
	}

	if locNum == 0 {
		delete(ngx.httpConf.Servers, host)
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
		log.Printf("nginx: SIGHUP: %s", err)
	} else {
		log.Printf("nginx: reload success")
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
		log.Printf("nginx: SIGQUIT: %s", err)
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

	locations := map[string]*Location{
		healthz.Path.String():    healthz,
		dumpConfig.Path.String(): dumpConfig,
	}

	httpConf.Servers = map[string]*Server{
		"_": {
			ServerName: "_",
			Locations:  locations,
		},
	}

	return &Nginx{
		mainConf: mainConf,
		httpConf: httpConf,
		stopCh:   make(chan struct{}),
	}
}
