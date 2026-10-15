package vasest

import (
	"bytes"
	"math"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestSliceSequenceMatchesIndependentStringOracle(t *testing.T) {
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
	words = append(words, "aabab", "abab", "\x00a\xffa\x00", "\xffa")
	for _, actual := range words {
		for _, expected := range words {
			values, required := []byte(actual), []byte(expected)
			matcher := expectSlice(values)
			if strings.Contains(actual, expected) {
				matcher.ToContainSequence(required)
				assertThrownMessage(t, "not to contain contiguous sequence", func() { matcher.Not.ToContainSequence(required) })
			} else {
				matcher.Not.ToContainSequence(required)
				assertThrownMessage(t, "to contain contiguous sequence", func() { matcher.ToContainSequence(required) })
			}
			if string(values) != actual || string(required) != expected {
				t.Fatal("sequence matching mutated inputs", actual, expected, values, required)
			}
		}
	}
}

func TestSliceSequenceEmptyAndNestedValuesKeepNativeSemantics(t *testing.T) {
	for _, actual := range [][]int{nil, {}, {42}} {
		for _, expected := range [][]int{nil, {}} {
			expectSlice(actual).ToContainSequence(expected)
			assertThrownMessage(t, "not to contain contiguous sequence", func() { expectSlice(actual).Not.ToContainSequence(expected) })
		}
	}
	values := [][]int{{42}, nil, {}, {1, 2}, {43}}
	expectSlice(values).ToContainSequence([][]int{nil, {}, {1, 2}})
	expectSlice(values).Not.ToContainSequence([][]int{{}, nil})
	expectSlice(values).Not.ToContainSequence([][]int{{2, 1}})
	expectSlice([]map[string][]int{{"key": nil}, {"key": {}}, {"key": {1, 2}}}).ToContainSequence([]map[string][]int{{"key": {}}, {"key": {1, 2}}})
	var pointer *int
	expectSlice([]any{42, nil, pointer, 43}).ToContainSequence([]any{nil, pointer})
	expectSlice([]any{42, nil, pointer, 43}).Not.ToContainSequence([]any{pointer, nil})
}

func TestSliceSequenceNativeFloatsFunctionsCyclesAndDiagnostics(t *testing.T) {
	nans := []float64{math.NaN()}
	expectSlice(nans).Not.ToContainSequence(nans)
	expectSlice([][]float64{nans}).ToContainSequence([][]float64{nans})
	expectSlice([][]float64{nans}).Not.ToContainSequence([][]float64{{math.NaN()}})
	expectSlice([]float64{0, math.Copysign(0, -1), math.Inf(1), math.Inf(-1)}).ToContainSequence([]float64{0, math.Inf(1)})
	callback := func() {}
	var missing func()
	expectSlice([]func(){callback, missing, callback}).ToContainSequence([]func(){nil})
	expectSlice([]func(){callback, missing, callback}).Not.ToContainSequence([]func(){callback})
	type node struct {
		Value int
		Next  *node
	}
	first, second := &node{Value: 42}, &node{Value: 42}
	first.Next, second.Next = first, second
	expectSlice([]*node{nil, first, first, nil}).ToContainSequence([]*node{second, second})
	second.Value = 43
	for _, check := range []func(){
		func() { expectSlice([]*node{first}).ToContainSequence([]*node{second}) },
		func() { expectSlice([]*node{first}).Not.ToContainSequence([]*node{first}) },
	} {
		func() {
			defer func() {
				thrown, ok := recover().(__kinmokuseiThrown)
				if !ok || !strings.Contains(thrown.err.Error(), "contiguous sequence") || len(thrown.err.Error()) > 1000 {
					t.Fatal("lost or unbounded cyclic diagnostic", thrown)
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

func TestSliceSequenceSavedMethodsAndNominalValues(t *testing.T) {
	type UserID uint64
	large := UserID(math.MaxUint64)
	values := []UserID{large, 42, large, 42, 43}
	matcher := NewSliceExpectation(values)
	var check func([]UserID) = matcher.ToContainSequence
	var negative func([]UserID) = matcher.Not.ToContainSequence
	check([]UserID{large, 42, 43})
	negative([]UserID{large, large})
	values[4] = 44
	negative([]UserID{large, 42, 43})
	check([]UserID{large, 42, 44})
	values = []UserID{0}
	check([]UserID{large, 42, 44})
	NewNegatedSliceExpectation([]UserID{large, 42}).ToContainSequence([]UserID{42, large})
	if !reflect.DeepEqual(values, []UserID{0}) {
		t.Fatal("saved matcher replaced caller binding")
	}
	calls := 0
	probes := []boundaryFormattingProbe{{Calls: &calls}}
	expectSlice(probes).ToContainSequence(probes)
	if calls != 0 {
		t.Fatal("successful check formatted native values", calls)
	}
}

func TestSliceSequenceLocationsCleanupAndContinuation(t *testing.T) {
	var want TestLocation
	record := func() {
		_, file, line, _ := runtime.Caller(1)
		want = TestLocation{File: file, Line: line + 1, Source: "go"}
	}
	for _, check := range []func(){
		func() {
			record()
			expectSlice([]int{1, 2, 3}).ToContainSequence([]int{1, 3})
		},
		func() {
			record()
			expectSlice([]int{1, 2, 3}).Not.ToContainSequence([]int{2, 3})
		},
	} {
		func() {
			defer func() {
				thrown, ok := recover().(__kinmokuseiThrown)
				if !ok || failureLocation(thrown.err) != want {
					t.Fatal("lost fluent location", thrown, want)
				}
			}()
			check()
			t.Fatal("mismatch accepted")
		}()
	}
	for _, negated := range []bool{false, true} {
		suite := NewSuite("slice sequence cleanup")
		var events []string
		suite.AfterEach(func() { events = append(events, "afterEach") })
		suite.AfterAll(func() { events = append(events, "afterAll") })
		suite.TestWithContext("mismatch", func(context *TestContext) {
			context.Cleanup(func() { events = append(events, "cleanup") })
			withTestLocation("domain.km", 42, func() {
				if negated {
					expectSlice([]int{1, 2, 3}).Not.ToContainSequence([]int{2, 3})
				} else {
					expectSlice([]int{1, 2, 3}).ToContainSequence([]int{1, 3})
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
