// Copyright (c) 2026 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lf-edge/eve/evetest/constants"
	pb "github.com/lf-edge/eve/evetest/grpcapi/go"
	"github.com/lf-edge/eve/evetest/utils"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"
)

var eveDeviceName string

func eveCommand() *cobra.Command {
	eveCmd := &cobra.Command{
		Use:   "eve",
		Short: "Commands for interacting with EVE devices",
	}
	eveCmd.PersistentFlags().StringVarP(&eveDeviceName, "devicename", "d", "", "EVE device name")
	eveCmd.AddCommand(
		eveHardRebootCmd(),
		eveSoftRebootCmd(),
		eveConfigCmd(),
		eveInfoCmd(),
		eveMetricsCmd(),
		eveLogsCmd(),
		eveConsoleOutputCmd(),
		eveAppInfoCmd(),
		eveAppMetricsCmd(),
		eveAppLogsCmd(),
		eveAppFlowLogsCmd(),
		eveNIInfoCmd(),
		eveNIMetricsCmd(),
		eveCollectInfoCmd(),
		eveSSHCmd(),
		eveSCPCmd(),
		evePortFwdCmd(),
		eveConsoleCmd(),
		eveKubectlCmd(),
	)
	return eveCmd
}

// addTailFlag adds a --tail/-t flag to a command. When used without a value
// (e.g. --tail), it defaults to 1. When used with a value (e.g. --tail 5),
// it uses that value. When not used at all, tail is 0 (meaning print all).
func addTailFlag(cmd *cobra.Command, tail *int) {
	cmd.Flags().IntVarP(tail, "tail", "t", 0,
		"Print only the last N entries (default 1 if no value given)")
	cmd.Flags().Lookup("tail").NoOptDefVal = "1"
}

// tailEntries returns the last n elements from entries. If n <= 0 or n >= len,
// it returns all entries.
func tailEntries(entries []string, n int) []string {
	if n <= 0 || n >= len(entries) {
		return entries
	}
	return entries[len(entries)-n:]
}

func eveHardRebootCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "hard-reboot",
		Short: "Hard reboots the device",
		RunE: func(cmd *cobra.Command, args []string) error {
			req := &pb.EVEDeviceRequest{DeviceName: eveDeviceName}
			_, err := client.HardRebootEVEDevice(context.Background(), req)
			if err != nil {
				return fmt.Errorf("failed to hard reboot device: %w", err)
			}
			fmt.Println("Hard reboot command sent.")
			return nil
		},
	}
}

func eveSoftRebootCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "soft-reboot",
		Short: "Soft reboots the device",
		RunE: func(cmd *cobra.Command, args []string) error {
			req := &pb.EVEDeviceRequest{DeviceName: eveDeviceName}
			_, err := client.SoftRebootEVEDevice(context.Background(), req)
			if err != nil {
				return fmt.Errorf("failed to soft reboot device: %w", err)
			}
			fmt.Println("Soft reboot command sent.")
			return nil
		},
	}
}

func eveConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Prints config submitted through the controller",
		RunE: func(cmd *cobra.Command, args []string) error {
			req := &pb.EVEDeviceRequest{DeviceName: eveDeviceName}
			resp, err := client.GetEVEConfig(context.Background(), req)
			if err != nil {
				return fmt.Errorf("failed to get EVE config: %w", err)
			}

			// Marshal the config to JSON for readable output
			jsonBytes, err := protojson.MarshalOptions{
				Multiline:       true,
				EmitUnpopulated: false,
			}.Marshal(resp.Config)
			if err != nil {
				return fmt.Errorf("failed to marshal device config to JSON: %w", err)
			}

			fmt.Println(string(jsonBytes))
			return nil
		},
	}
	return cmd
}

