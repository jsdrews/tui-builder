package build

import "testing"

// TestResolveSelectionExactBeatsPrefix is the regression test for the
// kube_multi 404. Pre-fix, ${selection.Name} matched the "Namespace"
// column because the iterator hit it first and "namespace" has "name"
// as a case-insensitive prefix. Detail URLs ended up with the namespace
// value in the pod-name slot and 404'd.
//
// Post-fix, exact case-insensitive match wins over prefix, so Name
// resolves to the Name column even when Namespace appears earlier.
func TestResolveSelectionExactBeatsPrefix(t *testing.T) {
	sel := Selection{
		Cells:   []string{"prod", "http://localhost:8001", "default", "nginx-xyz"},
		Columns: []string{"Cluster", "ClusterURL", "Namespace", "Name"},
	}
	cases := []struct {
		key, want string
	}{
		{"Name", "nginx-xyz"},      // exact match — must NOT hit Namespace
		{"Namespace", "default"},
		{"Cluster", "prod"},
		{"ClusterURL", "http://localhost:8001"},
		// Case-insensitive exact still wins:
		{"name", "nginx-xyz"},
		{"NAMESPACE", "default"},
	}
	for _, tc := range cases {
		got := resolveSelection(tc.key, sel)
		if got != tc.want {
			t.Errorf("resolveSelection(%q) = %q, want %q", tc.key, got, tc.want)
		}
	}
}

// TestResolveSelectionMissReturnsLiteral — unresolved keys pass
// through as the literal token so a typo or wrong column name is
// visible in the resulting URL / argv, not silently substituted to "".
// (Prefix fallback was removed — it was a footgun and nothing relied
// on it.)
func TestResolveSelectionMissReturnsLiteral(t *testing.T) {
	sel := Selection{
		Cells:   []string{"10.0.0.1"},
		Columns: []string{"Pod IP"},
	}
	if got := resolveSelection("Pod", sel); got != "${selection.Pod}" {
		t.Errorf("miss should pass through as literal token; got %q", got)
	}
}
