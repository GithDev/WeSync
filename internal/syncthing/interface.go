package syncthing

import "time"

// Backend is the subset of Syncthing API methods used by the WeSync API layer.
// Defined here so tests can substitute a mock without a live Syncthing daemon.
type Backend interface {
	ListDevices() ([]Device, error)
	ConnectedDeviceIDs() (map[string]bool, error)
	DeviceCompletion(folderID, deviceID string) (PeerNeed, error)
	DeviceLastSeen() (map[string]time.Time, error)
	GetConnectedAddresses() (map[string]string, error)
	UpdateDevice(id, name string) error
	UpdateDeviceIntroducer(id string, enabled bool) error
	AddDevice(id, name, addr string) error
	RemoveDevice(id string) error
	PendingDevices() ([]PendingDevice, error)
	DismissPendingDevice(id string) error
	ListFolders() ([]Folder, error)
	GetFolderStatus(folderID string) (FolderStatus, error)
	GetFolderIgnores(folderID string) ([]string, error)
	SetFolderIgnores(folderID string, patterns []string) error
	AddFolder(f Folder) error
	UpdateFolder(f Folder) error
	SetFolderPaused(id string, paused bool) error
	GetFolderPaused(id string) (bool, error)
	RemoveFolder(id string) error
	PendingFolders() ([]PendingFolder, error)
	DismissPendingFolder(folderID, deviceID string) error
	SetConnectivityLevel(level int) error
	ConnectivityStatus() (ConnectivityStatus, error)
	RescanFolder(folderID string) error
	RevertFolder(folderID string) error
	PauseDevice(deviceID string) error
	ResumeDevice(deviceID string) error
}
