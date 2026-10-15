package vasest

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"runtime"
	"strings"
	"testing"
)

func TestMapPredicateMatchersDistinguishMissingZeroAndNilEntries(t *testing.T) {
	for _, values := range []map[string]int{nil, {}, {"zero": 0, "positive": 42}} {
		matcher := expectMap(values)
		calls := 0
		predicate := func(value int) bool { calls++; return value == 0 }
		matcher.Not.ToHaveEntrySatisfying("missing", predicate)
		assertThrownMessage(t, "key is missing", func() { matcher.ToHaveEntrySatisfying("missing", predicate) })
		if calls != 0 {
			t.Fatal("missing key invoked predicate", calls)
		}
		for _, check := range []func(){func() { matcher.ToHaveEntrySatisfying("missing", nil) }, func() { matcher.Not.ToHaveEntrySatisfying("missing", nil) }} {
			assertThrownMessage(t, "predicate must not be nil", check)
		}
	}
	calls := 0
	matcher := NewMapExpectation(map[string]int{"zero": 0, "positive": 42})
	predicate := func(value int) bool { calls++; return value == 0 }
	matcher.ToHaveEntrySatisfying("zero", predicate)
	matcher.Not.ToHaveEntrySatisfying("positive", predicate)
	assertThrownMessage(t, "to satisfy predicate", func() { matcher.ToHaveEntrySatisfying("positive", predicate) })
	assertThrownMessage(t, "not to satisfy predicate", func() { matcher.Not.ToHaveEntrySatisfying("zero", predicate) })
	if calls != 4 {
		t.Fatal("predicate repeated or cached", calls)
	}
	pointers := map[string]*int{"nil": nil}
	expectMap(pointers).ToHaveEntrySatisfying("nil", func(value *int) bool { calls++; return value == nil })
	expectMap(pointers).Not.ToHaveEntrySatisfying("absent", func(*int) bool { t.Fatal("absent nil value evaluated"); return true })
	if calls != 5 {
		t.Fatal("present nil not evaluated", calls)
	}
}

func TestMapPredicateMatchersPreserveTypesAndNativeValueSemantics(t *testing.T) {
	type ID int64
	type Score int64
	id, score := ID(1<<60+1), Score(1<<60+2)
	values := map[ID]Score{id: score}
	matcher := expectMap[ID, Score](values)
	matcher.ToHaveEntrySatisfying(id, func(value Score) bool { return value == score })
	values[id] = -1
	matcher.Not.ToHaveEntrySatisfying(id, func(value Score) bool { return value > 0 })
	calls := 0
	// Lookup happens once: deleting or replacing the entry inside the callback
	// cannot change the selected value or trigger a second predicate evaluation.
	matcher.ToHaveEntrySatisfying(id, func(value Score) bool { calls++; delete(values, id); return value == -1 })
	values[id] = score
	matcher.Not.ToHaveEntrySatisfying(id, func(value Score) bool { calls++; values[id] = -1; return value < 0 })
	if calls != 2 || values[id] != -1 {
		t.Fatal(calls, values)
	}
	type record struct{ Value int }
	object := &record{42}
	objects := map[*record]*record{object: object}
	expectMap(objects).ToHaveEntrySatisfying(object, func(value *record) bool { value.Value++; return value == object })
	expectMap(objects).Not.ToHaveEntrySatisfying(&record{43}, func(*record) bool { t.Fatal("key identity collapsed"); return true })
	if object.Value != 43 {
		t.Fatal("reference changed", object)
	}
	type valueRecord struct{ Value int }
	records := map[string]valueRecord{"record": {42}}
	expectMap(records).ToHaveEntrySatisfying("record", func(value valueRecord) bool { value.Value++; return value.Value == 43 })
	if records["record"].Value != 42 {
		t.Fatal("value struct became a reference")
	}
	nested := map[string][]int{"nil": nil, "empty": {}, "values": {42}}
	expectMap(nested).ToHaveEntrySatisfying("nil", func(value []int) bool { return value == nil })
	expectMap(nested).ToHaveEntrySatisfying("empty", func(value []int) bool { return value != nil && len(value) == 0 })
	expectMap(nested).ToHaveEntrySatisfying("values", func(value []int) bool { value[0]++; return true })
	if nested["values"][0] != 43 {
		t.Fatal("nested reference copied")
	}
	nan := math.NaN()
	nanMap := map[float64]int{nan: 42}
	expectMap(nanMap).Not.ToHaveEntrySatisfying(nan, func(int) bool { t.Fatal("NaN key unexpectedly found"); return true })
	assertThrownMessage(t, "key is missing", func() {
		expectMap(nanMap).ToHaveEntrySatisfying(nan, func(int) bool { t.Fatal("NaN key unexpectedly found"); return true })
	})
}

