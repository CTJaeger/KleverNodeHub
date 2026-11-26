package version

import "testing"

func TestCompare(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"v0.3.93", "v0.3.92", 1},
		{"v0.3.93-knh.1", "v0.3.93", 1},
		{"v0.3.93-knh.2", "v0.3.93-knh.1", 1},
		{"v0.3.94", "v0.3.93-knh.9", 1},
		{"v0.3.93", "v0.3.93", 0},
		{"0.3.93", "v0.3.93", 0},
		{"v0.3.92", "v0.3.93-knh.1", -1},
		{"dev", "v0.3.93", -1},
	}
	for _, tt := range tests {
		if got := Compare(tt.a, tt.b); got != tt.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}
