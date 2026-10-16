package gliner

import "testing"

func TestKeepWord(t *testing.T) {
	cases := []struct {
		name              string
		emitted, n, limit int
		kept              bool
		want              bool
	}{
		{"first word is always kept", 32, 100, 512, false, true},
		{"first word larger than the limit is still kept", 32, 5000, 512, false, true},
		{"next word fits exactly", 500, 12, 512, true, true},
		{"next word overflows by one", 500, 13, 512, true, false},
		{"no room left", 512, 1, 512, true, false},
		{"room for exactly one token", 511, 1, 512, true, true},
	}
	for _, tc := range cases {
		if got := keepWord(tc.emitted, tc.n, tc.limit, tc.kept); got != tc.want {
			t.Errorf("%s: keepWord(%d, %d, %d, %v) = %v, want %v",
				tc.name, tc.emitted, tc.n, tc.limit, tc.kept, got, tc.want)
		}
	}
}
