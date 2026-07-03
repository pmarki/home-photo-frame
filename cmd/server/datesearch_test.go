package main

import (
	"reflect"
	"testing"
	"time"
)

func mkRange(y int, startM, endM time.Month) dateRange {
	return dateRange{
		startNs: time.Date(y, startM, 1, 0, 0, 0, 0, time.UTC).UnixNano(),
		endNs:   time.Date(y, endM, 1, 0, 0, 0, 0, time.UTC).UnixNano(),
	}
}

func rangesOnly(rs ...dateRange) *dateQuery {
	return &dateQuery{ranges: rs}
}

func monthsOnly(months ...int) *dateQuery {
	return &dateQuery{months: months}
}

func TestParseDateQuery(t *testing.T) {
	tests := []struct {
		in   string
		want *dateQuery
	}{
		// Numeric month.year
		{"2.2010", rangesOnly(mkRange(2010, time.February, time.March))},
		{"02.2010", rangesOnly(mkRange(2010, time.February, time.March))},
		{"2/2010", rangesOnly(mkRange(2010, time.February, time.March))},
		{"12.1999", rangesOnly(mkRange(1999, time.December, time.January+12))},

		// Polish months
		{"luty 2010", rangesOnly(mkRange(2010, time.February, time.March))},
		{"styczen 2010", rangesOnly(mkRange(2010, time.January, time.February))},
		{"styczeń 2010", rangesOnly(mkRange(2010, time.January, time.February))},
		{"grudzień 2010", rangesOnly(mkRange(2010, time.December, time.January+12))},
		{"październik 2010", rangesOnly(mkRange(2010, time.October, time.November))},
		{"LUTY 2010", rangesOnly(mkRange(2010, time.February, time.March))},
		{"  luty   2010  ", rangesOnly(mkRange(2010, time.February, time.March))},

		// English months
		{"February 2010", rangesOnly(mkRange(2010, time.February, time.March))},
		{"december 2010", rangesOnly(mkRange(2010, time.December, time.January+12))},

		// Polish seasons + year
		{"wiosna 2010", rangesOnly(mkRange(2010, time.March, time.June))},
		{"lato 2010", rangesOnly(mkRange(2010, time.June, time.September))},
		{"jesien 2010", rangesOnly(mkRange(2010, time.September, time.December))},
		{"jesień 2010", rangesOnly(mkRange(2010, time.September, time.December))},
		{"zima 2010", rangesOnly(
			mkRange(2010, time.January, time.March),
			mkRange(2010, time.December, time.January+12),
		)},

		// English seasons + year
		{"autumn 2010", rangesOnly(mkRange(2010, time.September, time.December))},
		{"fall 2010", rangesOnly(mkRange(2010, time.September, time.December))},
		{"spring 2010", rangesOnly(mkRange(2010, time.March, time.June))},
		{"summer 2010", rangesOnly(mkRange(2010, time.June, time.September))},
		{"winter 2010", rangesOnly(
			mkRange(2010, time.January, time.March),
			mkRange(2010, time.December, time.January+12),
		)},

		// Season-only (no year) → month set matching every year
		{"zima", monthsOnly(1, 2, 12)},
		{"wiosna", monthsOnly(3, 4, 5)},
		{"lato", monthsOnly(6, 7, 8)},
		{"jesien", monthsOnly(9, 10, 11)},
		{"jesień", monthsOnly(9, 10, 11)},
		{"ZIMA", monthsOnly(1, 2, 12)},
		{"  lato  ", monthsOnly(6, 7, 8)},
		{"winter", monthsOnly(1, 2, 12)},
		{"spring", monthsOnly(3, 4, 5)},
		{"summer", monthsOnly(6, 7, 8)},
		{"autumn", monthsOnly(9, 10, 11)},
		{"fall", monthsOnly(9, 10, 11)},

		// Non-matches — must return nil so callers fall back to filename search.
		{"", nil},
		{"beach", nil},
		{"luty", nil},          // month alone is not enough — needs a year
		{"2010", nil},          // year alone
		{"13.2010", nil},       // bad month
		{"0.2010", nil},        // bad month
		{"2.999", nil},         // bad year (too short)
		{"1234567890", nil},    // big number
		{"luty 12345", nil},    // 5-digit year
		{"luty 1500", nil},     // year outside sanity band
		{"luty 2200", nil},     // year outside sanity band
		{"nope 2010", nil},     // unknown token
		{"luty 2010 extra", nil}, // trailing garbage
	}
	for _, tc := range tests {
		got := parseDateQuery(tc.in)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("parseDateQuery(%q) = %+v, want %+v", tc.in, got, tc.want)
		}
	}
}
