package vasest

import (
	"bytes"
	"errors"
	"fmt"
	"runtime"
	"testing"
)

func checkMockTypePredicate[T any](matcher *MockOutcomeExpectation, kind string, negated bool, predicate func(T) bool) {
	switch kind {
	case "any":
		if negated {
			NegatedMockOutcomeExpectationToHaveThrownTypeSatisfying(matcher.Not, predicate)
		} else {
			MockOutcomeExpectationToHaveThrownTypeSatisfying(matcher, predicate)
		}
	case "nth":
		if negated {
			NegatedMockOutcomeExpectationToHaveNthThrownTypeSatisfying(matcher.Not, 1, predicate)
		} else {
			MockOutcomeExpectationToHaveNthThrownTypeSatisfying(matcher, 1, predicate)
		}
	case "last":
		if negated {
			NegatedMockOutcomeExpectationToHaveLastThrownTypeSatisfying(matcher.Not, predicate)
		} else {
			MockOutcomeExpectationToHaveLastThrownTypeSatisfying(matcher, predicate)
		}
	}
}

func TestMockTypePredicateStatusTypePredicateAndNegation(t *testing.T) {
	for _, kind := range []string{"any", "nth", "last"} {
		for _, status := range []string{"throw", "return", "incomplete", "panic", ""} {
			for _, present := range []bool{false, true} {
				for _, accepted := range []bool{false, true} {
					for _, negated := range []bool{false, true} {
						original := &predicateDomainError{Code: 1<<60 + 1}
						var actual error = errors.New("other")
						if present {
							actual = original
						}
						reads, calls := 0, 0
						matcher := expectMockOutcome(&matcherOutcomeSource{func() []MockOutcomeResult { reads++; return []MockOutcomeResult{{Status: status, Exception: actual}} }})
						predicate := func(value *predicateDomainError) bool {
							calls++
							if value != original || value.Code != 1<<60+1 {
								t.Fatal("lost native type/reference")
							}
							return accepted
						}
						check := func() { checkMockTypePredicate(matcher, kind, negated, predicate) }
						if (status == "throw" && present && accepted) == negated {
							assertThrownMessage(t, "expected mock", check)
						} else {
							check()
						}
						want := 0
						if status == "throw" && present {
							want = 1
						}
						if reads != 1 || calls != want || original.Reads != 0 {
							t.Fatal(kind, status, reads, calls, original.Reads)
						}
					}
				}
			}
		}
	}
}

func TestMockTypePredicateSnapshotShortCircuitAndFreshExtraction(t *testing.T) {
	first, second, third := &predicateDomainError{Code: 1}, &predicateDomainError{Code: 2}, &predicateDomainError{Code: 3}
	reads := 0
	results := []MockOutcomeResult{{Status: "throw", Exception: errors.New("other type")}, {Status: "return", Exception: third}, {Status: "throw", Exception: first}, {Status: "throw", Exception: second}, {Status: "throw", Exception: third}}
	matcher := expectMockOutcome(&matcherOutcomeSource{func() []MockOutcomeResult { reads++; return results }})
	visited := []*predicateDomainError{}
	MockOutcomeExpectationToHaveThrownTypeSatisfying(matcher, func(value *predicateDomainError) bool { visited = append(visited, value); return value == second })
	if reads != 1 || len(visited) != 2 || visited[0] != first || visited[1] != second {
		t.Fatal(reads, visited)
	}
	NegatedMockOutcomeExpectationToHaveThrownTypeSatisfying(matcher.Not, func(value *predicateDomainError) bool { return value.Code > 10 })
	MockOutcomeExpectationToHaveNthThrownTypeSatisfying(matcher, 4, func(value *predicateDomainError) bool { return value == second })
	MockOutcomeExpectationToHaveLastThrownTypeSatisfying(matcher, func(value *predicateDomainError) bool { return value == third })
	// As receives a new zero destination for every visited throw and every check.
	seen := 0
	results = []MockOutcomeResult{
		{Status: "throw", Exception: predicateAsError{as: func(target any) bool { *target.(**predicateDomainError) = first; return true }}},
		{Status: "throw", Exception: predicateAsError{as: func(target any) bool {
			if *target.(**predicateDomainError) != nil {
				t.Fatal("stale extraction destination")
			}
			return true
		}}},
	}
	MockOutcomeExpectationToHaveThrownTypeSatisfying(matcher, func(value *predicateDomainError) bool { seen++; return value == nil })
	if seen != 2 {
		t.Fatal(seen)
	}
	results = []MockOutcomeResult{{Status: "throw", Exception: predicateAsError{as: func(target any) bool { *target.(*fmt.Stringer) = nil; return true }}}}
	MockOutcomeExpectationToHaveLastThrownTypeSatisfying[fmt.Stringer](matcher, func(value fmt.Stringer) bool { return value == nil })
	results = []MockOutcomeResult{{Status: "throw", Exception: predicateAsError{as: func(target any) bool { *target.(**predicateDomainError) = first; return false }}}}
	NegatedMockOutcomeExpectationToHaveThrownTypeSatisfying(matcher.Not, func(*predicateDomainError) bool { t.Fatal("false As invoked predicate"); return true })
	results = []MockOutcomeResult{{Status: "throw", Exception: errors.Join(first, second)}}
	NegatedMockOutcomeExpectationToHaveLastThrownTypeSatisfying(matcher.Not, func(value *predicateDomainError) bool { return value == second })
}

