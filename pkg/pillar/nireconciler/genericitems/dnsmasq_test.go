// Copyright (c) 2023 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package genericitems_test

import (
	"bytes"
	"net"
	"regexp"
	"testing"

	"github.com/lf-edge/eve/pkg/pillar/base"
	"github.com/lf-edge/eve/pkg/pillar/nireconciler/genericitems"
	"github.com/sirupsen/logrus"
)

var (
	log          *base.LogObject
	logger       *logrus.Logger
	configurator *genericitems.DnsmasqConfigurator
)

func init() {
	logger = logrus.StandardLogger()
	log = base.NewSourceLogObject(logger, "nireconciler", 1234)
	configurator = &genericitems.DnsmasqConfigurator{
		Log:    log,
		Logger: logger,
	}
}

func exampleDnsmasqParams() genericitems.Dnsmasq {
	var dnsmasq genericitems.Dnsmasq
	dnsmasq.InstanceName = "br0"
	dnsmasq.ListenIf.IfName = "br0"
	_, subnet, _ := net.ParseCIDR("10.0.0.0/24")
	dnsmasq.DHCPServer = genericitems.DHCPServer{
		Subnet:         subnet,
		AllOnesNetmask: true,
		IPRange: genericitems.IPRange{
			FromIP: net.IP{10, 0, 0, 2},
			ToIP:   net.IP{10, 0, 0, 123},
		},
		GatewayIP:  net.IP{192, 168, 1, 1},
		DNSServers: []net.IP{{10, 0, 0, 1}, {1, 1, 1, 1}},
		NTPServers: []net.IP{{94, 130, 35, 4}, {94, 16, 114, 254}},
		StaticEntries: []genericitems.MACToIP{
			{
				MAC:      net.HardwareAddr{0x02, 0x00, 0x00, 0xA, 0xA, 0xB},
				IP:       net.IP{10, 0, 0, 5},
				Hostname: "app1",
			},
		},
	}
	dnsmasq.DNSServer = genericitems.DNSServer{
		ListenIP: net.IP{10, 0, 0, 1},
		UplinkIf: genericitems.NetworkIf{
			IfName: "eth0",
		},
		UpstreamServers: []net.IP{{1, 1, 1, 1}, {141, 1, 1, 1}, {208, 67, 220, 220}},
		StaticEntries: []genericitems.HostnameToIP{
			{
				Hostname: "router",
				IP:       net.IP{10, 0, 0, 1},
			},
			{
				Hostname: "app1",
				IP:       net.IP{10, 0, 0, 5},
			},
		},
		LinuxIPSets: []genericitems.LinuxIPSet{
			{
				Domains: []string{"zededa.com"},
				Sets:    []string{"ipv4.zededa.com", "ipv6.zededa.com"},
			},
			{
				Domains: []string{"example.com"},
				Sets:    []string{"ipv4.example.com", "ipv6.example.com"},
			},
		},
	}
	return dnsmasq
}

func createDnsmasqConfig(dnsmasq genericitems.Dnsmasq) string {
	var buf bytes.Buffer
	err := configurator.CreateDnsmasqConfig(&buf, dnsmasq)
	if err != nil {
		panic(err)
	}
	return buf.String()
}

func TestCreateDnsmasqConfigWithDhcpRangeEnd(t *testing.T) {
	t.Parallel()

	dnsmasq := exampleDnsmasqParams()
	config := createDnsmasqConfig(dnsmasq)

	configExpected := `
# Automatically generated by zedrouter
except-interface=lo
bind-interfaces
quiet-dhcp
quiet-dhcp6
no-hosts
no-ping
bogus-priv
neg-ttl=10
dhcp-ttl=600
dhcp-leasefile=/run/zedrouter/dnsmasq.leases/br0
server=1.1.1.1@eth0
server=141.1.1.1@eth0
server=208.67.220.220@eth0
no-resolv
ipset=/zededa.com/ipv4.zededa.com,ipv6.zededa.com
ipset=/example.com/ipv4.example.com,ipv6.example.com
pid-file=/run/zedrouter/dnsmasq.br0.pid
interface=br0
listen-address=10.0.0.1
hostsdir=/run/zedrouter/hosts.br0
dhcp-hostsdir=/run/zedrouter/dhcp-hosts.br0
dhcp-option=option:dns-server,10.0.0.1,1.1.1.1
dhcp-option=option:ntp-server,94.130.35.4,94.16.114.254
dhcp-option=option:netmask,255.255.255.255
dhcp-option=option:router,192.168.1.1
dhcp-option=option:classless-static-route,192.168.1.1/32,0.0.0.0,0.0.0.0/0,192.168.1.1,10.0.0.0/24,192.168.1.1
dhcp-range=10.0.0.2,10.0.0.123,255.255.255.255,60m
`
	if configExpected != config {
		t.Fatalf("expected '%s', but got '%s'", configExpected, config)
	}
}

func TestCreateDnsmasqConfigWithoutDhcpRangeEnd(t *testing.T) {
	t.Parallel()

	dnsmasq := exampleDnsmasqParams()
	dnsmasq.DHCPServer.IPRange.ToIP = nil
	config := createDnsmasqConfig(dnsmasq)

	dhcpRangeRex := "(?m)^dhcp-range=10.0.0.2,static,255.255.255.255,60m$"
	ok, err := regexp.MatchString(dhcpRangeRex, config)
	if err != nil {
		panic(err)
	}
	if !ok {
		t.Fatalf("expected to match '%s', but got '%s'", dhcpRangeRex, config)
	}
}

func TestCreateDnsmasqConfigWithoutGateway(t *testing.T) {
	t.Parallel()

	dnsmasq := exampleDnsmasqParams()
	dnsmasq.DHCPServer.GatewayIP = nil
	config := createDnsmasqConfig(dnsmasq)

	routerRex := "(?m)^dhcp-option=option:router$"
	ok, err := regexp.MatchString(routerRex, config)
	if err != nil {
		panic(err)
	}
	if !ok {
		t.Fatalf("expected to match '%s', but got '%s'", routerRex, config)
	}

	dhcpRangeRex := "(?m)^dhcp-range=10.0.0.2,10.0.0.123,255.255.255.0,60m$"
	ok, err = regexp.MatchString(dhcpRangeRex, config)
	if err != nil {
		panic(err)
	}
	if !ok {
		t.Fatalf("expected to match '%s', but got '%s'", dhcpRangeRex, config)
	}
}

func TestCreateDnsmasqConfigWithDisabledAllOnesNetmask(t *testing.T) {
	t.Parallel()

	dnsmasq := exampleDnsmasqParams()
	dnsmasq.DHCPServer.AllOnesNetmask = false
	config := createDnsmasqConfig(dnsmasq)

	dhcpRangeRex := "(?m)^dhcp-range=10.0.0.2,10.0.0.123,255.255.255.0,60m$"
	ok, err := regexp.MatchString(dhcpRangeRex, config)
	if err != nil {
		panic(err)
	}
	if !ok {
		t.Fatalf("expected to match '%s', but got '%s'", dhcpRangeRex, config)
	}
}

func TestRunDnsmasqInvalidDhcpRange(t *testing.T) {
	t.Parallel()
	line, err := configurator.CreateDHCPv4RangeConfig(nil, nil)
	if err != nil {
		panic(err)
	}
	if line != "" {
		t.Fatalf("dhcp-range is '%s', expected ''", line)
	}
	line, err = configurator.CreateDHCPv4RangeConfig(net.IP{10, 0, 0, 5}, net.IP{10, 0, 0, 3})
	if err == nil {
		t.Fatalf("expected dhcp range to fail, but got %s", line)
	}
}
