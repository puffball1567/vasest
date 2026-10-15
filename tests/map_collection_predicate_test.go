package vasest

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"reflect"
	"runtime"
	"testing"
)

func checkMapCollection[K comparable, V any](matcher *MapExpectation[K, V], all, negated bool, predicate func(K, V) bool) {
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

func TestMapCollectionPredicateAggregateAndShortCircuit(t *testing.T) {
	for _, tc := range []struct {
		name      string
		values    map[string]int
		predicate func(string, int) bool
		any, all  bool
	}{
		{"nil", nil, func(string, int) bool { return true }, false, true},
		{"empty", map[string]int{}, func(string, int) bool { return false }, false, true},
		{"all", map[string]int{"a": 1, "b": 2, "c": 3}, func(string, int) bool { return true }, true, true},
		{"none", map[string]int{"a": 1, "b": 2, "c": 3}, func(string, int) bool { return false }, false, false},
		{"mixed", map[string]int{"a": 1, "b": 2, "c": 3}, func(key string, value int) bool { return key == "b" && value == 2 }, true, false},
	} {
		for _, all := range []bool{false, true} {
			for _, negated := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/all=%t/not=%t", tc.name, all, negated), func(t *testing.T) {
					visited := map[string]bool{}
					decisive := false
					predicate := func(key string, value int) bool {
						if decisive || visited[key] || tc.values[key] != value {
							t.Fatal("pair lost, repeated, or visited after decision", key, value)
						}
						visited[key] = true
						result := tc.predicate(key, value)
						decisive = result != all
						return result
					}
					matcher := expectMap(tc.values)
					if len(visited) != 0 {
						t.Fatal("construction evaluated predicate")
					}
					check := func() { checkMapCollection(matcher, all, negated, predicate) }
					matches := tc.any
					if all {
						matches = tc.all
					}
					if matches == negated {
						assertThrownMessage(t, "to satisfy predicate", check)
					} else {
						check()
					}
					if !decisive && len(visited) != len(tc.values) {
						t.Fatal("skipped an entry without a decision", visited)
					}
					if len(tc.values) != 0 && ((all && tc.name == "none") || (!all && tc.name == "all")) && len(visited) != 1 {
						t.Fatal("no short circuit", visited)
					}
				})
			}
		}
	}
}

func TestMapCollectionPredicateNativePairsAndLiveValues(t *testing.T) {
	type ID uint64
	type Score int64
	id, score := ID(math.MaxUint64), Score(1<<60+1)
	values := map[ID]Score{id: score}
	matcher := NewMapExpectation[ID, Score](values)
	predicate := func(key ID, value Score) bool { return key == id && value == score }
	matcher.ToSatisfyAny(predicate)
	values[id] = -1
	matcher.Not.ToSatisfyAll(predicate)
	matcher.Not.ToSatisfyAny(predicate)
	values[id] = score
	matcher.ToSatisfyAll(predicate)
	// No second lookup: NaN keys cannot be looked up, but range returns their values.
	nan := math.NaN()
	nans := map[float64]int{}
	nans[nan], nans[nan] = 42, 43
	var seen []int
	expectMap(nans).ToSatisfyAll(func(key float64, value int) bool {
		seen = append(seen, value)
		return math.IsNaN(key) && (value == 42 || value == 43)
	})
	if len(seen) != 2 || seen[0] == seen[1] {
		t.Fatal("NaN entries lost", nans, seen)
	}
	expectMap(map[any]any{nil: nil}).ToSatisfyAll(func(key, value any) bool { return key == nil && value == nil })
	type record struct{ Value int }
	object := &record{42}
	expectMap(map[*record]*record{object: object}).ToSatisfyAll(func(key, value *record) bool {
		value.Value++
		return key == object && value == object
	})
	if object.Value != 43 {
		t.Fatal("reference changed", object)
	}
	records := map[record]record{{42}: {43}}
	expectMap(records).ToSatisfyAll(func(key, value record) bool { key.Value++; value.Value++; return key.Value == 43 && value.Value == 44 })
	if records[record{42}].Value != 43 {
		t.Fatal("value became a reference")
	}
	nested := map[string][]int{"nil": nil, "empty": {}, "values": {42}}
	expectMap(nested).ToSatisfyAll(func(key string, value []int) bool {
		switch key {
		case "nil":
			return value == nil
		case "empty":
			return value != nil && len(value) == 0
		default:
			value[0]++
			return true
		}
	})
	if nested["values"][0] != 43 {
		t.Fatal("nested reference copied")
	}
	// Native iteration skips entries deleted before they are visited.
	mutable := map[int]int{1: 42, 2: 42, 3: 42}
	calls := 0
	expectMap(mutable).ToSatisfyAll(func(key, value int) bool { calls++; clear(mutable); return value == 42 })
	if calls != 1 || len(mutable) != 0 {
		t.Fatal("iteration was copied or retried", calls, mutable)
	}
	mutable[1] = 42
	expectMap(mutable).ToSatisfyAny(func(key, value int) bool { mutable[key] = 99; return value == 42 })
	if mutable[1] != 99 {
		t.Fatal(mutable)
	}
}

