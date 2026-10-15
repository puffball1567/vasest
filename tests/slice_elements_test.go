package vasest

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"runtime"
	"strings"
	"testing"
)

func TestSliceElementsPresenceEqualityAndBounds(t *testing.T) {
	for _, values := range [][]int{nil, {}, {0, 42, 43}} {
		matcher := expectSlice(values)
		for _, index := range []int{len(values), math.MaxInt} {
			matcher.Not.ToHaveElement(index, 0)
			assertThrownMessage(t, "index is missing", func() { matcher.ToHaveElement(index, 0) })
		}
		for _, check := range []func(){
			func() { matcher.ToHaveElement(-1, 0) },
			func() { matcher.Not.ToHaveElement(-1, 0) },
			func() {
				matcher.ToHaveElementSatisfying(-1, func(int) bool { t.Fatal("negative index reached callback"); return true })
			},
			func() {
				matcher.Not.ToHaveElementSatisfying(-1, func(int) bool { t.Fatal("negative index reached callback"); return true })
			},
		} {
			assertThrownMessage(t, "index must not be negative", check)
		}
		for index, value := range values {
			matcher.ToHaveElement(index, value)
			matcher.Not.ToHaveElement(index, value+1)
			assertThrownMessage(t, "to equal", func() { matcher.ToHaveElement(index, value+1) })
			assertThrownMessage(t, "not to equal", func() { matcher.Not.ToHaveElement(index, value) })
		}
	}
}

func TestSliceElementsPredicatesValidateBeforeReadingAndUseOneSelectedValue(t *testing.T) {
	for _, values := range [][]int{nil, {}, {0, 42}} {
		matcher := expectSlice(values)
		calls := 0
		predicate := func(value int) bool { calls++; return true }
		for _, index := range []int{len(values), math.MaxInt} {
			matcher.Not.ToHaveElementSatisfying(index, predicate)
			assertThrownMessage(t, "index is missing", func() { matcher.ToHaveElementSatisfying(index, predicate) })
		}
		if calls != 0 {
			t.Fatal("absent position invoked callback", calls)
		}
		for _, index := range []int{0, len(values), math.MaxInt} {
			assertThrownMessage(t, "predicate must not be nil", func() { matcher.ToHaveElementSatisfying(index, nil) })
			assertThrownMessage(t, "predicate must not be nil", func() { matcher.Not.ToHaveElementSatisfying(index, nil) })
		}
	}
	type Score int64
	large := Score(1<<60 + 1)
	values := []Score{large}
	matcher := NewSliceExpectation(values)
	calls := 0
	matcher.ToHaveElementSatisfying(0, func(value Score) bool {
		calls++
		values[0] = -1
		return value == large
	})
	matcher.Not.ToHaveElementSatisfying(0, func(value Score) bool {
		calls++
		values[0] = large
		return value > 0
	})
	if calls != 2 || values[0] != large {
		t.Fatal("selected value reread or callback repeated", calls, values)
	}
	matcher.ToHaveElement(0, large)
	type record struct{ Value int }
	object := &record{42}
	expectSlice([]*record{nil, object}).ToHaveElementSatisfying(1, func(value *record) bool { value.Value++; return value == object })
	if object.Value != 43 {
		t.Fatal("reference identity changed")
	}
	expectSlice([][]int{nil, {}, {42}}).ToHaveElementSatisfying(0, func(value []int) bool { return value == nil })
	expectSlice([][]int{nil, {}, {42}}).ToHaveElementSatisfying(1, func(value []int) bool { return value != nil && len(value) == 0 })
}

func TestSliceElementsNativeEqualitySavedBindingsAndDiagnostics(t *testing.T) {
	type ID uint64
	large := ID(math.MaxUint64)
	values := []ID{large, 42}
	matcher := NewSliceExpectation(values)
	var equality func(int, ID) = matcher.ToHaveElement
	var negativeEquality func(int, ID) = matcher.Not.ToHaveElement
	var predicate func(int, func(ID) bool) = matcher.ToHaveElementSatisfying
	var negativePredicate func(int, func(ID) bool) = matcher.Not.ToHaveElementSatisfying
	equality(0, large)
	values[1] = 43
	equality(1, 43)
	negativeEquality(1, 42)
	predicate(0, func(value ID) bool { return value == large })
	negativePredicate(1, func(value ID) bool { return value == 42 })
	values = []ID{0}
	equality(0, large)
	NewNegatedSliceExpectation([]ID{large}).ToHaveElement(1, large)
	NewNegatedSliceExpectation([]ID{large}).ToHaveElementSatisfying(1, func(ID) bool { t.Fatal("absent position evaluated"); return true })
	expectSlice([][]int{nil, {}}).ToHaveElement(0, nil)
	expectSlice([][]int{nil, {}}).Not.ToHaveElement(0, []int{})
	expectSlice([]map[string]int{{"value": 42}}).ToHaveElement(0, map[string]int{"value": 42})
	nan := math.NaN()
	expectSlice([]float64{nan}).Not.ToHaveElement(0, nan)
	expectSlice([]float64{math.Copysign(0, -1), math.Inf(1)}).ToHaveElement(0, 0)
	expectSlice([]float64{math.Copysign(0, -1), math.Inf(1)}).ToHaveElement(1, math.Inf(1))
	var missing func()
	callback := func() {}
	expectSlice([]func(){missing, callback}).ToHaveElement(0, nil)
	expectSlice([]func(){missing, callback}).Not.ToHaveElement(1, callback)
	cycle := map[string]any{}
	cycle["self"] = cycle
	expectSlice([]map[string]any{cycle}).ToHaveElement(0, cycle)
	func() {
		defer func() {
			thrown, ok := recover().(__kinmokuseiThrown)
			if !ok || !strings.Contains(thrown.err.Error(), "element 0") || len(thrown.err.Error()) > 1000 {
				t.Fatal("unbounded or missing cycle diagnostic", thrown)
			}
		}()
		expectSlice([]map[string]any{cycle}).ToHaveElement(0, map[string]any{"different": 42})
		t.Fatal("cyclic mismatch accepted")
	}()
	calls := 0
	probes := []boundaryFormattingProbe{{Calls: &calls}}
	expectSlice(probes).ToHaveElement(0, probes[0])
	expectSlice(probes).Not.ToHaveElement(1, probes[0])
	assertThrownMessage(t, "to satisfy predicate", func() {
		expectSlice(probes).ToHaveElementSatisfying(0, func(boundaryFormattingProbe) bool { return false })
	})
	if calls != 0 {
		t.Fatal("selected predicate or successful equality formatted values", calls)
	}
}

