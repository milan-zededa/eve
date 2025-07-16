// A set of commonly-used Eden-SDN network model examples that tests can pick from
// instead of defining their own.

package netmodels

import (
	"github.com/lf-edge/eve/evetest"
	api "github.com/lf-edge/eve/evetest/grpcapi/go"
)

var SingleEthWithDHCP = &api.NetworkModel{
	Ports: []*api.Port{
		{
			LogicalLabel: "eth0",
			AdminUp:      true,
		},
	},
	Bridges: []*api.Bridge{
		{
			LogicalLabel: "bridge0",
			Ports:        []string{"eth0"},
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
				},
				UpstreamServers: []string{"8.8.8.8", "1.1.1.1"},
			},
		},
	},
}

var SingleEthWithDHCPAndIPv6 = &api.NetworkModel{
	Ports: []*api.Port{
		{
			LogicalLabel: "eth0",
			AdminUp:      true,
		},
	},
	Bridges: []*api.Bridge{
		{
			LogicalLabel: "bridge0",
			Ports:        []string{"eth0"},
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
			Ipv6: &api.NetworkIPConfig{
				Subnet: "fd3f:89fd:78c5::/64",
				GwIp:   "fd3f:89fd:78c5::1",
				Dhcp: &api.DHCP{
					Enable: true,
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
					Ipv6: &api.EndpointIPConfig{
						Subnet: "fd23:131b:6500::/64",
						Ip:     "fd23:131b:6500::1",
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
						FqdnSource: &api.DNSEntry_FqdnLiteral{
							FqdnLiteral: evetest.GetControllerHostname(),
						},
						IpSource: &api.DNSEntry_IpLiteral{
							IpLiteral: evetest.GetControllerIPv6().String(),
						},
					},
				},
				UpstreamServers: []string{
					"8.8.8.8",              // Google DNS (IPv4)
					"2001:4860:4860::8888", // Google DNS (IPv6)
					"1.1.1.1",              // Cloudflare DNS (IPv4)
					"2606:4700:4700::1111", // Cloudflare DNS (IPv6)
				},
			},
		},
	},
}

var SingleEthWithoutDHCP = &api.NetworkModel{
	Ports: []*api.Port{
		{
			LogicalLabel: "eth0",
			AdminUp:      true,
		},
	},
	Bridges: []*api.Bridge{
		{
			LogicalLabel: "bridge0",
			Ports:        []string{"eth0"},
		},
	},
	Networks: []*api.Network{
		{
			LogicalLabel: "network0",
			Bridge:       "bridge0",
			Ipv4: &api.NetworkIPConfig{
				Subnet: "172.20.20.0/24",
				GwIp:   "172.20.20.1",
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
				},
				UpstreamServers: []string{"8.8.8.8", "1.1.1.1"},
			},
		},
	},
}

