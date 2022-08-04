package kube

import (
	"context"
	"log"
)

type informerHandler[T Object] func(T)

type informerRef[T Object] struct {
	refCount int
	obj      T
}

func (ref *informerRef[T]) add(i int) int {
	ref.refCount += i
	log.Printf("informer: %s ref=%d", ref.obj.Name(), ref.refCount)
	return ref.refCount
}

type Informer[T Object] struct {
	Client    Client
	OnModify  informerHandler[T]
	OnRelease informerHandler[T]
	WatchFunc ReadFunc
	ref       map[string]*informerRef[T]
}

func (i *Informer[T]) Init() {
	i.ref = make(map[string]*informerRef[T])
}

func (i *Informer[T]) Get(namespace, name string, readFunc ReadFunc, obj *T) error {
	fullname := namespace + "/" + name

	if ref, ok := i.ref[fullname]; ok {
		ref.add(1)
		*obj = ref.obj
		return nil
	}

	if err := Get(i.Client, readFunc, *obj); err != nil {
		return err
	}

	ref := &informerRef[T]{obj: *obj}

	ref.add(1)

	i.ref[fullname] = ref
	return nil
}

func (i *Informer[T]) Release(namespace, name string) {
	fullname := namespace + "/" + name

	if ref, ok := i.ref[fullname]; ok {
		if ref.add(-1) <= 0 {
			i.OnRelease(ref.obj)
			delete(i.ref, fullname)
		}
	}
}

func (i *Informer[T]) Run(ctx context.Context) {
	handler := WatchHandler[T]{
		Modified: func(obj T) {
			if ref, ok := i.ref[obj.Name()]; ok {
				ref.obj = obj
				i.OnModify(obj)
			}
		},
		Deleted: func(obj T) {
			delete(i.ref, obj.Name())
		},
	}

	Watch(ctx, i.Client, i.WatchFunc, handler)
}
