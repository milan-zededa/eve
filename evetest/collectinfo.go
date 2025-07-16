package evetest

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	eveinfo "github.com/lf-edge/eve-api/go/info"
	api "github.com/lf-edge/eve/evetest/grpcapi/go"
	"github.com/lf-edge/eve/evetest/logger"
	"github.com/sirupsen/logrus"
)

// gatherLogsFromAllDevices retrieves device logs from all known EVE devices
// and stores them as artifact files under the test artifact directory.
//
// For each device, logs are fetched via Adam with a bounded timeout and
// written into a file named "<device-name>.log". Failures for individual
// devices are logged but do not abort processing of other devices.
func (th *TestHarness) gatherLogsFromAllDevices() {
	th.log.Infof("Gathering logs from all EVE devices...")
	th.devicesM.Lock()
	defer th.devicesM.Unlock()

	for _, dev := range th.devices {
		filePath := filepath.Join(
			th.test.artifactDir, fmt.Sprintf("%s.log", dev.name))

		outFile, err := os.Create(filePath)
		if err != nil {
			th.log.Errorf(
				"Unable to create log artifact file %q for device %q: %v",
				filePath, dev.name, err)
			continue
		}

		ctx, cancel := context.WithTimeout(th.ctx, gatherLogsTimeout)
		logWriter := &logger.PlainDeviceLogFile{OutFile: outFile}
		err = th.adamClient.IterateDeviceLogs(ctx, dev.ID, nil, logWriter, false)
		cancel()
		if err != nil {
			th.log.Errorf(
				"Failed to retrieve logs for device %q: %v",
				dev.name, err)
		}

		if err = outFile.Close(); err != nil {
			th.log.Errorf(
				"Failed to close log artifact file %q for device %q: %v",
				filePath, dev.name, err)
		}
	}
}

// gatherConsoleOutputFromAllDevices retrieves the current console output
// from all known EVE devices via the broker and stores it as artifact files.
//
// For each device, console output is requested with a bounded timeout and
// written into a file named "<device-name>.console". Failures are logged per
// device and do not interrupt processing of remaining devices.
func (th *TestHarness) gatherConsoleOutputFromAllDevices() {
	th.log.Infof("Gathering console output from all EVE devices...")
	th.devicesM.Lock()
	defer th.devicesM.Unlock()

	for _, dev := range th.devices {
		ctx, cancel := context.WithTimeout(
			th.ctx, brokerGetConsoleOutputTimeout)

		resp, err := th.brokerClient.GetDeviceConsoleOutput(
			ctx,
			&api.DeviceControlRequest{
				ClientId:   th.brokerClientID,
				DeviceName: dev.name,
			},
		)
		cancel()

		if err != nil {
			th.log.Errorf(
				"Failed to retrieve console output for device %q: %v",
				dev.name, err)
			continue
		}

		filePath := filepath.Join(
			th.test.artifactDir, fmt.Sprintf("%s.console", dev.name))

		if err = os.WriteFile(filePath,
			[]byte(resp.ConsoleOutput), 0666); err != nil {
			th.log.Errorf(
				"Failed to write console artifact file %q for device %q: %v",
				filePath, dev.name, err)
		}
	}
}

type infoMsgFileIterator struct {
	outFile io.Writer
}

func (w *infoMsgFileIterator) Iterate(msg *eveinfo.ZInfoMsg) (bool, error) {
	_, err := fmt.Fprintf(w.outFile, "%s\n\n", msg.String())
	return false, err
}

