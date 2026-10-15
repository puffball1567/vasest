package vasest

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"runtime"
	"testing"
)

func TestMockErrorMatchersPreserveChainsTypesAndLiveHistory(t *testing.T) {
	cause := errors.New("same message")
	path := &os.PathError{Op: "open", Path: "金木犀", Err: cause}
	wrapped := errors.Join(errors.New("other"), fmt.Errorf("load: %w", path))
	invoked := 0
	action := NewMockAction[int](func(int) { invoked++ })
	action.ImplementOnce(func(int) { panic(__kinmokuseiThrown{err: wrapped}) })
	matcher := NewMockOutcomeExpectation(action)
	matcher.Not.ToHaveNthThrownCause(1, cause)
	matcher.Not.ToHaveLastThrownCause(nil)
	NegatedMockOutcomeExpectationToHaveNthThrownType[*os.PathError](matcher.Not, 1)
	NegatedMockOutcomeExpectationToHaveLastThrownType[error](matcher.Not)
	if invoked != 0 {
		t.Fatal("matcher consumed queued implementation")
	}
	assertThrownMessage(t, "load", func() { action.Invoke(1) })
	matcher.ToHaveNthThrownCause(1, cause)
	matcher.ToHaveLastThrownCause(cause)
	matcher.Not.ToHaveNthThrownCause(1, errors.New("same message"))
	matcher.Not.ToHaveLastThrownCause(nil)
	var found *os.PathError
	MockOutcomeExpectationToHaveNthThrownType(matcher, 1, &found)
	if found != path {
		t.Fatal("extracted error identity changed")
	}
	MockOutcomeExpectationToHaveLastThrownType[*os.PathError](matcher, &found)
	var whole error
	MockOutcomeExpectationToHaveLastThrownType[error](matcher, &whole)
	if whole != wrapped {
		t.Fatal("error interface extraction changed")
	}
	NegatedMockOutcomeExpectationToHaveNthThrownType[*assertionCodeError](matcher.Not, 1)
	NegatedMockOutcomeExpectationToHaveLastThrownType[fmt.Stringer](matcher.Not)
	action.Invoke(2)
	matcher.ToHaveNthThrownCause(1, cause)
	matcher.Not.ToHaveLastThrownCause(cause)
	NegatedMockOutcomeExpectationToHaveLastThrownType[*os.PathError](matcher.Not)
	assertThrownMessage(t, `status "return"`, func() { matcher.ToHaveLastThrownCause(cause) })
	assertThrownMessage(t, `status "return"`, func() { MockOutcomeExpectationToHaveLastThrownType(matcher, &found) })
	if found != path || invoked != 1 {
		t.Fatal("nonthrow changed destination or queued behavior")
	}
	action.ClearCalls()
	matcher.Not.ToHaveNthThrownCause(1, cause)
	NegatedMockOutcomeExpectationToHaveLastThrownType[error](matcher.Not)
	assertThrownMessage(t, "got 0 call(s)", func() { matcher.ToHaveLastThrownCause(cause) })

	mock := NewMockFn[int, error](func(int) error { return wrapped })
	mock.Invoke(1)
	expectMockOutcome(mock).Not.ToHaveNthThrownCause(1, cause)
	mock.ImplementOnce(func(int) error { panic(__kinmokuseiThrown{err: wrapped}) })
	assertThrownMessage(t, "load", func() { mock.Invoke(2) })
	expectMockOutcome(mock).ToHaveNthThrownCause(2, cause)
	MockOutcomeExpectationToHaveLastThrownType(expectMockOutcome(mock), &found)
	if found != path {
		t.Fatal(found)
	}
}

