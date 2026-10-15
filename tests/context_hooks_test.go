package vasest

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestContextHooksShareCaseLifetimeAcrossBodyKinds(t *testing.T) {
	for _, kind := range []string{"plain", "context", "fixture", "indexed"} {
		t.Run(kind, func(t *testing.T) {
			t.Setenv("VASEST_CONTEXT_HOOK_DATA", "original")
			suite := NewSuite("context hooks")
			var before, previous *TestContext
			var phases []string
			path := ""
			suite.BeforeEachWithContext(func(context *TestContext) {
				if context == previous {
					t.Fatal("reused context on rerun")
				}
				before = context
				path = context.TempDir()
				context.SetEnv("VASEST_CONTEXT_HOOK_DATA", path)
				context.Cleanup(func() { phases = append(phases, "before cleanup") })
				phases = append(phases, "before")
			})
			body := func(context *TestContext) {
				if context != before || os.Getenv("VASEST_CONTEXT_HOOK_DATA") != path {
					t.Fatal("body lost context/resources")
				}
				context.Cleanup(func() { phases = append(phases, "body cleanup") })
				phases = append(phases, "body")
			}
			switch kind {
			case "plain":
				suite.Test("row", func() { body(before) })
			case "context":
				suite.TestWithContext("row", body)
			case "fixture":
				SuiteTestWithFixture(suite, "row", func(context *TestContext) *TestContext {
					if context != before {
						t.Fatal("fixture lost context")
					}
					context.Cleanup(func() { phases = append(phases, "fixture cleanup") })
					phases = append(phases, "fixture")
					return context
				}, func(fixture, context *TestContext) {
					if fixture != context {
						t.Fatal("fixture context")
					}
					body(context)
				})
			case "indexed":
				SuiteTestEachIndexedWithContext(suite, []int{42}, func(int, int) string { return "row" }, func(value, index int, context *TestContext) {
					if value != 42 || index != 0 {
						t.Fatal("row identity")
					}
					body(context)
				})
			}
			suite.AfterEachWithContext(func(context *TestContext) {
				if context != before || os.Getenv("VASEST_CONTEXT_HOOK_DATA") != path {
					t.Fatal("teardown lost context/resources")
				}
				if _, err := os.Stat(path); err != nil {
					t.Fatal("resources cleaned before hook", err)
				}
				context.Cleanup(func() { phases = append(phases, "after cleanup") })
				phases = append(phases, "after")
			})
			for run := 0; run < 2; run++ {
				phases = nil
				result := suite.RunTo(&bytes.Buffer{})
				want := "before,body,after,after cleanup,body cleanup,before cleanup"
				if kind == "fixture" {
					want = "before,fixture,body,after,after cleanup,body cleanup,fixture cleanup,before cleanup"
				}
				if result.Passed != 1 || result.Failed != 0 || strings.Join(phases, ",") != want {
					t.Fatal(result, phases)
				}
				if _, err := os.Stat(path); !os.IsNotExist(err) || os.Getenv("VASEST_CONTEXT_HOOK_DATA") != "original" {
					t.Fatal("hook resources leaked", err)
				}
				assertThrownMessage(t, "closed", func() { before.Cleanup(func() {}) })
				previous = before
			}
		})
	}
}

