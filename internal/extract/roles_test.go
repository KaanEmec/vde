package extract

import (
	"errors"
	"slices"
	"testing"

	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/document"
	"github.com/kaanemec/vde/internal/provider"
	"github.com/kaanemec/vde/internal/provider/fake"
)

// readersConfig: group wide reads PDFs natively and PNG; group pages reads only PNG and
// text, so PDFs only through page images; group text reads only text.
const readersConfig = `
endpoints:
  wide: {provider: fake, models: {wide-model: {accepts: [text/plain, application/pdf, image/png]}}}
  pages: {provider: fake, models: {pages-model: {accepts: [text/plain, image/png]}}}
  text: {provider: fake, models: {text-model: {accepts: [text/plain]}}}
groups:
  wide: {endpoints: [{endpoint: wide, model: wide-model}]}
  pages: {endpoints: [{endpoint: pages, model: pages-model}]}
  text: {endpoints: [{endpoint: text, model: text-model}]}
limits: {max_attempts: 3, job_timeout: 5m}
`

func TestNewRoles_tiers(t *testing.T) {
	cfg := loadConfig(t, readersConfig+`
roles:
  extractor: {group: wide, escalation: [{from_attempt: 2, group: pages}, {from_attempt: 3, group: wide}]}
  judge: {group: pages, escalation: [{from_attempt: 3, group: text}], feedback_fallback: llm, feedback_group: wide}
  second_judge: {group: text}
`)
	roles, err := NewRoles(cfg, func(*config.Endpoint) (provider.Provider, error) { return fake.New(nil), nil })
	if err != nil {
		t.Fatal(err)
	}
	names := func(steps []Escalation) (out []string) {
		for _, s := range steps {
			out = append(out, s.Group.Name())
		}
		return out
	}
	if got := names(roles.ExtractorEscalation); !slices.Equal(got, []string{"pages", "wide"}) {
		t.Errorf("extractor escalation = %v", got)
	}
	if roles.ExtractorEscalation[0].Group != roles.Judge || roles.ExtractorEscalation[1].Group != roles.Extractor ||
		roles.SecondJudge != roles.JudgeEscalation[0].Group || roles.FeedbackJudge != roles.Extractor {
		t.Error("groups of one name are not shared")
	}
	if len(roles.Groups()) != 3 {
		t.Errorf("Groups() = %d groups, want 3 distinct", len(roles.Groups()))
	}
	for attempt, want := range map[int]string{1: "wide", 2: "pages", 3: "wide", 4: "wide"} {
		if got := at(roles.Extractor, roles.ExtractorEscalation, attempt).Name(); got != want {
			t.Errorf("extractor attempt %d on %s, want %s", attempt, got, want)
		}
	}
	if len(roles.Classifiers) != 0 {
		t.Errorf("classifiers = %v, want none", roles.Classifiers)
	}

	failing := func(*config.Endpoint) (provider.Provider, error) { return nil, errors.New("no provider") }
	if _, err := NewRoles(cfg, failing); err == nil {
		t.Error("NewRoles with a failing build succeeded")
	}
}

func TestRoleGroups(t *testing.T) {
	cfg := loadConfig(t, readersConfig+`
roles:
  extractor: {group: wide, escalation: [{from_attempt: 2, group: pages}, {from_attempt: 3, group: wide}]}
  judge: {group: pages, escalation: [{from_attempt: 3, group: text}], feedback_fallback: llm, feedback_group: wide}
  second_judge: {group: text}
`)
	ext, jdg := RoleGroups(cfg)
	if !slices.Equal(ext, []string{"wide", "pages"}) || !slices.Equal(jdg, []string{"pages", "text", "wide"}) {
		t.Errorf("RoleGroups = %v, %v", ext, jdg)
	}
}

func TestRoleCapabilities(t *testing.T) {
	pdf := &document.Input{MediaType: "application/pdf", Data: []byte("%PDF-1.7 not really a pdf")}
	png := document.Page{MediaType: "image/png", Data: []byte("\x89PNG\r\n\x1a\n not really a png")}
	tests := []struct {
		name      string
		roles     string
		pdfVia    string // the extractor's via for application/pdf
		pdfKind   document.Kind
		pdfFails  bool // a PDF without pages is unsupported
		pagesKind document.Kind
	}{
		{
			name:   "one group per role: as Capabilities",
			roles:  "roles: {extractor: {group: wide}, judge: {group: wide}}",
			pdfVia: config.ViaNative, pdfKind: document.Native, pagesKind: document.Native,
		},
		{
			name:   "escalation to a group reading PDFs only via pages",
			roles:  "roles: {extractor: {group: wide, escalation: [{from_attempt: 3, group: pages}]}, judge: {group: wide}}",
			pdfVia: config.ViaPages, pdfFails: true, pagesKind: document.Pages,
		},
		{
			name:   "a second judge reading only text",
			roles:  "roles: {extractor: {group: wide}, judge: {group: wide}, second_judge: {group: text}}",
			pdfVia: config.ViaNative, pdfFails: true, pagesKind: -1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := loadConfig(t, readersConfig+tt.roles)
			ext, jdg := RoleCapabilities(cfg)
			i := slices.IndexFunc(ext, func(c config.Capability) bool { return c.MediaType == "application/pdf" })
			if i < 0 || ext[i].Via != tt.pdfVia {
				t.Fatalf("extractor PDF capability = %+v, want via %q", ext, tt.pdfVia)
			}
			for _, pages := range []bool{false, true} {
				in := *pdf
				if pages {
					in.Pages = []document.Page{png}
				}
				doc, err := document.New(in, cfg.Limits)
				if err != nil {
					t.Fatal(err)
				}
				rep, err := document.Select(doc, ext, jdg, document.RawHTML)
				want, fails := tt.pdfKind, tt.pdfFails
				if pages {
					want, fails = tt.pagesKind, tt.pagesKind < 0
				}
				if fails {
					if err == nil {
						t.Errorf("pages %t: selected %s, want unsupported_document", pages, rep.Kind)
					}
					continue
				}
				if err != nil || rep.Kind != want {
					t.Errorf("pages %t: Select = %v, %v; want %s", pages, rep.Kind, err, want)
				}
			}
		})
	}
}

func TestCommonCapabilities_pairs(t *testing.T) {
	cfg := loadConfig(t, readersConfig+"roles: {extractor: {group: wide}, judge: {group: wide}}")
	caps := commonCapabilities(cfg, []string{"wide", "pages"})
	for _, c := range caps {
		switch c.MediaType {
		case "image/png":
			if c.Via != config.ViaNative || !slices.Equal(c.Pairs, []string{"wide/wide-model+pages/pages-model"}) {
				t.Errorf("png = %+v", c)
			}
		case "image/jpeg":
			if c.Via != "" || c.Pairs != nil {
				t.Errorf("jpeg = %+v", c)
			}
		}
	}
	if commonCapabilities(cfg, []string{"wide", "missing"}) != nil || commonCapabilities(cfg, nil) != nil {
		t.Error("an undefined group or no group has capabilities")
	}
}
