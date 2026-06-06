package service

import "testing"

func TestLegacyEnabledNotificationKeys(t *testing.T) {
	tests := []struct {
		name  string
		level int
		want  map[string]bool
	}{
		{
			name:  "none keeps login code available",
			level: EmailLevelNone,
			want: map[string]bool{
				NotificationAuthLoginCode: true,
			},
		},
		{
			name:  "urgent enables peer lifecycle",
			level: EmailLevelUrgent,
			want: map[string]bool{
				NotificationAuthLoginCode:   true,
				NotificationPeerApproved:    true,
				NotificationPeerRejected:    true,
				NotificationPeerSuspended:   true,
				NotificationPeerUnsuspended: true,
				NotificationPeerDeleted:     true,
				NotificationPeerMTUUpdated:  true,
			},
		},
		{
			name:  "general enables connectivity",
			level: EmailLevelGeneral,
			want: map[string]bool{
				NotificationAuthLoginCode:      true,
				NotificationPeerSubmitted:      true,
				NotificationPeerBGPDown:        true,
				NotificationPeerBGPRecovered:   true,
				NotificationPeerHandshakeStale: true,
			},
		},
		{
			name:  "all enables monitoring",
			level: EmailLevelAll,
			want: map[string]bool{
				NotificationAuthLoginCode:        true,
				NotificationPeerLatencyHigh:      true,
				NotificationPeerLatencyRecovered: true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := keySet(LegacyEnabledNotificationKeys(tt.level))
			for key := range tt.want {
				if !got[key] {
					t.Fatalf("LegacyEnabledNotificationKeys(%d) missing %s", tt.level, key)
				}
			}
		})
	}
}

func TestNotificationPresetsDoNotRepresentFutureCatalogItems(t *testing.T) {
	presets := NotificationPresets()
	all := presets[len(presets)-1]
	// Email presets only cover the email channel; telegram options are managed separately.
	currentEmailCatalogSize := len(NotificationOptionsForChannel("email"))
	if len(all.EnabledKeys) != currentEmailCatalogSize {
		t.Fatalf("all preset enabled %d keys, want current email catalog size %d", len(all.EnabledKeys), currentEmailCatalogSize)
	}
}

func TestLoginCodeRequiresDisableConfirmation(t *testing.T) {
	option, ok := NotificationOptionByKey(NotificationAuthLoginCode)
	if !ok {
		t.Fatal("login code option not found")
	}
	if option.Kind != NotificationKindRequired {
		t.Fatalf("login code kind = %s, want required", option.Kind)
	}
	if !option.RequiresDisableConfirmation {
		t.Fatal("login code option should require disable confirmation")
	}
}

func keySet(keys []string) map[string]bool {
	set := make(map[string]bool, len(keys))
	for _, key := range keys {
		set[key] = true
	}
	return set
}