func TestMockTypePredicateLiveMockHistoryQueuesAndReentrantChecks(t *testing.T) {
	first, second := &predicateDomainError{Code: 1<<60 + 1}, &predicateDomainError{Code: 2}
	invoked := 0
	mock := NewMockAction[int](func(int) { invoked++ })
	mock.ImplementOnce(func(int) { panic(__kinmokuseiThrown{err: first}) })
	matcher := expectMockOutcome(mock)
	NegatedMockOutcomeExpectationToHaveThrownTypeSatisfying(matcher.Not, func(*predicateDomainError) bool { t.Fatal("empty history predicate"); return true })
	if invoked != 0 {
		t.Fatal("matcher invoked mock")
	}
	if expectThrows(func() { mock.Invoke(1) }) != first {
		t.Fatal("queued outcome replaced")
	}
	MockOutcomeExpectationToHaveLastThrownTypeSatisfying(matcher, func(value *predicateDomainError) bool { value.Code++; return value == first })
	mock.ImplementOnce(func(int) { panic(__kinmokuseiThrown{err: second}) })
	expectThrows(func() { mock.Invoke(2) })
	visited := []*predicateDomainError{}
	NegatedMockOutcomeExpectationToHaveThrownTypeSatisfying(matcher.Not, func(value *predicateDomainError) bool {
		visited = append(visited, value)
		mock.ClearCalls()
		mock.Invoke(3)
		return false
	})
	if len(visited) != 2 || visited[0] != first || visited[1] != second || first.Code != 1<<60+2 || len(mock.Outcomes()) != 1 || invoked != 2 {
		t.Fatal("snapshot or queue changed", len(visited), len(mock.Outcomes()), invoked)
	}
	NegatedMockOutcomeExpectationToHaveLastThrownTypeSatisfying(matcher.Not, func(*predicateDomainError) bool { t.Fatal("normal return callback"); return true })
	returned := NewMockFn[int, error](func(int) error { return first })
	returned.Invoke(1)
	NegatedMockOutcomeExpectationToHaveThrownTypeSatisfying(expectMockOutcome(returned).Not, func(*predicateDomainError) bool { t.Fatal("returned error treated as throw"); return true })
	returned.ImplementOnce(func(int) error { panic(__kinmokuseiThrown{err: (*predicateDomainError)(nil)}) })
	expectThrows(func() { returned.Invoke(2) })
	MockOutcomeExpectationToHaveLastThrownTypeSatisfying(expectMockOutcome(returned), func(value *predicateDomainError) bool { return value == nil })
	returned.ImplementOnce(func(int) error { panic(__kinmokuseiThrown{err: nil}) })
	expectThrows(func() { returned.Invoke(3) })
	NegatedMockOutcomeExpectationToHaveLastThrownTypeSatisfying[error](expectMockOutcome(returned).Not, func(error) bool { t.Fatal("nil throw matched error type"); return true })
	values := assertionSliceError{1, 2}
	mock.ImplementOnce(func(int) { panic(__kinmokuseiThrown{err: values}) })
	expectThrows(func() { mock.Invoke(4) })
	MockOutcomeExpectationToHaveLastThrownTypeSatisfying(matcher, func(value assertionSliceError) bool { value[0] = 7; return len(value) == 2 })
	if values[0] != 7 || first.Reads != 0 || second.Reads != 0 {
		t.Fatal(values)
	}
}

