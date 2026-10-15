package vasest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestSuiteNamesRejectInvalidUTF8BeforeSetup(t *testing.T) {
	for _, name := range []string{string([]byte{0xff}), "name\xc0\xaf", "name\xed\xa0\x80", "name\xf0\x9f"} {
		assertThrownMessage(t, "suite name must be valid UTF-8", func() { NewSuite(name) })
		assertThrownMessage(t, "suite name must be valid UTF-8", func() {
			NewFixtureSuite(name, func(*TestContext) int { t.Fatal("invalid suite ran setup"); return 0 })
		})
	}
}

func TestCaseNamesRejectInvalidUTF8WithoutChangingEarlierRegistrations(t *testing.T) {
	for _, mode := range []string{"test", "context", "fixture", "skip", "todo", "shared-test", "shared-skip", "shared-todo"} {
		t.Run(mode, func(t *testing.T) {
			plain := NewSuite("valid names")
			bodies := 0
			plain.Test("earlier", func() { bodies++ })
			shared := NewFixtureSuite("valid shared", func(*TestContext) int { return 42 })
			shared.Test("earlier", func(int, *TestContext) { bodies++ })
			bad := "case" + string([]byte{0xff})
			assertThrownMessage(t, "test name must be valid UTF-8", func() {
				switch mode {
				case "test":
					plain.Test(bad, func() { t.Fatal("invalid body ran") })
				case "context":
					plain.TestWithContext(bad, func(*TestContext) { t.Fatal("invalid body ran") })
				case "fixture":
					SuiteTestWithFixture(plain, bad, func(*TestContext) int { t.Fatal("invalid setup ran"); return 0 }, func(int, *TestContext) { t.Fatal("invalid body ran") })
				case "skip":
					plain.Skip(bad, func() { t.Fatal("invalid skipped body ran") })
				case "todo":
					plain.Todo(bad)
				case "shared-test":
					shared.Test(bad, func(int, *TestContext) { t.Fatal("invalid body ran") })
				case "shared-skip":
					shared.Skip(bad, func(int, *TestContext) { t.Fatal("invalid skipped body ran") })
				case "shared-todo":
					shared.Todo(bad)
				}
			})
			if len(plain.cases) != 1 || len(shared.suite.cases) != 1 {
				t.Fatal("failed registration changed earlier cases")
			}
			plainResult := plain.RunTo(&bytes.Buffer{})
			sharedResult := shared.RunTo(&bytes.Buffer{})
			if !plainResult.Success() || !sharedResult.Success() || bodies != 2 {
				t.Fatal(plainResult, sharedResult, bodies)
			}
		})
	}
}

func TestIndexedTablesRejectInvalidUTF8Atomically(t *testing.T) {
	for mode, register := range indexedRegistrars() {
		t.Run(mode, func(t *testing.T) {
			suite := NewSuite("atomic names")
			suite.Test("earlier", func() {})
			named := 0
			values := []indexedID{1<<60 + 1, 1<<60 + 2, 1<<60 + 3}
			assertThrownMessage(t, "test name must be valid UTF-8", func() {
				register(suite, values, func(value indexedID, index int) string {
					named++
					if value != values[index] {
						t.Fatal("typed row changed")
					}
					if index == 1 {
						return "bad\xff"
					}
					return fmt.Sprintf("row %d", index)
				}, func(indexedID, int, *TestContext) { t.Fatal("rejected row ran") })
			})
			if named != 2 || len(suite.cases) != 1 || suite.cases[0].name != "earlier" {
				t.Fatalf("partial rows were registered: %d %#v", named, suite.cases)
			}
			result := suite.RunTo(&bytes.Buffer{})
			if !result.Success() || result.Passed != 1 {
				t.Fatal(result)
			}
		})
	}
	for _, mode := range inactiveModes {
		t.Run(mode, func(t *testing.T) {
			entry := newInactiveTable(mode, func() { t.Fatal("inactive body/setup/hook ran") })
			entry.suite.Test("earlier", func() {})
			named := 0
			assertThrownMessage(t, "test name must be valid UTF-8", func() {
				entry.register([]inactiveID{1<<60 + 1, 1<<60 + 2}, func(inactiveID, int) string {
					named++
					if named == 2 {
						return "bad\xff"
					}
					return "first"
				}, func() { t.Fatal("inactive body ran") })
			})
			if named != 2 || len(entry.suite.cases) != 1 {
				t.Fatalf("partial inactive rows were registered: %d %#v", named, entry.suite.cases)
			}
		})
	}
}

