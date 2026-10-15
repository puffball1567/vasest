package vasest

import (
	"bytes"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"
)

func TestMockPredicateMatchersPreserveTypedLiveCallsAndQueuedBehavior(t *testing.T) {
	type ID int64
	first, second := ID(1<<60+1), ID(1<<60+2)
	invocations, predicates := 0, 0
	mock := NewMockFn[ID, string](func(ID) string { invocations++; return "default" })
	mock.ReturnValueOnce("queued")
	matcher := expectMock[ID](mock)
	predicate := func(value ID) bool { predicates++; return value == first }
	matcher.Not.ToHaveBeenCalledSatisfying(predicate)
	matcher.Not.ToHaveBeenNthCalledSatisfying(1, predicate)
	matcher.Not.ToHaveBeenLastCalledSatisfying(predicate)
	if predicates != 0 || invocations != 0 {
		t.Fatal("empty history executed callbacks")
	}
	if mock.Invoke(first) != "queued" {
		t.Fatal("matcher consumed queued behavior")
	}
	matcher.ToHaveBeenCalledSatisfying(predicate)
	matcher.ToHaveBeenNthCalledSatisfying(1, predicate)
	matcher.ToHaveBeenLastCalledSatisfying(predicate)
	mock.Invoke(second)
	matcher.ToHaveBeenCalledSatisfying(predicate)
	matcher.Not.ToHaveBeenNthCalledSatisfying(2, predicate)
	matcher.Not.ToHaveBeenLastCalledSatisfying(predicate)
	if predicates != 6 || invocations != 1 {
		t.Fatal(predicates, invocations)
	}
	mock.ClearCalls()
	matcher.Not.ToHaveBeenCalledSatisfying(predicate)
	matcher.Not.ToHaveBeenNthCalledSatisfying(1, predicate)
	matcher.Not.ToHaveBeenLastCalledSatisfying(predicate)
	if predicates != 6 {
		t.Fatal("clear not observed")
	}
	action := NewMockAction[ID](func(ID) {})
	action.Invoke(second)
	NewMockExpectation[ID](action).ToHaveBeenLastCalledSatisfying(func(value ID) bool { return value == second })
	type record struct{ Value int }
	object := &record{42}
	records := NewMockAction[*record](func(*record) {})
	records.Invoke(nil)
	records.Invoke(object)
	expectMock(records).ToHaveBeenCalledSatisfying(func(value *record) bool {
		if value == nil {
			return false
		}
		value.Value++
		return value == object
	})
	if object.Value != 43 {
		t.Fatal("native reference changed")
	}
	slices := NewMockAction[[]int](func([]int) {})
	slices.Invoke(nil)
	slices.Invoke([]int{})
	expectMock(slices).ToHaveBeenNthCalledSatisfying(1, func(value []int) bool { return value == nil })
	expectMock(slices).ToHaveBeenLastCalledSatisfying(func(value []int) bool { return value != nil && len(value) == 0 })
}

func TestMockPredicateMatchersReadOneSnapshotAndShortCircuit(t *testing.T) {
	reads, calls := 0, 0
	source := &matcherCallSource[int]{func() []int { reads++; return []int{1, 2, 3} }}
	matcher := expectMock(source)
	for _, tc := range []struct {
		check     func(func(int) bool)
		matches   bool
		wantCalls int
	}{
		{matcher.ToHaveBeenCalledSatisfying, true, 2},
		{matcher.Not.ToHaveBeenCalledSatisfying, false, 3},
		{func(p func(int) bool) { matcher.ToHaveBeenNthCalledSatisfying(2, p) }, true, 1},
		{func(p func(int) bool) { matcher.Not.ToHaveBeenNthCalledSatisfying(2, p) }, false, 1},
		{matcher.ToHaveBeenLastCalledSatisfying, true, 1},
		{matcher.Not.ToHaveBeenLastCalledSatisfying, false, 1},
	} {
		reads, calls = 0, 0
		tc.check(func(value int) bool { calls++; return tc.matches && value >= 2 })
		if reads != 1 || calls != tc.wantCalls {
			t.Fatal("snapshot or predicate repeated", reads, calls, tc.wantCalls)
		}
	}
	reads, calls = 0, 0
	assertThrownMessage(t, "checked 2 of 3 call(s)", func() { matcher.Not.ToHaveBeenCalledSatisfying(func(value int) bool { calls++; return value == 2 }) })
	if reads != 1 || calls != 2 {
		t.Fatal(reads, calls)
	}
	reads, calls = 0, 0
	assertThrownMessage(t, "checked 3 of 3 call(s)", func() { matcher.ToHaveBeenCalledSatisfying(func(int) bool { calls++; return false }) })
	if reads != 1 || calls != 3 {
		t.Fatal(reads, calls)
	}
	// A callback can record and clear calls; its own check still uses one snapshot.
	mock := NewMockAction[int](func(int) {})
	mock.Invoke(1)
	mock.Invoke(2)
	visited := []int{}
	expectMock(mock).Not.ToHaveBeenCalledSatisfying(func(value int) bool {
		visited = append(visited, value)
		mock.ClearCalls()
		mock.Invoke(3)
		return false
	})
	if fmt.Sprint(visited) != "[1 2]" || fmt.Sprint(mock.Calls()) != "[3]" {
		t.Fatal(visited, mock.Calls())
	}
}

