// Copyright (c) 2026 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package genericitems

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lf-edge/eve-libs/depgraph"
	"github.com/lf-edge/eve-libs/reconciler"
	"github.com/lf-edge/eve/pkg/pillar/base"
	"github.com/lf-edge/eve/pkg/pillar/types"
	"github.com/lf-edge/eve/pkg/pillar/utils/generics"
	"github.com/lf-edge/eve/pkg/pillar/utils/proc"
)

// Put config and PID files into the run directory of NIM.
const nimRunDir = "/run/nim"

const (
	wpaSupplicantStartTimeout = 5 * time.Second
	wpaSupplicantStopTimeout  = 10 * time.Second
)

// WpaSupplicant represents a Wi-Fi Protected Access client and IEEE 802.1X supplicant.
// It defines authentication configuration for a specific network adapter.
// See: https://linux.die.net/man/8/wpa_supplicant
type WpaSupplicant struct {
	// AdapterLL is the logical label of the target network adapter.
	AdapterLL string

	// AdapterIfName is the OS-level interface name of the target adapter.
	AdapterIfName string

	// Exactly one of WiFiConfigs or PNACConfig must be configured:

	// WiFiConfigs defines SSID-based Wi-Fi network configurations using
	// username/password authentication.
	// Supports WPA/WPA2 Personal (PSK) and password-based Enterprise Wi-Fi
	// networks (PEAP).
	WiFiConfigs []WifiConfig

	// PNACConfig defines port-based IEEE 802.1X configuration using EAP-TLS.
	// Intended for port-based network access control (PNAC) of physical adapters,
	// rather than for SSID-based Wi-Fi networks.
	PNACConfig *PNAC8021XConfig
}

// WifiConfig defines SSID-based Wi-Fi network configuration.
// Supports WPA/WPA2 Personal (PSK) and password-based Enterprise (PEAP)
// Wi-Fi authentication.
type WifiConfig struct {
	// SSID is the name of the Wi-Fi network to connect to.
	SSID string

	// KeyScheme specifies the Wi-Fi key management scheme (WPA-PSK, WPA-EAP).
	KeyScheme types.WifiKeySchemeType

	// Identity used for password-based Enterprise Wi-Fi authentication.
	// Ignored for personal (PSK-based) networks.
	Identity string

	// Pre-hashed EAP password in wpa_supplicant format.
	// The value must be generated using `wpa_passphrase`, which derives a
	// PBKDF2-HMAC-SHA1 hash from the plaintext password and the network SSID.
	PasswordHash string

	// Priority controls the preference of this network when multiple networks
	// are available. Higher values are preferred. Zero (default) means lowest priority.
	Priority int32
}

// String returns human-readable WifiConfig description without the sensitive
// password hash.
func (c WifiConfig) String() string {
	return fmt.Sprintf("SSID=%q, KeyScheme=%s, Identity=%q, Priority=%d",
		c.SSID, c.KeyScheme.ToProto().String(), c.Identity, c.Priority)
}

// PNAC8021XConfig defines per-port IEEE 802.1X configuration using EAP-TLS.
// This is typically used for port-based network access control (PNAC)
// on physical adapters.
type PNAC8021XConfig struct {
	// Optional EAP identity presented during authentication.
	// May differ from the certificate subject or SAN.
	EAPIdentity string

	// CACertChain contains the trusted CA certificate chain used to verify
	// the authentication server’s TLS certificate .
	CACertChain []*x509.Certificate

	// Path to the client certificate used for EAP-TLS authentication.
	ClientCertPath string

	// Path to the client private key used for EAP-TLS authentication.
	ClientKeyPath string
}

func equalCertificates(c1, c2 *x509.Certificate) bool {
	if c1 == nil || c2 == nil {
		return c1 == c2
	}
	return c1.Equal(c2)
}

// Equal is a comparison method for PNAC8021XConfig.
func (c *PNAC8021XConfig) Equal(other *PNAC8021XConfig) bool {
	if c == nil || other == nil {
		return c == other
	}
	return c.EAPIdentity == other.EAPIdentity &&
		generics.EqualSetsFn(c.CACertChain, other.CACertChain, equalCertificates) &&
		c.ClientCertPath == other.ClientCertPath &&
		c.ClientKeyPath == other.ClientKeyPath
}

