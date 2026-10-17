package config

import (
	"fmt"
	"math"
	"strings"
)

// JudgePolicy says when the judge is asked (ADR 0009): roles.judge.policy, per
// application applications[].judge.policy, and per request when the application allows
// override.
type JudgePolicy string

// Judge policies. JudgeAlways, the default, keeps contract v1 exactly: accepted means the
// judge accepted.
const (
	JudgeAlways JudgePolicy = "always"
	JudgeNever  JudgePolicy = "never"
	JudgeAuto   JudgePolicy = "auto"
)

// JudgePolicies lists every policy, the default first.
var JudgePolicies = []JudgePolicy{JudgeAlways, JudgeNever, JudgeAuto}

// DefaultSampleRate is applications[].judge.sample_rate when unset: the share of jobs
// that auto sends to the judge although grounding alone would accept them.
const DefaultSampleRate = 0.05

// ParseJudgePolicy returns the policy named s; "" is an error, not the default.
func ParseJudgePolicy(s string) (JudgePolicy, error) {
	for _, p := range JudgePolicies {
		if string(p) == s {
			return p, nil
		}
	}
	return "", fmt.Errorf("judge policy %q is not one of %s", s, judgePolicyList())
}

// String returns the policy's name, "always" for the zero value.
func (p JudgePolicy) String() string {
	if p == "" {
		return string(JudgeAlways)
	}
	return string(p)
}

func judgePolicyList() string {
	names := make([]string, len(JudgePolicies))
	for i, p := range JudgePolicies {
		names[i] = string(p)
	}
	return strings.Join(names, ", ")
}

// AppJudge is an application's judge settings (applications[].judge).
type AppJudge struct {
	// Policy overrides roles.judge.policy for the application; empty means the role's.
	Policy JudgePolicy `yaml:"policy,omitempty"`
	// AllowOverride lets a request choose the policy with the judge part. Off by default:
	// a caller cannot weaken the operator's choice unless the operator allows it.
	AllowOverride bool `yaml:"allow_override,omitempty"`
	// SampleRate, from 0 to 1, is the share of jobs auto sends to the judge although
	// grounding alone would accept them; nil means DefaultSampleRate.
	SampleRate *float64 `yaml:"sample_rate,omitempty"`
}

// Rate returns the sample rate, DefaultSampleRate when unset.
func (j AppJudge) Rate() float64 {
	if j.SampleRate == nil {
		return DefaultSampleRate
	}
	return *j.SampleRate
}

// RolePolicy is roles.judge.policy, JudgeAlways when unset.
func (c *Config) RolePolicy() JudgePolicy {
	if c.Roles.Judge.Policy == "" {
		return JudgeAlways
	}
	return c.Roles.Judge.Policy
}

// JudgePolicyOf is the application's effective policy: its own, else the role's.
func (c *Config) JudgePolicyOf(a Application) JudgePolicy {
	if a.Judge.Policy != "" {
		return a.Judge.Policy
	}
	return c.RolePolicy()
}

// judgeRole checks roles.judge.policy, and that the extractor sets none.
func (v *validator) judgeRole(c *Config) {
	if c.Roles.Extractor.Policy != "" {
		v.errorf("roles.extractor.policy", "applies only to roles.judge")
	}
	p := c.Roles.Judge.Policy
	if p == "" {
		return
	}
	if _, err := ParseJudgePolicy(string(p)); err != nil {
		v.errorf("roles.judge.policy", "%q is not one of %s", p, judgePolicyList())
		return
	}
	if p == JudgeNever && c.Roles.SecondJudge.Group != "" {
		v.warnf("roles.second_judge", "is never asked: roles.judge.policy is never")
	}
}

// appJudge checks an application's judge settings; key is the application's key.
func (v *validator) appJudge(key string, j AppJudge) {
	if j.Policy != "" {
		if _, err := ParseJudgePolicy(string(j.Policy)); err != nil {
			v.errorf(key+".judge.policy", "%q is not one of %s", j.Policy, judgePolicyList())
		}
	}
	if r := j.SampleRate; r != nil && (math.IsNaN(*r) || *r < 0 || *r > 1) {
		v.errorf(key+".judge.sample_rate", "%g must be from 0 to 1", *r)
	}
}
