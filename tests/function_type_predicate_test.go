package vasest

import (
	"bytes"
	"errors"
	"fmt"
	"runtime"
	"testing"
)

func TestFunctionTypePredicateNegatesThrowTypeAndPredicate(t *testing.T) {
	for _, threw := range []bool{false, true} {
		for _, present := range []bool{false, true} {
			for _, accepted := range []bool{false, true} {
				for _, negated := range []bool{false, true} {
					original := &predicateDomainError{Code: 1<<60 + 1}
					var actual error = errors.New("other")
					if present {
						actual = original
					}
					bodies, predicates := 0, 0
					matcher := expectFunction(func() {
						bodies++
						if threw {
							panic(__kinmokuseiThrown{err: actual})
						}
					})
					if bodies != 0 {
						t.Fatal("eager body invocation")
					}
					predicate := func(value *predicateDomainError) bool {
						predicates++
						if value != original || value.Code != 1<<60+1 {
							t.Fatal("lost native type/reference")
						}
						return accepted
					}
					check := func() {
						if negated {
							NegatedFunctionExpectationToThrowTypeSatisfying(matcher.Not, predicate)
						} else {
							FunctionExpectationToThrowTypeSatisfying(matcher, predicate)
						}
					}
					if (threw && present && accepted) == negated {
						assertThrownMessage(t, "expected function", check)
					} else {
						check()
					}
					want := 0
					if threw && present {
						want = 1
					}
					if bodies != 1 || predicates != want || original.Reads != 0 {
						t.Fatal("unexpected execution/formatting", bodies, predicates, original.Reads)
					}
				}
			}
		}
	}
}

func TestFunctionTypePredicateNativeSelectionAndFreshBody(t *testing.T) {
	original := &predicateDomainError{Code: 1<<60 + 1}
	calls := 0
	matcher := expectFunction(func() {
		calls++
		if calls <= 2 {
			panic(__kinmokuseiThrown{err: original})
		}
	})
	FunctionExpectationToThrowTypeSatisfying(matcher, func(value *predicateDomainError) bool { value.Code++; return value == original })
	FunctionExpectationToThrowTypeSatisfying(matcher, func(value *predicateDomainError) bool { return value.Code == 1<<60+2 })
	NegatedFunctionExpectationToThrowTypeSatisfying(matcher.Not, func(*predicateDomainError) bool { t.Fatal("normal return ran predicate"); return true })
	if calls != 3 {
		t.Fatal(calls)
	}
	wrapped := errors.Join(errors.New("other"), original)
	FunctionExpectationToThrowTypeSatisfying(expectFunction(func() { panic(__kinmokuseiThrown{err: wrapped}) }), func(value *predicateDomainError) bool { return value == original })
	FunctionExpectationToThrowTypeSatisfying[fmt.Stringer](expectFunction(func() { panic(__kinmokuseiThrown{err: wrapped}) }), func(value fmt.Stringer) bool { return value == original })
	first := &predicateDomainError{Code: 1}
	NegatedFunctionExpectationToThrowTypeSatisfying(expectFunction(func() { panic(__kinmokuseiThrown{err: errors.Join(first, original)}) }).Not, func(value *predicateDomainError) bool { return value == original })
	FunctionExpectationToThrowTypeSatisfying(expectFunction(func() { panic(__kinmokuseiThrown{err: (*predicateDomainError)(nil)}) }), func(value *predicateDomainError) bool { return value == nil })
	nilThrow := expectFunction(func() { panic(__kinmokuseiThrown{err: nil}) })
	NegatedFunctionExpectationToThrowTypeSatisfying[error](nilThrow.Not, func(error) bool { t.Fatal("nil payload matched error interface"); return true })
	assertThrownMessage(t, "assignable to error", func() { FunctionExpectationToThrowTypeSatisfying[error](nilThrow, func(error) bool { return true }) })
	values := assertionSliceError{1, 2}
	FunctionExpectationToThrowTypeSatisfying(expectFunction(func() { panic(__kinmokuseiThrown{err: values}) }), func(value assertionSliceError) bool { value[0] = 7; return len(value) == 2 })
	if values[0] != 7 || original.Reads != 0 {
		t.Fatal(values, original.Reads)
	}
	// Body skip is a thrown outcome; predicate skip is enclosing-suite control flow.
	FunctionExpectationToThrowTypeSatisfying[error](expectFunction(func() { skipTest("body unavailable") }), func(value error) bool { return testSkipReason(value) == "body unavailable" })
}

func TestFunctionTypePredicateCustomAs(t *testing.T) {
	original := &predicateDomainError{Code: 37}
	matched := true
	bodies, extractions, predicates := 0, 0, 0
	actual := predicateAsError{as: func(target any) bool { extractions++; *target.(**predicateDomainError) = original; return matched }}
	matcher := expectFunction(func() { bodies++; panic(__kinmokuseiThrown{err: actual}) })
	predicate := func(value *predicateDomainError) bool { predicates++; return value == original }
	FunctionExpectationToThrowTypeSatisfying(matcher, predicate)
	matched = false
	NegatedFunctionExpectationToThrowTypeSatisfying(matcher.Not, predicate)
	if bodies != 2 || extractions != 2 || predicates != 1 {
		t.Fatal(bodies, extractions, predicates)
	}
	nilMatch := predicateAsError{as: func(target any) bool { *target.(*fmt.Stringer) = nil; return true }}
	FunctionExpectationToThrowTypeSatisfying[fmt.Stringer](expectFunction(func() { panic(__kinmokuseiThrown{err: nilMatch}) }), func(value fmt.Stringer) bool { return value == nil })
}

