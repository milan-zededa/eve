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
// device's onboarding cert/bootstrap config/grub options injected via the
// "/in" volume mount, and stages it at the harness's own image server root.
// This runs in the main evetest process rather than through the broker,
// using the local Docker daemon directly.
//
// ipxe.efi, ipxe.efi.cfg and EFI/BOOT/BOOTX64.EFI are shared across every
// netboot device in the test: grub's own EFI binary hardcodes its
// config-file search to the image server's root (grub-mkimage -p /EFI/BOOT,
// see pkg/grub/Dockerfile), leaving no room for a per-device subdirectory.
// Sharing them is harmless, since none of them vary with EVE version/config.
//
// installer.iso does need to be device-specific (it carries this device's
// own injected config), which matters only once multiNetboot is true (more
// than one CreateFromScratchWithNetworkBoot device in the test): it is then
// renamed to installer-<dev.name>.iso, and one EFI/BOOT/grub.cfg-01-<mac> is
// generated per MAC in macs (grub's own per-client config lookup) pointing
// at it via installer_name -- see writeGrubCfgForMAC. With a single netboot
// device, the stock installer.iso/EFI/BOOT/grub.cfg pairing is left
// untouched.
func (th *TestHarness) buildNetbootArtifacts(
	dev *deviceState, macs []net.HardwareAddr, multiNetboot bool) {
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
		th.t.Fatalf("Failed to open the built installer.net bundle %s: %v",
			bundleTar, err)
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

	installerISO := "installer.iso"
	if multiNetboot {
		installerISO = "installer-" + dev.name + ".iso"
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
			th.t.Fatalf("Failed to remove generic netboot grub.cfg %s: %v",
				stockGrubCfg, err)
		}
	} else if err := switchGrubCfgInstallerToHTTP(th.imgServerDir); err != nil {
		th.t.Fatalf("%v", err)
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
// ipxe.efi.cfg, pointing every chainload it performs (EFI/BOOT/BOOTX64.EFI
// and on) at the image server's root. The stock script ships with no
// "set url ..." of its own -- docs/DEPLOYMENT.md documents editing this
// line by hand as a deployment step; this does the same edit
// programmatically.
func setIpxeScriptURL(imgServerDir string) error {
	scriptPath := filepath.Join(imgServerDir, ipxeScriptFilename)
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", scriptPath, err)
	}
	shebang, rest, ok := bytes.Cut(content, []byte("\n"))
	if !ok || string(shebang) != "#!ipxe" {
		return fmt.Errorf("%s does not start with the expected #!ipxe shebang",
			scriptPath)
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

// setGrubVariables adds a "set cmddevice=http,<image-server-ip>" line to
// content, so grub fetches over HTTP instead of TFTP. When installerName is
// non-empty, it also replaces the stock "set installer_name=installer.iso"
// line, telling grub_installer.cfg which image to fetch for this device.
func setGrubVariables(content []byte, installerName string) ([]byte, error) {
	patched := append(
		[]byte(fmt.Sprintf("set cmddevice=http,%s\n", GetImageServerIPv4())), content...)
	if installerName == "" {
		return patched, nil
	}
	const stockLine = "set installer_name=installer.iso"
	newLine := "set installer_name=" + installerName
	replaced := bytes.Replace(patched, []byte(stockLine), []byte(newLine), 1)
	if bytes.Equal(replaced, patched) {
		return nil, fmt.Errorf("grub.cfg does not contain %q", stockLine)
	}
	return replaced, nil
}

// switchGrubCfgInstallerToHTTP patches EFI/BOOT/grub.cfg in place (see
// setGrubVariables) for the single-netboot-device case, where installer.iso
// keeps its stock name and there's no need for a per-MAC grub.cfg.
func switchGrubCfgInstallerToHTTP(imgServerDir string) error {
	path := filepath.Join(imgServerDir, "EFI", "BOOT", "grub.cfg")
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", path, err)
	}
	patched, err := setGrubVariables(content, "")
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if err := os.WriteFile(path, patched, 0644); err != nil {
		return fmt.Errorf("failed to write patched %s: %w", path, err)
	}
	return nil
}

// writeGrubCfgForMAC generates one per-device netboot grub.cfg (see
// setGrubVariables), staged under mac's filename in grub's own PXE-style
// client config lookup convention (hardware type 01 = Ethernet, hyphenated
// hex MAC bytes -- the same convention PXELINUX's pxelinux.cfg/<mac> uses).
func writeGrubCfgForMAC(
	imgServerDir string, mac net.HardwareAddr, installerISO string) error {
	stockPath := filepath.Join(imgServerDir, "EFI", "BOOT", "grub.cfg")
	content, err := os.ReadFile(stockPath)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", stockPath, err)
	}
	patched, err := setGrubVariables(content, installerISO)
	if err != nil {
		return fmt.Errorf("%s: %w", stockPath, err)
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
