// Copyright (c) 2022 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package genericitems

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"

	"github.com/lf-edge/eve/libs/depgraph"
	"github.com/lf-edge/eve/pkg/pillar/base"
	"github.com/lf-edge/eve/pkg/pillar/devicenetwork"
	"github.com/lf-edge/eve/pkg/pillar/types"
)

// Wwan : WWAN (LTE) configuration (read by wwan microservice).
// This is a singleton item, grouping configuration for all LTE modems on the device.
type Wwan struct {
	Config types.WwanConfig
}

// Name returns the full path to the wwan config file.
func (w Wwan) Name() string {
	return devicenetwork.WwanConfigPath
}

// Label is not defined.
func (w Wwan) Label() string {
	return ""
}

// Type of the item.
func (w Wwan) Type() string {
	return WwanTypename
}

// Equal compares two WWAN configs.
func (w Wwan) Equal(other depgraph.Item) bool {
	w2 := other.(Wwan)
	return w.Config.Equal(w2.Config)
}

// External is false.
func (w Wwan) External() bool {
	return false
}

// String describes wwan config.
func (w Wwan) String() string {
	return fmt.Sprintf("WWAN configuration: %+v", w.Config)
}

// Dependencies return empty list - wwan config file can be created even before
// the referenced wwanX interface(s) are ready (the wwan microservice can deal with it).
func (w Wwan) Dependencies() (deps []depgraph.Dependency) {
	return nil
}

// WwanConfigurator implements Configurator interface (libs/reconciler) for WWAN config.
type WwanConfigurator struct {
	Log *base.LogObject
	// LastChecksum : checksum of the last written wwan configuration.
	LastChecksum string
}

// Create writes config for wwan microservice.
func (c *WwanConfigurator) Create(ctx context.Context, item depgraph.Item) error {
	wwan := item.(Wwan)
	return c.installWwanConfig(wwan.Config)
}

// Modify writes updated config for wwan microservice.
func (c *WwanConfigurator) Modify(ctx context.Context, oldItem, newItem depgraph.Item) (err error) {
	wwan := newItem.(Wwan)
	return c.installWwanConfig(wwan.Config)
}

// Delete writes empty config for wwan microservice.
func (c *WwanConfigurator) Delete(ctx context.Context, item depgraph.Item) error {
	return c.installWwanConfig(types.WwanConfig{})
}

// NeedsRecreate returns false - Modify can apply any change.
func (c *WwanConfigurator) NeedsRecreate(oldItem, newItem depgraph.Item) (recreate bool) {
	return false
}

// Write cellular config into /run/wwan/config.json
func (c *WwanConfigurator) installWwanConfig(config types.WwanConfig) (err error) {
	bytes, hash, err := MarshalWwanConfig(config)
	if err != nil {
		c.Log.Error(err)
		return err
	}
	tmpfile, err := os.CreateTemp(devicenetwork.RunWwanDir, devicenetwork.WwanConfigTempname)
	if err != nil {
		err = fmt.Errorf("failed to create temporary file %s/%s: %v",
			devicenetwork.RunWwanDir, devicenetwork.WwanConfigTempname, err)
		c.Log.Error(err)
		return err
	}
	defer os.Remove(tmpfile.Name())
	defer tmpfile.Close()
	if r, err := tmpfile.Write(bytes); err != nil || r != len(bytes) {
		err = fmt.Errorf("failed to write %d bytes to file %s: %w",
			len(bytes), tmpfile.Name(), err)
		c.Log.Error(err)
		return err
	}
	if err = tmpfile.Sync(); err != nil {
		err = fmt.Errorf("failed to sync temporary file %s: %v\n",
			tmpfile.Name(), err)
		c.Log.Error(err)
		return err
	}
	if err = tmpfile.Close(); err != nil {
		err = fmt.Errorf("failed to close temporary file %s: %v\n",
			tmpfile.Name(), err)
		c.Log.Error(err)
		return err
	}
	if err := os.Rename(tmpfile.Name(), devicenetwork.WwanConfigPath); err != nil {
		err = fmt.Errorf("failed to rename file %s to %s: %v\n",
			tmpfile.Name(), devicenetwork.WwanConfigPath, err)
		c.Log.Error(err)
		return err
	}
	c.LastChecksum = hash
	return nil
}

// MarshalWwanConfig is exposed only for unit-testing purposes.
func MarshalWwanConfig(config types.WwanConfig) (bytes []byte, hash string, err error) {
	bytes, err = json.MarshalIndent(config, "", "    ")
	if err != nil {
		err = fmt.Errorf("failed to serialize wwan config: %w", err)
		return nil, "", err
	}
	shaHash := sha256.Sum256(bytes)
	hash = hex.EncodeToString(shaHash[:])
	return bytes, hash, err
}