// String returns a human-readable description of PNAC8021XConfig.
// Only certificate subjects are included (not full certificates).
func (c *PNAC8021XConfig) String() string {
	if c == nil {
		return "<nil>"
	}

	var caCertSubjects []string
	for _, cert := range c.CACertChain {
		if cert == nil {
			continue
		}
		caCertSubjects = append(caCertSubjects, cert.Subject.String())
	}

	return fmt.Sprintf(
		"EAPIdentity=%q, CACerts=[%s], ClientCertPath=%q, ClientKeyPath=%q",
		c.EAPIdentity,
		strings.Join(caCertSubjects, ", "),
		c.ClientCertPath,
		c.ClientKeyPath,
	)
}

// Name is based on the adapter interface name (one supplicant per interface).
func (s WpaSupplicant) Name() string {
	return s.AdapterIfName
}

// Label is more human-readable than name.
func (s WpaSupplicant) Label() string {
	return "wpa_supplicant for " + s.AdapterLL
}

// Type of the item.
func (s WpaSupplicant) Type() string {
	return WpaSupplicantTypename
}

// Equal is a comparison method for two equally-named WpaSupplicant instances.
func (s WpaSupplicant) Equal(other depgraph.Item) bool {
	s2, isWpaSupplicant := other.(WpaSupplicant)
	if !isWpaSupplicant {
		return false
	}
	return generics.EqualSets(s.WiFiConfigs, s2.WiFiConfigs) &&
		s.PNACConfig.Equal(s2.PNACConfig)
}

// External returns false.
func (s WpaSupplicant) External() bool {
	return false
}

// String returns a human-readable description of the wpa_supplicant configuration.
func (s WpaSupplicant) String() string {
	switch {
	case len(s.WiFiConfigs) > 0:
		var networks []string
		for _, cfg := range s.WiFiConfigs {
			networks = append(networks, cfg.String())
		}
		return fmt.Sprintf(
			"WPA supplicant (Wi-Fi): adapter=%s (%s), Networks=%v",
			s.AdapterLL, s.AdapterIfName, networks)

	case s.PNACConfig != nil:
		return fmt.Sprintf(
			"WPA supplicant (802.1X): adapter=%s (%s), %s",
			s.AdapterLL, s.AdapterIfName, s.PNACConfig)

	default:
		return fmt.Sprintf(
			"WPA supplicant: adapter=%s (%s), no configuration",
			s.AdapterLL, s.AdapterIfName)
	}
}

type wirelessTypeGetter interface {
	GetWirelessType() types.WirelessType
}

// Dependencies lists the adapter as the only dependency of the wpa_supplicant.
func (s WpaSupplicant) Dependencies() (deps []depgraph.Dependency) {
	return []depgraph.Dependency{
		{
			RequiredItem: depgraph.ItemRef{
				ItemType: AdapterTypename,
				ItemName: s.AdapterIfName,
			},
			MustSatisfy: func(item depgraph.Item) bool {
				adapter, ok := item.(wirelessTypeGetter)
				if !ok {
					// Unreachable, linuxitems.Adapter implements GetWirelessType().
					return false
				}
				if len(s.WiFiConfigs) > 0 {
					return adapter.GetWirelessType() == types.WirelessTypeWifi
				}
				return adapter.GetWirelessType() == types.WirelessTypeNone // Ethernet
			},
			Description: "Network adapter must exist",
		},
	}
}

// WpaSupplicantConfigurator implements Configurator interface (libs/reconciler)
// for WpaSupplicant.
type WpaSupplicantConfigurator struct {
	Log          *base.LogObject
	procManagers map[string]*processManagers // key: interface name
}

type processManagers struct {
	supplicantPM *proc.ProcessManager
	watcherPM    *proc.ProcessManager
}

