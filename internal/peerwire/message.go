package peerwire

import "wesync/internal/sysinfo"

type MsgType string

const (
	Hello        MsgType = "hello"
	PeerState    MsgType = "peer_state"    // name + sysinfo — sent to all peers after hello
	Accepted     MsgType = "accepted"
	Cancelled    MsgType = "cancelled"
	FolderOffer   MsgType = "folder_offer"   // A invites B to share a folder
	FolderAccept  MsgType = "folder_accept"  // B accepts, provides their local path
	FolderDecline MsgType = "folder_decline" // B declines
	FolderRemove  MsgType = "folder_remove"  // either side removes the shared folder
	FolderSync    MsgType = "folder_sync"    // A tells B which offers A currently has for B (trusted only)
)

// Message is the envelope for all peer-to-peer WebSocket messages.
type Message struct {
	Type     MsgType             `json:"type"`
	DeviceID string              `json:"deviceID,omitempty"`
	Port     int                 `json:"port,omitempty"`
	Name     string              `json:"name,omitempty"`
	STPort   int                 `json:"stPort,omitempty"`
	Info     *sysinfo.DeviceInfo `json:"info,omitempty"`
	CertFP   string              `json:"certFP,omitempty"`
	// Trusted signals that the sender has added the receiver to their ST device config.
	// Receiver should show an incoming trust request if they haven't added the sender yet.
	Trusted *bool `json:"trusted,omitempty"`

	// Folder sharing fields
	FolderID       string   `json:"folderID,omitempty"`
	FolderLabel    string   `json:"folderLabel,omitempty"`
	FolderPath     string   `json:"folderPath,omitempty"`
	FolderType     string   `json:"folderType,omitempty"`
	FolderIDs      []string `json:"folderIDs,omitempty"`
	TargetDeviceID string   `json:"targetDeviceID,omitempty"`
}

// Callbacks are invoked by the Hub when messages arrive from a peer.
type Callbacks struct {
	OnAccepted  func(fromDeviceID string)
	OnCancelled func(fromDeviceID string)
	// OnHello is called after identity verification. Name/info are also delivered via OnPeerState.
	OnHello     func(fromDeviceID, fromAddr string, fromPort, fromSTPort int)
	// OnPeerState carries name + sysinfo and is sent to all peers (trusted and untrusted).
	OnPeerState func(fromDeviceID, name string, info *sysinfo.DeviceInfo)
	// OnTrusted is called when a peer signals their trust state toward us.
	// trusted=true means they've added us to their ST config (show incoming request if we haven't added them).
	// trusted=false means they've removed us (cancel any pending incoming request).
	OnTrusted   func(fromDeviceID string, trusted bool)
	OnFolderOffer   func(fromDeviceID, folderID, folderLabel, senderType string)
	OnFolderAccept  func(fromDeviceID, folderID, folderPath string)
	OnFolderDecline func(fromDeviceID, folderID string)
	OnFolderRemove  func(fromDeviceID, folderID, targetDeviceID string)
	OnFolderSync   func(fromDeviceID string, folderIDs []string)
	OnPeerVerified func(deviceID, certFP string)
	// OnValidateCertFP is called with a peer's deviceID and the TLS cert fingerprint
	// they presented. Return true to allow, false to reject.
	// Used to enforce cert-pinning: reject connections where the fingerprint doesn't
	// match what was stored from a previous session (prevents device impersonation).
	OnValidateCertFP func(deviceID, certFP string) bool
}
