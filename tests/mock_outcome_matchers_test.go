package vasest

import (
	"bytes"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"testing"
)

type matcherOutcomeSource struct{ snapshot func() []MockOutcomeResult }

func (source *matcherOutcomeSource) Outcomes() []MockOutcomeResult { return source.snapshot() }

type outcomeMessagePanic struct{}

func (*outcomeMessagePanic) Error() string { panic("outcome message panic") }

func TestMockOutcomeMatchersShareFunctionAndActionSemantics(t *testing.T) {
	invoked := 0
	action := NewMockAction[string](func(string) { invoked++ })
	exception := errors.New("金木犀 delivery failed")
	action.ImplementOnce(func(string) { panic(__kinmokuseiThrown{err: exception}) })
	matcher := expectMockOutcome(action)
	matcher.Not.ToHaveReturned()
	matcher.Not.ToHaveThrown()
	matcher.ToHaveReturnedTimes(0)
	matcher.ToHaveThrownTimes(0)
	matcher.Not.ToHaveNthThrownMessage(1, "delivery")
	matcher.Not.ToHaveLastThrownMessage("delivery")
	if invoked != 0 {
		t.Fatal("matcher invoked implementation")
	}
	assertThrownMessage(t, "delivery failed", func() { action.Invoke("first") })
	matcher.ToHaveThrown()
	matcher.ToHaveThrownTimes(1)
	matcher.ToHaveNthThrownMessage(1, "金木犀")
	matcher.ToHaveLastThrownMessage("delivery failed")
	matcher.Not.ToHaveReturned()
	matcher.Not.ToHaveThrownTimes(0)
	matcher.Not.ToHaveNthThrownMessage(1, "missing")
	matcher.Not.ToHaveLastThrownMessage("missing")
	action.Invoke("second")
	matcher.ToHaveReturned()
	matcher.ToHaveReturnedTimes(1)
	matcher.Not.ToHaveReturnedTimes(0)
	matcher.Not.ToHaveNthThrownMessage(2, "delivery")
	matcher.Not.ToHaveLastThrownMessage("delivery")
	matcher.ToHaveNthThrownMessage(1, "delivery")
	if invoked != 1 {
		t.Fatal("matcher changed queued behavior", invoked)
	}
	snapshot := action.Outcomes()
	if snapshot[0].Exception != exception {
		t.Fatal("outcome projection changed the exception")
	}
	snapshot[0].Status = "return"
	snapshot[0].Exception = nil
	matcher.ToHaveThrownTimes(1)
	action.ClearCalls()
	matcher.Not.ToHaveReturned()
	matcher.Not.ToHaveThrown()
	mock := NewMockFn[int, error](func(int) error { return errors.New("returned error value") })
	mock.Invoke(1)
	functionMatcher := NewMockOutcomeExpectation(mock)
	functionMatcher.ToHaveReturnedTimes(1)
	functionMatcher.Not.ToHaveThrown()
	if mock.Outcomes()[0].Exception != nil {
		t.Fatal("returned error value was projected as a thrown exception")
	}
	mock.ImplementOnce(func(int) error { panic(__kinmokuseiThrown{err: nil}) })
	func() {
		defer func() {
			thrown, ok := recover().(__kinmokuseiThrown)
			if !ok || thrown.err != nil {
				t.Fatal("nil throw changed")
			}
		}()
		mock.Invoke(2)
	}()
	functionMatcher.ToHaveThrownTimes(1)
	functionMatcher.Not.ToHaveLastThrownMessage("anything")
	assertThrownMessage(t, `got ""`, func() { functionMatcher.ToHaveLastThrownMessage("anything") })
	if mock.Outcomes()[1].Exception != nil {
		t.Fatal("nil exception changed")
	}
}

func TestMockOutcomeMatchersTrackPendingCompletionOrderAndPanics(t *testing.T) {
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	action := NewMockAction[int](func(value int) {
		if value == 1 {
			close(entered)
			<-release
		}
		if value == 2 {
			panic(__kinmokuseiThrown{err: errors.New("second throw")})
		}
	})
	matcher := expectMockOutcome(action)
	go func() { defer close(done); action.Invoke(1) }()
	<-entered
	matcher.Not.ToHaveReturned()
	matcher.Not.ToHaveThrown()
	matcher.Not.ToHaveNthThrownMessage(1, "throw")
	assertThrownMessage(t, `status "incomplete"`, func() { matcher.ToHaveLastThrownMessage("throw") })
	assertThrownMessage(t, "second throw", func() { action.Invoke(2) })
	matcher.ToHaveThrownTimes(1)
	matcher.ToHaveLastThrownMessage("second")
	close(release)
	<-done
	matcher.ToHaveReturnedTimes(1)
	matcher.ToHaveLastThrownMessage("second")
	action.ImplementOnce(func(int) { panic("native action panic") })
	func() {
		defer func() {
			if recover() != "native action panic" {
				t.Fatal("panic changed")
			}
		}()
		action.Invoke(3)
	}()
	matcher.ToHaveThrownTimes(1)
	matcher.ToHaveReturnedTimes(1)
	matcher.Not.ToHaveLastThrownMessage("panic")
	assertThrownMessage(t, `status "panic"`, func() { matcher.ToHaveLastThrownMessage("panic") })
	entered, release, done = make(chan struct{}), make(chan struct{}), make(chan struct{})
	action.Implement(func(int) { close(entered); <-release })
	go func() { defer close(done); action.Invoke(4) }()
	<-entered
	action.ClearCalls()
	close(release)
	<-done
	matcher.ToHaveReturnedTimes(0)
	matcher.ToHaveThrownTimes(0)
}