type mapCollectionFormattingKey struct {
	Value int
	Reads *int
}

func (key mapCollectionFormattingKey) String() string {
	(*key.Reads)++
	panic("must not format an entry")
}

func TestMapCollectionPredicateValidationDiagnosticsAndLocations(t *testing.T) {
	for _, values := range []map[string]int{nil, {}, {"a": 42}} {
		for _, all := range []bool{false, true} {
			for _, negated := range []bool{false, true} {
				assertThrownMessage(t, "predicate must not be nil", func() { checkMapCollection(expectMap(values), all, negated, nil) })
			}
		}
	}
	for _, all := range []bool{false, true} {
		for _, negated := range []bool{false, true} {
			reads := 0
			assertThrownMessage(t, "checked 1 entry(s)", func() {
				checkMapCollection(expectMap(map[mapCollectionFormattingKey]mapCollectionFormattingKey{{42, &reads}: {43, &reads}}), all, negated, func(mapCollectionFormattingKey, mapCollectionFormattingKey) bool { return negated })
			})
			if reads != 0 {
				t.Fatal("mismatch formatted an entry", reads)
			}
		}
	}
	matcher := expectMap(map[string]int{"a": 42})
	yes, no := func(string, int) bool { return true }, func(string, int) bool { return false }
	var want TestLocation
	record := func() {
		_, file, line, _ := runtime.Caller(1)
		want = TestLocation{File: file, Line: line + 1, Source: "go"}
	}
	for _, check := range []func(){
		func() {
			record()
			matcher.ToSatisfyAny(no)
		},
		func() {
			record()
			matcher.Not.ToSatisfyAny(yes)
		},
		func() {
			record()
			matcher.ToSatisfyAll(no)
		},
		func() {
			record()
			matcher.Not.ToSatisfyAll(yes)
		},
		func() {
			record()
			matcher.ToSatisfyAny(nil)
		},
		func() {
			record()
			matcher.Not.ToSatisfyAny(nil)
		},
		func() {
			record()
			matcher.ToSatisfyAll(nil)
		},
		func() {
			record()
			matcher.Not.ToSatisfyAll(nil)
		},
	} {
		func() {
			defer func() {
				thrown, ok := recover().(__kinmokuseiThrown)
				if !ok || failureLocation(thrown.err) != want {
					t.Fatal("caller location", thrown, want)
				}
			}()
			check()
		}()
	}
}

func TestMapCollectionPredicateCallbackControlFlow(t *testing.T) {
	for _, all := range []bool{false, true} {
		for _, negated := range []bool{false, true} {
			for _, exception := range []error{nil, errors.New("callback error"), NewFailureError(errors.New("located"), TestLocation{File: "domain.km", Line: 42, Source: "explicit"})} {
				func() {
					calls := 0
					defer func() {
						thrown, ok := recover().(__kinmokuseiThrown)
						if !ok || thrown.err != exception || calls != 1 {
							t.Fatal("callback throw changed", thrown, exception, calls)
						}
					}()
					checkMapCollection(expectMap(map[int]int{1: 42, 2: 43}), all, negated, func(int, int) bool { calls++; panic(__kinmokuseiThrown{err: exception}) })
				}()
			}
			for _, mode := range []string{"skip", "throw", "panic"} {
				func() {
					var events []string
					calls := 0
					if mode == "panic" {
						defer func() {
							if recover() != "map collection panic" || !reflect.DeepEqual(events, []string{"afterEach", "cleanup", "afterAll"}) || calls != 1 {
								t.Fatal("panic cleanup", events, calls)
							}
						}()
					}
					suite := NewSuite("map collection cleanup")
					suite.AfterEach(func() { events = append(events, "afterEach") })
					suite.AfterAll(func() { events = append(events, "afterAll") })
					suite.TestWithContext("case", func(context *TestContext) {
						context.Cleanup(func() { events = append(events, "cleanup") })
						checkMapCollection(expectMap(map[int]int{1: 42, 2: 43}), all, negated, func(int, int) bool {
							calls++
							switch mode {
							case "skip":
								skipTest("map unavailable")
							case "throw":
								withTestLocation("domain.km", 42, func() { panic(__kinmokuseiThrown{err: errors.New("domain failure")}) })
							case "panic":
								panic("map collection panic")
							}
							t.Fatal("callback control flow swallowed")
							return true
						})
					})
					result := suite.RunTo(&bytes.Buffer{})
					if mode == "panic" {
						t.Fatal("native panic swallowed")
					}
					if calls != 1 || !reflect.DeepEqual(events, []string{"afterEach", "cleanup", "afterAll"}) {
						t.Fatal(result, events, calls)
					}
					if mode == "skip" {
						if result.Skipped != 1 || result.Failed != 0 {
							t.Fatal(result)
						}
					} else if result.Failed != 1 || result.Results[0].Failures[0].Location != (TestLocation{File: "domain.km", Line: 42, Source: "explicit"}) {
						t.Fatal(result)
					}
				}()
			}
		}
	}
}
