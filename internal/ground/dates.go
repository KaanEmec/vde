package ground

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The date and time table. Every entry widens what counts as grounded, so the table is
// small, kept in this one file and versioned by RulesVersion. Forms are compared after
// Normalize, so punctuation at token edges does not matter: "{d}. {month} {yyyy}" and
// "{d} {month} {yyyy}" are the same form, and "March 21, 2027" is "march 21 2027".

// DateForms are the printed forms of an ISO date (YYYY-MM-DD), in the order they are
// tried. Placeholders: {yyyy}, {yy} (two-digit year), {mm} and {dd} (two digits), {m}
// and {d} (no leading zero), and {month}, which stands for every full and short month
// name in Months. The US month-first numeric order (03/21/2027) is deliberately absent.
var DateForms = []string{
	"{yyyy}-{mm}-{dd}",         // 2027-03-21
	"{dd}.{mm}.{yyyy}",         // 21.03.2027
	"{d}.{m}.{yyyy}",           // 1.3.2027
	"{dd}/{mm}/{yyyy}",         // 21/03/2027
	"{d}/{m}/{yyyy}",           // 1/3/2027
	"{dd}-{mm}-{yyyy}",         // 21-03-2027
	"{yyyy}/{mm}/{dd}",         // 2027/03/21
	"{dd}.{mm}.{yy}",           // 21.03.27
	"{d}. {month} {yyyy}",      // 21 Mar 2027, 21. März 2027, 21 Mart 2027, 1 mars 2027
	"{dd} {month} {yyyy}",      // 01 March 2027
	"{month} {d}, {yyyy}",      // March 21, 2027
	"{month} {dd}, {yyyy}",     // March 01, 2027
	"{d}-{month}-{yyyy}",       // 21-Mar-2027
	"{dd}-{month}-{yyyy}",      // 01-Mar-2027
	"{d} de {month} de {yyyy}", // 21 de marzo de 2027
}

// TimeForms are the printed forms of a time of day, in the order they are tried.
// Placeholders: {HH} (24-hour, two digits), {H} (24-hour, no leading zero), {hh} and {h}
// (12-hour), {MM} (minutes) and {ampm} (am, a.m., pm or p.m.). Seconds are not compared.
var TimeForms = []string{
	"{HH}:{MM}",        // 09:05, 14:45
	"{H}:{MM}",         // 9:05
	"{h}:{MM} {ampm}",  // 2:45 pm, 2:45 p.m.
	"{h}:{MM}{ampm}",   // 2:45pm
	"{hh}:{MM} {ampm}", // 02:45 PM
}

// MonthNames are one language's month names, January first.
type MonthNames struct {
	Language string
	Full     [12]string
	Short    [12]string
}

