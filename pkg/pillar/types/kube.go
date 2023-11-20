// Copyright (c) 20 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package types

import "net"

// CommonCNIRPCArgs : arguments used for every CNI RPC method
// (called by eve-bridge, served by zedrouter).
type CommonCNIRPCArgs struct {
	Pod AppPod
	// Interface inside the pod.
	PodInterface NetInterfaceWithNs
}

// ConnectPodAtL2Args : arguments for the ConnectPodAtL2 RPC method.
type ConnectPodAtL2Args struct {
	CommonCNIRPCArgs
}

// ConnectPodAtL2Retval : type of the value returned by the ConnectPodAtL2 RPC method.
type ConnectPodAtL2Retval struct {
	UseDHCP bool
	// Interfaces include the bridge interface and both sides of the VETH connecting
	// pod with the host.
	Interfaces []NetInterfaceWithNs
}

// ConnectPodAtL3Args : arguments for the ConnectPodAtL3 RPC method.
type ConnectPodAtL3Args struct {
	CommonCNIRPCArgs
	PodIPAMConfig
}

// ConnectPodAtL3Retval : type of the value returned by the ConnectPodAtL3 RPC method.
type ConnectPodAtL3Retval struct{}

// DisconnectPodArgs : arguments for the DisconnectPod RPC method.
type DisconnectPodArgs struct {
	CommonCNIRPCArgs
}

// DisconnectPodRetval : type of the value returned by the DisconnectPod RPC method.
type DisconnectPodRetval struct {
	UsedDHCP bool
}

// CheckPodConnectionArgs : arguments for the CheckPodConnection RPC method.
type CheckPodConnectionArgs struct {
	CommonCNIRPCArgs
}

// CheckPodConnectionRetval : type of the value returned by the CheckPodConnection RPC method.
type CheckPodConnectionRetval struct {
	UsesDHCP bool
}

// AppPod is defined only in the Kubernetes mode.
// It describes Kubernetes Pod under which a given app is running.
type AppPod struct {
	Name string
	// PodNetNsPath references network namespace of the Kubernetes pod
	// inside which the application is running.
	NetNsPath string
}

// NetInterfaceWithNs : single network interface (configured by zedrouter for Kube CNI).
type NetInterfaceWithNs struct {
	Name      string
	MAC       net.HardwareAddr
	NetNsPath string
}

// PodVIF : configuration parameters for VIF connecting Kubernetes pod with the host.
type PodVIF struct {
	GuestIfName string
	IPAM        PodIPAMConfig
}

// PodIPAMConfig : IP config assigned to Pod by a Kubernetes IPAM plugin.
type PodIPAMConfig struct {
	IPs    []PodIPAddress
	Routes []PodRoute
	DNS    PodDNS
}

// PodIPAddress : ip address assigned to kubernetes pod network interface.
type PodIPAddress struct {
	Address *net.IPNet
	Gateway net.IP
}

// PodRoute : network IP route configured for kubernetes pod network interface.
type PodRoute struct {
	Dst *net.IPNet
	GW  net.IP
}

// PodDNS : settings for DNS resolver inside a kubernetes pod.
type PodDNS struct {
	Nameservers []string
	Domain      string
	Search      []string
	Options     []string
}