var (
	ProxyCACertPEM = "-----BEGIN CERTIFICATE-----\n" +
		"MIIDVTCCAj2gAwIBAgIUPGtlx1k08RmWd9RxiCKTXYnAUkIwDQYJKoZIhvcNAQEL\n" +
		"BQAwOjETMBEGA1UEAwwKemVkZWRhLmNvbTELMAkGA1UEBhMCVVMxFjAUBgNVBAcM\n" +
		"DVNhbiBGcmFuY2lzY28wHhcNMjIwOTA3MTcwMDE0WhcNMzIwNjA2MTcwMDE0WjA6\n" +
		"MRMwEQYDVQQDDAp6ZWRlZGEuY29tMQswCQYDVQQGEwJVUzEWMBQGA1UEBwwNU2Fu\n" +
		"IEZyYW5jaXNjbzCCASIwDQYJKoZIhvcNAQEBBQADggEPADCCAQoCggEBALQsi7IG\n" +
		"M8KApujL71MJXbuPQNn/g+RItQeehaFRcqcCcpFW4k1YveMNdf5HReKlAfufFtaa\n" +
		"IF368t33UlleblopLM8m8r9Ev1sSJOS1yYgU1HABjyw54LXBqT4tAf0xjlRaLn4L\n" +
		"QBUAS0TTywTppGXtNwXpxqdDuQdigNskqzEFaGI52IQezfGt7L2CeeJ/YJNcbImR\n" +
		"eCXMPwTatUHLLE29Qv8GQQfy7TpCXdXVLvQAyfZJi7lY7DjPqBab5ocnVTRcEpKz\n" +
		"FwH2+KTokQkU1UF614IveRF3ZOqqmrQvy1AdSvekFLIz2uP7xsfy3I3HNQcPJ4DI\n" +
		"5vNzBaE/hF5xK40CAwEAAaNTMFEwHQYDVR0OBBYEFPxOB5cxsf89x6KdFSTTFV2L\n" +
		"wta1MB8GA1UdIwQYMBaAFPxOB5cxsf89x6KdFSTTFV2Lwta1MA8GA1UdEwEB/wQF\n" +
		"MAMBAf8wDQYJKoZIhvcNAQELBQADggEBAFXqCJuq4ifMw3Hre7+X23q25jOb1nzd\n" +
		"8qs+1Tij8osUC5ekD21x/k9g+xHvacoJIOzsAmpAPSnwXKMnvVdAeX6Scg1Bvejj\n" +
		"TdXfNEJ7jcvDROUNjlWYjwiY+7ahDkj56nahwGjjUQdgCCzRiSYPOq6N1tRkn97a\n" +
		"i6+jB8DnTSDnv5j8xiPDbWJ+nv2O1NNsoHS91UrTqkVXxNItrCdPPh21hzrTJxs4\n" +
		"oSf4wbaF5n3E2cPpSAaXBEyxBdXAqUCIhP0q9/pgBTYuJ+eW467u4xWqUVi4iBtN\n" +
		"wVfYelYC2v03Rn433kv624oJDQ7MM5bDUv3nqPtkUys0ARwxs8tQCgg=\n" +
		"-----END CERTIFICATE-----"
	ProxyCAKeyPEM = "-----BEGIN PRIVATE KEY-----\n" +
		"MIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQC0LIuyBjPCgKbo\n" +
		"y+9TCV27j0DZ/4PkSLUHnoWhUXKnAnKRVuJNWL3jDXX+R0XipQH7nxbWmiBd+vLd\n" +
		"91JZXm5aKSzPJvK/RL9bEiTktcmIFNRwAY8sOeC1wak+LQH9MY5UWi5+C0AVAEtE\n" +
		"08sE6aRl7TcF6canQ7kHYoDbJKsxBWhiOdiEHs3xrey9gnnif2CTXGyJkXglzD8E\n" +
		"2rVByyxNvUL/BkEH8u06Ql3V1S70AMn2SYu5WOw4z6gWm+aHJ1U0XBKSsxcB9vik\n" +
		"6JEJFNVBeteCL3kRd2Tqqpq0L8tQHUr3pBSyM9rj+8bH8tyNxzUHDyeAyObzcwWh\n" +
		"P4RecSuNAgMBAAECggEAazt75Pd2BNQHAtSlWplfdQq8gUJm4A452BAL3kgYYbe+\n" +
		"MiwwwfIICcNwL2eB+3NTq8syj4TpsKVzuJHDLDdcnEKXTa8TmKy06uHwnUJocJpd\n" +
		"GVCEQsErsWFSdhPZdDTzTdbihtfxSs6C/bLDyOe5lYRKVDWfqttOm0uP/11imehq\n" +
		"5CbnirPJF80i7SSR3ft743SbE9NMXy7IYlGZ9NDUaKcPVhH+oxEB81DodnIxk7BD\n" +
		"IiPa44m2XyCbDFWY9gmKGCr838tG8DG9at4SldG18JwobJsjFgOTJTIrPZEd8aUS\n" +
		"Wx21YITEzQG4RMp3/RvNNiWNgvqSPuuoov5qS0O8TQKBgQDkm5RRQGAr2f4Giodr\n" +
		"+CaSrOdTB2wGTS/w5xKktkOa/0ZVW4QOgKu04bSp8BJ88JvOfwdX8WuAqa+4ZQa1\n" +
		"d76Ya0nGotY125ZQ5RYgKaaFaWUJy/CAquet7cr7mbGWYhGbngL1qWQMkxcZlJnL\n" +
		"ZSR83c8oSUMNIsA2ZXnjh1+iBwKBgQDJw0mcpnrvOgf5MP7NSjiAMrt+YgRCcx2D\n" +
		"KPIZuxn6t0N9+HRnQC5EN3twSXp5HE2XjPn8jG1xl345E/Ev2t3vzbe8iabzcEne\n" +
		"w9/6Wqd5ENmk/Qib3T2RZshl1zymSRdSVZexcjd9f1nmsq1JyhEk5s4ZsIkk5U0Z\n" +
		"/3SM6NrQywKBgFaFm6j02HFAXChVndN7Y/33esWt9XCdHhvrGN9GLGgpXZFIxb5H\n" +
		"bLVVB2+Z8SVgW1fYNAtQ0AMuNddwRQ3BeF1vnciUMMbJiSaszab2nJO5xAflK+1G\n" +
		"wdDOQxjenpvwGgHv1+bqaXdo5EFGQL7+VMT9nj39HGeIU39DANLglY1ZAoGBALrU\n" +
		"4sJzix0hoKaJTzmsg/t6fxJ+EzGxRV/iN6XKEzmOIKpyut+tl+pFckG9WPLzWYp/\n" +
		"2jGZm/L29MRICixlQOTBm2W0FewRS+ZDfZFoBvLdvpzATwt96HhPNDzR/fCBeF4e\n" +
		"slR3zpigqBAv3rWYrx17uNgjGCwZRbdQTY36Rj3XAoGAOKrgsJkWPNV08Sw9DX6R\n" +
		"SyODv0NpdCKlGcDZX/LZc/imic0eCUww64ZqPFHHdRkIEj3cVtSTryqfXPFheVxB\n" +
		"JA/5Rtu/UAatNxhUwA3NT1WJewBsTQyds75Vwz0TBvqr0VWEi5GbxlZReLu7v5gj\n" +
		"rt3dAPD3c4Szs8PbWB9pGso=\n" +
		"-----END PRIVATE KEY-----"
)

