package prflow

import "testing"

func TestParsePRBranch(t *testing.T) {
	cases := []struct {
		name     string
		ok       bool
		userID   string
		topic    string
	}{
		{"pr/77b4cf0e/dark-mode", true, "77b4cf0e", "dark-mode"},
		{"pr/abcdef01/foo", true, "abcdef01", "foo"},
		{"pr/00000000/a", true, "00000000", "a"},
		{"pr/77b4cf0e/with.dots-and_dashes", true, "77b4cf0e", "with.dots-and_dashes"},

		// Bad cases
		{"main", false, "", ""},
		{"user/77b4cf0e", false, "", ""},                    // user branch, not PR
		{"pr/77b4cf0e", false, "", ""},                      // missing topic
		{"pr/77b4cf0e/", false, "", ""},                     // empty topic
		{"pr/77b4cf0e//foo", false, "", ""},                 // slash in topic not allowed
		{"pr/77b4cf0e/nested/topic", false, "", ""},         // nested
		{"pr/SHORT/topic", false, "", ""},                   // userid too short
		{"pr/TOOLONGID/topic", false, "", ""},               // userid too long
		{"pr/UPPER123/topic", false, "", ""},                // uppercase in userid
		{"pr/77b4cf0e/space topic", false, "", ""},          // space in topic
		{"pr/77b4cf0e/topic!bang", false, "", ""},           // disallowed char
		{"", false, "", ""},                                 // empty
		{"prxxx/77b4cf0e/topic", false, "", ""},             // wrong prefix
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ParsePRBranch(tc.name)
			if ok != tc.ok {
				t.Fatalf("ok: got %v, want %v", ok, tc.ok)
			}
			if !ok {
				return
			}
			if got.UserID != tc.userID || got.Topic != tc.topic {
				t.Errorf("parsed: got {%q, %q}, want {%q, %q}",
					got.UserID, got.Topic, tc.userID, tc.topic)
			}
			if got.Name != tc.name {
				t.Errorf("Name: got %q, want %q", got.Name, tc.name)
			}
		})
	}
}

func TestIsPRBranch(t *testing.T) {
	if !IsPRBranch("pr/77b4cf0e/foo") {
		t.Error("valid → got false")
	}
	if IsPRBranch("main") {
		t.Error("main → got true")
	}
}

func TestParseShortStat(t *testing.T) {
	// All three numbers present
	f, i, d := parseShortStat(" 5 files changed, 120 insertions(+), 45 deletions(-)")
	if f != 5 || i != 120 || d != 45 {
		t.Errorf("3-part: got f=%d i=%d d=%d", f, i, d)
	}

	// One file
	f, i, d = parseShortStat(" 1 file changed, 3 insertions(+)")
	if f != 1 || i != 3 || d != 0 {
		t.Errorf("ins-only: got f=%d i=%d d=%d", f, i, d)
	}

	// Deletions only
	f, i, d = parseShortStat(" 2 files changed, 7 deletions(-)")
	if f != 2 || i != 0 || d != 7 {
		t.Errorf("del-only: got f=%d i=%d d=%d", f, i, d)
	}

	// Empty
	f, i, d = parseShortStat("")
	if f != 0 || i != 0 || d != 0 {
		t.Errorf("empty: got f=%d i=%d d=%d", f, i, d)
	}

	// Garbage
	f, i, d = parseShortStat("not a stat line")
	if f != 0 || i != 0 || d != 0 {
		t.Errorf("garbage: got f=%d i=%d d=%d", f, i, d)
	}
}
