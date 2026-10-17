package ground

import (
	"strings"
	"testing"
)

func TestText_Contains_dates(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, doc, value string
		want             bool
	}{
		{"iso", "Datum 2027-03-21", "2027-03-21", true},
		{"dd.mm.yyyy", "Datum: 21.03.2027.", "2027-03-21", true},
		{"d.m.yyyy", "am 1.3.2027", "2027-03-01", true},
		{"dd/mm/yyyy", "21/03/2027", "2027-03-21", true},
		{"d/m/yyyy", "1/3/2027", "2027-03-01", true},
		{"dd-mm-yyyy", "21-03-2027", "2027-03-21", true},
		{"yyyy/mm/dd", "2027/03/21", "2027-03-21", true},
		{"dd.mm.yy", "21.03.27", "2027-03-21", true},
		{"english short", "21 Mar 2027", "2027-03-21", true},
		{"english full", "21 March 2027", "2027-03-21", true},
		{"english month first", "March 21, 2027", "2027-03-21", true},
		{"english short month first", "Mar 21 2027", "2027-03-21", true},
		{"leading zero day with name", "01 March 2027", "2027-03-01", true},
		{"month first leading zero", "March 01, 2027", "2027-03-01", true},
		{"dashed month name", "21-Mar-2027", "2027-03-21", true},
		{"german", "Rechnungsdatum: 21. März 2027", "2027-03-21", true},
		{"german december", "3. Dezember 2027", "2027-12-03", true},
		{"german short", "21. Mär. 2027", "2027-03-21", true},
		{"turkish", "Tarih: 21 Mart 2027", "2027-03-21", true},
		{"turkish with dotted letters", "1 ŞUBAT 2027", "2027-02-01", true},
		{"turkish dotless", "5 Mayıs 2027", "2027-05-05", true},
		{"turkish short", "12 Ağu 2027", "2027-08-12", true},
		{"french", "le 1 février 2027", "2027-02-01", true},
		{"spanish", "21 de marzo de 2027", "2027-03-21", true},
		{"dutch", "21 maart 2027", "2027-03-21", true},
		{"dutch short", "21 mrt 2027", "2027-03-21", true},
		{"iso glued to a time", "2027-03-21T14:45", "2027-03-21", true},
		{"us order is not in the table", "03/21/2027", "2027-03-21", false},
		{"day and month swapped", "03.04.2027", "2027-03-04", false},
		{"other day", "22.03.2027", "2027-03-21", false},
		{"other year", "21.03.2026", "2027-03-21", false},
		{"digit before the day", "121.03.2027", "2027-03-21", false},
		{"digit after the year", "21.03.20271", "2027-03-21", false},
		{"other month name", "21 April 2027", "2027-03-21", false},
		{"letters before the month name", "21 Xmar 2027", "2027-03-21", false},
		{"year absent", "21.03.", "2027-03-21", false},
		{"impossible date is a plain string", "2027-02-30", "2027-02-30", true},
		{"impossible date absent", "nothing", "2027-02-30", false},
		{"impossible month", "nothing", "2027-13-01", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := NewText(tt.doc).Contains(tt.value); got != tt.want {
				t.Errorf("text %q: Contains(%q) = %v, want %v", tt.doc, tt.value, got, tt.want)
			}
		})
	}
}

func TestText_Contains_times(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, doc, value string
		want             bool
	}{
		{"datetime 24h", "21.03.2027 14:45", "2027-03-21T14:45:00", true},
		{"datetime with zone", "21.03.2027 14:45", "2027-03-21T14:45:00+01:00", true},
		{"datetime utc fraction", "21.03.2027 14:45", "2027-03-21T14:45:00.000Z", true},
		{"datetime space separator", "21.03.2027 14:45", "2027-03-21 14:45", true},
		{"datetime pm", "21 March 2027, 2:45 pm", "2027-03-21T14:45", true},
		{"datetime p.m.", "21 March 2027, 2:45 p.m.", "2027-03-21T14:45", true},
		{"datetime glued pm", "21/03/2027 2:45PM", "2027-03-21T14:45", true},
		{"datetime padded 12h", "21/03/2027 02:45 PM", "2027-03-21T14:45", true},
		{"datetime iso glued", "dep 2027-03-21T14:45", "2027-03-21T14:45:00", true},
		{"datetime wrong time", "21.03.2027 15:45", "2027-03-21T14:45", false},
		{"datetime pm is not am", "21.03.2027 2:45 am", "2027-03-21T14:45", false},
		{"datetime date missing", "14:45", "2027-03-21T14:45", false},
		{"datetime time missing", "21.03.2027", "2027-03-21T14:45", false},
		{"time padded found unpadded", "boarding 9:05", "09:05", true},
		{"time unpadded found padded", "boarding 09:05", "9:05", true},
		{"time with seconds", "departure 14:45", "14:45:30", true},
		{"time seconds printed", "departure 14:45:00", "14:45", true},
		{"midnight 12h", "12:30 am", "00:30", true},
		{"noon 12h", "12:15 pm", "12:15", true},
		{"time digit before", "114:45", "14:45", false},
		{"time partial", "boarding 19:05", "9:05", false},
		{"impossible time is a plain string", "nothing", "25:61", false},
		{"impossible datetime is a plain string", "nothing", "2027-03-21T25:00", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := NewText(tt.doc).Contains(tt.value); got != tt.want {
				t.Errorf("text %q: Contains(%q) = %v, want %v", tt.doc, tt.value, got, tt.want)
			}
		})
	}
}

func TestDateTables(t *testing.T) {
	t.Parallel()
	for i, names := range monthNames {
		if len(names) == 0 {
			t.Errorf("month %d has no names", i+1)
		}
		for _, n := range names {
			if n == "" || n != Normalize(n) {
				t.Errorf("month %d name %q is not normalized", i+1, n)
			}
		}
	}
	for _, l := range Months {
		for i := range 12 {
			if l.Full[i] == "" || l.Short[i] == "" {
				t.Errorf("%s month %d is empty", l.Language, i+1)
			}
		}
	}
	forms := dateForms(2027, 3, 21)
	for _, f := range forms {
		if strings.Contains(f, "{") {
			t.Errorf("form %q has an unfilled placeholder", f)
		}
	}
	if len(forms) < len(DateForms) {
		t.Errorf("rendered %d forms from %d entries", len(forms), len(DateForms))
	}
}