var SingleEthWithDHCPAndExplicitProxy = &api.NetworkModel{
	Ports: []*api.Port{
		{
			LogicalLabel: "eth0",
			AdminUp:      true,
		},
	},
	Bridges: []*api.Bridge{
		{
			LogicalLabel: "bridge0",
			Ports:        []string{"eth0"},
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
						PrivateDns: []string{"dns-server-for-device"},
					},
				},
			},
			Router: &api.Router{
				// Internet is reachable but firewall will block access to the controller
				// that bypasses proxy.
				OutsideReachability: true,
				ReachableEndpoints:  []string{"dns-server-for-device", "http-proxy", "wpad"},
			},
		},
	},
	Firewall: &api.Firewall{
		Rules: []*api.FwRule{
			// It is not allowed to access the controller directly, proxy must be used.
			{
				SrcSubnet: "172.20.20.0/24",
				DstSubnet: evetest.GetControllerIPv4().String() + "/32",
				Action:    api.FwAction_FW_DROP,
			},
		},
	},
	Endpoints: &api.Endpoints{
		DnsServers: []*api.DNSServer{
			{
				Endpoint: &api.Endpoint{
					LogicalLabel: "dns-server-for-device",
					Fqdn:         "dns-server-for-device.test",
					Ipv4: &api.EndpointIPConfig{
						Subnet: "10.16.16.0/24",
						Ip:     "10.16.16.25",
					},
				},
				StaticEntries: []*api.DNSEntry{
					{
						FqdnSource: &api.DNSEntry_EndpointFqdnRef{
							EndpointFqdnRef: "http-proxy",
						},
						IpSource: &api.DNSEntry_EndpointIpRef{
							EndpointIpRef: &api.EndpointIPRef{
								LogicalLabel: "http-proxy",
								IpVersion:    api.IPVersion_IPV4,
							},
						},
					},
				},
				UpstreamServers: []string{"8.8.8.8", "1.1.1.1"},
			},
			{
				Endpoint: &api.Endpoint{
					LogicalLabel: "dns-server-for-proxy",
					Fqdn:         "dns-server-for-proxy.test",
					Ipv4: &api.EndpointIPConfig{
						Subnet: "10.17.17.0/24",
						Ip:     "10.17.17.25",
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
				},
				UpstreamServers: []string{"8.8.8.8", "1.1.1.1"},
			},
		},
		ExplicitProxies: []*api.ExplicitProxy{
			{
				Endpoint: &api.Endpoint{
					LogicalLabel: "http-proxy",
					Fqdn:         "http-proxy.test",
					Ipv4: &api.EndpointIPConfig{
						Subnet: "10.18.18.0/24",
						Ip:     "10.18.18.25",
					},
				},
				Proxy: &api.Proxy{
					DnsClientConfig: &api.DNSClientConfig{
						PrivateDns: []string{"dns-server-for-proxy"},
					},
					CaCertPem: ProxyCACertPEM,
					CaKeyPem:  ProxyCAKeyPEM,
					ProxyRules: []*api.ProxyRule{
						{
							ReqHost: "github.com",
							Action:  api.ProxyAction_PX_REJECT,
						},
						{
							Action: api.ProxyAction_PX_MITM,
						},
					},
				},
				HttpProxy: &api.ProxyPort{
					Port:        9090,
					ListenProto: api.ProxyListenProto_HTTP,
				},
				HttpsProxy: &api.ProxyPort{
					Port:        9091,
					ListenProto: api.ProxyListenProto_HTTP,
				},
			},
		},
	},
}

