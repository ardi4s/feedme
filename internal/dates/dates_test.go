package dates

import (
	"testing"
	"time"
)

func TestParseCommonLayouts(t *testing.T) {
	cases := []struct {
		in   string
		want string // YYYY-MM-DD, or "" when no date is recognised
	}{
		{"2026-09-26T10:30:00Z", "2026-09-26"},
		{"2026-09-26T10:30:00+07:00", "2026-09-26"},
		{"2026-09-26", "2026-09-26"},
		{"2026/09/26", "2026-09-26"},
		{"26 September 2026", "2026-09-26"},
		{"2 September 2026", "2026-09-02"},
		{"26 Sep 2026", "2026-09-26"},
		{"26 September 2026 19:30", "2026-09-26"},
		{"26 September 2026 19:30 WIB", "2026-09-26"},
		{"Senin, 26 September 2026", "2026-09-26"},
		{"Sabtu, 26 September 2026 pukul 19.30 WIB", "2026-09-26"},
		{"September 26, 2026", "2026-09-26"},
		{"Sep 26, 2026", "2026-09-26"},
		{"20260926", "2026-09-26"},
		{"26/09/2026", "2026-09-26"},
		{"26 September 2026 (GMT+7)", "2026-09-26"},
		{"26 September 2026 19:30:00", "2026-09-26"},
	}
	for _, tc := range cases {
		got, err := Parse(tc.in)
		if err != nil {
			t.Errorf("Parse(%q) error: %v", tc.in, err)
			continue
		}
		if tc.want == "" {
			if !got.IsZero() {
				t.Errorf("Parse(%q) = %s, want no date", tc.in, got.Format("2006-01-02"))
			}
			continue
		}
		if got.Format("2006-01-02") != tc.want {
			t.Errorf("Parse(%q) = %s, want %s", tc.in, got.Format("2006-01-02"), tc.want)
		}
	}
}

// Day-first and month-first slash dates are genuinely ambiguous. Indonesia and
// most of the world write day-first, so that is tried first, but a value whose
// first number exceeds 12 can only be day-first and must not be misread.
func TestParseSlashAmbiguity(t *testing.T) {
	got, _ := Parse("05/09/2026")
	if got.Format("2006-01-02") != "2026-09-05" {
		t.Errorf("05/09/2026 = %s, want day-first 2026-09-05", got.Format("2006-01-02"))
	}
	got, _ = Parse("26/09/2026")
	if got.Format("2006-01-02") != "2026-09-26" {
		t.Errorf("26/09/2026 = %s, want 2026-09-26", got.Format("2006-01-02"))
	}
	// 13 cannot be a month, so day-first is the only reading.
	got, _ = Parse("13/09/2026")
	if got.Format("2006-01-02") != "2026-09-13" {
		t.Errorf("13/09/2026 = %s", got.Format("2006-01-02"))
	}
}

func TestParseRejectsNonDates(t *testing.T) {
	// A zero time is a normal outcome, not an error: callers scan a whole page
	// for the first element that carries a date.
	for _, in := range []string{"", "   ", "baca selengkapnya", "2026", "9999", "32 September 2026", "26 September"} {
		got, err := Parse(in)
		if err != nil {
			t.Errorf("Parse(%q) unexpected error: %v", in, err)
		}
		if !got.IsZero() {
			t.Errorf("Parse(%q) = %s, want zero time", in, got.Format(time.RFC3339))
		}
	}
}

func TestParseRejectsImpossibleDay(t *testing.T) {
	// 31 February must not silently roll into March.
	if got, _ := Parse("31 Februari 2026"); !got.IsZero() {
		t.Errorf("31 Februari 2026 parsed as %s, want zero", got.Format("2006-01-02"))
	}
	if got, _ := Parse("31 April 2026"); !got.IsZero() {
		t.Errorf("31 April 2026 parsed as %s, want zero", got.Format("2006-01-02"))
	}
	// A real 31-day month still works.
	if got, _ := Parse("31 Januari 2026"); got.Format("2006-01-02") != "2026-01-31" {
		t.Errorf("31 Januari 2026 = %s", got.Format("2006-01-02"))
	}
}

func TestParseNamedMonthTime(t *testing.T) {
	got, _ := Parse("26 September 2026 19:30")
	if got.Hour() != 19 || got.Minute() != 30 {
		t.Errorf("time of day lost: %s", got.Format(time.RFC3339))
	}
}

func TestFormatters(t *testing.T) {
	ts := time.Date(2026, 9, 26, 19, 30, 0, 0, time.FixedZone("WIB", 7*3600))
	if got := FormatRFC1123Z(ts); got != "Sat, 26 Sep 2026 12:30:00 +0000" {
		t.Errorf("FormatRFC1123Z = %q", got)
	}
	if got := FormatISO(ts); got != "2026-09-26T12:30:00Z" {
		t.Errorf("FormatISO = %q", got)
	}
}

// Find exists for cards where the date is glued to other text in one node,
// which is how Indonesian listing pages usually render it.
func TestFindInsideText(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"AAA26 September 2026 19:30 WIB", "2026-09-26"},
		{"Ringkasan 12/08/2026周四", "2026-08-12"},
		{"Diperbarui: 2026-09-26T10:30:00Z", "2026-09-26"},
		{"Judul artikel September 5, 2026", "2026-09-05"},
		{"5 Oktober 2026 08.00", "2026-10-05"},
		{"Tidak ada tanggal di sini", ""},
		{"", ""},
	}
	for _, tc := range cases {
		got, ok := Find(tc.in)
		if tc.want == "" {
			if ok {
				t.Errorf("Find(%q) = %s, want nothing", tc.in, got.Format("2006-01-02"))
			}
			continue
		}
		if !ok {
			t.Errorf("Find(%q) found nothing, want %s", tc.in, tc.want)
			continue
		}
		if got.Format("2006-01-02") != tc.want {
			t.Errorf("Find(%q) = %s, want %s", tc.in, got.Format("2006-01-02"), tc.want)
		}
	}
}

// The earliest date in the text must win, not the first one that happens to
// parse: a card's headline may contain a year.
func TestFindTakesEarliest(t *testing.T) {
	got, ok := Find("Beasiswa 2026 dibuka, catfish registration 01/02/2026")
	if !ok {
		t.Fatal("nothing found")
	}
	if got.Format("2006-01-02") != "2026-02-01" {
		t.Errorf("got %s", got.Format("2006-01-02"))
	}
}
