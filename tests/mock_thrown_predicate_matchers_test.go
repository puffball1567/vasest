package vasest

import (
	"bytes"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"
)

type predicateOnlyError struct{ value int }

func (*predicateOnlyError) Error() string { panic("unexpected Error call") }

func TestMockThrownPredicateMatchersPreserveErrorsQueuesAndLiveHistory(t *testing.T) {
	original := &predicateOnlyError{42}
	invoked, calls := 0, 0
	mock := NewMockAction[int](func(int) { invoked++ })
	mock.ImplementOnce(func(int) { panic(__kinmokuseiThrown{err: original}) })
	matcher := NewMockOutcomeExpectation(mock)
	predicate := func(value error) bool { calls++; return value == original }
	matcher.Not.ToHaveThrownSatisfying(predicate)
	matcher.Not.ToHaveNthThrownSatisfying(1, predicate)
	matcher.Not.ToHaveLastThrownSatisfying(predicate)
	if invoked != 0 || calls != 0 {
		t.Fatal("empty history executed callbacks")
	}
	func() {
		defer func() {
			thrown, ok := recover().(__kinmokuseiThrown)
			if !ok || thrown.err != original {
				t.Fatal("queued throw identity changed")
			}
		}()
		mock.Invoke(1)
	}()
	matcher.ToHaveThrownSatisfying(predicate)
	matcher.ToHaveNthThrownSatisfying(1, predicate)
	matcher.ToHaveLastThrownSatisfying(func(value error) bool { calls++; value.(*predicateOnlyError).value++; return value == original })
	mock.Invoke(2)
	matcher.ToHaveThrownSatisfying(predicate)
	matcher.Not.ToHaveNthThrownSatisfying(2, predicate)
	matcher.Not.ToHaveLastThrownSatisfying(predicate)
	if invoked != 1 || calls != 4 || original.value != 43 {
		t.Fatal(invoked, calls, original.value)
	}
	mock.ClearCalls()
	matcher.Not.ToHaveThrownSatisfying(predicate)
	if calls != 4 {
		t.Fatal("clear not observed")
	}
	returned := NewMockFn[int, error](func(int) error { return original })
	returned.Invoke(1)
	expectMockOutcome(returned).Not.ToHaveThrownSatisfying(func(error) bool { t.Fatal("returned error treated as throw"); return true })
	returned.ImplementOnce(func(int) error { panic(__kinmokuseiThrown{err: nil}) })
	expectFunction(func() { returned.Invoke(2) }).ToThrow()
	expectMockOutcome(returned).ToHaveNthThrownSatisfying(2, func(value error) bool { return value == nil })
	var typedNil *predicateOnlyError
	returned.ImplementOnce(func(int) error { panic(__kinmokuseiThrown{err: typedNil}) })
	expectFunction(func() { returned.Invoke(3) }).ToThrow()
	expectMockOutcome(returned).ToHaveLastThrownSatisfying(func(value error) bool { return value != nil && value == typedNil })
}

func TestMockThrownPredicateMatchersSnapshotOrderAndShortCircuit(t *testing.T) {
	first, second, third := errors.New("first"), errors.New("second"), errors.New("third")
	reads, calls := 0, 0
	results := []MockOutcomeResult{{Status: "return", Exception: first}, {Status: "throw", Exception: first}, {Status: "incomplete", Exception: first}, {Status: "throw", Exception: second}, {Status: "panic", Exception: first}, {Status: "throw", Exception: third}}
	matcher := expectMockOutcome(&matcherOutcomeSource{func() []MockOutcomeResult { reads++; return results }})
	for _, tc := range []struct {
		check   func(func(error) bool)
		matches bool
		count   int
	}{
		{matcher.ToHaveThrownSatisfying, true, 2}, {matcher.Not.ToHaveThrownSatisfying, false, 3},
		{func(p func(error) bool) { matcher.ToHaveNthThrownSatisfying(4, p) }, true, 1},
		{func(p func(error) bool) { matcher.Not.ToHaveNthThrownSatisfying(4, p) }, false, 1},
		{matcher.ToHaveLastThrownSatisfying, true, 1}, {matcher.Not.ToHaveLastThrownSatisfying, false, 1},
	} {
		reads, calls = 0, 0
		tc.check(func(value error) bool { calls++; return tc.matches && value != first })
		if reads != 1 || calls != tc.count {
			t.Fatal("snapshot or predicate repeated", reads, calls, tc.count)
		}
	}
	reads, calls = 0, 0
	assertThrownMessage(t, "checked 2 thrown error(s) across 6 call(s)", func() { matcher.Not.ToHaveThrownSatisfying(func(value error) bool { calls++; return value == second }) })
	if reads != 1 || calls != 2 {
		t.Fatal(reads, calls)
	}
	for _, status := range []string{"return", "incomplete", "panic", ""} {
		results = []MockOutcomeResult{{Status: status, Exception: first}}
		predicate := func(error) bool { t.Fatal("nonthrow callback invoked", status); return true }
		matcher.Not.ToHaveThrownSatisfying(predicate)
		matcher.Not.ToHaveNthThrownSatisfying(1, predicate)
		matcher.Not.ToHaveLastThrownSatisfying(predicate)
		assertThrownMessage(t, "checked 0 thrown error(s) across 1 call(s)", func() { matcher.ToHaveThrownSatisfying(predicate) })
		assertThrownMessage(t, "got status", func() { matcher.ToHaveNthThrownSatisfying(1, predicate) })
		assertThrownMessage(t, "got status", func() { matcher.ToHaveLastThrownSatisfying(predicate) })
	}
	mock := NewMockAction[int](func(value int) {
		if value == 1 {
			panic(__kinmokuseiThrown{err: first})
		}
		panic(__kinmokuseiThrown{err: second})
	})
	expectFunction(func() { mock.Invoke(1) }).ToThrow()
	expectFunction(func() { mock.Invoke(2) }).ToThrow()
	visited := []error{}
	expectMockOutcome(mock).Not.ToHaveThrownSatisfying(func(value error) bool {
		visited = append(visited, value)
		mock.ClearCalls()
		expectFunction(func() { mock.Invoke(3) }).ToThrow()
		return false
	})
	if len(visited) != 2 || visited[0] != first || visited[1] != second || len(mock.Outcomes()) != 1 {
		t.Fatal("selected snapshot changed")
	}
}