func TestMapPredicateMatchersIdentifyEveryCaller(t *testing.T) {
	matcher := expectMap(map[string]int{"value": 42})
	yes, no := func(int) bool { return true }, func(int) bool { return false }
	var want TestLocation
	record := func() {
		_, file, line, _ := runtime.Caller(1)
		want = TestLocation{File: file, Line: line, Source: "go"}
	}
	for _, check := range []func(){
		func() { record(); matcher.ToHaveEntrySatisfying("value", no) }, func() { record(); matcher.Not.ToHaveEntrySatisfying("value", yes) },
		func() { record(); matcher.ToHaveEntrySatisfying("absent", yes) },
		func() { record(); matcher.ToHaveEntrySatisfying("value", nil) }, func() { record(); matcher.Not.ToHaveEntrySatisfying("value", nil) },
		func() { record(); matcher.ToHaveEntrySatisfying("absent", nil) }, func() { record(); matcher.Not.ToHaveEntrySatisfying("absent", nil) },
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

func TestMapPredicateMatchersPropagateCallbackControlFlow(t *testing.T) {
	for _, negated := range []bool{false, true} {
		check := func(predicate func(int) bool) {
			matcher := expectMap(map[string]int{"value": 42})
			if negated {
				matcher.Not.ToHaveEntrySatisfying("value", predicate)
			} else {
				matcher.ToHaveEntrySatisfying("value", predicate)
			}
		}
		for _, exception := range []error{nil, errors.New("predicate threw"), NewFailureError(errors.New("located"), TestLocation{File: "domain.km", Line: 42, Source: "explicit"})} {
			func() {
				calls := 0
				defer func() {
					thrown, ok := recover().(__kinmokuseiThrown)
					if !ok || thrown.err != exception || calls != 1 {
						t.Fatal("callback exception changed", thrown, exception, calls)
					}
				}()
				check(func(int) bool { calls++; panic(__kinmokuseiThrown{err: exception}) })
			}()
		}
		for _, mode := range []string{"throw", "skip", "panic", "key panic"} {
			func() {
				var events []string
				calls := 0
				native := mode == "panic" || mode == "key panic"
				if native {
					defer func() {
						recovered := recover()
						if mode == "panic" && recovered != "map predicate panic" {
							t.Fatal("callback panic changed", recovered)
						}
						if mode == "key panic" && !strings.Contains(fmt.Sprint(recovered), "hash of unhashable type []int") {
							t.Fatal("lookup panic changed", recovered)
						}
						if fmt.Sprint(events) != "[afterEach cleanup afterAll]" || (mode == "panic" && calls != 1) || (mode == "key panic" && calls != 0) {
							t.Fatal(events, calls)
						}
					}()
				}
				suite := NewSuite("map predicate cleanup")
				suite.AfterEach(func() { events = append(events, "afterEach") })
				suite.AfterAll(func() { events = append(events, "afterAll") })
				suite.TestWithContext("case", func(context *TestContext) {
					context.Cleanup(func() { events = append(events, "cleanup") })
					if mode == "key panic" {
						matcher := expectMap(map[any]int{"value": 42})
						predicate := func(int) bool { calls++; return true }
						if negated {
							matcher.Not.ToHaveEntrySatisfying([]int{1}, predicate)
						} else {
							matcher.ToHaveEntrySatisfying([]int{1}, predicate)
						}
						return
					}
					check(func(int) bool {
						calls++
						switch mode {
						case "panic":
							panic("map predicate panic")
						case "skip":
							skipTest("map predicate unavailable")
						case "throw":
							withTestLocation("domain.km", 42, func() { panic(__kinmokuseiThrown{err: errors.New("located")}) })
						}
						t.Fatal("callback control flow swallowed")
						return true
					})
				})
				summary := suite.RunTo(&bytes.Buffer{})
				if native {
					t.Fatal("native panic swallowed")
				}
				if calls != 1 || fmt.Sprint(events) != "[afterEach cleanup afterAll]" {
					t.Fatal(summary, events, calls)
				}
				if mode == "skip" {
					if summary.Skipped != 1 || summary.Failed != 0 || !strings.Contains(summary.Results[0].Message, "map predicate unavailable") {
						t.Fatal(summary)
					}
				} else if summary.Failed != 1 || summary.Results[0].Failures[0].Location != (TestLocation{File: "domain.km", Line: 42, Source: "explicit"}) {
					t.Fatal(summary)
				}
			}()
		}
	}
	// Validation precedes even a lookup that would otherwise panic.
	matcher := expectMap(map[any]int{"value": 42})
	assertThrownMessage(t, "predicate must not be nil", func() { matcher.ToHaveEntrySatisfying([]int{1}, nil) })
	assertThrownMessage(t, "predicate must not be nil", func() { matcher.Not.ToHaveEntrySatisfying([]int{1}, nil) })
}
