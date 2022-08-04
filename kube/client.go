package kube

import (
	"crypto/tls"
	"crypto/x509"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
)

type Client interface {
	Do(r *http.Request) (*http.Response, error)
}

type InClusterClient struct {
	client *http.Client
	token  string
}

func (i *InClusterClient) Do(r *http.Request) (*http.Response, error) {
	u := r.URL
	u.Scheme = "https"
	u.Host = os.Getenv("KUBERNETES_SERVICE_HOST") + ":" + os.Getenv("KUBERNETES_SERVICE_PORT_HTTPS")
	r.Header.Set("Authorization", "Bearer "+i.token)

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
	endpoint *url.URL
}

func (p *ProxyClient) Do(r *http.Request) (*http.Response, error) {
	r.URL.Scheme = p.endpoint.Scheme
	r.URL.Host = p.endpoint.Host

	return http.DefaultClient.Do(r)
}

func NewProxyClient(proxyAddr string) *ProxyClient {
	if u, err := url.Parse(proxyAddr); err != nil {
		panic(err)
	} else {
		return &ProxyClient{endpoint: u}
	}
}
