package vasest

import (
	"bytes"
	"errors"
	"fmt"
	"runtime"
	"testing"
)

type predicateCode int64
type predicateDomainError struct {
	Code  predicateCode
	Reads int
}

func (e *predicateDomainError) Error() string  { e.Reads++; panic("unexpected Error call") }
func (e *predicateDomainError) String() string { return "domain" }

type predicateAsError struct{ as func(any) bool }

func (e predicateAsError) Error() string      { panic("unexpected As error formatting") }
func (e predicateAsError) As(target any) bool { return e.as(target) }

func TestErrorTypePredicateNegatesWholeMatchAndCallsOnce(t *testing.T) {
	for _, present := range []bool{false, true} {
		for _, accepted := range []bool{false, true} {
			for _, negated := range []bool{false, true} {
				original := &predicateDomainError{Code: 1<<60 + 1}
				var actual error = errors.New("other type")
				if present {
					actual = original
				}
				calls := 0
				predicate := func(value *predicateDomainError) bool {
					calls++
					if value != original || value.Code != 1<<60+1 {
						t.Fatal("lost native type/reference", value)
					}
					return accepted
				}
				check := func() {
					if negated {
						NegatedErrorExpectationToMatchTypeSatisfying(expectErrorValue(actual).Not, predicate)
					} else {
						ErrorExpectationToMatchTypeSatisfying(expectErrorValue(actual), predicate)
					}
				}
				if (present && accepted) == negated {
					assertThrownMessage(t, "satisfying predicate", check)
				} else {
					check()
				}
				want := 0
				if present {
					want = 1
				}
				if calls != want || original.Reads != 0 {
					t.Fatal("unexpected invocation/formatting", calls, original.Reads)
				}
			}
		}
	}
}

func TestErrorTypePredicateNativeExtractionAndFreshValues(t *testing.T) {
	original := &predicateDomainError{Code: 1<<60 + 1}
	matcher := expectErrorValue(original)
	ErrorExpectationToMatchTypeSatisfying(matcher, func(value *predicateDomainError) bool { value.Code++; return value == original })
	ErrorExpectationToMatchTypeSatisfying(matcher, func(value *predicateDomainError) bool { return value.Code == 1<<60+2 })
	ErrorExpectationToMatchTypeSatisfying[fmt.Stringer](matcher, func(value fmt.Stringer) bool { return value == original })
	ErrorExpectationToMatchTypeSatisfying[error](matcher, func(value error) bool { return value == original })
	wrapped := fmt.Errorf("outer: %w", errors.Join(errors.New("other"), original))
	ErrorExpectationToMatchTypeSatisfying(expectErrorValue(wrapped), func(value *predicateDomainError) bool { return value == original })
	// As selects its first assignable error; a false predicate does not restart traversal.
	first := &predicateDomainError{Code: 1}
	joined := expectErrorValue(errors.Join(first, original))
	NegatedErrorExpectationToMatchTypeSatisfying(joined.Not, func(value *predicateDomainError) bool { return value == original })
	for _, actual := range []error{nil, errors.New("missing")} {
		NegatedErrorExpectationToMatchTypeSatisfying(expectErrorValue(actual).Not, func(*predicateDomainError) bool { t.Fatal("no type match invoked predicate"); return true })
	}
	ErrorExpectationToMatchTypeSatisfying(expectErrorValue((*predicateDomainError)(nil)), func(value *predicateDomainError) bool { return value == nil })
	// Named slice error values need no comparable constraint and keep their backing slice.
	values := assertionSliceError{1, 2}
	ErrorExpectationToMatchTypeSatisfying(expectErrorValue(values), func(value assertionSliceError) bool { value[0] = 7; return len(value) == 2 })
	if values[0] != 7 {
		t.Fatal(values)
	}
}

func TestErrorTypePredicateCustomAsIncludingNilInterfaces(t *testing.T) {
	original := &predicateDomainError{Code: 37}
	asCalls, predicates := 0, 0
	matched := true
	actual := predicateAsError{as: func(target any) bool {
		asCalls++
		*target.(**predicateDomainError) = original
		return matched
	}}
	matcher := expectErrorValue(actual)
	predicate := func(value *predicateDomainError) bool { predicates++; return value == original }
	ErrorExpectationToMatchTypeSatisfying(matcher, predicate)
	matched = false
	NegatedErrorExpectationToMatchTypeSatisfying(matcher.Not, predicate)
	if asCalls != 2 || predicates != 1 {
		t.Fatal(asCalls, predicates)
	}
	// A native custom As can report success with a nil interface destination.
	nilMatch := expectErrorValue(predicateAsError{as: func(target any) bool { *target.(*fmt.Stringer) = nil; return true }})
	ErrorExpectationToMatchTypeSatisfying[fmt.Stringer](nilMatch, func(value fmt.Stringer) bool { return value == nil })
	NegatedErrorExpectationToMatchTypeSatisfying[fmt.Stringer](nilMatch.Not, func(value fmt.Stringer) bool { return value != nil })
}

