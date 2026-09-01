// MIT License
//
// Copyright (c) 2025 lok8s
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package native

import (
	"fmt"
	"net"
)

// FindFreeSubnet finds a subnet CIDR that doesn't overlap with any existing
// `container` network, starting from startSubnet and incrementing by step
// each try. Unlike pkg/network/subnet.go's libvirt equivalent, overlap is
// checked directly against `container network list`'s live output rather
// than a separately persisted allocation file, since container networks are
// already queryable as the source of truth.
//
// This only checks other project networks, not Cilium's own pod-CIDR pool
// (10.0.0.0/8 by default), which numerically contains the project subnets
// we draw from too. That's a latent overlap, not an active one: Cilium
// hands out per-node /24s
// sequentially from the bottom of its pool, so a project subnet like
// 10.89.0.0/16 would only collide with real pod IPs after tens of thousands
// of nodes in one cluster - far beyond anything lok8s creates. Pod and
// service CIDRs are also reused as-is across every cluster in a project (see
// CreateClusters), which is fine for the same reason: they're internal to
// each cluster's own Cilium/kube-proxy and never routed onto the shared
// network, so two clusters reusing the same private range don't conflict
// any more than two unrelated home routers both using 192.168.1.0/24 would.
func FindFreeSubnet(client *ContainerClient, startSubnet string, step, tries int) (string, error) {
	existing, err := client.ListNetworkSubnets()
	if err != nil {
		return "", fmt.Errorf("failed to list existing container networks: %w", err)
	}

	currSubnet := startSubnet
	for try := 0; try < tries; try++ {
		_, ipNet, err := net.ParseCIDR(currSubnet)
		if err != nil {
			return "", fmt.Errorf("failed to parse subnet %s: %w", currSubnet, err)
		}

		if !subnetOverlapsAny(ipNet, existing) {
			return currSubnet, nil
		}

		prefix, _ := ipNet.Mask.Size()
		nextIP := ipNet.IP.To4()
		if nextIP == nil {
			return "", fmt.Errorf("invalid IPv4 subnet: %s", currSubnet)
		}

		next := make(net.IP, len(nextIP))
		copy(next, nextIP)
		if prefix <= 16 {
			next[1] += byte(step)
		} else {
			next[2] += byte(step)
		}
		currSubnet = fmt.Sprintf("%s/%d", next.String(), prefix)
	}

	return "", fmt.Errorf("no free subnet found after %d tries starting from %s", tries, startSubnet)
}

// subnetOverlapsAny reports whether candidate overlaps with any of the given subnet CIDRs
func subnetOverlapsAny(candidate *net.IPNet, subnetCIDRs map[string]string) bool {
	for _, cidr := range subnetCIDRs {
		_, existingNet, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}
		if existingNet.Contains(candidate.IP) || candidate.Contains(existingNet.IP) {
			return true
		}
	}
	return false
}
