package main

import (
	"context"
	"fmt"
	"github.com/spf13/viper"
	"net"
	"strconv"

	"github.com/lf-edge/eve/evetest/constants"
	pb "github.com/lf-edge/eve/evetest/grpcapi/go"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	// version is set using "go build -ldflags", this here is just a fallback value.
	version = "v0.0.1"
	client  pb.EvetestClient
)

func main() {
	constants.InitViperConfig()
	grpcClient, err := newGrpcClient()
	if err != nil {
		log.Fatalf("failed to create gRPC client: %v", err)
	}
	defer grpcClient.Close()
	client = pb.NewEvetestClient(grpcClient)

	rootCmd := &cobra.Command{
		Use:   "evetest",
		Short: "evetest CLI for controlling and inspecting EVE test runs",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("evetest CLI version:", version)
			req := &pb.StatusRequest{}
			resp, err := client.Status(context.Background(), req)
			if err != nil {
				log.Warnf("Failed to get evetest (backend) version:: %v", err)
			} else {
				fmt.Println("evetest (backend) version:", resp.EvetestVersion)
			}
			cmd.Help()
		},
	}

	rootCmd.AddCommand(
		continueCmd(),
		restartCmd(),
		exitCmd(),
		statusCmd(),
		eveCommand(),
		clusterCommand(),
		sdnCommand(),
	)

	if err := rootCmd.Execute(); err != nil {
		log.Fatalf("error executing command: %v", err)
	}
}

func newGrpcClient() (*grpc.ClientConn, error) {
	ip := viper.GetString(constants.APIAddressEnv)
	if ip == "" {
		// evetest gRPC server address is unset.
		// Assume that the evetest container runs on the same host.
		ip = "localhost"
	}
	port := strconv.Itoa(viper.GetInt(constants.APIPortEnv))
	address := net.JoinHostPort("localhost", port)
	return grpc.NewClient(
		address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
}

func continueCmd() *cobra.Command {
	var until string
	cmd := &cobra.Command{
		Use:   "continue",
		Short: "Continue test execution until a checkpoint, test completion, or failure",
		Run: func(cmd *cobra.Command, args []string) {
			req := &pb.ContinueRequest{UntilCheckpoint: until}
			_, err := client.Continue(context.Background(), req)
			if err != nil {
				log.Fatalf("Continue RPC failed: %v", err)
			}
			fmt.Println("Test continues")
		},
	}
	cmd.Flags().StringVar(&until, "until", "", "continue until this checkpoint")
	return cmd
}

func restartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restart",
		Short: "Restart the same test",
		Run: func(cmd *cobra.Command, args []string) {
			req := &pb.RestartRequest{}
			_, err := client.Restart(context.Background(), req)
			if err != nil {
				log.Fatalf("Restart RPC failed: %v", err)
			}
			fmt.Println("Test was restarted")
		},
	}
}

func exitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "exit",
		Short: "Exit the test early",
		Run: func(cmd *cobra.Command, args []string) {
			req := &pb.ExitRequest{}
			_, err := client.Exit(context.Background(), req)
			if err != nil {
				log.Fatalf("Exit RPC failed: %v", err)
			}
			fmt.Println("Test exited")
		},
	}
}

func printEVEDevices(devices []*pb.EVEDevice) {
	if len(devices) == 0 {
		fmt.Println("No EVE devices found.")
		return
	}
	fmt.Println("EVE Devices:")
	for i, dev := range devices {
		fmt.Printf("  Device #%d:\n", i+1)
		fmt.Printf("    Name:         %s\n", dev.DeviceName)
		fmt.Printf("    CPUs:         %d\n", dev.Cpus)
		fmt.Printf("    Memory:       %.2f GiB\n", float64(dev.MemoryBytes)/(1<<30))
		// Interfaces
		if len(dev.Interfaces) == 0 {
			fmt.Println("    Interfaces:   none")
		} else {
			fmt.Println("    Interfaces:")
			for _, iface := range dev.Interfaces {
				fmt.Printf("      - Name:            %s\n", iface.Name)
				fmt.Printf("        MAC Address:     %s\n", iface.MacAddress)
				fmt.Printf("        SDN MAC Address: %s\n", iface.SdnMacAddress)
			}
		}
		// Image
		if dev.Image != nil {
			fmt.Println("    Image:")
			fmt.Printf("      Repo:        %s\n", dev.Image.Repo)
			fmt.Printf("      Version:     %s\n", dev.Image.Version)
			fmt.Printf("      Hypervisor:  %s\n", dev.Image.Hypervisor.String())
			fmt.Printf("      Arch:        %s\n", dev.Image.Arch.String())
		} else {
			fmt.Println("    Image:         <none>")
		}
		fmt.Println()
	}
}

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Get current test execution status",
		Run: func(cmd *cobra.Command, args []string) {
			req := &pb.StatusRequest{}
			resp, err := client.Status(context.Background(), req)
			if err != nil {
				log.Fatalf("Status RPC failed: %v", err)
			}
			fmt.Println("Running test:", resp.TestName)
			if resp.TestSuiteName != "" {
				fmt.Println("From test suite:", resp.TestSuiteName)
			}
			if resp.Paused {
				if resp.TestFailure != "" {
					fmt.Println("Test failed with:", resp.TestFailure)
				} else {
					fmt.Println("Paused at checkpoint:", resp.CurrentCheckpoint)
				}
			}
			printEVEDevices(resp.EveDevices)
		},
	}
}
