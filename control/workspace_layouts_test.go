package main

import "testing"

func TestValidateWorkspaceGrid(t *testing.T) {
	cases := []struct {
		name string
		grid workspaceGrid
		ok   bool
	}{
		{"default", defaultWorkspaceLayout.Layout, true},
		{"empty", workspaceGrid{}, true},
		{"unknown panel", workspaceGrid{Items: []workspaceItem{{Panel: "nope", W: 1, H: 1}}}, false},
		{"duplicate", workspaceGrid{Items: []workspaceItem{
			{Panel: "chat", X: 0, W: 1, H: 1}, {Panel: "chat", X: 1, W: 1, H: 1},
		}}, false},
		{"overflows", workspaceGrid{Items: []workspaceItem{{Panel: "chat", X: 12, W: 13, H: 1}}}, false},
		{"zero size", workspaceGrid{Items: []workspaceItem{{Panel: "chat", W: 0, H: 1}}}, false},
		{"overlap", workspaceGrid{Items: []workspaceItem{
			{Panel: "chat", X: 0, Y: 0, W: 6, H: 6}, {Panel: "diff", X: 5, Y: 5, W: 2, H: 2},
		}}, false},
		{"touching", workspaceGrid{Items: []workspaceItem{
			{Panel: "chat", X: 0, Y: 0, W: 6, H: 6}, {Panel: "diff", X: 6, Y: 0, W: 6, H: 6},
		}}, true},
	}
	for _, tc := range cases {
		if err := validateWorkspaceGrid(tc.grid); (err == nil) != tc.ok {
			t.Errorf("%s: got %v, want ok=%v", tc.name, err, tc.ok)
		}
	}
}