func TestErrorTypePredicateValidationAndCallerLocation(t *testing.T) {
	asCalls, predicates := 0, 0
	matcher := expectErrorValue(predicateAsError{as: func(any) bool { asCalls++; return false }})
	assertThrownMessage(t, "predicate must not be nil", func() { ErrorExpectationToMatchTypeSatisfying[*predicateDomainError](matcher, nil) })
	assertThrownMessage(t, "predicate must not be nil", func() { NegatedErrorExpectationToMatchTypeSatisfying[*predicateDomainError](matcher.Not, nil) })
	for _, actual := range []error{nil, matcher.actual} {
		invalid := func(int) bool { predicates++; return true }
		assertThrownMessage(t, "requires an error type or interface, got int", func() { ErrorExpectationToMatchTypeSatisfying(expectErrorValue(actual), invalid) })
		assertThrownMessage(t, "requires an error type or interface, got int", func() { NegatedErrorExpectationToMatchTypeSatisfying(expectErrorValue(actual).Not, invalid) })
	}
	if asCalls != 0 || predicates != 0 {
		t.Fatal("validation touched callbacks", asCalls, predicates)
	}
	var want TestLocation
	record := func() {
		_, file, line, _ := runtime.Caller(1)
		want = TestLocation{File: file, Line: line + 1, Source: "go"}
	}
	domain := expectErrorValue(&predicateDomainError{})
	checks := []func(){
		func() {
			record()
			ErrorExpectationToMatchTypeSatisfying(domain, func(*predicateDomainError) bool { return false })
		},
		func() {
			record()
			NegatedErrorExpectationToMatchTypeSatisfying(domain.Not, func(*predicateDomainError) bool { return true })
		},
		func() {
			record()
			ErrorExpectationToMatchTypeSatisfying[*predicateDomainError](matcher, nil)
		},
		func() {
			record()
			NegatedErrorExpectationToMatchTypeSatisfying[*predicateDomainError](matcher.Not, nil)
		},
		func() {
			record()
			ErrorExpectationToMatchTypeSatisfying(matcher, func(int) bool { return true })
		},
		func() {
			record()
			NegatedErrorExpectationToMatchTypeSatisfying(matcher.Not, func(int) bool { return true })
		},
	}
	for _, check := range checks {
		failure := expectThrows(check)
		if got := failureLocation(failure); got != want {
			t.Fatal(got, want)
		}
	}
}

func TestErrorTypePredicatePreservesCallbackAndAsControlFlow(t *testing.T) {
	for _, negated := range []bool{false, true} {
		for _, inAs := range []bool{false, true} {
			for _, attached := range []bool{false, true} {
				cause := errors.New("callback outcome")
				if attached {
					cause = NewFailureError(cause, TestLocation{File: "predicate.km", Line: 42, Source: "explicit"})
				}
				body := func() { panic(__kinmokuseiThrown{err: cause}) }
				actual := error(&predicateDomainError{})
				if inAs {
					actual = predicateAsError{as: func(any) bool { body(); return false }}
				}
				predicate := func(*predicateDomainError) bool { body(); return false }
				check := func() {
					if negated {
						NegatedErrorExpectationToMatchTypeSatisfying(expectErrorValue(actual).Not, predicate)
					} else {
						ErrorExpectationToMatchTypeSatisfying(expectErrorValue(actual), predicate)
					}
				}
				if got := expectThrows(check); got != cause {
					t.Fatal("callback exception replaced", got, cause)
				}
			}
		}
	}
	for _, negated := range []bool{false, true} {
		check := func() {
			predicate := func(*predicateDomainError) bool { panic(__kinmokuseiThrown{err: nil}) }
			if negated {
				NegatedErrorExpectationToMatchTypeSatisfying(expectErrorValue(&predicateDomainError{}).Not, predicate)
			} else {
				ErrorExpectationToMatchTypeSatisfying(expectErrorValue(&predicateDomainError{}), predicate)
			}
		}
		if got := expectThrows(check); got != nil {
			t.Fatal("nil throw changed", got)
		}
	}
	for _, inAs := range []bool{false, true} {
		for _, nativePanic := range []bool{false, true} {
			cleaned := false
			suite := NewSuite("control flow")
			suite.TestWithContext("callback", func(context *TestContext) {
				context.Cleanup(func() { cleaned = true })
				body := func() {
					if nativePanic {
						panic("native callback")
					}
					skipTest("not available")
				}
				actual := error(&predicateDomainError{})
				if inAs {
					actual = predicateAsError{as: func(any) bool { body(); return false }}
				}
				ErrorExpectationToMatchTypeSatisfying(expectErrorValue(actual), func(*predicateDomainError) bool { body(); return false })
				t.Fatal("continued after callback")
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
				t.Fatal("callback bypassed cleanup")
			}
		}
	}
}
