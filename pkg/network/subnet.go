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

//go:build linux && cgo

package network

import (
	"encoding/xml"
	"fmt"
	"net"
	"strings"

	"libvirt.org/go/libvirt"

	"github.com/day0ops/lok8s/pkg/config"
	"github.com/day0ops/lok8s/pkg/logger"
)

// Interface contains main network interface parameters
type Interface struct {
	IfaceName string
	IfaceIPv4 string
	IfaceMTU  int
	IfaceMAC  string
}

// Parameters contains main network parameters
type Parameters struct {
	IP        string // IP address of network
	Netmask   string // dotted-decimal format ('a.b.c.d')
	Prefix    int    // network prefix length (number of leading ones in network mask)
	CIDR      string // CIDR format ('a.b.c.d/n')
	Gateway   string // taken from network interface address or assumed as first network IP address from given addr
	ClientMin string // first available client IP address after gateway
	ClientMax string // last available client IP address before broadcast
	Broadcast string // last network IP address
	IsPrivate bool   // whether the IP is private or not
	Interface
}

// libvirtNetworkXML represents the structure of a libvirt network XML
type libvirtNetworkXML struct {
	XMLName xml.Name           `xml:"network"`
	IP      []libvirtIPElement `xml:"ip"`
}

// libvirtIPElement represents an IP element in libvirt network XML
type libvirtIPElement struct {
	Address string `xml:"address,attr"`
	Prefix  string `xml:"prefix,attr"`
	Netmask string `xml:"netmask,attr"`
}

// FindFreeLibvirtSubnet finds a free subnet starting from the given subnet by checking libvirt networks
// returns the CIDR of the free subnet found, or error if none found
func FindFreeLibvirtSubnet(startSubnet string, step, tries int) (string, error) {
	currSubnet := startSubnet
	for try := 0; try < tries; try++ {
		// parse current subnet
		_, ipNet, err := net.ParseCIDR(currSubnet)
		if err != nil {
			return "", fmt.Errorf("failed to parse subnet %s: %w", currSubnet, err)
		}

		// check if subnet overlaps with existing libvirt networks
		if err := checkLibvirtSubnetOverlap(ipNet); err == nil {
			// no overlap found - subnet is free
			logger.Debugf("found free subnet %s", currSubnet)
			return currSubnet, nil
		} else if strings.Contains(err.Error(), "overlaps") {
			// subnet is taken, try next one
			logger.Debugf("subnet %s is taken: %v", currSubnet, err)
		} else {
			// error checking (e.g., libvirt not available), assume subnet is free
			logger.Debugf("could not check subnet %s, assuming free: %v", currSubnet, err)
			return currSubnet, nil
		}

		// calculate next subnet to try
		prefix, _ := ipNet.Mask.Size()
		nextIP := net.ParseIP(ipNet.IP.String()).To4()
		if nextIP == nil {
			return "", fmt.Errorf("invalid IPv4 subnet: %s", currSubnet)
		}

		if prefix <= 16 {
			nextIP[1] += byte(step)
		} else {
			nextIP[2] += byte(step)
		}

		// construct next subnet CIDR
		currSubnet = fmt.Sprintf("%s/%d", nextIP.String(), prefix)
	}

	return "", fmt.Errorf("no free subnet found after %d tries starting from %s", tries, startSubnet)
}

// checkLibvirtSubnetOverlap checks if the given subnet overlaps with any existing libvirt network
// returns nil if subnet is free (no overlap), error if subnet overlaps with existing network
func checkLibvirtSubnetOverlap(ipNet *net.IPNet) error {
	conn, err := getLibvirtConnection(config.MinikubeQemuSystem)
	if err != nil {
		return fmt.Errorf("failed to connect to libvirt: %w", err)
	}
	defer func() {
		if _, err := conn.Close(); err != nil {
			logger.Debugf("failed closing libvirt connection: %v", lvErr(err))
		}
	}()

	// get all networks
	nets, err := conn.ListAllNetworks(0)
	if err != nil {
		return fmt.Errorf("failed to list libvirt networks: %w", err)
	}

	for _, libvirtNet := range nets {
		defer func(net libvirt.Network) {
			if err := net.Free(); err != nil {
				logger.Warnf("failed freeing network: %v", err)
			}
		}(libvirtNet)

		// get network XML to extract subnet
		xmlDesc, err := libvirtNet.GetXMLDesc(0)
		if err != nil {
			logger.Debugf("failed to get network XML: %v", err)
			continue
		}

		// unmarshal network XML to extract IP configuration
		var networkXML libvirtNetworkXML
		if err := xml.Unmarshal([]byte(xmlDesc), &networkXML); err != nil {
			logger.Debugf("failed to unmarshal network XML: %v", err)
			continue
		}

		// process each IP element in the network
		for _, ipElem := range networkXML.IP {
			if ipElem.Address == "" {
				continue
			}

			netIPAddr := net.ParseIP(ipElem.Address)
			if netIPAddr == nil {
				continue
			}

			// determine prefix length from prefix or netmask attribute
			var prefixLen int
			if ipElem.Prefix != "" {
				if _, err := fmt.Sscanf(ipElem.Prefix, "%d", &prefixLen); err != nil {
					logger.Debugf("failed to parse prefix %s: %v", ipElem.Prefix, err)
					continue
				}
			} else if ipElem.Netmask != "" {
				netmaskIP := net.ParseIP(ipElem.Netmask)
				if netmaskIP != nil {
					ones, _ := net.IPMask(netmaskIP.To4()).Size()
					prefixLen = ones
				} else {
					logger.Debugf("failed to parse netmask %s", ipElem.Netmask)
					continue
				}
			} else {
				// no prefix or netmask specified, default to /24
				prefixLen = 24
			}

			if prefixLen == 0 {
				// default to /24 if we can't determine prefix
				prefixLen = 24
			}

			// create network from extracted IP and prefix
			_, existingNet, err := net.ParseCIDR(fmt.Sprintf("%s/%d", ipElem.Address, prefixLen))
			if err != nil {
				logger.Debugf("failed to parse CIDR %s/%d: %v", ipElem.Address, prefixLen, err)
				continue
			}

			// check if networks overlap
			if existingNet.Contains(ipNet.IP) || ipNet.Contains(existingNet.IP) {
				return fmt.Errorf("subnet %s overlaps with existing libvirt network %s", ipNet.String(), existingNet.String())
			}
		}
	}

	// no overlap found - subnet is free
	return nil
}

// getLibvirtConnection establishes a libvirt connection
// this is a helper function for subnet checking
func getLibvirtConnection(connectionURI string) (*libvirt.Connect, error) {
	conn, err := libvirt.NewConnect(connectionURI)
	if err != nil {
		return nil, fmt.Errorf("failed connecting to libvirt socket: %w", lvErr(err))
	}

	return conn, nil
}