func TestMockPredicateMatchersValidateBeforeReadingAndSkipMissingCalls(t *testing.T) {
	reads, calls := 0, 0
	matcher := expectMock(&matcherCallSource[int]{func() []int { reads++; return nil }})
	predicate := func(int) bool { calls++; return true }
	for _, check := range []func(){func() { matcher.ToHaveBeenCalledSatisfying(nil) }, func() { matcher.Not.ToHaveBeenCalledSatisfying(nil) },
		func() { matcher.ToHaveBeenNthCalledSatisfying(1, nil) }, func() { matcher.Not.ToHaveBeenNthCalledSatisfying(1, nil) },
		func() { matcher.ToHaveBeenLastCalledSatisfying(nil) }, func() { matcher.Not.ToHaveBeenLastCalledSatisfying(nil) },
		func() { matcher.ToHaveBeenNthCalledSatisfying(0, predicate) }, func() { matcher.Not.ToHaveBeenNthCalledSatisfying(-1, predicate) },
	} {
		assertThrownMessage(t, "must", check)
	}
	if reads != 0 || calls != 0 {
		t.Fatal("invalid arguments evaluated source", reads, calls)
	}
	matcher.Not.ToHaveBeenCalledSatisfying(predicate)
	matcher.Not.ToHaveBeenNthCalledSatisfying(1, predicate)
	matcher.Not.ToHaveBeenLastCalledSatisfying(predicate)
	assertThrownMessage(t, "checked 0 of 0 call(s)", func() { matcher.ToHaveBeenCalledSatisfying(predicate) })
	assertThrownMessage(t, "got 0 call(s)", func() { matcher.ToHaveBeenNthCalledSatisfying(1, predicate) })
	assertThrownMessage(t, "got 0 call(s)", func() { matcher.ToHaveBeenLastCalledSatisfying(predicate) })
	if reads != 6 || calls != 0 {
		t.Fatal("missing call evaluated predicate", reads, calls)
	}
	for _, source := range []MockCallSource[int]{nil, (*MockFn[int, int])(nil), (*MockAction[int])(nil)} {
		matcher := expectMock[int](source)
		for _, check := range []func(){func() { matcher.ToHaveBeenCalledSatisfying(predicate) }, func() { matcher.Not.ToHaveBeenCalledSatisfying(predicate) },
			func() { matcher.ToHaveBeenNthCalledSatisfying(1, predicate) }, func() { matcher.Not.ToHaveBeenNthCalledSatisfying(1, predicate) },
			func() { matcher.ToHaveBeenLastCalledSatisfying(predicate) }, func() { matcher.Not.ToHaveBeenLastCalledSatisfying(predicate) },
		} {
			assertThrownMessage(t, "mock call source must not be nil", check)
		}
	}
	if calls != 0 {
		t.Fatal("nil source evaluated predicate")
	}
}

func TestMockPredicateMatchersRetainInvocationOrderAndAllCompletionStates(t *testing.T) {
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	action := NewMockAction[int](func(value int) {
		if value == 1 {
			close(entered)
			<-release
		}
		if value == 2 {
			panic(__kinmokuseiThrown{err: errors.New("throw")})
		}
		if value == 3 {
			panic("native panic")
		}
	})
	go func() { defer close(done); action.Invoke(1) }()
	<-entered
	matcher := expectMock(action)
	matcher.ToHaveBeenLastCalledSatisfying(func(value int) bool { return value == 1 })
	assertThrownMessage(t, "throw", func() { action.Invoke(2) })
	matcher.ToHaveBeenNthCalledSatisfying(2, func(value int) bool { return value == 2 })
	close(release)
	<-done
	matcher.ToHaveBeenLastCalledSatisfying(func(value int) bool { return value == 2 })
	func() {
		defer func() {
			if recover() != "native panic" {
				t.Fatal("panic changed")
			}
		}()
		action.Invoke(3)
	}()
	matcher.ToHaveBeenLastCalledSatisfying(func(value int) bool { return value == 3 })
	matcher.ToHaveBeenCalledSatisfying(func(value int) bool { return value == 1 })
}

