package state

import (
	"sync"

	eveinfo "github.com/lf-edge/eve-api/go/info"
	"github.com/lf-edge/eve-api/go/profile"
)

// Config holds data that LPS sends back to EVE in responses.
type Config struct {
	ServerToken       string              `json:"serverToken"`
	Profile           string              `json:"profile"`
	RadioSilence      bool                `json:"radioSilence"`
	AppCommands       []*profile.AppCommand `json:"appCommands,omitempty"`
	DevCommand        *DevCmdConfig       `json:"devCommand,omitempty"`
	AppBootConfigs    []*profile.AppBootConfig `json:"appBootConfigs,omitempty"`
	LocalNetworkConfig *profile.LocalNetworkConfig `json:"localNetworkConfig,omitempty"`
}

// DevCmdConfig holds a device command to be sent to EVE.
type DevCmdConfig struct {
	Timestamp uint64 `json:"timestamp"`
	Command   string `json:"command"`
}

// Received holds data that EVE posts to LPS.
type Received struct {
	RadioStatus  *profile.RadioStatus      `json:"radioStatus,omitempty"`
	AppInfoList  *profile.LocalAppInfoList  `json:"appInfoList,omitempty"`
	DevInfo      *profile.LocalDevInfo      `json:"devInfo,omitempty"`
	Location     *eveinfo.ZInfoLocation     `json:"location,omitempty"`
	NetworkInfo  *profile.NetworkInfo       `json:"networkInfo,omitempty"`
	AppBootInfo  *profile.AppBootInfoList   `json:"appBootInfo,omitempty"`
}

// State is the shared in-memory state of the LPS.
type State struct {
	mu       sync.RWMutex
	Config   Config   `json:"config"`
	Received Received `json:"received"`
}

// New creates a new State with default values.
func New() *State {
	return &State{}
}

// GetConfig returns a copy of the current config.
func (s *State) GetConfig() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Config
}

// SetServerToken sets the server token.
func (s *State) SetServerToken(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Config.ServerToken = token
	if s.Config.LocalNetworkConfig != nil {
		s.Config.LocalNetworkConfig.ServerToken = token
	}
}

// SetProfile sets the local profile.
func (s *State) SetProfile(p string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Config.Profile = p
}

// SetRadioSilence sets the radio silence flag.
func (s *State) SetRadioSilence(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Config.RadioSilence = on
}

// SetAppCommands sets the app commands list.
func (s *State) SetAppCommands(cmds []*profile.AppCommand) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Config.AppCommands = cmds
}

// SetDevCommand sets the device command.
func (s *State) SetDevCommand(cmd *DevCmdConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Config.DevCommand = cmd
}

// SetAppBootConfigs sets the app boot configurations.
func (s *State) SetAppBootConfigs(configs []*profile.AppBootConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Config.AppBootConfigs = configs
}

// SetLocalNetworkConfig sets the local network configuration.
func (s *State) SetLocalNetworkConfig(cfg *profile.LocalNetworkConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Config.LocalNetworkConfig = cfg
}

// SetRadioStatus stores the radio status received from EVE.
func (s *State) SetRadioStatus(rs *profile.RadioStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Received.RadioStatus = rs
}

// SetAppInfoList stores the app info list received from EVE.
func (s *State) SetAppInfoList(info *profile.LocalAppInfoList) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Received.AppInfoList = info
}

// SetDevInfo stores the device info received from EVE.
func (s *State) SetDevInfo(info *profile.LocalDevInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Received.DevInfo = info
}

// SetLocation stores the location received from EVE.
func (s *State) SetLocation(loc *eveinfo.ZInfoLocation) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Received.Location = loc
}

// SetNetworkInfo stores the network info received from EVE.
func (s *State) SetNetworkInfo(info *profile.NetworkInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Received.NetworkInfo = info
}

// SetAppBootInfo stores the app boot info list received from EVE.
func (s *State) SetAppBootInfo(info *profile.AppBootInfoList) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Received.AppBootInfo = info
}

// GetReceived returns a copy of all received data.
func (s *State) GetReceived() Received {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Received
}

// GetAll returns both config and received data.
func (s *State) GetAll() (Config, Received) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Config, s.Received
}
