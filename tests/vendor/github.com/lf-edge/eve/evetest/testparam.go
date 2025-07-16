package evetest

import (
	"os"
	"strconv"

	"github.com/lf-edge/eve/evetest/constants"
)

// TestParameterDefinition describes a parameter that a test or test-suite
// can accept. Parameters may have a default value, can be overridden by
// test-suites, or via environment variables.
type TestParameterDefinition struct {
	// Key is the unique identifier of the parameter.
	Key string

	// DefaultValue is the value used if the parameter is not explicitly set.
	DefaultValue interface{}

	// Description is a human-readable explanation of the parameter.
	Description string
}

// TestParameterValue represents a concrete value assigned to a test parameter,
// typically by a test-suite when running parameterized tests.
type TestParameterValue struct {
	// Key is the identifier of the parameter.
	Key string

	// Value is the concrete value assigned to the parameter.
	Value interface{}
}

// FromStringer should be implemented by a test parameter if its type is not a basic
// Go type.
type FromStringer interface {
	FromString(string) error
}

// DefineTestParameters defines the set of parameters available to the
// currently executing test or test suite.
func DefineTestParameters(params ...TestParameterDefinition) {
	th := getTestHarness()
	th.testM.Lock()
	defer th.testM.Unlock()
	th.test.paramDefs = params
}

// GetTestParameter returns the value of a test parameter with the given key,
// resolved in the following order:
//
//  1. Value set explicitly by the test-suite
//  2. Value provided via environment variable EVETEST_<KEY>
//  3. Default value from the parameter definition
//
// The type parameter T must match the parameter’s declared type, otherwise
// the test will fail.
func GetTestParameter[T any](key string) T {
	th := getTestHarness()
	th.testM.Lock()
	defer th.testM.Unlock()

	// Check that the given parameter is defined for the current test.
	var definition TestParameterDefinition
	for _, param := range th.test.paramDefs {
		if param.Key == key {
			definition = param
			break
		}
	}
	if th.suite != nil {
		for _, param := range th.suite.paramDefs {
			if param.Key == key {
				definition = param
				break
			}
		}
	}
	if definition.Key == "" {
		th.t.Fatalf("Parameter %q is not defined for test %q",
			key, th.test.name)
	}

	// Check if RunTestSuite has set some value for the parameter.
	for _, param := range th.test.paramVals {
		if param.Key == key {
			val, ok := param.Value.(T)
			if !ok {
				th.t.Fatalf(
					"parameter %q has type %T, expected %T",
					key, param.Value, *new(T),
				)
			}
			return val
		}
	}

	// Check environment variables.
	val := os.Getenv(constants.EnvPrefix + key)
	if val != "" {
		var zero T
		switch any(zero).(type) {
		case string:
			return any(val).(T)
		case bool:
			parsed, err := strconv.ParseBool(val)
			if err != nil {
				th.t.Fatalf("invalid boolean for %s: %v", key, err)
			}
			return any(parsed).(T)
		case int:
			parsed, err := strconv.Atoi(val)
			if err != nil {
				th.t.Fatalf("invalid int for %s: %v", key, err)
			}
			return any(parsed).(T)
		case int8:
			parsed, err := strconv.ParseInt(val, 10, 8)
			if err != nil {
				th.t.Fatalf("invalid int8 for %s: %v", key, err)
			}
			return any(int8(parsed)).(T)
		case int16:
			parsed, err := strconv.ParseInt(val, 10, 16)
			if err != nil {
				th.t.Fatalf("invalid int16 for %s: %v", key, err)
			}
			return any(int16(parsed)).(T)
		case int32:
			parsed, err := strconv.ParseInt(val, 10, 32)
			if err != nil {
				th.t.Fatalf("invalid int32 for %s: %v", key, err)
			}
			return any(int32(parsed)).(T)
		case int64:
			parsed, err := strconv.ParseInt(val, 10, 64)
			if err != nil {
				th.t.Fatalf("invalid int64 for %s: %v", key, err)
			}
			return any(parsed).(T)
		case uint:
			parsed, err := strconv.ParseUint(val, 10, 0)
			if err != nil {
				th.t.Fatalf("invalid uint for %s: %v", key, err)
			}
			return any(uint(parsed)).(T)
		case uint8:
			parsed, err := strconv.ParseUint(val, 10, 8)
			if err != nil {
				th.t.Fatalf("invalid uint8 for %s: %v", key, err)
			}
			return any(uint8(parsed)).(T)
		case uint16:
			parsed, err := strconv.ParseUint(val, 10, 16)
			if err != nil {
				th.t.Fatalf("invalid uint16 for %s: %v", key, err)
			}
			return any(uint16(parsed)).(T)
		case uint32:
			parsed, err := strconv.ParseUint(val, 10, 32)
			if err != nil {
				th.t.Fatalf("invalid uint32 for %s: %v", key, err)
			}
			return any(uint32(parsed)).(T)
		case uint64:
			parsed, err := strconv.ParseUint(val, 10, 64)
			if err != nil {
				th.t.Fatalf("invalid uint64 for %s: %v", key, err)
			}
			return any(parsed).(T)
		}

		// Try FromStringer interface
		ptr := new(T)
		if fm, ok := any(ptr).(FromStringer); ok {
			if err := fm.FromString(val); err != nil {
				th.t.Fatalf("error parsing %s from env: %v", key, err)
			}
			return *ptr
		}

		th.t.Fatalf("unsupported parameter type for key %s", key)
	}

	// Return default value.
	defVal, ok := definition.DefaultValue.(T)
	if !ok {
		th.t.Fatalf(
			"default value for parameter %q has type %T, expected %T",
			key, definition.DefaultValue, *new(T),
		)
	}
	return defVal
}

