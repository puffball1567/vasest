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

func TestCycleDiagnosticsRetainAcyclicNativeFormatting(t *testing.T) {
	shared := map[string]int{"value": 42}
	pointer := new(int)
	*pointer = 42
	for _, value := range []any{nil, true, int64(1<<60 + 1), uint64(^uint64(0)), "金木犀", []int(nil), []int{}, [2]int{1, 2}, map[string]int(nil), shared, []*int{pointer, pointer}, []map[string]int{shared, shared}, struct{ private []int }{[]int{42}}, make(chan int), func() {}} {
		if got, want := diagnosticValue(value), fmt.Sprintf("%#v", value); got != want {
			t.Fatal("native formatting changed", got, want)
		}
	}
	if got := expectThrows(func() { expectEqual("a\nb", "a\nc") }); got == nil || !strings.Contains(got.Error(), `line 2: expected "c", got "b"`) {
		t.Fatal(got)
	}
}

func TestCycleDiagnosticsDetectActivePathsAndBoundTraversal(t *testing.T) {
	cycle := map[string]any{}
	cycle["self"] = cycle
	slice := make([]any, 1)
	slice[0] = slice
	type node struct {
		value int
		next  *node
	}
	object := &node{value: 42}
	object.next = object
	for _, value := range []any{cycle, slice, object, struct{ private map[string]any }{cycle}, []any{cycle}, map[string]any{"loop": slice}} {
		got := diagnosticValue(value)
		if !strings.Contains(got, "<cyclic value>") || !strings.Contains(got, reflect.TypeOf(value).String()) || len(got) > 200 {
			t.Fatal("missing bounded cycle diagnostic", got)
		}
	}
	// Scan the iterated value directly: a NaN key cannot be looked up again.
	if got := diagnosticValue(map[float64]any{math.NaN(): cycle}); !strings.Contains(got, "<cyclic value>") {
		t.Fatal("NaN-key cycle was missed", got)
	}
	if reflect.ValueOf(cycle["self"]).Pointer() != reflect.ValueOf(cycle).Pointer() || reflect.ValueOf(slice[0]).Pointer() != reflect.ValueOf(slice).Pointer() || object.next != object {
		t.Fatal("diagnostics changed the graph")
	}
	// Shared siblings and overlapping slices are not active-path cycles.
	shared := []int{1, 2, 3}
	aliases := [][]int{shared, shared, shared[:1]}
	if got := diagnosticValue(aliases); got != fmt.Sprintf("%#v", aliases) {
		t.Fatal("alias mistaken for cycle", got)
	}
	deep := &node{value: 1}
	for i := 0; i < 100; i++ {
		deep = &node{value: 1, next: deep}
	}
	for _, value := range []any{deep, make([]int, 5000)} {
		if got := diagnosticValue(value); !strings.Contains(got, "<diagnostic traversal limit>") || len(got) > 200 {
			t.Fatal(got)
		}
	}
	// The traversal budget also prevents an exponential walk of a shared DAG.
	var dag any = 42
	for i := 0; i < 30; i++ {
		dag = []any{dag, dag}
	}
	if got := diagnosticValue(dag); !strings.Contains(got, "traversal limit") {
		t.Fatal(got)
	}
	for _, value := range []any{map[float64]int{0: 42}, map[any]any{nil: nil}, struct{ private *int }{nil}} {
		if got := diagnosticValue(value); got != fmt.Sprintf("%#v", value) {
			t.Fatal(got)
		}
	}
}

type diagnosticGoStringer struct {
	data  map[string]any
	reads *int
}

func (value diagnosticGoStringer) GoString() string { (*value.reads)++; return "domain-value" }

type diagnosticFormatter struct {
	data  map[string]any
	reads *int
}

func (value diagnosticFormatter) Format(state fmt.State, verb rune) {
	(*value.reads)++
	if verb != 'v' || !state.Flag('#') {
		panic("format flags changed")
	}
	fmt.Fprint(state, "domain-format")
}

type diagnosticPanicFormatter struct{ reads *int }

func (value diagnosticPanicFormatter) GoString() string {
	(*value.reads)++
	panic("domain formatting panic")
}

type diagnosticMutatingFormatter struct{ values []int }

func (value diagnosticMutatingFormatter) GoString() string {
	value.values[0] = 99
	return "changed-value"
}

func TestCycleDiagnosticsCaptureArgumentsBeforeFormatting(t *testing.T) {
	values := []int{42}
	err := fmt.Errorf("expected %#v, got %#v", diagnosticArgument(diagnosticMutatingFormatter{values}), diagnosticArgument(values[0]))
	if err.Error() != "expected changed-value, got 42" || values[0] != 99 {
		t.Fatal(err, values)
	}
}

