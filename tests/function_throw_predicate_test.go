package vasest

import (
	"bytes"
	"errors"
	"os"
	"runtime"
	"testing"
)

type uninspectedFunctionError struct {
	Code  int64
	Reads int
}

func (err *uninspectedFunctionError) Error() string { err.Reads++; panic("matcher read Error") }
func (err *uninspectedFunctionError) Is(error) bool { err.Reads++; panic("matcher called Is") }
func (err *uninspectedFunctionError) As(any) bool   { err.Reads++; panic("matcher called As") }

func TestFunctionThrowPredicateInvokesBodyOnceAndNegatesResult(t *testing.T) {
	for _, threw := range []bool{false, true} {
		for _, matches := range []bool{false, true} {
			for _, negated := range []bool{false, true} {
				bodies, predicates := 0, 0
				cause := errors.New("original")
				matcher := NewFunctionExpectation(func() {
					bodies++
					if threw {
						panic(__kinmokuseiThrown{err: cause})
					}
				})
				if bodies != 0 {
					t.Fatal("constructor invoked body")
				}
				predicate := func(actual error) bool {
					predicates++
					if actual != cause {
						t.Fatal("exception changed")
					}
					return matches
				}
				check := func() {
					if negated {
						matcher.Not.ToThrowSatisfying(predicate)
					} else {
						matcher.ToThrowSatisfying(predicate)
					}
				}
				if (threw && matches) == negated {
					assertThrownMessage(t, "expected function", check)
				} else {
					check()
				}
				want := 0
				if threw {
					want = 1
				}
				if bodies != 1 || predicates != want {
					t.Fatal("body/predicate repeated", threw, matches, negated, bodies, predicates)
				}
			}
		}
	}
	// Saved matchers evaluate the body anew rather than caching its first throw.
	count := 0
	matcher := expectFunction(func() {
		count++
		if count == 1 {
			panic(__kinmokuseiThrown{err: errors.New("first")})
		}
	})
	matcher.ToThrowSatisfying(func(actual error) bool { return actual.Error() == "first" })
	matcher.Not.ToThrowSatisfying(func(error) bool { t.Fatal("normal return evaluated predicate"); return false })
	if count != 2 {
		t.Fatal(count)
	}
}

func TestFunctionThrowPredicatePreservesNativeErrorsAndDoesNotInspectThem(t *testing.T) {
	original := &uninspectedFunctionError{Code: 1<<60 + 1}
	matcher := expectFunction(func() { panic(__kinmokuseiThrown{err: original}) })
	matcher.ToThrowSatisfying(func(actual error) bool {
		if actual != original {
			t.Fatal("lost error identity")
		}
		original.Code++
		return true
	})
	if original.Code != 1<<60+2 {
		t.Fatal("lost native integer/reference", original.Code)
	}
	assertThrownMessage(t, "satisfying predicate", func() { matcher.ToThrowSatisfying(func(error) bool { return false }) })
	assertThrownMessage(t, "not to throw", func() { matcher.Not.ToThrowSatisfying(func(error) bool { return true }) })
	if original.Reads != 0 {
		t.Fatal("matcher inspected native error methods", original.Reads)
	}
	for _, cause := range []error{nil, (*os.PathError)(nil)} {
		expectFunction(func() { panic(__kinmokuseiThrown{err: cause}) }).ToThrowSatisfying(func(actual error) bool { return actual == cause })
	}
	cause := errors.New("cause")
	wrapped := errors.Join(errors.New("other"), cause)
	expectFunction(func() { panic(__kinmokuseiThrown{err: wrapped}) }).ToThrowSatisfying(func(actual error) bool { return actual == wrapped && errors.Is(actual, cause) })
	// Body skip signals are thrown outcomes, like other function matchers.
	expectFunction(func() { skipTest("body unavailable") }).ToThrowSatisfying(func(actual error) bool { return testSkipReason(actual) == "body unavailable" })
}

