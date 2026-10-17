package config

import (
	"strings"
	"testing"
)

func TestParseJudgePolicy(t *testing.T) {
	t.Parallel()
	for _, p := range JudgePolicies {
		got, err := ParseJudgePolicy(p.String())
		if err != nil || got != p {
			t.Errorf("ParseJudgePolicy(%q) = %q, %v", p, got, err)
		}
	}
	for _, bad := range []string{"", "Always", " never", "sometimes"} {
		if _, err := ParseJudgePolicy(bad); err == nil {
			t.Errorf("ParseJudgePolicy(%q) succeeded", bad)
		}
	}
	if JudgePolicy("").String() != "always" {
		t.Error("the zero policy does not print as always")
	}
}

func TestJudgePolicy_parseAndDefaults(t *testing.T) {
	t.Parallel()
	yaml := strings.Replace(validYAML, "judge: {group: review}", "judge: {group: review, policy: never}", 1)
	yaml = strings.Replace(yaml, "  - {id: travelinbox}", `  - {id: travelinbox}
  - {id: lab, judge: {policy: auto, allow_override: true, sample_rate: 0.25}}`, 1)
	cfg, err := Parse([]byte(yaml), env(map[string]string{"ANTHROPIC_API_KEY": testSecret}))
	if err != nil {
		t.Fatal(err)
	}
	if issues := cfg.Validate(); len(issues) != 0 {
		t.Fatalf("Validate() = %v", issues)
	}
	inbox, lab := cfg.Applications[0], cfg.Applications[1]
	if got := cfg.JudgePolicyOf(inbox); got != JudgeNever {
		t.Errorf("travelinbox policy = %q, want the role's never", got)
	}
	if inbox.Judge.AllowOverride || inbox.Judge.Rate() != DefaultSampleRate {
		t.Errorf("travelinbox judge = %+v, want no override and the default sample rate", inbox.Judge)
	}
	if cfg.JudgePolicyOf(lab) != JudgeAuto || !lab.Judge.AllowOverride || lab.Judge.Rate() != 0.25 {
		t.Errorf("lab judge = %+v", lab.Judge)
	}

	def := validConfig(t)
	if def.Roles.Judge.Policy != JudgeAlways || def.JudgePolicyOf(def.Applications[0]) != JudgeAlways {
		t.Errorf("default role policy = %q, want always", def.Roles.Judge.Policy)
	}
	if (&Config{}).RolePolicy() != JudgeAlways {
		t.Error("an unparsed configuration's role policy is not always")
	}
}

func TestJudgePolicy_extractorKeyRefused(t *testing.T) {
	t.Parallel()
	yaml := strings.Replace(validYAML, "extractor: {group: extraction}", "extractor: {group: extraction, policy: never}", 1)
	cfg, err := Parse([]byte(yaml), env(map[string]string{"ANTHROPIC_API_KEY": testSecret}))
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate().Err(false); err == nil || !strings.Contains(err.Error(), "roles.extractor.policy") {
		t.Errorf("Validate() = %v, want roles.extractor.policy refused", err)
	}
}

func TestCheckApplication_judge(t *testing.T) {
	t.Parallel()
	cfg := validConfig(t)
	rate := 2.0
	a := Application{ID: "dyn", Judge: AppJudge{Policy: "maybe", SampleRate: &rate}}
	issues := cfg.CheckApplication(&a)
	keys := map[string]bool{}
	for _, i := range issues {
		keys[i.Key] = true
	}
	if !keys["judge.policy"] || !keys["judge.sample_rate"] {
		t.Errorf("CheckApplication = %v, want judge.policy and judge.sample_rate", issues)
	}
	ok := Application{ID: "dyn", Judge: AppJudge{Policy: JudgeNever, AllowOverride: true}}
	if issues := cfg.CheckApplication(&ok); issues.Err(false) != nil {
		t.Errorf("CheckApplication = %v", issues)
	}
}
