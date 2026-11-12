// Package junit holds the one JUnit XML model the repository writes: a testsuite of testcase
// elements, each with a time in seconds and, when it did not pass, one of failure, error or
// skipped. The sample adapter's import profile and the catalogue actions' runners both use it, so
// there is no second model.
package junit

import (
	"encoding/xml"
	"os"
)

// Suite is a testsuite element with its testcase children, in order.
type Suite struct {
	XMLName  xml.Name `xml:"testsuite"`
	Name     string   `xml:"name,attr"`
	Tests    int      `xml:"tests,attr"`
	Failures int      `xml:"failures,attr"`
	Errors   int      `xml:"errors,attr"`
	Skipped  int      `xml:"skipped,attr"`
	Cases    []Case   `xml:"testcase"`
}

// Case is one testcase element. At most one of Failure, Error and Skipped is set.
type Case struct {
	Name    string   `xml:"name,attr"`
	Time    string   `xml:"time,attr"`
	Failure *Problem `xml:"failure"`
	Error   *Problem `xml:"error"`
	Skipped *Problem `xml:"skipped"`
}

// Problem is a failure, error or skipped element: a message attribute and the text beneath it.
type Problem struct {
	Message string `xml:"message,attr,omitempty"`
	Text    string `xml:",chardata"`
}

// Marshal returns the XML document for the suite: the XML header, then the indented suite.
func Marshal(s Suite) ([]byte, error) {
	doc, err := xml.MarshalIndent(s, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), append(doc, '\n')...), nil
}

// Write marshals the suite and writes it to path, readable by its owner only.
func Write(path string, s Suite) error {
	doc, err := Marshal(s)
	if err != nil {
		return err
	}
	return os.WriteFile(path, doc, 0o600)
}