// Create prepares config file and starts wpa_supplicant.
func (c *WpaSupplicantConfigurator) Create(ctx context.Context, item depgraph.Item) error {
	s, ok := item.(WpaSupplicant)
	if !ok {
		return fmt.Errorf("invalid item type %T, expected WpaSupplicant", item)
	}

	if c.procManagers == nil {
		c.procManagers = make(map[string]*processManagers)
	}

	if err := c.createConfigFile(s); err != nil {
		return err
	}

	if s.PNACConfig != nil {
		err := c.createCACertChainFile(s.AdapterIfName, s.PNACConfig.CACertChain)
		if err != nil {
			return err
		}
		err = c.createEventWatcherScript(s.AdapterIfName)
		if err != nil {
			return err
		}
	}

	pms := &processManagers{
		supplicantPM: c.initPMForSupplicant(s),
		watcherPM:    c.initPMForWatcher(s),
	}
	c.procManagers[s.AdapterIfName] = pms

	done := reconciler.ContinueInBackground(ctx)
	go func() {
		ctx, cancel := context.WithTimeout(ctx, wpaSupplicantStartTimeout)
		err := pms.supplicantPM.Start(ctx)
		if err != nil {
			err = fmt.Errorf("failed to start wpa_supplicant: %w", err)
			c.Log.Error(err)
			cancel()
			done(err)
			return
		}

		if s.PNACConfig != nil {
			// Register event action script using wpa_cli to obtain 802.1x state updates.
			retryDelay := 500 * time.Millisecond
			var startErr error
			for {
				if ctx.Err() != nil {
					err = startErr
					if err == nil {
						err = ctx.Err()
					}
					err = fmt.Errorf(
						"failed to start wpa_supplicant event watcher: %w", err)
					c.Log.Error(err)
					cancel()
					done(err)
					return
				}

				startErr = pms.watcherPM.Start(ctx)
				if startErr == nil {
					break
				}
				c.Log.Functionf("wpa_cli not ready yet, retrying: %v", startErr)
				time.Sleep(retryDelay)
			}
		}

		cancel()
		done(nil)
	}()
	return nil
}

func (c *WpaSupplicantConfigurator) initPMForSupplicant(
	s WpaSupplicant) *proc.ProcessManager {
	// Determine the appropriate driver
	var driver string
	switch {
	case len(s.WiFiConfigs) > 0:
		driver = "nl80211,wext" // try modern driver first, then legacy
	case s.PNACConfig != nil:
		driver = "wired" // Wired PNAC / 802.1X
	default:
		driver = "none" // No interface configured (should be rare)
	}

	args := []string{
		"-i", s.AdapterIfName,
		"-c", c.configPath(s.AdapterIfName),
		"-D", driver, // explicitly set driver
		"-d", // increase debugging verbosity
	}
	return &proc.ProcessManager{
		Log:  c.Log,
		Cmd:  "wpa_supplicant",
		Args: args,

		// In Alpine, wpa_supplicant is built without syslog support (CONFIG_DEBUG_SYSLOG),
		// so the "-s" option is unavailable. To ensure logs are visible in EVE,
		// we run the process in the foreground (without -B) and let ProcessManager capture
		// stdout and stderr, forwarding them through the NIM logger.
		WillFork:  false,
		LogOutput: true,
	}
}

// Use `wpa_cli -a` to maintain a per-interface "pnac.state" file containing:
//
//	STATE: <CONNECTED|DISCONNECTED>
//	TIMESTAMP: <last-change-time>
func (c *WpaSupplicantConfigurator) initPMForWatcher(
	s WpaSupplicant) *proc.ProcessManager {
	pidFile := c.eventWatcherPIDFilePath(s.AdapterIfName)

	args := []string{
		"-p", types.WpaSupplicantCtrlSockDir,
		"-i", s.AdapterIfName,
		"-a", c.eventWatcherScriptPath(s.AdapterIfName),
		"-P", pidFile,
		"-r", // auto reconnect
		"-B", // daemonize
	}
	return &proc.ProcessManager{
		Log:       c.Log,
		PidFile:   pidFile,
		Cmd:       "wpa_cli",
		Args:      args,
		WithNohup: true,
		WillFork:  true,
	}
}

// Modify is not implemented.
func (c *WpaSupplicantConfigurator) Modify(ctx context.Context,
	oldItem, newItem depgraph.Item) (err error) {
	return errors.New("not implemented")
}

