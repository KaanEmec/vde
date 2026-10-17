package ground_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kaanemec/vde/internal/ground"
)

func pointers(m ground.Misses) []string {
	out := []string{}
	for _, x := range m {
		out = append(out, x.Pointer)
	}
	return out
}

func mustRules(t *testing.T, schema string) ground.Rules {
	t.Helper()
	rules, _, err := ground.RulesFromSchema([]byte(schema))
	if err != nil {
		t.Fatal(err)
	}
	return rules
}

const bookingText = `Buchungsbestätigung
Reisender: MÜLLER/ANNA MRS
Flug LH 1234 von München nach İstanbul
Abflug: 14.05.2027 um 09:05, Ankunft 13:20
Hotel Şehir Palas, Beyoğlu
Gesamtpreis: 1.234,50 EUR (inkl. MwSt.)
Buchungsnummer: LX42QZ`

func TestCheck_planted(t *testing.T) {
	t.Parallel()
	doc := ground.NewText(bookingText)
	tests := []struct {
		name, candidate string
		want            []string
	}{
		{"all printed in other formats", `{"traveller":"Anna Müller","flight":"LH 1234","from":"MUNCHEN","to":"Istanbul",
			"departure":"2027-05-14T09:05:00","arrival_time":"13:20","hotel":"sehir palas","total":1234.5,"total_text":"EUR 1,234.50","ref":"lx42qz"}`, []string{}},
		{"invented date", `{"departure":"2027-05-15T09:05"}`, []string{"/departure"}},
		{"invented time", `{"departure":"2027-05-14T10:05"}`, []string{"/departure"}},
		{"invented name", `{"traveller":"Anna Schmidt"}`, []string{"/traveller"}},
		{"invented hotel", `{"hotel":"Grand Palas Hotel"}`, []string{"/hotel"}},
		{"invented amount", `{"total":1243.5}`, []string{"/total"}},
		{"invented reference", `{"ref":"LX42QX"}`, []string{"/ref"}},
		{"several invented among printed", `{"a":"Beyoğlu","b":["Anna Müller","Max Muster"],"c":{"d":"2027-06-01","e":12}}`,
			[]string{"/b/1", "/c/d", "/c/e"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res := ground.Check(doc, json.RawMessage(tt.candidate), nil, ground.Options{})
			if got := pointers(res.Ungrounded); !slices.Equal(got, tt.want) {
				t.Errorf("misses = %q, want %q", got, tt.want)
			}
			if res.Missed != len(tt.want) || res.CriticalMissed != len(tt.want) {
				t.Errorf("Missed, CriticalMissed = %d, %d, want %d", res.Missed, res.CriticalMissed, len(tt.want))
			}
		})
	}
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "event-invoice", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestCheck_eventInvoice(t *testing.T) {
	t.Parallel()
	doc := ground.NewText(string(readFixture(t, "text.txt")))
	rules := mustRules(t, string(readFixture(t, "schema.json")))
	res := ground.Check(doc, readFixture(t, "candidate.json"), rules, ground.Options{})
	want := []string{"/event/start_date", "/event/end_date", "/event/venue", "/event/organiser", "/travellers/0/name", "/travellers/1/name"}
	if got := pointers(res.Ungrounded); !slices.Equal(got, want) {
		t.Errorf("misses = %q, want %q", got, want)
	}
	wantKinds := []ground.Kind{ground.KindDate, ground.KindDateTime, ground.KindString, ground.KindString, ground.KindString, ground.KindString}
	for i, m := range res.Ungrounded {
		if i < len(wantKinds) && (m.Kind != wantKinds[i] || !m.Critical) {
			t.Errorf("miss %s: kind %v critical %v, want %v critical", m.Pointer, m.Kind, m.Critical, wantKinds[i])
		}
	}
	// skipped: currency (derived), notes and confidence (skip), document_type (enum), two booleans
	if res.Skipped != 6 || res.Checked != 19 || res.CriticalMissed != 6 || res.CriticalOK() {
		t.Errorf("Skipped %d Checked %d CriticalMissed %d CriticalOK %v, want 6, 19, 6, false",
			res.Skipped, res.Checked, res.CriticalMissed, res.CriticalOK())
	}
}

func TestCheck_annotations(t *testing.T) {
	t.Parallel()
	doc := ground.NewText("Invoice 2027-03-21 total 100,00 EUR from Hamburg")
	schema := `{"type":"object","properties":{
		"total":{"type":"number","x-vde-grounding":"critical"},
		"city":{"type":"string","x-vde-grounding":"derived"},
		"note":{"type":"string","x-vde-grounding":"skip"},
		"venue":{"type":"string"},
		"travellers":{"type":"array","x-vde-grounding":"skip","items":{"type":"string"}},
		"guests":{"type":"array","x-vde-grounding":"critical","items":{"type":"object","properties":{"name":{"type":"string"},"seat":{"type":"string","x-vde-grounding":"check"}}}}}}`
	rules := mustRules(t, schema)
	tests := []struct {
		name, candidate      string
		want                 []string
		checked, skipped     int
		criticalMissed       int
		criticalOK, critical bool
	}{
		{"skip and derived are skipped", `{"city":"Berlin","note":"invented","travellers":["A B","C D"]}`, []string{}, 0, 4, 0, false, false},
		{"misses outside critical do not count", `{"total":100,"venue":"Messe Nord"}`, []string{"/venue"}, 2, 0, 0, true, false},
		{"critical miss counts", `{"total":101,"venue":"Messe Nord"}`, []string{"/total", "/venue"}, 2, 0, 1, false, true},
		{"annotation on an array applies to its items", `{"guests":[{"name":"Jane Roe"},{"name":"John Roe"}]}`, []string{"/guests/0/name", "/guests/1/name"}, 2, 0, 2, false, true},
		{"a deeper annotation overrides", `{"guests":[{"name":"Jane Roe","seat":"12A"}],"venue":"Messe Nord"}`, []string{"/guests/0/name", "/guests/0/seat", "/venue"}, 3, 0, 1, false, true},
		{"critical leaf that is null still marks critical", `{"total":null,"venue":"Messe Nord"}`, []string{"/venue"}, 1, 1, 0, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res := ground.Check(doc, json.RawMessage(tt.candidate), rules, ground.Options{})
			if got := pointers(res.Ungrounded); !slices.Equal(got, tt.want) {
				t.Errorf("misses = %q, want %q", got, tt.want)
			}
			if res.Checked != tt.checked || res.Skipped != tt.skipped || res.CriticalMissed != tt.criticalMissed || res.CriticalOK() != tt.criticalOK {
				t.Errorf("Checked %d Skipped %d CriticalMissed %d CriticalOK %v, want %d %d %d %v",
					res.Checked, res.Skipped, res.CriticalMissed, res.CriticalOK(), tt.checked, tt.skipped, tt.criticalMissed, tt.criticalOK)
			}
			if len(res.Ungrounded) > 0 && res.Ungrounded[0].Critical != tt.critical {
				t.Errorf("first miss critical = %v, want %v", res.Ungrounded[0].Critical, tt.critical)
			}
		})
	}
}