// Months is the month-name table used by the {month} placeholder.
var Months = []MonthNames{
	{"English",
		[12]string{"January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"},
		[12]string{"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}},
	{"Turkish",
		[12]string{"Ocak", "Şubat", "Mart", "Nisan", "Mayıs", "Haziran", "Temmuz", "Ağustos", "Eylül", "Ekim", "Kasım", "Aralık"},
		[12]string{"Oca", "Şub", "Mar", "Nis", "May", "Haz", "Tem", "Ağu", "Eyl", "Eki", "Kas", "Ara"}},
	{"German",
		[12]string{"Januar", "Februar", "März", "April", "Mai", "Juni", "Juli", "August", "September", "Oktober", "November", "Dezember"},
		[12]string{"Jan", "Feb", "Mär", "Apr", "Mai", "Jun", "Jul", "Aug", "Sep", "Okt", "Nov", "Dez"}},
	{"French",
		[12]string{"janvier", "février", "mars", "avril", "mai", "juin", "juillet", "août", "septembre", "octobre", "novembre", "décembre"},
		[12]string{"janv", "févr", "mars", "avr", "mai", "juin", "juil", "août", "sept", "oct", "nov", "déc"}},
	{"Spanish",
		[12]string{"enero", "febrero", "marzo", "abril", "mayo", "junio", "julio", "agosto", "septiembre", "octubre", "noviembre", "diciembre"},
		[12]string{"ene", "feb", "mar", "abr", "may", "jun", "jul", "ago", "sep", "oct", "nov", "dic"}},
	{"Dutch",
		[12]string{"januari", "februari", "maart", "april", "mei", "juni", "juli", "augustus", "september", "oktober", "november", "december"},
		[12]string{"jan", "feb", "mrt", "apr", "mei", "jun", "jul", "aug", "sep", "okt", "nov", "dec"}},
}

// monthNames holds the normalized, distinct names of each month across Months.
var monthNames = func() (out [12][]string) {
	for i := range out {
		seen := map[string]bool{}
		for _, l := range Months {
			for _, n := range [2]string{l.Full[i], l.Short[i]} {
				if n = Normalize(n); n != "" && !seen[n] {
					seen[n] = true
					out[i] = append(out[i], n)
				}
			}
		}
	}
	return out
}()

var (
	isoDateTime = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})(?:[Tt ](\d{2}):(\d{2})(?::\d{2}(?:[.,]\d+)?)?(?:[Zz]|[+-]\d{2}(?::?\d{2})?)?)?$`)
	isoTime     = regexp.MustCompile(`^(\d{1,2}):(\d{2})(?::\d{2}(?:[.,]\d+)?)?$`)
)

// matchTemporal grounds an ISO date, datetime or time-of-day string. temporal is false
// when s is none of those (or names an impossible date), and the caller matches it as an
// ordinary string.
func (t *Text) matchTemporal(s string) (k Kind, ok, temporal bool) {
	if m := isoDateTime.FindStringSubmatch(s); m != nil {
		y, _ := strconv.Atoi(m[1])
		mo, _ := strconv.Atoi(m[2])
		d, _ := strconv.Atoi(m[3])
		if mo < 1 || mo > 12 || time.Date(y, time.Month(mo), d, 0, 0, 0, 0, time.UTC).Day() != d {
			return 0, false, false
		}
		if m[4] == "" {
			return KindDate, t.containsDate(y, mo, d), true
		}
		h, _ := strconv.Atoi(m[4])
		mi, _ := strconv.Atoi(m[5])
		if h > 23 || mi > 59 {
			return 0, false, false
		}
		return KindDateTime, t.containsDate(y, mo, d) && t.containsTime(h, mi), true
	}
	if m := isoTime.FindStringSubmatch(s); m != nil {
		h, _ := strconv.Atoi(m[1])
		mi, _ := strconv.Atoi(m[2])
		if h > 23 || mi > 59 {
			return 0, false, false
		}
		return KindTime, t.containsTime(h, mi), true
	}
	return 0, false, false
}

// containsDate reports whether any of DateForms prints the date.
func (t *Text) containsDate(y, mo, d int) bool {
	yyyy := strconv.Itoa(y)
	yy := yyyy[len(yyyy)-2:]
	if len(t.words[yyyy]) == 0 && len(t.words[yy]) == 0 {
		return false // every form prints the year as a word of its own
	}
	for _, f := range dateForms(y, mo, d) {
		if t.containsTemporal(f) {
			return true
		}
	}
	return false
}

// dateForms renders DateForms for a date, normalized and without duplicates.
func dateForms(y, mo, d int) []string {
	base := []string{
		"{yyyy}", strconv.Itoa(y), "{yy}", pad2(y % 100),
		"{mm}", pad2(mo), "{m}", strconv.Itoa(mo), "{dd}", pad2(d), "{d}", strconv.Itoa(d),
	}
	return render(DateForms, base, "{month}", monthNames[mo-1])
}

// containsTime reports whether any of TimeForms prints the time.
func (t *Text) containsTime(h, mi int) bool {
	h12, ampm := h%12, []string{"am", "a.m."}
	if h12 == 0 {
		h12 = 12
	}
	if h >= 12 {
		ampm = []string{"pm", "p.m."}
	}
	base := []string{
		"{HH}", pad2(h), "{H}", strconv.Itoa(h), "{hh}", pad2(h12), "{h}", strconv.Itoa(h12),
		"{MM}", pad2(mi),
	}
	for _, f := range render(TimeForms, base, "{ampm}", ampm) {
		if t.containsTemporal(f) {
			return true
		}
	}
	return false
}

// render fills forms with the base placeholders and each value of the one multi-valued
// placeholder, and returns the normalized, distinct results in table order.
func render(forms, base []string, multi string, values []string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		if s = Normalize(s); !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	r := strings.NewReplacer(base...)
	for _, f := range forms {
		f = r.Replace(f)
		if !strings.Contains(f, multi) {
			add(f)
			continue
		}
		for _, v := range values {
			add(strings.ReplaceAll(f, multi, v))
		}
	}
	return out
}

func pad2(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}