// Delete stops wpa_supplicant and removes the config file.
func (c *WpaSupplicantConfigurator) Delete(ctx context.Context, item depgraph.Item) error {
	s, ok := item.(WpaSupplicant)
	if !ok {
		return fmt.Errorf("invalid item type %T, expected WpaSupplicant", item)
	}

	pms := c.procManagers[s.AdapterIfName]
	delete(c.procManagers, s.AdapterIfName)

	done := reconciler.ContinueInBackground(ctx)
	go func() {
		if pms != nil && pms.watcherPM != nil {
			// Stop the wpa_supplicant event watcher (wpa_cli -a) first.
			// Errors are logged but otherwise ignored, since the watcher
			// should exit automatically anyway once wpa_supplicant stops.
			ctx, cancel := context.WithTimeout(ctx, wpaSupplicantStopTimeout)
			if err := pms.watcherPM.Stop(ctx); err != nil {
				c.Log.Errorf("Failed to stop wpa_supplicant event watcher: %v", err)
			}
			cancel()
		}

		// Stop wpa_supplicant itself.
		var err error
		if pms != nil && pms.supplicantPM != nil {
			ctx, cancel := context.WithTimeout(ctx, wpaSupplicantStopTimeout)
			err = pms.supplicantPM.Stop(ctx)
			cancel()
		}

		// Clean up all files created for this supplicant instance. Ignore errors.
		_ = os.Remove(c.configPath(s.AdapterIfName))
		if s.PNACConfig != nil {
			_ = os.Remove(c.caCertChainPath(s.AdapterIfName))
			_ = os.Remove(c.eventWatcherScriptPath(s.AdapterIfName))
			_ = os.Remove(c.eventWatcherPIDFilePath(s.AdapterIfName))
			_ = os.Remove(c.pnacStatePath(s.AdapterIfName))
		}
		done(err)
	}()
	return nil
}

// NeedsRecreate returns true because Modify is not implemented.
func (c *WpaSupplicantConfigurator) NeedsRecreate(
	oldItem, newItem depgraph.Item) (recreate bool) {
	return true
}

func (c *WpaSupplicantConfigurator) configPath(ifName string) string {
	return filepath.Join(nimRunDir, "wpa_supplicant-"+ifName+".conf")
}

func (c *WpaSupplicantConfigurator) caCertChainPath(ifName string) string {
	return filepath.Join(nimRunDir, "wpa_supplicant-"+ifName+"-ca.pem")
}

func (c *WpaSupplicantConfigurator) eventWatcherScriptPath(ifName string) string {
	return filepath.Join(nimRunDir, "wpa_supplicant-"+ifName+"-watcher.sh")
}

func (c *WpaSupplicantConfigurator) eventWatcherPIDFilePath(ifName string) string {
	return filepath.Join(nimRunDir, "wpa_supplicant-"+ifName+"-watcher.pid")
}

func (c *WpaSupplicantConfigurator) pnacStatePath(ifName string) string {
	return filepath.Join(types.PNACStateDir, ifName)
}

func (c *WpaSupplicantConfigurator) createConfigFile(s WpaSupplicant) error {
	cfgPath := c.configPath(s.AdapterIfName)
	file, err := os.Create(cfgPath)
	if err != nil {
		err = fmt.Errorf("failed to create wpa_supplicant config %s: %w", cfgPath, err)
		c.Log.Error(err)
		return err
	}
	defer file.Close()

	var cfg string
	switch {
	case len(s.WiFiConfigs) > 0:
		cfg = c.renderWifiConfig(s.WiFiConfigs)
	case s.PNACConfig != nil:
		cfg = c.renderPNACConfig(s.AdapterIfName, *s.PNACConfig)
	default:
		return fmt.Errorf("wpa_supplicant has no configuration")
	}

	if _, err := file.WriteString(cfg); err != nil {
		err = fmt.Errorf("failed to write wpa_supplicant config %s: %w", cfgPath, err)
		c.Log.Error(err)
		return err
	}
	return nil
}

