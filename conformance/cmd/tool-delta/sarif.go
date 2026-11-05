package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

// finding is one SARIF result reduced to the fields the delta may print.
// The message is hashed into the identity when needed and then dropped, so a
// later stage cannot write it out.
type finding struct {
	ruleID   string
	identity string
	uri      string
	line     int
	prints   []string
}

type sarifLog struct {
	Runs []sarifRun `json:"runs"`
}

type sarifRun struct {
	Results []sarifResult `json:"results"`
}

type sarifResult struct {
	RuleID    string          `json:"ruleId"`
	Message   sarifMessage    `json:"message"`
	Partial   orderedStrings  `json:"partialFingerprints"`
	Full      orderedStrings  `json:"fingerprints"`
	Locations []sarifLocation `json:"locations"`
}

type sarifMessage struct {
	Text string `json:"text"`
}

type sarifLocation struct {
	Physical struct {
		Artifact struct {
			URI string `json:"uri"`
		} `json:"artifactLocation"`
		Region struct {
			StartLine int `json:"startLine"`
		} `json:"region"`
	} `json:"physicalLocation"`
}

// orderedStrings keeps JSON object values in document order. encoding/json's
// map iteration is not that order, and "first fingerprint" would otherwise
// change from run to run.
type orderedStrings []string

func (o *orderedStrings) UnmarshalJSON(b []byte) error {
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return errors.New("fingerprint property must be a JSON object")
	}
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return err
		}
		name, ok := key.(string)
		if !ok {
			return errors.New("fingerprint key must be a string")
		}
		var value string
		if err := dec.Decode(&value); err != nil {
			return fmt.Errorf("fingerprint %q: %w", name, err)
		}
		*o = append(*o, value)
	}
	tok, err = dec.Token()
	if err != nil {
		return err
	}
	if d, ok := tok.(json.Delim); !ok || d != '}' {
		return errors.New("fingerprint property must be a JSON object")
	}
	return nil
}

func loadFindings(path string) ([]finding, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	var doc sarifLog
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: data follows the first JSON value", path)
	}
	var out []finding
	for ri, run := range doc.Runs {
		for i, res := range run.Results {
			f, err := res.toFinding()
			if err != nil {
				return nil, fmt.Errorf("%s: run %d result %d: %w", path, ri, i, err)
			}
			out = append(out, f)
		}
	}
	return out, nil
}

func (r sarifResult) toFinding() (finding, error) {
	if r.RuleID == "" {
		return finding{}, errors.New("missing ruleId")
	}
	if len(r.Locations) == 0 || r.Locations[0].Physical.Artifact.URI == "" {
		return finding{}, errors.New("missing artifactLocation.uri")
	}
	uri := r.Locations[0].Physical.Artifact.URI
	line := r.Locations[0].Physical.Region.StartLine
	derived, err := derivedFingerprint(r.RuleID, uri, r.Message.Text)
	if err != nil {
		return finding{}, err
	}
	prints := append(append([]string{}, r.Partial...), r.Full...)
	identity := derived
	if len(r.Partial) > 0 {
		identity = r.Partial[0]
	} else if len(r.Full) > 0 {
		identity = r.Full[0]
	} else {
		prints = []string{derived}
	}
	return finding{ruleID: r.RuleID, identity: identity, uri: uri, line: line, prints: prints}, nil
}
