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

func TestSliceIndexedPredicatePositionsSavedMethodsAndReentrantChecks(t *testing.T) {
	type ID uint64
	large := ID(math.MaxUint64)
	values := []ID{large, large, 42}
	matcher := NewSliceExpectation(values)
	var any func(func(ID, int) bool) = matcher.ToSatisfyAnyIndexed
	var all func(func(ID, int) bool) = matcher.ToSatisfyAllIndexed
	var notAny func(func(ID, int) bool) = matcher.Not.ToSatisfyAnyIndexed
	var notAll func(func(ID, int) bool) = matcher.Not.ToSatisfyAllIndexed
	visited := []int{}
	any(func(value ID, index int) bool {
		visited = append(visited, index)
		return index == 1 && value == large
	})
	if fmt.Sprint(visited) != "[0 1]" {
		t.Fatal("duplicate values lost positions or short circuit", visited)
	}
	all(func(value ID, index int) bool {
		// Nested checks have their own index and do not reset the outer scan.
		expectSlice([]int{42}).ToSatisfyAllIndexed(func(inner int, position int) bool { return inner == 42 && position == 0 })
		return (index < 2 && value == large) || (index == 2 && value == 42)
	})
	values[2] = 43
	notAny(func(value ID, index int) bool { return index == 2 && value == 42 })
	notAll(func(value ID, index int) bool { return index < 2 && value == large })
	values = []ID{0}
	all(func(value ID, index int) bool { return (index < 2 && value == large) || (index == 2 && value == 43) })
	NewNegatedSliceExpectation([]ID{large}).ToSatisfyAnyIndexed(func(value ID, index int) bool { return index != 0 })
	NewNegatedSliceExpectation([]ID{large}).ToSatisfyAllIndexed(func(value ID, index int) bool { return value != large })
}

func TestSliceIndexedPredicateReadsLaterBackingMutationsAtOriginalPositions(t *testing.T) {
	values := []int{1, 0, 3}
	visited := []string{}
	expectSlice(values).ToSatisfyAllIndexed(func(value int, index int) bool {
		visited = append(visited, fmt.Sprintf("%d:%d", index, value))
		if index == 0 {
			values[1] = 2
		}
		return value == index+1
	})
	if fmt.Sprint(visited) != "[0:1 1:2 2:3]" {
		t.Fatal("snapshot or index changed", visited)
	}
	calls := 0
	probes := []boundaryFormattingProbe{{Calls: &calls}, {Calls: &calls}}
	expectSlice(probes).ToSatisfyAllIndexed(func(value boundaryFormattingProbe, index int) bool { return value.Calls == &calls && index >= 0 })
	assertThrownMessage(t, "indexed predicate", func() {
		expectSlice(probes).ToSatisfyAnyIndexed(func(value boundaryFormattingProbe, index int) bool { return false })
	})
	if calls != 0 {
		t.Fatal("indexed mismatch formatted application values", calls)
	}
}