func (c *WpaSupplicantConfigurator) createCACertChainFile(ifName string,
	caCertChain []*x509.Certificate) error {
	if len(caCertChain) == 0 {
		return fmt.Errorf("CA certificate chain is empty")
	}

	path := c.caCertChainPath(ifName)

	f, err := os.Create(path)
	if err != nil {
		err = fmt.Errorf("failed to create CA cert chain file %s: %w", path, err)
		c.Log.Error(err)
		return err
	}

	for _, cert := range caCertChain {
		if cert == nil {
			continue
		}
		if err = pem.Encode(f, &pem.Block{
			Type:  "CERTIFICATE",
			Bytes: cert.Raw,
		}); err != nil {
			_ = f.Close()
			_ = os.Remove(path)
			err = fmt.Errorf("failed to encode CA certificate to %s: %w", path, err)
			c.Log.Error(err)
			return err
		}
	}

	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		err = fmt.Errorf("failed to close CA cert chain file %s: %w", path, err)
		c.Log.Error(err)
		return err
	}
	return nil
}

func (c *WpaSupplicantConfigurator) renderWifiConfig(cfgs []WifiConfig) string {
	var b strings.Builder

	b.WriteString("# Automatically generated by NIM\n")
	fmt.Fprintf(&b, "ctrl_interface=%s\n", types.WpaSupplicantCtrlSockDir)
	b.WriteString("ap_scan=1\n\n")

	for _, cfg := range cfgs {
		b.WriteString("network={\n")
		fmt.Fprintf(&b, "    ssid=\"%s\"\n", cfg.SSID)
		b.WriteString("    scan_ssid=1\n")

		switch cfg.KeyScheme {
		case types.KeySchemeWpaPsk:
			b.WriteString("    key_mgmt=WPA-PSK\n")
			if cfg.PasswordHash != "" {
				fmt.Fprintf(&b, "    psk=%s\n", cfg.PasswordHash)
			}

		case types.KeySchemeWpaEap:
			b.WriteString("    key_mgmt=WPA-EAP\n")
			b.WriteString("    eap=PEAP\n")

			if cfg.Identity != "" {
				fmt.Fprintf(&b, "    identity=\"%s\"\n", cfg.Identity)
			}
			if cfg.PasswordHash != "" {
				fmt.Fprintf(&b, "    password=hash:%s\n", cfg.PasswordHash)
			}

			b.WriteString("    phase1=\"peaplabel=1\"\n")
			b.WriteString("    phase2=\"auth=MSCHAPV2\"\n")
		}

		if cfg.Priority != 0 {
			fmt.Fprintf(&b, "    priority=%d\n", cfg.Priority)
		}
		b.WriteString("}\n\n")
	}

	return b.String()
}

func (c *WpaSupplicantConfigurator) renderPNACConfig(
	ifName string, cfg PNAC8021XConfig) string {
	return fmt.Sprintf(`# Automatically generated by NIM
ctrl_interface=%s
ap_scan=0

network={
    key_mgmt=IEEE8021X
    eap=TLS
    eapol_flags=0
    identity="%s"
    ca_cert="%s"
    client_cert="%s"
    private_key="%s"
}
`,
		types.WpaSupplicantCtrlSockDir,
		cfg.EAPIdentity,
		c.caCertChainPath(ifName),
		cfg.ClientCertPath,
		cfg.ClientKeyPath,
	)
}

// Script executed by `wpa_cli -a`. Receives two args:
//
//	$1 = interface name
//	$2 = event (CONNECTED/DISCONNECTED)
//
// Updates per-interface PNAC state file with current connectivity status
// and timestamp for the latest state change.
func (c *WpaSupplicantConfigurator) createEventWatcherScript(ifName string) error {
	scriptPath := c.eventWatcherScriptPath(ifName)
	statePath := c.pnacStatePath(ifName)

	// Ensure state directory exists
	if err := os.MkdirAll(filepath.Dir(statePath), 0755); err != nil {
		err = fmt.Errorf("failed to create PNAC state dir: %w", err)
		c.Log.Error(err)
		return err
	}

	script := fmt.Sprintf(`#!/bin/sh
# Auto-generated by NIM.
# Invoked by: wpa_cli -a <script>

IFACE="%s"
STATE="$2"
STATE_FILE="%s"
TMP_FILE="${STATE_FILE}.tmp"

{
    echo "STATE: $STATE"
    echo "TIMESTAMP: $(date +%%s)"
} > "$TMP_FILE"

mv "$TMP_FILE" "$STATE_FILE"
`, ifName, statePath)

	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		err = fmt.Errorf("failed to write event watcher script %s: %w", scriptPath, err)
		c.Log.Error(err)
		return err
	}
	return nil
}