func TestSliceElementsPreserveCallerAndCallbackLocations(t *testing.T) {
	var want TestLocation
	record := func() {
		_, file, line, _ := runtime.Caller(1)
		want = TestLocation{File: file, Line: line, Source: "go"}
	}
	matcher := expectSlice([]int{42})
	yes, no := func(int) bool { return true }, func(int) bool { return false }
	for _, check := range []func(){
		func() { record(); matcher.ToHaveElement(0, 43) }, func() { record(); matcher.Not.ToHaveElement(0, 42) },
		func() { record(); matcher.ToHaveElement(1, 42) }, func() { record(); matcher.Not.ToHaveElement(-1, 42) },
		func() { record(); matcher.ToHaveElementSatisfying(0, no) }, func() { record(); matcher.Not.ToHaveElementSatisfying(0, yes) },
		func() { record(); matcher.ToHaveElementSatisfying(1, yes) }, func() { record(); matcher.Not.ToHaveElementSatisfying(-1, yes) },
		func() { record(); matcher.ToHaveElementSatisfying(0, nil) }, func() { record(); matcher.Not.ToHaveElementSatisfying(1, nil) },
	} {
		func() {
			defer func() {
				thrown, ok := recover().(__kinmokuseiThrown)
				if !ok || failureLocation(thrown.err) != want {
					t.Fatal("lost matcher location", thrown, want)
				}
			}()
			check()
			t.Fatal("mismatch accepted")
		}()
	}
	for _, negative := range []bool{false, true} {
		for _, exception := range []error{nil, errors.New("callback"), NewFailureError(errors.New("located"), TestLocation{File: "domain.km", Line: 42, Source: "explicit"})} {
			func() {
				defer func() {
					thrown, ok := recover().(__kinmokuseiThrown)
					if !ok || thrown.err != exception {
						t.Fatal("callback identity changed", thrown, exception)
					}
				}()
				callback := func(int) bool { panic(__kinmokuseiThrown{err: exception}) }
				if negative {
					matcher.Not.ToHaveElementSatisfying(0, callback)
				} else {
					matcher.ToHaveElementSatisfying(0, callback)
				}
				t.Fatal("callback throw swallowed")
			}()
		}
	}
}

func TestSliceElementsCleanupOnFailureSkipAndNativePanic(t *testing.T) {
	for _, negative := range []bool{false, true} {
		for _, mode := range []string{"mismatch", "throw", "skip", "panic", "missing"} {
			func() {
				var events []string
				calls := 0
				if mode == "panic" {
					defer func() {
						if recover() != "selected element panic" || fmt.Sprint(events) != "[afterEach cleanup afterAll]" || calls != 1 {
							t.Fatal(events, calls)
						}
					}()
				}
				suite := NewSuite("selected element lifecycle")
				suite.AfterEach(func() { events = append(events, "afterEach") })
				suite.AfterAll(func() { events = append(events, "afterAll") })
				suite.TestWithContext("selected", func(context *TestContext) {
					context.Cleanup(func() { events = append(events, "cleanup") })
					index := 0
					if mode == "missing" {
						index = 1
					}
					predicate := func(int) bool {
						calls++
						switch mode {
						case "throw":
							panic(__kinmokuseiThrown{err: errors.New("selected throw")})
						case "skip":
							skipTest("selected element unavailable")
						case "panic":
							panic("selected element panic")
						}
						return negative
					}
					if negative {
						expectSlice([]int{42}).Not.ToHaveElementSatisfying(index, predicate)
					} else {
						expectSlice([]int{42}).ToHaveElementSatisfying(index, predicate)
					}
				})
				summary := suite.RunTo(&bytes.Buffer{})
				if mode == "panic" {
					t.Fatal("native panic swallowed")
				}
				wantCalls := 1
				if mode == "missing" {
					wantCalls = 0
				}
				if calls != wantCalls || fmt.Sprint(events) != "[afterEach cleanup afterAll]" {
					t.Fatal(summary, events, calls)
				}
				if mode == "skip" {
					if summary.Skipped != 1 || summary.Failed != 0 {
						t.Fatal(summary)
					}
				} else if mode == "missing" && negative {
					if summary.Passed != 1 {
						t.Fatal(summary)
					}
				} else if summary.Failed != 1 {
					t.Fatal(summary)
				}
			}()
		}
	}
}
