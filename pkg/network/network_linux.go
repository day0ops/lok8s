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
	"bytes"
	"fmt"
	"net"
	"strings"
	"text/template"
	"time"

	"github.com/pkg/errors"
	"libvirt.org/go/libvirt"

	"github.com/day0ops/lok8s/pkg/config"
	"github.com/day0ops/lok8s/pkg/logger"
	"github.com/day0ops/lok8s/pkg/util"
)

// libvirtNetwork represents the template data for libvirt network XML
type libvirtNetwork struct {
	Name   string
	Bridge string
	Parameters
}

// PrerequisiteChecks check if all the required pre-reqs are present
func (n *Network) PrerequisiteChecks() bool {
	return true
}

// EnsureNetwork creates or ensures the network exists and is active
func (n *Network) EnsureNetwork() error {
	status := logger.NewStatus()
	status.Start(fmt.Sprintf("ensuring network %s", n.Name))
	defer status.End(true)

	conn, err := getConnection(n.ConnectionURI)
	if err != nil {
		status.End(false)
		return fmt.Errorf("failed opening libvirt connection: %w", err)
	}
	defer func() {
		if _, err := conn.Close(); err != nil {
			logger.Errorf("failed closing libvirt connection: %v", lvErr(err))
		}
	}()

	logger.Debugf("ensuring network %s is active", n.Name)

	// check if network exists
	libvirtNet, err := conn.LookupNetworkByName(n.Name)
	if err != nil {
		// network doesn't exist, create it
		logger.Debugf("network %s does not exist, creating it", n.Name)
		if err := n.createNetwork(); err != nil {
			status.End(false)
			return errors.Wrapf(err, "creating network %s", n.Name)
		}
		// ensure it's set up (autostart and active)
		if err := setupNetwork(conn, n.Name); err != nil {
			status.End(false)
			return errors.Wrapf(err, "setting up network %s", n.Name)
		}
		logger.Debugf("successfully created and activated network %s", n.Name)
		return nil
	}

	// network exists, free the handle (setupNetwork will look it up again)
	if err := libvirtNet.Free(); err != nil {
		logger.Debugf("failed freeing network handle: %v", lvErr(err))
	}

	// ensure autostart is enabled and it's active
	if err := setupNetwork(conn, n.Name); err != nil {
		status.End(false)
		return errors.Wrapf(err, "setting up existing network %s", n.Name)
	}

	return nil
}

// createNetwork creates a new libvirt network
func (n *Network) createNetwork() error {
	if n.Name == config.MinikubeLibvirtPvtNetworkName {
		return fmt.Errorf("network can't be named %s. This is the name of the private network created by minikube by default", config.MinikubeLibvirtPvtNetworkName)
	}
	conn, err := getConnection(n.ConnectionURI)
	if err != nil {
		return fmt.Errorf("failed opening libvirt connection: %w", err)
	}
	defer func() {
		if _, err := conn.Close(); err != nil {
			logger.Errorf("failed closing libvirt connection: %v", lvErr(err))
		}
	}()

	// check if network already exists
	if netp, err := conn.LookupNetworkByName(n.Name); err == nil {
		logger.Warnf("found existing %s network, skipping creation", n.Name)

		if netXML, err := netp.GetXMLDesc(0); err != nil {
			logger.Debugf("failed getting %s network XML: %v", n.Name, lvErr(err))
		} else {
			logger.Debug(netXML)
		}

		if err := netp.Free(); err != nil {
			logger.Errorf("failed freeing %s network: %v", n.Name, lvErr(err))
		}
		return nil
	}

	// check if subnet is free and find a free subnet if needed (libvirt-specific)
	initialSubnet := n.Subnet
	var freeSubnetCIDR string
	freeSubnetCIDR, err = FindFreeLibvirtSubnet(n.Subnet, 1, 50)
	if err != nil {
		return fmt.Errorf("failed to find free subnet starting from %s: %w", n.Subnet, err)
	}

	// update subnet if a different free subnet was found
	if freeSubnetCIDR != initialSubnet {
		logger.Infof("subnet %s is in use, using free subnet %s instead", initialSubnet, freeSubnetCIDR)
		n.Subnet = freeSubnetCIDR
	}

	// parse subnet to get network parameters
	_, ipNet, err := net.ParseCIDR(n.Subnet)
	if err != nil {
		return fmt.Errorf("invalid subnet CIDR format %s: %w", n.Subnet, err)
	}

	// calculate network parameters from the subnet CIDR
	subnet := calculateSubnetParameters(ipNet)

	// create the XML for the private network from our networkTmpl
	tryNet := libvirtNetwork{
		Name:       n.Name,
		Bridge:     n.Bridge,
		Parameters: subnet,
	}
	tmpl := template.Must(template.New("network").Parse(config.NetworkTemplate))
	var networkXML bytes.Buffer
	if err = tmpl.Execute(&networkXML, tryNet); err != nil {
		return fmt.Errorf("executing private network template: %w", err)
	}

	// define and create the network with retry logic
	logger.Debugf("generated network template as XML:\n%s", networkXML.String())

	createFunc := func() error {
		// define the network using our template
		libvirtNet, err := conn.NetworkDefineXML(networkXML.String())
		if err != nil {
			return fmt.Errorf("defining network %s %s from xml: %w", n.Name, subnet.CIDR, err)
		}

		// create and start the network
		logger.Debugf("creating network %s %s...", n.Name, subnet.CIDR)
		if err = libvirtNet.Create(); err != nil {
			// Free the network handle if creation failed
			_ = libvirtNet.Free()
			return fmt.Errorf("creating network %s %s: %w", n.Name, subnet.CIDR, err)
		}

		// network created successfully
		logger.Debugf("network %s %s created", n.Name, subnet.CIDR)
		return nil
	}

	// retry network creation with exponential backoff (up to 30 seconds)
	if err := util.LocalRetry(createFunc, 30*time.Second); err != nil {
		return err
	}

	// verify network was created
	libvirtNet, err := conn.LookupNetworkByName(n.Name)
	if err != nil {
		return fmt.Errorf("network %s was not created successfully: %w", n.Name, err)
	}
	defer func() {
		if err := libvirtNet.Free(); err != nil {
			logger.Warnf("failed freeing network %s: %v", n.Name, lvErr(err))
		}
	}()

	logger.Debugf("network %s %s created", n.Name, subnet.CIDR)
	if netXML, err := libvirtNet.GetXMLDesc(0); err != nil {
		logger.Debugf("failed getting %s network XML: %v", n.Name, lvErr(err))
	} else {
		logger.Debugf("dumping network information as XML:\n%s", netXML)
	}

	return nil
}