func TestCheck_leaves(t *testing.T) {
	t.Parallel()
	doc := ground.NewText("Order 12345678901234567890 on 2027-03-21, class economy, a/b and m~n")
	enumRules := func(loc []string) (ground.Rule, []string) {
		if slices.Equal(loc, []string{"class"}) {
			return ground.RuleNone, []string{"business", "first"}
		}
		if slices.Equal(loc, []string{"count"}) {
			return ground.RuleNone, []string{"7", "x"}
		}
		return ground.RuleNone, nil
	}
	tests := []struct {
		name, candidate string
		rules           ground.Rules
		opts            ground.Options
		want            []string
		checked         int
		skipped         int
	}{
		{"booleans and nulls skipped", `{"a":true,"b":false,"c":null}`, nil, ground.Options{}, []string{}, 0, 3},
		{"empty and short strings skipped", `{"a":"","b":"  ","c":"x","d":"--"}`, nil, ground.Options{}, []string{}, 0, 4},
		{"min length option", `{"a":"xyz"}`, nil, ground.Options{MinLength: 4}, []string{}, 0, 1},
		{"enum value skipped", `{"class":"first","count":7}`, enumRules, ground.Options{}, []string{}, 0, 2},
		{"non-enum value checked", `{"class":"premium","count":8}`, enumRules, ground.Options{}, []string{"/class", "/count"}, 2, 0},
		{"pointer escaping", `{"a/b":"zz","m~n":"yy"}`, nil, ground.Options{}, []string{"/a~1b", "/m~0n"}, 2, 0},
		{"scalar candidate", `"invented value"`, nil, ground.Options{}, []string{""}, 1, 0},
		{"top-level array", `["economy","invented"]`, nil, ground.Options{}, []string{"/1"}, 2, 0},
		{"big integer printed", `{"order":12345678901234567890}`, nil, ground.Options{}, []string{}, 1, 0},
		{"overflowing number", `{"n":1e400}`, nil, ground.Options{}, []string{"/n"}, 1, 0},
		{"max misses caps entries only", `["q1","q2","q3"]`, nil, ground.Options{MaxMisses: 2}, []string{"/0", "/1"}, 3, 0},
		{"empty containers", `{"a":{},"b":[]}`, nil, ground.Options{}, []string{}, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res := ground.Check(doc, json.RawMessage(tt.candidate), tt.rules, tt.opts)
			if res.Ungroundable {
				t.Fatal("Ungroundable")
			}
			if got := pointers(res.Ungrounded); !slices.Equal(got, tt.want) {
				t.Errorf("misses = %q, want %q", got, tt.want)
			}
			if res.Checked != tt.checked || res.Skipped != tt.skipped {
				t.Errorf("Checked %d Skipped %d, want %d %d", res.Checked, res.Skipped, tt.checked, tt.skipped)
			}
		})
	}
	if res := ground.Check(doc, json.RawMessage(`["q1","q2","q3"]`), nil, ground.Options{MaxMisses: 2}); res.Missed != 3 || res.CriticalMissed != 3 {
		t.Errorf("capped counts: Missed %d CriticalMissed %d, want 3 3", res.Missed, res.CriticalMissed)
	}
}