func TestMockErrorMatchersRetainCustomMatchingAndNilPayloads(t *testing.T) {
	code := &assertionCodeError{Code: 42}
	var results []MockOutcomeResult
	matcher := expectMockOutcome(&matcherOutcomeSource{func() []MockOutcomeResult { return results }})
	results = []MockOutcomeResult{{Status: "throw", Exception: assertionMappedError{code}}}
	matcher.ToHaveLastThrownCause(&assertionCodeError{Code: 42})
	var found *assertionCodeError
	MockOutcomeExpectationToHaveNthThrownType(matcher, 1, &found)
	if found != code {
		t.Fatal(found)
	}
	assertThrownMessage(t, "(errors.As)", func() { NegatedMockOutcomeExpectationToHaveLastThrownType[*assertionCodeError](matcher.Not) })
	if found != code {
		t.Fatal("negative changed caller destination")
	}
	results[0].Exception = fmt.Errorf("wrapped: %w", code)
	var text fmt.Stringer
	MockOutcomeExpectationToHaveLastThrownType(matcher, &text)
	if text != code {
		t.Fatal(text)
	}
	results[0].Exception = assertionMutatingAsError{}
	assertThrownMessage(t, "(errors.As)", func() { MockOutcomeExpectationToHaveLastThrownType(matcher, &found) })
	if found == nil || found.Code != 37 {
		t.Fatal("custom As destination mutation lost", found)
	}
	NegatedMockOutcomeExpectationToHaveNthThrownType[*assertionCodeError](matcher.Not, 1)
	NegatedMockOutcomeExpectationToHaveLastThrownType[*assertionCodeError](matcher.Not)
	if found.Code != 37 {
		t.Fatal("negative changed caller destination")
	}
	results[0].Exception = errors.New("unrelated")
	assertThrownMessage(t, "(errors.As)", func() { MockOutcomeExpectationToHaveNthThrownType(matcher, 1, &found) })
	if found.Code != 37 {
		t.Fatal("ordinary mismatch overwrote target")
	}
	results[0].Exception = nil
	matcher.ToHaveNthThrownCause(1, nil)
	matcher.ToHaveLastThrownCause(nil)
	matcher.Not.ToHaveLastThrownCause(code)
	NegatedMockOutcomeExpectationToHaveLastThrownType[error](matcher.Not)
	assertThrownMessage(t, "(errors.As)", func() { MockOutcomeExpectationToHaveLastThrownType(matcher, &found) })
	var empty *os.PathError
	results[0].Exception = empty
	var extracted *os.PathError = &os.PathError{Path: "unchanged"}
	MockOutcomeExpectationToHaveLastThrownType(matcher, &extracted)
	if extracted != nil {
		t.Fatal("typed nil lost")
	}
	matcher.ToHaveLastThrownCause(empty)
	matcher.Not.ToHaveLastThrownCause(nil)
	for _, status := range []string{"return", "incomplete", "panic", ""} {
		results[0] = MockOutcomeResult{Status: status, Exception: assertionMappedError{code}}
		matcher.Not.ToHaveNthThrownCause(1, code)
		NegatedMockOutcomeExpectationToHaveLastThrownType[*assertionCodeError](matcher.Not)
		assertThrownMessage(t, "got status", func() { matcher.ToHaveLastThrownCause(code) })
	}
}

func TestMockErrorMatchersValidateBeforeOneSnapshot(t *testing.T) {
	reads := 0
	cause := errors.New("cause")
	results := []MockOutcomeResult{{Status: "throw", Exception: cause}}
	matcher := expectMockOutcome(&matcherOutcomeSource{func() []MockOutcomeResult { reads++; return results }})
	var target error
	checks := []func(){
		func() { matcher.ToHaveNthThrownCause(1, cause) }, func() { matcher.ToHaveLastThrownCause(cause) },
		func() { MockOutcomeExpectationToHaveNthThrownType(matcher, 1, &target) }, func() { MockOutcomeExpectationToHaveLastThrownType(matcher, &target) },
		func() { matcher.Not.ToHaveNthThrownCause(2, cause) }, func() { matcher.Not.ToHaveLastThrownCause(nil) },
		func() { NegatedMockOutcomeExpectationToHaveNthThrownType[*os.PathError](matcher.Not, 1) }, func() { NegatedMockOutcomeExpectationToHaveLastThrownType[*os.PathError](matcher.Not) },
	}
	for _, check := range checks {
		reads = 0
		check()
		if reads != 1 {
			t.Fatal(reads)
		}
	}
	reads = 0
	var invalid int
	for i, check := range []func(){
		func() { matcher.ToHaveNthThrownCause(0, cause) }, func() { matcher.Not.ToHaveNthThrownCause(-1, cause) },
		func() { MockOutcomeExpectationToHaveNthThrownType(matcher, 0, &target) }, func() { NegatedMockOutcomeExpectationToHaveNthThrownType[error](matcher.Not, 0) },
		func() { MockOutcomeExpectationToHaveNthThrownType[error](matcher, 1, nil) }, func() { MockOutcomeExpectationToHaveLastThrownType[error](matcher, nil) },
		func() { MockOutcomeExpectationToHaveNthThrownType(matcher, 1, &invalid) }, func() { MockOutcomeExpectationToHaveLastThrownType(matcher, &invalid) },
		func() { NegatedMockOutcomeExpectationToHaveNthThrownType[int](matcher.Not, 1) }, func() { NegatedMockOutcomeExpectationToHaveLastThrownType[int](matcher.Not) },
	} {
		fragment := "must"
		if i >= 8 {
			fragment = "requires"
		}
		assertThrownMessage(t, fragment, check)
	}
	if reads != 0 {
		t.Fatal("invalid arguments read snapshot", reads)
	}
	for _, source := range []MockOutcomeSource{nil, (*MockAction[int])(nil), (*MockFn[int, int])(nil)} {
		missing := expectMockOutcome(source)
		for _, check := range []func(){
			func() { missing.ToHaveNthThrownCause(1, cause) }, func() { missing.ToHaveLastThrownCause(cause) },
			func() { missing.Not.ToHaveNthThrownCause(1, cause) }, func() { missing.Not.ToHaveLastThrownCause(cause) },
			func() { MockOutcomeExpectationToHaveNthThrownType(missing, 1, &target) }, func() { MockOutcomeExpectationToHaveLastThrownType(missing, &target) },
			func() { NegatedMockOutcomeExpectationToHaveNthThrownType[error](missing.Not, 1) }, func() { NegatedMockOutcomeExpectationToHaveLastThrownType[error](missing.Not) },
		} {
			assertThrownMessage(t, "outcome source must not be nil", check)
		}
	}
}