var SingleEthWithDHCPAndTransparentProxy = &api.NetworkModel{
	Ports: []*api.Port{
		{
			LogicalLabel: "eth0",
			AdminUp:      true,
		},
	},
	Bridges: []*api.Bridge{
		{
			LogicalLabel: "bridge0",
			Ports:        []string{"eth0"},
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
			TransparentProxy: "tproxy",
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
				},
				UpstreamServers: []string{"8.8.8.8", "1.1.1.1"},
			},
		},
		TransparentProxies: []*api.TransparentProxy{
			{
				Endpoint: &api.Endpoint{
					LogicalLabel: "tproxy",
					Fqdn:         "tproxy.test",
					Ipv4: &api.EndpointIPConfig{
						Subnet: "10.17.17.0/24",
						Ip:     "10.17.17.25",
					},
				},
				Proxy: &api.Proxy{
					DnsClientConfig: &api.DNSClientConfig{
						PrivateDns: []string{"dns-server"},
					},
					CaCertPem: ProxyCACertPEM,
					CaKeyPem:  ProxyCAKeyPEM,
					ProxyRules: []*api.ProxyRule{
						{
							Action: api.ProxyAction_PX_MITM,
						},
					},
				},
			},
		},
	},
}

var SingleEthWithDHCPAndAutoDiscoveredProxy = &api.NetworkModel{
	Ports: []*api.Port{
		{
			LogicalLabel: "eth0",
			AdminUp:      true,
		},
	},
	Bridges: []*api.Bridge{
		{
			LogicalLabel: "bridge0",
			Ports:        []string{"eth0"},
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
						PrivateDns: []string{"dns-server-for-device"},
					},
				},
			},
			Router: &api.Router{
				// Internet is reachable but firewall will block access to the controller
				// that bypasses proxy.
				OutsideReachability: true,
				ReachableEndpoints:  []string{"dns-server-for-device", "http-proxy", "wpad"},
			},
		},
	},
	Firewall: &api.Firewall{
		Rules: []*api.FwRule{
			// It is not allowed to access the controller directly, proxy must be used.
			{
				SrcSubnet: "172.20.20.0/24",
				DstSubnet: evetest.GetControllerIPv4().String() + "/32",
				Action:    api.FwAction_FW_DROP,
			},
		},
	},
	Endpoints: &api.Endpoints{
		DnsServers: []*api.DNSServer{
			{
				Endpoint: &api.Endpoint{
					LogicalLabel: "dns-server-for-device",
					Fqdn:         "dns-server-for-device.test",
					Ipv4: &api.EndpointIPConfig{
						Subnet: "10.16.16.0/24",
						Ip:     "10.16.16.25",
					},
				},
				StaticEntries: []*api.DNSEntry{
					{
						FqdnSource: &api.DNSEntry_EndpointFqdnRef{
							EndpointFqdnRef: "http-proxy",
						},
						IpSource: &api.DNSEntry_EndpointIpRef{
							EndpointIpRef: &api.EndpointIPRef{
								LogicalLabel: "http-proxy",
								IpVersion:    api.IPVersion_IPV4,
							},
						},
					},
					{
						FqdnSource: &api.DNSEntry_EndpointFqdnRef{
							EndpointFqdnRef: "wpad",
						},
						IpSource: &api.DNSEntry_EndpointIpRef{
							EndpointIpRef: &api.EndpointIPRef{
								LogicalLabel: "wpad",
								IpVersion:    api.IPVersion_IPV4,
							},
						},
					},
				},
				UpstreamServers: []string{"8.8.8.8", "1.1.1.1"},
			},
			{
				Endpoint: &api.Endpoint{
					LogicalLabel: "dns-server-for-proxy",
					Fqdn:         "dns-server-for-proxy.test",
					Ipv4: &api.EndpointIPConfig{
						Subnet: "10.17.17.0/24",
						Ip:     "10.17.17.25",
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
				},
				UpstreamServers: []string{"8.8.8.8", "1.1.1.1"},
			},
		},
		ExplicitProxies: []*api.ExplicitProxy{
			{
				Endpoint: &api.Endpoint{
					LogicalLabel: "http-proxy",
					Fqdn:         "http-proxy.test",
					Ipv4: &api.EndpointIPConfig{
						Subnet: "10.18.18.0/24",
						Ip:     "10.18.18.25",
					},
				},
				Proxy: &api.Proxy{
					DnsClientConfig: &api.DNSClientConfig{
						PrivateDns: []string{"dns-server-for-proxy"},
					},
					CaCertPem: ProxyCACertPEM,
					CaKeyPem:  ProxyCAKeyPEM,
					ProxyRules: []*api.ProxyRule{
						{
							ReqHost: "github.com",
							Action:  api.ProxyAction_PX_REJECT,
						},
						{
							Action: api.ProxyAction_PX_MITM,
						},
					},
				},
				HttpProxy: &api.ProxyPort{
					Port:        9090,
					ListenProto: api.ProxyListenProto_HTTP,
				},
				HttpsProxy: &api.ProxyPort{
					Port:        9091,
					ListenProto: api.ProxyListenProto_HTTP,
				},
			},
		},
		HttpServers: []*api.HTTPServer{
			{
				Endpoint: &api.Endpoint{
					LogicalLabel: "wpad",
					Fqdn:         "wpad.test",
					Ipv4: &api.EndpointIPConfig{
						Subnet: "10.19.19.0/24",
						Ip:     "10.19.19.25",
					},
				},
				HttpPort: 80,
				Paths: map[string]*api.HTTPContent{
					"/wpad.dat": {
						ContentType: "application/x-ns-proxy-autoconfig",
						Content: "function FindProxyForURL (url, host) {\n" +
							"  if (host == 'github.com') {\n" +
							"    return 'DIRECT';\n" +
							"  }\n" +
							"  if (url.substring(0, 5) == 'http:') {\n" +
							"    return 'PROXY my-proxy.sdn:9090';\n" +
							"  }\n" +
							"  if (url.substring(0, 6) == 'https:') {\n" +
							"    return 'PROXY my-proxy.sdn:9091';\n" +
							"  }\n" +
							"  return 'DIRECT';\n" +
							"}",
					},
				},
			},
		},
	},
}