// TestCheck_nothingCritical: a candidate without a checked critical leaf gives grounding
// no evidence, so it never passes the auto gate.
func TestCheck_nothingCritical(t *testing.T) {
	t.Parallel()
	doc := ground.NewText("Invoice 2027-03-21 total 100,00 EUR from Hamburg")
	tests := []struct {
		name, candidate string
		criticalOK      bool
	}{
		{"empty object", `{}`, false},
		{"empty array", `[]`, false},
		{"only nulls", `{"a":null,"b":{"c":null}}`, false},
		{"only booleans", `{"a":true,"b":false}`, false},
		{"one grounded leaf", `{"city":"Hamburg","a":null}`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res := ground.Check(doc, json.RawMessage(tt.candidate), nil, ground.Options{})
			if res.CriticalOK() != tt.criticalOK {
				t.Errorf("CriticalOK = %v (CriticalChecked %d), want %v", res.CriticalOK(), res.CriticalChecked, tt.criticalOK)
			}
		})
	}
}

func TestCheck_ungroundable(t *testing.T) {
	t.Parallel()
	doc := ground.NewText("text")
	deep := strings.Repeat("[", 1100) + strings.Repeat("]", 1100)
	tests := []struct {
		name      string
		doc       *ground.Text
		candidate string
	}{
		{"nil text", nil, `{"a":"b"}`},
		{"invalid json", doc, `{"a":`},
		{"trailing data", doc, `{"a":"text"} {}`},
		{"empty", doc, ``},
		{"too deep", doc, deep},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res := ground.Check(tt.doc, json.RawMessage(tt.candidate), nil, ground.Options{})
			if !res.Ungroundable || len(res.Ungrounded) != 0 || res.Checked != 0 || res.CriticalOK() {
				t.Errorf("Check = %+v, want Ungroundable with nothing checked", res)
			}
		})
	}
}

func TestMisses_Feedback(t *testing.T) {
	t.Parallel()
	if got := ground.Misses(nil).Feedback(); got != nil {
		t.Errorf("Feedback of no misses = %v, want nil", got)
	}
	m := ground.Misses{{Pointer: "/a", Kind: ground.KindDate, Critical: true}, {Pointer: "/b/0"}}
	got := m.Feedback()
	want := []ground.Feedback{
		{Pointer: "/a", Message: "value not found in the document", Source: "grounding"},
		{Pointer: "/b/0", Message: "value not found in the document", Source: "grounding"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("Feedback = %+v, want %+v", got, want)
	}
	b, err := json.Marshal(got[0])
	if err != nil || string(b) != `{"pointer":"/a","message":"value not found in the document","source":"grounding"}` {
		t.Errorf("json = %s, %v", b, err)
	}
}

func TestKind_String(t *testing.T) {
	t.Parallel()
	for k, want := range map[ground.Kind]string{ground.KindString: "string", ground.KindNumber: "number",
		ground.KindDate: "date", ground.KindDateTime: "datetime", ground.KindTime: "time", ground.Kind(9): "kind(9)"} {
		if got := k.String(); got != want {
			t.Errorf("Kind(%d).String() = %q, want %q", k, got, want)
		}
	}
}

// TestCheck_noValueInOutput shows that nothing Check returns carries a candidate value.
func TestCheck_noValueInOutput(t *testing.T) {
	t.Parallel()
	doc := ground.NewText(string(readFixture(t, "text.txt")))
	candidate := readFixture(t, "candidate.json")
	res := ground.Check(doc, candidate, mustRules(t, string(readFixture(t, "schema.json"))), ground.Options{})
	fb, err := json.Marshal(res.Ungrounded.Feedback())
	if err != nil {
		t.Fatal(err)
	}
	out := fmt.Sprintf("%+v %v %s", res, res.Ungrounded, fb)
	var values []string
	var collect func(v any)
	collect = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			for _, c := range v {
				collect(c)
			}
		case []any:
			for _, c := range v {
				collect(c)
			}
		case string:
			values = append(values, v)
		case float64:
			values = append(values, fmt.Sprint(v))
		}
	}
	var decoded any
	if err := json.Unmarshal(candidate, &decoded); err != nil {
		t.Fatal(err)
	}
	collect(decoded)
	for _, v := range values {
		if len(v) > 3 && strings.Contains(out, v) {
			t.Errorf("output carries a candidate value (%d bytes)", len(v))
		}
	}
}
