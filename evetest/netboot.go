// Copyright (c) 2026 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package evetest

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	api "github.com/lf-edge/eve/evetest/grpcapi/go"
	"github.com/lf-edge/eve/evetest/utils"
)

const netbootArtifactBuildTimeout = 15 * time.Minute

// buildNetbootArtifacts builds the same artifact bundle that `make
// installer-net` produces (see docs/BOOT-INSTALLER.md's "PXE" section and
// pkg/eve/runme.sh's do_installer_net) for dev's target EVE image, with the
// device's onboarding cert/bootstrap config/grub options injected into its
// config.img via the "/in" volume mount (the same mechanism
// prepareImageForEVEDevice/the broker use for disk-based devices -- see
// buildEveConfig/utils.MakeEVEConfigDir), and stages it directly under the
// harness's own HTTP/HTTPS/SFTP/TFTP image server root.
//
// This runs in the main evetest process rather than through the broker: the
// bundle is built directly from the target EVE docker image using the local
// Docker daemon, and the EVE devices fetch it directly from evetest's own image
// server (see harness.go's TFTP listener).
//
// ipxe.efi, ipxe.efi.cfg and EFI/BOOT/BOOTX64.EFI are shared across every
// netboot device in the test: grub's own EFI binary hardcodes its config-file
// search to "(whatever device it booted from)/EFI/BOOT/grub.cfg[-variant]" at
// build time (grub-mkimage -p /EFI/BOOT, see pkg/grub/Dockerfile), with no way
// to parameterize a per-device subdirectory, so these MUST live at the image
// server's root. When multiple netboot devices are set up in the same test
// (e.g. a cluster), the last device's build simply overwrites them -- harmless,
// since none of them vary with EVE version/config.
//
// installer.iso is the one artifact that DOES need to be device-specific (it
// carries this device's own injected onboarding cert/config), so it is renamed
// to installer-<dev.name>.iso; and one EFI/BOOT/grub.cfg-01-<mac> is generated
// per MAC address in macs (grub's own built-in per-client config lookup, the
// same convention PXELINUX's pxelinux.cfg/<mac> uses -- see
// grub_net_search_config_file in grub-core/net/net.c), each patched to
// loopback-mount this device's own installer ISO instead of the generic
// installer.iso the stock file references -- see writeGrubCfgForMAC.
func (th *TestHarness) buildNetbootArtifacts(dev *deviceState, macs []net.HardwareAddr) {
	ctx, cancel := context.WithTimeout(th.ctx, netbootArtifactBuildTimeout)
	defer cancel()
	logger := th.log.WithField("component", "netboot")

	if err := utils.PullDockerImage(ctx, logger, dev.imageName); err != nil {
		th.t.Fatalf("Failed to pull EVE image %s for netboot artifacts: %v",
			dev.imageName, err)
	}

	archStr := "amd64"
	if dev.imageRef.GetArch() == api.ArchType_ARCH_ARM64 {
		archStr = "arm64"
	}
	platform := "linux/" + archStr

	softSerial := dev.requirement.WithSoftSerial
	if softSerial == "" {
		softSerial = uuid.NewString()
	}
	configDir, err := utils.MakeEVEConfigDir(
		th.imgServerDir, th.buildEveConfig(dev), nil, softSerial)
	if err != nil {
		th.t.Fatalf("Failed to prepare EVE config dir for netboot artifacts: %v", err)
	}
	if configDir != "" {
		defer os.RemoveAll(configDir)
	}

	th.log.Infof("Building netboot artifacts from %s", dev.imageName)
	volumeMap := map[string]string{"/out": th.imgServerDir}
	if configDir != "" {
		volumeMap["/in"] = configDir
	}
	if _, err := utils.RunDockerCommand(ctx, logger, dev.imageName, "installer_net",
		volumeMap, platform); err != nil {
		th.t.Fatalf("Failed to build the installer_net bundle from %s: %v",
			dev.imageName, err)
	}

	bundleTar := filepath.Join(th.imgServerDir, "installer.net")
	f, err := os.Open(bundleTar)
	if err != nil {
		th.t.Fatalf("Failed to open the built installer.net bundle %s: %v", bundleTar, err)
	}
	if err := utils.ExtractFromTar(f, th.imgServerDir); err != nil {
		f.Close()
		th.t.Fatalf("Failed to extract the installer.net bundle into %s: %v",
			th.imgServerDir, err)
	}
	f.Close()
	if err := os.Remove(bundleTar); err != nil {
		th.t.Fatalf("Failed to remove extracted bundle tar %s: %v", bundleTar, err)
	}

	if _, err := findGrubEFIName(th.imgServerDir); err != nil {
		th.t.Fatalf("%v", err)
	}
	if err := setIpxeScriptURL(th.imgServerDir); err != nil {
		th.t.Fatalf("%v", err)
	}

	installerISO := GetImageServerInstallerISOFilename(dev.name)
	if err := os.Rename(
		filepath.Join(th.imgServerDir, "installer.iso"),
		filepath.Join(th.imgServerDir, installerISO)); err != nil {
		th.t.Fatalf("Failed to rename installer.iso for %s: %v", dev.name, err)
	}

	for _, mac := range macs {
		if err := writeGrubCfgForMAC(th.imgServerDir, mac, installerISO); err != nil {
			th.t.Fatalf("%v", err)
		}
	}
	stockGrubCfg := filepath.Join(th.imgServerDir, "EFI", "BOOT", "grub.cfg")
	if err := os.Remove(stockGrubCfg); err != nil {
		th.t.Fatalf("Failed to remove generic netboot grub.cfg %s: %v", stockGrubCfg, err)
	}

	th.log.Infof("Netboot artifacts for %s staged at %s (installer=%s)",
		dev.imageName, th.imgServerDir, installerISO)
}

