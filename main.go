package main

import (
	"context"
	"errors"
	"flag"
	"ingress-controller/controller"
	"ingress-controller/ingress"
	"ingress-controller/kube"
	"ingress-controller/nginx"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

var (
	ingressClassName     = flag.String("ingress-class", "", "")
	ngxWorkerProcesses   = flag.Int("ngx.worker-processes", -1, "")
	ngxWorkerConnections = flag.Int("ngx.worker-connections", 256, "")
	ngxLogFormat         = flag.String("ngx.log-format", nginx.MainLogFormat, "")
	ngxListenPort        = flag.Int("ngx.listen", 3000, "")
	ngxPrefix            = flag.String("ngx.prefix", "/etc/nginx", "")
	ngxUser              = flag.String("ngx.user", "nginx", "")
	ngxAccessLog         = flag.String("ngx.access-log", "/dev/stdout", "")
	kubeProxy            = flag.String("kube.proxy", "", "")
)

func main() {
	flag.Parse()

	var kubeClient kube.Client

	if *kubeProxy != "" {
		kubeClient = kube.NewProxyClient(*kubeProxy)
	} else {
		kubeClient = kube.NewInClusterClient()
	}

	ngxConf := &nginx.Config{
		WorkerProcesses:   *ngxWorkerProcesses,
		WorkerConnections: *ngxWorkerConnections,
		User:              *ngxUser,
		Prefix:            *ngxPrefix,
	}

	httpConf := &nginx.HttpConfig{
		LogFormat: *ngxLogFormat,
		Listen:    *ngxListenPort,
		AccessLog: *ngxAccessLog,
	}

	ngx := nginx.New(ngxConf, httpConf)

	var iss []*ingress.Ingress

	err := kube.List(kubeClient, listIngressFunc, &iss)

	if err != nil {
		panic(err)
	}

	ctr := controller.New(ngx)

	if err = setupNginxConf(iss, ngx, ctr); err != nil {
		panic(err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer cancel()

	go func() {
		if err = ngx.Run(); err != nil {
			panic(err)
		}
	}()

	go startWatch(ctx, kubeClient, ctr, ngx)

	log.Printf("PID: %d", os.Getpid())

	<-ctx.Done()
	ngx.Shutdown()
}

func watchIngressFunc(r *http.Request) {
	r.URL.Path = "/apis/networking.k8s.io/v1/watch/ingresses"
}

func listIngressFunc(r *http.Request) {
	r.URL.Path = "/apis/networking.k8s.io/v1/ingresses"
}

func filterIngress(is *ingress.Ingress) bool {
	if *ingressClassName == "" {
		return true
	}

	if annos := is.Metadata.Annotations; annos != nil {
		if v, ok := annos[ingress.AnnotationKubernetesIngressClass]; ok {
			return v == *ingressClassName
		}
	}

	return false
}

func setupNginxConf(
	iss []*ingress.Ingress,
	ngx *nginx.Nginx,
	ctr *controller.Controller,
) error {
	if err := ngx.BuildMainConfig(); err != nil {
		return err
	}

	for _, is := range iss {
		if filterIngress(is) {
			ctr.Added(is)
		}
	}

	return ngx.BuildHttpConfig()
}

func startWatch(
	ctx context.Context,
	kubeClient kube.Client,
	ctr *controller.Controller,
	ngx *nginx.Nginx,
) {
	eventCh := make(chan kube.Event[*ingress.Ingress], 100)
	defer close(eventCh)

	go func() {
		for event := range eventCh {
			if !filterIngress(event.Object) {
				continue
			}

			log.Printf("watch: receive event for %s, event=%s", event.Object.Metadata.FullName(), event.Type)

			var needReload bool

			switch event.Type {
			case kube.EventAdd:
				needReload = ctr.Added(event.Object)
			case kube.EventDelete:
				needReload = ctr.Delete(event.Object)
			case kube.EventModify:
				needReload = ctr.Modify(event.Object)
			}

			if !needReload {
				continue
			}

			if err := ngx.BuildHttpConfig(); err != nil {
				log.Printf("watch: %s", err)
			} else {
				ngx.Reload()
			}
		}
	}()

	for {
		log.Printf("watch: start watch")

		err := kube.Watch(ctx, kubeClient, watchIngressFunc, eventCh)

		if errors.Is(err, context.Canceled) {
			return
		}

		log.Printf("watch: %s", err)
		time.Sleep(time.Second * 5)
	}
}