func TestCycleDiagnosticsPreserveNativeFormattingMethods(t *testing.T) {
	cycle := map[string]any{}
	cycle["self"] = cycle
	reads := 0
	if got := diagnosticValue(diagnosticGoStringer{cycle, &reads}); got != "domain-value" || reads != 1 {
		t.Fatal(got, reads)
	}
	reads = 0
	if got := diagnosticValue([]diagnosticFormatter{{cycle, &reads}}); !strings.Contains(got, "domain-format") || reads != 1 {
		t.Fatal(got, reads)
	}
	reads = 0
	if got := diagnosticValue(diagnosticPanicFormatter{&reads}); !strings.Contains(got, "PANIC=GoString method: domain formatting panic") || reads != 1 {
		t.Fatal(got, reads)
	}
	// fmt cannot invoke methods on unexported fields; scan those raw values.
	reads = 0
	if got := diagnosticValue(struct{ private diagnosticGoStringer }{diagnosticGoStringer{cycle, &reads}}); !strings.Contains(got, "cyclic value") || reads != 0 {
		t.Fatal(got, reads)
	}
}

func TestCycleDiagnosticsKeepAssertionsAndMocksAsCaseFailures(t *testing.T) {
	cycle := map[string]any{}
	cycle["self"] = cycle
	other := map[string]any{"different": true}
	mock := NewMockFn(func(value map[string]any) map[string]any { return value })
	mock.Invoke(cycle)
	checks := []func(){
		func() { expectEqual(cycle, other) },
		func() { expectNotEqual(cycle, cycle) },
		func() { expect(cycle).ToBeNil() },
		func() { expect(cycle).ToSatisfy(func(map[string]any) bool { return false }) },
		func() { expect(cycle).Not.ToSatisfy(func(map[string]any) bool { return true }) },
		func() { expectSlice([]map[string]any{cycle}).ToContain(other) },
		func() { expectSlice([]map[string]any{cycle}).Not.ToContain(cycle) },
		func() { expectSlice([]map[string]any{cycle}).ToContainAll([]map[string]any{other}) },
		func() { expectSlice([]map[string]any{cycle}).Not.ToContainAll([]map[string]any{cycle}) },
		func() { expectSlice([]map[string]any{cycle}).ToEqualUnordered([]map[string]any{other}) },
		func() { expectSlice([]map[string]any{cycle}).Not.ToEqualUnordered([]map[string]any{cycle}) },
		func() { expectMap(map[string]map[string]any{"value": cycle}).ToHaveEntry("value", other) },
		func() { expectMap(map[string]map[string]any{"value": cycle}).Not.ToHaveEntry("value", cycle) },
		func() { mock.ExpectCalledWith(other) },
		func() { mock.ExpectNthCalledWith(1, other) },
		func() { expectMock[map[string]any](mock).ToHaveBeenCalledWith(other) },
		func() { expectMock[map[string]any](mock).Not.ToHaveBeenNthCalledWith(1, cycle) },
		func() { mock.ExpectReturnedWith(other) },
		func() { mock.ExpectNthReturnedWith(1, other) },
		func() { expectMockReturn[map[string]any](mock).ToHaveReturnedWith(other) },
		func() { expectMockReturn[map[string]any](mock).Not.ToHaveLastReturnedWith(cycle) },
	}
	suite := NewSuite("cycle diagnostics")
	cleaned := 0
	for index, check := range checks {
		suite.TestWithContext(fmt.Sprint(index), func(context *TestContext) { context.Cleanup(func() { cleaned++ }); check() })
	}
	suite.Test("later", func() { cleaned++ })
	result := suite.RunTo(&bytes.Buffer{})
	if result.Failed != len(checks) || result.Passed != 1 || cleaned != len(checks)+1 {
		t.Fatal(result, cleaned)
	}
	for _, row := range result.Results[:len(checks)] {
		if !strings.Contains(row.Message, "<cyclic value>") || len(row.Failures) != 1 || row.Failures[0].Phase != "body" || row.Failures[0].Location.Source != "go" {
			t.Fatal(row)
		}
	}
	if mock.CallCount() != 1 || mock.Results()[0].Value["self"] == nil {
		t.Fatal("diagnostics changed mock histories")
	}
}

func TestCycleDiagnosticsPreserveFluentAndExplicitLocations(t *testing.T) {
	cycle := map[string]any{}
	cycle["self"] = cycle
	var want TestLocation
	record := func() {
		_, file, line, _ := runtime.Caller(1)
		want = TestLocation{File: file, Line: line + 1, Source: "go"}
	}
	for _, check := range []func(){
		func() {
			record()
			expect(cycle).ToEqual(map[string]any{})
		},
		func() {
			record()
			expectSlice([]map[string]any{cycle}).Not.ToContainAll([]map[string]any{cycle})
		},
	} {
		caught := expectThrows(check)
		if caught == nil || failureLocation(caught) != want {
			t.Fatal(caught, want)
		}
	}
	caught := expectThrows(func() { withTestLocation("domain.km", 42, func() { expectEqual(cycle, map[string]any{}) }) })
	if caught == nil || failureLocation(caught) != (TestLocation{File: "domain.km", Line: 42, Source: "explicit"}) {
		t.Fatal(caught)
	}
}