func TestValidUnicodeNamesRoundTripAndInvalidUTF8ValuesRemainNative(t *testing.T) {
	name := " 金木犀 <&>\n😀 � "
	suite := NewSuite(name)
	bad := string([]byte{0xff, 0, 0xfe})
	names := []string{"é", "e\u0301", " � ", "名前\n<&> 😀"}
	for _, name := range names {
		name := name
		suite.Test(name, func() {
			expect[string](bad).ToEqual(bad)
			expectString(bad).ToStartWith(string([]byte{0xff}))
		})
	}
	result := suite.RunTo(&bytes.Buffer{})
	if !result.Success() || result.Passed != len(names) || result.Suite != name {
		t.Fatal(result)
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var restored Summary
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	for index, result := range restored.Results {
		if result.Name != names[index] {
			t.Fatalf("unicode name was normalized: %#v", restored)
		}
	}
	if !strings.Contains(string(data), "金木犀") {
		t.Fatal("unicode names lost", string(data))
	}
}

func TestInvalidUTF8RerunSelectionCannotAliasReplacementNames(t *testing.T) {
	suite := NewSuite("suite�")
	bodies, hooks := 0, 0
	suite.BeforeAll(func() { hooks++ })
	suite.Test("case�", func() { bodies++ })
	t.Setenv("VASEST_RERUN_SELECTION", `{"all":false,"suites":[{"name":"suite`+"\xff"+`","cases":["case�"]}]}`)
	assertThrownMessage(t, "rerun selection must be valid UTF-8", func() { suite.RunTo(&bytes.Buffer{}) })
	if bodies != 0 || hooks != 0 {
		t.Fatal("invalid selection reached replacement-name hooks/body", bodies, hooks)
	}
	t.Setenv("VASEST_RERUN_SELECTION", `{"all":false,"suites":[{"name":"suite�","cases":["case�"]}]}`)
	result := suite.RunTo(&bytes.Buffer{})
	if !result.Success() || result.Passed != 1 || bodies != 1 || hooks != 1 {
		t.Fatal(result, bodies, hooks)
	}
}

func TestJSONUnicodeRerunSelectionCannotAliasReplacementNames(t *testing.T) {
	suite := NewSuite("suite�")
	bodies, hooks := 0, 0
	suite.BeforeAll(func() { hooks++ })
	suite.Test("case�", func() { bodies++ })
	for _, escaped := range []string{`\ud800`, `\udbff`, `\udc00`, `\udfff`, `\uD800\u0041`, `\ud800\ud800`, `\ud800\\udc00`, `\udc00\ud800`} {
		for _, field := range []string{"suite", "case"} {
			selection := `{"all":false,"suites":[{"name":"suite�","cases":["case�"]}]}`
			selection = strings.Replace(selection, field+"�", field+escaped, 1)
			if !json.Valid([]byte(selection)) {
				t.Fatal("probe must have valid JSON syntax", selection)
			}
			t.Setenv("VASEST_RERUN_SELECTION", selection)
			assertThrownMessage(t, "rerun selection must use paired Unicode surrogate escapes", func() { suite.RunTo(&bytes.Buffer{}) })
			if bodies != 0 || hooks != 0 {
				t.Fatal("invalid escape reached replacement-name hooks/body", selection, bodies, hooks)
			}
		}
	}
}

func TestJSONUnicodeRerunSelectionPreservesValidEscapes(t *testing.T) {
	for _, probe := range []struct{ encoded, decoded string }{
		{`\ufffd`, "�"}, {`�`, "�"}, {`\uD83D\uDE00`, "😀"},
		{`\ud800\udc00`, "𐀀"}, {`\udbff\udfff`, "\U0010ffff"},
		{`\\ud800`, `\ud800`}, {`\\\ud83d\ude00`, "\\😀"},
		{`\"\\ud800\/`, `"\ud800/`}, {`\u0061\ud83d\ude00\ufffd`, "a😀�"},
	} {
		suite := NewSuite("suite" + probe.decoded)
		body := 0
		suite.Test("case"+probe.decoded, func() { body++ })
		suite.Test("unselected", func() { t.Fatal("selected unrelated case") })
		t.Setenv("VASEST_RERUN_SELECTION", `{"all":false,"suites":[{"name":"suite`+probe.encoded+`","cases":["case`+probe.encoded+`"]}]}`)
		result := suite.RunTo(&bytes.Buffer{})
		if !result.Success() || result.Passed != 1 || body != 1 || result.Results[0].Name != "case"+probe.decoded {
			t.Fatal("valid JSON escape changed selection", probe, result, body)
		}
	}
}
