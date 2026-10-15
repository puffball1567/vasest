package vasest

import (
	"bytes"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"
)

func TestSlicePredicateMatchersShortCircuitAndNegateTheAggregate(t *testing.T) {
	for _, tc := range []struct {
		name               string
		values             []int
		predicate          func(int) bool
		any, all           bool
		anyCalls, allCalls int
	}{
		{"nil", nil, func(int) bool { return true }, false, true, 0, 0},
		{"empty", []int{}, func(int) bool { return false }, false, true, 0, 0},
		{"all match", []int{1, 2, 3}, func(int) bool { return true }, true, true, 1, 3},
		{"none match", []int{1, 2, 3}, func(int) bool { return false }, false, false, 3, 1},
		{"middle matches", []int{1, 2, 3}, func(value int) bool { return value == 2 }, true, false, 2, 1},
		{"last fails", []int{1, 2, 3}, func(value int) bool { return value < 3 }, true, false, 1, 3},
		{"last matches", []int{1, 2, 3}, func(value int) bool { return value == 3 }, true, false, 3, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, all := range []bool{false, true} {
				for _, negated := range []bool{false, true} {
					calls := 0
					predicate := func(value int) bool { calls++; return tc.predicate(value) }
					matcher := NewSliceExpectation(tc.values)
					if calls != 0 {
						t.Fatal("construction executed predicate")
					}
					check := func() {
						if all {
							if negated {
								matcher.Not.ToSatisfyAll(predicate)
							} else {
								matcher.ToSatisfyAll(predicate)
							}
						} else if negated {
							matcher.Not.ToSatisfyAny(predicate)
						} else {
							matcher.ToSatisfyAny(predicate)
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

func TestSlicePredicateMatchersRetainNativeTypesReferencesAndLiveElements(t *testing.T) {
	type ID int64
	values := []ID{1<<60 + 1, 1<<60 + 2}
	matcher := expectSlice[ID](values)
	matcher.ToSatisfyAny(func(actual ID) bool { return actual == ID(1<<60+1) })
	matcher.ToSatisfyAll(func(actual ID) bool { return actual > ID(1<<60) })
	matcher.Not.ToSatisfyAny(func(actual ID) bool { return actual < 0 })
	values[1] = -1
	matcher.Not.ToSatisfyAll(func(actual ID) bool { return actual > 0 })
	// Each visited element is read from the native backing slice in order.
	expectSlice([]int{1, 2}).ToSatisfyAll(func(value int) bool { return value > 0 })
	live := []int{1, 0}
	visited := []int{}
	expectSlice(live).ToSatisfyAll(func(value int) bool { visited = append(visited, value); live[1] = 2; return value > 0 })
	if fmt.Sprint(visited) != "[1 2]" {
		t.Fatal("elements were copied before evaluation", visited)
	}
	type record struct{ Value int }
	object := &record{42}
	expectSlice([]*record{nil, object}).ToSatisfyAny(func(value *record) bool {
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
	expectSlice(nested).ToSatisfyAny(func(value []int) bool { return value == nil })
	expectSlice(nested).Not.ToSatisfyAll(func(value []int) bool { return len(value) == 0 })
	expectSlice([]any{int32(42), "金木犀"}).ToSatisfyAny(func(value any) bool { _, ok := value.(int32); return ok })
}

func TestSlicePredicateMatchersRejectNilPredicatesAndIdentifyCaller(t *testing.T) {
	for _, values := range [][]int{nil, {}, {42}} {
		matcher := expectSlice(values)
		for _, check := range []func(){func() { matcher.ToSatisfyAny(nil) }, func() { matcher.ToSatisfyAll(nil) }, func() { matcher.Not.ToSatisfyAny(nil) }, func() { matcher.Not.ToSatisfyAll(nil) }} {
			assertThrownMessage(t, "predicate must not be nil", check)
		}
	}
	matcher := expectSlice([]int{42})
	yes, no := func(int) bool { return true }, func(int) bool { return false }
	var want TestLocation
	record := func() {
		_, file, line, _ := runtime.Caller(1)
		want = TestLocation{File: file, Line: line, Source: "go"}
	}
	for _, check := range []func(){
		func() { record(); matcher.ToSatisfyAny(no) }, func() { record(); matcher.Not.ToSatisfyAny(yes) },
		func() { record(); matcher.ToSatisfyAll(no) }, func() { record(); matcher.Not.ToSatisfyAll(yes) },
		func() { record(); matcher.ToSatisfyAny(nil) }, func() { record(); matcher.Not.ToSatisfyAny(nil) },
		func() { record(); matcher.ToSatisfyAll(nil) }, func() { record(); matcher.Not.ToSatisfyAll(nil) },
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

func TestSlicePredicateMatchersPropagateCallbackControlFlow(t *testing.T) {
	for _, all := range []bool{false, true} {
		for _, negated := range []bool{false, true} {
			check := func(predicate func(int) bool) {
				matcher := expectSlice([]int{1, 2, 3})
				if all {
					if negated {
						matcher.Not.ToSatisfyAll(predicate)
					} else {
						matcher.ToSatisfyAll(predicate)
					}
				} else if negated {
					matcher.Not.ToSatisfyAny(predicate)
				} else {
					matcher.ToSatisfyAny(predicate)
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
					check(func(int) bool { calls++; panic(__kinmokuseiThrown{err: exception}) })
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
						check(func(int) bool {
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
