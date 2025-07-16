package logger

import (
	"fmt"
	evelogs "github.com/lf-edge/eve-api/go/logs"
	api "github.com/lf-edge/eve/evetest/grpcapi/go"
	uuid "github.com/satori/go.uuid"
	"io"
	"strings"
)

// DeviceLogEntry represents a single log entry emitted by an EVE device.
// It includes the original evelogs.LogEntry fields along with metadata such as
// the device UUID, software image, and EVE version.
type DeviceLogEntry struct {
	*evelogs.LogEntry
	UUID       uuid.UUID `json:"uuid,omitempty"`       // device ID
	Image      string    `json:"image,omitempty"`      // SW image the log got emitted from
	EveVersion string    `json:"eveVersion,omitempty"` // EVE software version
}

// LogEntryMatcher defines a predicate used to determine whether
// a LogEntry should be delivered to a subscriber.
type LogEntryMatcher func(entry DeviceLogEntry) bool

// DeviceLogWriter defines an interface for writing or processing
// DeviceLogEntry instances. Implementations may write to a file,
// stream over gRPC, or perform filtering/formatting.
type DeviceLogWriter interface {
	Write(DeviceLogEntry) error
}

// PlainDeviceLogFile writes DeviceLogEntry instances in a human-readable
// "plain text" format (not JSON) to the provided output writer.
// Each log line is written as:
//
//	<timestamp>|<severity>|<source>|<function>| <content>\n
//
// Example:
//
//	2025-08-05 02:42:41.267|info|domainmgr|/pillar/cmd/domainmgr/domainmgr.go:421| waiting for GCComplete
type PlainDeviceLogFile struct {
	OutFile io.Writer
}

// WriteEntry formats the given DeviceLogEntry as a human-readable line
// and writes it to the underlying OutFile.
func (w *PlainDeviceLogFile) Write(entry DeviceLogEntry) error {
	var ts string
	if entry.Timestamp != nil {
		ts = entry.Timestamp.AsTime().
			UTC().
			Format("2006-01-02 15:04:05.000")
	}
	line := fmt.Sprintf("%s|%s|%s|%s| %s\n",
		ts,
		strings.ToLower(entry.Severity),
		entry.Source,
		entry.Function,
		entry.Content,
	)
	_, err := io.WriteString(w.OutFile, line)
	return err
}

// GrpcDeviceLogStreamer wraps a gRPC streaming interface and implements
// the DeviceLogWriter interface. Each log entry written via Write() is
// sent to the connected gRPC client as an api.LogMessage.
type GrpcDeviceLogStreamer struct {
	Stream GrpcLogStream
}

// Write formats the given DeviceLogEntry as an api.LogMessage and sends
// it over the gRPC stream. Severity is converted from EVE's string format
// to the corresponding api.LogSeverity.
func (w *GrpcDeviceLogStreamer) Write(entry DeviceLogEntry) error {
	return w.Stream.Send(&api.LogMessage{
		Message:   entry.Content,
		Severity:  EVESeverityToAPILogSeverity(entry.Severity),
		Source:    entry.Source,
		Timestamp: entry.Timestamp,
	})
}

// EVESeverityToAPILogSeverity converts a log severity string as produced
// by an EVE device into the corresponding api.LogSeverity enumeration
// used by the gRPC API. Unknown severities are mapped to LOG_UNKNOWN.
func EVESeverityToAPILogSeverity(severity string) api.LogSeverity {
	switch severity {
	case "fatal":
		return api.LogSeverity_LOG_FATAL
	case "error":
		return api.LogSeverity_LOG_ERROR
	case "warning":
		return api.LogSeverity_LOG_WARN
	case "info", "notice":
		return api.LogSeverity_LOG_INFO
	case "debug":
		return api.LogSeverity_LOG_DEBUG
	default:
		return api.LogSeverity_LOG_UNKNOWN
	}
}
