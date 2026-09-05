package completer

import (
	"testing"
)

func TestFuzzyScore(t *testing.T) {
	cases := []struct {
		name      string
		pattern   string
		candidate string
		wantOK    bool
		minScore  int
	}{
		{"empty pattern matches", "", "film", true, 0},
		{"prefix matches", "fi", "film", true, 1},
		{"case insensitive", "FIL", "film", true, 1},
		{"subsequence across separator", "uid", "user_id", true, 1},
		{"subsequence across words", "unam", "user_name", true, 1},
		{"no match", "xyz", "film", false, 0},
		{"missing char", "filx", "film", false, 0},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			score := fuzzyScore(test.pattern, test.candidate)
			if !test.wantOK {
				if score != -1 {
					t.Fatalf("fuzzyScore(%q, %q) = %d, want -1", test.pattern, test.candidate, score)
				}
				return
			}
			if score < test.minScore {
				t.Fatalf("fuzzyScore(%q, %q) = %d, want >= %d", test.pattern, test.candidate, score, test.minScore)
			}
		})
	}
}

func TestFuzzyScoreRanking(t *testing.T) {
	cases := []struct {
		name    string
		pattern string
		best    string
		other   string
	}{
		{"prefix beats spread", "up", "UPDATE", "group_updated"},
		{"boundary run beats prefix run", "uid", "user_id", "uuid_field"},
		{"boundary beats interior", "nm", "name", "enigma"},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			best := fuzzyScore(test.pattern, test.best)
			other := fuzzyScore(test.pattern, test.other)
			if best <= other {
				t.Errorf("fuzzyScore(%q, %q) = %d, want > fuzzyScore(%q, %q) = %d",
					test.pattern, test.best, best, test.pattern, test.other, other)
			}
		})
	}
}

func TestCompleteFuzzy(t *testing.T) {
	cases := []struct {
		name    string
		text    string
		options []string
		want    []string
	}{
		{
			"exact prefix first, fuzzy second",
			"sel",
			[]string{"SELECT", "SET", "name"},
			[]string{"ect"},
		},
		{
			"lowercase input keeps lowercase suffix",
			"se",
			[]string{"SELECT"},
			[]string{"lect"},
		},
		{
			"ranked matches",
			"up",
			[]string{"backup", "UPDATE", "user_password"},
			[]string{"date", "er_password", "ckup"},
		},
		{
			"non matching options excluded",
			"zz",
			[]string{"SELECT", "FROM"},
			nil,
		},
		{
			"empty text returns everything in length order",
			"",
			[]string{"actor", "film", "address"},
			[]string{"film", "actor", "address"},
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got := completeFuzzy([]rune(test.text), test.options...)
			if len(got) != len(test.want) {
				t.Fatalf("completeFuzzy(%q, %v) = %q, want %q", test.text, test.options, got, test.want)
			}
			for i := range got {
				if string(got[i]) != test.want[i] {
					t.Errorf("got[%d] = %q, want %q", i, got[i], test.want[i])
				}
			}
		})
	}
}
