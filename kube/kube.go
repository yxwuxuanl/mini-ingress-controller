package kube

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"
)

const serviceaccountMountPath = "/var/run/secrets/kubernetes.io/serviceaccount"

const (
	EventAdd    = "ADDED"
	EventDelete = "DELETED"
	EventModify = "MODIFIED"
)

type listWatchFunc func(r *http.Request)

type Metadata struct {
	Name              string            `json:"name"`
	Namespace         string            `json:"namespace"`
	Uid               string            `json:"uid"`
	ResourceVersion   string            `json:"resourceVersion"`
	Generation        int               `json:"generation"`
	CreationTimestamp time.Time         `json:"creationTimestamp"`
	Annotations       map[string]string `json:"annotations"`
}

func (m *Metadata) FullName() string {
	return m.Namespace + "/" + m.Name
}

func List[T any](client Client, listFunc listWatchFunc, items *[]T) error {
	r := client.Request()
	listFunc(r)

	res, err := client.Do(r)

	if err != nil {
		return err
	}

	if res.StatusCode != http.StatusOK {
		return errors.New("list: http: " + res.Status)
	}

	dec := json.NewDecoder(res.Body)
	defer res.Body.Close()

	list := new(struct {
		Items []T `json:"items"`
	})

	if err := dec.Decode(list); err != nil {
		return err
	}

	*items = list.Items
	return nil
}

type Event[T any] struct {
	Type   string `json:"type"`
	Object T      `json:"object"`
}

func Watch[T any](
	ctx context.Context,
	client Client,
	watchFunc listWatchFunc,
	ch chan<- Event[T],
) error {
	r := client.Request()

	watchFunc(r)

	res, err := client.Do(r.WithContext(ctx))

	if err != nil {
		return err
	}

	reader := bufio.NewReader(res.Body)
	defer res.Body.Close()

	for {
		chunk, _, err := reader.ReadLine()

		if err != nil {
			return err
		}

		event := Event[T]{}

		if err := json.Unmarshal(chunk, &event); err != nil {
			log.Printf("kube: watch: unmarshal event error: %s", err)
			continue
		}

		ch <- event
	}
}