func TestFunctionThrowPredicateValidatesBeforeCallbacksAndReportsCaller(t *testing.T) {
	calls := 0
	matcher := expectFunction(func() { calls++; panic(__kinmokuseiThrown{err: errors.New("body")}) })
	assertThrownMessage(t, "predicate must not be nil", func() { matcher.ToThrowSatisfying(nil) })
	assertThrownMessage(t, "predicate must not be nil", func() { matcher.Not.ToThrowSatisfying(nil) })
	if calls != 0 {
		t.Fatal("invalid predicate invoked body")
	}
	predicate := func(error) bool { calls++; return true }
	assertThrownMessage(t, "body must not be nil", func() { NewFunctionExpectation(nil).ToThrowSatisfying(predicate) })
	assertThrownMessage(t, "body must not be nil", func() { NewNegatedFunctionExpectation(nil).ToThrowSatisfying(predicate) })
	if calls != 0 {
		t.Fatal("invalid body invoked predicate")
	}
	var want TestLocation
	record := func() {
		_, file, line, _ := runtime.Caller(1)
		want = TestLocation{File: file, Line: line, Source: "go"}
	}
	checks := []func(){
		func() { record(); matcher.ToThrowSatisfying(func(error) bool { return false }) },
		func() { record(); matcher.Not.ToThrowSatisfying(func(error) bool { return true }) },
		func() { record(); expectFunction(func() {}).ToThrowSatisfying(predicate) },
		func() { record(); matcher.ToThrowSatisfying(nil) },
		func() { record(); matcher.Not.ToThrowSatisfying(nil) },
		func() { record(); expectFunction(nil).ToThrowSatisfying(predicate) },
		func() { record(); expectFunction(nil).Not.ToThrowSatisfying(predicate) },
	}
	for _, check := range checks {
		func() {
			defer func() {
				thrown, ok := recover().(__kinmokuseiThrown)
				if !ok || failureLocation(thrown.err) != want {
					t.Fatal("fluent caller location", thrown, want)
				}
			}()
			check()
		}()
	}
}

func TestFunctionThrowPredicatePropagatesPredicateControlFlow(t *testing.T) {
	for _, negated := range []bool{false, true} {
		for _, kind := range []string{"throw", "nil throw", "location", "skip", "panic"} {
			matcher := expectFunction(func() { panic(__kinmokuseiThrown{err: errors.New("body outcome")}) })
			cause := errors.New("predicate failure")
			var predicateError error
			predicate := func(error) bool {
				switch kind {
				case "throw":
					predicateError = cause
					panic(__kinmokuseiThrown{err: cause})
				case "nil throw":
					panic(__kinmokuseiThrown{err: nil})
				case "location":
					withTestLocation("predicate.km", 42, func() { panic(__kinmokuseiThrown{err: cause}) })
				case "skip":
					skipTest("predicate unavailable")
				case "panic":
					panic("native predicate panic")
				}
				return false
			}
			func() {
				defer func() {
					got := recover()
					if kind == "panic" {
						if got != "native predicate panic" {
							t.Fatal(got)
						}
						return
					}
					thrown, ok := got.(__kinmokuseiThrown)
					if !ok {
						t.Fatal("predicate throw replaced", got)
					}
					switch kind {
					case "throw", "nil throw":
						if thrown.err != predicateError {
							t.Fatal("predicate error identity changed")
						}
					case "location":
						if !errors.Is(thrown.err, cause) || failureLocation(thrown.err) != (TestLocation{File: "predicate.km", Line: 42, Source: "explicit"}) {
							t.Fatal(thrown.err)
						}
					case "skip":
						if testSkipReason(thrown.err) != "predicate unavailable" {
							t.Fatal(thrown.err)
						}
					}
				}()
				if negated {
					matcher.Not.ToThrowSatisfying(predicate)
				} else {
					matcher.ToThrowSatisfying(predicate)
				}
				t.Fatal("predicate control flow swallowed")
			}()
		}
	}
}

func TestFunctionThrowPredicateNativePanicsStillUnwindCleanup(t *testing.T) {
	for _, phase := range []string{"body", "predicate"} {
		suite := NewSuite("function throw predicate panic")
		cleaned, after, afterAll, predicates := false, false, false, 0
		suite.AfterEach(func() { after = true })
		suite.AfterAll(func() { afterAll = true })
		suite.TestWithContext("row", func(context *TestContext) {
			context.Cleanup(func() { cleaned = true })
			expectFunction(func() {
				if phase == "body" {
					panic("native body panic")
				}
				panic(__kinmokuseiThrown{err: errors.New("body outcome")})
			}).ToThrowSatisfying(func(error) bool { predicates++; panic("native predicate panic") })
		})
		func() {
			defer func() {
				got := recover()
				if got != "native "+phase+" panic" {
					t.Fatal(got)
				}
			}()
			suite.RunTo(&bytes.Buffer{})
			t.Fatal("native panic swallowed")
		}()
		want := 0
		if phase == "predicate" {
			want = 1
		}
		if !cleaned || !after || !afterAll || predicates != want {
			t.Fatal("panic cleanup", phase, cleaned, after, afterAll, predicates)
		}
	}
}
