package main

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// waiver is one entry of a .saphan-waivers.yaml file, after the four required
// fields have been checked.
type waiver struct {
	index       int
	rule        string
	fingerprint string
	reason      string
	expires     time.Time
	expiresText string
}

type waiverFile struct {
	Waivers []waiverYAML `yaml:"waivers"`
}

// Pointers distinguish a missing field from a present one. A blank value is
// refused as missing too: the spec requires each field, and an empty one
// names nothing.
type waiverYAML struct {
	Rule        *string `yaml:"rule"`
	Fingerprint *string `yaml:"fingerprint"`
	Reason      *string `yaml:"reason"`
	Expires     *string `yaml:"expires"`
}

func loadWaivers(path string) ([]waiver, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc waiverFile
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	out := make([]waiver, 0, len(doc.Waivers))
	for i, entry := range doc.Waivers {
		w, err := entry.waiver(i)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, nil
}

func (e waiverYAML) waiver(index int) (waiver, error) {
	rule, ok := requiredField(e.Rule)
	if !ok {
		return waiver{}, missingField(index, "rule")
	}
	fp, ok := requiredField(e.Fingerprint)
	if !ok {
		return waiver{}, missingField(index, "fingerprint")
	}
	reason, ok := requiredField(e.Reason)
	if !ok {
		return waiver{}, missingField(index, "reason")
	}
	expText, ok := requiredField(e.Expires)
	if !ok {
		return waiver{}, missingField(index, "expires")
	}
	exp, err := time.ParseInLocation(dateLayout, expText, time.UTC)
	if err != nil {
		return waiver{}, fmt.Errorf("waiver entry %d: expires %q is not a UTC date YYYY-MM-DD", index, expText)
	}
	return waiver{
		index:       index,
		rule:        rule,
		fingerprint: fp,
		reason:      reason,
		expires:     exp,
		expiresText: expText,
	}, nil
}

func requiredField(v *string) (string, bool) {
	if v == nil || *v == "" {
		return "", false
	}
	return *v, true
}

func missingField(index int, field string) error {
	return fmt.Errorf("waiver entry %d: missing field %s", index, field)
}

// matchWaivers reports the first unexpired waiver that covers f, and every
// expired waiver that covers f. An unexpired match means the finding is
// waived. Expired matches are still returned so they are reported.
func matchWaivers(f finding, waivers []waiver, today time.Time) (waived bool, until string, expired []waiver) {
	for i := range waivers {
		w := &waivers[i]
		if w.rule != f.ruleID || !hasFingerprint(f.prints, w.fingerprint) {
			continue
		}
		if waiverInForce(w.expires, today) {
			if !waived {
				waived = true
				until = w.expiresText
			}
			continue
		}
		expired = append(expired, *w)
	}
	return waived, until, expired
}

func hasFingerprint(prints []string, want string) bool {
	for _, p := range prints {
		if p == want {
			return true
		}
	}
	return false
}

// waiverInForce reports whether a waiver dated expires still covers a finding
// on today. Both are UTC calendar dates. The waiver covers today through the
// end of expires, so the two dates being equal is still in force.
func waiverInForce(expires, today time.Time) bool {
	return !today.After(expires)
}
