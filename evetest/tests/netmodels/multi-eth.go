// Copyright (c) 2026 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package netmodels

import (
	"github.com/lf-edge/eve/evetest"
	api "github.com/lf-edge/eve/evetest/grpcapi/go"
)

// TwoMgmtPorts is a network model with two ethernet ports, each on its own
// bridge and network with DHCP and access to the controller. Both are intended
// to be used as management ports on the EVE side.
var TwoMgmtPorts = &api.NetworkModel{
	Ports: []*api.Port{
		{
			LogicalLabel: "eth0",
			AdminUp:      true,
		},
		{
			LogicalLabel: "eth1",
			AdminUp:      true,
		},
	},
	Bridges: []*api.Bridge{
		{
			LogicalLabel: "bridge0",
			Ports:        []string{"eth0"},
		},
		{
			LogicalLabel: "bridge1",
			Ports:        []string{"eth1"},
		},
	},
	Networks: []*api.Network{
		{
			LogicalLabel: "network0",
			Bridge:       "bridge0",
			Ipv4: &api.NetworkIPConfig{
				Subnet: "172.20.20.0/24",
				GwIp:   "172.20.20.1",
				Dhcp: &api.DHCP{
					Enable:     true,
					DomainName: "test",
					Dns: &api.DNSClientConfig{
						PrivateDns: []string{"dns-server0"},
					},
				},
			},
		},
		{
			LogicalLabel: "network1",
			Bridge:       "bridge1",
			Ipv4: &api.NetworkIPConfig{
				Subnet: "172.20.21.0/24",
				GwIp:   "172.20.21.1",
				Dhcp: &api.DHCP{
					Enable:     true,
					DomainName: "test",
					Dns: &api.DNSClientConfig{
						PrivateDns: []string{"dns-server1"},
					},
				},
			},
		},
	},
	Endpoints: &api.Endpoints{
		DnsServers: []*api.DNSServer{
			{
				Endpoint: &api.Endpoint{
					LogicalLabel: "dns-server0",
					Fqdn:         "dns-server0.test",
					Ipv4: &api.EndpointIPConfig{
						Subnet: "10.16.16.0/24",
						Ip:     "10.16.16.25",
					},
				},
				StaticEntries: []*api.DNSEntry{
					{
						FqdnSource: &api.DNSEntry_FqdnLiteral{
							FqdnLiteral: evetest.GetControllerHostname(),
						},
						IpSource: &api.DNSEntry_IpLiteral{
							IpLiteral: evetest.GetControllerIPv4().String(),
						},
					},
					{
						FqdnSource: &api.DNSEntry_EndpointFqdnRef{
							EndpointFqdnRef: "http-server",
						},
						IpSource: &api.DNSEntry_EndpointIpRef{
							EndpointIpRef: &api.EndpointIPRef{
								LogicalLabel: "http-server",
								IpVersion:    api.IPVersion_IPV4,
							},
						},
					},
				},
				UpstreamServers: []string{"8.8.8.8", "1.1.1.1"},
			},
			{
				Endpoint: &api.Endpoint{
					LogicalLabel: "dns-server1",
					Fqdn:         "dns-server1.test",
					Ipv4: &api.EndpointIPConfig{
						Subnet: "10.16.17.0/24",
						Ip:     "10.16.17.25",
					},
				},
				StaticEntries: []*api.DNSEntry{
					{
						FqdnSource: &api.DNSEntry_FqdnLiteral{
							FqdnLiteral: evetest.GetControllerHostname(),
						},
						IpSource: &api.DNSEntry_IpLiteral{
							IpLiteral: evetest.GetControllerIPv4().String(),
						},
					},
					{
						FqdnSource: &api.DNSEntry_EndpointFqdnRef{
							EndpointFqdnRef: "http-server",
						},
						IpSource: &api.DNSEntry_EndpointIpRef{
							EndpointIpRef: &api.EndpointIPRef{
								LogicalLabel: "http-server",
								IpVersion:    api.IPVersion_IPV4,
							},
						},
					},
				},
				UpstreamServers: []string{"8.8.8.8", "1.1.1.1"},
			},
		},
		HttpServers: []*api.HTTPServer{
			{
				Endpoint: &api.Endpoint{
					LogicalLabel: "http-server",
					Fqdn:         "http-server.test",
					Ipv4: &api.EndpointIPConfig{
						Subnet: "10.17.17.0/24",
						Ip:     "10.17.17.25",
					},
				},
				HttpPort: 80,
				Paths: map[string]*api.HTTPContent{
					"/helloworld": {
						ContentType: "text/plain",
						Content:     "Hello world!",
					},
				},
			},
		},
	},
}

