package main

import (
	"testing"
	"time"
)

func TestMinutesLeft(t *testing.T) {
	t.Parallel()

	tests := []struct {
		left time.Duration
		want string
	}{
		{30 * time.Second, "不到 1 分钟"},
		{time.Minute, " 1 分钟"},
		{58*time.Minute + time.Second, " 59 分钟"},
		{time.Hour, " 60 分钟"},
	}

	for _, tt := range tests {
		if got := minutesLeft(tt.left); got != tt.want {
			t.Errorf("minutesLeft(%v) = %q, want %q", tt.left, got, tt.want)
		}
	}
}
