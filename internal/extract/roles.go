package extract

import (
	"fmt"
	"slices"
	"strings"

	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/provider"
	"github.com/kaanemec/vde/internal/router"
)

// Roles are the endpoint groups serving the roles and the loop limits taken from the
// configuration. Build them with NewRoles.
type Roles struct {
	Extractor *router.Group
	Judge     *router.Group
	// ExtractorEscalation and JudgeEscalation serve the role from a later attempt on
	// (roles.<role>.escalation), in increasing FromAttempt order. Empty means none.
	ExtractorEscalation []Escalation
	JudgeEscalation     []Escalation
	// SecondJudge, when set, must also accept a candidate the judge accepted
	// (roles.second_judge). Its reject feeds the next attempt like the judge's.
	SecondJudge *router.Group
	// FeedbackJudge, when set, is asked with the judge framing for pointer-level feedback
	// each time an endpoint in Classifiers rejects (roles.judge.feedback_fallback: llm).
	FeedbackJudge *router.Group
	// Classifiers names the endpoints of provider classifier.
	Classifiers map[string]bool
	// JudgeReparseTries is the number of extra judge calls after a verdict that cannot
	// be parsed (limits.judge_reparse_tries; the contract asks for 1).
	JudgeReparseTries int
	// FeedbackMessageChars caps each feedback message (limits.feedback_message_chars);
	// 0 or less means DefaultFeedbackMessageChars.
	FeedbackMessageChars int
}

// Escalation serves a role from Group from attempt FromAttempt on.
type Escalation struct {
	FromAttempt int
	Group       *router.Group
}

// NewRoles builds the groups of roles.extractor, roles.judge, their escalation steps,
// roles.second_judge and roles.judge.feedback_group from cfg, which must have passed
// Validate. Every group name gets one *router.Group, shared, with its endpoint health,
// by all the places that name it. build returns the provider of an endpoint.
func NewRoles(cfg *config.Config, build func(*config.Endpoint) (provider.Provider, error)) (Roles, error) {
	built := map[string]*router.Group{}
	group := func(name string) (*router.Group, error) {
		if g, ok := built[name]; ok {
			return g, nil
		}
		g, err := router.NewGroup(name, cfg.Groups[name], cfg, build)
		if err != nil {
			return nil, fmt.Errorf("extract: %w", err)
		}
		built[name] = g
		return g, nil
	}
	steps := func(in []config.Escalation) ([]Escalation, error) {
		var out []Escalation
		for _, e := range in {
			g, err := group(e.Group)
			if err != nil {
				return nil, err
			}
			out = append(out, Escalation{FromAttempt: e.FromAttempt, Group: g})
		}
		return out, nil
	}

	r := Roles{JudgeReparseTries: cfg.Limits.JudgeReparseTries, FeedbackMessageChars: cfg.Limits.FeedbackMessageChars}
	var err error
	if r.Extractor, err = group(cfg.Roles.Extractor.Group); err != nil {
		return Roles{}, err
	}
	if r.Judge, err = group(cfg.Roles.Judge.Group); err != nil {
		return Roles{}, err
	}
	if r.ExtractorEscalation, err = steps(cfg.Roles.Extractor.Escalation); err != nil {
		return Roles{}, err
	}
	if r.JudgeEscalation, err = steps(cfg.Roles.Judge.Escalation); err != nil {
		return Roles{}, err
	}
	if name := cfg.Roles.SecondJudge.Group; name != "" {
		if r.SecondJudge, err = group(name); err != nil {
			return Roles{}, err
		}
	}
	if cfg.Roles.Judge.FeedbackFallback == config.FeedbackLLM {
		if r.FeedbackJudge, err = group(cfg.Roles.Judge.FeedbackGroup); err != nil {
			return Roles{}, err
		}
		for name, e := range cfg.Endpoints {
			if e != nil && e.Provider == config.ProviderClassifier {
				if r.Classifiers == nil {
					r.Classifiers = map[string]bool{}
				}
				r.Classifiers[name] = true
			}
		}
	}
	return r, nil
}

