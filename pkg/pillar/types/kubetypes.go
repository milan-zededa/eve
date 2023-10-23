// Copyright (c) 2023 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package types

import "github.com/lf-edge/eve/pkg/pillar/base"

// AppKubeNetworkStatus is published by Kubernetes CNI plugin.
// This is used to advertise pod interface IP, MAC, interface name, etc.
// The ContainerID is Pod's container UUID from kubernetes.
type AppKubeNetworkStatus struct {
	UUIDandVersion      UUIDandVersion
	DisplayName         string
	ContainerID         string
	ULNetworkStatusList []struct{} // TODO: find out what is used
}

// Key returns app UUID.
func (s AppKubeNetworkStatus) Key() string {
	return s.UUIDandVersion.UUID.String()
}

// LogCreate logs newly published AppKubeNetworkStatus instance.
func (s AppKubeNetworkStatus) LogCreate(logBase *base.LogObject) {
	logObject := base.NewLogObject(logBase, base.AppKubeNetworkStatusLogType, "",
		nilUUID, s.LogKey())
	logObject.Metricf("AppKubeNetworkStatus create %+v", s)
}

// LogModify :
func (s AppKubeNetworkStatus) LogModify(logBase *base.LogObject, old interface{}) {
	logObject := base.EnsureLogObject(logBase, base.AppKubeNetworkStatusLogType, "",
		nilUUID, s.LogKey())
	oldVal, ok := old.(AppKubeNetworkStatus)
	if !ok {
		logObject.Clone().Fatalf(
			"LogModify: Old object interface passed is not of AppKubeNetworkStatus type")
	}
	logObject.Metricf("AppKubeNetworkStatus modified from %+v to %+v", oldVal, s)
}

// LogDelete :
func (s AppKubeNetworkStatus) LogDelete(logBase *base.LogObject) {
	logObject := base.EnsureLogObject(logBase, base.AppKubeNetworkStatusLogType, "",
		nilUUID, s.LogKey())
	logObject.Metricf("AppKubeNetworkStatus delete %+v", s)
	base.DeleteLogObject(logBase, s.LogKey())
}

// LogKey :
func (s AppKubeNetworkStatus) LogKey() string {
	return string(base.AppKubeNetworkStatusLogType) + "-" + s.Key()
}
