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

func TestSliceBoundaryOrderAndMultiplicityAgainstStringOracle(t *testing.T) {
	// Binary words supply an independent prefix/suffix oracle for every short
	// sequence, including repeats, interior matches and overlong fragments.
	var words []string
	for length := 0; length <= 4; length++ {
		for bits := 0; bits < 1<<length; bits++ {
			word := make([]byte, length)
			for index := range word {
				word[index] = 'a' + byte((bits>>index)&1)
			}
			words = append(words, string(word))
		}
	}
	for _, actual := range words {
		for _, fragment := range words {
			values, required := []byte(actual), []byte(fragment)
			matcher := expectSlice[byte](values)
			for _, check := range []struct {
				positive, negative func([]byte)
				matches            bool
				relation           string
			}{
				{matcher.ToStartWith, matcher.Not.ToStartWith, strings.HasPrefix(actual, fragment), "start with"},
				{matcher.ToEndWith, matcher.Not.ToEndWith, strings.HasSuffix(actual, fragment), "end with"},
			} {
				if check.matches {
					check.positive(required)
					assertThrownMessage(t, "not to "+check.relation, func() { check.negative(required) })
				} else {
					check.negative(required)
					assertThrownMessage(t, "to "+check.relation, func() { check.positive(required) })
				}
			}
			if string(values) != actual || string(required) != fragment {
				t.Fatal("matcher mutated sequences", actual, fragment, values, required)
			}
		}
	}
}

func TestSliceBoundaryEmptyFragmentsAndNestedNativeValues(t *testing.T) {
	for _, actual := range [][]int{nil, {}, {42}} {
		for _, fragment := range [][]int{nil, {}} {
			expectSlice(actual).ToStartWith(fragment)
			expectSlice(actual).ToEndWith(fragment)
			assertThrownMessage(t, "not to start with", func() { expectSlice(actual).Not.ToStartWith(fragment) })
			assertThrownMessage(t, "not to end with", func() { expectSlice(actual).Not.ToEndWith(fragment) })
		}
	}
	values := [][]int{nil, {}, {1, 2}, {1, 2}}
	expectSlice(values).ToStartWith([][]int{nil, {}})
	expectSlice(values).ToEndWith([][]int{{1, 2}, {1, 2}})
	expectSlice(values).Not.ToStartWith([][]int{{}, nil})
	expectSlice(values).Not.ToEndWith([][]int{{1, 2}, {2, 1}})
	expectSlice([]map[string][]int{{"key": nil}, {"key": {1, 2}}}).ToStartWith([]map[string][]int{{"key": nil}})
	expectSlice([]map[string][]int{{"key": nil}, {"key": {1, 2}}}).Not.ToStartWith([]map[string][]int{{"key": {}}})
	var pointer *int
	expectSlice([]any{nil, pointer}).ToStartWith([]any{nil})
	expectSlice([]any{nil, pointer}).ToEndWith([]any{pointer})
	expectSlice([]any{nil, pointer}).Not.ToEndWith([]any{nil})
}

func TestSliceBoundaryNativeFloatsFunctionsAndCycles(t *testing.T) {
	nan := []float64{math.NaN()}
	expectSlice(nan).Not.ToStartWith(nan)
	expectSlice(nan).Not.ToEndWith(nan)
	// Native deep equality retains identity shortcuts inside each element.
	expectSlice([][]float64{nan}).ToStartWith([][]float64{nan})
	expectSlice([][]float64{nan}).Not.ToEndWith([][]float64{{math.NaN()}})
	expectSlice([]float64{math.Copysign(0, -1), math.Inf(1), math.Inf(-1)}).ToStartWith([]float64{0, math.Inf(1)})
	expectSlice([]float64{math.Copysign(0, -1), math.Inf(1), math.Inf(-1)}).ToEndWith([]float64{math.Inf(1), math.Inf(-1)})
	var missing func()
	callback := func() {}
	expectSlice([]func(){missing, callback}).ToStartWith([]func(){nil})
	expectSlice([]func(){missing, callback}).Not.ToEndWith([]func(){callback})
	type node struct {
		Value int
		Next  *node
	}
	first, second := &node{Value: 42}, &node{Value: 42}
	first.Next, second.Next = first, second
	expectSlice([]*node{first, nil}).ToStartWith([]*node{second})
	expectSlice([]*node{nil, first}).ToEndWith([]*node{second})
	second.Value = 43
	for _, check := range []func(){
		func() { expectSlice([]*node{first}).ToStartWith([]*node{second}) },
		func() { expectSlice([]*node{first}).ToEndWith([]*node{second}) },
		func() { expectSlice([]*node{first}).Not.ToStartWith([]*node{first}) },
		func() { expectSlice([]*node{first}).Not.ToEndWith([]*node{first}) },
	} {
		func() {
			defer func() {
				thrown, ok := recover().(__kinmokuseiThrown)
				if !ok || !strings.Contains(thrown.err.Error(), "expected slice") || len(thrown.err.Error()) > 1000 {
					t.Fatal("cyclic diagnostics were lost or unbounded", thrown)
				}
			}()
			check()
			t.Fatal("cyclic mismatch accepted")
		}()
	}
	if first.Next != first || second.Next != second || first.Value != 42 || second.Value != 43 {
		t.Fatal("native graphs mutated")
	}
}