func TestContextHooksFailuresSkipsAndCleanupKeepPhases(t *testing.T) {
	for _, mode := range []string{"before failure", "before skip", "after failure", "after skip", "cleanup failure", "multiple failures"} {
		t.Run(mode, func(t *testing.T) {
			suite := NewSuite("context hook failures")
			var before *TestContext
			var phases []string
			suite.BeforeEachWithContext(func(context *TestContext) {
				before = context
				phases = append(phases, "before")
				context.Cleanup(func() {
					phases = append(phases, "cleanup")
					if mode == "cleanup failure" || mode == "multiple failures" {
						panic(__kinmokuseiThrown{err: errors.New("cleanup failed")})
					}
				})
				if mode == "before failure" || mode == "multiple failures" {
					withTestLocation("setup.km", 7, func() { panic(__kinmokuseiThrown{err: errors.New("setup failed")}) })
				}
				if mode == "before skip" {
					skipTest("setup unavailable")
				}
			})
			suite.AfterEachWithContext(func(context *TestContext) {
				if context != before {
					t.Fatal("failure path changed context")
				}
				phases = append(phases, "after")
				context.Cleanup(func() { phases = append(phases, "after cleanup") })
				if mode == "after failure" || mode == "multiple failures" {
					withTestLocation("teardown.km", 9, func() { panic(__kinmokuseiThrown{err: errors.New("teardown failed")}) })
				}
				if mode == "after skip" {
					skipTest("teardown unavailable")
				}
			})
			suite.Test("row", func() { phases = append(phases, "body") })
			result := suite.RunTo(&bytes.Buffer{})
			want := "before,body,after,after cleanup,cleanup"
			if strings.HasPrefix(mode, "before") || mode == "multiple failures" {
				want = "before,after,after cleanup,cleanup"
			}
			if strings.Join(phases, ",") != want {
				t.Fatal(phases)
			}
			row := result.Results[0]
			if mode == "before skip" {
				if result.Skipped != 1 || result.Failed != 0 || row.Message != "setup unavailable" {
					t.Fatal(result)
				}
				return
			}
			if result.Failed != 1 {
				t.Fatal(result)
			}
			var got []string
			for _, failure := range row.Failures {
				got = append(got, failure.Phase)
				if failure.Phase == "beforeEach" && failure.Location != (TestLocation{File: "setup.km", Line: 7, Source: "explicit"}) {
					t.Fatal(failure)
				}
				if failure.Phase == "afterEach" && mode != "after skip" && failure.Location != (TestLocation{File: "teardown.km", Line: 9, Source: "explicit"}) {
					t.Fatal(failure)
				}
			}
			wantPhase := "afterEach"
			if mode == "before failure" {
				wantPhase = "beforeEach"
			}
			if mode == "cleanup failure" {
				wantPhase = "cleanup"
			}
			if mode == "multiple failures" {
				wantPhase = "beforeEach,afterEach,cleanup"
			}
			if strings.Join(got, ",") != wantPhase {
				t.Fatal(row)
			}
		})
	}
}

func TestContextHooksNativePanicsUnwindHooksAndResources(t *testing.T) {
	for _, phase := range []string{"before", "body", "after"} {
		t.Run(phase, func(t *testing.T) {
			suite := NewSuite("panic")
			var visited []string
			var saved *TestContext
			suite.BeforeEachWithContext(func(context *TestContext) {
				saved = context
				visited = append(visited, "before")
				context.Cleanup(func() { visited = append(visited, "cleanup") })
				if phase == "before" {
					panic("native hook panic")
				}
			})
			suite.Test("row", func() {
				visited = append(visited, "body")
				if phase == "body" {
					panic("native hook panic")
				}
			})
			suite.AfterEachWithContext(func(context *TestContext) {
				if context != saved {
					t.Fatal("panic changed context")
				}
				visited = append(visited, "after")
				context.Cleanup(func() { visited = append(visited, "after cleanup") })
				if phase == "after" {
					panic("native hook panic")
				}
			})
			suite.AfterAll(func() { visited = append(visited, "afterAll") })
			func() {
				defer func() {
					if got := recover(); got != "native hook panic" {
						t.Fatal(got)
					}
				}()
				suite.RunTo(&bytes.Buffer{})
				t.Fatal("panic swallowed")
			}()
			want := "before,body,after,after cleanup,cleanup,afterAll"
			if phase == "before" {
				want = "before,after,after cleanup,cleanup,afterAll"
			}
			if strings.Join(visited, ",") != want {
				t.Fatal(visited)
			}
			assertThrownMessage(t, "closed", func() { saved.Cleanup(func() {}) })
		})
	}
}

func TestContextHooksReplacementValidationAndInactiveCases(t *testing.T) {
	for _, contextLast := range []bool{false, true} {
		suite := NewSuite("replacement")
		var visited []string
		plain := func() {
			suite.BeforeEach(func() { visited = append(visited, "plain before") })
			suite.AfterEach(func() { visited = append(visited, "plain after") })
		}
		withContext := func() {
			suite.BeforeEachWithContext(func(*TestContext) { visited = append(visited, "context before") })
			suite.AfterEachWithContext(func(*TestContext) { visited = append(visited, "context after") })
		}
		if contextLast {
			plain()
			withContext()
		} else {
			withContext()
			plain()
		}
		for _, check := range []func(){func() { suite.BeforeEachWithContext(nil) }, func() { suite.AfterEachWithContext(nil) }, func() { suite.BeforeEach(nil) }, func() { suite.AfterEach(nil) }} {
			assertThrownMessage(t, "must not be nil", check)
		}
		suite.Test("run", func() {})
		suite.Skip("skip", func() { t.Fatal("skip body") })
		suite.Todo("todo")
		result := suite.RunTo(&bytes.Buffer{})
		want := "plain before,plain after"
		if contextLast {
			want = "context before,context after"
		}
		if result.Passed != 1 || strings.Join(visited, ",") != want {
			t.Fatal(result, visited)
		}
	}
	for _, selection := range []string{"filter", "rerun", "bail", "shuffle"} {
		t.Run(selection, func(t *testing.T) {
			switch selection {
			case "filter":
				t.Setenv("VASEST_FILTER", "row 1")
			case "rerun":
				t.Setenv("VASEST_RERUN_SELECTION", `{"all":false,"suites":[{"name":"selection","wholeSuite":false,"cases":["row 1"]}]}`)
			case "bail":
				t.Setenv("VASEST_BAIL", "1")
			case "shuffle":
				t.Setenv("VASEST_SHUFFLE_SEED", "42")
			}
			suite := NewSuite("selection")
			var opened, closed []*TestContext
			suite.BeforeEachWithContext(func(context *TestContext) { opened = append(opened, context) })
			suite.AfterEachWithContext(func(context *TestContext) { closed = append(closed, context) })
			SuiteTestEachIndexedWithContext(suite, []int{0, 1, 2}, func(value, index int) string { return fmt.Sprintf("row %d", index) }, func(value, index int, context *TestContext) {
				if context != opened[len(opened)-1] {
					t.Fatal("selected context mismatch")
				}
				if selection == "bail" {
					fail("bail")
				}
			})
			result := suite.RunTo(&bytes.Buffer{})
			want := 1
			if selection == "shuffle" {
				want = 3
			}
			if len(opened) != want || !reflect.DeepEqual(opened, closed) {
				t.Fatal(result, opened, closed)
			}
		})
	}
}

