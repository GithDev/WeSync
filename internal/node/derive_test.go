package node

import "testing"

// Table-driven coverage of DeriveFolderRelationState. Each row pins down one
// dimension combination the UI must render correctly. Add a row before fixing
// any new state bug — never just patch derive without a test pinning behavior.

func TestDeriveFolderRelationState(t *testing.T) {
	t.Parallel()

	type row struct {
		name string
		in   FolderRelationDimensions
		want FolderRelationState
	}

	tests := []row{
		{
			name: "device not in folder.Devices",
			in:   FolderRelationDimensions{InDeviceList: false},
			want: FolderRelationNotMember,
		},
		{
			name: "device not in list — other dimensions ignored",
			in: FolderRelationDimensions{
				InDeviceList:     false,
				InRemoteSequence: true,
				BEPLive:          true,
			},
			want: FolderRelationNotMember,
		},
		{
			name: "invited — fresh add, B not yet accepted",
			in: FolderRelationDimensions{
				InDeviceList:     true,
				InRemoteSequence: false,
				WireAccepted:     false,
			},
			want: FolderRelationInvited,
		},
		{
			name: "invited — BEP live for OTHER folder doesn't imply this one",
			in: FolderRelationDimensions{
				InDeviceList:     true,
				InRemoteSequence: false,
				BEPLive:          true,
			},
			want: FolderRelationInvited,
		},
		{
			name: "accepted via ST remoteSequence — idle online",
			in: FolderRelationDimensions{
				InDeviceList:     true,
				InRemoteSequence: true,
				BEPLive:          true,
				FolderState:      "idle",
			},
			want: FolderRelationAcceptedIdle,
		},
		{
			name: "accepted via wire fast-path before ST catches up",
			in: FolderRelationDimensions{
				InDeviceList:     true,
				InRemoteSequence: false,
				WireAccepted:     true,
				BEPLive:          true,
				FolderState:      "idle",
			},
			want: FolderRelationAcceptedIdle,
		},
		{
			name: "accepted-offline — B was paused/offline, signal persists",
			in: FolderRelationDimensions{
				InDeviceList:     true,
				InRemoteSequence: true,
				BEPLive:          false,
			},
			want: FolderRelationAcceptedOffline,
		},
		{
			name: "accepted-behind-offline — B offline but still owes us data",
			in: FolderRelationDimensions{
				InDeviceList:     true,
				InRemoteSequence: true,
				BEPLive:          false,
				PeerNeed:         true,
			},
			want: FolderRelationAcceptedBehindOffline,
		},
		{
			name: "accepted-sending — connected, our folder idle, B still needs data, flowing",
			in: FolderRelationDimensions{
				InDeviceList:     true,
				InRemoteSequence: true,
				BEPLive:          true,
				FolderState:      "idle",
				PeerNeed:         true,
			},
			want: FolderRelationAcceptedSending,
		},
		{
			name: "accepted-stalled — B needs data but nothing is flowing",
			in: FolderRelationDimensions{
				InDeviceList:     true,
				InRemoteSequence: true,
				BEPLive:          true,
				FolderState:      "idle",
				PeerNeed:         true,
				PeerStalled:      true,
			},
			want: FolderRelationAcceptedStalled,
		},
		{
			name: "our scan dominates — PeerNeed ignored while we are scanning",
			in: FolderRelationDimensions{
				InDeviceList:     true,
				InRemoteSequence: true,
				BEPLive:          true,
				FolderState:      "syncing",
				PeerNeed:         true,
				PeerStalled:      true,
			},
			want: FolderRelationAcceptedSyncing,
		},
		{
			name: "PeerStalled without PeerNeed is meaningless — stays idle",
			in: FolderRelationDimensions{
				InDeviceList:     true,
				InRemoteSequence: true,
				BEPLive:          true,
				FolderState:      "idle",
				PeerStalled:      true,
			},
			want: FolderRelationAcceptedIdle,
		},
		{
			name: "wire-only accepted but offline — still accepted",
			in: FolderRelationDimensions{
				InDeviceList: true,
				WireAccepted: true,
				BEPLive:      false,
			},
			want: FolderRelationAcceptedOffline,
		},
		{
			name: "syncing",
			in: FolderRelationDimensions{
				InDeviceList:     true,
				InRemoteSequence: true,
				BEPLive:          true,
				FolderState:      "syncing",
			},
			want: FolderRelationAcceptedSyncing,
		},
		{
			name: "scanning maps to syncing visual",
			in: FolderRelationDimensions{
				InDeviceList:     true,
				InRemoteSequence: true,
				BEPLive:          true,
				FolderState:      "scanning",
			},
			want: FolderRelationAcceptedSyncing,
		},
		{
			name: "error state falls back to idle (no separate state for now)",
			in: FolderRelationDimensions{
				InDeviceList:     true,
				InRemoteSequence: true,
				BEPLive:          true,
				FolderState:      "error",
			},
			want: FolderRelationAcceptedIdle,
		},
		{
			name: "local pause takes precedence over remote pause",
			in: FolderRelationDimensions{
				InDeviceList:        true,
				InRemoteSequence:    true,
				BEPLive:             true,
				FolderPausedLocally: true,
				RemoteStatePaused:   true,
			},
			want: FolderRelationAcceptedPausedLocal,
		},
		{
			name: "remote pause shown when local is running",
			in: FolderRelationDimensions{
				InDeviceList:        true,
				InRemoteSequence:    true,
				BEPLive:             true,
				FolderPausedLocally: false,
				RemoteStatePaused:   true,
			},
			want: FolderRelationAcceptedPausedRemote,
		},
		{
			name: "pause beats syncing — folder paused while a sync was in flight",
			in: FolderRelationDimensions{
				InDeviceList:        true,
				InRemoteSequence:    true,
				BEPLive:             true,
				FolderState:         "syncing",
				FolderPausedLocally: true,
			},
			want: FolderRelationAcceptedPausedLocal,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := DeriveFolderRelationState(tc.in)
			if got != tc.want {
				t.Fatalf("DeriveFolderRelationState() = %q, want %q\ninput: %+v", got, tc.want, tc.in)
			}
		})
	}
}

func TestFolderRelationStateIsAccepted(t *testing.T) {
	t.Parallel()

	cases := map[FolderRelationState]bool{
		FolderRelationNotMember:             false,
		FolderRelationInvited:               false,
		FolderRelationAcceptedPausedLocal:   true,
		FolderRelationAcceptedPausedRemote:  true,
		FolderRelationAcceptedSyncing:       true,
		FolderRelationAcceptedSending:       true,
		FolderRelationAcceptedStalled:       true,
		FolderRelationAcceptedIdle:          true,
		FolderRelationAcceptedBehindOffline: true,
		FolderRelationAcceptedOffline:       true,
	}
	for s, want := range cases {
		if got := s.IsAccepted(); got != want {
			t.Errorf("%q IsAccepted() = %v, want %v", s, got, want)
		}
	}
}