func TestMockTypePredicateValidationAndMissingCalls(t *testing.T) {
	reads, calls := 0, 0
	matcher := expectMockOutcome(&matcherOutcomeSource{func() []MockOutcomeResult { reads++; return nil }})
	for _, kind := range []string{"any", "nth", "last"} {
		for _, negated := range []bool{false, true} {
			assertThrownMessage(t, "predicate must not be nil", func() { checkMockTypePredicate[error](matcher, kind, negated, nil) })
			assertThrownMessage(t, "requires an error type or interface", func() { checkMockTypePredicate(matcher, kind, negated, func(int) bool { calls++; return true }) })
		}
	}
	predicate := func(error) bool { calls++; return true }
	assertThrownMessage(t, "index must be at least 1", func() { MockOutcomeExpectationToHaveNthThrownTypeSatisfying[error](matcher, 0, predicate) })
	assertThrownMessage(t, "index must be at least 1", func() { NegatedMockOutcomeExpectationToHaveNthThrownTypeSatisfying[error](matcher.Not, -1, predicate) })
	if reads != 0 || calls != 0 {
		t.Fatal("invalid arguments read source", reads, calls)
	}
	for _, kind := range []string{"any", "nth", "last"} {
		checkMockTypePredicate(matcher, kind, true, predicate)
		assertThrownMessage(t, "expected mock", func() { checkMockTypePredicate(matcher, kind, false, predicate) })
		for _, source := range []MockOutcomeSource{nil, (*MockAction[int])(nil), (*MockFn[int, int])(nil)} {
			for _, negated := range []bool{false, true} {
				assertThrownMessage(t, "mock outcome source must not be nil", func() { checkMockTypePredicate(expectMockOutcome(source), kind, negated, predicate) })
			}
		}
	}
	if reads != 6 || calls != 0 {
		t.Fatal(reads, calls)
	}
}

func TestMockTypePredicateCallerLocations(t *testing.T) {
	matcher := expectMockOutcome(&matcherOutcomeSource{func() []MockOutcomeResult {
		return []MockOutcomeResult{{Status: "throw", Exception: errors.New("body")}}
	}})
	var want TestLocation
	record := func() {
		_, file, line, _ := runtime.Caller(1)
		want = TestLocation{File: file, Line: line + 1, Source: "go"}
	}
	yes, no := func(error) bool { return true }, func(error) bool { return false }
	checks := []func(){
		func() {
			record()
			MockOutcomeExpectationToHaveThrownTypeSatisfying[error](matcher, no)
		},
		func() {
			record()
			NegatedMockOutcomeExpectationToHaveThrownTypeSatisfying[error](matcher.Not, yes)
		},
		func() {
			record()
			MockOutcomeExpectationToHaveNthThrownTypeSatisfying[error](matcher, 1, no)
		},
		func() {
			record()
			NegatedMockOutcomeExpectationToHaveNthThrownTypeSatisfying[error](matcher.Not, 1, yes)
		},
		func() {
			record()
			MockOutcomeExpectationToHaveLastThrownTypeSatisfying[error](matcher, no)
		},
		func() {
			record()
			NegatedMockOutcomeExpectationToHaveLastThrownTypeSatisfying[error](matcher.Not, yes)
		},
		func() {
			record()
			MockOutcomeExpectationToHaveThrownTypeSatisfying[error](matcher, nil)
		},
		func() {
			record()
			NegatedMockOutcomeExpectationToHaveNthThrownTypeSatisfying[error](matcher.Not, 0, yes)
		},
		func() {
			record()
			MockOutcomeExpectationToHaveLastThrownTypeSatisfying(matcher, func(int) bool { return true })
		},
	}
	for _, check := range checks {
		if got := failureLocation(expectThrows(check)); got != want {
			t.Fatal(got, want)
		}
	}
}

