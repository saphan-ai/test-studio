package junit

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteThenParseKeepsEveryCaseAndItsProblem(t *testing.T) {
	t.Parallel()
	in := Suite{Name: "s", Tests: 2, Failures: 1, Cases: []Case{
		{Name: "a", Time: "0.001"},
		{Name: "b", Time: "0.002", Failure: &Problem{Message: "m", Text: "boom"}},
	}}
	path := filepath.Join(t.TempDir(), "r.xml")
	if err := Write(path, in); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out Suite
	if err := xml.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Cases) != 2 || out.Cases[0].Failure != nil || out.Cases[1].Failure == nil || out.Cases[1].Failure.Text != "boom" {
		t.Fatalf("round trip lost a case or a problem: %+v", out)
	}
}
