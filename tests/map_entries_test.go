package vasest

import (
	"bytes"
	"fmt"
	"math"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestMapEntriesExhaustiveSubsetAndAggregateNegation(t *testing.T) {
	// Independently enumerate missing/zero/one entries for two keys.
	for actualCode := 0; actualCode < 9; actualCode++ {
		for expectedCode := 0; expectedCode < 9; expectedCode++ {
			actual, expected := map[int]int{}, map[int]int{}
			matches := true
			for key := 0; key < 2; key++ {
				divisor := 1
				if key == 1 {
					divisor = 3
				}
				a, e := actualCode/divisor%3, expectedCode/divisor%3
				if a != 0 {
					actual[key] = a - 1
				}
				if e != 0 {
					expected[key] = e - 1
					matches = matches && a == e
				}
			}
			beforeActual, beforeExpected := fmt.Sprint(actual), fmt.Sprint(expected)
			matcher := expectMap(actual)
			if matches {
				matcher.ToHaveEntries(expected)
				assertThrownMessage(t, "not to have entries", func() { matcher.Not.ToHaveEntries(expected) })
			} else {
				matcher.Not.ToHaveEntries(expected)
				assertThrownMessage(t, "to have entries", func() { matcher.ToHaveEntries(expected) })
			}
			if fmt.Sprint(actual) != beforeActual || fmt.Sprint(expected) != beforeExpected {
				t.Fatal("entry checks mutated input", actual, expected)
			}
		}
	}
}

func TestMapEntriesPreserveEmptyRequirementsAndNativeTypes(t *testing.T) {
	for _, actual := range []map[string]int{nil, {}, {"zero": 0}} {
		for _, required := range []map[string]int{nil, {}} {
			NewMapExpectation(actual).ToHaveEntries(required)
			assertThrownMessage(t, "not to have entries", func() { expectMap(actual).Not.ToHaveEntries(required) })
		}
		expectMap(actual).Not.ToHaveEntries(map[string]int{"absent": 0})
	}
	type ID uint64
	type Score int64
	actual := map[ID]Score{ID(^uint64(0)): Score(1<<60 + 1), ID(42): 0}
	required := map[ID]Score{ID(^uint64(0)): Score(1<<60 + 1)}
	matcher := expectMap[ID, Score](actual)
	var bound func(map[ID]Score) = matcher.ToHaveEntries
	bound(required)
	actual[ID(^uint64(0))]++
	matcher.Not.ToHaveEntries(required)
	required[ID(^uint64(0))]++
	bound(required)
	// Rebinding the caller's map does not rebind the saved matcher.
	actual = map[ID]Score{}
	bound(required)
	type record struct{ Value int }
	key, otherKey := &record{42}, &record{42}
	objects := map[*record]*record{key: {42}}
	expectMap(objects).ToHaveEntries(map[*record]*record{key: {42}})
	expectMap(objects).Not.ToHaveEntries(map[*record]*record{otherKey: {42}})
	expectMap(map[any]any{nil: nil}).ToHaveEntries(map[any]any{nil: nil})
}

func TestMapEntriesRetainNativeDeepValueAndFloatEquality(t *testing.T) {
	nested := map[string][]int{"nil": nil, "empty": {}, "row": {42, 43}}
	expectMap(nested).ToHaveEntries(map[string][]int{"nil": nil, "empty": {}, "row": {42, 43}})
	expectMap(nested).Not.ToHaveEntries(map[string][]int{"nil": {}})
	expectMap(nested).Not.ToHaveEntries(map[string][]int{"row": {43, 42}})
	var nilFunction func()
	expectMap(map[string]func(){"nil": nilFunction}).ToHaveEntries(map[string]func(){"nil": nil})
	calls := 0
	function := func() { calls++ }
	expectMap(map[string]func(){"fn": function}).Not.ToHaveEntries(map[string]func(){"fn": function})
	if calls != 0 {
		t.Fatal("comparison invoked function")
	}
	nan := math.NaN()
	nanKeys := map[float64]int{nan: 42}
	nanValues := map[string]float64{"nan": nan}
	// Aliasing must not bypass per-entry native lookup/deep equality.
	expectMap(nanKeys).Not.ToHaveEntries(nanKeys)
	expectMap(nanValues).Not.ToHaveEntries(nanValues)
	assertThrownMessage(t, "to have entries", func() { expectMap(nanKeys).ToHaveEntries(nanKeys) })
	assertThrownMessage(t, "to have entries", func() { expectMap(nanValues).ToHaveEntries(nanValues) })
	expectMap(map[float64]float64{0: math.Inf(1)}).ToHaveEntries(map[float64]float64{math.Copysign(0, -1): math.Inf(1)})
	// Deep equality of a nested aliased map retains its own native shortcut.
	expectMap(map[string]map[string]float64{"inner": nanValues}).ToHaveEntries(map[string]map[string]float64{"inner": nanValues})
	cycle := map[string]any{}
	cycle["self"] = cycle
	values := map[string]map[string]any{"loop": cycle}
	expectMap(values).ToHaveEntries(values)
	assertThrownMessage(t, "<cyclic value>", func() { expectMap(values).Not.ToHaveEntries(values) })
	if reflect.ValueOf(cycle["self"]).Pointer() != reflect.ValueOf(cycle).Pointer() {
		t.Fatal("cycle was altered")
	}
}

func TestMapEntriesPreserveLocationsCleanupAndFormatting(t *testing.T) {
	actual, expected := map[string]int{"value": 42}, map[string]int{"value": 43}
	var want TestLocation
	record := func() {
		_, file, line, _ := runtime.Caller(1)
		want = TestLocation{File: file, Line: line + 1, Source: "go"}
	}
	for _, check := range []func(){
		func() {
			record()
			expectMap(actual).ToHaveEntries(expected)
		},
		func() {
			record()
			expectMap(actual).Not.ToHaveEntries(actual)
		},
	} {
		caught := expectThrows(check)
		if caught == nil || failureLocation(caught) != want {
			t.Fatal(caught, want)
		}
	}
	var events []string
	suite := NewSuite("map entry subset")
	suite.AfterEach(func() { events = append(events, "afterEach") })
	suite.AfterAll(func() { events = append(events, "afterAll") })
	suite.TestWithContext("mismatch", func(context *TestContext) {
		context.Cleanup(func() { events = append(events, "cleanup") })
		withTestLocation("domain.km", 42, func() { expectMap(actual).ToHaveEntries(expected) })
	})
	suite.Test("later", func() { events = append(events, "later") })
	result := suite.RunTo(&bytes.Buffer{})
	if result.Failed != 1 || result.Passed != 1 || fmt.Sprint(events) != "[afterEach cleanup later afterEach afterAll]" {
		t.Fatal(result, events)
	}
	failure := result.Results[0].Failures[0]
	if failure.Phase != "body" || failure.Location != (TestLocation{File: "domain.km", Line: 42, Source: "explicit"}) || !strings.Contains(failure.Message, `map[string]int{"value":43}`) {
		t.Fatal(failure)
	}
	reads := 0
	value := diagnosticGoStringer{reads: &reads}
	formatted := map[string]diagnosticGoStringer{"value": value}
	expectMap(formatted).ToHaveEntries(formatted)
	expectMap(formatted).Not.ToHaveEntries(map[string]diagnosticGoStringer{"absent": value})
	if reads != 0 {
		t.Fatal("successful check formatted values", reads)
	}
}