func TestSliceIndexedPredicateMatchersShortCircuitAndNegateTheAggregate(t *testing.T) {
	for _, tc := range []struct {
		name               string
		values             []int
		predicate          func(int, int) bool
		any, all           bool
		anyCalls, allCalls int
	}{
		{"nil", nil, func(int, int) bool { return true }, false, true, 0, 0},
		{"empty", []int{}, func(int, int) bool { return false }, false, true, 0, 0},
		{"all match", []int{1, 2, 3}, func(int, int) bool { return true }, true, true, 1, 3},
		{"none match", []int{1, 2, 3}, func(int, int) bool { return false }, false, false, 3, 1},
		{"middle matches", []int{1, 2, 3}, func(value int, index int) bool { return value == 2 }, true, false, 2, 1},
		{"last fails", []int{1, 2, 3}, func(value int, index int) bool { return value < 3 }, true, false, 1, 3},
		{"last matches", []int{1, 2, 3}, func(value int, index int) bool { return value == 3 }, true, false, 3, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, all := range []bool{false, true} {
				for _, negated := range []bool{false, true} {
					calls := 0
					predicate := func(value int, index int) bool {
						calls++
						if index != calls-1 {
							t.Fatal("incorrect or repeated index", index, calls)
						}
						return tc.predicate(value, index)
					}
					matcher := NewSliceExpectation(tc.values)
					if calls != 0 {
						t.Fatal("construction executed predicate")
					}
					check := func() {
						if all {
							if negated {
								matcher.Not.ToSatisfyAllIndexed(predicate)
							} else {
								matcher.ToSatisfyAllIndexed(predicate)
							}
						} else if negated {
							matcher.Not.ToSatisfyAnyIndexed(predicate)
						} else {
							matcher.ToSatisfyAnyIndexed(predicate)
						}
					}
					matches, wantCalls := tc.any, tc.anyCalls
					if all {
						matches, wantCalls = tc.all, tc.allCalls
					}
					if matches == negated {
						assertThrownMessage(t, fmt.Sprintf("checked %d of %d element(s)", wantCalls, len(tc.values)), check)
					} else {
						check()
					}
					if calls != wantCalls {
						t.Fatal("predicate repeated or did not stop", all, negated, calls, wantCalls)
					}
				}
			}
		})
	}
}

func TestSliceIndexedPredicateMatchersRetainNativeTypesReferencesAndLiveElements(t *testing.T) {
	type ID int64
	values := []ID{1<<60 + 1, 1<<60 + 2}
	matcher := expectSlice[ID](values)
	matcher.ToSatisfyAnyIndexed(func(actual ID, index int) bool { return actual == ID(1<<60+1) })
	matcher.ToSatisfyAllIndexed(func(actual ID, index int) bool { return actual > ID(1<<60) })
	matcher.Not.ToSatisfyAnyIndexed(func(actual ID, index int) bool { return actual < 0 })
	values[1] = -1
	matcher.Not.ToSatisfyAllIndexed(func(actual ID, index int) bool { return actual > 0 })
	// Each visited element is read from the native backing slice in order.
	expectSlice([]int{1, 2}).ToSatisfyAllIndexed(func(value int, index int) bool { return value > 0 })
	live := []int{1, 0}
	visited := []int{}
	expectSlice(live).ToSatisfyAllIndexed(func(value int, index int) bool { visited = append(visited, value); live[1] = 2; return value > 0 })
	if fmt.Sprint(visited) != "[1 2]" {
		t.Fatal("elements were copied before evaluation", visited)
	}
	type record struct{ Value int }
	object := &record{42}
	expectSlice([]*record{nil, object}).ToSatisfyAnyIndexed(func(value *record, index int) bool {
		if value == nil {
			return false
		}
		value.Value++
		return value == object
	})
	if object.Value != 43 {
		t.Fatal("class/reference identity changed", object)
	}
	nested := [][]int{nil, {}, {42}}
	expectSlice(nested).ToSatisfyAnyIndexed(func(value []int, index int) bool { return value == nil })
	expectSlice(nested).Not.ToSatisfyAllIndexed(func(value []int, index int) bool { return len(value) == 0 })
	expectSlice([]any{int32(42), "金木犀"}).ToSatisfyAnyIndexed(func(value any, index int) bool { _, ok := value.(int32); return ok })
}

