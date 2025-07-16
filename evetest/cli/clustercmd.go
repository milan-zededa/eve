package main

import (
	"context"
	"fmt"
	"io"

	pb "github.com/lf-edge/eve/evetest/grpcapi/go"
	"github.com/spf13/cobra"
)

func clusterCommand() *cobra.Command {
	clusterCmd := &cobra.Command{
		Use:   "cluster",
		Short: "Commands for interacting with the cluster of EVE devices",
	}
	clusterCmd.AddCommand(
		clusterInfoCmd(),
		clusterMetricsCmd(),
	)
	return clusterCmd
}

func clusterInfoCmd() *cobra.Command {
	var follow bool
	cmd := &cobra.Command{
		Use:   "info",
		Short: "Prints cluster info",
		RunE: func(cmd *cobra.Command, args []string) error {
			req := &pb.ClusterRequest{Follow: follow}
			stream, err := client.GetClusterInfo(context.Background(), req)
			if err != nil {
				return fmt.Errorf("failed to get EVE cluster info stream: %w", err)
			}
			for {
				resp, err := stream.Recv()
				if err == io.EOF {
					break
				}
				if err != nil {
					return fmt.Errorf("stream error: %w", err)
				}
				printClusterInfo(resp)
				if !follow {
					break
				}
				fmt.Println()
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Follow cluster info updates")
	return cmd
}

func printClusterInfo(info *pb.ClusterInfoResponse) {
	if info == nil {
		fmt.Println("No cluster info available.")
		return
	}
	if len(info.NodeInfo) > 0 {
		fmt.Println("Nodes:")
		for _, node := range info.NodeInfo {
			fmt.Printf(" - Node %q: %s\n", node.NodeId, node.String())
		}
	}
	if len(info.PodNamespaceInfo) > 0 {
		fmt.Println("Pod Namespaces:")
		for _, ns := range info.PodNamespaceInfo {
			fmt.Printf(" - Pod namespace %q: %s\n", ns.Name, ns.String())
		}
	}
	if len(info.PodInfo) > 0 {
		fmt.Println("Pods:")
		for _, pod := range info.PodInfo {
			fmt.Printf(" - Pod %q: %s\n", pod.Name, pod.String())
		}
	}
	if info.StorageInfo != nil {
		fmt.Printf("Storage Info: %s\n", info.StorageInfo.String())
	}
}

func clusterMetricsCmd() *cobra.Command {
	var follow bool
	cmd := &cobra.Command{
		Use:   "metrics",
		Short: "Prints cluster metrics",
		RunE: func(cmd *cobra.Command, args []string) error {
			req := &pb.ClusterRequest{Follow: follow}
			stream, err := client.GetClusterMetrics(context.Background(), req)
			if err != nil {
				return fmt.Errorf("failed to get cluster metrics stream: %w", err)
			}
			for {
				resp, err := stream.Recv()
				if err == io.EOF {
					break
				}
				if err != nil {
					return fmt.Errorf("stream error: %w", err)
				}
				fmt.Println(resp.ClusterMetrics.String())
				if !follow {
					break
				}
				fmt.Println()
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Follow cluster metrics updates")
	return cmd
}
