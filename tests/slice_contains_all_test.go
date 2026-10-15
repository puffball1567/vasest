package vasest

import (
	"bytes"
	"math"
	"reflect"
	"runtime"
	"testing"
)

func TestSliceContainsAllMultiplicityAndNegation(t *testing.T) {
	rows := [][]int{nil, {}}
	var generate func([]int, int)
	generate = func(prefix []int, remaining int) {
		if remaining == 0 {
			rows = append(rows, append([]int{}, prefix...))
			return
		}
		for _, value := range []int{-1, 0, 1} {
			generate(append(prefix, value), remaining-1)
		}
	}
	for length := 1; length <= 3; length++ {
		generate(nil, length)
	}
	count := func(values []int) map[int]int {
		counts := map[int]int{}
		for _, value := range values {
			counts[value]++
		}
		return counts
	}
	for _, actual := range rows {
		for _, expected := range rows {
			available, required := count(actual), count(expected)
			matches := true
			for value, amount := range required {
				if available[value] < amount {
					matches = false
				}
			}
			for _, negated := range []bool{false, true} {
				check := func() {
					matcher := expectSlice(actual)
					if negated {
						matcher.Not.ToContainAll(expected)
					} else {
						matcher.ToContainAll(expected)
					}
				}
				if matches == negated {
					assertThrownMessage(t, "including duplicate counts", check)
				} else {
					check()
				}
			}
		}
	}
}

func TestSliceContainsAllNativeTypesAndUnchangedInputs(t *testing.T) {
	type ID uint64
	first := ID(math.MaxUint64)
	actual, expected := []ID{first, 42, first, 43}, []ID{first, first, 42}
	a, b := append([]ID{}, actual...), append([]ID{}, expected...)
	matcher := expectSlice[ID](actual)
	matcher.ToContainAll(expected)
	NewSliceExpectation[ID](actual).ToContainAll(expected)
	matcher.Not.ToContainAll([]ID{first, first, first})
	if !reflect.DeepEqual(actual, a) || !reflect.DeepEqual(expected, b) {
		t.Fatal("inputs reordered or modified", actual, expected)
	}
	actual[0] = 0
	matcher.Not.ToContainAll(expected)
	expected[1] = 0
	matcher.ToContainAll(expected)
	// Inner order and nil/empty distinctions still use native deep equality.
	expectSlice([][]int{nil, {}, {1, 2}, {1, 2}, {42}}).ToContainAll([][]int{{1, 2}, nil, {1, 2}})
	expectSlice([][]int{{1, 2}}).Not.ToContainAll([][]int{{2, 1}})
	expectSlice([][]int{nil}).Not.ToContainAll([][]int{{}})
	expectSlice([][]int{{1, 2}}).Not.ToContainAll([][]int{{1, 2}, {1, 2}})
	expectSlice([]map[string]int{{"a": 1}, nil, {}, {"a": 1}}).ToContainAll([]map[string]int{{"a": 1}, {"a": 1}, nil})
	expectSlice([]any{int32(42), int64(42)}).ToContainAll([]any{int64(42)})
	expectSlice([]any{int32(42)}).Not.ToContainAll([]any{int64(42)})
	var pointer *int
	expectSlice([]any{pointer}).Not.ToContainAll([]any{nil})
	var callback func()
	expectSlice([]func(){callback}).ToContainAll([]func(){nil})
	fn := func() { t.Fatal("function element invoked") }
	expectSlice([]func(){fn}).Not.ToContainAll([]func(){fn})
}

func TestSliceContainsAllReferencesCyclesAndFloats(t *testing.T) {
	type node struct {
		Value int
		Next  *node
	}
	first, second := &node{Value: 42}, &node{Value: 42}
	first.Next, second.Next = first, second
	expectSlice([]*node{nil, first, first}).ToContainAll([]*node{second, second})
	second.Value = 43
	expectSlice([]*node{first}).Not.ToContainAll([]*node{second})
	if first.Next != first || second.Next != second || first.Value != 42 {
		t.Fatal("native references modified")
	}
	expectSlice([]float64{math.Copysign(0, -1), math.Inf(1), math.Inf(-1)}).ToContainAll([]float64{math.Inf(-1), 0})
	expectSlice([]float64{0}).Not.ToContainAll([]float64{0, math.Copysign(0, -1)})
	nans := []float64{math.NaN()}
	expectSlice(nans).Not.ToContainAll(nans)
	assertThrownMessage(t, "including duplicate counts", func() { expectSlice(nans).ToContainAll(nans) })
	// Nested slices retain native deep-equality's identity shortcut.
	expectSlice([][]float64{nans}).ToContainAll([][]float64{nans})
	expectSlice([][]float64{nans}).Not.ToContainAll([][]float64{{math.NaN()}})
	type Score float32
	expectSlice[Score]([]Score{1.25, 2.5}).ToContainAll([]Score{2.5})
}

func TestSliceContainsAllCallerLocationsAndCleanup(t *testing.T) {
	var want TestLocation
	record := func() {
		_, file, line, _ := runtime.Caller(1)
		want = TestLocation{File: file, Line: line + 1, Source: "go"}
	}
	for _, check := range []func(){
		func() {
			record()
			expectSlice([]int{1, 1}).ToContainAll([]int{1, 2})
		},
		func() {
			record()
			NewSliceExpectation([]int{1, 2, 3}).Not.ToContainAll([]int{2, 1})
		},
		func() {
			record()
			expectSlice[int](nil).Not.ToContainAll(nil)
		},
		func() {
			record()
			expectSlice([]int{}).Not.ToContainAll([]int{})
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
		}()
	}
	for _, negated := range []bool{false, true} {
		suite := NewSuite("contains all cleanup")
		var events []string
		suite.AfterEach(func() { events = append(events, "afterEach") })
		suite.TestWithContext("mismatch", func(context *TestContext) {
			context.Cleanup(func() { events = append(events, "cleanup") })
			withTestLocation("domain.km", 42, func() {
				if negated {
					expectSlice([]int{1, 2}).Not.ToContainAll([]int{1})
				} else {
					expectSlice([]int{1}).ToContainAll([]int{1, 1})
				}
			})
		})
		suite.Test("later", func() { events = append(events, "later") })
		result := suite.RunTo(&bytes.Buffer{})
		if result.Failed != 1 || result.Passed != 1 || !reflect.DeepEqual(events, []string{"afterEach", "cleanup", "later", "afterEach"}) || result.Results[0].Failures[0].Location != (TestLocation{File: "domain.km", Line: 42, Source: "explicit"}) {
			t.Fatal(result, events)
		}
	}
}
