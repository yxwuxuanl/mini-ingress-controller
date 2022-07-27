package kube

import (
	"crypto/tls"
	"crypto/x509"
	"io/ioutil"
	"net/http"
	"os"
)

type Client interface {
	Request() *http.Request
	Do(r *http.Request) (*http.Response, error)
}

type InClusterClient struct {
	client *http.Client
	token  string
}

func (i *InClusterClient) Request() *http.Request {
	r, _ := http.NewRequest(
		http.MethodGet,
		"https://"+os.Getenv("KUBERNETES_SERVICE_HOST")+":"+os.Getenv("KUBERNETES_SERVICE_PORT_HTTPS"),
		nil,
	)

	r.Header = make(http.Header)
	r.Header.Set("Authorization", "Bearer "+i.token)

	return r
}

func (i *InClusterClient) Do(r *http.Request) (*http.Response, error) {
	return i.client.Do(r)
}

func NewInClusterClient() *InClusterClient {
	if v := os.Getenv("KUBERNETES_PORT"); v == "" {
		panic("not in cluster")
	}

	certPool := x509.NewCertPool()

	if ca, err := ioutil.ReadFile(serviceaccountMountPath + "/ca.crt"); err != nil {
		panic(err)
	} else {
		certPool.AppendCertsFromPEM(ca)
	}

	token, err := ioutil.ReadFile(serviceaccountMountPath + "/token")

	if err != nil {
		panic(err)
	}

	httpClient := new(http.Client)

	httpClient.Transport = &http.Transport{
		TLSClientConfig: &tls.Config{
			RootCAs: certPool,
		},
	}

	return &InClusterClient{
		client: httpClient,
		token:  string(token),
	}
}

type ProxyClient struct {
	proxyAddr string
}

func (p *ProxyClient) Request() *http.Request {
	r, _ := http.NewRequest(http.MethodGet, p.proxyAddr, nil)
	return r
}

func (p *ProxyClient) Do(r *http.Request) (*http.Response, error) {
	return http.DefaultClient.Do(r)
}

func NewProxyClient(proxyAddr string) *ProxyClient {
	return &ProxyClient{proxyAddr: proxyAddr}
}
