package tui

import "testing"

func TestLiteralOccurrencesAndColumns(t *testing.T) {
	first, ok := findLiteral("aaaaa", "aa", true, nil, 0)
	if !ok || first.byteOffset != 0 {
		t.Fatal("missing first non-overlapping occurrence")
	}
	second, ok := findLiteral("aaaaa", "aa", true, &first, 0)
	if !ok || second.byteOffset != 2 {
		t.Fatal("missing second non-overlapping occurrence")
	}
	if _, ok := findLiteral("aaaaa", "aa", true, &second, 0); ok {
		t.Fatal("search wrapped or admitted an overlapping occurrence")
	}
	previous, ok := findLiteral("aaaaa", "aa", false, &second, 0)
	if !ok || previous != first {
		t.Fatal("reverse traversal changed the occurrence set")
	}
	for _, tc := range []struct{ column, wantByte, wantColumn int }{
		{2, 3, 2},
		{3, 10, 9},
	} {
		p, ok := findLiteral("界needle needle", "needle", true, nil, tc.column)
		if !ok || p.byteOffset != tc.wantByte || p.column != tc.wantColumn {
			t.Errorf("column %d: got %+v, found=%v", tc.column, p, ok)
		}
	}
	if _, ok := findLiteral("text", "", true, nil, 0); ok {
		t.Fatal("empty query matched")
	}
}
