package main

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// count formats a count in full up to 9,999, then shortened: 12.4k, 1.2M.
func count(v float64) string {
	v = math.Round(v)
	switch a := math.Abs(v); {
	case a < 10_000:
		return grouped(int64(v))
	case a < 1_000_000:
		return short(v/1_000) + "k"
	case a < 1_000_000_000:
		return short(v/1_000_000) + "M"
	default:
		return short(v/1_000_000_000) + "B"
	}
}

// short writes v with a decimal under 100: 12.4, 123.
func short(v float64) string {
	if math.Abs(v) >= 100 {
		return strconv.FormatFloat(math.Floor(v), 'f', 0, 64)
	}
	s := strconv.FormatFloat(math.Floor(v*10)/10, 'f', 1, 64)
	return strings.TrimSuffix(s, ".0")
}

// grouped writes n with commas between thousands.
func grouped(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

// percent formats a fraction: 42%, 4.2%, 0.4%.
func percent(f float64) string {
	p := math.Abs(f * 100)
	switch {
	case p >= 10 || p == 0:
		return fmt.Sprintf("%.0f%%", p)
	case p >= 0.1:
		return strings.TrimSuffix(fmt.Sprintf("%.1f", p), ".0") + "%"
	default:
		return "<0.1%"
	}
}

// duration formats the length of a visit: 48s, 2m 05s, 1h 12m.
func duration(d time.Duration) string {
	d = d.Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm %02ds", int(d.Minutes()), int(d.Seconds())%60)
	default:
		return fmt.Sprintf("%dh %02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}

// ago says how long ago t was, in few words.
func ago(now, t time.Time) string {
	d := now.Sub(t)
	switch {
	case d < 10*time.Second:
		return "just now"
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	default:
		return t.Format("15:04")
	}
}

// bucketLabel names a bucket of a chart: 14:00, or Mon, Oct 12.
func bucketLabel(t time.Time, hours bool) string {
	if hours {
		return t.Format("Jan 2, 15:04")
	}
	return t.Format("Mon, Jan 2")
}

// pathLabel shortens what a breakdown shows: a referrer's domain without
// www., a path as it is, and names for nothing.
func pathLabel(kind, x string) string {
	switch {
	case x == "" && kind == "referrer":
		return "Direct"
	case x == "":
		return "Unknown"
	case kind == "referrer":
		return strings.TrimPrefix(x, "www.")
	case kind == "country":
		if name, ok := countries[strings.ToUpper(x)]; ok {
			return name
		}
	}
	return x
}

var countries = map[string]string{
	"US": "United States", "GB": "United Kingdom", "DE": "Germany", "FR": "France",
	"CN": "China", "JP": "Japan", "IN": "India", "CA": "Canada", "AU": "Australia",
	"BR": "Brazil", "NL": "Netherlands", "ES": "Spain", "IT": "Italy", "SE": "Sweden",
	"KR": "South Korea", "TW": "Taiwan", "HK": "Hong Kong", "SG": "Singapore",
	"RU": "Russia", "PL": "Poland", "CH": "Switzerland", "AT": "Austria", "BE": "Belgium",
	"DK": "Denmark", "NO": "Norway", "FI": "Finland", "IE": "Ireland", "PT": "Portugal",
	"MX": "Mexico", "AR": "Argentina", "ID": "Indonesia", "VN": "Vietnam", "TH": "Thailand",
	"PH": "Philippines", "MY": "Malaysia", "TR": "Türkiye", "UA": "Ukraine", "IL": "Israel",
	"ZA": "South Africa", "NZ": "New Zealand", "CZ": "Czechia", "RO": "Romania",
	"HU": "Hungary", "GR": "Greece", "EG": "Egypt", "NG": "Nigeria", "PK": "Pakistan",
	"BD": "Bangladesh", "AE": "United Arab Emirates", "SA": "Saudi Arabia", "CL": "Chile",
	"CO": "Colombia", "PE": "Peru",
}
