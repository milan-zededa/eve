package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/lf-edge/eve/evetest/constants"
	pb "github.com/lf-edge/eve/evetest/grpcapi/go"
	"github.com/lf-edge/eve/evetest/utils"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"
)

func sdnCommand() *cobra.Command {
	sdnCmd := &cobra.Command{
		Use:   "sdn",
		Short: "Commands for interacting with the SDN (network emulator)",
	}
	sdnCmd.AddCommand(
		sdnStatusCmd(),
		sdnNetModelCmd(),
		sdnConfigGraphCmd(),
		sdnLogsCmd(),
		sdnSSHCmd(),
	)
	return sdnCmd
}

func sdnStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Get SDN status and config errors",
		RunE: func(cmd *cobra.Command, args []string) error {
			req := &pb.SDNRequest{}
			resp, err := client.GetSDNStatus(context.Background(), req)
			if err != nil {
				return fmt.Errorf("failed to get SDN status: %w", err)
			}

			if len(resp.MgmtIps) > 0 {
				fmt.Println("SDN Management IPs:")
				for _, ip := range resp.MgmtIps {
					fmt.Printf("  - %s\n", ip)
				}
			}

			if len(resp.ConfigErrors) > 0 {
				fmt.Println("SDN Configuration Errors:")
				for _, err := range resp.ConfigErrors {
					fmt.Printf("  - Item: %s\n    Error: %s\n", err.ItemRef, err.ErrorMsg)
				}
			} else {
				fmt.Println("No SDN configuration errors.")
			}

			return nil
		},
	}
	return cmd
}

func sdnNetModelCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "net-model",
		Short: "Print abstract network model maintained by SDN",
		RunE: func(cmd *cobra.Command, args []string) error {
			req := &pb.SDNRequest{}
			resp, err := client.GetSDNNetworkModel(context.Background(), req)
			if err != nil {
				return fmt.Errorf("failed to get SDN network model: %w", err)
			}

			// Marshal the network model to JSON for readable output
			jsonBytes, err := protojson.MarshalOptions{
				Multiline:       true,
				EmitUnpopulated: false,
			}.Marshal(resp.NetworkModel)
			if err != nil {
				return fmt.Errorf("failed to marshal network model to JSON: %w", err)
			}

			fmt.Println(string(jsonBytes))
			return nil
		},
	}
	return cmd
}

func sdnConfigGraphCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "graph",
		Short: "Print SDN config graph as Graphviz dot format",
		RunE: func(cmd *cobra.Command, args []string) error {
			req := &pb.SDNRequest{}
			resp, err := client.GetSDNConfigGraph(context.Background(), req)
			if err != nil {
				return fmt.Errorf("failed to get SDN config graph: %w", err)
			}
			fmt.Println(resp.ConfigGraphviz)
			return nil
		},
	}
	return cmd
}

func sdnLogsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logs",
		Short: "Stream logs from SDN",
		RunE: func(cmd *cobra.Command, args []string) error {
			req := &pb.SDNRequest{}
			stream, err := client.StreamSDNLogs(context.Background(), req)
			if err != nil {
				return fmt.Errorf("failed to stream SDN logs: %w", err)
			}
			for {
				logMsg, err := stream.Recv()
				if err == io.EOF {
					break
				}
				if err != nil {
					return fmt.Errorf("stream error: %w", err)
				}
				fmt.Println(logMsg.String())
			}
			return nil
		},
	}
	return cmd
}

func sdnSSHCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ssh",
		Short: "Establish SSH connection to SDN",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Establish TCP proxy for the SDN SSH access.
			req := &pb.SDNRequest{}
			resp, err := client.ConnectSSHToSDN(context.Background(), req)
			if err != nil {
				return fmt.Errorf("failed to setup SDN SSH proxy: %w", err)
			}
			defer func() {
				closeReq := &pb.CloseTCPProxyRequest{
					ProxyIpAddress: resp.ProxyIpAddress,
					ProxyPort:      resp.ProxyPort,
				}
				_, err = client.CloseTCPProxy(context.Background(), closeReq)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Failed to close SDN SSH proxy: %v\n", err)
				}
			}()

			// Write SDN SSH private key to a temporary file.
			keyFilepath, err := createTmpSSHKeyFile(constants.SDNSSHPrivateKey)
			if err != nil {
				return err
			}
			defer func() {
				_ = os.Remove(keyFilepath)
			}()

			// Run the ssh command.
			sshArgs := []string{
				"-o", "IdentitiesOnly=yes",
				"-o", "ConnectTimeout=5",
				"-o", "StrictHostKeyChecking=no",
				"-o", "UserKnownHostsFile=/dev/null",
				"-i", keyFilepath,
				"-p", strconv.Itoa(int(resp.ProxyPort)),
				"root@" + resp.ProxyIpAddress,
			}
			sshArgs = append(sshArgs, args...)
			err = utils.RunCommandForeground("ssh", sshArgs, utils.SetThisProcessStdin())
			if err != nil {
				return fmt.Errorf("ssh command failed: %w", err)
			}
			return nil
		},
	}
	return cmd
}
