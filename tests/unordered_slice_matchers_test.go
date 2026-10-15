package vasest

import (
	"bytes"
	"math"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func TestUnorderedSliceMatchersCountDuplicatesAndNegate(t *testing.T) {
	// Compare against sorted integer copies, independently of element matching.
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
	for _, actual := range rows {
		for _, expected := range rows {
			a, b := append([]int{}, actual...), append([]int{}, expected...)
			sort.Ints(a)
			sort.Ints(b)
			matches := reflect.DeepEqual(a, b) && ((actual == nil) == (expected == nil))
			for _, negated := range []bool{false, true} {
				check := func() {
					matcher := expectSlice(actual)
					if negated {
						matcher.Not.ToEqualUnordered(expected)
					} else {
						matcher.ToEqualUnordered(expected)
					}
				}
				if matches == negated {
					assertThrownMessage(t, "ignoring order", check)
				} else {
					check()
				}
			}
		}
	}
}

func TestUnorderedSliceMatchersKeepNativeValuesAndReferences(t *testing.T) {
	type ID int64
	actual := []ID{1<<60 + 1, 42, 1<<60 + 1}
	expected := []ID{1<<60 + 1, 1<<60 + 1, 42}
	saved := expectSlice[ID](actual)
	saved.ToEqualUnordered(expected)
	NewSliceExpectation[ID](actual).ToEqualUnordered(expected)
	if !reflect.DeepEqual(actual, []ID{1<<60 + 1, 42, 1<<60 + 1}) || !reflect.DeepEqual(expected, []ID{1<<60 + 1, 1<<60 + 1, 42}) {
		t.Fatal("matcher reordered inputs")
	}
	actual[1] = 43
	saved.Not.ToEqualUnordered(expected)
	expected[2] = 43
	saved.ToEqualUnordered(expected)
	// Inferred calls and constructors also support non-comparable element types.
	expectSlice([][]int{nil, {}, {1, 2}, {1, 2}}).ToEqualUnordered([][]int{{1, 2}, nil, {1, 2}, {}})
	expectSlice([][]int{{1, 2}}).Not.ToEqualUnordered([][]int{{2, 1}})
	expectSlice([][]int{nil}).Not.ToEqualUnordered([][]int{{}})
	expectSlice([]map[string]int{{"a": 1}, nil, {}}).ToEqualUnordered([]map[string]int{{}, {"a": 1}, nil})
	type node struct {
		Value int
		Next  *node
	}
	first, second := &node{Value: 42}, &node{Value: 42}
	first.Next, second.Next = first, second
	expectSlice([]*node{nil, first}).ToEqualUnordered([]*node{second, nil})
	second.Value = 43
	expectSlice([]*node{first}).Not.ToEqualUnordered([]*node{second})
	expectSlice([]any{int32(42), int64(42)}).ToEqualUnordered([]any{int64(42), int32(42)})
	expectSlice([]any{int32(42)}).Not.ToEqualUnordered([]any{int64(42)})
	var pointer *node
	expectSlice([]any{pointer}).Not.ToEqualUnordered([]any{nil})
	var fn func()
	expectSlice([]func(){fn}).ToEqualUnordered([]func(){nil})
	callback := func() { t.Fatal("function element invoked") }
	expectSlice([]func(){callback}).Not.ToEqualUnordered([]func(){callback})
}

func TestUnorderedSliceMatchersUseNativeElementFloatEquality(t *testing.T) {
	expectSlice([]float64{math.Copysign(0, -1), math.Inf(1), math.Inf(-1)}).ToEqualUnordered([]float64{math.Inf(-1), 0, math.Inf(1)})
	nans := []float64{math.NaN()}
	// There is no whole-slice identity shortcut: each selected element is compared.
	expectSlice(nans).Not.ToEqualUnordered(nans)
	assertThrownMessage(t, "ignoring order", func() { expectSlice(nans).ToEqualUnordered(nans) })
	type Score float32
	expectSlice[Score]([]Score{Score(1.25), Score(2.5)}).ToEqualUnordered([]Score{Score(2.5), Score(1.25)})
}

func TestUnorderedSliceMatchersReportFluentCallerAndCleanup(t *testing.T) {
	var want TestLocation
	record := func() {
		_, file, line, _ := runtime.Caller(1)
		want = TestLocation{File: file, Line: line, Source: "go"}
	}
	for _, check := range []func(){
		func() { record(); expectSlice([]int{1, 1}).ToEqualUnordered([]int{1, 2}) },
		func() { record(); NewSliceExpectation([]int{1, 2}).Not.ToEqualUnordered([]int{2, 1}) },
	} {
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
	suite := NewSuite("unordered slices")
	var phases []string
	suite.AfterEach(func() { phases = append(phases, "afterEach") })
	suite.TestWithContext("mismatch", func(context *TestContext) {
		context.Cleanup(func() { phases = append(phases, "cleanup") })
		expectSlice([]int{1, 1}).ToEqualUnordered([]int{1, 2})
	})
	suite.Test("later", func() { phases = append(phases, "later") })
	result := suite.RunTo(&bytes.Buffer{})
	if result.Failed != 1 || result.Passed != 1 || strings.Join(phases, ",") != "afterEach,cleanup,later,afterEach" || len(result.Results[0].Failures) != 1 || result.Results[0].Failures[0].Phase != "body" {
		t.Fatal(result, phases)
	}
}