func TestMockThrownPredicateMatchersValidationAndMissingCalls(t *testing.T) {
	reads, calls := 0, 0
	matcher := expectMockOutcome(&matcherOutcomeSource{func() []MockOutcomeResult { reads++; return nil }})
	predicate := func(error) bool { calls++; return true }
	for _, check := range []func(){
		func() { matcher.ToHaveThrownSatisfying(nil) }, func() { matcher.Not.ToHaveThrownSatisfying(nil) },
		func() { matcher.ToHaveNthThrownSatisfying(1, nil) }, func() { matcher.Not.ToHaveNthThrownSatisfying(1, nil) },
		func() { matcher.ToHaveLastThrownSatisfying(nil) }, func() { matcher.Not.ToHaveLastThrownSatisfying(nil) },
		func() { matcher.ToHaveNthThrownSatisfying(0, predicate) }, func() { matcher.Not.ToHaveNthThrownSatisfying(-1, predicate) },
	} {
		assertThrownMessage(t, "must", check)
	}
	if reads != 0 || calls != 0 {
		t.Fatal("invalid arguments read source")
	}
	matcher.Not.ToHaveThrownSatisfying(predicate)
	matcher.Not.ToHaveNthThrownSatisfying(1, predicate)
	matcher.Not.ToHaveLastThrownSatisfying(predicate)
	assertThrownMessage(t, "checked 0 thrown error(s) across 0 call(s)", func() { matcher.ToHaveThrownSatisfying(predicate) })
	assertThrownMessage(t, "got 0 call(s)", func() { matcher.ToHaveNthThrownSatisfying(1, predicate) })
	assertThrownMessage(t, "got 0 call(s)", func() { matcher.ToHaveLastThrownSatisfying(predicate) })
	if reads != 6 || calls != 0 {
		t.Fatal("missing call invoked predicate")
	}
	for _, source := range []MockOutcomeSource{nil, (*MockFn[int, int])(nil), (*MockAction[int])(nil)} {
		matcher := expectMockOutcome(source)
		for _, check := range []func(){
			func() { matcher.ToHaveThrownSatisfying(predicate) }, func() { matcher.Not.ToHaveThrownSatisfying(predicate) },
			func() { matcher.ToHaveNthThrownSatisfying(1, predicate) }, func() { matcher.Not.ToHaveNthThrownSatisfying(1, predicate) },
			func() { matcher.ToHaveLastThrownSatisfying(predicate) }, func() { matcher.Not.ToHaveLastThrownSatisfying(predicate) },
		} {
			assertThrownMessage(t, "mock outcome source must not be nil", check)
		}
	}
}

func TestMockThrownPredicateMatchersPendingCallsAndInvocationOrder(t *testing.T) {
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	first, second := errors.New("first"), errors.New("second")
	mock := NewMockAction[int](func(value int) {
		if value == 1 {
			close(entered)
			<-release
			panic(__kinmokuseiThrown{err: first})
		}
		if value == 2 {
			panic(__kinmokuseiThrown{err: second})
		}
		if value == 3 {
			panic("native panic")
		}
	})
	go func() { defer close(done); expectFunction(func() { mock.Invoke(1) }).ToThrow() }()
	<-entered
	matcher := expectMockOutcome(mock)
	unreachable := func(error) bool { t.Fatal("nonthrow callback invoked"); return true }
	matcher.Not.ToHaveThrownSatisfying(unreachable)
	matcher.Not.ToHaveLastThrownSatisfying(unreachable)
	assertThrownMessage(t, `status "incomplete"`, func() { matcher.ToHaveLastThrownSatisfying(unreachable) })
	expectFunction(func() { mock.Invoke(2) }).ToThrow()
	matcher.ToHaveLastThrownSatisfying(func(value error) bool { return value == second })
	close(release)
	<-done
	matcher.ToHaveNthThrownSatisfying(1, func(value error) bool { return value == first })
	matcher.ToHaveLastThrownSatisfying(func(value error) bool { return value == second })
	func() {
		defer func() {
			if recover() != "native panic" {
				t.Fatal("panic changed")
			}
		}()
		mock.Invoke(3)
	}()
	matcher.Not.ToHaveLastThrownSatisfying(unreachable)
	assertThrownMessage(t, `status "panic"`, func() { matcher.ToHaveLastThrownSatisfying(unreachable) })
	mock.Invoke(4)
	matcher.Not.ToHaveLastThrownSatisfying(unreachable)
	matcher.ToHaveThrownSatisfying(func(value error) bool { return value == first })
}