func TestMockOutcomeMatchersReadOneSnapshotAndValidateBeforeReading(t *testing.T) {
	reads := 0
	var results []MockOutcomeResult
	source := &matcherOutcomeSource{func() []MockOutcomeResult { reads++; return results }}
	matcher := expectMockOutcome(source)
	checks := []func(){matcher.ToHaveReturned, func() { matcher.ToHaveReturnedTimes(1) }, matcher.ToHaveThrown,
		func() { matcher.ToHaveThrownTimes(1) }, func() { matcher.ToHaveNthThrownMessage(1, "failed") }, func() { matcher.ToHaveLastThrownMessage("failed") },
		matcher.Not.ToHaveReturned, func() { matcher.Not.ToHaveReturnedTimes(1) }, matcher.Not.ToHaveThrown,
		func() { matcher.Not.ToHaveThrownTimes(1) }, func() { matcher.Not.ToHaveNthThrownMessage(1, "failed") }, func() { matcher.Not.ToHaveLastThrownMessage("failed") }}
	for i, check := range checks {
		reads, results = 0, nil
		if i < 6 {
			results = []MockOutcomeResult{{Status: "throw", Exception: errors.New("failed")}}
			if i < 2 {
				results[0].Status = "return"
			}
		}
		check()
		if reads != 1 {
			t.Fatal("snapshot read count", reads)
		}
	}
	reads = 0
	for _, attempt := range []func(){func() { matcher.ToHaveReturnedTimes(-1) }, func() { matcher.Not.ToHaveReturnedTimes(-1) },
		func() { matcher.ToHaveThrownTimes(-1) }, func() { matcher.Not.ToHaveThrownTimes(-1) },
		func() { matcher.ToHaveNthThrownMessage(0, "failed") }, func() { matcher.Not.ToHaveNthThrownMessage(-1, "failed") },
		func() { matcher.ToHaveNthThrownMessage(1, "") }, func() { matcher.Not.ToHaveNthThrownMessage(1, "") },
		func() { matcher.ToHaveLastThrownMessage("") }, func() { matcher.Not.ToHaveLastThrownMessage("") }} {
		assertThrownMessage(t, "must", attempt)
	}
	if reads != 0 {
		t.Fatal("invalid argument read source", reads)
	}
	for _, source := range []MockOutcomeSource{nil, (*MockFn[int, int])(nil), (*MockAction[int])(nil)} {
		matcher := expectMockOutcome(source)
		for _, attempt := range []func(){matcher.ToHaveReturned, matcher.Not.ToHaveReturned, matcher.ToHaveThrown, matcher.Not.ToHaveThrown,
			func() { matcher.ToHaveReturnedTimes(0) }, func() { matcher.Not.ToHaveReturnedTimes(1) }, func() { matcher.ToHaveThrownTimes(0) }, func() { matcher.Not.ToHaveThrownTimes(1) },
			func() { matcher.ToHaveNthThrownMessage(1, "failed") }, func() { matcher.Not.ToHaveNthThrownMessage(1, "failed") },
			func() { matcher.ToHaveLastThrownMessage("failed") }, func() { matcher.Not.ToHaveLastThrownMessage("failed") }} {
			assertThrownMessage(t, "outcome source must not be nil", attempt)
		}
	}
}