func TestSliceBoundarySavedMatchersPreserveNominalValuesAndBackingStorage(t *testing.T) {
	type UserID uint64
	large := UserID(math.MaxUint64)
	actual := []UserID{large, 42, large}
	matcher := NewSliceExpectation(actual)
	var prefix func([]UserID) = matcher.ToStartWith
	var suffix func([]UserID) = matcher.ToEndWith
	var negativePrefix func([]UserID) = matcher.Not.ToStartWith
	var negativeSuffix func([]UserID) = matcher.Not.ToEndWith
	prefix([]UserID{large, 42})
	suffix([]UserID{42, large})
	actual[0], actual[2] = 43, 44
	prefix([]UserID{43})
	suffix([]UserID{44})
	negativePrefix([]UserID{large})
	negativeSuffix([]UserID{large})
	actual = []UserID{large}
	prefix([]UserID{43})
	suffix([]UserID{44})
	NewNegatedSliceExpectation([]UserID{large}).ToStartWith([]UserID{42})
	NewNegatedSliceExpectation([]UserID{large}).ToEndWith([]UserID{42})
}

func TestSliceBoundaryCallerLocationsAndCleanup(t *testing.T) {
	var want TestLocation
	record := func() {
		_, file, line, _ := runtime.Caller(1)
		want = TestLocation{File: file, Line: line + 1, Source: "go"}
	}
	for _, check := range []func(){
		func() {
			record()
			expectSlice([]int{1, 2}).ToStartWith([]int{2})
		},
		func() {
			record()
			expectSlice([]int{1, 2}).Not.ToStartWith([]int{1})
		},
		func() {
			record()
			expectSlice([]int{1, 2}).ToEndWith([]int{1})
		},
		func() {
			record()
			expectSlice([]int{1, 2}).Not.ToEndWith([]int{2})
		},
	} {
		func() {
			defer func() {
				thrown, ok := recover().(__kinmokuseiThrown)
				if !ok || failureLocation(thrown.err) != want {
					t.Fatal("fluent location", thrown, want)
				}
			}()
			check()
			t.Fatal("mismatch accepted")
		}()
	}
	for _, suffix := range []bool{false, true} {
		suite := NewSuite("slice boundary cleanup")
		var events []string
		suite.AfterEach(func() { events = append(events, "afterEach") })
		suite.AfterAll(func() { events = append(events, "afterAll") })
		suite.TestWithContext("mismatch", func(context *TestContext) {
			context.Cleanup(func() { events = append(events, "cleanup") })
			withTestLocation("domain.km", 42, func() {
				if suffix {
					expectSlice([]int{1, 2}).ToEndWith([]int{1})
				} else {
					expectSlice([]int{1, 2}).ToStartWith([]int{2})
				}
			})
		})
		suite.Test("later", func() { events = append(events, "later") })
		result := suite.RunTo(&bytes.Buffer{})
		if result.Failed != 1 || result.Passed != 1 || !reflect.DeepEqual(events, []string{"afterEach", "cleanup", "later", "afterEach", "afterAll"}) || result.Results[0].Failures[0].Phase != "body" || result.Results[0].Failures[0].Location != (TestLocation{File: "domain.km", Line: 42, Source: "explicit"}) {
			t.Fatal(result, events)
		}
	}
}

type boundaryFormattingProbe struct{ Calls *int }

func (value boundaryFormattingProbe) GoString() string {
	(*value.Calls)++
	return fmt.Sprintf("probe(%d)", *value.Calls)
}

func TestSliceBoundaryPassingChecksDoNotFormatValues(t *testing.T) {
	calls := 0
	values := []boundaryFormattingProbe{{Calls: &calls}}
	expectSlice(values).ToStartWith(values)
	expectSlice(values).ToEndWith(values)
	if calls != 0 {
		t.Fatal("successful check invoked native formatting", calls)
	}
}