// Groups returns every distinct group the roles use, primary groups first, for health
// checks and metrics hooks.
func (r Roles) Groups() []*router.Group {
	var out []*router.Group
	add := func(g *router.Group) {
		if g != nil && !slices.Contains(out, g) {
			out = append(out, g)
		}
	}
	add(r.Extractor)
	add(r.Judge)
	for _, e := range r.ExtractorEscalation {
		add(e.Group)
	}
	for _, e := range r.JudgeEscalation {
		add(e.Group)
	}
	add(r.SecondJudge)
	add(r.FeedbackJudge)
	return out
}

// at returns the group serving attempt n: the last escalation step whose FromAttempt is
// at most n, else primary.
func at(primary *router.Group, steps []Escalation, n int) *router.Group {
	g := primary
	for _, s := range steps {
		if n >= s.FromAttempt {
			g = s.Group
		}
	}
	return g
}

// RoleGroups returns the names of the groups that may serve each role, in order and
// without repeats: the extractor's group and its escalation groups; the judge's group,
// its escalation groups, the second judge's group and the feedback group (with
// feedback_fallback: llm). The representation must be readable by all of a role's groups.
func RoleGroups(cfg *config.Config) (extractor, judge []string) {
	add := func(list []string, name string) []string {
		if name == "" || slices.Contains(list, name) {
			return list
		}
		return append(list, name)
	}
	extractor = add(extractor, cfg.Roles.Extractor.Group)
	for _, e := range cfg.Roles.Extractor.Escalation {
		extractor = add(extractor, e.Group)
	}
	judge = add(judge, cfg.Roles.Judge.Group)
	for _, e := range cfg.Roles.Judge.Escalation {
		judge = add(judge, e.Group)
	}
	judge = add(judge, cfg.Roles.SecondJudge.Group)
	if cfg.Roles.Judge.FeedbackFallback == config.FeedbackLLM {
		judge = add(judge, cfg.Roles.Judge.FeedbackGroup)
	}
	return extractor, judge
}

// RoleCapabilities returns, per role, how the role reads each media type across all its
// groups (RoleGroups), for document.Select: a media type is readable only when every
// group of the role reads it. With one group per role it is cfg.Capabilities of that
// group.
func RoleCapabilities(cfg *config.Config) (extractor, judge []config.Capability) {
	e, j := RoleGroups(cfg)
	return commonCapabilities(cfg, e), commonCapabilities(cfg, j)
}

// commonCapabilities intersects the capabilities of groups. A media type keeps its Via
// when every group reads it the same way; a PDF read natively by some groups and through
// page images by others reads through page images. Pairs are then combinations, one
// pair per group joined by "+", so that document.Select's rule (one pair reads every
// media type of a representation) holds for each group at once.
func commonCapabilities(cfg *config.Config, groups []string) []config.Capability {
	if len(groups) == 0 {
		return nil
	}
	per := make([][]config.Capability, len(groups))
	for i, g := range groups {
		if per[i] = cfg.Capabilities(g); per[i] == nil {
			return nil
		}
	}
	if len(per) == 1 {
		return per[0]
	}
	out := make([]config.Capability, 0, len(per[0]))
	for _, first := range per[0] {
		c := config.Capability{MediaType: first.MediaType, Required: first.Required, Via: first.Via}
		combos := []string{""}
		for _, caps := range per {
			i := slices.IndexFunc(caps, func(x config.Capability) bool { return x.MediaType == first.MediaType })
			if i < 0 || caps[i].Via == "" {
				c.Via, combos = "", nil
				break
			}
			if caps[i].Via != c.Via {
				c.Via = config.ViaPages // only a PDF is read two ways: natively or via pages
			}
			combos = product(combos, caps[i].Pairs)
		}
		if len(combos) > 0 {
			c.Pairs = combos
		}
		out = append(out, c)
	}
	return out
}

func product(prefixes, pairs []string) []string {
	out := make([]string, 0, len(prefixes)*len(pairs))
	for _, p := range prefixes {
		for _, q := range pairs {
			if p == "" {
				out = append(out, q)
			} else {
				out = append(out, strings.Join([]string{p, q}, "+"))
			}
		}
	}
	return out
}
