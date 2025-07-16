package evetest

import (
	"fmt"
	"strings"

	api "github.com/lf-edge/eve/evetest/grpcapi/go"
	pillartypes "github.com/lf-edge/eve/pkg/pillar/types"
)

type Requirement interface {
	isRequirement()
}

type Hypervisor int

const (
	HypervisorUndefined Hypervisor = iota
	HypervisorKVM
	HypervisorXen
	HypervisorKubevirt
)

func (h Hypervisor) String() string {
	switch h {
	case HypervisorKVM:
		return "kvm"
	case HypervisorXen:
		return "xen"
	case HypervisorKubevirt:
		return "kubevirt"
	case HypervisorUndefined:
		fallthrough
	default:
		return "undefined"
	}
}

func (h *Hypervisor) FromString(s string) error {
	switch strings.ToLower(s) {
	case "kvm":
		*h = HypervisorKVM
	case "xen":
		*h = HypervisorXen
	case "kubevirt":
		*h = HypervisorKubevirt
	case "", "undefined":
		*h = HypervisorUndefined
	default:
		return fmt.Errorf("invalid Hypervisor: %q", s)
	}
	return nil
}

type Filesystem int

const (
	FilesystemUndefined Filesystem = iota
	FilesystemEXT4
	FilesystemZFS
)

func (f *Filesystem) FromString(s string) error {
	switch strings.ToLower(s) {
	case "ext4":
		*f = FilesystemEXT4
	case "zfs":
		*f = FilesystemZFS
	case "", "undefined":
		*f = FilesystemUndefined
	default:
		return fmt.Errorf("invalid Filesystem: %q", s)
	}
	return nil
}

func (f Filesystem) String() string {
	switch f {
	case FilesystemEXT4:
		return "ext4"
	case FilesystemZFS:
		return "zfs"
	case FilesystemUndefined:
		fallthrough
	default:
		return "undefined"
	}
}

// ExistingEdgeDeviceReusePolicy defines how to re-use an existing EdgeDevice
// that already satisfies test requirements. Only one strategy can be selected.
// This helps control whether to reuse as-is, reset, or recreate the edge device
// before test execution.
type ExistingEdgeDeviceReusePolicy int

const (
	// UseAsIs : do nothing special, keep existing state.
	UseAsIs ExistingEdgeDeviceReusePolicy = iota
	// RebootEdgeDevice : just reboot edge device matching the requirements.
	RebootEdgeDevice
	// ResetDeviceConfig : reset the device configuration by clearing all
	// application-related settings while preserving the device network configuration.
	ResetDeviceConfig
	// ResetDeviceConfigAndReboot : combines ResetDeviceConfig with RebootEdgeDevice.
	ResetDeviceConfigAndReboot
	// ReonboardEdgeDevice forces re-onboarding of the device, even if it was previously
	// onboarded.
	// It removes the OnboardingStatus and edge device certificate, clears TPM, recreates
	// the device entry in the controller, resets device configuration (see ResetDeviceConfig)
	// and then reboots the device.
	ReonboardEdgeDevice
	// CreateFromScratchWithInstaller : re-create VM even if already exists using
	// EVE installer image.
	CreateFromScratchWithInstaller
	// CreateFromScratchWithLiveImage : re-create VM even if already exists using
	// EVE live image.
	CreateFromScratchWithLiveImage
)

type USBDevice struct {
	VendorID  uint16
	ProductID uint16
}

type PCIDevice struct {
	VendorID uint16
	DeviceID uint16
}

// RequireEdgeDevice : requirement to deploy single EVE device.
type RequireEdgeDevice struct {
	// Logical name used to reference the device within the evetest framework.
	Name string

	// Zero values mean that the test does not care about the particular resource size.
	// None of these will be ever created with zero count - not even ethernet interfaces.
	MinCPUs         uint8  // Default will be 4.
	MinRAMInMB      uint32 // Default will be 4096 MB.
	MinDiskSizeInMB uint32 // Default will be 8192 MB.

	WithEVEVersion string
	WithHypervisor Hypervisor
	WithFilesystem Filesystem // TODO
	WithTPM        bool

	// Configuration injected into the /config partition.
	WithSoftSerial              string
	WithInjectedBootstrapConfig *EdgeDeviceConfig
	WithInjectedNetworkOverride *pillartypes.DevicePortConfig
	// framework automatically adds SSH key and enables console
	WithInjectedConfigProperties *pillartypes.ConfigItemValueMap

	// USB/PCI devices to passthrough into the edge device VM.
	WithUSBPassthrough []USBDevice // TODO
	WithPCIPassthrough []PCIDevice // TODO

	// What to do if EdgeDevice is already available (and still manageable)
	// from the previous test:
	DeviceReusePolicy ExistingEdgeDeviceReusePolicy
}

func (r RequireEdgeDevice) isRequirement() {}

// RequireNetworkModel : required Evetest-SDN network model.
type RequireNetworkModel struct {
	// NetworkModel.ControllerConfig is filled by the framework, do not set from the test.
	*api.NetworkModel
}

func (r RequireNetworkModel) isRequirement() {}

// RequireInternetConnectivity : requirement to provide IPv4/IPv6 Internet connectivity.
type RequireInternetConnectivity struct {
	RequireIPv6 bool
}

func (r RequireInternetConnectivity) isRequirement() {}