type outcomeMatchingError struct {
	is func(error) bool
	as func(any) bool
}

func (*outcomeMatchingError) Error() string            { return "matching error" }
func (err *outcomeMatchingError) Is(target error) bool { return err.is(target) }
func (err *outcomeMatchingError) As(target any) bool   { return err.as(target) }

func TestMockErrorMatchersPreserveCallbackFailuresAndNativeCleanup(t *testing.T) {
	sentinel := errors.New("matching failed")
	for _, mode := range []string{"source", "Is", "As"} {
		for _, negated := range []bool{false, true} {
			build := func(raise func()) *MockOutcomeExpectation {
				err := &outcomeMatchingError{func(error) bool { raise(); return false }, func(any) bool { raise(); return false }}
				return expectMockOutcome(&matcherOutcomeSource{func() []MockOutcomeResult {
					if mode == "source" {
						raise()
					}
					return []MockOutcomeResult{{Status: "throw", Exception: err}}
				}})
			}
			check := func(matcher *MockOutcomeExpectation) {
				if mode == "As" {
					var target *assertionCodeError
					if negated {
						NegatedMockOutcomeExpectationToHaveLastThrownType[*assertionCodeError](matcher.Not)
					} else {
						MockOutcomeExpectationToHaveNthThrownType(matcher, 1, &target)
					}
				} else if negated {
					matcher.Not.ToHaveNthThrownCause(1, sentinel)
				} else {
					matcher.ToHaveLastThrownCause(sentinel)
				}
			}
			func() {
				defer func() {
					thrown, ok := recover().(__kinmokuseiThrown)
					if !ok || thrown.err != sentinel {
						t.Fatal("callback error identity changed", thrown)
					}
				}()
				check(build(func() { panic(__kinmokuseiThrown{err: sentinel}) }))
			}()
			for _, native := range []bool{false, true} {
				func() {
					var events []string
					if native {
						defer func() {
							if recover() != "native matching panic" || fmt.Sprint(events) != "[afterEach cleanup afterAll]" {
								t.Fatal(events)
							}
						}()
					}
					suite := NewSuite("matching callbacks")
					suite.AfterEach(func() { events = append(events, "afterEach") })
					suite.AfterAll(func() { events = append(events, "afterAll") })
					suite.TestWithContext("case", func(context *TestContext) {
						context.Cleanup(func() { events = append(events, "cleanup") })
						check(build(func() {
							if native {
								panic("native matching panic")
							}
							withTestLocation("matching-source.km", 42, func() { panic(__kinmokuseiThrown{err: sentinel}) })
						}))
					})
					summary := suite.RunTo(&bytes.Buffer{})
					if native {
						t.Fatal("native panic swallowed")
					}
					if summary.Failed != 1 || summary.Results[0].Failures[0].Location != (TestLocation{File: "matching-source.km", Line: 42, Source: "explicit"}) || fmt.Sprint(events) != "[afterEach cleanup afterAll]" {
						t.Fatal(summary, events)
					}
				}()
			}
		}
	}
	// Nonthrow selections must not execute Is or As hooks.
	hook := &outcomeMatchingError{func(error) bool { t.Fatal("Is hook on nonthrow"); return false }, func(any) bool { t.Fatal("As hook on nonthrow"); return false }}
	for _, status := range []string{"return", "incomplete", "panic"} {
		matcher := expectMockOutcome(&matcherOutcomeSource{func() []MockOutcomeResult { return []MockOutcomeResult{{Status: status, Exception: hook}} }})
		matcher.Not.ToHaveLastThrownCause(sentinel)
		NegatedMockOutcomeExpectationToHaveLastThrownType[*assertionCodeError](matcher.Not)
	}
}

