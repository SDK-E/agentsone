// SPDX-License-Identifier: MIT

// Package upstream resolves allowed names from the edge, through the edge container's own
// resolver on its outer network (Docker's 127.0.0.11, which forwards to the host's DNS).
package upstream

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"

	"github.com/SDK-E/agentsone/internal/edge/net/app"
)

// Resolver is an app.Resolver over a net.Resolver.
type Resolver struct{ r *net.Resolver }

// New makes a resolver. A nil r uses net.DefaultResolver.
func New(r *net.Resolver) *Resolver {
	if r == nil {
		r = net.DefaultResolver
	}
	return &Resolver{r: r}
}

// LookupIPv4 returns the IPv4 addresses of host. The name is looked up as a fully qualified name,
// so search domains never turn an allowed name into a name on a local network.
func (r *Resolver) LookupIPv4(ctx context.Context, host string) ([]netip.Addr, error) {
	fqdn := strings.TrimSuffix(host, ".") + "."
	addrs, err := r.r.LookupNetIP(ctx, "ip4", fqdn)
	if err != nil {
		var dnsErr *net.DNSError
		if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
			return nil, fmt.Errorf("%w: %s", app.ErrNameNotFound, host)
		}
		return nil, fmt.Errorf("resolve %s: %w", host, err)
	}
	return addrs, nil
}
