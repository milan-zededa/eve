// Copyright (c) 2026 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

//go:build k

package kubeapi

import (
	"context"
	"fmt"

	lhv1beta2 "github.com/longhorn/longhorn-manager/k8s/pkg/apis/longhorn/v1beta2"
	"github.com/longhorn/longhorn-manager/k8s/pkg/client/clientset/versioned"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// diskStatusGetter is the subset of the generated Longhorn client this file
// needs, kept narrow so the state logic below can be exercised without a
// live API server.
type diskStatusGetter interface {
	Get(ctx context.Context, name string, opts metav1.GetOptions) (*lhv1beta2.Node, error)
}

// allNodeDisksReady reports whether every disk nodeName's Longhorn Node
// object declares in Spec.Disks has a matching Status.DiskStatus entry
// recording a disk UUID and a True Ready condition. The disk monitor that
// fills in that status needs a running instance-manager, so a node whose
// instance-manager only just started can pass instanceManagerReady before
// the monitor has completed even once.
func allNodeDisksReady(ctx context.Context, getter diskStatusGetter, nodeName string) (bool, error) {
	node, err := getter.Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		return false, err
	}
	if len(node.Spec.Disks) == 0 {
		return false, nil
	}
	for diskName := range node.Spec.Disks {
		status := node.Status.DiskStatus[diskName]
		if status == nil || status.DiskUUID == "" {
			return false, nil
		}
		ready := false
		for _, cond := range status.Conditions {
			if cond.Type == lhv1beta2.DiskConditionTypeReady &&
				cond.Status == lhv1beta2.ConditionStatusTrue {
				ready = true
				break
			}
		}
		if !ready {
			return false, nil
		}
	}
	return true, nil
}

// diskStatusReady is the gate checkLonghornReady applies after the
// instance-manager gate. A variable so tests can stub it, like
// instanceManagerReady.
var diskStatusReady = checkLonghornDiskStatusReady

// checkLonghornDiskStatusReady fails while nodeName's Longhorn Node
// object has not finished recording status for every disk in its spec.
func checkLonghornDiskStatusReady(ctx context.Context, nodeName string) error {
	config, err := GetKubeConfig()
	if err != nil {
		return fmt.Errorf("longhorn disk status: kubeconfig: %v", err)
	}
	lhClient, err := versioned.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("longhorn disk status: versioned client: %v", err)
	}
	ready, err := allNodeDisksReady(ctx,
		lhClient.LonghornV1beta2().Nodes(longhornNamespace), nodeName)
	if err != nil {
		return fmt.Errorf("longhorn disk status: get node %s: %v", nodeName, err)
	}
	if !ready {
		return fmt.Errorf("longhorn disk status not yet recorded on node")
	}
	return nil
}