var (
	PnacCACertPEM = "-----BEGIN CERTIFICATE-----\n" +
		"MIIDaDCCAlCgAwIBAgITTkcl52rZ0untPRkDdjOrdx7RxjANBgkqhkiG9w0BAQsF\n" +
		"ADBEMRUwEwYDVQQDDAxwbmFjLVRlc3QgQ0ExDDAKBgNVBAsMA0xhYjEQMA4GA1UE\n" +
		"CgwHRXhhbXBsZTELMAkGA1UEBhMCVVMwHhcNMjUxMjE5MTA0MjU0WhcNMzUxMjE3\n" +
		"MTA0MjU0WjBEMRUwEwYDVQQDDAxwbmFjLVRlc3QgQ0ExDDAKBgNVBAsMA0xhYjEQ\n" +
		"MA4GA1UECgwHRXhhbXBsZTELMAkGA1UEBhMCVVMwggEiMA0GCSqGSIb3DQEBAQUA\n" +
		"A4IBDwAwggEKAoIBAQDTZRaekBmbwefplSXvf3VCg5Iq3SBH8ZxyFLHlOw+HytVc\n" +
		"GMiGUTjJ29OJU+sa3Mn0UfPWPD5Sp41N811l9yT8+eObklu/CdAjF3hCg8keYgpj\n" +
		"4YqvfX80JvmpMOGDdhk00NGEtXz715TCNTJIXs5tLEuaYY+Le5tC669DELhgHCz6\n" +
		"8aALsjY3ulzVnEDmQ7wVtLs7jzz887aJLwRVQkeOsPs6EDmhEdz/FwXgBCAkGAhS\n" +
		"QsO4euK3pGsnQxqULphZMNjYU59k+bm2ARpHZcnqxU4MRnuxam0/9TQSKxNSHRLZ\n" +
		"8Qq6yoHRtQn+YtZxOYw5JJbR4qSvhGsJ4WIqMiaJAgMBAAGjUzBRMB0GA1UdDgQW\n" +
		"BBTaTfYxOkr5wpkpQwIurZglTEgppzAfBgNVHSMEGDAWgBTaTfYxOkr5wpkpQwIu\n" +
		"rZglTEgppzAPBgNVHRMBAf8EBTADAQH/MA0GCSqGSIb3DQEBCwUAA4IBAQAWUQZq\n" +
		"ZAziZSEYJttXqZ8dqcpsc0BESldnUhClqcs3pNLKXBTic3JkcaQoZJp926YXvVD4\n" +
		"97ecOVi4NSwMdew7MwgdTK92ajBAAEuG2aWsvUQ4zsm9aRiNIYV2hgCWgZGcHtHy\n" +
		"VVA4aAVCoJOf1b2et3cOD4HDOfFzHeeqNSU6lIkkvv00T4JTTEl51Rxiyz/6JYp/\n" +
		"3Rneb6ClplHAGyhX9ioOcJuKxaBF/KsveykkuIFrnwXuRawD4cFuSgM17NQfTqK8\n" +
		"G01TASzVNVPHWw5sH2uNPw8Fl4ZiFM64CQeRlT9fsOhSoBhgp3GsLVhsAxBnr8ZS\n" +
		"73NS+oxBkWjgz9yc" +
		"\n-----END CERTIFICATE-----"
	PnacCAKeyPEM = "-----BEGIN RSA PRIVATE KEY-----\n" +
		"MIIEpAIBAAKCAQEA02UWnpAZm8Hn6ZUl7391QoOSKt0gR/GcchSx5TsPh8rVXBjI\n" +
		"hlE4ydvTiVPrGtzJ9FHz1jw+UqeNTfNdZfck/Pnjm5JbvwnQIxd4QoPJHmIKY+GK\n" +
		"r31/NCb5qTDhg3YZNNDRhLV8+9eUwjUySF7ObSxLmmGPi3ubQuuvQxC4YBws+vGg\n" +
		"C7I2N7pc1ZxA5kO8FbS7O488/PO2iS8EVUJHjrD7OhA5oRHc/xcF4AQgJBgIUkLD\n" +
		"uHrit6RrJ0MalC6YWTDY2FOfZPm5tgEaR2XJ6sVODEZ7sWptP/U0EisTUh0S2fEK\n" +
		"usqB0bUJ/mLWcTmMOSSW0eKkr4RrCeFiKjImiQIDAQABAoIBABIhHsX9fLy/bfi5\n" +
		"2lWGXFOWfhAgA7c30N91x+XtYUEXk6HA3F2slI63RBaIdGbK3aUO7DcI1p13EibH\n" +
		"wtBOWEU71xnN/QFOwaNozV8C91ocNWDohGDVhfW+i/XHI+gG1lWRvQ5BFFVy4Sag\n" +
		"sk3Dl7eEL32vdUSUlrWwCclaIz3WtNvadhlrtgp+hIXzkfq5/zYN3EO7RC4O+hq1\n" +
		"EF9HLeApoe8nkCOewzAEJwoAi3P29CyJxf1/u/mZMGVcVNVnbp82KtbAfJCno1Ug\n" +
		"kMyynrc+ULXtCYFjpa4Uq835ScywD/zIBr6KQMCB6vWaA7XmemnImf+xZo+YCYuF\n" +
		"/h7Ec8ECgYEA53VlmptDd1yZpauiUxhj2OXoX1Dbt0ivA6tP9fPb2Gt2XnvQ8Q7g\n" +
		"UEtTjsRb7FgAlxP2aB59sc6v0w12IP/wK/nlyrRsArDIUvQlL7DCgooF0zpYUaKd\n" +
		"LXNXhUgFG7f6hRujQNAEQDi2Gl6AznXplJGMCtFo+a0EShGzpsLtr5ECgYEA6c8X\n" +
		"d5wHvdJhR0FK2Og5ksr/kBCG722SSBJyw2TEsBKmNsWabvtpshtYkAnMb/zmZ3N0\n" +
		"ZI/eA9NV7fSldB0xdsAjChcXTwnzI3LciNqqryoLQA1+x+f9AK7KAXmAjH+EyrFv\n" +
		"oSBQ0kR/VgmN76nGDtOSRW9vBWYtRX7t6dPH+3kCgYBGns8taQogtSQ8JC4W5G4y\n" +
		"k5Ne4bDoL0kW+YIgLRN66O7ozSZnJn7SgOkxuj/B0Of9MJ4SDpuTUNjcsFLGptCE\n" +
		"2m5+dqYt+/pjNRLThj8SzUIRvM+NuOv0HikqBVtppazOSCx7bfyeC6+kRAlQ9TEb\n" +
		"n3z3IAXDiEKyxsvlqbwTwQKBgQCZvdk6h1j30tywlBh5ZMpm4iEGRDfWPICR77+T\n" +
		"CDHlbX3qSilwjNVFjoG/xRGvGecPY3XHompkrZS1ccdSANhDs7fWrLRg/rPoPWES\n" +
		"hGbz43ueVMFnBf7xcf3W1mRW/or9FYvHsY4zlWL92i6Ax2w615g5HDsum69tITek\n" +
		"J+Q6UQKBgQDSLQegqYcy2ChQwzBfsxQRY1p8PDLRcwJq2OMXogZ7T/3V3FL3oy/U\n" +
		"jL9EFl/nPp8Wt0Q7Y6krBmBK/pmEwLntGhbQGbu4hR+78hVZraLAFzvlvnLToUua\n" +
		"G6ikJD8Ks9LS2SDfDboEkPbzk9bXEsFgn6HUyuScJTFCbyTRPtXZVg==\n" +
		"-----END RSA PRIVATE KEY-----"
)

