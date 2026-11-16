package discovery

import (
	"context"

	"github.com/brutella/dnssd"
)

var lookupType mdnsLookup = endCanceled(dnssd.LookupType)

func endCanceled(lookup mdnsLookup) mdnsLookup {
	return func(ctx context.Context, service string, add dnssd.AddFunc, rmv dnssd.RmvFunc) error {
		lctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
		defer cancel()
		stop := context.AfterFunc(ctx, cancel)
		defer stop()
		return lookup(lctx, service, add, rmv)
	}
}