func TestMockPredicateMatchersPreserveCallbackAndSourceControlFlow(t *testing.T) {
	for _, kind := range []string{"any", "nth", "last"} {
		for _, negated := range []bool{false, true} {
			check := func(source MockCallSource[int], predicate func(int) bool) {
				matcher := expectMock(source)
				switch kind {
				case "any":
					if negated {
						matcher.Not.ToHaveBeenCalledSatisfying(predicate)
					} else {
						matcher.ToHaveBeenCalledSatisfying(predicate)
					}
				case "nth":
					if negated {
						matcher.Not.ToHaveBeenNthCalledSatisfying(1, predicate)
					} else {
						matcher.ToHaveBeenNthCalledSatisfying(1, predicate)
					}
				case "last":
					if negated {
						matcher.Not.ToHaveBeenLastCalledSatisfying(predicate)
					} else {
						matcher.ToHaveBeenLastCalledSatisfying(predicate)
					}
				}
			}
			for _, fromSource := range []bool{false, true} {
				for _, exception := range []error{nil, errors.New("callback failed"), NewFailureError(errors.New("located"), TestLocation{File: "domain.km", Line: 42, Source: "explicit"})} {
					func() {
						reads, calls := 0, 0
						defer func() {
							thrown, ok := recover().(__kinmokuseiThrown)
							wantCalls := 1
							if fromSource {
								wantCalls = 0
							}
							if !ok || thrown.err != exception || reads != 1 || calls != wantCalls {
								t.Fatal("exception identity changed", thrown, exception, reads, calls)
							}
						}()
						source := &matcherCallSource[int]{func() []int {
							reads++
							if fromSource {
								panic(__kinmokuseiThrown{err: exception})
							}
							return []int{42}
						}}
						check(source, func(int) bool { calls++; panic(__kinmokuseiThrown{err: exception}) })
					}()
				}
				for _, mode := range []string{"throw", "skip", "panic"} {
					func() {
						var events []string
						if mode == "panic" {
							defer func() {
								if recover() != "mock predicate panic" || fmt.Sprint(events) != "[afterEach cleanup afterAll]" {
									t.Fatal("panic cleanup", events)
								}
							}()
						}
						raise := func() {
							switch mode {
							case "panic":
								panic("mock predicate panic")
							case "skip":
								skipTest("mock predicate unavailable")
							case "throw":
								withTestLocation("domain.km", 42, func() { panic(__kinmokuseiThrown{err: errors.New("located")}) })
							}
						}
						suite := NewSuite("mock predicate cleanup")
						suite.AfterEach(func() { events = append(events, "afterEach") })
						suite.AfterAll(func() { events = append(events, "afterAll") })
						suite.TestWithContext("case", func(context *TestContext) {
							context.Cleanup(func() { events = append(events, "cleanup") })
							source := &matcherCallSource[int]{func() []int {
								if fromSource {
									raise()
								}
								return []int{42}
							}}
							check(source, func(int) bool { raise(); return true })
							t.Fatal("control flow swallowed")
						})
						summary := suite.RunTo(&bytes.Buffer{})
						if mode == "panic" {
							t.Fatal("native panic swallowed")
						}
						if fmt.Sprint(events) != "[afterEach cleanup afterAll]" {
							t.Fatal(summary, events)
						}
						if mode == "skip" {
							if summary.Skipped != 1 || summary.Failed != 0 || !strings.Contains(summary.Results[0].Message, "mock predicate unavailable") {
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
}

func TestMockPredicateMatchersIdentifyEveryCaller(t *testing.T) {
	matcher := expectMock(&matcherCallSource[int]{func() []int { return []int{42} }})
	yes, no := func(int) bool { return true }, func(int) bool { return false }
	var want TestLocation
	record := func() {
		_, file, line, _ := runtime.Caller(1)
		want = TestLocation{File: file, Line: line, Source: "go"}
	}
	for _, check := range []func(){
		func() { record(); matcher.ToHaveBeenCalledSatisfying(no) }, func() { record(); matcher.Not.ToHaveBeenCalledSatisfying(yes) },
		func() { record(); matcher.ToHaveBeenNthCalledSatisfying(1, no) }, func() { record(); matcher.Not.ToHaveBeenNthCalledSatisfying(1, yes) },
		func() { record(); matcher.ToHaveBeenLastCalledSatisfying(no) }, func() { record(); matcher.Not.ToHaveBeenLastCalledSatisfying(yes) },
		func() { record(); matcher.ToHaveBeenCalledSatisfying(nil) }, func() { record(); matcher.Not.ToHaveBeenCalledSatisfying(nil) },
		func() { record(); matcher.ToHaveBeenNthCalledSatisfying(1, nil) }, func() { record(); matcher.Not.ToHaveBeenNthCalledSatisfying(1, nil) },
		func() { record(); matcher.ToHaveBeenLastCalledSatisfying(nil) }, func() { record(); matcher.Not.ToHaveBeenLastCalledSatisfying(nil) },
		func() { record(); matcher.ToHaveBeenNthCalledSatisfying(0, yes) }, func() { record(); matcher.Not.ToHaveBeenNthCalledSatisfying(0, yes) },
		func() { record(); matcher.ToHaveBeenNthCalledSatisfying(2, yes) },
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

func TestMockPredicateMatchersConcurrentInvokeAndClear(t *testing.T) {
	mock := NewMockAction[int](func(int) {})
	matcher := expectMock(mock)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			mock.Invoke(i)
			if i%20 == 0 {
				mock.ClearCalls()
			}
		}
	}()
	no := func(int) bool { return false }
	for i := 0; i < 200; i++ {
		matcher.Not.ToHaveBeenCalledSatisfying(no)
		matcher.Not.ToHaveBeenNthCalledSatisfying(1, no)
		matcher.Not.ToHaveBeenLastCalledSatisfying(no)
	}
	<-done
}