// gatherInfoMsgsFromAllDevices retrieves published informational messages
// from all known EVE devices and stores them as artifact files under the
// test artifact directory.
//
// For each device, info messages are fetched via Adam with a bounded timeout
// and written into a file named "<device-name>.info". Errors encountered for
// individual devices are logged but do not prevent processing of others.
func (th *TestHarness) gatherInfoMsgsFromAllDevices() {
	th.log.Infof("Gathering published info messages from all EVE devices...")
	th.devicesM.Lock()
	defer th.devicesM.Unlock()

	for _, dev := range th.devices {
		filePath := filepath.Join(
			th.test.artifactDir, fmt.Sprintf("%s.info", dev.name))

		outFile, err := os.Create(filePath)
		if err != nil {
			th.log.Errorf(
				"Unable to create artifact file %q with info messages for device %q: %v",
				filePath, dev.name, err)
			continue
		}
		iterator := &infoMsgFileIterator{outFile: outFile}

		ctx, cancel := context.WithTimeout(th.ctx, gatherInfoMsgsTimeout)
		err = th.adamClient.IterateDeviceInfoMsgs(ctx, dev.ID, nil, iterator, false)
		cancel()

		if err != nil {
			th.log.Errorf(
				"Failed to retrieve info messages for device %q: %v",
				dev.name, err)
		}

		if err = outFile.Close(); err != nil {
			th.log.Errorf(
				"Failed to close artifact file %q with info messages for device %q: %v",
				filePath, dev.name, err)
		}
	}
}

// Try to obtain collect-info tarball from every EVE device.
func (th *TestHarness) collectInfoFromAllDevices() {
	var wg sync.WaitGroup
	th.log.Infof("Trying to obtain collect-info tarball from every EVE device...")

	const maxAttempts = 3

	th.devicesM.Lock()
	for _, dev := range th.devices {
		wg.Add(1)
		go func(devName string) {
			defer wg.Done()

			var lastErr error
			for attempt := 1; attempt <= maxAttempts; attempt++ {
				ctx, cancel := context.WithTimeout(th.ctx, collectInfoTimeout)
				_, err := th.collectInfoFromDevice(ctx, devName)
				cancel()

				if err == nil {
					if attempt > 1 {
						th.log.Infof(
							"Successfully collected info from device %q on attempt %d/%d",
							devName, attempt, maxAttempts,
						)
					}
					return
				}

				lastErr = err
				th.log.Warnf(
					"Failed to collect info from device %q (attempt %d/%d): %v",
					devName, attempt, maxAttempts, err,
				)
			}

			th.log.Errorf(
				"Giving up collecting info from device %q after %d attempts: %v",
				devName, maxAttempts, lastErr,
			)
		}(dev.name)
	}
	th.devicesM.Unlock()

	wg.Wait()
}

// Try to obtain collect-info tarball from the given EVE device.
func (th *TestHarness) collectInfoFromDevice(
	ctx context.Context, devName string) (filePath string, err error) {
	// Run collect-info.sh and capture its output.
	ciStdout := logger.LogWriter{
		Log:    th.log,
		Level:  logrus.DebugLevel,
		Prefix: fmt.Sprintf("collect-info (%s): ", devName),
	}
	// Expect collect-info.sh to emit stdout at least once every 5 minutes.
	// The relatively long timeout accounts for copying /sys/fs/cgroup/memory,
	// which can be slow due to the large number of cgroups (notably on eve-k).
	stdoutWatchdogTimeout := 5 * time.Minute
	err = th.runScriptOnEVEOverSSH(ctx,
		devName, "collect-info.sh", ciStdout, nil, stdoutWatchdogTimeout)
	if err != nil {
		err = fmt.Errorf("collect-info.sh failed on device %q: %v",
			devName, err)
		return "", err
	}

	// Prepare output file for the collect-info artifact.
	filePath = filepath.Join(th.test.artifactDir,
		fmt.Sprintf("eve-info-%s.tar", devName),
	)
	outFile, err := os.Create(filePath)
	if err != nil {
		err = fmt.Errorf("failed to create collect-info artifact for device %q: %v",
			devName, err)
		return "", err
	}
	defer outFile.Close()

	// Archive the collected info (alongside any other previously collected infos)
	// and stream it to the artifact file.
	// We should see a constant stream of tar-ed data coming.
	stdoutWatchdogTimeout = 20 * time.Second
	err = th.runScriptOnEVEOverSSH(ctx,
		devName, "tar -C /persist -cf - eve-info", outFile, nil, stdoutWatchdogTimeout)
	if err != nil {
		err = fmt.Errorf("failed to archive collect-info from device %q: %v",
			devName, err)
		return "", err
	}

	th.log.Infof("Received collect-info tarball from EVE device %q", devName)
	return filePath, nil
}
