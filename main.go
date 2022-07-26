package main

import (
	"context"
	"flag"
	"ingress-controller/controller"
	"ingress-controller/kube"
	"ingress-controller/nginx"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"syscall"
)

var (
	ngxWorkerProcesses   = flag.Int("ngx.worker-processes", -1, "")
	ngxWorkerConnections = flag.Int("ngx.worker-connections", 256, "")
	ngxLogFormat         = flag.String("ngx.log-format", nginx.MainLogFormat, "")
	ngxListenPort        = flag.Int("ngx.listen", 80, "")
	ngxHttpsListenPort   = flag.Int("ngx.https-listen", 443, "")
	ngxUser              = flag.String("ngx.user", "nginx", "")
	ngxHttp2             = flag.Bool("ngx.http2", true, "")
	ngxLogLevel          = flag.String("ngx.log-level", "notice", "")
	ngxAccessLog         = flag.String("ngx.access-log", "/dev/stdout", "")
	kubeProxy            = flag.String("kube.proxy", "", "")
	pprofAddr            = flag.String("pprof.addr", "", "")
)

func main() {
	flag.Parse()

	var kubeClient kube.Client

	if *kubeProxy != "" {
		kubeClient = kube.NewProxyClient(*kubeProxy)
	} else {
		kubeClient = kube.NewInClusterClient()
	}

	ngxConf := &nginx.Main{
		WorkerProcesses:   *ngxWorkerProcesses,
		WorkerConnections: *ngxWorkerConnections,
		LogLevel:          *ngxLogLevel,
		User:              *ngxUser,
	}

	httpConf := &nginx.Http{
		Http2:     *ngxHttp2,
		LogFormat: *ngxLogFormat,
		Listen:    *ngxListenPort,
		TLSListen: *ngxHttpsListenPort,
		AccessLog: *ngxAccessLog,
	}

	ngx := nginx.New(ngxConf, httpConf)

	if err := ngx.BuildMainConfig(); err != nil {
		panic(err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer cancel()

	ctr := controller.New(ngx, kubeClient)

	go func() {
		if err := ctr.Run(ctx); err != nil {
			panic(err)
		}
	}()

	if *pprofAddr != "" {
		http.HandleFunc("/debug/pprof/heap", pprof.Index)
		go http.ListenAndServe(*pprofAddr, nil)
	}

	<-ctx.Done()
	ctr.Shutdown()
}