// ipxeScriptFilename is EVE's boot script inside the extracted netboot
// bundle, chainloaded by ipxe.efi (see the dnsmasq iPXE-detection rule in
// sdn/vm/pkg/configitems/dhcpSrv.go, which hands ipxe.efi's second, "iPXE"-
// tagged DHCP request this file instead of ipxe.efi itself).
const ipxeScriptFilename = "ipxe.efi.cfg"

// setIpxeScriptURL prepends a "set url ..." line to the extracted
// ipxe.efi.cfg, pointing it at the image server's root. Every chainload the
// script performs (EFI/BOOT/BOOTX64.EFI, and from there grub's own
// grub.cfg/module fetches) is relative to ${url}. pkg/eve/runme.sh's
// do_installer_net copies pkg/eve/installer/ipxe.efi.cfg into the bundle
// verbatim, with no "set url ..." of its own -- this is not an oversight:
// docs/DEPLOYMENT.md's "Running the installer image via iPXE" section
// documents editing this exact line by hand as the deployment step that
// points the stock script at wherever the artifacts actually got published.
// This does the same edit programmatically; without it, every fetch after
// the script itself resolves against the wrong protocol/host default.
func setIpxeScriptURL(imgServerDir string) error {
	scriptPath := filepath.Join(imgServerDir, ipxeScriptFilename)
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", scriptPath, err)
	}
	shebang, rest, ok := bytes.Cut(content, []byte("\n"))
	if !ok || string(shebang) != "#!ipxe" {
		return fmt.Errorf("%s does not start with the expected #!ipxe shebang", scriptPath)
	}
	url := fmt.Sprintf("tftp://%s/", GetImageServerIPv4())
	var patched bytes.Buffer
	patched.Write(shebang)
	patched.WriteString("\nset url ")
	patched.WriteString(url)
	patched.WriteByte('\n')
	patched.Write(rest)
	if err := os.WriteFile(scriptPath, patched.Bytes(), 0644); err != nil {
		return fmt.Errorf("failed to write patched %s: %w", scriptPath, err)
	}
	return nil
}

// writeGrubCfgForMAC generates one per-device netboot grub.cfg: the stock
// EFI/BOOT/grub.cfg (do_installer_net's wrapper, which loopback-mounts the
// generic "installer.iso") patched to loopback-mount installerISO instead,
// staged under mac's filename in grub's own PXE-style client config lookup
// convention (hardware type 01 = Ethernet, hyphenated hex MAC bytes -- the
// same algorithm PXELINUX's pxelinux.cfg/<mac> convention uses; see
// grub_net_search_config_file in grub-core/net/net.c). This is the only way
// to give each device its own grub.cfg, since grub's own $prefix is fixed to
// "(whatever device it booted from)/EFI/BOOT" with no room for a per-device
// subdirectory -- see buildNetbootArtifacts' doc comment.
func writeGrubCfgForMAC(imgServerDir string, mac net.HardwareAddr, installerISO string) error {
	stockPath := filepath.Join(imgServerDir, "EFI", "BOOT", "grub.cfg")
	content, err := os.ReadFile(stockPath)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", stockPath, err)
	}
	const stockRef = "($cmddevice)/installer.iso"
	patchedRef := fmt.Sprintf("($cmddevice)/%s", installerISO)
	patched := bytes.Replace(content, []byte(stockRef), []byte(patchedRef), 1)
	if bytes.Equal(patched, content) {
		return fmt.Errorf("%s does not contain the expected %q reference",
			stockPath, stockRef)
	}
	macFilename := "grub.cfg-01-" + strings.ReplaceAll(mac.String(), ":", "-")
	macPath := filepath.Join(imgServerDir, "EFI", "BOOT", macFilename)
	if err := os.WriteFile(macPath, patched, 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", macPath, err)
	}
	return nil
}

// findGrubEFIName returns the basename of the single arch-specific
// EFI/BOOT/BOOT*.EFI grub bootloader inside an extracted installer.net bundle
// (e.g. BOOTX64.EFI, BOOTAA64.EFI). Only used to fail fast with a clear error
// if the bundle turns out not to contain it; ipxe.efi.cfg itself already
// picks the right name at runtime based on ${buildarch}.
func findGrubEFIName(bundleDir string) (string, error) {
	matches, err := filepath.Glob(filepath.Join(bundleDir, "EFI", "BOOT", "BOOT*.EFI"))
	if err != nil {
		return "", fmt.Errorf("failed to glob for the grub EFI bootloader: %w", err)
	}
	if len(matches) != 1 {
		return "", fmt.Errorf(
			"expected exactly one EFI/BOOT/BOOT*.EFI file in %s, found %d",
			bundleDir, len(matches))
	}
	return filepath.Base(matches[0]), nil
}
