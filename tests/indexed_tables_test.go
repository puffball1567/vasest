package vasest

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"
)

type indexedID int64
type indexedRegistrar func(*Suite, []indexedID, func(indexedID, int) string, func(indexedID, int, *TestContext))

func indexedRegistrars() map[string]indexedRegistrar {
	return map[string]indexedRegistrar{
		"plain": func(s *Suite, rows []indexedID, name func(indexedID, int) string, body func(indexedID, int, *TestContext)) {
			SuiteTestEachIndexed(s, rows, name, func(value indexedID, index int) { body(value, index, nil) })
		},
		"context": func(s *Suite, rows []indexedID, name func(indexedID, int) string, body func(indexedID, int, *TestContext)) {
			SuiteTestEachIndexedWithContext(s, rows, name, body)
		},
		"fixture": func(s *Suite, rows []indexedID, name func(indexedID, int) string, body func(indexedID, int, *TestContext)) {
			SuiteTestEachIndexedWithFixture(s, rows, name, func(value indexedID, index int, context *TestContext) int { return index },
				func(value indexedID, index int, fixture int, context *TestContext) {
					if fixture != index {
						panic("fixture index changed")
					}
					body(value, index, context)
				})
		},
	}
}

func TestIndexedTablesCaptureNativeRowsAndOriginalIndices(t *testing.T) {
	for mode, register := range indexedRegistrars() {
		t.Run(mode, func(t *testing.T) {
			suite := NewSuite("indexed rows")
			first := indexedID(1<<60 + 1)
			values := []indexedID{first, first, first}
			var named, bodies, cleaned []int
			register(suite, values, func(value indexedID, index int) string {
				if value != first || index != len(named) {
					t.Fatal("names lost row value or original index")
				}
				named = append(named, index)
				values[2] = 99
				return fmt.Sprintf("row %d", index)
			}, func(value indexedID, index int, context *TestContext) {
				if value != first {
					t.Fatal("registration did not capture native row values")
				}
				bodies = append(bodies, index)
				if context != nil {
					context.Cleanup(func() { cleaned = append(cleaned, index) })
				}
			})
			values[0] = 98
			if len(bodies) != 0 || fmt.Sprint(named) != "[0 1 2]" {
				t.Fatal("body ran during registration")
			}
			for run := 0; run < 2; run++ {
				bodies, cleaned = nil, nil
				summary := suite.RunTo(&bytes.Buffer{})
				if summary.Passed != 3 || summary.Failed != 0 || fmt.Sprint(bodies) != "[0 1 2]" || (mode != "plain" && fmt.Sprint(cleaned) != "[0 1 2]") {
					t.Fatal(summary, bodies, cleaned)
				}
			}
			if fmt.Sprint(named) != "[0 1 2]" {
				t.Fatal("rerun called name callback again")
			}
		})
	}
	type record struct{ Value int }
	object := &record{42}
	rows := []*record{object, object}
	suite := NewSuite("references")
	SuiteTestEachIndexed(suite, rows, func(value *record, index int) string { value.Value++; return fmt.Sprint(index) },
		func(value *record, index int) {
			if value != object || value.Value != 44 {
				t.Fatal("class reference copied")
			}
		})
	rows[0] = &record{99}
	if result := suite.RunTo(&bytes.Buffer{}); result.Passed != 2 {
		t.Fatal(result)
	}
	collections := NewSuite("collections")
	values := [][]int{nil, {}, {42}}
	SuiteTestEachIndexed(collections, values, func(value []int, index int) string { return fmt.Sprint(index) }, func(value []int, index int) {
		if (value == nil) != (index == 0) || (index == 2 && value[0] != 43) {
			t.Fatal("native nil or backing storage changed")
		}
	})
	values[2][0] = 43
	if result := collections.RunTo(&bytes.Buffer{}); result.Passed != 3 {
		t.Fatal(result)
	}
}

