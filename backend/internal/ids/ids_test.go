package ids

import (
	"regexp"
	"testing"
	"time"
)

func TestNewShape(t *testing.T) {
	re := regexp.MustCompile(`^pol_[a-z2-7]{12}$`)
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		id := New(Policy)
		if !re.MatchString(id) {
			t.Fatalf("bad id %q", id)
		}
		if seen[id] {
			t.Fatalf("duplicate %q", id)
		}
		seen[id] = true
	}
}

func TestTimeRoundTrip(t *testing.T) {
	tm := time.Date(2024, 7, 8, 9, 15, 0, 0, time.UTC)
	if s := FormatTime(tm); s != "2024-07-08T09:15:00Z" {
		t.Fatal(s)
	}
	if got, err := ParseTime("2024-07-08T09:15:00Z"); err != nil || !got.Equal(tm) {
		t.Fatal(got, err)
	}
	for _, bad := range []string{"", "2024-07-08T09:15:00+00:00", "2024-07-08 09:15:00Z"} {
		if _, err := ParseTime(bad); err == nil {
			t.Errorf("%q: want error", bad)
		}
	}
}
