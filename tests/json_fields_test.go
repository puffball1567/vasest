package vasest

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestJSONFieldRerunRejectsOverridesBeforeHooks(t *testing.T) {
	for _, selection := range []string{
		`{"all":false,"all":true,"suites":[]}`,
		`{"all":false,"All":true,"suites":[]}`,
		`{"all":false,"\u0061ll":true,"suites":[]}`,
		`{"all":false,"suites":[],"suites":[{"name":"suite","cases":["case"]}]}`,
		`{"all":false,"suites":[{"name":"other","NAME":"suite","cases":["case"]}]}`,
		`{"all":false,"suites":[{"name":"suite","wholeSuite":false,"WholeSuite":true,"cases":[]}]}`,
		`{"all":false,"suites":[{"name":"suite","cases":["other"],"ca\u0073es":["case"]}]}`,
		`{"all":false,"suites":[{"name":"suite","caſes":["other"],"cases":["case"]}]}`,
	} {
		t.Run(selection, func(t *testing.T) {
			if !json.Valid([]byte(selection)) {
				t.Fatal("must isolate duplicate fields from JSON syntax")
			}
			suite := NewSuite("suite")
			calls := 0
			suite.BeforeAll(func() { calls++ })
			suite.Test("case", func() { calls++ })
			t.Setenv("VASEST_RERUN_SELECTION", selection)
			assertThrownMessage(t, "duplicate JSON field", func() { suite.RunTo(&bytes.Buffer{}) })
			if calls != 0 {
				t.Fatal("ambiguous selection reached hooks/body", calls)
			}
		})
	}
}

func TestJSONFieldRerunPreservesSeparateObjectsAndLiteralValues(t *testing.T) {
	for _, selection := range []string{
		`{"All":false,"Suites":[{"Name":"other","Cases":["case"]},{"Name":"suite","Cases":["case"]}]}`,
		`{"all":false,"suites":[{"name":"other","cases":[]},{"name":"suite","cases":["case"]}]}`,
		`{"all":false,"suites":[{"name":"suite","cases":["case"]}],"extension":{"name":"suite","cases":[{"name":"first"},{"name":"second"}]}}`,
	} {
		suite := NewSuite("suite")
		calls := 0
		suite.Test("case", func() { calls++ })
		suite.Test("other", func() { t.Fatal("selected unrelated test") })
		t.Setenv("VASEST_RERUN_SELECTION", selection)
		result := suite.RunTo(&bytes.Buffer{})
		if !result.Success() || result.Passed != 1 || calls != 1 {
			t.Fatal("scopes or native field aliases changed selection", selection, result, calls)
		}
	}
	suite := NewSuite(`{"all":false,"all":true}`)
	suite.Test(`{"name":"first","name":"second"}`, func() {})
	selection, err := json.Marshal(RerunSelection{Suites: []RerunSuite{{Name: `{"all":false,"all":true}`, Cases: []string{`{"name":"first","name":"second"}`}}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("VASEST_RERUN_SELECTION", string(selection))
	if result := suite.RunTo(&bytes.Buffer{}); !result.Success() || result.Passed != 1 {
		t.Fatal("literal JSON text was treated as fields", result)
	}
}