func TestIndexedTablesSelectionShuffleAndBailKeepIndices(t *testing.T) {
	for mode, register := range indexedRegistrars() {
		for _, selection := range []string{"filter", "rerun", "shuffle", "bail"} {
			t.Run(mode+"/"+selection, func(t *testing.T) {
				switch selection {
				case "filter":
					t.Setenv("VASEST_FILTER", "row 1")
				case "rerun":
					t.Setenv("VASEST_RERUN_SELECTION", `{"all":false,"suites":[{"name":"selection","wholeSuite":false,"cases":["row 2"]}]}`)
				case "shuffle":
					t.Setenv("VASEST_SHUFFLE_SEED", "42")
				case "bail":
					t.Setenv("VASEST_BAIL", "1")
				}
				suite := NewSuite("selection")
				var visited, cleaned []int
				register(suite, []indexedID{42, 42, 42}, func(value indexedID, index int) string { return fmt.Sprintf("row %d", index) }, func(value indexedID, index int, context *TestContext) {
					if value != 42 {
						t.Fatal("native value changed")
					}
					visited = append(visited, index)
					if context != nil {
						context.Cleanup(func() { cleaned = append(cleaned, index) })
					}
					if selection == "bail" {
						fail("row failure")
					}
				})
				result := suite.RunTo(&bytes.Buffer{})
				if mode != "plain" && !reflect.DeepEqual(visited, cleaned) {
					t.Fatal("cleanup lost original indices", visited, cleaned)
				}
				switch selection {
				case "filter":
					if result.Passed != 1 || result.Skipped != 2 || fmt.Sprint(visited) != "[1]" {
						t.Fatal(result, visited)
					}
				case "rerun":
					if result.Passed != 1 || result.Skipped != 2 || fmt.Sprint(visited) != "[2]" {
						t.Fatal(result, visited)
					}
				case "bail":
					if result.Failed != 1 || result.Skipped != 2 || fmt.Sprint(visited) != "[0]" {
						t.Fatal(result, visited)
					}
				case "shuffle":
					first := append([]int(nil), visited...)
					visited, cleaned = nil, nil
					second := suite.RunTo(&bytes.Buffer{})
					if result.Passed != 3 || second.Passed != 3 || !reflect.DeepEqual(first, visited) {
						t.Fatal("shuffle changed row identity", first, visited)
					}
					for i, row := range second.Results {
						if row.Name != fmt.Sprintf("row %d", visited[i]) {
							t.Fatal("index became execution position")
						}
					}
				}
			})
		}
	}
}

func TestIndexedTablesRegistrationIsAtomic(t *testing.T) {
	for mode, register := range indexedRegistrars() {
		for _, failure := range []string{"blank", "duplicate", "existing", "throw", "panic", "reentrant"} {
			t.Run(mode+"/"+failure, func(t *testing.T) {
				suite := NewSuite("atomic")
				bodies, names := 0, 0
				suite.Test("existing", func() { bodies++ })
				cause := errors.New("naming failed")
				func() {
					defer func() {
						got := recover()
						if failure == "panic" {
							if got != "native name panic" {
								t.Fatal(got)
							}
							return
						}
						thrown, ok := got.(__kinmokuseiThrown)
						if !ok || (failure == "throw" && thrown.err != cause) {
							t.Fatal("naming error changed", got)
						}
					}()
					register(suite, []indexedID{42, 42}, func(value indexedID, index int) string {
						names++
						if index == 1 {
							switch failure {
							case "blank":
								return " "
							case "duplicate":
								return "row 0"
							case "existing":
								return "existing"
							case "throw":
								panic(__kinmokuseiThrown{err: cause})
							case "panic":
								panic("native name panic")
							case "reentrant":
								suite.Test("row 0", func() { bodies++ })
							}
						}
						return fmt.Sprintf("row %d", index)
					}, func(indexedID, int, *TestContext) { t.Fatal("partial batch committed") })
					t.Fatal("invalid table accepted")
				}()
				result := suite.RunTo(&bytes.Buffer{})
				want := 1
				if failure == "reentrant" {
					want = 2
				}
				if result.Passed != want || result.Failed != 0 || bodies != want || names != 2 {
					t.Fatal(result, bodies, names)
				}
			})
		}
	}
}

