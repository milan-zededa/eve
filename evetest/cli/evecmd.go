package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/lf-edge/eve/evetest/constants"
	pb "github.com/lf-edge/eve/evetest/grpcapi/go"
	"github.com/lf-edge/eve/evetest/utils"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"
)

var eveDeviceName string

func eveCommand() *cobra.Command {
	eveCmd := &cobra.Command{
		Use:   "eve",
		Short: "Commands for interacting with EVE devices",
	}
	eveCmd.PersistentFlags().StringVar(&eveDeviceName, "devicename", "", "EVE device name")
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
		eveConsoleCmd(),
		eveKubectlCmd(),
	)
	return eveCmd
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
	cmd := &cobra.Command{
		Use:   "info",
		Short: "Prints device info (HW specs, adapter info, etc.)",
		RunE: func(cmd *cobra.Command, args []string) error {
			req := &pb.EVEDeviceStreamableRequest{
				DeviceName: eveDeviceName,
				Follow:     follow,
			}
			stream, err := client.GetEVEInfo(context.Background(), req)
			if err != nil {
				return fmt.Errorf("failed to get EVE info stream: %w", err)
			}
			for {
				resp, err := stream.Recv()
				if err == io.EOF {
					break
				}
				if err != nil {
					return fmt.Errorf("stream error: %w", err)
				}
				fmt.Println(resp.DeviceInfo.String())
				if !follow {
					break
				}
				fmt.Println()
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Follow device info updates")
	return cmd
}

func eveMetricsCmd() *cobra.Command {
	var follow bool
	cmd := &cobra.Command{
		Use:   "metrics",
		Short: "Prints device metrics",
		RunE: func(cmd *cobra.Command, args []string) error {
			req := &pb.EVEDeviceStreamableRequest{
				DeviceName: eveDeviceName,
				Follow:     follow,
			}
			stream, err := client.GetEVEMetrics(context.Background(), req)
			if err != nil {
				return fmt.Errorf("failed to get device metrics stream: %w", err)
			}
			for {
				resp, err := stream.Recv()
				if err == io.EOF {
					break
				}
				if err != nil {
					return fmt.Errorf("stream error: %w", err)
				}
				fmt.Println(resp.DeviceMetrics.String())
				if !follow {
					break
				}
				fmt.Println()
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Follow device metrics")
	return cmd
}

func eveLogsCmd() *cobra.Command {
	var follow bool
	cmd := &cobra.Command{
		Use:   "logs",
		Short: "Prints all device logs",
		RunE: func(cmd *cobra.Command, args []string) error {
			req := &pb.EVEDeviceStreamableRequest{
				DeviceName: eveDeviceName,
				Follow:     follow,
			}
			stream, err := client.GetEVELogs(context.Background(), req)
			if err != nil {
				return fmt.Errorf("failed to get device logs stream: %w", err)
			}
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
				fmt.Printf("%s|%s|%s| %s\n",
					ts, severity, logMsg.Source, logMsg.Message)
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Follow logs")
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
	cmd := &cobra.Command{
		Use:   "app-info <app-name-OR-UUID>",
		Short: "Prints application info for the given app",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
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
			for {
				resp, err := stream.Recv()
				if err == io.EOF {
					break
				}
				if err != nil {
					return fmt.Errorf("stream error: %w", err)
				}
				fmt.Println(resp.AppInfo.String())
				if !follow {
					break
				}
				fmt.Println()
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Follow app info updates")
	return cmd
}

func eveAppMetricsCmd() *cobra.Command {
	var follow bool
	cmd := &cobra.Command{
		Use:   "app-metrics <appname-OR-UUID>",
		Short: "Prints application metrics for the given app",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
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
			for {
				resp, err := stream.Recv()
				if err == io.EOF {
					break
				}
				if err != nil {
					return fmt.Errorf("stream error: %w", err)
				}
				fmt.Println(resp.AppMetrics.String())
				if !follow {
					break
				}
				fmt.Println()
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Follow app metrics")
	return cmd
}

func eveAppLogsCmd() *cobra.Command {
	var follow bool
	cmd := &cobra.Command{
		Use:   "app-logs <appname-OR-UUID>",
		Short: "Prints logs received from the given app",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
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
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Follow app logs")
	return cmd
}

func eveAppFlowLogsCmd() *cobra.Command {
	var follow bool
	cmd := &cobra.Command{
		Use:   "flow-logs <app-name-OR-UUID>",
		Short: "Prints flow logs captured for the given application",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
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
			for {
				resp, err := stream.Recv()
				if err == io.EOF {
					break
				}
				if err != nil {
					return fmt.Errorf("stream error: %w", err)
				}
				for _, ipFlow := range resp.IpFlows {
					fmt.Printf("IP flow: %s\n", ipFlow.String())
				}
				for _, dnsReq := range resp.DnsRequests {
					fmt.Printf("DNS request: %s\n", dnsReq.String())
				}
				if !follow {
					break
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Follow flow logs")
	return cmd
}

func eveNIInfoCmd() *cobra.Command {
	var follow bool
	cmd := &cobra.Command{
		Use:   "ni-info <ni-name-OR-UUID>",
		Short: "Prints network interface info",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
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
			for {
				resp, err := stream.Recv()
				if err == io.EOF {
					break
				}
				if err != nil {
					return fmt.Errorf("stream error: %w", err)
				}
				fmt.Println(resp.NiInfo.String())
				if !follow {
					break
				}
				fmt.Println()
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Follow NI info updates")
	return cmd
}

func eveNIMetricsCmd() *cobra.Command {
	var follow bool
	cmd := &cobra.Command{
		Use:   "ni-metrics <ni-name-OR-UUID>",
		Short: "Prints network instance metrics",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
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
			for {
				resp, err := stream.Recv()
				if err == io.EOF {
					break
				}
				if err != nil {
					return fmt.Errorf("stream error: %w", err)
				}
				fmt.Println(resp.NiMetrics.String())
				if !follow {
					break
				}
				fmt.Println()
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Follow NI metrics")
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
			req := &pb.EVEDeviceRequest{DeviceName: eveDeviceName}
			resp, err := client.ConnectSSHToEVE(context.Background(), req)
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
			req := &pb.EVEDeviceRequest{DeviceName: eveDeviceName}
			resp, err := client.ConnectSSHToEVE(context.Background(), req)
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

			// Run the SCP command.
			scpArgs := []string{
				"-o", "IdentitiesOnly=yes",
				"-o", "ConnectTimeout=5",
				"-o", "StrictHostKeyChecking=no",
				"-o", "UserKnownHostsFile=/dev/null",
				"-i", keyFilepath,
				"-P", strconv.Itoa(int(resp.ProxyPort)),
			}

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
	cmd.Flags().BoolVar(&fromDevice, "from-device", false,
		"Copy file from device (default)")
	cmd.Flags().BoolVar(&toDevice, "to-device", false,
		"Copy file to device")
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
