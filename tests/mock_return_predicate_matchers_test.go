package vasest

import (
	"bytes"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"
)

func TestMockReturnPredicateMatchersPreserveTypedReturnsAndQueue(t *testing.T) {
	type ID int64
	first := ID(1<<60 + 1)
	invocations, predicates := 0, 0
	mock := NewMockFn[int, ID](func(value int) ID { invocations++; return ID(value) })
	mock.ReturnValueOnce(first)
	matcher := expectMockReturn[ID](mock)
	predicate := func(value ID) bool { predicates++; return value == first }
	matcher.Not.ToHaveReturnedSatisfying(predicate)
	matcher.Not.ToHaveNthReturnedSatisfying(1, predicate)
	matcher.Not.ToHaveLastReturnedSatisfying(predicate)
	if predicates != 0 || invocations != 0 {
		t.Fatal("empty history executed callbacks")
	}
	if mock.Invoke(1) != first {
		t.Fatal("matcher consumed queued behavior")
	}
	matcher.ToHaveReturnedSatisfying(predicate)
	matcher.ToHaveNthReturnedSatisfying(1, predicate)
	matcher.ToHaveLastReturnedSatisfying(predicate)
	mock.Invoke(2)
	matcher.ToHaveReturnedSatisfying(predicate)
	matcher.Not.ToHaveNthReturnedSatisfying(2, predicate)
	matcher.Not.ToHaveLastReturnedSatisfying(predicate)
	if predicates != 6 || invocations != 1 {
		t.Fatal(predicates, invocations)
	}
	mock.ClearCalls()
	matcher.Not.ToHaveReturnedSatisfying(predicate)
	matcher.Not.ToHaveNthReturnedSatisfying(1, predicate)
	matcher.Not.ToHaveLastReturnedSatisfying(predicate)
	if predicates != 6 {
		t.Fatal("clear not observed")
	}
	type record struct{ Value int }
	object := &record{42}
	records := NewMockFn[int, *record](func(int) *record { return object })
	records.Invoke(1)
	NewMockReturnExpectation[*record](records).ToHaveLastReturnedSatisfying(func(value *record) bool { value.Value++; return value == object })
	if object.Value != 43 {
		t.Fatal("native reference changed")
	}
	slices := NewMockFn[int, []int](func(int) []int { return nil })
	slices.Invoke(1)
	slices.ReturnValueOnce([]int{})
	slices.Invoke(2)
	expectMockReturn(slices).ToHaveNthReturnedSatisfying(1, func(value []int) bool { return value == nil })
	expectMockReturn(slices).ToHaveLastReturnedSatisfying(func(value []int) bool { return value != nil && len(value) == 0 })
	sentinel := errors.New("returned error")
	returned := NewMockFn[int, error](func(int) error { return sentinel })
	returned.Invoke(1)
	expectMockReturn(returned).ToHaveReturnedSatisfying(func(value error) bool { return value == sentinel })
}

func TestMockReturnPredicateMatchersReadOneSnapshotAndSkipNonreturns(t *testing.T) {
	reads, calls := 0, 0
	results := []MockResult[int]{{Status: "throw", Value: 99}, {Status: "return", Value: 1}, {Status: "incomplete", Value: 99}, {Status: "return", Value: 2}, {Status: "panic", Value: 99}, {Status: "return", Value: 3}}
	source := &matcherReturnSource[int]{func() []MockResult[int] { reads++; return results }}
	matcher := expectMockReturn(source)
	for _, tc := range []struct {
		check     func(func(int) bool)
		matches   bool
		wantCalls int
	}{
		{matcher.ToHaveReturnedSatisfying, true, 2},
		{matcher.Not.ToHaveReturnedSatisfying, false, 3},
		{func(p func(int) bool) { matcher.ToHaveNthReturnedSatisfying(4, p) }, true, 1},
		{func(p func(int) bool) { matcher.Not.ToHaveNthReturnedSatisfying(4, p) }, false, 1},
		{matcher.ToHaveLastReturnedSatisfying, true, 1},
		{matcher.Not.ToHaveLastReturnedSatisfying, false, 1},
	} {
		reads, calls = 0, 0
		tc.check(func(value int) bool {
			calls++
			if value == 99 {
				t.Fatal("nonreturn evaluated")
			}
			return tc.matches && value >= 2
		})
		if reads != 1 || calls != tc.wantCalls {
			t.Fatal("snapshot or predicate repeated", reads, calls, tc.wantCalls)
		}
	}
	reads, calls = 0, 0
	assertThrownMessage(t, "checked 2 returned value(s) across 6 call(s)", func() { matcher.Not.ToHaveReturnedSatisfying(func(value int) bool { calls++; return value == 2 }) })
	if reads != 1 || calls != 2 {
		t.Fatal(reads, calls)
	}
	reads, calls = 0, 0
	assertThrownMessage(t, "checked 3 returned value(s) across 6 call(s)", func() { matcher.ToHaveReturnedSatisfying(func(int) bool { calls++; return false }) })
	if reads != 1 || calls != 3 {
		t.Fatal(reads, calls)
	}
	for _, status := range []string{"throw", "panic", "incomplete", ""} {
		results = []MockResult[int]{{Status: status, Value: 42}}
		calls = 0
		predicate := func(int) bool { calls++; return true }
		matcher.Not.ToHaveReturnedSatisfying(predicate)
		matcher.Not.ToHaveNthReturnedSatisfying(1, predicate)
		matcher.Not.ToHaveLastReturnedSatisfying(predicate)
		assertThrownMessage(t, "got status", func() { matcher.ToHaveNthReturnedSatisfying(1, predicate) })
		assertThrownMessage(t, "got status", func() { matcher.ToHaveLastReturnedSatisfying(predicate) })
		assertThrownMessage(t, "checked 0 returned value(s) across 1 call(s)", func() { matcher.ToHaveReturnedSatisfying(predicate) })
		if calls != 0 {
			t.Fatal("nonreturn invoked predicate", status)
		}
	}
	results = []MockResult[int]{{Status: "return", Value: 0}}
	matcher.ToHaveLastReturnedSatisfying(func(value int) bool { return value == 0 })
	mock := NewMockFn[int, int](func(value int) int { return value })
	mock.Invoke(1)
	mock.Invoke(2)
	visited := []int{}
	expectMockReturn(mock).Not.ToHaveReturnedSatisfying(func(value int) bool {
		visited = append(visited, value)
		mock.ClearCalls()
		mock.Invoke(3)
		return false
	})
	if fmt.Sprint(visited) != "[1 2]" || len(mock.Results()) != 1 || mock.Results()[0].Value != 3 {
		t.Fatal(visited, mock.Results())
	}
}

