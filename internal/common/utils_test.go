package common

import (
	"reflect"
	"testing"
)

func TestParsePlaylistIndices(t *testing.T) {
	tests := []struct {
		name      string
		spec      string
		total     int
		startFlag int
		endFlag   int
		expected  map[int]bool
	}{
		{
			name:      "empty spec with default range",
			spec:      "",
			total:     5,
			startFlag: 1,
			endFlag:   0,
			expected:  map[int]bool{1: true, 2: true, 3: true, 4: true, 5: true},
		},
		{
			name:      "empty spec with bounded range",
			spec:      "",
			total:     5,
			startFlag: 2,
			endFlag:   4,
			expected:  map[int]bool{2: true, 3: true, 4: true},
		},
		{
			name:      "single index",
			spec:      "3",
			total:     5,
			startFlag: 1,
			endFlag:   0,
			expected:  map[int]bool{3: true},
		},
		{
			name:      "multiple comma indices",
			spec:      "1,3,5",
			total:     5,
			startFlag: 1,
			endFlag:   0,
			expected:  map[int]bool{1: true, 3: true, 5: true},
		},
		{
			name:      "range",
			spec:      "2-4",
			total:     5,
			startFlag: 1,
			endFlag:   0,
			expected:  map[int]bool{2: true, 3: true, 4: true},
		},
		{
			name:      "colon prefix (up to N)",
			spec:      ":3",
			total:     5,
			startFlag: 1,
			endFlag:   0,
			expected:  map[int]bool{1: true, 2: true, 3: true},
		},
		{
			name:      "colon suffix (from N to total)",
			spec:      "3:",
			total:     5,
			startFlag: 1,
			endFlag:   0,
			expected:  map[int]bool{3: true, 4: true, 5: true},
		},
		{
			name:      "mixed list and ranges",
			spec:      "1,3-4,6",
			total:     6,
			startFlag: 1,
			endFlag:   0,
			expected:  map[int]bool{1: true, 3: true, 4: true, 6: true},
		},
		{
			name:      "out of bounds ignored",
			spec:      "1,7,8",
			total:     5,
			startFlag: 1,
			endFlag:   0,
			expected:  map[int]bool{1: true},
		},
		{
			name:      "spec constrained by start/end flags",
			spec:      "1,2,3,4,5",
			total:     5,
			startFlag: 2,
			endFlag:   4,
			expected:  map[int]bool{2: true, 3: true, 4: true},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := ParsePlaylistIndices(tc.spec, tc.total, tc.startFlag, tc.endFlag)
			if !reflect.DeepEqual(res, tc.expected) {
				t.Errorf("got %v, want %v", res, tc.expected)
			}
		})
	}
}

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"normal_name", "normal_name"},
		{"name with spaces", "name with spaces"},
		{"illegal/path\\traversal:name?*<>|\"", "illegal_path_traversal_name______"},
		{"...leading.dots", "leading.dots"},
		{"CON", "_CON"},
		{"", "video"},
	}

	for _, tc := range tests {
		res := SanitizeFilename(tc.input)
		if res != tc.expected {
			t.Errorf("SanitizeFilename(%q) = %q, want %q", tc.input, res, tc.expected)
		}
	}
}
