package main

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// dateRange is a half-open interval [startNs, endNs) in UnixNano.
type dateRange struct {
	startNs, endNs int64
}

// dateQuery is the parsed form of a date search. It carries either:
//   - one or more concrete [start, end) ranges (for month+year, numeric, or
//     season+year queries), and/or
//   - a set of month numbers (1..12) for season-only queries that should
//     match photos in those months across every year.
type dateQuery struct {
	ranges []dateRange
	months []int
}

const (
	minYear = 1900
	maxYear = 2100
)

var numericDate = regexp.MustCompile(`^(\d{1,2})[./](\d{4})$`)

// Folded, ASCII-only keys. Polish + English month names, both languages'
// diacritics stripped so foldQuery matches them.
var monthNames = map[string]time.Month{
	"styczen": time.January, "january": time.January,
	"luty": time.February, "february": time.February,
	"marzec": time.March, "march": time.March,
	"kwiecien": time.April, "april": time.April,
	"maj": time.May, "may": time.May,
	"czerwiec": time.June, "june": time.June,
	"lipiec": time.July, "july": time.July,
	"sierpien": time.August, "august": time.August,
	"wrzesien": time.September, "september": time.September,
	"pazdziernik": time.October, "october": time.October,
	"listopad": time.November, "november": time.November,
	"grudzien": time.December, "december": time.December,
}

// Season → list of (startMonth, endMonthExclusive) tuples within the same
// calendar year. Winter is two ranges: Jan-Feb and Dec of year N.
var seasonMonths = map[string][][2]time.Month{
	"wiosna": {{time.March, time.June}},
	"spring": {{time.March, time.June}},
	"lato":   {{time.June, time.September}},
	"summer": {{time.June, time.September}},
	"jesien": {{time.September, time.December}},
	"autumn": {{time.September, time.December}},
	"fall":   {{time.September, time.December}},
	"zima":   {{time.January, time.March}, {time.December, time.January + 12}},
	"winter": {{time.January, time.March}, {time.December, time.January + 12}},
}

// parseDateQuery attempts to parse s as a natural-language date expression.
// Returns nil when s doesn't parse as any known date form, in which case
// callers should fall back to a plain filename search.
func parseDateQuery(s string) *dateQuery {
	q := foldQuery(s)
	if q == "" {
		return nil
	}

	// Single-token season → match those months across every year.
	if spans, ok := seasonMonths[q]; ok {
		return &dateQuery{months: monthsFromSpans(spans)}
	}

	if m := numericDate.FindStringSubmatch(q); m != nil {
		month, _ := strconv.Atoi(m[1])
		year, _ := strconv.Atoi(m[2])
		if month < 1 || month > 12 || year < minYear || year > maxYear {
			return nil
		}
		return &dateQuery{ranges: []dateRange{monthRange(year, time.Month(month))}}
	}

	fields := strings.Fields(q)
	if len(fields) != 2 {
		return nil
	}
	year, err := strconv.Atoi(fields[1])
	if err != nil || year < minYear || year > maxYear {
		return nil
	}
	if month, ok := monthNames[fields[0]]; ok {
		return &dateQuery{ranges: []dateRange{monthRange(year, month)}}
	}
	if spans, ok := seasonMonths[fields[0]]; ok {
		out := make([]dateRange, 0, len(spans))
		for _, sp := range spans {
			out = append(out, monthSpanRange(year, sp[0], sp[1]))
		}
		return &dateQuery{ranges: out}
	}
	return nil
}

// monthsFromSpans flattens season month spans into the concrete month numbers
// they cover (1..12). Winter's Dec→Jan+12 span wraps back to month 12.
func monthsFromSpans(spans [][2]time.Month) []int {
	var out []int
	for _, sp := range spans {
		for m := sp[0]; m < sp[1]; m++ {
			out = append(out, ((int(m)-1)%12)+1)
		}
	}
	return out
}

// monthRange returns [Y-M-01, Y-(M+1)-01) in UTC.
func monthRange(year int, m time.Month) dateRange {
	return monthSpanRange(year, m, m+1)
}

// monthSpanRange returns [Y-start-01, Y-end-01) in UTC. end may exceed 12
// (time.Date normalizes to the next year), which is how winter's Dec range
// carries into January of year+1.
func monthSpanRange(year int, start, end time.Month) dateRange {
	return dateRange{
		startNs: time.Date(year, start, 1, 0, 0, 0, 0, time.UTC).UnixNano(),
		endNs:   time.Date(year, end, 1, 0, 0, 0, 0, time.UTC).UnixNano(),
	}
}

// foldQuery lowercases s, strips Polish diacritics, and collapses whitespace.
func foldQuery(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	b.Grow(len(s))
	lastSpace := false
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' {
			if !lastSpace && b.Len() > 0 {
				b.WriteByte(' ')
				lastSpace = true
			}
			continue
		}
		lastSpace = false
		switch r {
		case 'ą':
			b.WriteByte('a')
		case 'ć':
			b.WriteByte('c')
		case 'ę':
			b.WriteByte('e')
		case 'ł':
			b.WriteByte('l')
		case 'ń':
			b.WriteByte('n')
		case 'ó':
			b.WriteByte('o')
		case 'ś':
			b.WriteByte('s')
		case 'ź', 'ż':
			b.WriteByte('z')
		default:
			b.WriteRune(r)
		}
	}
	return strings.TrimRight(b.String(), " ")
}