func TestMockErrorMatchersIdentifyEveryCaller(t *testing.T) {
	cause := errors.New("cause")
	source := &matcherOutcomeSource{func() []MockOutcomeResult { return []MockOutcomeResult{{Status: "throw", Exception: cause}} }}
	matcher := expectMockOutcome(source)
	var destination error
	var wrong *os.PathError
	var invalid int
	nilMatcher := expectMockOutcome((*MockAction[int])(nil))
	var want TestLocation
	record := func() {
		_, file, line, _ := runtime.Caller(1)
		want = TestLocation{File: file, Line: line, Source: "go"}
	}
	checks := []func(){
		func() { record(); matcher.ToHaveNthThrownCause(1, errors.New("cause")) }, func() { record(); matcher.ToHaveLastThrownCause(nil) },
		func() { record(); matcher.Not.ToHaveNthThrownCause(1, cause) }, func() { record(); matcher.Not.ToHaveLastThrownCause(cause) },
		func() { record(); MockOutcomeExpectationToHaveNthThrownType(matcher, 1, &wrong) }, func() { record(); MockOutcomeExpectationToHaveLastThrownType(matcher, &wrong) },
		func() { record(); NegatedMockOutcomeExpectationToHaveNthThrownType[error](matcher.Not, 1) }, func() { record(); NegatedMockOutcomeExpectationToHaveLastThrownType[error](matcher.Not) },
		func() { record(); matcher.ToHaveNthThrownCause(0, cause) }, func() { record(); matcher.Not.ToHaveNthThrownCause(0, cause) },
		func() { record(); MockOutcomeExpectationToHaveNthThrownType(matcher, 0, &destination) }, func() { record(); NegatedMockOutcomeExpectationToHaveNthThrownType[error](matcher.Not, 0) },
		func() { record(); MockOutcomeExpectationToHaveNthThrownType[error](matcher, 1, nil) }, func() { record(); MockOutcomeExpectationToHaveLastThrownType[error](matcher, nil) },
		func() { record(); MockOutcomeExpectationToHaveNthThrownType(matcher, 1, &invalid) }, func() { record(); MockOutcomeExpectationToHaveLastThrownType(matcher, &invalid) },
		func() { record(); NegatedMockOutcomeExpectationToHaveNthThrownType[int](matcher.Not, 1) }, func() { record(); NegatedMockOutcomeExpectationToHaveLastThrownType[int](matcher.Not) },
		func() { record(); expectMockOutcome(nil).ToHaveLastThrownCause(nil) }, func() { record(); NegatedMockOutcomeExpectationToHaveLastThrownType[error](nilMatcher.Not) },
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
}

func TestMockErrorMatchersTrackPendingCallsInInvocationOrder(t *testing.T) {
	cause := errors.New("second")
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	action := NewMockAction[int](func(value int) {
		if value == 1 {
			close(entered)
			<-release
		} else {
			panic(__kinmokuseiThrown{err: cause})
		}
	})
	matcher := expectMockOutcome(action)
	go func() { defer close(done); action.Invoke(1) }()
	<-entered
	matcher.Not.ToHaveNthThrownCause(1, nil)
	NegatedMockOutcomeExpectationToHaveLastThrownType[error](matcher.Not)
	assertThrownMessage(t, `status "incomplete"`, func() { matcher.ToHaveLastThrownCause(cause) })
	assertThrownMessage(t, "second", func() { action.Invoke(2) })
	var found error
	MockOutcomeExpectationToHaveLastThrownType(matcher, &found)
	if found != cause {
		t.Fatal(found)
	}
	matcher.ToHaveNthThrownCause(2, cause)
	close(release)
	<-done
	// Finishing call 1 after call 2 must not replace the last invocation.
	matcher.ToHaveLastThrownCause(cause)
	MockOutcomeExpectationToHaveLastThrownType(matcher, &found)
	matcher.Not.ToHaveNthThrownCause(1, cause)
	NegatedMockOutcomeExpectationToHaveNthThrownType[error](matcher.Not, 1)
	action.ImplementOnce(func(int) { panic("native panic") })
	func() {
		defer func() {
			if recover() != "native panic" {
				t.Fatal("panic changed")
			}
		}()
		action.Invoke(3)
	}()
	matcher.Not.ToHaveLastThrownCause(nil)
	NegatedMockOutcomeExpectationToHaveLastThrownType[error](matcher.Not)
	assertThrownMessage(t, `status "panic"`, func() { MockOutcomeExpectationToHaveLastThrownType(matcher, &found) })
}

func TestMockErrorMatchersConcurrentInvokeAndClear(t *testing.T) {
	mock := NewMockAction[int](func(int) {})
	matcher := expectMockOutcome(mock)
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
	for i := 0; i < 200; i++ {
		matcher.Not.ToHaveNthThrownCause(1, nil)
		matcher.Not.ToHaveLastThrownCause(nil)
		NegatedMockOutcomeExpectationToHaveNthThrownType[error](matcher.Not, 1)
		NegatedMockOutcomeExpectationToHaveLastThrownType[error](matcher.Not)
	}
	<-done
}