func TestSliceIndexedPredicateMatchersRejectNilPredicatesAndIdentifyCaller(t *testing.T) {
	for _, values := range [][]int{nil, {}, {42}} {
		matcher := expectSlice(values)
		for _, check := range []func(){func() { matcher.ToSatisfyAnyIndexed(nil) }, func() { matcher.ToSatisfyAllIndexed(nil) }, func() { matcher.Not.ToSatisfyAnyIndexed(nil) }, func() { matcher.Not.ToSatisfyAllIndexed(nil) }} {
			assertThrownMessage(t, "predicate must not be nil", check)
		}
	}
	matcher := expectSlice([]int{42})
	yes, no := func(int, int) bool { return true }, func(int, int) bool { return false }
	var want TestLocation
	record := func() {
		_, file, line, _ := runtime.Caller(1)
		want = TestLocation{File: file, Line: line, Source: "go"}
	}
	for _, check := range []func(){
		func() { record(); matcher.ToSatisfyAnyIndexed(no) }, func() { record(); matcher.Not.ToSatisfyAnyIndexed(yes) },
		func() { record(); matcher.ToSatisfyAllIndexed(no) }, func() { record(); matcher.Not.ToSatisfyAllIndexed(yes) },
		func() { record(); matcher.ToSatisfyAnyIndexed(nil) }, func() { record(); matcher.Not.ToSatisfyAnyIndexed(nil) },
		func() { record(); matcher.ToSatisfyAllIndexed(nil) }, func() { record(); matcher.Not.ToSatisfyAllIndexed(nil) },
	} {
		func() {
			defer func() {
				thrown, ok := recover().(__kinmokuseiThrown)
				if !ok || failureLocation(thrown.err) != want {
					t.Fatal("matcher location", thrown, want)
				}
			}()
			check()
		}()
	}
}

func TestSliceIndexedPredicateMatchersPropagateCallbackControlFlow(t *testing.T) {
	for _, all := range []bool{false, true} {
		for _, negated := range []bool{false, true} {
			check := func(predicate func(int, int) bool) {
				matcher := expectSlice([]int{1, 2, 3})
				if all {
					if negated {
						matcher.Not.ToSatisfyAllIndexed(predicate)
					} else {
						matcher.ToSatisfyAllIndexed(predicate)
					}
				} else if negated {
					matcher.Not.ToSatisfyAnyIndexed(predicate)
				} else {
					matcher.ToSatisfyAnyIndexed(predicate)
				}
			}
			for _, exception := range []error{nil, errors.New("predicate threw"), NewFailureError(errors.New("located"), TestLocation{File: "domain.km", Line: 42, Source: "explicit"})} {
				func() {
					calls := 0
					defer func() {
						thrown, ok := recover().(__kinmokuseiThrown)
						if !ok || thrown.err != exception || calls != 1 {
							t.Fatal("callback exception changed", thrown, exception, calls)
						}
					}()
					check(func(int, int) bool { calls++; panic(__kinmokuseiThrown{err: exception}) })
				}()
			}
			for _, mode := range []string{"throw", "skip", "panic"} {
				func() {
					var events []string
					calls := 0
					if mode == "panic" {
						defer func() {
							if recover() != "slice predicate panic" || fmt.Sprint(events) != "[afterEach cleanup afterAll]" || calls != 1 {
								t.Fatal("panic cleanup", events, calls)
							}
						}()
					}
					suite := NewSuite("slice predicate cleanup")
					suite.AfterEach(func() { events = append(events, "afterEach") })
					suite.AfterAll(func() { events = append(events, "afterAll") })
					suite.TestWithContext("case", func(context *TestContext) {
						context.Cleanup(func() { events = append(events, "cleanup") })
						check(func(int, int) bool {
							calls++
							switch mode {
							case "panic":
								panic("slice predicate panic")
							case "skip":
								skipTest("slice predicate unavailable")
							case "throw":
								withTestLocation("domain.km", 42, func() { panic(__kinmokuseiThrown{err: errors.New("located")}) })
							}
							t.Fatal("callback control flow swallowed")
							return true
						})
					})
					summary := suite.RunTo(&bytes.Buffer{})
					if mode == "panic" {
						t.Fatal("native panic swallowed")
					}
					if calls != 1 || fmt.Sprint(events) != "[afterEach cleanup afterAll]" {
						t.Fatal(summary, events, calls)
					}
					if mode == "skip" {
						if summary.Skipped != 1 || summary.Failed != 0 || !strings.Contains(summary.Results[0].Message, "slice predicate unavailable") {
							t.Fatal(summary)
						}
					} else if summary.Failed != 1 || summary.Results[0].Failures[0].Location != (TestLocation{File: "domain.km", Line: 42, Source: "explicit"}) {
						t.Fatal(summary)
					}
				}()
			}
		}
	}
}