func TestMockReturnPredicateMatchersValidateBeforeReadingAndSkipMissingCalls(t *testing.T) {
	reads, calls := 0, 0
	matcher := expectMockReturn(&matcherReturnSource[int]{func() []MockResult[int] { reads++; return nil }})
	predicate := func(int) bool { calls++; return true }
	for _, check := range []func(){func() { matcher.ToHaveReturnedSatisfying(nil) }, func() { matcher.Not.ToHaveReturnedSatisfying(nil) },
		func() { matcher.ToHaveNthReturnedSatisfying(1, nil) }, func() { matcher.Not.ToHaveNthReturnedSatisfying(1, nil) },
		func() { matcher.ToHaveLastReturnedSatisfying(nil) }, func() { matcher.Not.ToHaveLastReturnedSatisfying(nil) },
		func() { matcher.ToHaveNthReturnedSatisfying(0, predicate) }, func() { matcher.Not.ToHaveNthReturnedSatisfying(-1, predicate) },
	} {
		assertThrownMessage(t, "must", check)
	}
	if reads != 0 || calls != 0 {
		t.Fatal("invalid arguments evaluated source", reads, calls)
	}
	matcher.Not.ToHaveReturnedSatisfying(predicate)
	matcher.Not.ToHaveNthReturnedSatisfying(1, predicate)
	matcher.Not.ToHaveLastReturnedSatisfying(predicate)
	assertThrownMessage(t, "checked 0 returned value(s) across 0 call(s)", func() { matcher.ToHaveReturnedSatisfying(predicate) })
	assertThrownMessage(t, "got 0 call(s)", func() { matcher.ToHaveNthReturnedSatisfying(1, predicate) })
	assertThrownMessage(t, "got 0 call(s)", func() { matcher.ToHaveLastReturnedSatisfying(predicate) })
	if reads != 6 || calls != 0 {
		t.Fatal("missing call evaluated predicate", reads, calls)
	}
	for _, source := range []MockReturnSource[int]{nil, (*MockFn[int, int])(nil)} {
		matcher := expectMockReturn[int](source)
		for _, check := range []func(){func() { matcher.ToHaveReturnedSatisfying(predicate) }, func() { matcher.Not.ToHaveReturnedSatisfying(predicate) },
			func() { matcher.ToHaveNthReturnedSatisfying(1, predicate) }, func() { matcher.Not.ToHaveNthReturnedSatisfying(1, predicate) },
			func() { matcher.ToHaveLastReturnedSatisfying(predicate) }, func() { matcher.Not.ToHaveLastReturnedSatisfying(predicate) },
		} {
			assertThrownMessage(t, "mock return source must not be nil", check)
		}
	}
	if calls != 0 {
		t.Fatal("nil source evaluated predicate")
	}
}

