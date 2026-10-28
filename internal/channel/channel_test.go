package channel

import (
	"errors"
	"testing"
)

func TestSplitID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		id, channel, platformID string
		wantErr                 bool
	}{
		{id: "lark:om_1", channel: "lark", platformID: "om_1"},
		{id: "discord:123/456", channel: "discord", platformID: "123/456"},
		{id: "lark:oc_x:y", channel: "lark", platformID: "oc_x:y"},
		{id: "om_1", wantErr: true},
		{id: ":om_1", wantErr: true},
		{id: "lark:", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			t.Parallel()

			ch, pid, err := SplitID(tt.id)
			if tt.wantErr {
				if !errors.Is(err, ErrUnknownChannel) {
					t.Fatalf("SplitID(%q) err = %v, want ErrUnknownChannel", tt.id, err)
				}

				return
			}

			if err != nil || ch != tt.channel || pid != tt.platformID {
				t.Errorf("SplitID(%q) = %q, %q, %v", tt.id, ch, pid, err)
			}

			if back := ID(ch, pid); back != tt.id {
				t.Errorf("ID round trip = %q, want %q", back, tt.id)
			}
		})
	}
}

func TestPathSafe(t *testing.T) {
	t.Parallel()

	if got := PathSafe("lark:om_../../x"); got != "lark_om_.._.._x" {
		t.Errorf("PathSafe() = %q; separators must not survive", got)
	}
}