func eveInfoCmd() *cobra.Command {
	var follow bool
	var tail int
	cmd := &cobra.Command{
		Use:   "info",
		Short: "Prints device info (HW specs, adapter info, etc.)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if tail > 0 && follow {
				return fmt.Errorf("--tail and --follow cannot be used together")
			}
			req := &pb.EVEDeviceStreamableRequest{
				DeviceName: eveDeviceName,
				Follow:     follow,
			}
			stream, err := client.GetEVEInfo(context.Background(), req)
			if err != nil {
				return fmt.Errorf("failed to get EVE info stream: %w", err)
			}
			var entries []string
			for {
				resp, err := stream.Recv()
				if err == io.EOF {
					break
				}
				if err != nil {
					return fmt.Errorf("stream error: %w", err)
				}
				if tail > 0 {
					entries = append(entries, resp.DeviceInfo.String())
				} else {
					fmt.Println(resp.DeviceInfo.String())
					fmt.Println()
				}
			}
			for _, e := range tailEntries(entries, tail) {
				fmt.Println(e)
				fmt.Println()
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Follow device info updates")
	addTailFlag(cmd, &tail)
	return cmd
}

func eveMetricsCmd() *cobra.Command {
	var follow bool
	var tail int
	cmd := &cobra.Command{
		Use:   "metrics",
		Short: "Prints device metrics",
		RunE: func(cmd *cobra.Command, args []string) error {
			if tail > 0 && follow {
				return fmt.Errorf("--tail and --follow cannot be used together")
			}
			req := &pb.EVEDeviceStreamableRequest{
				DeviceName: eveDeviceName,
				Follow:     follow,
			}
			stream, err := client.GetEVEMetrics(context.Background(), req)
			if err != nil {
				return fmt.Errorf("failed to get device metrics stream: %w", err)
			}
			var entries []string
			for {
				resp, err := stream.Recv()
				if err == io.EOF {
					break
				}
				if err != nil {
					return fmt.Errorf("stream error: %w", err)
				}
				if tail > 0 {
					entries = append(entries, resp.DeviceMetrics.String())
				} else {
					fmt.Println(resp.DeviceMetrics.String())
					fmt.Println()
				}
			}
			for _, e := range tailEntries(entries, tail) {
				fmt.Println(e)
				fmt.Println()
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Follow device metrics")
	addTailFlag(cmd, &tail)
	return cmd
}

func eveLogsCmd() *cobra.Command {
	var follow bool
	var tail int
	cmd := &cobra.Command{
		Use:   "logs",
		Short: "Prints all device logs",
		RunE: func(cmd *cobra.Command, args []string) error {
			if tail > 0 && follow {
				return fmt.Errorf("--tail and --follow cannot be used together")
			}
			req := &pb.EVEDeviceStreamableRequest{
				DeviceName: eveDeviceName,
				Follow:     follow,
			}
			stream, err := client.GetEVELogs(context.Background(), req)
			if err != nil {
				return fmt.Errorf("failed to get device logs stream: %w", err)
			}
			var entries []string
			for {
				logMsg, err := stream.Recv()
				if err == io.EOF {
					break
				}
				if err != nil {
					return fmt.Errorf("stream error: %w", err)
				}
				var ts string
				if logMsg.Timestamp != nil {
					ts = logMsg.Timestamp.AsTime().UTC().Format("2006-01-02 15:04:05.000")
				}
				severity := strings.ToLower(logMsg.Severity.String())
				severity = strings.TrimPrefix(severity, "log_")
				line := fmt.Sprintf("%s|%s|%s| %s",
					ts, severity, logMsg.Source, logMsg.Message)
				if tail > 0 {
					entries = append(entries, line)
				} else {
					fmt.Println(line)
				}
			}
			for _, e := range tailEntries(entries, tail) {
				fmt.Println(e)
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Follow logs")
	addTailFlag(cmd, &tail)
	return cmd
}

func eveConsoleOutputCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "console-output",
		Short: "Prints full EVE console output",
		RunE: func(cmd *cobra.Command, args []string) error {
			req := &pb.EVEDeviceRequest{DeviceName: eveDeviceName}
			resp, err := client.GetEVEConsoleOutput(context.Background(), req)
			if err != nil {
				return fmt.Errorf("failed to get console output: %w", err)
			}
			fmt.Println("EVE console output:")
			fmt.Println(resp.ConsoleOutput)
			return nil
		},
	}
	return cmd
}

func eveAppInfoCmd() *cobra.Command {
	var follow bool
	var tail int
	cmd := &cobra.Command{
		Use:   "app-info <app-name-OR-UUID>",
		Short: "Prints application info for the given app",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if tail > 0 && follow {
				return fmt.Errorf("--tail and --follow cannot be used together")
			}
			appID := args[0]
			req := &pb.AppRequest{
				DeviceName:    eveDeviceName,
				AppNameOrUuid: appID,
				Follow:        follow,
			}
			stream, err := client.GetAppInfo(context.Background(), req)
			if err != nil {
				return fmt.Errorf("failed to get app info stream: %w", err)
			}
			var entries []string
			for {
				resp, err := stream.Recv()
				if err == io.EOF {
					break
				}
				if err != nil {
					return fmt.Errorf("stream error: %w", err)
				}
				if tail > 0 {
					entries = append(entries, resp.AppInfo.String())
				} else {
					fmt.Println(resp.AppInfo.String())
					fmt.Println()
				}
			}
			for _, e := range tailEntries(entries, tail) {
				fmt.Println(e)
				fmt.Println()
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Follow app info updates")
	addTailFlag(cmd, &tail)
	return cmd
}

func eveAppMetricsCmd() *cobra.Command {
	var follow bool
	var tail int
	cmd := &cobra.Command{
		Use:   "app-metrics <appname-OR-UUID>",
		Short: "Prints application metrics for the given app",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if tail > 0 && follow {
				return fmt.Errorf("--tail and --follow cannot be used together")
			}
			appID := args[0]
			req := &pb.AppRequest{
				DeviceName:    eveDeviceName,
				AppNameOrUuid: appID,
				Follow:        follow,
			}
			stream, err := client.GetAppMetrics(context.Background(), req)
			if err != nil {
				return fmt.Errorf("failed to get app metrics stream: %w", err)
			}
			var entries []string
			for {
				resp, err := stream.Recv()
				if err == io.EOF {
					break
				}
				if err != nil {
					return fmt.Errorf("stream error: %w", err)
				}
				if tail > 0 {
					entries = append(entries, resp.AppMetrics.String())
				} else {
					fmt.Println(resp.AppMetrics.String())
					fmt.Println()
				}
			}
			for _, e := range tailEntries(entries, tail) {
				fmt.Println(e)
				fmt.Println()
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Follow app metrics")
	addTailFlag(cmd, &tail)
	return cmd
}

func eveAppLogsCmd() *cobra.Command {
	var follow bool
	var tail int
	cmd := &cobra.Command{
		Use:   "app-logs <appname-OR-UUID>",
		Short: "Prints logs received from the given app",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if tail > 0 && follow {
				return fmt.Errorf("--tail and --follow cannot be used together")
			}
			appID := args[0]
			req := &pb.AppRequest{
				DeviceName:    eveDeviceName,
				AppNameOrUuid: appID,
				Follow:        follow,
			}
			stream, err := client.GetAppLogs(context.Background(), req)
			if err != nil {
				return fmt.Errorf("failed to get app logs stream: %w", err)
			}
			var entries []string
			for {
				logMsg, err := stream.Recv()
				if err == io.EOF {
					break
				}
				if err != nil {
					return fmt.Errorf("stream error: %w", err)
				}
				if tail > 0 {
					entries = append(entries, logMsg.String())
				} else {
					fmt.Println(logMsg.String())
				}
			}
			for _, e := range tailEntries(entries, tail) {
				fmt.Println(e)
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Follow app logs")
	addTailFlag(cmd, &tail)
	return cmd
}

func eveAppFlowLogsCmd() *cobra.Command {
	var follow bool
	var tail int
	cmd := &cobra.Command{
		Use:   "flow-logs <app-name-OR-UUID>",
		Short: "Prints flow logs captured for the given application",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if tail > 0 && follow {
				return fmt.Errorf("--tail and --follow cannot be used together")
			}
			appID := args[0]
			req := &pb.AppRequest{
				DeviceName:    eveDeviceName,
				AppNameOrUuid: appID,
				Follow:        follow,
			}
			stream, err := client.GetAppFlowLogs(context.Background(), req)
			if err != nil {
				return fmt.Errorf("failed to get flow logs stream: %w", err)
			}
			var entries []string
			for {
				resp, err := stream.Recv()
				if err == io.EOF {
					break
				}
				if err != nil {
					return fmt.Errorf("stream error: %w", err)
				}
				var lines []string
				for _, ipFlow := range resp.IpFlows {
					lines = append(lines, fmt.Sprintf("IP flow: %s", ipFlow.String()))
				}
				for _, dnsReq := range resp.DnsRequests {
					lines = append(lines, fmt.Sprintf("DNS request: %s", dnsReq.String()))
				}
				entry := strings.Join(lines, "\n")
				if tail > 0 {
					entries = append(entries, entry)
				} else {
					fmt.Println(entry)
				}
			}
			for _, e := range tailEntries(entries, tail) {
				fmt.Println(e)
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Follow flow logs")
	addTailFlag(cmd, &tail)
	return cmd
}

func eveNIInfoCmd() *cobra.Command {
	var follow bool
	var tail int
	cmd := &cobra.Command{
		Use:   "ni-info <ni-name-OR-UUID>",
		Short: "Prints network interface info",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if tail > 0 && follow {
				return fmt.Errorf("--tail and --follow cannot be used together")
			}
			niID := args[0]
			req := &pb.NIRequest{
				DeviceName:   eveDeviceName,
				NiNameOrUuid: niID,
				Follow:       follow,
			}
			stream, err := client.GetNIInfo(context.Background(), req)
			if err != nil {
				return fmt.Errorf("failed to get NI info stream: %w", err)
			}
			var entries []string
			for {
				resp, err := stream.Recv()
				if err == io.EOF {
					break
				}
				if err != nil {
					return fmt.Errorf("stream error: %w", err)
				}
				if tail > 0 {
					entries = append(entries, resp.NiInfo.String())
				} else {
					fmt.Println(resp.NiInfo.String())
					fmt.Println()
				}
			}
			for _, e := range tailEntries(entries, tail) {
				fmt.Println(e)
				fmt.Println()
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Follow NI info updates")
	addTailFlag(cmd, &tail)
	return cmd
}

func eveNIMetricsCmd() *cobra.Command {
	var follow bool
	var tail int
	cmd := &cobra.Command{
		Use:   "ni-metrics <ni-name-OR-UUID>",
		Short: "Prints network instance metrics",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if tail > 0 && follow {
				return fmt.Errorf("--tail and --follow cannot be used together")
			}
			niID := args[0]
			req := &pb.NIRequest{
				DeviceName:   eveDeviceName,
				NiNameOrUuid: niID,
				Follow:       follow,
			}
			stream, err := client.GetNIMetrics(context.Background(), req)
			if err != nil {
				return fmt.Errorf("failed to get NI metrics stream: %w", err)
			}
			var entries []string
			for {
				resp, err := stream.Recv()
				if err == io.EOF {
					break
				}
				if err != nil {
					return fmt.Errorf("stream error: %w", err)
				}
				if tail > 0 {
					entries = append(entries, resp.NiMetrics.String())
				} else {
					fmt.Println(resp.NiMetrics.String())
					fmt.Println()
				}
			}
			for _, e := range tailEntries(entries, tail) {
				fmt.Println(e)
				fmt.Println()
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Follow NI metrics")
	addTailFlag(cmd, &tail)
	return cmd
}

func eveCollectInfoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "collect-info",
		Short: "Collects info for troubleshooting",
		RunE: func(cmd *cobra.Command, args []string) error {
			req := &pb.EVEDeviceRequest{DeviceName: eveDeviceName}
			resp, err := client.CollectInfo(context.Background(), req)
			if err != nil {
				return fmt.Errorf("failed to collect EVE info: %w", err)
			}
			fmt.Printf("EVE info is collected into %q\n", resp.ArtifactPath)
			return nil
		},
	}
}

func eveSSHCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ssh [command args...]",
		Short: "SSH into EVE",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Establish TCP proxy for the EVE SSH access.
			req := &pb.OpenTCPProxyRequest{
				DeviceName: eveDeviceName,
				TargetPort: 22,
			}
			resp, err := client.OpenTCPProxy(context.Background(), req)
			if err != nil {
				return fmt.Errorf("failed to setup EVE SSH proxy: %w", err)
			}
			defer func() {
				closeReq := &pb.CloseTCPProxyRequest{
					ProxyIpAddress: resp.ProxyIpAddress,
					ProxyPort:      resp.ProxyPort,
				}
				_, err = client.CloseTCPProxy(context.Background(), closeReq)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Failed to close EVE SSH proxy: %v\n", err)
				}
			}()

			// Write EVE SSH private key to a temporary file.
			keyFilepath, err := createTmpSSHKeyFile(constants.EVESSHPrivateKey)
			if err != nil {
				return err
			}
			defer func() {
				_ = os.Remove(keyFilepath)
			}()

			// Run the ssh command.
			var sshArgs []string
			sshArgs = append(sshArgs, utils.EveSSHCommonArgs...)
			sshArgs = append(sshArgs,
				"-i", keyFilepath,
				"-p", strconv.Itoa(int(resp.ProxyPort)),
				"root@"+resp.ProxyIpAddress,
			)
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

func createTmpSSHKeyFile(sshKey string) (filepath string, err error) {
	keyFile, err := os.CreateTemp("", "ssh-key-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temporary SSH key file: %w", err)
	}

	// SSH requires private keys to not be world-readable.
	if err := keyFile.Chmod(0600); err != nil {
		keyFile.Close()
		_ = os.Remove(keyFile.Name())
		return "", fmt.Errorf("failed to chmod SSH key file: %w", err)
	}

	if _, err := keyFile.WriteString(sshKey); err != nil {
		keyFile.Close()
		_ = os.Remove(keyFile.Name())
		return "", fmt.Errorf("failed to write SSH key: %w", err)
	}

	if err := keyFile.Close(); err != nil {
		_ = os.Remove(keyFile.Name())
		return "", fmt.Errorf("failed to close SSH key file: %w", err)
	}
	return keyFile.Name(), nil
}

func eveSCPCmd() *cobra.Command {
	var fromDevice bool
	var toDevice bool
	cmd := &cobra.Command{
		Use:   "scp [--from-device|--to-device] <source-path> <dest-path>",
		Short: "SCP files from/to device",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			sourcePath := args[0]
			destPath := args[1]
			if fromDevice && toDevice {
				return fmt.Errorf("cannot specify both --from-device and --to-device")
			}
			if !fromDevice && !toDevice {
				// Default direction
				fromDevice = true
			}

			// Establish TCP proxy for the EVE SSH access.
			req := &pb.OpenTCPProxyRequest{
				DeviceName: eveDeviceName,
				TargetPort: 22,
			}
			resp, err := client.OpenTCPProxy(context.Background(), req)
			if err != nil {
				return fmt.Errorf("failed to setup EVE SSH proxy: %w", err)
			}
			defer func() {
				closeReq := &pb.CloseTCPProxyRequest{
					ProxyIpAddress: resp.ProxyIpAddress,
					ProxyPort:      resp.ProxyPort,
				}
				_, err = client.CloseTCPProxy(context.Background(), closeReq)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Failed to close EVE SSH proxy: %v\n", err)
				}
			}()

			// Write EVE SSH private key to a temporary file.
			keyFilepath, err := createTmpSSHKeyFile(constants.EVESSHPrivateKey)
			if err != nil {
				return err
			}
			defer func() {
				_ = os.Remove(keyFilepath)
			}()

			var scpArgs []string
			scpArgs = append(scpArgs, utils.EveSSHCommonArgs...)
			scpArgs = append(scpArgs,
				"-i", keyFilepath,
				"-P", strconv.Itoa(int(resp.ProxyPort)),
			)

			deviceAddr := "root@" + resp.ProxyIpAddress
			if fromDevice {
				// root@ip:/path -> local
				scpArgs = append(scpArgs,
					fmt.Sprintf("%s:%s", deviceAddr, sourcePath), destPath)
			} else {
				// local -> root@ip:/path
				scpArgs = append(scpArgs,
					sourcePath, fmt.Sprintf("%s:%s", deviceAddr, destPath),
				)
			}

			if err := utils.RunCommandForeground("scp", scpArgs); err != nil {
				return fmt.Errorf("scp command failed: %w", err)
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&fromDevice, "from-device", "f", false,
		"Copy file from device (default)")
	cmd.Flags().BoolVarP(&toDevice, "to-device", "t", false,
		"Copy file to device")
	return cmd
}

func evePortFwdCmd() *cobra.Command {
	var interfaceName string
	cmd := &cobra.Command{
		Use:   "portfwd <source-port>:<target-port>",
		Short: "Forward a local TCP port to a port on the EVE device",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			parts := strings.SplitN(args[0], ":", 2)
			if len(parts) != 2 {
				return fmt.Errorf(
					"invalid port mapping %q, expected <source-port>:<target-port>",
					args[0])
			}
			sourcePort, err := strconv.Atoi(parts[0])
			if err != nil || sourcePort <= 0 || sourcePort > 65535 {
				return fmt.Errorf("invalid source port %q", parts[0])
			}
			targetPort, err := strconv.Atoi(parts[1])
			if err != nil || targetPort <= 0 || targetPort > 65535 {
				return fmt.Errorf("invalid target port %q", parts[1])
			}

			// Establish TCP proxy on the server side.
			req := &pb.OpenTCPProxyRequest{
				DeviceName:    eveDeviceName,
				TargetPort:    uint32(targetPort),
				InterfaceName: interfaceName,
			}
			resp, err := client.OpenTCPProxy(context.Background(), req)
			if err != nil {
				return fmt.Errorf("failed to open TCP proxy: %w", err)
			}
			defer func() {
				closeReq := &pb.CloseTCPProxyRequest{
					ProxyIpAddress: resp.ProxyIpAddress,
					ProxyPort:      resp.ProxyPort,
				}
				_, err = client.CloseTCPProxy(context.Background(), closeReq)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Failed to close TCP proxy: %v\n", err)
				}
			}()

			// Start a local listener on the source port.
			ln, err := net.Listen("tcp",
				net.JoinHostPort("127.0.0.1", parts[0]))
			if err != nil {
				return fmt.Errorf("failed to listen on localhost:%d: %w",
					sourcePort, err)
			}
			var closeLnOnce sync.Once
			closeLn := func() { closeLnOnce.Do(func() { ln.Close() }) }
			defer closeLn()

			fmt.Printf("Forwarding localhost:%d -> EVE device port %d\n",
				sourcePort, targetPort)
			fmt.Println("Press Ctrl+C to stop.")

			serverAddr := net.JoinHostPort(
				resp.ProxyIpAddress, strconv.Itoa(int(resp.ProxyPort)))

			for {
				localConn, err := ln.Accept()
				if err != nil {
					// Listener was closed (e.g. server went away).
					return nil
				}
				go func(c net.Conn) {
					defer c.Close()
					remoteConn, err := net.Dial("tcp", serverAddr)
					if err != nil {
						fmt.Fprintf(os.Stderr,
							"Lost connection to evetest server.\n")
						closeLn()
						return
					}
					defer remoteConn.Close()

					localPipe := utils.ReadWriterPipe{
						PipeName: "local connection",
						RW:       c,
						Buf:      make([]byte, os.Getpagesize()),
					}
					remotePipe := utils.ReadWriterPipe{
						PipeName: "remote proxy connection",
						RW:       remoteConn,
						Buf:      make([]byte, os.Getpagesize()),
					}
					proxyLog := log.WithField("component", "portfwd")
					utils.RunPipeProxy(context.Background(), proxyLog,
						"portfwd", localPipe, remotePipe)
					// After proxy ends, check if server is still reachable.
					probe, err := net.DialTimeout("tcp", serverAddr, 3*time.Second)
					if err != nil {
						fmt.Fprintf(os.Stderr,
							"Lost connection to evetest server.\n")
						closeLn()
						return
					}
					probe.Close()
				}(localConn)
			}
		},
	}
	cmd.Flags().StringVarP(&interfaceName, "interface", "i", "",
		"EVE interface logical label (e.g. eth0)")
	return cmd
}

func eveConsoleCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "console",
		Short: "Connect to the EVE console",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Establish TCP proxy for the EVE console access.
			req := &pb.EVEDeviceRequest{DeviceName: eveDeviceName}
			resp, err := client.ConnectConsoleToEVE(context.Background(), req)
			if err != nil {
				return fmt.Errorf("failed to setup EVE console proxy: %w", err)
			}
			defer func() {
				closeReq := &pb.CloseTCPProxyRequest{
					ProxyIpAddress: resp.ProxyIpAddress,
					ProxyPort:      resp.ProxyPort,
				}
				_, err = client.CloseTCPProxy(context.Background(), closeReq)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Failed to close EVE console proxy: %v\n", err)
				}
			}()

			// Run the telnet command.
			telnetArgs := []string{
				resp.ProxyIpAddress,
				strconv.Itoa(int(resp.ProxyPort)),
			}
			err = utils.RunCommandForeground(
				"telnet", telnetArgs, utils.SetThisProcessStdin())
			if err != nil {
				return fmt.Errorf("telnet command failed: %w", err)
			}
			return nil
		},
	}
	return cmd
}

func eveKubectlCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "kubectl [<kubectl-arg>...]",
		Short: "Run kubectl commands against the EVE device Kubernetes cluster",
		Args:  cobra.ArbitraryArgs, // accept any args kubectl supports
		RunE: func(cmd *cobra.Command, args []string) error {
			// args contains *exactly* what the user passed after `kubectl`
			// Example:
			//   eve --devicename foo kubectl get pods -A
			// args == []string{"get", "pods", "-A"}

			fmt.Println("Device:", eveDeviceName)
			fmt.Println("kubectl args:", args)
			fmt.Println("Kubectl command not fully implemented yet.")

			// TODO: - SSH into EVE, enter kube container and run kubectl
			//       - or can we access kubectl over the k3s API?
			return nil
		},
	}
}