// TwoMgmtPortsOneBridge is a network model with two ethernet ports on a single
// bridge and network with DHCP and access to the controller. It is intended
// for bond (link aggregation) tests where both ports must reach the same network.
var TwoMgmtPortsOneBridge = &api.NetworkModel{
	Ports: []*api.Port{
		{
			LogicalLabel: "eth0",
			AdminUp:      true,
		},
		{
			LogicalLabel: "eth1",
			AdminUp:      true,
		},
	},
	Bridges: []*api.Bridge{
		{
			LogicalLabel: "bridge0",
			Ports:        []string{"eth0", "eth1"},
		},
	},
	Networks: []*api.Network{
		{
			LogicalLabel: "network0",
			Bridge:       "bridge0",
			Ipv4: &api.NetworkIPConfig{
				Subnet: "172.20.20.0/24",
				GwIp:   "172.20.20.1",
				Dhcp: &api.DHCP{
					Enable:     true,
					DomainName: "test",
					Dns: &api.DNSClientConfig{
						PrivateDns: []string{"dns-server"},
					},
				},
			},
		},
	},
	Endpoints: &api.Endpoints{
		DnsServers: []*api.DNSServer{
			{
				Endpoint: &api.Endpoint{
					LogicalLabel: "dns-server",
					Fqdn:         "dns-server.test",
					Ipv4: &api.EndpointIPConfig{
						Subnet: "10.16.16.0/24",
						Ip:     "10.16.16.25",
					},
				},
				StaticEntries: []*api.DNSEntry{
					{
						FqdnSource: &api.DNSEntry_FqdnLiteral{
							FqdnLiteral: evetest.GetControllerHostname(),
						},
						IpSource: &api.DNSEntry_IpLiteral{
							IpLiteral: evetest.GetControllerIPv4().String(),
						},
					},
					{
						FqdnSource: &api.DNSEntry_EndpointFqdnRef{
							EndpointFqdnRef: "http-server",
						},
						IpSource: &api.DNSEntry_EndpointIpRef{
							EndpointIpRef: &api.EndpointIPRef{
								LogicalLabel: "http-server",
								IpVersion:    api.IPVersion_IPV4,
							},
						},
					},
				},
				UpstreamServers: []string{"8.8.8.8", "1.1.1.1"},
			},
		},
		HttpServers: []*api.HTTPServer{
			{
				Endpoint: &api.Endpoint{
					LogicalLabel: "http-server",
					Fqdn:         "http-server.test",
					Ipv4: &api.EndpointIPConfig{
						Subnet: "10.17.17.0/24",
						Ip:     "10.17.17.25",
					},
				},
				HttpPort: 80,
				Paths: map[string]*api.HTTPContent{
					"/helloworld": {
						ContentType: "text/plain",
						Content:     "Hello world!",
					},
				},
			},
		},
	},
}

// TwoMgmtPortsWithLACPBond is a network model with two ethernet ports aggregated
// by an SDN-side LACP (802.3ad) bond. The bond is attached to a bridge with
// a DHCP network and access to the controller. It is intended for LACP bond
// tests where the SDN side must participate in LACP negotiation with EVE.
var TwoMgmtPortsWithLACPBond = &api.NetworkModel{
	Ports: []*api.Port{
		{
			LogicalLabel: "eth0",
			AdminUp:      true,
		},
		{
			LogicalLabel: "eth1",
			AdminUp:      true,
		},
	},
	Bonds: []*api.Bond{
		{
			LogicalLabel: "sdn-bond0",
			Ports:        []string{"eth0", "eth1"},
			Mode:         api.BondMode_BOND_MODE_802_3AD,
			LacpRate:     api.LacpRate_LACP_RATE_FAST,
			MiiMonitor: &api.BondMIIMonitor{
				Enabled:  true,
				Interval: 100,
			},
		},
	},
	Bridges: []*api.Bridge{
		{
			LogicalLabel: "bridge0",
			Bonds:        []string{"sdn-bond0"},
		},
	},
	Networks: []*api.Network{
		{
			LogicalLabel: "network0",
			Bridge:       "bridge0",
			Ipv4: &api.NetworkIPConfig{
				Subnet: "172.20.20.0/24",
				GwIp:   "172.20.20.1",
				Dhcp: &api.DHCP{
					Enable:     true,
					DomainName: "test",
					Dns: &api.DNSClientConfig{
						PrivateDns: []string{"dns-server"},
					},
				},
			},
		},
	},
	Endpoints: &api.Endpoints{
		DnsServers: []*api.DNSServer{
			{
				Endpoint: &api.Endpoint{
					LogicalLabel: "dns-server",
					Fqdn:         "dns-server.test",
					Ipv4: &api.EndpointIPConfig{
						Subnet: "10.16.16.0/24",
						Ip:     "10.16.16.25",
					},
				},
				StaticEntries: []*api.DNSEntry{
					{
						FqdnSource: &api.DNSEntry_FqdnLiteral{
							FqdnLiteral: evetest.GetControllerHostname(),
						},
						IpSource: &api.DNSEntry_IpLiteral{
							IpLiteral: evetest.GetControllerIPv4().String(),
						},
					},
					{
						FqdnSource: &api.DNSEntry_EndpointFqdnRef{
							EndpointFqdnRef: "http-server",
						},
						IpSource: &api.DNSEntry_EndpointIpRef{
							EndpointIpRef: &api.EndpointIPRef{
								LogicalLabel: "http-server",
								IpVersion:    api.IPVersion_IPV4,
							},
						},
					},
				},
				UpstreamServers: []string{"8.8.8.8", "1.1.1.1"},
			},
		},
		HttpServers: []*api.HTTPServer{
			{
				Endpoint: &api.Endpoint{
					LogicalLabel: "http-server",
					Fqdn:         "http-server.test",
					Ipv4: &api.EndpointIPConfig{
						Subnet: "10.17.17.0/24",
						Ip:     "10.17.17.25",
					},
				},
				HttpPort: 80,
				Paths: map[string]*api.HTTPContent{
					"/helloworld": {
						ContentType: "text/plain",
						Content:     "Hello world!",
					},
				},
			},
		},
	},
}