func TestFunctionTypePredicateValidationBeforeBodyAndCallerLocation(t *testing.T) {
	bodies, predicates := 0, 0
	matcher := expectFunction(func() { bodies++ })
	assertThrownMessage(t, "predicate must not be nil", func() { FunctionExpectationToThrowTypeSatisfying[error](matcher, nil) })
	assertThrownMessage(t, "predicate must not be nil", func() { NegatedFunctionExpectationToThrowTypeSatisfying[error](matcher.Not, nil) })
	invalid := func(int) bool { predicates++; return true }
	assertThrownMessage(t, "toThrowTypeSatisfying requires an error type or interface", func() { FunctionExpectationToThrowTypeSatisfying(matcher, invalid) })
	assertThrownMessage(t, "toThrowTypeSatisfying requires an error type or interface", func() { NegatedFunctionExpectationToThrowTypeSatisfying(matcher.Not, invalid) })
	valid := func(error) bool { predicates++; return true }
	assertThrownMessage(t, "body must not be nil", func() { FunctionExpectationToThrowTypeSatisfying(expectFunction(nil), valid) })
	assertThrownMessage(t, "body must not be nil", func() { NegatedFunctionExpectationToThrowTypeSatisfying(expectFunction(nil).Not, valid) })
	if bodies != 0 || predicates != 0 {
		t.Fatal("invalid matcher invoked callback", bodies, predicates)
	}
	var want TestLocation
	record := func() {
		_, file, line, _ := runtime.Caller(1)
		want = TestLocation{File: file, Line: line + 1, Source: "go"}
	}
	thrown := expectFunction(func() { panic(__kinmokuseiThrown{err: errors.New("body")}) })
	checks := []func(){
		func() {
			record()
			FunctionExpectationToThrowTypeSatisfying[error](thrown, func(error) bool { return false })
		},
		func() {
			record()
			NegatedFunctionExpectationToThrowTypeSatisfying[error](thrown.Not, valid)
		},
		func() {
			record()
			FunctionExpectationToThrowTypeSatisfying[error](matcher, valid)
		},
		func() {
			record()
			FunctionExpectationToThrowTypeSatisfying[*predicateDomainError](thrown, func(*predicateDomainError) bool { return true })
		},
		func() {
			record()
			FunctionExpectationToThrowTypeSatisfying[error](matcher, nil)
		},
		func() {
			record()
			NegatedFunctionExpectationToThrowTypeSatisfying[error](matcher.Not, nil)
		},
		func() {
			record()
			FunctionExpectationToThrowTypeSatisfying(matcher, invalid)
		},
		func() {
			record()
			NegatedFunctionExpectationToThrowTypeSatisfying(matcher.Not, invalid)
		},
		func() {
			record()
			FunctionExpectationToThrowTypeSatisfying(expectFunction(nil), valid)
		},
		func() {
			record()
			NegatedFunctionExpectationToThrowTypeSatisfying(expectFunction(nil).Not, valid)
		},
	}
	for _, check := range checks {
		if got := failureLocation(expectThrows(check)); got != want {
			t.Fatal(got, want)
		}
	}
}

func TestFunctionTypePredicatePreservesAsAndPredicateExceptions(t *testing.T) {
	for _, negated := range []bool{false, true} {
		for _, inAs := range []bool{false, true} {
			for _, cause := range []error{nil, errors.New("predicate"), NewFailureError(errors.New("located"), TestLocation{File: "predicate.km", Line: 42, Source: "explicit"})} {
				body := func() { panic(__kinmokuseiThrown{err: cause}) }
				actual := error(&predicateDomainError{})
				if inAs {
					actual = predicateAsError{as: func(any) bool { body(); return false }}
				}
				matcher := expectFunction(func() { panic(__kinmokuseiThrown{err: actual}) })
				predicate := func(*predicateDomainError) bool { body(); return false }
				check := func() {
					if negated {
						NegatedFunctionExpectationToThrowTypeSatisfying(matcher.Not, predicate)
					} else {
						FunctionExpectationToThrowTypeSatisfying(matcher, predicate)
					}
				}
				if got := expectThrows(check); got != cause {
					t.Fatal("callback exception replaced", got, cause)
				}
			}
		}
	}
}

func TestFunctionTypePredicateControlFlowCleanup(t *testing.T) {
	for _, phase := range []string{"body", "As", "predicate"} {
		for _, negated := range []bool{false, true} {
			for _, nativePanic := range []bool{false, true} {
				cleaned := false
				suite := NewSuite("control flow")
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
					matcher := expectFunction(func() {
						if phase == "body" {
							control()
						}
						panic(__kinmokuseiThrown{err: actual})
					})
					predicate := func(error) bool {
						if phase == "predicate" {
							control()
						}
						return !negated
					}
					if phase == "As" {
						selected := func(*predicateDomainError) bool { return !negated }
						if negated {
							NegatedFunctionExpectationToThrowTypeSatisfying(matcher.Not, selected)
						} else {
							FunctionExpectationToThrowTypeSatisfying(matcher, selected)
						}
					} else {
						if negated {
							NegatedFunctionExpectationToThrowTypeSatisfying[error](matcher.Not, predicate)
						} else {
							FunctionExpectationToThrowTypeSatisfying[error](matcher, predicate)
						}
					}
					if phase != "body" || nativePanic {
						t.Fatal("continued after callback control flow")
					}
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
					wantSkip, wantPass := 1, 0
					if phase == "body" {
						wantSkip, wantPass = 0, 1
					}
					if result.Skipped != wantSkip || result.Passed != wantPass || result.Failed != 0 {
						t.Fatal(phase, negated, result)
					}
				}
				if !cleaned {
					t.Fatal("callback bypassed cleanup")
				}
			}
		}
	}
}