func TestContextHooksSharedFixturesKeepNativeTypeAndCaseContext(t *testing.T) {
	type resource struct{ ID int64 }
	setups, cleanups, before, after := 0, 0, 0, 0
	contextActive := true
	var fixture *resource
	var saved *TestContext
	suite := NewFixtureSuite("shared", func(context *TestContext) *resource {
		setups++
		fixture = &resource{ID: 1<<60 + 1}
		context.Cleanup(func() { cleanups++ })
		return fixture
	})
	suite.BeforeEach(func(*resource) { t.Fatal("replaced typed before") })
	suite.BeforeEachWithContext(func(value *resource, context *TestContext) {
		if value != fixture || value.ID != 1<<60+1 {
			t.Fatal("fixture type/value")
		}
		before++
		saved = context
	})
	suite.AfterEach(func(*resource) { t.Fatal("replaced typed after") })
	suite.AfterEachWithContext(func(value *resource, context *TestContext) {
		if value != fixture || context != saved {
			t.Fatal("shared case context")
		}
		after++
	})
	assertThrownMessage(t, "must not be nil", func() { suite.BeforeEachWithContext(nil) })
	assertThrownMessage(t, "must not be nil", func() { suite.AfterEachWithContext(nil) })
	suite.Test("row", func(value *resource, context *TestContext) {
		if value != fixture || (contextActive && context != saved) {
			t.Fatal("shared body context")
		}
	})
	for run := 0; run < 2; run++ {
		if result := suite.RunTo(&bytes.Buffer{}); result.Passed != 1 || result.Failed != 0 {
			t.Fatal(result)
		}
	}
	if setups != 2 || cleanups != 2 || before != 2 || after != 2 {
		t.Fatal(setups, cleanups, before, after)
	}
	// Ordinary typed hooks can replace the context variants too.
	contextActive = false
	suite.BeforeEach(func(*resource) { before++ })
	suite.AfterEach(func(*resource) { after++ })
	suite.Test("ordinary hooks", func(*resource, *TestContext) {})
	if result := suite.RunTo(&bytes.Buffer{}); result.Passed != 2 || before != 4 || after != 4 {
		t.Fatal(result, before, after)
	}
}

func TestContextHooksSharedSetupFailureAndInactiveSuitesRunNoCaseHooks(t *testing.T) {
	for _, mode := range []string{"setup failure", "inactive", "filtered"} {
		t.Run(mode, func(t *testing.T) {
			if mode == "filtered" {
				t.Setenv("VASEST_FILTER", "missing")
			}
			setups, cleaned := 0, 0
			suite := NewFixtureSuite("shared", func(context *TestContext) int {
				setups++
				context.Cleanup(func() { cleaned++ })
				panic(__kinmokuseiThrown{err: errors.New("shared setup failed")})
			})
			suite.BeforeEachWithContext(func(int, *TestContext) { t.Fatal("case hook ran without shared fixture") })
			suite.AfterEachWithContext(func(int, *TestContext) { t.Fatal("after hook ran without shared fixture") })
			if mode == "inactive" {
				suite.Skip("disabled", func(int, *TestContext) {})
			} else {
				suite.Test("row", func(int, *TestContext) { t.Fatal("body ran without shared fixture") })
			}
			result := suite.RunTo(&bytes.Buffer{})
			want := 0
			if mode == "setup failure" {
				want = 1
			}
			if setups != want || cleaned != want || result.Failed != want || result.Skipped != 1 {
				t.Fatal(result, setups, cleaned)
			}
		})
	}
}
