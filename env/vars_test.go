package env

import "testing"

func TestRowLimitVariable(t *testing.T) {
	setupConnsTest(t)
	if got := RowLimit(); got != 100 {
		t.Fatalf("default RowLimit = %d, want 100", got)
	}
	for _, test := range []struct {
		v    string
		want int
	}{
		{"50", 50},
		{"0", 0},
		{"-1", 0},
		{"", 0},
		{"off", 0},
	} {
		if err := Vars().Set("ROWLIMIT", test.v); err != nil {
			t.Fatal(err)
		}
		if got := RowLimit(); got != test.want {
			t.Errorf("ROWLIMIT=%q: RowLimit() = %d, want %d", test.v, got, test.want)
		}
	}
}