// calculateSubnetParameters calculates network parameters from a CIDR subnet
func calculateSubnetParameters(ipNet *net.IPNet) Parameters {
	ones, _ := ipNet.Mask.Size()
	ip := ipNet.IP.To4() // ensure IPv4
	if ip == nil {
		ip = ipNet.IP // fallback to original if not IPv4
	}

	gateway := make(net.IP, len(ip))
	copy(gateway, ip)
	gateway[len(gateway)-1]++ // gateway is first IP

	// calculate broadcast
	broadcast := make(net.IP, len(ip))
	copy(broadcast, ip)
	for i := range broadcast {
		broadcast[i] |= ^ipNet.Mask[i]
	}

	// client range: gateway + 1 to broadcast - 1
	clientMin := make(net.IP, len(gateway))
	copy(clientMin, gateway)
	clientMin[len(clientMin)-1]++

	clientMax := make(net.IP, len(broadcast))
	copy(clientMax, broadcast)
	clientMax[len(clientMax)-1]--

	// reserve last client IP address for multi-control-plane loadbalancer VIP address in HA cluster
	clientMax[len(clientMax)-1]--

	// convert netmask to dotted decimal format
	netmask := fmt.Sprintf("%d.%d.%d.%d", ipNet.Mask[0], ipNet.Mask[1], ipNet.Mask[2], ipNet.Mask[3])

	return Parameters{
		IP:        ip.String(),
		Netmask:   netmask,
		Prefix:    ones,
		CIDR:      ipNet.String(),
		Gateway:   gateway.String(),
		ClientMin: clientMin.String(),
		ClientMax: clientMax.String(),
		Broadcast: broadcast.String(),
		IsPrivate: isPrivateIP(ip),
	}
}

// isPrivateIP checks if an IP address is in a private network range
func isPrivateIP(ip net.IP) bool {
	privateRanges := []*net.IPNet{
		{IP: net.IPv4(10, 0, 0, 0), Mask: net.CIDRMask(8, 32)},
		{IP: net.IPv4(172, 16, 0, 0), Mask: net.CIDRMask(12, 32)},
		{IP: net.IPv4(192, 168, 0, 0), Mask: net.CIDRMask(16, 32)},
	}
	for _, r := range privateRanges {
		if r.Contains(ip) {
			return true
		}
	}
	return false
}

