package claudeagentsdk

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

const SkillsAll = "all"

func (o Options) settingsValue() (string, error) {
	if o.Settings == "" && o.Sandbox == nil {
		return "", nil
	}
	if o.Sandbox == nil {
		return o.Settings, nil
	}
	settings := map[string]any{}
	if o.Settings != "" {
		s := strings.TrimSpace(o.Settings)
		var b []byte
		if strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}") {
			b = []byte(s)
		} else if data, err := os.ReadFile(s); err == nil {
			b = data
		}
		if len(b) > 0 {
			if err := json.Unmarshal(b, &settings); err != nil {
				return "", fmt.Errorf("parse settings: %w", err)
			}
		}
	}
	settings["sandbox"] = o.Sandbox
	b, err := json.Marshal(settings)
	if err != nil {
		return "", fmt.Errorf("marshal settings: %w", err)
	}
	return string(b), nil
}

func (o Options) skillsDefaults() ([]string, []string, error) {
	allowed := append([]string(nil), o.AllowedTools...)
	var sources []string
	if o.SettingSources != nil {
		sources = append([]string(nil), o.SettingSources...)
	}
	if o.Skills == nil {
		return allowed, sources, nil
	}
	if o.SettingSources == nil {
		sources = []string{"user", "project"}
	}
	if s, ok := o.Skills.(string); ok {
		if s != SkillsAll {
			return nil, nil, fmt.Errorf("Options.Skills must be []string or %q", SkillsAll)
		}
		if !contains(allowed, "Skill") {
			allowed = append(allowed, "Skill")
		}
		return allowed, sources, nil
	}
	names, ok := o.Skills.([]string)
	if !ok {
		return nil, nil, fmt.Errorf("Options.Skills must be []string or %q", SkillsAll)
	}
	for _, name := range names {
		if err := validateSkillName(name); err != nil {
			return nil, nil, err
		}
		rule := "Skill(" + name + ")"
		if !contains(allowed, rule) {
			allowed = append(allowed, rule)
		}
	}
	return allowed, sources, nil
}

func validateSkillName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("skill names must be non-empty")
	}
	if name != strings.TrimSpace(name) || name == "*" || strings.HasPrefix(name, "/") || strings.HasSuffix(name, `\`) || strings.Contains(name, `\\`) {
		return fmt.Errorf("invalid skill name %q", name)
	}
	for _, r := range name {
		if r == '(' || r == ')' || r == ',' || r == '\ufeff' || r < 0x20 || (r >= 0x7f && r <= 0x9f) || (r >= 0xd800 && r <= 0xdfff) {
			return fmt.Errorf("invalid skill name %q", name)
		}
	}
	if strings.HasSuffix(name, ":*") || strings.HasSuffix(name, " *") {
		return fmt.Errorf("invalid skill name %q", name)
	}
	return nil
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