// HypervisorParameterKey is the key used for the Hypervisor parameter.
const HypervisorParameterKey = "HYPERVISOR"

// HypervisorParameter is a predefined TestParameterDefinition for Hypervisor parameter.
// defaultValue should be treated as an optional argument (not used as a variadic arg).
func HypervisorParameter(defaultValue ...Hypervisor) TestParameterDefinition {
	definition := TestParameterDefinition{
		Key:          HypervisorParameterKey,
		DefaultValue: HypervisorKVM,
		Description:  "Specify hypervisor (kvm, xen, etc.) to use for the test suite",
	}
	if len(defaultValue) > 0 {
		definition.DefaultValue = defaultValue[0]
	}
	return definition
}

// GetHypervisorParameterValue returns the value set for the Hypervisor parameter.
func GetHypervisorParameterValue() Hypervisor {
	return GetTestParameter[Hypervisor](HypervisorParameterKey)
}

// FilesystemParameterKey is the key used for the Filesystem parameter.
const FilesystemParameterKey = "FILESYSTEM"

// FilesystemParameter is a predefined TestParameterDefinition for Filesystem parameter.
// defaultValue should be treated as an optional argument (not used as a variadic arg).
func FilesystemParameter(defaultValue ...Filesystem) TestParameterDefinition {
	definition := TestParameterDefinition{
		Key:          FilesystemParameterKey,
		DefaultValue: FilesystemEXT4,
		Description:  "Specify which filesystem (ext4, ZFS, etc.) EVE should use",
	}
	if len(defaultValue) > 0 {
		definition.DefaultValue = defaultValue[0]
	}
	return definition
}

// GetFilesystemParameterValue returns the value set for the Filesystem parameter.
func GetFilesystemParameterValue() Filesystem {
	return GetTestParameter[Filesystem](FilesystemParameterKey)
}

// TPMParameterKey is the key used for the TPM parameter.
const TPMParameterKey = "TPM"

// TPMParameter is a predefined TestParameterDefinition for TPM parameter.
// defaultValue should be treated as an optional argument (not used as a variadic arg).
func TPMParameter(defaultValue ...Filesystem) TestParameterDefinition {
	definition := TestParameterDefinition{
		Key:          TPMParameterKey,
		DefaultValue: true,
		Description:  "Enable or disable TPM",
	}
	if len(defaultValue) > 0 {
		definition.DefaultValue = defaultValue[0]
	}
	return definition
}

// GetTPMParameterValue returns the value set for the TPM parameter.
func GetTPMParameterValue() (useTPM bool) {
	return GetTestParameter[bool](TPMParameterKey)
}