// DeleteNetwork deletes the libvirt network
func (n *Network) DeleteNetwork(force bool) error {
	status := logger.NewStatus()
	status.Start(fmt.Sprintf("deleting network %s", n.Name))
	defer status.End(true)

	conn, err := getConnection(n.ConnectionURI)
	if err != nil {
		status.End(false)
		return fmt.Errorf("failed opening libvirt connection: %w", err)
	}
	defer func() {
		if _, err := conn.Close(); err != nil {
			logger.Errorf("failed closing libvirt connection: %v", lvErr(err))
		}
	}()

	logger.Debugf("checking if network %s exists...", n.Name)
	libvirtNet, err := conn.LookupNetworkByName(n.Name)
	if err != nil {
		// check if error indicates network doesn't exist
		// libvirt returns Code=43 or Code=50 when network is not found
		errStr := err.Error()

		// first, try to cast to libvirt.Error to check error codes
		if lverr, ok := err.(libvirt.Error); ok {
			// check for network not found error codes (43 or 50)
			if lverr.Code == 43 || lverr.Code == 50 {
				logger.Debugf("network %s does not exist (error code %d). Skipping deletion", n.Name, lverr.Code)
				status.End(true) // Success - network already doesn't exist
				return nil
			}
			// also check the error message for network not found patterns
			if len(lverr.Message) > 0 && (strings.Contains(strings.ToLower(lverr.Message), "network not found") || strings.Contains(lverr.Message, "Network not found")) {
				logger.Debugf("network %s does not exist (message: %s). Skipping deletion", n.Name, lverr.Message)
				status.End(true) // Success - network already doesn't exist
				return nil
			}
		}

		// fallback: check error string for network not found patterns (case-insensitive)
		errStrLower := strings.ToLower(errStr)
		if strings.Contains(errStrLower, "network not found") ||
			strings.Contains(errStr, "Code=43") ||
			strings.Contains(errStr, "Code=50") ||
			strings.Contains(errStrLower, "no network with matching name") {
			logger.Debugf("network %s does not exist (error: %s). Skipping deletion", n.Name, errStr)
			status.End(true) // Success - network already doesn't exist
			return nil
		}
		status.End(false)
		return errors.Wrapf(err, "failed looking up network %s", n.Name)
	}
	defer func() {
		if libvirtNet == nil {
			logger.Warnf("nil network, cannot free")
		} else if err := libvirtNet.Free(); err != nil {
			logger.Errorf("failed freeing %s network: %v", n.Name, lvErr(err))
		}
	}()

	logger.Debugf("network %s exists", n.Name)

	// delete the network
	logger.Debugf("trying to delete network %s...", n.Name)
	deleteFunc := func() error {
		active, err := libvirtNet.IsActive()
		if err != nil {
			return err
		}
		if active {
			logger.Debugf("destroying active network %s", n.Name)
			if err := libvirtNet.Destroy(); err != nil {
				return err
			}
		}
		logger.Debugf("undefining inactive network %s", n.Name)
		return libvirtNet.Undefine()
	}
	if err := util.LocalRetry(deleteFunc, 10*time.Second); err != nil {
		status.End(false)
		return errors.Wrap(err, "deleting network")
	}
	logger.Debugf("network %s deleted", n.Name)

	return nil
}

// setupNetwork ensures the network is active and has autostart enabled
func setupNetwork(conn *libvirt.Connect, name string) error {
	n, err := conn.LookupNetworkByName(name)
	if err != nil {
		return fmt.Errorf("failed looking up network %s: %w", name, lvErr(err))
	}
	defer func() {
		if n == nil {
			logger.Warn("nil network, cannot free")
		} else if err := n.Free(); err != nil {
			logger.Errorf("failed freeing %s network: %v", name, lvErr(err))
		}
	}()

	// always ensure autostart is set on the network
	autostart, err := n.GetAutostart()
	if err != nil {
		return errors.Wrapf(err, "checking network %s autostart", name)
	}
	if !autostart {
		if err := n.SetAutostart(true); err != nil {
			return errors.Wrapf(err, "setting autostart for network %s", name)
		}
	}

	// always ensure the network is started (active)
	active, err := n.IsActive()
	if err != nil {
		return errors.Wrapf(err, "checking network status for %s", name)
	}

	if !active {
		logger.Debugf("network %s is not active, trying to start it...", name)
		if err := n.Create(); err != nil {
			return errors.Wrapf(err, "starting network %s", name)
		}
	}
	return nil
}

// getConnection establishes a libvirt connection
func getConnection(connectionURI string) (*libvirt.Connect, error) {
	conn, err := libvirt.NewConnect(connectionURI)
	if err != nil {
		return nil, fmt.Errorf("failed connecting to libvirt socket: %w", lvErr(err))
	}

	return conn, nil
}

// lvErr converts error to libvirt Error struct
func lvErr(err error) libvirt.Error {
	if err != nil {
		if lverr, ok := err.(libvirt.Error); ok {
			return lverr
		}
		return libvirt.Error{Code: libvirt.ERR_INTERNAL_ERROR, Message: "internal error"}
	}
	return libvirt.Error{Code: libvirt.ERR_OK, Message: ""}
}
