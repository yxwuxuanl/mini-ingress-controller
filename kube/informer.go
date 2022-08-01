package kube

import (
	"context"
)

type informerHandler[T Object] func(T)

type informerRef[T Object] struct {
	refCount int
	obj      T
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
		ref.refCount++
		*obj = ref.obj
		return nil
	}

	if err := Get(i.Client, readFunc, *obj); err != nil {
		return err
	}

	ref := &informerRef[T]{
		obj:      *obj,
		refCount: 1,
	}

	i.ref[fullname] = ref
	return nil
}

func (i *Informer[T]) Release(namespace, name string) {
	fullname := namespace + "/" + name

	if ref, ok := i.ref[fullname]; ok {
		if ref.refCount <= 1 {
			i.OnRelease(ref.obj)
			delete(i.ref, fullname)
		} else {
			ref.refCount--
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