var SeparateClusterPort = &api.NetworkModel{
	Ports: []*api.Port{
		{
			LogicalLabel:  "dev1-eth0",
			AdminUp:       true,
			EveDeviceName: "edge-dev1",
		},
		{
			LogicalLabel:  "dev1-eth1",
			AdminUp:       true,
			EveDeviceName: "edge-dev1",
		},
		{
			LogicalLabel:  "dev2-eth0",
			AdminUp:       true,
			EveDeviceName: "edge-dev2",
		},
		{
			LogicalLabel:  "dev2-eth1",
			AdminUp:       true,
			EveDeviceName: "edge-dev2",
		},
		{
			LogicalLabel:  "dev3-eth0",
			AdminUp:       true,
			EveDeviceName: "edge-dev3",
		},
		{
			LogicalLabel:  "dev3-eth1",
			AdminUp:       true,
			EveDeviceName: "edge-dev3",
		},
	},
	Bridges: []*api.Bridge{
		{
			LogicalLabel: "bridge0",
			Ports:        []string{"dev1-eth0", "dev2-eth0", "dev3-eth0"},
		},
		{
			LogicalLabel: "bridge1",
			Ports:        []string{"dev1-eth1", "dev2-eth1", "dev3-eth1"},
		},
	},
	Networks: []*api.Network{
		{
			LogicalLabel: "mgmt-and-app-network",
			Bridge:       "bridge0",
			Ipv4: &api.NetworkIPConfig{
				Subnet: "172.20.20.0/24",
				GwIp:   "172.20.20.1",
				Dhcp: &api.DHCP{
					Enable:     true,
					DomainName: "test",
					Dns: &api.DNSClientConfig{
						PrivateDns: []string{"dns-server"},
					},
				},
			},
		},
		{
			LogicalLabel: "cluster-network",
			Bridge:       "bridge1",
			Ipv4: &api.NetworkIPConfig{
				Subnet: "10.244.244.0/24",
				GwIp:   "10.244.244.1",
			},
			Router: &api.Router{
				OutsideReachability: false,
			},
		},
	},
	Endpoints: &api.Endpoints{
		DnsServers: []*api.DNSServer{
			{
				Endpoint: &api.Endpoint{
					LogicalLabel: "dns-server",
					Fqdn:         "dns-server.test",
					Ipv4: &api.EndpointIPConfig{
						Subnet: "10.16.16.0/24",
						Ip:     "10.16.16.25",
					},
				},
				StaticEntries: []*api.DNSEntry{
					{
						FqdnSource: &api.DNSEntry_FqdnLiteral{
							FqdnLiteral: evetest.GetControllerHostname(),
						},
						IpSource: &api.DNSEntry_IpLiteral{
							IpLiteral: evetest.GetControllerIPv4().String(),
						},
					},
					{
						FqdnSource: &api.DNSEntry_EndpointFqdnRef{
							EndpointFqdnRef: "http-server",
						},
						IpSource: &api.DNSEntry_EndpointIpRef{
							EndpointIpRef: &api.EndpointIPRef{
								LogicalLabel: "http-server",
								IpVersion:    api.IPVersion_IPV4,
							},
						},
					},
				},
				UpstreamServers: []string{"8.8.8.8", "1.1.1.1"},
			},
		},
		// This HTTP server can be used as a target for application connectivity testing.
		HttpServers: []*api.HTTPServer{
			{
				Endpoint: &api.Endpoint{
					LogicalLabel: "http-server",
					Fqdn:         "http-server.test",
					Ipv4: &api.EndpointIPConfig{
						Subnet: "10.17.17.0/24",
						Ip:     "10.17.17.25",
					},
				},
				HttpPort: 80,
				Paths: map[string]*api.HTTPContent{
					"/helloworld": {
						ContentType: "text/plain",
						Content:     "Hello world!",
					},
				},
			},
		},
	},
}