func TestMockThrownPredicateMatchersPreserveCallbackAndSourceControlFlow(t *testing.T) {
	for _, kind := range []string{"any", "nth", "last"} {
		for _, negated := range []bool{false, true} {
			check := func(source MockOutcomeSource, predicate func(error) bool) {
				matcher := expectMockOutcome(source)
				switch kind {
				case "any":
					if negated {
						matcher.Not.ToHaveThrownSatisfying(predicate)
					} else {
						matcher.ToHaveThrownSatisfying(predicate)
					}
				case "nth":
					if negated {
						matcher.Not.ToHaveNthThrownSatisfying(1, predicate)
					} else {
						matcher.ToHaveNthThrownSatisfying(1, predicate)
					}
				case "last":
					if negated {
						matcher.Not.ToHaveLastThrownSatisfying(predicate)
					} else {
						matcher.ToHaveLastThrownSatisfying(predicate)
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
						source := &matcherOutcomeSource{func() []MockOutcomeResult {
							reads++
							if fromSource {
								panic(__kinmokuseiThrown{err: exception})
							}
							return []MockOutcomeResult{{Status: "throw", Exception: errors.New("original")}}
						}}
						check(source, func(error) bool { calls++; panic(__kinmokuseiThrown{err: exception}) })
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
							source := &matcherOutcomeSource{func() []MockOutcomeResult {
								if fromSource {
									raise()
								}
								return []MockOutcomeResult{{Status: "throw", Exception: errors.New("original")}}
							}}
							check(source, func(error) bool { raise(); return true })
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

func TestMockThrownPredicateMatchersIdentifyEveryCaller(t *testing.T) {
	matcher := expectMockOutcome(&matcherOutcomeSource{func() []MockOutcomeResult {
		return []MockOutcomeResult{{Status: "throw", Exception: errors.New("original")}}
	}})
	yes, no := func(error) bool { return true }, func(error) bool { return false }
	var want TestLocation
	record := func() {
		_, file, line, _ := runtime.Caller(1)
		want = TestLocation{File: file, Line: line, Source: "go"}
	}
	for _, check := range []func(){
		func() { record(); matcher.ToHaveThrownSatisfying(no) }, func() { record(); matcher.Not.ToHaveThrownSatisfying(yes) },
		func() { record(); matcher.ToHaveNthThrownSatisfying(1, no) }, func() { record(); matcher.Not.ToHaveNthThrownSatisfying(1, yes) },
		func() { record(); matcher.ToHaveLastThrownSatisfying(no) }, func() { record(); matcher.Not.ToHaveLastThrownSatisfying(yes) },
		func() { record(); matcher.ToHaveThrownSatisfying(nil) }, func() { record(); matcher.Not.ToHaveThrownSatisfying(nil) },
		func() { record(); matcher.ToHaveNthThrownSatisfying(1, nil) }, func() { record(); matcher.Not.ToHaveNthThrownSatisfying(1, nil) },
		func() { record(); matcher.ToHaveLastThrownSatisfying(nil) }, func() { record(); matcher.Not.ToHaveLastThrownSatisfying(nil) },
		func() { record(); matcher.ToHaveNthThrownSatisfying(0, yes) }, func() { record(); matcher.Not.ToHaveNthThrownSatisfying(0, yes) },
		func() { record(); matcher.ToHaveNthThrownSatisfying(2, yes) },
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

func TestMockThrownPredicateMatchersConcurrentInvokeAndClear(t *testing.T) {
	mock := NewMockAction[int](func(int) { panic(__kinmokuseiThrown{err: nil}) })
	matcher := expectMockOutcome(mock)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			expectFunction(func() { mock.Invoke(i) }).ToThrow()
			if i%20 == 0 {
				mock.ClearCalls()
			}
		}
	}()
	no := func(error) bool { return false }
	for i := 0; i < 200; i++ {
		matcher.Not.ToHaveThrownSatisfying(no)
		matcher.Not.ToHaveNthThrownSatisfying(1, no)
		matcher.Not.ToHaveLastThrownSatisfying(no)
	}
	<-done
}
