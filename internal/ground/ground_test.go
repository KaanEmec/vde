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

func pointers(m []ground.Miss) []string {
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
			res := ground.Check(doc, json.RawMessage(tt.candidate), nil, 0)
			if got := pointers(res.Ungrounded); !slices.Equal(got, tt.want) {
				t.Errorf("misses = %q, want %q", got, tt.want)
			}
			if res.CriticalMissed != len(tt.want) {
				t.Errorf("CriticalMissed = %d, want %d", res.CriticalMissed, len(tt.want))
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
	res := ground.Check(doc, readFixture(t, "candidate.json"), rules, 0)
	want := []string{"/event/start_date", "/event/end_date", "/event/venue", "/event/organiser", "/travellers/0/name", "/travellers/1/name"}
	if got := pointers(res.Ungrounded); !slices.Equal(got, want) {
		t.Errorf("misses = %q, want %q", got, want)
	}
	for _, m := range res.Ungrounded {
		if !m.Critical {
			t.Errorf("miss %s is not critical", m.Pointer)
		}
	}
	// 25 leaves; not checked: currency (derived), notes and confidence (skip),
	// document_type (enum), two booleans
	if res.CriticalChecked != 19 || res.CriticalMissed != 6 || res.CriticalOK() {
		t.Errorf("CriticalChecked %d CriticalMissed %d CriticalOK %v, want 19, 6, false",
			res.CriticalChecked, res.CriticalMissed, res.CriticalOK())
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
		criticalChecked      int
		criticalMissed       int
		criticalOK, critical bool
	}{
		{"skip and derived are skipped", `{"city":"Berlin","note":"invented","travellers":["A B","C D"]}`, []string{}, 0, 0, false, false},
		{"misses outside critical do not count", `{"total":100,"venue":"Messe Nord"}`, []string{"/venue"}, 1, 0, true, false},
		{"critical miss counts", `{"total":101,"venue":"Messe Nord"}`, []string{"/total", "/venue"}, 1, 1, false, true},
		{"annotation on an array applies to its items", `{"guests":[{"name":"Jane Roe"},{"name":"John Roe"}]}`, []string{"/guests/0/name", "/guests/1/name"}, 2, 2, false, true},
		{"a deeper annotation overrides", `{"guests":[{"name":"Jane Roe","seat":"12A"}],"venue":"Messe Nord"}`, []string{"/guests/0/name", "/guests/0/seat", "/venue"}, 1, 1, false, true},
		{"critical leaf that is null still marks critical", `{"total":null,"venue":"Messe Nord"}`, []string{"/venue"}, 0, 0, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res := ground.Check(doc, json.RawMessage(tt.candidate), rules, 0)
			if got := pointers(res.Ungrounded); !slices.Equal(got, tt.want) {
				t.Errorf("misses = %q, want %q", got, tt.want)
			}
			if res.CriticalChecked != tt.criticalChecked || res.CriticalMissed != tt.criticalMissed || res.CriticalOK() != tt.criticalOK {
				t.Errorf("CriticalChecked %d CriticalMissed %d CriticalOK %v, want %d %d %v",
					res.CriticalChecked, res.CriticalMissed, res.CriticalOK(), tt.criticalChecked, tt.criticalMissed, tt.criticalOK)
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
		maxMisses       int
		want            []string
		checked         int // all checked leaves are critical here: no rule is critical
	}{
		{"booleans and nulls skipped", `{"a":true,"b":false,"c":null}`, nil, 0, []string{}, 0},
		{"empty and short strings skipped", `{"a":"","b":"  ","c":"x","d":"--"}`, nil, 0, []string{}, 0},
		{"two runes are checked", `{"a":"zq"}`, nil, 0, []string{"/a"}, 1},
		{"enum value skipped", `{"class":"first","count":7}`, enumRules, 0, []string{}, 0},
		{"non-enum value checked", `{"class":"premium","count":8}`, enumRules, 0, []string{"/class", "/count"}, 2},
		{"pointer escaping", `{"a/b":"zz","m~n":"yy"}`, nil, 0, []string{"/a~1b", "/m~0n"}, 2},
		{"scalar candidate", `"invented value"`, nil, 0, []string{""}, 1},
		{"top-level array", `["economy","invented"]`, nil, 0, []string{"/1"}, 2},
		{"big integer printed", `{"order":12345678901234567890}`, nil, 0, []string{}, 1},
		{"overflowing number", `{"n":1e400}`, nil, 0, []string{"/n"}, 1},
		{"max misses caps entries only", `["q1","q2","q3"]`, nil, 2, []string{"/0", "/1"}, 3},
		{"empty containers", `{"a":{},"b":[]}`, nil, 0, []string{}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res := ground.Check(doc, json.RawMessage(tt.candidate), tt.rules, tt.maxMisses)
			if res.Ungroundable {
				t.Fatal("Ungroundable")
			}
			if got := pointers(res.Ungrounded); !slices.Equal(got, tt.want) {
				t.Errorf("misses = %q, want %q", got, tt.want)
			}
			if res.CriticalChecked != tt.checked {
				t.Errorf("CriticalChecked %d, want %d", res.CriticalChecked, tt.checked)
			}
		})
	}
	if res := ground.Check(doc, json.RawMessage(`["q1","q2","q3"]`), nil, 2); res.CriticalMissed != 3 {
		t.Errorf("capped count: CriticalMissed %d, want 3", res.CriticalMissed)
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
			res := ground.Check(doc, json.RawMessage(tt.candidate), nil, 0)
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
			res := ground.Check(tt.doc, json.RawMessage(tt.candidate), nil, 0)
			if !res.Ungroundable || len(res.Ungrounded) != 0 || res.CriticalChecked != 0 || res.CriticalOK() {
				t.Errorf("Check = %+v, want Ungroundable with nothing checked", res)
			}
		})
	}
}

// TestCheck_noValueInOutput shows that nothing Check returns carries a candidate value.
func TestCheck_noValueInOutput(t *testing.T) {
	t.Parallel()
	doc := ground.NewText(string(readFixture(t, "text.txt")))
	candidate := readFixture(t, "candidate.json")
	res := ground.Check(doc, candidate, mustRules(t, string(readFixture(t, "schema.json"))), 0)
	fb, err := json.Marshal(res.Ungrounded)
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