func TestMockReturnPredicateMatchersRetainInvocationOrderAndPendingStates(t *testing.T) {
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	mock := NewMockFn[int, int](func(value int) int {
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
		return value
	})
	go func() { defer close(done); mock.Invoke(1) }()
	<-entered
	matcher := expectMockReturn(mock)
	unreachable := func(int) bool { t.Fatal("nonreturn predicate evaluated"); return true }
	matcher.Not.ToHaveReturnedSatisfying(unreachable)
	matcher.Not.ToHaveLastReturnedSatisfying(unreachable)
	assertThrownMessage(t, `status "incomplete"`, func() { matcher.ToHaveLastReturnedSatisfying(unreachable) })
	assertThrownMessage(t, "throw", func() { mock.Invoke(2) })
	matcher.Not.ToHaveNthReturnedSatisfying(2, unreachable)
	close(release)
	<-done
	matcher.ToHaveNthReturnedSatisfying(1, func(value int) bool { return value == 1 })
	matcher.Not.ToHaveLastReturnedSatisfying(unreachable)
	assertThrownMessage(t, `status "throw"`, func() { matcher.ToHaveLastReturnedSatisfying(unreachable) })
	func() {
		defer func() {
			if recover() != "native panic" {
				t.Fatal("panic changed")
			}
		}()
		mock.Invoke(3)
	}()
	matcher.Not.ToHaveLastReturnedSatisfying(unreachable)
	assertThrownMessage(t, `status "panic"`, func() { matcher.ToHaveLastReturnedSatisfying(unreachable) })
	mock.Invoke(4)
	matcher.ToHaveLastReturnedSatisfying(func(value int) bool { return value == 4 })
	matcher.ToHaveReturnedSatisfying(func(value int) bool { return value == 1 })
}

func TestMockReturnPredicateMatchersPreserveCallbackAndSourceControlFlow(t *testing.T) {
	for _, kind := range []string{"any", "nth", "last"} {
		for _, negated := range []bool{false, true} {
			check := func(source MockReturnSource[int], predicate func(int) bool) {
				matcher := expectMockReturn(source)
				switch kind {
				case "any":
					if negated {
						matcher.Not.ToHaveReturnedSatisfying(predicate)
					} else {
						matcher.ToHaveReturnedSatisfying(predicate)
					}
				case "nth":
					if negated {
						matcher.Not.ToHaveNthReturnedSatisfying(1, predicate)
					} else {
						matcher.ToHaveNthReturnedSatisfying(1, predicate)
					}
				case "last":
					if negated {
						matcher.Not.ToHaveLastReturnedSatisfying(predicate)
					} else {
						matcher.ToHaveLastReturnedSatisfying(predicate)
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
						source := &matcherReturnSource[int]{func() []MockResult[int] {
							reads++
							if fromSource {
								panic(__kinmokuseiThrown{err: exception})
							}
							return []MockResult[int]{{Status: "return", Value: 42}}
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
							source := &matcherReturnSource[int]{func() []MockResult[int] {
								if fromSource {
									raise()
								}
								return []MockResult[int]{{Status: "return", Value: 42}}
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

func TestMockReturnPredicateMatchersIdentifyEveryCaller(t *testing.T) {
	matcher := expectMockReturn(&matcherReturnSource[int]{func() []MockResult[int] { return []MockResult[int]{{Status: "return", Value: 42}} }})
	yes, no := func(int) bool { return true }, func(int) bool { return false }
	var want TestLocation
	record := func() {
		_, file, line, _ := runtime.Caller(1)
		want = TestLocation{File: file, Line: line, Source: "go"}
	}
	for _, check := range []func(){
		func() { record(); matcher.ToHaveReturnedSatisfying(no) }, func() { record(); matcher.Not.ToHaveReturnedSatisfying(yes) },
		func() { record(); matcher.ToHaveNthReturnedSatisfying(1, no) }, func() { record(); matcher.Not.ToHaveNthReturnedSatisfying(1, yes) },
		func() { record(); matcher.ToHaveLastReturnedSatisfying(no) }, func() { record(); matcher.Not.ToHaveLastReturnedSatisfying(yes) },
		func() { record(); matcher.ToHaveReturnedSatisfying(nil) }, func() { record(); matcher.Not.ToHaveReturnedSatisfying(nil) },
		func() { record(); matcher.ToHaveNthReturnedSatisfying(1, nil) }, func() { record(); matcher.Not.ToHaveNthReturnedSatisfying(1, nil) },
		func() { record(); matcher.ToHaveLastReturnedSatisfying(nil) }, func() { record(); matcher.Not.ToHaveLastReturnedSatisfying(nil) },
		func() { record(); matcher.ToHaveNthReturnedSatisfying(0, yes) }, func() { record(); matcher.Not.ToHaveNthReturnedSatisfying(0, yes) },
		func() { record(); matcher.ToHaveNthReturnedSatisfying(2, yes) },
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

func TestMockReturnPredicateMatchersConcurrentInvokeAndClear(t *testing.T) {
	mock := NewMockFn[int, int](func(value int) int { return value })
	matcher := expectMockReturn(mock)
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
		matcher.Not.ToHaveReturnedSatisfying(no)
		matcher.Not.ToHaveNthReturnedSatisfying(1, no)
		matcher.Not.ToHaveLastReturnedSatisfying(no)
	}
	<-done
}