func SingleEthWithPNAC(requireSCEPProxy bool) *api.NetworkModel {
	model := &api.NetworkModel{
		Ports: []*api.Port{
			{
				LogicalLabel: "eth0",
				AdminUp:      true,
			},
		},
		Bridges: []*api.Bridge{
			{
				LogicalLabel: "bridge0",
				Ports:        []string{"eth0"},
				Pnac: &api.PNAC{
					Enable_8021X: true,
					CaCertPem:    PnacCACertPEM,
					CaKeyPem:     PnacCAKeyPEM,
					Users: []*api.EAPUser{
						{
							Identity: "evetest",
							Methods:  []api.EAPMethod{api.EAPMethod_EAP_METHOD_TLS},
						},
					},
					PreAuthVlanId:  10,
					PostAuthVlanId: 20,
				},
			},
		},
		Networks: []*api.Network{
			{
				LogicalLabel: "onboarding-network",
				Bridge:       "bridge0",
				VlanId:       10,
				Ipv4: &api.NetworkIPConfig{
					Subnet: "172.20.10.0/24",
					GwIp:   "172.20.10.1",
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
				LogicalLabel: "authenticated-network",
				Bridge:       "bridge0",
				VlanId:       20,
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
								EndpointFqdnRef: "scep-server",
							},
							IpSource: &api.DNSEntry_EndpointIpRef{
								EndpointIpRef: &api.EndpointIPRef{
									LogicalLabel: "scep-server",
									IpVersion:    api.IPVersion_IPV4,
								},
							},
						},
					},
					UpstreamServers: []string{"8.8.8.8", "1.1.1.1"},
				},
			},
			ScepServers: []*api.SCEPServer{
				{
					Endpoint: &api.Endpoint{
						LogicalLabel: "scep-server",
						Fqdn:         "scep-server.test",
						Ipv4: &api.EndpointIPConfig{
							Subnet: "10.17.17.0/24",
							Ip:     "10.17.17.25",
						},
					},
					Port:              8080,
					CaCertPem:         PnacCACertPEM,
					CaKeyPem:          PnacCAKeyPEM,
					ChallengePassword: "123456789",
				},
			},
		},
	}

	// Configure Firewall.
	// Common allow rules.
	rules := []*api.FwRule{
		// Allow edge-device to resolve Controller and SCEP server IP addresses:
		{
			SrcSubnet: "172.20.10.0/24",
			DstSubnet: "10.16.16.25/32", // DNS Server
			Action:    api.FwAction_FW_ALLOW,
		},
		// Allow edge-device to onboard and retrieve configuration:
		{
			SrcSubnet: "172.20.10.0/24",
			DstSubnet: evetest.GetControllerIPv4().String() + "/32",
			Action:    api.FwAction_FW_ALLOW,
		},
		// Allow SSH access to EVE even when port is not authenticated:
		{
			SrcSubnet: "172.20.10.0/24",
			DstSubnet: evetest.GetSrcIPv4ForEVEAccess().String() + "/32",
			Action:    api.FwAction_FW_ALLOW,
		},
	}

	// Optionally allow direct access to the SCEP server.
	if !requireSCEPProxy {
		rules = append(rules, &api.FwRule{
			SrcSubnet: "172.20.10.0/24",
			DstSubnet: "10.17.17.25/32", // SCEP server
			Action:    api.FwAction_FW_ALLOW,
		})
	}

	// Default drop.
	rules = append(rules, &api.FwRule{
		SrcSubnet: "172.20.10.0/24",
		Action:    api.FwAction_FW_DROP,
	})

	model.Firewall = &api.Firewall{
		// Allow onboarding-network to access only explicitly permitted services;
		// drop all other traffic.
		Rules: rules,
	}
	return model
}