func TestMockTypePredicatePreservesSourceAsAndPredicateControlFlow(t *testing.T) {
	for _, kind := range []string{"any", "nth", "last"} {
		for _, negated := range []bool{false, true} {
			for _, phase := range []string{"source", "As", "predicate"} {
				for _, cause := range []error{nil, errors.New("callback"), NewFailureError(errors.New("located"), TestLocation{File: "predicate.km", Line: 42, Source: "explicit"})} {
					control := func() { panic(__kinmokuseiThrown{err: cause}) }
					actual := error(&predicateDomainError{})
					if phase == "As" {
						actual = predicateAsError{as: func(any) bool { control(); return false }}
					}
					matcher := expectMockOutcome(&matcherOutcomeSource{func() []MockOutcomeResult {
						if phase == "source" {
							control()
						}
						return []MockOutcomeResult{{Status: "throw", Exception: actual}}
					}})
					if got := expectThrows(func() {
						checkMockTypePredicate(matcher, kind, negated, func(*predicateDomainError) bool { control(); return true })
					}); got != cause {
						t.Fatal("control flow replaced", kind, phase, got, cause)
					}
				}
				for _, nativePanic := range []bool{false, true} {
					cleaned := false
					suite := NewSuite("cleanup")
					suite.TestWithContext("case", func(context *TestContext) {
						context.Cleanup(func() { cleaned = true })
						control := func() {
							if nativePanic {
								panic("native callback")
							}
							skipTest("not available")
						}
						actual := error(&predicateDomainError{})
						if phase == "As" {
							actual = predicateAsError{as: func(any) bool { control(); return false }}
						}
						matcher := expectMockOutcome(&matcherOutcomeSource{func() []MockOutcomeResult {
							if phase == "source" {
								control()
							}
							return []MockOutcomeResult{{Status: "throw", Exception: actual}}
						}})
						checkMockTypePredicate(matcher, kind, negated, func(*predicateDomainError) bool { control(); return true })
						t.Fatal("continued after control flow")
					})
					if nativePanic {
						func() {
							defer func() {
								if got := recover(); got != "native callback" {
									t.Fatal(got)
								}
							}()
							suite.RunTo(&bytes.Buffer{})
						}()
					} else {
						result := suite.RunTo(&bytes.Buffer{})
						if result.Skipped != 1 || result.Failed != 0 {
							t.Fatal(result)
						}
					}
					if !cleaned {
						t.Fatal("cleanup skipped")
					}
				}
			}
		}
	}
}

func TestMockTypePredicatePendingAndCompletionOrder(t *testing.T) {
	first, second := &predicateDomainError{Code: 1}, &predicateDomainError{Code: 2}
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	mock := NewMockAction[int](func(value int) {
		if value == 1 {
			close(entered)
			<-release
			panic(__kinmokuseiThrown{err: first})
		}
		panic(__kinmokuseiThrown{err: second})
	})
	go func() { defer close(done); expectThrows(func() { mock.Invoke(1) }) }()
	<-entered
	matcher := expectMockOutcome(mock)
	NegatedMockOutcomeExpectationToHaveLastThrownTypeSatisfying(matcher.Not, func(*predicateDomainError) bool { t.Fatal("pending predicate called"); return true })
	expectThrows(func() { mock.Invoke(2) })
	MockOutcomeExpectationToHaveLastThrownTypeSatisfying(matcher, func(value *predicateDomainError) bool { return value == second })
	close(release)
	<-done
	MockOutcomeExpectationToHaveNthThrownTypeSatisfying(matcher, 1, func(value *predicateDomainError) bool { return value == first })
	MockOutcomeExpectationToHaveLastThrownTypeSatisfying(matcher, func(value *predicateDomainError) bool { return value == second })
}