func TestIndexedTablesValidateArgumentsBeforeNaming(t *testing.T) {
	suite := NewSuite("validation")
	names, setups, bodies := 0, 0, 0
	rows := []int{42}
	name := func(int, int) string { names++; return "row" }
	body := func(int, int) { bodies++ }
	contextBody := func(int, int, *TestContext) { bodies++ }
	setup := func(int, int, *TestContext) string { setups++; return "fixture" }
	fixtureBody := func(int, int, string, *TestContext) { bodies++ }
	for _, check := range []func(){
		func() { SuiteTestEachIndexed(suite, rows, nil, body) }, func() { SuiteTestEachIndexed(suite, rows, name, nil) }, func() { SuiteTestEachIndexed(suite, []int{}, name, body) },
		func() { SuiteTestEachIndexedWithContext(suite, rows, nil, contextBody) }, func() { SuiteTestEachIndexedWithContext(suite, rows, name, nil) }, func() { SuiteTestEachIndexedWithContext(suite, []int{}, name, contextBody) },
		func() { SuiteTestEachIndexedWithFixture(suite, rows, nil, setup, fixtureBody) }, func() { SuiteTestEachIndexedWithFixture[int, string](suite, rows, name, nil, fixtureBody) },
		func() { SuiteTestEachIndexedWithFixture(suite, rows, name, setup, nil) }, func() { SuiteTestEachIndexedWithFixture(suite, []int{}, name, setup, fixtureBody) },
	} {
		assertThrownMessage(t, "testEachIndexed", check)
	}
	shared := NewFixtureSuite[string]("shared validation", func(*TestContext) string { setups++; return "shared" })
	sharedBody := func(int, int, string, *TestContext) { bodies++ }
	for _, check := range []func(){
		func() { FixtureSuiteTestEachIndexed(shared, rows, nil, sharedBody) }, func() { FixtureSuiteTestEachIndexed(shared, rows, name, nil) }, func() { FixtureSuiteTestEachIndexed(shared, []int{}, name, sharedBody) },
	} {
		assertThrownMessage(t, "testEachIndexed", check)
	}
	if names != 0 || setups != 0 || bodies != 0 {
		t.Fatal("invalid registration ran callbacks")
	}
}

func TestIndexedTablesFixturesKeepContextsPhasesAndCleanup(t *testing.T) {
	suite := NewSuite("fixture phases")
	var events []string
	var contexts []*TestContext
	SuiteTestEachIndexedWithFixture(suite, []int{42, 42, 42}, func(value, index int) string { return fmt.Sprint(index) },
		func(value, index int, context *TestContext) *TestContext {
			contexts = append(contexts, context)
			events = append(events, fmt.Sprintf("setup %d", index))
			path := context.TempDir()
			context.Cleanup(func() { events = append(events, fmt.Sprintf("cleanup %d", index)) })
			if index == 1 {
				withTestLocation("setup.km", 7, func() { fail("setup failed") })
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatal(err)
			}
			return context
		}, func(value, index int, fixture *TestContext, context *TestContext) {
			if context != fixture {
				t.Fatal("body received another context")
			}
			events = append(events, fmt.Sprintf("body %d", index))
			if index == 2 {
				skipTest("row unavailable")
			}
		})
	result := suite.RunTo(&bytes.Buffer{})
	if result.Passed != 1 || result.Failed != 1 || result.Skipped != 1 || result.Results[1].Failures[0].Phase != "fixture" || result.Results[1].Failures[0].Location != (TestLocation{File: "setup.km", Line: 7, Source: "explicit"}) || fmt.Sprint(events) != "[setup 0 body 0 cleanup 0 setup 1 cleanup 1 setup 2 body 2 cleanup 2]" {
		t.Fatal(result, events)
	}
	for _, context := range contexts {
		assertThrownMessage(t, "already closed", func() { context.Cleanup(func() {}) })
	}
}

func TestIndexedTablesSharedFixturesRetainTypesAndRefreshOnRerun(t *testing.T) {
	type resource struct{ Serial int }
	setups, cleanups := 0, 0
	suite := NewFixtureSuite[*resource]("shared", func(context *TestContext) *resource {
		setups++
		context.Cleanup(func() { cleanups++ })
		return &resource{setups}
	})
	var visited []int
	FixtureSuiteTestEachIndexed(suite, []indexedID{42, 42, 42}, func(value indexedID, index int) string { return fmt.Sprintf("row %d", index) },
		func(value indexedID, index int, fixture *resource, context *TestContext) {
			if value != 42 || fixture.Serial != setups {
				t.Fatal("shared fixture or row changed")
			}
			visited = append(visited, index)
			context.Cleanup(func() { visited = append(visited, index) })
		})
	t.Setenv("VASEST_FILTER", "row 2")
	for run := 0; run < 2; run++ {
		result := suite.RunTo(&bytes.Buffer{})
		if result.Passed != 1 || result.Skipped != 2 || setups != run+1 || cleanups != run+1 {
			t.Fatal(result, setups, cleanups)
		}
	}
	if fmt.Sprint(visited) != "[2 2 2 2]" {
		t.Fatal("shared fixture renumbered filtered rows", visited)
	}
}
