package nodectl

import "testing"

func TestClusterViewContains(t *testing.T) {
	view := ClusterView{
		LiveNodes:        []string{"10.0.0.1"},
		UnreachableNodes: []string{"10.0.0.2"},
		LeavingNodes:     []string{"10.0.0.3"},
		JoiningNodes:     []string{"10.0.0.4"},
		MovingNodes:      []string{"10.0.0.5"},
	}

	tests := []struct {
		ip   string
		want bool
	}{
		{"10.0.0.1", true},
		{"10.0.0.2", true}, // down, but still owns tokens
		{"10.0.0.3", true},
		{"10.0.0.4", true},
		{"10.0.0.5", true},
		{"10.0.0.9", false}, // left the ring
	}
	for _, tt := range tests {
		if got := view.Contains(tt.ip); got != tt.want {
			t.Errorf("Contains(%q) = %v, want %v", tt.ip, got, tt.want)
		}
	}
}