func TestMockOutcomeMatchersPreserveSourceErrorsAndMessagePanics(t *testing.T) {
	sentinel := errors.New("outcome source failed")
	for _, negated := range []bool{false, true} {
		func() {
			defer func() {
				thrown, ok := recover().(__kinmokuseiThrown)
				if !ok || thrown.err != sentinel {
					t.Fatal("source throw changed", thrown)
				}
			}()
			source := &matcherOutcomeSource{func() []MockOutcomeResult { panic(__kinmokuseiThrown{err: sentinel}) }}
			if negated {
				expectMockOutcome(source).Not.ToHaveThrown()
			} else {
				expectMockOutcome(source).ToHaveThrown()
			}
		}()
		for _, mode := range []string{"source", "message", "explicit"} {
			func() {
				var events []string
				if mode != "explicit" {
					defer func() {
						if recovered := recover(); recovered != "outcome "+mode+" panic" || fmt.Sprint(events) != "[afterEach cleanup afterAll]" {
							t.Fatal(recovered, events)
						}
					}()
				}
				suite := NewSuite("outcome cleanup")
				suite.AfterEach(func() { events = append(events, "afterEach") })
				suite.AfterAll(func() { events = append(events, "afterAll") })
				suite.TestWithContext("case", func(context *TestContext) {
					context.Cleanup(func() { events = append(events, "cleanup") })
					source := &matcherOutcomeSource{func() []MockOutcomeResult {
						if mode == "source" {
							panic("outcome source panic")
						}
						if mode == "explicit" {
							withTestLocation("outcome-spy.km", 42, func() { panic(__kinmokuseiThrown{err: sentinel}) })
						}
						return []MockOutcomeResult{{Status: "throw", Exception: &outcomeMessagePanic{}}}
					}}
					if mode == "message" {
						expectMockOutcome(source).ToHaveThrownTimes(1)
					}
					if negated {
						expectMockOutcome(source).Not.ToHaveLastThrownMessage("failed")
					} else {
						expectMockOutcome(source).ToHaveLastThrownMessage("failed")
					}
				})
				summary := suite.RunTo(&bytes.Buffer{})
				if mode != "explicit" {
					t.Fatal("native panic swallowed")
				}
				if summary.Failed != 1 || summary.Results[0].Failures[0].Location != (TestLocation{File: "outcome-spy.km", Line: 42, Source: "explicit"}) || fmt.Sprint(events) != "[afterEach cleanup afterAll]" {
					t.Fatal(summary, events)
				}
			}()
		}
	}
}

func TestMockOutcomeMatchersConcurrentInvokeAndClear(t *testing.T) {
	mock := NewMockAction[int](func(int) {})
	matcher := expectMockOutcome(mock)
	var group sync.WaitGroup
	group.Add(1)
	go func() {
		defer group.Done()
		for i := 0; i < 200; i++ {
			mock.Invoke(i)
			if i%20 == 0 {
				mock.ClearCalls()
			}
		}
	}()
	for i := 0; i < 200; i++ {
		matcher.Not.ToHaveThrown()
		matcher.ToHaveThrownTimes(0)
		matcher.Not.ToHaveReturnedTimes(201)
		matcher.Not.ToHaveNthThrownMessage(1, "failed")
		matcher.Not.ToHaveLastThrownMessage("failed")
	}
	group.Wait()
}

func TestMockOutcomeMatchersIdentifyCallerAndMismatches(t *testing.T) {
	returned := NewMockAction[int](func(int) {})
	returned.Invoke(1)
	throwing := NewMockAction[int](func(int) { panic(__kinmokuseiThrown{err: errors.New("failed")}) })
	assertThrownMessage(t, "failed", func() { throwing.Invoke(1) })
	success, failure := expectMockOutcome(returned), expectMockOutcome(throwing)
	empty := expectMockOutcome(NewMockAction[int](func(int) {}))
	var want TestLocation
	record := func() {
		_, file, line, _ := runtime.Caller(1)
		want = TestLocation{File: file, Line: line, Source: "go"}
	}
	checks := []func(){
		func() { record(); empty.ToHaveReturned() }, func() { record(); success.Not.ToHaveReturned() },
		func() { record(); success.ToHaveReturnedTimes(2) }, func() { record(); success.Not.ToHaveReturnedTimes(1) },
		func() { record(); empty.ToHaveThrown() }, func() { record(); failure.Not.ToHaveThrown() },
		func() { record(); failure.ToHaveThrownTimes(2) }, func() { record(); failure.Not.ToHaveThrownTimes(1) },
		func() { record(); failure.ToHaveNthThrownMessage(1, "missing") }, func() { record(); failure.Not.ToHaveNthThrownMessage(1, "failed") },
		func() { record(); failure.ToHaveLastThrownMessage("missing") }, func() { record(); failure.Not.ToHaveLastThrownMessage("failed") },
		func() { record(); empty.ToHaveNthThrownMessage(1, "failed") }, func() { record(); success.ToHaveLastThrownMessage("failed") },
		func() { record(); success.ToHaveReturnedTimes(-1) }, func() { record(); success.Not.ToHaveReturnedTimes(-1) },
		func() { record(); failure.ToHaveThrownTimes(-1) }, func() { record(); failure.Not.ToHaveThrownTimes(-1) },
		func() { record(); failure.ToHaveNthThrownMessage(0, "failed") }, func() { record(); failure.Not.ToHaveNthThrownMessage(0, "failed") },
		func() { record(); failure.ToHaveNthThrownMessage(1, "") }, func() { record(); failure.Not.ToHaveNthThrownMessage(1, "") },
		func() { record(); failure.ToHaveLastThrownMessage("") }, func() { record(); failure.Not.ToHaveLastThrownMessage("") },
		func() { record(); expectMockOutcome(nil).ToHaveReturned() }, func() { record(); expectMockOutcome((*MockAction[int])(nil)).Not.ToHaveReturned() },
	}
	for _, check := range checks {
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
	assertThrownMessage(t, `status "return"`, func() { success.ToHaveLastThrownMessage("failed") })
	assertThrownMessage(t, `got "failed"`, func() { failure.ToHaveNthThrownMessage(1, "missing") })
}
