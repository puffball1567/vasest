package vasest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

type matcherCallSource[A any] struct{ snapshot func() []A }

func (source *matcherCallSource[A]) Calls() []A { return source.snapshot() }

func TestMockMatchersObserveLiveTypedCallsWithoutInvokingMock(t *testing.T) {
	type ID int64
	invocations := 0
	mock := NewMockFn[ID, string](func(value ID) string { invocations++; return "done" })
	matcher := expectMock[ID](mock)
	mock.ReturnValueOnce("queued")
	matcher.Not.ToHaveBeenCalled()
	matcher.ToHaveBeenCalledTimes(0)
	matcher.Not.ToHaveBeenCalledWith(ID(42))
	matcher.Not.ToHaveBeenNthCalledWith(1, ID(42))
	matcher.Not.ToHaveBeenLastCalledWith(ID(42))
	if invocations != 0 || mock.Invoke(ID(42)) != "queued" {
		t.Fatal("matcher consumed behavior")
	}
	matcher.ToHaveBeenCalled()
	matcher.ToHaveBeenCalledTimes(1)
	matcher.ToHaveBeenCalledWith(ID(42))
	matcher.ToHaveBeenNthCalledWith(1, ID(42))
	matcher.ToHaveBeenLastCalledWith(ID(42))
	mock.Invoke(ID(43))
	matcher.Not.ToHaveBeenCalledTimes(1)
	matcher.Not.ToHaveBeenCalledWith(ID(44))
	matcher.Not.ToHaveBeenNthCalledWith(1, ID(43))
	matcher.Not.ToHaveBeenNthCalledWith(3, ID(42))
	matcher.Not.ToHaveBeenLastCalledWith(ID(42))
	matcher.ToHaveBeenLastCalledWith(ID(43))
	matcher.ToHaveBeenCalledTimes(2)
	mock.ClearCalls()
	matcher.Not.ToHaveBeenCalled()
	matcher.ToHaveBeenCalledTimes(0)
	if invocations != 1 {
		t.Fatal("matcher invoked callback", invocations)
	}
	action := NewMockAction[ID](func(ID) {})
	actionMatcher := NewMockExpectation[ID](action)
	action.Invoke(ID(1))
	actionMatcher.ToHaveBeenCalledWith(ID(1))
	actionMatcher.ToHaveBeenLastCalledWith(ID(1))
	actionMatcher.Not.ToHaveBeenCalledTimes(0)
}

func TestMockMatchersUseDeepEqualityAndNativeTypeIdentity(t *testing.T) {
	type request struct {
		IDs    []int
		Values map[string]int
	}
	mock := NewMockAction[request](func(request) {})
	value := request{[]int{1, 2}, map[string]int{"金木犀": 42}}
	mock.Invoke(value)
	expectMock[request](mock).ToHaveBeenCalledWith(request{[]int{1, 2}, map[string]int{"金木犀": 42}})
	expectMock[request](mock).ToHaveBeenNthCalledWith(1, request{[]int{1, 2}, map[string]int{"金木犀": 42}})
	value.IDs[0] = 3
	expectMock[request](mock).ToHaveBeenLastCalledWith(request{[]int{3, 2}, map[string]int{"金木犀": 42}})
	slices := NewMockAction[[]int](func([]int) {})
	slices.Invoke(nil)
	expectMock[[]int](slices).ToHaveBeenCalledWith(nil)
	expectMock[[]int](slices).Not.ToHaveBeenCalledWith([]int{})
	interfaces := NewMockAction[any](func(any) {})
	interfaces.Invoke(int32(42))
	expectMock[any](interfaces).Not.ToHaveBeenCalledWith(int64(42))
}

func TestMockMatchersRejectMismatchesAndInvalidArguments(t *testing.T) {
	mock := NewMockFn[int, int](func(value int) int { return value })
	matcher := expectMock[int](mock)
	assertThrownMessage(t, "got no calls", func() { matcher.ToHaveBeenCalled() })
	assertThrownMessage(t, "got 0 call(s)", func() { matcher.ToHaveBeenLastCalledWith(42) })
	assertThrownMessage(t, "got 0 call(s)", func() { matcher.ToHaveBeenNthCalledWith(1, 42) })
	mock.Invoke(42)
	checks := []struct {
		want string
		body func()
	}{
		{"not to have been called", func() { matcher.Not.ToHaveBeenCalled() }},
		{"called 2 time(s), got 1", func() { matcher.ToHaveBeenCalledTimes(2) }},
		{"not to have been called 1 time(s)", func() { matcher.Not.ToHaveBeenCalledTimes(1) }},
		{"called with 43", func() { matcher.ToHaveBeenCalledWith(43) }},
		{"not to have been called with 42", func() { matcher.Not.ToHaveBeenCalledWith(42) }},
		{"call 1 with 43, got 42", func() { matcher.ToHaveBeenNthCalledWith(1, 43) }},
		{"call 2 with 42, got 1 call(s)", func() { matcher.ToHaveBeenNthCalledWith(2, 42) }},
		{"call 1 not to have argument 42", func() { matcher.Not.ToHaveBeenNthCalledWith(1, 42) }},
		{"last call with 43, got 42", func() { matcher.ToHaveBeenLastCalledWith(43) }},
		{"last call not to have argument 42", func() { matcher.Not.ToHaveBeenLastCalledWith(42) }},
		{"count must not be negative", func() { matcher.ToHaveBeenCalledTimes(-1) }},
		{"count must not be negative", func() { matcher.Not.ToHaveBeenCalledTimes(-1) }},
		{"index must be at least 1", func() { matcher.ToHaveBeenNthCalledWith(0, 42) }},
		{"index must be at least 1", func() { matcher.Not.ToHaveBeenNthCalledWith(-1, 42) }},
	}
	for _, check := range checks {
		assertThrownMessage(t, check.want, check.body)
	}
	for _, source := range []MockCallSource[int]{nil, (*MockFn[int, int])(nil), (*MockAction[int])(nil)} {
		invalid := expectMock[int](source)
		for _, attempt := range []func(){invalid.ToHaveBeenCalled, invalid.Not.ToHaveBeenCalled,
			func() { invalid.ToHaveBeenCalledTimes(0) }, func() { invalid.Not.ToHaveBeenCalledTimes(1) },
			func() { invalid.ToHaveBeenCalledWith(42) }, func() { invalid.Not.ToHaveBeenCalledWith(42) },
			func() { invalid.ToHaveBeenNthCalledWith(1, 42) }, func() { invalid.Not.ToHaveBeenNthCalledWith(1, 42) },
			func() { invalid.ToHaveBeenLastCalledWith(42) }, func() { invalid.Not.ToHaveBeenLastCalledWith(42) }} {
			assertThrownMessage(t, "source must not be nil", attempt)
		}
	}
}

func TestMockMatchersReadOneSnapshotAndValidateBeforeReading(t *testing.T) {
	reads := 0
	var calls []int
	source := &matcherCallSource[int]{func() []int { reads++; return calls }}
	matcher := expectMock[int](source)
	checks := []func(){matcher.ToHaveBeenCalled,
		func() { matcher.ToHaveBeenCalledTimes(1) }, func() { matcher.ToHaveBeenCalledWith(42) },
		func() { matcher.ToHaveBeenNthCalledWith(1, 42) }, func() { matcher.ToHaveBeenLastCalledWith(42) },
		matcher.Not.ToHaveBeenCalled, func() { matcher.Not.ToHaveBeenCalledTimes(1) },
		func() { matcher.Not.ToHaveBeenCalledWith(42) }, func() { matcher.Not.ToHaveBeenNthCalledWith(1, 42) },
		func() { matcher.Not.ToHaveBeenLastCalledWith(42) }}
	for i, check := range checks {
		reads = 0
		calls = nil
		if i < 5 {
			calls = []int{42}
		}
		check()
		if reads != 1 {
			t.Fatal("snapshot read count", reads)
		}
	}
	reads = 0
	for _, attempt := range []func(){func() { matcher.ToHaveBeenCalledTimes(-1) }, func() { matcher.Not.ToHaveBeenCalledTimes(-1) },
		func() { matcher.ToHaveBeenNthCalledWith(0, 42) }, func() { matcher.Not.ToHaveBeenNthCalledWith(0, 42) }} {
		assertThrownMessage(t, "must", attempt)
	}
	if reads != 0 {
		t.Fatal("invalid arguments read source", reads)
	}
}

func TestMockMatchersPreserveCustomSourceThrowsAndPanicCleanup(t *testing.T) {
	sentinel := errors.New("call source failed")
	for _, negated := range []bool{false, true} {
		source := &matcherCallSource[int]{func() []int { panic(__kinmokuseiThrown{err: sentinel}) }}
		func() {
			defer func() {
				thrown, ok := recover().(__kinmokuseiThrown)
				if !ok || thrown.err != sentinel {
					t.Fatal("source throw changed", thrown)
				}
			}()
			if negated {
				expectMock[int](source).Not.ToHaveBeenLastCalledWith(42)
			} else {
				expectMock[int](source).ToHaveBeenLastCalledWith(42)
			}
		}()
		for _, nativePanic := range []bool{false, true} {
			func() {
				var events []string
				if nativePanic {
					defer func() {
						if recovered := recover(); recovered != "call source panic" || fmt.Sprint(events) != "[afterEach cleanup afterAll]" {
							t.Fatal(recovered, events)
						}
					}()
				}
				suite := NewSuite("mock source cleanup")
				suite.AfterEach(func() { events = append(events, "afterEach") })
				suite.AfterAll(func() { events = append(events, "afterAll") })
				suite.TestWithContext("case", func(context *TestContext) {
					context.Cleanup(func() { events = append(events, "cleanup") })
					source := &matcherCallSource[int]{func() []int {
						if nativePanic {
							panic("call source panic")
						}
						withTestLocation("spy.km", 42, func() { panic(__kinmokuseiThrown{err: sentinel}) })
						return nil
					}}
					if negated {
						expectMock[int](source).Not.ToHaveBeenCalled()
					} else {
						expectMock[int](source).ToHaveBeenCalled()
					}
				})
				summary := suite.RunTo(&bytes.Buffer{})
				if nativePanic {
					t.Fatal("native panic was swallowed")
				}
				if summary.Failed != 1 || summary.Results[0].Failures[0].Location != (TestLocation{File: "spy.km", Line: 42, Source: "explicit"}) || fmt.Sprint(events) != "[afterEach cleanup afterAll]" {
					t.Fatal(summary, events)
				}
			}()
		}
	}
}

func TestMockMatchersIncludePendingThrownAndPanickingCalls(t *testing.T) {
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	mock := NewMockFn[int, int](func(value int) int { close(entered); <-release; return value })
	go func() { defer close(done); mock.Invoke(42) }()
	<-entered
	expectMock[int](mock).ToHaveBeenCalledTimes(1)
	expectMock[int](mock).ToHaveBeenLastCalledWith(42)
	close(release)
	<-done
	mock.ImplementOnce(func(int) int { panic(__kinmokuseiThrown{err: errors.New("mock throw")}) })
	assertThrownMessage(t, "mock throw", func() { mock.Invoke(43) })
	mock.ImplementOnce(func(int) int { panic("mock panic") })
	func() {
		defer func() {
			if recover() != "mock panic" {
				t.Fatal("panic changed")
			}
		}()
		mock.Invoke(44)
	}()
	expectMock[int](mock).ToHaveBeenCalledTimes(3)
	expectMock[int](mock).ToHaveBeenNthCalledWith(2, 43)
	expectMock[int](mock).ToHaveBeenLastCalledWith(44)
}

func TestMockMatchersConcurrentCallsAndClearing(t *testing.T) {
	mock := NewMockAction[int](func(int) {})
	matcher := expectMock[int](mock)
	var group sync.WaitGroup
	group.Add(1)
	go func() {
		defer group.Done()
		for i := 0; i < 200; i++ {
			mock.Invoke(i)
			if i%20 == 0 {
				mock.ClearCalls()
			}
		}
	}()
	for i := 0; i < 200; i++ {
		matcher.Not.ToHaveBeenCalledWith(-1)
		matcher.Not.ToHaveBeenNthCalledWith(1, -1)
		matcher.Not.ToHaveBeenLastCalledWith(-1)
		matcher.Not.ToHaveBeenCalledTimes(201)
	}
	group.Wait()
}

func TestMockFnRecordsTypedCallsAndBindsCallback(t *testing.T) {
	type request struct {
		Path string
		IDs  []int
	}
	mock := NewMockFn[request, string](func(value request) string { return value.Path + "!" })
	callback := mock.Invoke
	mock.ExpectCalledTimes(0)
	if got := callback(request{Path: "金木犀", IDs: []int{1, 2}}); got != "金木犀!" {
		t.Fatalf("callback returned %q", got)
	}
	mock.ExpectCalledTimes(1)
	mock.ExpectCalledWith(request{Path: "金木犀", IDs: []int{1, 2}})
	mock.ExpectNthCalledWith(1, request{Path: "金木犀", IDs: []int{1, 2}})
	calls := mock.Calls()
	calls[0] = request{Path: "replaced"}
	if mock.Calls()[0].Path != "金木犀" {
		t.Fatal("changing the returned history slice changed the mock")
	}
}

func TestMockFnQueuedImplementationsAndClearCalls(t *testing.T) {
	mock := NewMockFn[int, int](func(value int) int { return value * 2 })
	mock.ImplementOnce(func(value int) int { return value + 10 })
	mock.ReturnValueOnce(42)
	mock.ReturnValue(7)
	mock.ClearCalls()
	for index, want := range []int{11, 42, 7} {
		if got := mock.Invoke(index + 1); got != want {
			t.Fatalf("call %d returned %d, want %d", index+1, got, want)
		}
	}
	mock.ExpectCalledTimes(3)
	mock.ClearCalls()
	mock.ExpectCalledTimes(0)
	if len(mock.Calls()) != 0 || mock.Invoke(9) != 7 {
		t.Fatal("clearing calls changed the default implementation")
	}
	mock.Implement(func(value int) int { return value * 3 })
	if got := mock.Invoke(4); got != 12 {
		t.Fatalf("replacement returned %d", got)
	}
}

func TestMockFnRecordsThrownAndPanickingCalls(t *testing.T) {
	mock := NewMockFn[int, int](func(value int) int { return value })
	mock.ImplementOnce(func(value int) int {
		panic(__kinmokuseiThrown{err: errors.New("mock error")})
	})
	mock.ImplementOnce(func(value int) int { panic("native mock panic") })
	assertThrownMessage(t, "mock error", func() { mock.Invoke(1) })
	func() {
		defer func() {
			if recovered := recover(); recovered != "native mock panic" {
				t.Fatalf("panic = %#v", recovered)
			}
		}()
		mock.Invoke(2)
	}()
	if mock.Invoke(3) != 3 {
		t.Fatal("one-shot implementations were not consumed after errors")
	}
	mock.ExpectCalledTimes(3)
	mock.ExpectNthCalledWith(1, 1)
	mock.ExpectNthCalledWith(2, 2)
}

func TestMockFnValidationAndDiagnostics(t *testing.T) {
	assertThrownMessage(t, "mock implementation must not be nil", func() { NewMockFn[int, int](nil) })
	mock := NewMockFn[int, int](func(value int) int { return value })
	assertThrownMessage(t, "mock implementation must not be nil", func() { mock.Implement(nil) })
	assertThrownMessage(t, "mock implementation must not be nil", func() { mock.ImplementOnce(nil) })
	assertThrownMessage(t, "expected mock call count must not be negative", func() { mock.ExpectCalledTimes(-1) })
	assertThrownMessage(t, "expected mock to be called 1 time(s), got 0", func() { mock.ExpectCalledTimes(1) })
	assertThrownMessage(t, "expected mock to be called with 1", func() { mock.ExpectCalledWith(1) })
	assertThrownMessage(t, "mock call index must be at least 1", func() { mock.ExpectNthCalledWith(0, 1) })
	assertThrownMessage(t, "expected mock call 1, got 0 call(s)", func() { mock.ExpectNthCalledWith(1, 1) })
	if mock.Invoke(5) != 5 {
		t.Fatal("invalid implementations changed the default")
	}
	assertThrownMessage(t, "expected mock call 1 with 6, got 5", func() { mock.ExpectNthCalledWith(1, 6) })
	assertThrownMessage(t, "expected mock call 2, got 1 call(s)", func() { mock.ExpectNthCalledWith(2, 5) })
	mock.ExpectCalledWith(5)
	collections := NewMockFn[[]int, bool](func(values []int) bool { return true })
	collections.Invoke(nil)
	collections.ExpectCalledWith(nil)
	assertThrownMessage(t, "expected mock to be called with", func() { collections.ExpectCalledWith([]int{}) })
}

func TestMockFnImplementationCanInspectAndInvokeMock(t *testing.T) {
	var mock *MockFn[int, int]
	mock = NewMockFn[int, int](func(value int) int {
		mock.CallCount()
		mock.Calls()
		if value == 0 {
			return 0
		}
		return 1 + mock.Invoke(value-1)
	})
	result := make(chan int, 1)
	go func() { result <- mock.Invoke(2) }()
	select {
	case got := <-result:
		if got != 2 {
			t.Fatalf("recursive callback returned %d", got)
		}
	case <-time.After(time.Second):
		t.Fatal("mock held its lock while running the callback")
	}
	mock.ExpectCalledTimes(3)
	mock.ExpectNthCalledWith(1, 2)
	mock.ExpectNthCalledWith(3, 0)
}

func TestMockFnConcurrentInvocationsConsumeQueueOnce(t *testing.T) {
	const count = 64
	mock := NewMockFn[int, int](func(value int) int { return -1 })
	for value := 0; value < count; value++ {
		mock.ReturnValueOnce(value)
	}
	var workers sync.WaitGroup
	results := make(chan int, count)
	for argument := 0; argument < count; argument++ {
		workers.Add(1)
		go func(argument int) {
			defer workers.Done()
			results <- mock.Invoke(argument)
			mock.Calls()
			mock.CallCount()
			mock.Implement(func(value int) int { return -2 })
		}(argument)
	}
	workers.Wait()
	close(results)
	seen := make(map[int]bool)
	for result := range results {
		if result < 0 || result >= count || seen[result] {
			t.Fatalf("one-shot result was lost or reused: %d", result)
		}
		seen[result] = true
	}
	mock.ExpectCalledTimes(count)
	mock.ExpectReturnedTimes(count)
	for argument := 0; argument < count; argument++ {
		mock.ExpectCalledWith(argument)
	}
	if got := mock.Invoke(count); got != -2 {
		t.Fatalf("fallback returned %d", got)
	}
}

type mockTypedException struct{ err error }

func (exception *mockTypedException) KinmokuseiExceptionError() error { return exception.err }

func TestMockResultsPreserveReturnsExceptionsAndPanics(t *testing.T) {
	wantError := errors.New("金木犀 unavailable")
	exception := &mockTypedException{err: wantError}
	mock := NewMockFn[int, int](func(value int) int { return value * 10 })
	mock.ImplementOnce(func(value int) int { panic(exception) })
	mock.ImplementOnce(func(value int) int { panic("native result panic") })
	for index, want := range []any{exception, "native result panic"} {
		func() {
			defer func() {
				if got := recover(); got != want {
					t.Fatalf("panic type or identity changed: got %#v, want %#v", got, want)
				}
			}()
			mock.Invoke(index + 1)
		}()
	}
	if mock.Invoke(3) != 30 {
		t.Fatal("return value changed")
	}
	results := mock.Results()
	if len(results) != 3 || results[0].Status != "throw" || results[0].Exception != wantError || results[1].Status != "panic" || results[1].Exception != nil || results[2].Status != "return" || results[2].Value != 30 || results[2].Exception != nil {
		t.Fatalf("unexpected results: %#v", results)
	}
	mock.ExpectReturnedTimes(1)
	mock.ExpectThrownTimes(1)
	mock.ExpectReturnedWith(30)
	mock.ExpectNthReturnedWith(3, 30)
	mock.ExpectNthThrownMessage(1, "金木犀")
	results[2].Value = -1
	results[0].Status = "changed"
	if mock.Results()[2].Value != 30 || mock.Results()[0].Status != "throw" {
		t.Fatal("mutating a result snapshot changed the mock")
	}
	mock.ClearCalls()
	if len(mock.Results()) != 0 {
		t.Fatal("clearing calls left results behind")
	}
	mock.ExpectReturnedTimes(0)
	mock.ExpectThrownTimes(0)
}

func TestMockResultExpectationsRejectWrongOutcomes(t *testing.T) {
	mock := NewMockFn[int, int](func(value int) int { return value })
	assertThrownMessage(t, "expected mock return count must not be negative", func() { mock.ExpectReturnedTimes(-1) })
	assertThrownMessage(t, "expected mock throw count must not be negative", func() { mock.ExpectThrownTimes(-1) })
	assertThrownMessage(t, "expected mock to return 1 time(s), got 0", func() { mock.ExpectReturnedTimes(1) })
	assertThrownMessage(t, "expected mock to throw 1 time(s), got 0", func() { mock.ExpectThrownTimes(1) })
	assertThrownMessage(t, "mock call index must be at least 1", func() { mock.ExpectNthReturnedWith(0, 1) })
	assertThrownMessage(t, "expected mock call 1, got 0 call(s)", func() { mock.ExpectNthReturnedWith(1, 1) })
	assertThrownMessage(t, "expected mock error fragment must not be empty", func() { mock.ExpectNthThrownMessage(1, "") })
	mock.ImplementOnce(func(value int) int { panic(__kinmokuseiThrown{err: errors.New("")}) })
	assertThrownMessage(t, "", func() { mock.Invoke(1) })
	assertThrownMessage(t, "expected mock to return 0", func() { mock.ExpectReturnedWith(0) })
	assertThrownMessage(t, "got status \"throw\"", func() { mock.ExpectNthReturnedWith(1, 0) })
	assertThrownMessage(t, "expected mock call 1 to throw containing \"missing\", got \"\"", func() { mock.ExpectNthThrownMessage(1, "missing") })
	mock.Invoke(2)
	assertThrownMessage(t, "expected mock call 2 to return 3, got 2", func() { mock.ExpectNthReturnedWith(2, 3) })
	assertThrownMessage(t, "got status \"return\"", func() { mock.ExpectNthThrownMessage(2, "missing") })
	collections := NewMockFn[int, []int](func(value int) []int { return nil })
	collections.Invoke(1)
	collections.ExpectReturnedWith(nil)
	assertThrownMessage(t, "expected mock to return", func() { collections.ExpectReturnedWith([]int{}) })
}

func TestMockResultsTrackInvocationOrderAndIgnoreClearedInflightCalls(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	mock := NewMockFn[int, int](func(value int) int {
		if value == 1 {
			close(started)
			<-release
		}
		return value * 10
	})
	go func() { defer close(done); mock.Invoke(1) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first callback did not start")
	}
	mock.Invoke(2)
	results := mock.Results()
	if len(results) != 2 || results[0].Status != "incomplete" || results[1].Status != "return" || results[1].Value != 20 {
		t.Fatalf("results follow completion order: %#v", results)
	}
	mock.ExpectReturnedTimes(1)
	mock.ExpectThrownTimes(0)
	assertThrownMessage(t, "got status \"incomplete\"", func() { mock.ExpectNthReturnedWith(1, 10) })
	mock.ClearCalls()
	mock.Invoke(3)
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cleared callback did not finish")
	}
	results = mock.Results()
	if len(results) != 1 || results[0].Status != "return" || results[0].Value != 30 {
		t.Fatalf("old completion changed new history: %#v", results)
	}
	mock.ExpectCalledTimes(1)
	mock.ExpectNthCalledWith(1, 3)
	mock.ExpectNthReturnedWith(1, 30)
}

func TestMockResultsStayInInvocationOrderDuringRecursion(t *testing.T) {
	var mock *MockFn[int, int]
	mock = NewMockFn[int, int](func(value int) int {
		results := mock.Results()
		if results[len(results)-1].Status != "incomplete" {
			t.Fatal("active callback did not have an incomplete result")
		}
		if value == 0 {
			return 0
		}
		return mock.Invoke(value-1) + 1
	})
	if mock.Invoke(2) != 2 {
		t.Fatal("recursive result changed")
	}
	mock.ExpectReturnedTimes(3)
	for index, want := range []int{2, 1, 0} {
		mock.ExpectNthReturnedWith(index+1, want)
	}
}

func TestMockActionResultsExposeCompletionAndErrors(t *testing.T) {
	mock := NewMockAction[string](func(value string) {})
	mock.ImplementOnce(func(value string) { panic(__kinmokuseiThrown{err: errors.New("notification failed")}) })
	mock.ImplementOnce(func(value string) { panic("native action result") })
	assertThrownMessage(t, "notification failed", func() { mock.Invoke("throw") })
	func() {
		defer func() {
			if got := recover(); got != "native action result" {
				t.Fatalf("panic = %#v", got)
			}
		}()
		mock.Invoke("panic")
	}()
	mock.Invoke("return")
	results := mock.Results()
	if len(results) != 3 || results[0].Status != "throw" || results[0].Exception.Error() != "notification failed" || results[1].Status != "panic" || results[2].Status != "return" {
		t.Fatalf("action results = %#v", results)
	}
	mock.ExpectReturnedTimes(1)
	mock.ExpectThrownTimes(1)
	mock.ExpectNthThrownMessage(1, "notification")
	results[0].Status = "changed"
	if mock.Results()[0].Status != "throw" {
		t.Fatal("changing the result snapshot changed the action")
	}
	mock.ClearCalls()
	if len(mock.Results()) != 0 {
		t.Fatal("clearing action calls left results behind")
	}
}

func TestMockActionRecordsTypedCallsAndBindsVoidCallback(t *testing.T) {
	type event struct {
		Topic string
		IDs   []int
	}
	var received []event
	mock := NewMockAction[event](func(value event) { received = append(received, value) })
	var callback func(event) = mock.Invoke
	mock.ExpectCalledTimes(0)
	callback(event{Topic: "金木犀", IDs: []int{1, 2}})
	mock.ExpectCalledTimes(1)
	mock.ExpectCalledWith(event{Topic: "金木犀", IDs: []int{1, 2}})
	mock.ExpectNthCalledWith(1, event{Topic: "金木犀", IDs: []int{1, 2}})
	if len(received) != 1 || received[0].Topic != "金木犀" {
		t.Fatalf("callback events = %#v", received)
	}
	calls := mock.Calls()
	calls[0] = event{Topic: "replaced"}
	if mock.Calls()[0].Topic != "金木犀" {
		t.Fatal("changing the returned history slice changed the action")
	}
}

func TestMockActionQueueAndClearCallsPreserveBehavior(t *testing.T) {
	var events []string
	mock := NewMockAction[string](func(value string) { events = append(events, "initial:"+value) })
	mock.Invoke("first")
	mock.ImplementOnce(func(value string) { events = append(events, "once:"+value) })
	mock.ImplementOnce(func(value string) { events = append(events, "second:"+value) })
	mock.Implement(func(value string) { events = append(events, "default:"+value) })
	mock.ClearCalls()
	mock.ExpectCalledTimes(0)
	mock.Invoke("a")
	mock.Invoke("b")
	mock.Invoke("c")
	if got := strings.Join(events, ","); got != "initial:first,once:a,second:b,default:c" {
		t.Fatalf("action order = %s", got)
	}
	mock.ExpectCalledTimes(3)
	mock.ExpectNthCalledWith(1, "a")
}

func TestMockActionRecordsThrownAndPanickingCalls(t *testing.T) {
	var values []int
	mock := NewMockAction[int](func(value int) { values = append(values, value) })
	mock.ImplementOnce(func(value int) { panic(__kinmokuseiThrown{err: errors.New("action failed")}) })
	mock.ImplementOnce(func(value int) { panic("native action panic") })
	assertThrownMessage(t, "action failed", func() { mock.Invoke(1) })
	func() {
		defer func() {
			if recovered := recover(); recovered != "native action panic" {
				t.Fatalf("panic = %#v", recovered)
			}
		}()
		mock.Invoke(2)
	}()
	mock.Invoke(3)
	if len(values) != 1 || values[0] != 3 {
		t.Fatalf("default actions = %v", values)
	}
	mock.ExpectCalledTimes(3)
	mock.ExpectNthCalledWith(1, 1)
	mock.ExpectNthCalledWith(2, 2)
}

func TestMockActionValidationPreservesQueuedAndDefaultBehavior(t *testing.T) {
	assertThrownMessage(t, "mock implementation must not be nil", func() { NewMockAction[int](nil) })
	var values []int
	mock := NewMockAction[int](func(value int) { values = append(values, value) })
	mock.ImplementOnce(func(value int) { values = append(values, value*10) })
	assertThrownMessage(t, "mock implementation must not be nil", func() { mock.Implement(nil) })
	assertThrownMessage(t, "mock implementation must not be nil", func() { mock.ImplementOnce(nil) })
	assertThrownMessage(t, "expected mock to be called 1 time(s), got 0", func() { mock.ExpectCalledTimes(1) })
	assertThrownMessage(t, "expected mock to be called with 1", func() { mock.ExpectCalledWith(1) })
	assertThrownMessage(t, "mock call index must be at least 1", func() { mock.ExpectNthCalledWith(0, 1) })
	mock.Invoke(1)
	mock.Invoke(2)
	if len(values) != 2 || values[0] != 10 || values[1] != 2 {
		t.Fatalf("invalid configuration changed behavior: %v", values)
	}
}

func TestMockActionCallbackCanInspectAndInvokeAction(t *testing.T) {
	var mock *MockAction[int]
	mock = NewMockAction[int](func(value int) {
		mock.CallCount()
		mock.Calls()
		if value > 0 {
			mock.Invoke(value - 1)
		}
	})
	done := make(chan struct{})
	go func() { mock.Invoke(2); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("action held its lock while running the callback")
	}
	mock.ExpectCalledTimes(3)
	mock.ExpectNthCalledWith(1, 2)
	mock.ExpectNthCalledWith(3, 0)
}

func TestMockActionConcurrentInvocationsConsumeQueueOnce(t *testing.T) {
	const count = 32
	results := make(chan int, count+1)
	mock := NewMockAction[int](func(value int) { results <- -1 })
	for value := 0; value < count; value++ {
		value := value
		mock.ImplementOnce(func(argument int) { results <- value })
	}
	var workers sync.WaitGroup
	for argument := 0; argument < count; argument++ {
		workers.Add(1)
		go func(argument int) {
			defer workers.Done()
			mock.Invoke(argument)
			mock.Calls()
			mock.CallCount()
		}(argument)
	}
	workers.Wait()
	seen := make(map[int]bool)
	for index := 0; index < count; index++ {
		result := <-results
		if result < 0 || result >= count || seen[result] {
			t.Fatalf("one-shot action was lost or reused: %d", result)
		}
		seen[result] = true
	}
	mock.ExpectCalledTimes(count)
	for argument := 0; argument < count; argument++ {
		mock.ExpectCalledWith(argument)
	}
	mock.Invoke(count)
	if result := <-results; result != -1 {
		t.Fatalf("default action result = %d", result)
	}
}

func TestSharedFixtureLifecycleIsTypedAndFreshForEachRun(t *testing.T) {
	type resource struct {
		path  string
		calls int
	}
	t.Setenv("VASEST_SHARED_FIXTURE", "original")
	var events []string
	var paths []string
	var contexts []*TestContext
	var resources []*resource
	suite := NewFixtureSuite("shared lifecycle", func(context *TestContext) *resource {
		events = append(events, "setup")
		value := &resource{path: context.TempDir()}
		context.SetEnv("VASEST_SHARED_FIXTURE", "suite")
		paths = append(paths, value.path)
		resources = append(resources, value)
		contexts = append(contexts, context)
		context.Cleanup(func() { events = append(events, "shared cleanup") })
		return value
	})
	suite.BeforeAll(func(value *resource) { events = append(events, "beforeAll") })
	suite.BeforeEach(func(value *resource) {
		events = append(events, "beforeEach")
		if os.Getenv("VASEST_SHARED_FIXTURE") != "suite" {
			t.Fatal("case environment leaked into the shared fixture")
		}
	})
	suite.AfterEach(func(value *resource) {
		events = append(events, "afterEach")
		if os.Getenv("VASEST_SHARED_FIXTURE") != "case" {
			t.Fatal("case fixture disappeared before afterEach")
		}
	})
	suite.AfterAll(func(value *resource) {
		events = append(events, "afterAll")
		if value.calls != 3 || os.Getenv("VASEST_SHARED_FIXTURE") != "suite" {
			t.Fatal("shared value or environment lost before afterAll")
		}
		if _, err := os.Stat(value.path); err != nil {
			t.Fatal(err)
		}
	})
	body := func(value *resource, context *TestContext) {
		events = append(events, "body")
		if value != resources[len(resources)-1] {
			t.Fatal("case received a different shared value")
		}
		value.calls++
		context.SetEnv("VASEST_SHARED_FIXTURE", "case")
		context.Cleanup(func() { events = append(events, "case cleanup") })
	}
	suite.Test("single", body)
	FixtureSuiteTestEach(suite, []int{1, 2}, func(row int) string { return fmt.Sprintf("row %d", row) }, func(row int, value *resource, context *TestContext) { body(value, context) })
	if len(events) != 0 {
		t.Fatal("shared setup ran during registration")
	}
	for run := 0; run < 2; run++ {
		events = nil
		summary := suite.RunTo(&bytes.Buffer{})
		want := "setup,beforeAll,beforeEach,body,afterEach,case cleanup,beforeEach,body,afterEach,case cleanup,beforeEach,body,afterEach,case cleanup,afterAll,shared cleanup"
		if !summary.Success() || summary.Passed != 3 || strings.Join(events, ",") != want {
			t.Fatalf("summary=%#v events=%v", summary, events)
		}
		if os.Getenv("VASEST_SHARED_FIXTURE") != "original" {
			t.Fatal("shared environment was not restored")
		}
		if _, err := os.Stat(paths[run]); !os.IsNotExist(err) {
			t.Fatal("shared directory leaked")
		}
		assertThrownMessage(t, "test context is already closed", func() { contexts[run].Cleanup(func() {}) })
	}
	if resources[0] == resources[1] || paths[0] == paths[1] || contexts[0] == contexts[1] {
		t.Fatal("rerun reused a shared fixture")
	}
}

func TestSharedFixtureSetupFailuresSkipBodiesAndReleasePartialResources(t *testing.T) {
	for _, phase := range []string{"setup", "beforeAll"} {
		t.Run(phase, func(t *testing.T) {
			var path string
			var context *TestContext
			var events []string
			suite := NewFixtureSuite("partial setup", func(current *TestContext) string {
				context = current
				path = current.TempDir()
				if phase == "setup" {
					panic(__kinmokuseiThrown{err: errors.New("")})
				}
				return path
			})
			suite.BeforeAll(func(string) { events = append(events, "beforeAll"); panic(__kinmokuseiThrown{err: errors.New("")}) })
			suite.AfterAll(func(string) { events = append(events, "afterAll") })
			suite.Test("active", func(string, *TestContext) { t.Fatal("body ran after shared setup failure") })
			suite.Skip("disabled", func(string, *TestContext) { t.Fatal("skipped body ran") })
			suite.Todo("planned")
			summary := suite.RunTo(&bytes.Buffer{})
			if summary.Success() || summary.Failed != 1 || summary.Skipped != 2 || summary.Todo != 1 || summary.Results[0].Name != "<beforeAll>" || len(summary.Results[0].Failures) != 1 || summary.Results[0].Failures[0].Phase != "beforeAll" || summary.Results[0].Failures[0].Message != "" {
				t.Fatalf("summary=%#v", summary)
			}
			if (phase == "setup" && len(events) != 0) || (phase == "beforeAll" && strings.Join(events, ",") != "beforeAll,afterAll") {
				t.Fatalf("hooks ran with an unprepared value: %v", events)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("partial setup leaked its directory")
			}
			assertThrownMessage(t, "test context is already closed", func() { context.Cleanup(func() {}) })
		})
	}
}

func TestSharedFixtureCleanupAggregatesErrorsAndIncludesDuration(t *testing.T) {
	var events []string
	suite := NewFixtureSuite("shared cleanup", func(context *TestContext) int {
		context.Cleanup(func() { events = append(events, "first"); panic(__kinmokuseiThrown{err: errors.New("first failure")}) })
		context.Cleanup(func() {
			events = append(events, "second")
			context.Cleanup(func() { events = append(events, "nested"); time.Sleep(time.Millisecond) })
			panic(__kinmokuseiThrown{err: errors.New("")})
		})
		return 42
	})
	suite.AfterAll(func(int) {
		events = append(events, "afterAll")
		panic(__kinmokuseiThrown{err: errors.New("hook failure")})
	})
	suite.Test("passes", func(value int, context *TestContext) { expectEqual(value, 42) })
	summary := suite.RunTo(&bytes.Buffer{})
	want := []TestFailure{{Phase: "afterAll", Message: "hook failure"}, {Phase: "cleanup", Message: ""}, {Phase: "cleanup", Message: "first failure"}}
	if summary.Success() || summary.Passed != 1 || summary.Failed != 1 || len(summary.Results) != 2 || summary.Results[1].Name != "<afterAll>" || fmt.Sprint(summary.Results[1].Failures) != fmt.Sprint(want) || summary.Results[1].DurationNs < int64(time.Millisecond) || summary.DurationNs < summary.Results[1].DurationNs || strings.Join(events, ",") != "afterAll,second,nested,first" {
		t.Fatalf("summary=%#v events=%v", summary, events)
	}
}

func TestSharedFixtureNativePanicsStillReleaseResourcesAndAllowRerun(t *testing.T) {
	for _, phase := range []string{"setup", "beforeAll", "body", "afterAll", "cleanup"} {
		t.Run(phase, func(t *testing.T) {
			panics := true
			var contexts []*TestContext
			var paths []string
			suite := NewFixtureSuite("shared panic", func(context *TestContext) string {
				contexts = append(contexts, context)
				path := context.TempDir()
				paths = append(paths, path)
				context.Cleanup(func() {
					if phase == "cleanup" && panics {
						panic("shared panic")
					}
				})
				if phase == "setup" && panics {
					panic("shared panic")
				}
				return path
			})
			suite.BeforeAll(func(string) {
				if phase == "beforeAll" && panics {
					panic("shared panic")
				}
			})
			suite.AfterAll(func(path string) {
				if _, err := os.Stat(path); err != nil {
					t.Fatal(err)
				}
				if phase == "afterAll" && panics {
					panic("shared panic")
				}
			})
			suite.Test("case", func(string, *TestContext) {
				if phase == "body" && panics {
					panic("shared panic")
				}
			})
			func() {
				defer func() {
					if got := recover(); got != "shared panic" {
						t.Fatalf("panic=%#v", got)
					}
				}()
				suite.RunTo(&bytes.Buffer{})
			}()
			if _, err := os.Stat(paths[0]); !os.IsNotExist(err) {
				t.Fatal("panic leaked a shared resource")
			}
			assertThrownMessage(t, "test context is already closed", func() { contexts[0].Cleanup(func() {}) })
			panics = false
			if summary := suite.RunTo(&bytes.Buffer{}); !summary.Success() {
				t.Fatalf("rerun failed after panic: %#v", summary)
			}
			if _, err := os.Stat(paths[1]); !os.IsNotExist(err) {
				t.Fatal("rerun leaked a shared resource")
			}
		})
	}
}

func TestSharedFixtureSelectionBailAndShuffle(t *testing.T) {
	for _, mode := range []string{"filter", "skip", "todo", "no match", "bail", "shuffle"} {
		t.Run(mode, func(t *testing.T) {
			filter := map[string]string{"filter": "row 2", "skip": "disabled", "todo": "planned", "no match": "absent"}[mode]
			t.Setenv("VASEST_FILTER", filter)
			if mode == "bail" {
				t.Setenv("VASEST_BAIL", "1")
			}
			if mode == "shuffle" {
				t.Setenv("VASEST_SHUFFLE_SEED", "42")
			}
			setups, cleanups := 0, 0
			var rows []int
			suite := NewFixtureSuite("selected shared", func(context *TestContext) int {
				setups++
				context.Cleanup(func() { cleanups++ })
				return 42
			})
			suite.Skip("disabled", func(int, *TestContext) { t.Fatal("skipped body ran") })
			suite.Todo("planned")
			FixtureSuiteTestEach(suite, []int{1, 2, 3}, func(row int) string { return fmt.Sprintf("row %d", row) }, func(row int, value int, context *TestContext) {
				expectEqual(value, 42)
				rows = append(rows, row)
				if mode == "bail" {
					expectTrue(false)
				}
			})
			summary := suite.RunTo(&bytes.Buffer{})
			switch mode {
			case "skip", "todo", "no match":
				if !summary.Success() || setups != 0 || cleanups != 0 || len(rows) != 0 {
					t.Fatalf("inactive selection allocated shared resources: %#v", summary)
				}
			case "filter":
				if !summary.Success() || fmt.Sprint(rows) != "[2]" || setups != 1 || cleanups != 1 {
					t.Fatalf("filtered rows=%v summary=%#v", rows, summary)
				}
			case "bail":
				if summary.Failed != 1 || summary.Skipped != 3 || len(rows) != 1 || setups != 1 || cleanups != 1 {
					t.Fatalf("bail rows=%v summary=%#v", rows, summary)
				}
			case "shuffle":
				first := fmt.Sprint(rows)
				rows = nil
				second := suite.RunTo(&bytes.Buffer{})
				if !summary.Success() || !second.Success() || len(rows) != 3 || fmt.Sprint(rows) != first || setups != 2 || cleanups != 2 {
					t.Fatalf("shuffle rows=%v summary=%#v", rows, second)
				}
			}
		})
	}
}

func TestSharedFixtureValidationAndAtomicTables(t *testing.T) {
	assertThrownMessage(t, "fixture suite setup must not be nil", func() { NewFixtureSuite[int]("nil", nil) })
	assertThrownMessage(t, "suite name must not be empty", func() { NewFixtureSuite(" ", func(*TestContext) int { return 0 }) })
	suite := NewFixtureSuite("validation", func(*TestContext) int { return 42 })
	for _, test := range []struct {
		message string
		attempt func()
	}{
		{"beforeAll body must not be nil", func() { suite.BeforeAll(nil) }}, {"afterAll body must not be nil", func() { suite.AfterAll(nil) }},
		{"beforeEach body must not be nil", func() { suite.BeforeEach(nil) }}, {"afterEach body must not be nil", func() { suite.AfterEach(nil) }},
		{"test body must not be nil", func() { suite.Test("invalid", nil) }}, {"skip body must not be nil", func() { suite.Skip("invalid", nil) }},
		{"test name must not be empty", func() { suite.Test(" ", func(int, *TestContext) {}) }},
		{"testEach name callback must not be nil", func() { FixtureSuiteTestEach(suite, []int{1}, nil, func(int, int, *TestContext) {}) }},
		{"testEach body must not be nil", func() { FixtureSuiteTestEach(suite, []int{1}, func(int) string { return "row" }, nil) }},
		{"testEach requires at least one value", func() {
			FixtureSuiteTestEach(suite, []int{}, func(int) string { return "row" }, func(int, int, *TestContext) {})
		}},
	} {
		assertThrownMessage(t, test.message, test.attempt)
	}
	suite.Test("existing", func(int, *TestContext) {})
	assertThrownMessage(t, "duplicate test name", func() { suite.Todo("existing") })
	assertThrownMessage(t, "duplicate test name", func() {
		FixtureSuiteTestEach(suite, []int{1, 2}, func(row int) string {
			if row == 2 {
				suite.Test("row 1", func(int, *TestContext) {})
			}
			return fmt.Sprintf("row %d", row)
		}, func(int, int, *TestContext) { t.Fatal("invalid table body ran") })
	})
	if summary := suite.RunTo(&bytes.Buffer{}); !summary.Success() || summary.Passed != 2 {
		t.Fatalf("invalid registration changed existing cases: %#v", summary)
	}
}

func TestSharedFixtureAllowsNilValuesAndRejectsRecursiveRuns(t *testing.T) {
	suite := NewFixtureSuite("nil value", func(*TestContext) *int { return nil })
	suite.AfterAll(func(value *int) {
		if value != nil {
			t.Fatal("nil shared value changed")
		}
	})
	suite.Test("case", func(value *int, context *TestContext) {
		if value != nil {
			t.Fatal("nil shared value changed")
		}
		assertThrownMessage(t, "fixture suite is already running", func() { suite.RunTo(&bytes.Buffer{}) })
	})
	for run := 0; run < 2; run++ {
		if summary := suite.RunTo(&bytes.Buffer{}); !summary.Success() {
			t.Fatalf("summary=%#v", summary)
		}
	}
}

func TestBeforeAllContextCleanupAndHookReplacement(t *testing.T) {
	suite := NewSuite("shared context")
	assertThrownMessage(t, "beforeAllWithContext body must not be nil", func() { suite.BeforeAllWithContext(nil) })
	var events []string
	suite.BeforeAllWithContext(func(*TestContext) { t.Fatal("replaced context hook ran") })
	suite.BeforeAll(func() { events = append(events, "plain") })
	suite.Test("case", func() {})
	if summary := suite.RunTo(&bytes.Buffer{}); !summary.Success() || strings.Join(events, ",") != "plain" {
		t.Fatalf("summary=%#v events=%v", summary, events)
	}
	suite.BeforeAll(func() { t.Fatal("replaced plain hook ran") })
	suite.BeforeAllWithContext(func(context *TestContext) {
		events = append(events, "context")
		context.Cleanup(func() { events = append(events, "cleanup") })
	})
	suite.AfterAll(func() { events = append(events, "afterAll") })
	events = nil
	if summary := suite.RunTo(&bytes.Buffer{}); !summary.Success() || strings.Join(events, ",") != "context,afterAll,cleanup" {
		t.Fatalf("summary=%#v events=%v", summary, events)
	}
}

func TestTypedFixtureLifecycleAndFreshValues(t *testing.T) {
	type resource struct{ path string }
	suite := NewSuite("typed fixtures")
	var events []string
	var contexts []*TestContext
	var paths []string
	suite.BeforeEach(func() { events = append(events, "before") })
	suite.AfterEach(func() {
		events = append(events, "after")
		if _, err := os.Stat(paths[len(paths)-1]); err != nil {
			t.Fatalf("fixture unavailable in afterEach: %v", err)
		}
	})
	setup := func(context *TestContext) resource {
		events = append(events, "setup")
		contexts = append(contexts, context)
		path := context.TempDir()
		paths = append(paths, path)
		context.Cleanup(func() {
			events = append(events, "cleanup")
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("fixture removed before custom cleanup: %v", err)
			}
			time.Sleep(time.Millisecond)
		})
		return resource{path}
	}
	SuiteTestWithFixture(suite, "case", setup, func(value resource, context *TestContext) {
		events = append(events, "body")
		if context != contexts[len(contexts)-1] || value.path != paths[len(paths)-1] {
			t.Fatal("fixture value or context changed before body")
		}
	})
	if len(events) != 0 {
		t.Fatal("fixture ran during registration")
	}
	for run := 0; run < 2; run++ {
		summary := suite.RunTo(&bytes.Buffer{})
		if !summary.Success() || summary.Passed != 1 || summary.Results[0].DurationNs < int64(time.Millisecond) {
			t.Fatalf("summary = %#v", summary)
		}
		if _, err := os.Stat(paths[run]); !os.IsNotExist(err) {
			t.Fatal("fixture directory leaked")
		}
		assertThrownMessage(t, "test context is already closed", func() { contexts[run].Cleanup(func() {}) })
	}
	if strings.Join(events, ",") != "before,setup,body,after,cleanup,before,setup,body,after,cleanup" || contexts[0] == contexts[1] || paths[0] == paths[1] {
		t.Fatalf("events = %v; fixture state was reused", events)
	}
}

func TestTypedFixtureFailuresRetainPhasesAndCleanup(t *testing.T) {
	for _, stage := range []string{"beforeEach", "fixture", "body"} {
		t.Run(stage, func(t *testing.T) {
			suite := NewSuite("fixture errors")
			var events []string
			throw := func(message string) { panic(__kinmokuseiThrown{err: errors.New(message)}) }
			suite.BeforeEach(func() {
				if stage == "beforeEach" {
					throw("")
				}
			})
			suite.AfterEach(func() { events = append(events, "after"); throw("hook failure") })
			SuiteTestWithFixture(suite, "fails", func(context *TestContext) int {
				events = append(events, "setup")
				context.Cleanup(func() { events = append(events, "cleanup"); throw("cleanup failure") })
				if stage == "fixture" {
					throw("")
				}
				return 42
			}, func(value int, context *TestContext) { events = append(events, "body"); throw("") })
			suite.Test("later", func() { events = append(events, "later") })
			summary := suite.RunTo(&bytes.Buffer{})
			want := []TestFailure{{Phase: stage, Message: ""}, {Phase: "afterEach", Message: "hook failure"}}
			wantEvents := "after,later,after"
			if stage != "beforeEach" {
				want = append(want, TestFailure{Phase: "cleanup", Message: "cleanup failure"})
				wantEvents = "setup,after,cleanup,later,after"
				if stage == "body" {
					wantEvents = "setup,body,after,cleanup,later,after"
				}
			} else {
				wantEvents = "after,after"
			}
			if summary.Success() || summary.Failed != 2 || fmt.Sprint(summary.Results[0].Failures) != fmt.Sprint(want) || strings.Join(events, ",") != wantEvents {
				t.Fatalf("summary = %#v; events = %v", summary, events)
			}
		})
	}
}

func TestTypedFixtureNativePanicRunsCleanupAndHooks(t *testing.T) {
	suite := NewSuite("fixture panic")
	var events []string
	var context *TestContext
	suite.AfterEach(func() { events = append(events, "after") })
	suite.AfterAll(func() { events = append(events, "afterAll") })
	SuiteTestWithFixture(suite, "panic", func(current *TestContext) int {
		context = current
		current.Cleanup(func() { events = append(events, "cleanup") })
		panic("fixture panic")
	}, func(int, *TestContext) { t.Fatal("body ran after fixture panic") })
	func() {
		defer func() {
			if got := recover(); got != "fixture panic" {
				t.Fatalf("panic = %#v", got)
			}
		}()
		suite.RunTo(&bytes.Buffer{})
	}()
	if strings.Join(events, ",") != "after,cleanup,afterAll" {
		t.Fatalf("events = %v", events)
	}
	assertThrownMessage(t, "test context is already closed", func() { context.Cleanup(func() {}) })
}

func TestTypedFixtureTableSelectionBailAndShuffle(t *testing.T) {
	for _, mode := range []string{"filter", "bail", "shuffle"} {
		t.Run(mode, func(t *testing.T) {
			if mode == "filter" {
				t.Setenv("VASEST_FILTER", "row 2")
			} else if mode == "bail" {
				t.Setenv("VASEST_BAIL", "1")
			} else {
				t.Setenv("VASEST_SHUFFLE_SEED", "42")
			}
			suite := NewSuite("fixture rows")
			var prepared, cleaned, bodies []int
			SuiteTestEachWithFixture(suite, []int{1, 2, 3}, func(row int) string { return fmt.Sprintf("row %d", row) },
				func(row int, context *TestContext) string {
					prepared = append(prepared, row)
					context.Cleanup(func() { cleaned = append(cleaned, row) })
					if mode == "bail" {
						panic(__kinmokuseiThrown{err: errors.New("fixture failed")})
					}
					return fmt.Sprintf("value %d", row)
				}, func(row int, fixture string, context *TestContext) {
					if fixture != fmt.Sprintf("value %d", row) {
						t.Fatal("table row received another row's fixture")
					}
					bodies = append(bodies, row)
				})
			summary := suite.RunTo(&bytes.Buffer{})
			if fmt.Sprint(prepared) != fmt.Sprint(cleaned) {
				t.Fatalf("cleanup order changed: prepared=%v, cleaned=%v", prepared, cleaned)
			}
			switch mode {
			case "filter":
				if !summary.Success() || fmt.Sprint(prepared) != "[2]" || summary.Skipped != 2 {
					t.Fatalf("summary = %#v; prepared = %v", summary, prepared)
				}
			case "bail":
				if summary.Failed != 1 || summary.Skipped != 2 || fmt.Sprint(prepared) != "[1]" || len(bodies) != 0 {
					t.Fatalf("summary = %#v; prepared = %v", summary, prepared)
				}
			case "shuffle":
				first := fmt.Sprint(prepared)
				prepared, cleaned, bodies = nil, nil, nil
				second := suite.RunTo(&bytes.Buffer{})
				if !summary.Success() || !second.Success() || first != fmt.Sprint(prepared) || len(bodies) != 3 {
					t.Fatalf("shuffle changed fixture rows: first=%s, second=%v", first, prepared)
				}
			}
		})
	}
}

func TestTypedFixtureRegistrationIsAtomic(t *testing.T) {
	setup := func(*TestContext) int { t.Fatal("setup ran during registration"); return 0 }
	body := func(int, *TestContext) {}
	rowSetup := func(int, *TestContext) int { t.Fatal("row setup ran during registration"); return 0 }
	rowBody := func(int, int, *TestContext) {}
	nameOf := func(row int) string { return fmt.Sprintf("row %d", row) }
	for _, test := range []struct {
		message string
		attempt func(*Suite)
	}{
		{"testWithFixture setup must not be nil", func(s *Suite) { SuiteTestWithFixture[int](s, "invalid", nil, body) }},
		{"testWithFixture body must not be nil", func(s *Suite) { SuiteTestWithFixture(s, "invalid", setup, nil) }},
		{"test name must not be empty", func(s *Suite) { SuiteTestWithFixture(s, " ", setup, body) }},
		{"duplicate test name", func(s *Suite) { SuiteTestWithFixture(s, "existing", setup, body) }},
		{"testEachWithFixture name callback must not be nil", func(s *Suite) { SuiteTestEachWithFixture(s, []int{1}, nil, rowSetup, rowBody) }},
		{"testEachWithFixture setup must not be nil", func(s *Suite) { SuiteTestEachWithFixture[int, int](s, []int{1}, nameOf, nil, rowBody) }},
		{"testEachWithFixture body must not be nil", func(s *Suite) { SuiteTestEachWithFixture(s, []int{1}, nameOf, rowSetup, nil) }},
		{"testEachWithFixture requires at least one value", func(s *Suite) { SuiteTestEachWithFixture(s, []int{}, nameOf, rowSetup, rowBody) }},
		{"test name must not be empty", func(s *Suite) {
			SuiteTestEachWithFixture(s, []int{1, 2}, func(row int) string {
				if row == 2 {
					return " "
				}
				return "first"
			}, rowSetup, rowBody)
		}},
		{"duplicate test name", func(s *Suite) {
			SuiteTestEachWithFixture(s, []int{1, 2}, func(int) string { return "same" }, rowSetup, rowBody)
		}},
		{"naming failure", func(s *Suite) {
			SuiteTestEachWithFixture(s, []int{1, 2}, func(row int) string {
				if row == 2 {
					panic(__kinmokuseiThrown{err: errors.New("naming failure")})
				}
				return "first"
			}, rowSetup, rowBody)
		}},
	} {
		t.Run(test.message, func(t *testing.T) {
			suite := NewSuite("fixture validation")
			suite.Test("existing", func() {})
			assertThrownMessage(t, test.message, func() { test.attempt(suite) })
			summary := suite.RunTo(&bytes.Buffer{})
			if !summary.Success() || summary.Passed != 1 || len(summary.Results) != 1 {
				t.Fatalf("invalid registration changed cases: %#v", summary)
			}
		})
	}
	// A naming callback may itself register a colliding case.
	suite := NewSuite("fixture naming mutation")
	assertThrownMessage(t, "duplicate test name", func() {
		SuiteTestEachWithFixture(suite, []int{1, 2}, func(row int) string {
			if row == 2 {
				suite.Test("row 1", func() {})
			}
			return nameOf(row)
		}, rowSetup, rowBody)
	})
	if summary := suite.RunTo(&bytes.Buffer{}); !summary.Success() || summary.Passed != 1 {
		t.Fatalf("fixture batch partially registered: %#v", summary)
	}
}

func TestContextCleanupRunsAfterHooksInLIFOOrder(t *testing.T) {
	suite := NewSuite("context order")
	var events []string
	var context *TestContext
	suite.BeforeEach(func() { events = append(events, "before") })
	suite.AfterEach(func() {
		events = append(events, "after")
		context.Cleanup(func() { events = append(events, "hook cleanup") })
	})
	suite.AfterAll(func() { events = append(events, "afterAll") })
	suite.TestWithContext("case", func(current *TestContext) {
		context = current
		events = append(events, "body")
		current.Cleanup(func() { events = append(events, "first") })
		current.Cleanup(func() {
			events = append(events, "second")
			current.Cleanup(func() { events = append(events, "nested") })
		})
	})
	summary := suite.RunTo(&bytes.Buffer{})
	if !summary.Success() || strings.Join(events, ",") != "before,body,after,hook cleanup,second,nested,first,afterAll" {
		t.Fatalf("summary = %#v; events = %v", summary, events)
	}
	assertThrownMessage(t, "test context is already closed", func() { context.Cleanup(func() {}) })
}

func TestContextCleanupAggregatesFailuresAndContinues(t *testing.T) {
	suite := NewSuite("cleanup errors")
	var events []string
	suite.AfterEach(func() { expectTrue(false) })
	suite.TestWithContext("fails", func(context *TestContext) {
		context.Cleanup(func() {
			events = append(events, "first")
			panic(__kinmokuseiThrown{err: errors.New("first cleanup")})
		})
		context.Cleanup(func() {
			events = append(events, "second")
			panic(__kinmokuseiThrown{err: errors.New("second cleanup")})
		})
		panic(__kinmokuseiThrown{err: errors.New("body failure")})
	})
	suite.Test("continues", func() { events = append(events, "later") })
	summary := suite.RunTo(&bytes.Buffer{})
	if summary.Failed != 2 || strings.Join(events, ",") != "second,first,later" {
		t.Fatalf("summary = %#v; events = %v", summary, events)
	}
	want := "body failure; afterEach: expected true, got false; cleanup: second cleanup; cleanup: first cleanup"
	if summary.Results[0].Message != want {
		t.Fatalf("failure = %q, want %q", summary.Results[0].Message, want)
	}
}

func TestContextCleanupEmptyErrorFailsAndBails(t *testing.T) {
	t.Setenv("VASEST_BAIL", "1")
	suite := NewSuite("cleanup bail")
	var events []string
	suite.TestWithContext("cleanup failure", func(context *TestContext) {
		context.Cleanup(func() { events = append(events, "remaining") })
		context.Cleanup(func() { panic(__kinmokuseiThrown{err: errors.New("")}) })
	})
	suite.TestWithContext("not run", func(context *TestContext) { t.Fatal("bail ran later context body") })
	summary := suite.RunTo(&bytes.Buffer{})
	if summary.Success() || summary.Failed != 1 || summary.Skipped != 1 || summary.Results[0].Message != "cleanup: " || strings.Join(events, ",") != "remaining" {
		t.Fatalf("summary = %#v; events = %v", summary, events)
	}
}

func TestContextCleanupScopeIsFreshForEachRunAndIncludedInDuration(t *testing.T) {
	suite := NewSuite("fresh context")
	var contexts []*TestContext
	cleaned := 0
	suite.TestWithContext("case", func(context *TestContext) {
		contexts = append(contexts, context)
		context.Cleanup(func() { time.Sleep(2 * time.Millisecond); cleaned++ })
	})
	for run := 0; run < 2; run++ {
		summary := suite.RunTo(&bytes.Buffer{})
		if !summary.Success() || cleaned != run+1 || summary.Results[0].DurationNs < int64(2*time.Millisecond) {
			t.Fatalf("run %d summary = %#v; cleaned = %d", run, summary, cleaned)
		}
		assertThrownMessage(t, "test context is already closed", func() { contexts[run].Cleanup(func() {}) })
	}
	if contexts[0] == contexts[1] {
		t.Fatal("suite runs shared a context")
	}
}

func TestContextCleanupRunsBeforePanicPropagates(t *testing.T) {
	for _, stage := range []string{"body", "afterEach", "cleanup"} {
		t.Run(stage, func(t *testing.T) {
			suite := NewSuite("cleanup panic")
			var events []string
			var context *TestContext
			suite.AfterEach(func() {
				events = append(events, "after")
				if stage == "afterEach" {
					panic("original panic")
				}
			})
			suite.AfterAll(func() { events = append(events, "afterAll") })
			suite.TestWithContext("case", func(current *TestContext) {
				context = current
				current.Cleanup(func() { events = append(events, "first") })
				current.Cleanup(func() {
					events = append(events, "second")
					if stage == "cleanup" {
						panic("original panic")
					}
				})
				if stage == "body" {
					panic("original panic")
				}
			})
			func() {
				defer func() {
					if got := recover(); got != "original panic" {
						t.Fatalf("panic = %#v", got)
					}
				}()
				suite.RunTo(&bytes.Buffer{})
			}()
			if strings.Join(events, ",") != "after,second,first,afterAll" {
				t.Fatalf("cleanup order = %v", events)
			}
			assertThrownMessage(t, "test context is already closed", func() { context.Cleanup(func() {}) })
		})
	}
}

func TestContextRegistrationAndSnapshotScope(t *testing.T) {
	assertThrownMessage(t, "cleanup registrar must not be nil", func() { NewTestContext(nil) })
	suite := NewSuite("context validation")
	assertThrownMessage(t, "testWithContext body must not be nil", func() { suite.TestWithContext("nil", nil) })
	assertThrownMessage(t, "test name must not be empty", func() { suite.TestWithContext(" ", func(*TestContext) {}) })
	suite.TestWithContext("case", func(context *TestContext) {
		assertThrownMessage(t, "cleanup body must not be nil", func() { context.Cleanup(nil) })
		context.Cleanup(func() {
			if got := os.Getenv("VASEST_SNAPSHOT_SCOPE"); got != "\"context validation\" / \"case\"" {
				t.Fatalf("cleanup snapshot scope = %q", got)
			}
		})
	})
	assertThrownMessage(t, "duplicate test name", func() { suite.TestWithContext("case", func(*TestContext) {}) })
	assertThrownMessage(t, "duplicate test name", func() { suite.Test("case", func() {}) })
	summary := suite.RunTo(&bytes.Buffer{})
	if !summary.Success() || summary.Passed != 1 {
		t.Fatalf("registration changed existing cases: %#v", summary)
	}
}

func TestContextFilteringDoesNotRunBodiesOrHooks(t *testing.T) {
	t.Setenv("VASEST_FILTER", "absent")
	suite := NewSuite("filtered context")
	suite.BeforeAll(func() { t.Fatal("filtered suite ran setup") })
	suite.AfterAll(func() { t.Fatal("filtered suite ran cleanup") })
	suite.TestWithContext("case", func(*TestContext) { t.Fatal("filtered body ran") })
	summary := suite.RunTo(&bytes.Buffer{})
	if !summary.Success() || summary.Skipped != 1 || len(summary.Results) != 0 {
		t.Fatalf("filtered summary = %#v", summary)
	}
}

func TestContextTempDirsAreUniqueAndLiveThroughAfterEach(t *testing.T) {
	suite := NewSuite("context directories")
	var paths []string
	suite.TestWithContext("directories", func(context *TestContext) {
		for index := 0; index < 2; index++ {
			path := context.TempDir()
			paths = append(paths, path)
			if err := os.Mkdir(filepath.Join(path, "nested"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(path, "nested", "fixture.txt"), []byte("金木犀"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		if paths[0] == paths[1] {
			t.Fatal("temporary directory calls reused a path")
		}
	})
	suite.AfterEach(func() {
		for _, path := range paths {
			if data, err := os.ReadFile(filepath.Join(path, "nested", "fixture.txt")); err != nil || string(data) != "金木犀" {
				t.Fatalf("fixture unavailable in afterEach: data=%q, error=%v", data, err)
			}
		}
	})
	summary := suite.RunTo(&bytes.Buffer{})
	if !summary.Success() {
		t.Fatalf("summary = %#v", summary)
	}
	for _, path := range paths {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("directory remained after case: %s, error=%v", path, err)
		}
	}
}

func TestContextFixturesRestoreAfterThrowOrPanic(t *testing.T) {
	for _, stage := range []string{"body throw", "body panic", "hook throw", "hook panic", "cleanup throw", "cleanup panic"} {
		t.Run(stage, func(t *testing.T) {
			t.Setenv("VASEST_CONTEXT_FIXTURE", "original")
			suite := NewSuite("fixture failure")
			var path string
			fail := func() {
				if strings.HasSuffix(stage, "panic") {
					panic("fixture panic")
				}
				panic(__kinmokuseiThrown{err: errors.New("fixture throw")})
			}
			suite.TestWithContext("case", func(context *TestContext) {
				path = context.TempDir()
				context.SetEnv("VASEST_CONTEXT_FIXTURE", "temporary")
				if strings.HasPrefix(stage, "cleanup") {
					context.Cleanup(fail)
				}
				if strings.HasPrefix(stage, "body") {
					fail()
				}
			})
			suite.AfterEach(func() {
				if _, err := os.Stat(path); err != nil || os.Getenv("VASEST_CONTEXT_FIXTURE") != "temporary" {
					t.Fatalf("fixtures vanished before afterEach: %v", err)
				}
				if strings.HasPrefix(stage, "hook") {
					fail()
				}
			})
			func() {
				defer func() {
					got := recover()
					if strings.HasSuffix(stage, "panic") && got != "fixture panic" {
						t.Fatalf("panic = %#v", got)
					}
					if strings.HasSuffix(stage, "throw") && got != nil {
						t.Fatalf("throw escaped suite: %#v", got)
					}
				}()
				summary := suite.RunTo(&bytes.Buffer{})
				if summary.Success() || summary.Failed != 1 {
					t.Fatalf("failure summary = %#v", summary)
				}
			}()
			if _, err := os.Stat(path); !os.IsNotExist(err) || os.Getenv("VASEST_CONTEXT_FIXTURE") != "original" {
				t.Fatalf("fixtures not restored: stat=%v, env=%q", err, os.Getenv("VASEST_CONTEXT_FIXTURE"))
			}
		})
	}
}

func TestContextSetEnvRestoresOriginalAndUnsetValuesInLIFOOrder(t *testing.T) {
	for _, original := range []string{"", "original", "<unset>"} {
		t.Run(original, func(t *testing.T) {
			const key = "VASEST_CONTEXT_ENV_ORDER"
			t.Setenv(key, original)
			if original == "<unset>" {
				if err := os.Unsetenv(key); err != nil {
					t.Fatal(err)
				}
			}
			suite := NewSuite("context environment")
			suite.TestWithContext("case", func(context *TestContext) {
				context.SetEnv(key, "first")
				context.Cleanup(func() {
					if got := os.Getenv(key); got != "first" {
						t.Fatalf("nested restoration order: got %q", got)
					}
				})
				context.SetEnv(key, "second")
			})
			suite.AfterEach(func() {
				if got := os.Getenv(key); got != "second" {
					t.Fatalf("environment unavailable in afterEach: %q", got)
				}
			})
			summary := suite.RunTo(&bytes.Buffer{})
			actual, exists := os.LookupEnv(key)
			if !summary.Success() || (original == "<unset>" && exists) || (original != "<unset>" && (!exists || actual != original)) {
				t.Fatalf("summary = %#v; environment = %q, exists=%v", summary, actual, exists)
			}
		})
	}
}

func TestContextFixturesRejectClosedScopeBeforeChangingState(t *testing.T) {
	parent := t.TempDir()
	t.Setenv("TMPDIR", parent)
	t.Setenv("VASEST_CLOSED_CONTEXT_ENV", "original")
	suite := NewSuite("closed fixtures")
	var context *TestContext
	suite.TestWithContext("case", func(current *TestContext) { context = current })
	suite.RunTo(&bytes.Buffer{})
	assertThrownMessage(t, "test context is already closed", func() { context.TempDir() })
	assertThrownMessage(t, "test context is already closed", func() { context.SetEnv("VASEST_CLOSED_CONTEXT_ENV", "changed") })
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 0 || os.Getenv("VASEST_CLOSED_CONTEXT_ENV") != "original" {
		t.Fatalf("closed context changed state: entries=%v, error=%v, env=%q", entries, err, os.Getenv("VASEST_CLOSED_CONTEXT_ENV"))
	}
}

func TestContextFixtureSetupFailureLeavesNoCleanupFailure(t *testing.T) {
	parent := t.TempDir()
	t.Setenv("TMPDIR", filepath.Join(parent, "missing"))
	suite := NewSuite("fixture setup errors")
	suite.TestWithContext("create fails", func(context *TestContext) { context.TempDir() })
	suite.TestWithContext("later fixture works", func(context *TestContext) {
		t.Setenv("TMPDIR", parent)
		for _, name := range []string{"", "INVALID=NAME"} {
			assertThrownMessage(t, "cannot set environment variable", func() { context.SetEnv(name, "value") })
		}
		assertThrownMessage(t, "cannot set environment variable", func() { context.SetEnv("VASEST_INVALID_CONTEXT_VALUE", "bad\x00value") })
		path := context.TempDir()
		if _, err := os.Stat(path); err != nil {
			t.Fatal(err)
		}
	})
	summary := suite.RunTo(&bytes.Buffer{})
	if summary.Failed != 1 || summary.Passed != 1 || !strings.Contains(summary.Results[0].Message, "cannot create temporary directory") || strings.Contains(summary.Results[0].Message, "cleanup:") {
		t.Fatalf("setup errors produced incorrect cleanup: %#v", summary)
	}
	if entries, err := os.ReadDir(parent); err != nil || len(entries) != 0 {
		t.Fatalf("fixture directory leaked: entries=%v, error=%v", entries, err)
	}
}

func TestContextFixturesAreIsolatedBetweenCasesAndReruns(t *testing.T) {
	t.Setenv("VASEST_ISOLATED_CONTEXT_ENV", "original")
	suite := NewSuite("isolated fixtures")
	var previous string
	var paths []string
	for _, name := range []string{"first", "second"} {
		suite.TestWithContext(name, func(context *TestContext) {
			if os.Getenv("VASEST_ISOLATED_CONTEXT_ENV") != "original" {
				t.Fatal("previous case environment leaked")
			}
			if previous != "" {
				if _, err := os.Stat(previous); !os.IsNotExist(err) {
					t.Fatal("previous case directory leaked")
				}
			}
			previous = context.TempDir()
			paths = append(paths, previous)
			context.SetEnv("VASEST_ISOLATED_CONTEXT_ENV", "temporary")
		})
	}
	for run := 0; run < 2; run++ {
		if summary := suite.RunTo(&bytes.Buffer{}); !summary.Success() || summary.Passed != 2 {
			t.Fatalf("run %d summary = %#v", run, summary)
		}
	}
	unique := make(map[string]bool)
	for _, path := range paths {
		if unique[path] {
			t.Fatalf("reused fixture path %s", path)
		}
		unique[path] = true
	}
}

func TestSuiteContinuesAfterAssertionFailure(t *testing.T) {
	suite := NewSuite("sample")
	var order []string
	suite.Test("first", func() {
		order = append(order, "first")
		expectEqual(1, 2)
	})
	suite.Test("second", func() {
		order = append(order, "second")
		expectTrue(true)
	})
	suite.Test("third", func() {
		order = append(order, "third")
		expectFalse(true)
	})

	var output bytes.Buffer
	summary := suite.RunTo(&output)
	if summary.Passed != 1 || summary.Failed != 2 || summary.Total() != 3 || summary.Success() || summary.ExitCode() != 1 {
		t.Fatalf("unexpected summary: %#v", summary)
	}
	if strings.Join(order, ",") != "first,second,third" {
		t.Fatalf("execution order = %q", order)
	}
	if len(summary.Results) != 3 || summary.Results[0].Passed || !summary.Results[1].Passed || summary.Results[2].Passed {
		t.Fatalf("unexpected per-test results: %#v", summary.Results)
	}
	for _, fragment := range []string{"FAIL sample / first: expected 2, got 1", "PASS sample / second", "FAIL sample / third", "1 passed, 2 failed"} {
		if !strings.Contains(output.String(), fragment) {
			t.Errorf("output missing %q:\n%s", fragment, output.String())
		}
	}
}

func TestBailStopsLaterCasesButRunsCleanup(t *testing.T) {
	t.Setenv("VASEST_BAIL", "1")
	suite := NewSuite("bail")
	var events []string
	suite.BeforeAll(func() { events = append(events, "beforeAll") })
	suite.BeforeEach(func() { events = append(events, "beforeEach") })
	suite.AfterEach(func() { events = append(events, "afterEach") })
	suite.AfterAll(func() { events = append(events, "afterAll") })
	suite.Skip("already skipped", func() { t.Fatal("skipped body ran") })
	suite.Todo("already todo")
	suite.Test("pass", func() { events = append(events, "pass") })
	suite.Test("fail", func() {
		events = append(events, "fail")
		expectTrue(false)
	})
	suite.Test("not run", func() { t.Fatal("body ran after bail") })
	var output bytes.Buffer
	summary := suite.RunTo(&output)
	if summary.Success() || summary.Passed != 1 || summary.Failed != 1 || summary.Skipped != 2 || summary.Todo != 1 || strings.Join(events, ",") != "beforeAll,beforeEach,pass,afterEach,beforeEach,fail,afterEach,afterAll" {
		t.Fatalf("summary = %#v; events = %v; output = %s", summary, events, output.String())
	}
	if summary.Results[len(summary.Results)-1].Status != "skip" || !strings.Contains(summary.Results[len(summary.Results)-1].Message, "--bail") || !strings.Contains(output.String(), "SKIP bail / not run: not run after failure (--bail)") {
		t.Fatalf("missing bail result: %#v; output = %s", summary.Results, output.String())
	}
}

func TestBailAfterCleanupFailure(t *testing.T) {
	t.Setenv("VASEST_BAIL", "1")
	suite := NewSuite("cleanup bail")
	suite.AfterEach(func() { expectTrue(false) })
	suite.Test("first", func() {})
	suite.Test("second", func() { t.Fatal("second body ran after cleanup failure") })
	summary := suite.RunTo(&bytes.Buffer{})
	if summary.Passed != 0 || summary.Failed != 1 || summary.Skipped != 1 || summary.Results[1].Status != "skip" {
		t.Fatalf("unexpected cleanup bail summary: %#v", summary)
	}
}

func TestEmptyAndSuccessfulSuites(t *testing.T) {
	var output bytes.Buffer
	empty := NewSuite("empty").RunTo(&output)
	if empty.Success() || empty.ExitCode() != 1 || empty.Total() != 0 || !strings.Contains(output.String(), "no tests registered") {
		t.Fatalf("empty suite: %#v, output = %q", empty, output.String())
	}

	suite := NewSuite("success")
	suite.Test("only", func() { expectEqual([]int{1, 2}, []int{1, 2}) })
	output.Reset()
	summary := suite.RunTo(&output)
	if !summary.Success() || summary.ExitCode() != 0 || summary.Passed != 1 || summary.Failed != 0 {
		t.Fatalf("successful suite: %#v", summary)
	}
	suite.RunAndExit()
}

func TestRegistrationRejectsInvalidInputs(t *testing.T) {
	assertThrownMessage(t, "suite name must not be empty", func() { NewSuite("  ") })
	suite := NewSuite("registration")
	assertThrownMessage(t, "test name must not be empty", func() { suite.Test("\t", func() {}) })
	assertThrownMessage(t, "test body must not be nil", func() { suite.Test("nil", nil) })
	assertThrownMessage(t, "skip body must not be nil", func() { suite.Skip("nil skip", nil) })
	assertThrownMessage(t, "test name must not be empty", func() { suite.Skip(" ", func() {}) })
	assertThrownMessage(t, "test name must not be empty", func() { suite.Todo(" ") })
	assertThrownMessage(t, "beforeEach body must not be nil", func() { suite.BeforeEach(nil) })
	assertThrownMessage(t, "afterEach body must not be nil", func() { suite.AfterEach(nil) })
	assertThrownMessage(t, "beforeAll body must not be nil", func() { suite.BeforeAll(nil) })
	assertThrownMessage(t, "afterAll body must not be nil", func() { suite.AfterAll(nil) })
	suite.Test("unique", func() {})
	assertThrownMessage(t, `duplicate test name "unique"`, func() { suite.Test("unique", func() {}) })
	assertThrownMessage(t, `duplicate test name "unique"`, func() { suite.Skip("unique", func() {}) })
	assertThrownMessage(t, `duplicate test name "unique"`, func() { suite.Todo("unique") })
	if summary := suite.RunTo(&bytes.Buffer{}); summary.Total() != 1 {
		t.Fatalf("failed registrations changed case count: %#v", summary)
	}
}

func TestSkipAndTodoMatrix(t *testing.T) {
	suite := NewSuite("status")
	var events []string
	suite.BeforeAll(func() { events = append(events, "beforeAll") })
	suite.AfterAll(func() { events = append(events, "afterAll") })
	suite.BeforeEach(func() { events = append(events, "beforeEach") })
	suite.AfterEach(func() { events = append(events, "afterEach") })
	suite.Test("active", func() { events = append(events, "body") })
	suite.Skip("paused", func() { events = append(events, "wrong") })
	suite.Todo("not implemented")
	var output bytes.Buffer
	summary := suite.RunTo(&output)
	if !summary.Success() || summary.Passed != 1 || summary.Failed != 0 || summary.Skipped != 1 || summary.Todo != 1 || strings.Join(events, ",") != "beforeAll,beforeEach,body,afterEach,afterAll" {
		t.Fatalf("summary = %#v; events = %v; output = %s", summary, events, output.String())
	}
	for i, status := range []string{"pass", "skip", "todo"} {
		if summary.Results[i].Status != status {
			t.Errorf("result %d = %#v, want status %q", i, summary.Results[i], status)
		}
	}
	if !strings.Contains(output.String(), "SKIP status / paused") || !strings.Contains(output.String(), "TODO status / not implemented") || !strings.Contains(output.String(), "1 passed, 0 failed, 1 skipped, 1 todo") {
		t.Fatalf("unexpected output: %s", output.String())
	}
}

func TestOnlySkippedAndTodoCasesDoNotRunHooks(t *testing.T) {
	suite := NewSuite("inactive")
	var hooks int
	suite.BeforeAll(func() { hooks++ })
	suite.AfterAll(func() { hooks++ })
	suite.BeforeEach(func() { hooks++ })
	suite.AfterEach(func() { hooks++ })
	suite.Skip("paused", func() { t.Fatal("skipped body ran") })
	suite.Todo("future")
	summary := suite.RunTo(&bytes.Buffer{})
	if !summary.Success() || summary.Total() != 0 || summary.Skipped != 1 || summary.Todo != 1 || hooks != 0 {
		t.Fatalf("summary = %#v; hooks = %d", summary, hooks)
	}
}

func TestFailOnTodoChangesSuiteOutcome(t *testing.T) {
	t.Setenv("VASEST_FAIL_ON_TODO", "1")
	suite := NewSuite("strict")
	suite.Test("active", func() {})
	suite.Todo("planned")
	var output bytes.Buffer
	summary := suite.RunTo(&output)
	if summary.Success() || summary.ExitCode() != 1 || summary.Failed != 0 || summary.Todo != 1 || !summary.FailOnTodo || !strings.Contains(output.String(), "todo tests are not allowed") {
		t.Fatalf("strict summary = %#v; output = %s", summary, output.String())
	}
	t.Setenv("VASEST_FAIL_ON_TODO", "")
	if summary.Success() {
		t.Fatal("strict summary changed after environment changed")
	}
	t.Setenv("VASEST_FAIL_ON_TODO", "1")
	t.Setenv("VASEST_FILTER", "active")
	selected := suite.RunTo(&bytes.Buffer{})
	if !selected.Success() || selected.Todo != 0 || selected.Passed != 1 {
		t.Fatalf("filtered strict summary = %#v", selected)
	}
	ignored := NewSuite("ignored")
	ignored.Skip("disabled", func() { t.Fatal("skipped body ran") })
	if skipped := ignored.RunTo(&bytes.Buffer{}); !skipped.Success() {
		t.Fatalf("strict mode rejected a skipped-only suite: %#v", skipped)
	}
}

func TestBeforeAllFailurePreservesSkipAndTodoStatuses(t *testing.T) {
	suite := NewSuite("mixed")
	suite.BeforeAll(func() { expectTrue(false) })
	var afterAll bool
	suite.AfterAll(func() { afterAll = true })
	suite.Skip("paused", func() { t.Fatal("skipped body ran") })
	suite.Todo("future")
	suite.Test("active", func() { t.Fatal("active body ran after beforeAll failure") })
	var output bytes.Buffer
	summary := suite.RunTo(&output)
	if summary.Success() || summary.Failed != 1 || summary.Skipped != 2 || summary.Todo != 1 || !afterAll || len(summary.Results) != 4 || summary.Results[3].Status != "skip" {
		t.Fatalf("summary = %#v; afterAll = %v; output = %s", summary, afterAll, output.String())
	}
}

func TestFilterMatchesSkippedAndTodoCases(t *testing.T) {
	matchLog := filepath.Join(t.TempDir(), "matches")
	if err := os.WriteFile(matchLog, nil, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VASEST_FILTER", "paused")
	t.Setenv("VASEST_MATCH_LOG", matchLog)
	suite := NewSuite("filter")
	suite.Skip("paused", func() { t.Fatal("skipped body ran") })
	suite.Todo("future")
	summary := suite.RunTo(&bytes.Buffer{})
	matchData, err := os.ReadFile(matchLog)
	if err != nil || string(matchData) != "1\n" || !summary.Success() || summary.Skipped != 2 || summary.Todo != 0 {
		t.Fatalf("summary = %#v; log = %q; error = %v", summary, matchData, err)
	}
}

func TestParameterizedTestsKeepNamesValuesAndOrder(t *testing.T) {
	type row struct {
		name  string
		input int
		want  int
	}
	suite := NewSuite("table")
	var ran []int
	var beforeCount int
	suite.BeforeEach(func() { beforeCount++ })
	SuiteTestEach(suite, []row{
		{"negative", -3, 3},
		{"zero", 0, 0},
		{"positive", 7, 7},
	}, func(value row) string { return value.name }, func(value row) {
		ran = append(ran, value.input)
		got := value.input
		if got < 0 {
			got = -got
		}
		expectEqual(got, value.want)
	})
	if len(ran) != 0 {
		t.Fatalf("table bodies ran during registration: %v", ran)
	}
	var output bytes.Buffer
	summary := suite.RunTo(&output)
	if !summary.Success() || summary.Passed != 3 || beforeCount != 3 || fmt.Sprint(ran) != "[-3 0 7]" {
		t.Fatalf("summary = %#v; values = %v; hooks = %d", summary, ran, beforeCount)
	}
	for i, name := range []string{"negative", "zero", "positive"} {
		if summary.Results[i].Name != name || !strings.Contains(output.String(), "PASS table / "+name) {
			t.Fatalf("row %d = %#v; output = %s", i, summary.Results[i], output.String())
		}
	}
}

func TestParameterizedRegistrationIsAtomic(t *testing.T) {
	for _, tc := range []struct {
		name    string
		want    string
		attempt func(*Suite)
	}{
		{"nil names", "testEach name callback must not be nil", func(s *Suite) { SuiteTestEach(s, []int{1}, nil, func(int) {}) }},
		{"nil body", "testEach body must not be nil", func(s *Suite) { SuiteTestEach(s, []int{1}, func(int) string { return "one" }, nil) }},
		{"empty values", "testEach requires at least one value", func(s *Suite) { SuiteTestEach(s, []int{}, func(int) string { return "one" }, func(int) {}) }},
		{"blank later name", "test name must not be empty", func(s *Suite) {
			SuiteTestEach(s, []int{1, 2}, func(v int) string {
				if v == 2 {
					return " "
				}
				return "one"
			}, func(int) {})
		}},
		{"duplicate in batch", `duplicate test name "same"`, func(s *Suite) { SuiteTestEach(s, []int{1, 2}, func(int) string { return "same" }, func(int) {}) }},
		{"duplicate existing", `duplicate test name "existing"`, func(s *Suite) {
			SuiteTestEach(s, []int{1, 2}, func(v int) string {
				if v == 2 {
					return "existing"
				}
				return "one"
			}, func(int) {})
		}},
		{"name callback throws", "expected true", func(s *Suite) {
			SuiteTestEach(s, []int{1, 2}, func(v int) string {
				if v == 2 {
					expectTrue(false)
				}
				return "one"
			}, func(int) {})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			suite := NewSuite("atomic")
			suite.Test("existing", func() {})
			assertThrownMessage(t, tc.want, func() { tc.attempt(suite) })
			summary := suite.RunTo(&bytes.Buffer{})
			if summary.Total() != 1 || summary.Results[0].Name != "existing" {
				t.Fatalf("failed batch partially registered: %#v", summary)
			}
		})
	}
}

func TestParameterizedContextsReceiveTypedRowsAndIndependentFixtures(t *testing.T) {
	type row struct {
		name        string
		value, want int
	}
	t.Setenv("VASEST_TABLE_CONTEXT_ENV", "original")
	suite := NewSuite("context table")
	var paths []string
	var contexts []*TestContext
	var ran, cleaned []int
	hooks := 0
	active := ""
	suite.BeforeEach(func() {
		hooks++
		if os.Getenv("VASEST_TABLE_CONTEXT_ENV") != "original" {
			t.Fatal("prior row environment leaked")
		}
		for _, path := range paths {
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("prior row directory leaked")
			}
		}
	})
	suite.AfterEach(func() {
		if _, err := os.Stat(paths[len(paths)-1]); err != nil || os.Getenv("VASEST_TABLE_CONTEXT_ENV") != active {
			t.Fatalf("row fixtures unavailable in afterEach: %v", err)
		}
	})
	SuiteTestEachWithContext(suite, []row{{"negative", -3, 3}, {"zero", 0, 0}, {"positive", 7, 7}},
		func(value row) string { return value.name },
		func(value row, context *TestContext) {
			active = value.name
			ran = append(ran, value.value)
			contexts = append(contexts, context)
			paths = append(paths, context.TempDir())
			context.SetEnv("VASEST_TABLE_CONTEXT_ENV", value.name)
			context.Cleanup(func() {
				cleaned = append(cleaned, value.value)
				if got := os.Getenv("VASEST_SNAPSHOT_SCOPE"); got != fmt.Sprintf("%q / %q", "context table", value.name) {
					t.Fatalf("row cleanup snapshot scope = %q", got)
				}
			})
			actual := value.value
			if actual < 0 {
				actual = -actual
			}
			expectEqual(actual, value.want)
		})
	if len(ran) != 0 || len(contexts) != 0 {
		t.Fatal("row contexts or bodies ran during registration")
	}
	summary := suite.RunTo(&bytes.Buffer{})
	if !summary.Success() || summary.Passed != 3 || hooks != 3 || fmt.Sprint(ran) != "[-3 0 7]" || fmt.Sprint(cleaned) != fmt.Sprint(ran) {
		t.Fatalf("summary = %#v; ran = %v; cleaned = %v; hooks = %d", summary, ran, cleaned, hooks)
	}
	for index, context := range contexts {
		if index > 0 && (context == contexts[index-1] || paths[index] == paths[index-1]) {
			t.Fatal("rows shared a context or directory")
		}
		if _, err := os.Stat(paths[index]); !os.IsNotExist(err) {
			t.Fatal("row directory leaked")
		}
		assertThrownMessage(t, "test context is already closed", func() { context.Cleanup(func() {}) })
	}
}

func TestParameterizedContextRegistrationIsAtomic(t *testing.T) {
	body := func(int, *TestContext) { t.Fatal("invalid batch body ran") }
	for _, test := range []struct {
		name, message string
		attempt       func(*Suite)
	}{
		{"nil names", "testEachWithContext name callback must not be nil", func(s *Suite) { SuiteTestEachWithContext(s, []int{1}, nil, body) }},
		{"nil body", "testEachWithContext body must not be nil", func(s *Suite) { SuiteTestEachWithContext(s, []int{1}, func(int) string { return "one" }, nil) }},
		{"empty table", "testEachWithContext requires at least one value", func(s *Suite) { SuiteTestEachWithContext(s, []int{}, func(int) string { return "one" }, body) }},
		{"blank later name", "test name must not be empty", func(s *Suite) {
			SuiteTestEachWithContext(s, []int{1, 2}, func(value int) string {
				if value == 2 {
					return " "
				}
				return "one"
			}, body)
		}},
		{"duplicate row", "duplicate test name \"same\"", func(s *Suite) { SuiteTestEachWithContext(s, []int{1, 2}, func(int) string { return "same" }, body) }},
		{"duplicate existing", "duplicate test name \"existing\"", func(s *Suite) {
			SuiteTestEachWithContext(s, []int{1, 2}, func(value int) string {
				if value == 2 {
					return "existing"
				}
				return "one"
			}, body)
		}},
		{"naming throws", "naming failure", func(s *Suite) {
			SuiteTestEachWithContext(s, []int{1, 2}, func(value int) string {
				if value == 2 {
					panic(__kinmokuseiThrown{err: errors.New("naming failure")})
				}
				return "one"
			}, body)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			suite := NewSuite("atomic context table")
			suite.TestWithContext("existing", func(*TestContext) {})
			assertThrownMessage(t, test.message, func() { test.attempt(suite) })
			summary := suite.RunTo(&bytes.Buffer{})
			if !summary.Success() || summary.Passed != 1 || len(summary.Results) != 1 || summary.Results[0].Name != "existing" {
				t.Fatalf("invalid batch changed registration: %#v", summary)
			}
		})
	}
}

func TestParameterizedRegistrationRechecksCasesAddedByNamingCallbacks(t *testing.T) {
	for _, contextual := range []bool{false, true} {
		t.Run(fmt.Sprintf("context=%v", contextual), func(t *testing.T) {
			suite := NewSuite("naming changes")
			suite.Test("existing", func() {})
			rowBodies := 0
			nameOf := func(value int) string {
				if value == 3 {
					suite.Test("row 2", func() {})
				}
				return fmt.Sprintf("row %d", value)
			}
			assertThrownMessage(t, "duplicate test name \"row 2\"", func() {
				if contextual {
					SuiteTestEachWithContext(suite, []int{1, 2, 3}, nameOf, func(int, *TestContext) { rowBodies++ })
				} else {
					SuiteTestEach(suite, []int{1, 2, 3}, nameOf, func(int) { rowBodies++ })
				}
			})
			summary := suite.RunTo(&bytes.Buffer{})
			if !summary.Success() || summary.Passed != 2 || rowBodies != 0 || len(summary.Results) != 2 || summary.Results[0].Name != "existing" || summary.Results[1].Name != "row 2" {
				t.Fatalf("batch partially registered after naming mutation: summary=%#v, rowBodies=%d", summary, rowBodies)
			}
		})
	}
}

func TestParameterizedContextSelectionAndBailStillCleanFixtures(t *testing.T) {
	for _, test := range []struct {
		name, filter, bail, ran string
		passed, failed, skipped int
	}{
		{"continues", "", "", "[1 2 3]", 2, 1, 0},
		{"bails", "", "1", "[1 2]", 1, 1, 1},
		{"filters", "行 3", "", "[3]", 1, 0, 2},
		{"no match", "absent", "", "[]", 0, 0, 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("VASEST_FILTER", test.filter)
			t.Setenv("VASEST_BAIL", test.bail)
			suite := NewSuite("selected context table")
			var ran, cleaned []int
			var paths []string
			SuiteTestEachWithContext(suite, []int{1, 2, 3}, func(value int) string { return fmt.Sprintf("行 %d", value) },
				func(value int, context *TestContext) {
					ran = append(ran, value)
					paths = append(paths, context.TempDir())
					context.Cleanup(func() { cleaned = append(cleaned, value) })
					if value == 2 {
						context.Cleanup(func() { expectTrue(false) })
					}
				})
			summary := suite.RunTo(&bytes.Buffer{})
			if summary.Passed != test.passed || summary.Failed != test.failed || summary.Skipped != test.skipped || fmt.Sprint(ran) != test.ran || fmt.Sprint(cleaned) != test.ran {
				t.Fatalf("summary = %#v; ran = %v; cleaned = %v", summary, ran, cleaned)
			}
			for _, path := range paths {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatal("selected row directory leaked")
				}
			}
		})
	}
}

func TestSuiteWideHooksRunOnceAroundSelectedTests(t *testing.T) {
	suite := NewSuite("all hooks")
	var events []string
	suite.BeforeAll(func() { events = append(events, "beforeAll") })
	suite.BeforeEach(func() { events = append(events, "beforeEach") })
	suite.AfterEach(func() { events = append(events, "afterEach") })
	suite.AfterAll(func() { events = append(events, "afterAll") })
	suite.Test("one", func() { events = append(events, "one") })
	suite.Test("two", func() { events = append(events, "two") })
	summary := suite.RunTo(&bytes.Buffer{})
	if !summary.Success() || summary.Passed != 2 || strings.Join(events, ",") != "beforeAll,beforeEach,one,afterEach,beforeEach,two,afterEach,afterAll" {
		t.Fatalf("summary = %#v; order = %v", summary, events)
	}

	t.Setenv("VASEST_FILTER", "none")
	events = nil
	noMatch := suite.RunTo(&bytes.Buffer{})
	if !noMatch.Success() || noMatch.Skipped != 2 || len(events) != 0 {
		t.Fatalf("no-match summary = %#v; hooks ran = %v", noMatch, events)
	}
}

func TestSuiteWideHookFailureMatrix(t *testing.T) {
	for _, tc := range []struct {
		name          string
		beforeFailure bool
		afterFailure  bool
		wantPassed    int
		wantFailed    int
		wantSkipped   int
		wantEvents    string
	}{
		{"beforeAll fails", true, false, 0, 1, 1, "beforeAll,afterAll"},
		{"afterAll fails", false, true, 1, 1, 0, "beforeAll,body,afterAll"},
		{"both fail", true, true, 0, 2, 1, "beforeAll,afterAll"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			suite := NewSuite("failures")
			var events []string
			suite.BeforeAll(func() {
				events = append(events, "beforeAll")
				if tc.beforeFailure {
					expectTrue(false)
				}
			})
			suite.AfterAll(func() {
				events = append(events, "afterAll")
				if tc.afterFailure {
					expectFalse(true)
				}
			})
			suite.Test("body", func() { events = append(events, "body") })
			var output bytes.Buffer
			summary := suite.RunTo(&output)
			if summary.Success() || summary.Passed != tc.wantPassed || summary.Failed != tc.wantFailed || summary.Skipped != tc.wantSkipped || strings.Join(events, ",") != tc.wantEvents {
				t.Fatalf("summary = %#v; events = %v; output = %s", summary, events, output.String())
			}
			if tc.beforeFailure && (summary.Results[0].Name != "<beforeAll>" || !strings.Contains(output.String(), "FAIL failures / beforeAll")) {
				t.Fatalf("missing beforeAll failure: %#v; %s", summary, output.String())
			}
			if tc.afterFailure && (summary.Results[len(summary.Results)-1].Name != "<afterAll>" || !strings.Contains(output.String(), "FAIL failures / afterAll")) {
				t.Fatalf("missing afterAll failure: %#v; %s", summary, output.String())
			}
		})
	}
}

func TestEmptyMessageExceptionsStillFail(t *testing.T) {
	throwEmpty := func() { panic(__kinmokuseiThrown{err: errors.New("")}) }
	for _, tc := range []struct {
		name  string
		setup func(*Suite)
	}{
		{"beforeAll", func(s *Suite) { s.BeforeAll(throwEmpty) }},
		{"beforeEach", func(s *Suite) { s.BeforeEach(throwEmpty) }},
		{"body", func(s *Suite) {}},
		{"afterEach", func(s *Suite) { s.AfterEach(throwEmpty) }},
		{"afterAll", func(s *Suite) { s.AfterAll(throwEmpty) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			suite := NewSuite("empty exception")
			tc.setup(suite)
			suite.Test("case", func() {
				if tc.name == "body" {
					throwEmpty()
				}
			})
			summary := suite.RunTo(&bytes.Buffer{})
			if summary.Success() || summary.Failed != 1 || summary.ExitCode() != 1 {
				t.Fatalf("empty error was treated as success: %#v", summary)
			}
		})
	}
}

func TestFailureDetailsIdentifyPhaseWithoutParsingMessages(t *testing.T) {
	for _, phase := range []string{"beforeAll", "beforeEach", "body", "afterEach", "cleanup", "afterAll"} {
		for _, message := range []string{"", "cleanup: misleading; afterEach: 金木犀\nsecond line"} {
			t.Run(phase+"/"+message, func(t *testing.T) {
				suite := NewSuite("diagnostics")
				throw := func() { panic(__kinmokuseiThrown{err: errors.New(message)}) }
				switch phase {
				case "beforeAll":
					suite.BeforeAll(throw)
				case "beforeEach":
					suite.BeforeEach(throw)
				case "afterEach":
					suite.AfterEach(throw)
				case "afterAll":
					suite.AfterAll(throw)
				}
				bodyRan := false
				suite.TestWithContext("case", func(context *TestContext) {
					bodyRan = true
					if phase == "body" {
						throw()
					}
					if phase == "cleanup" {
						context.Cleanup(throw)
					}
				})
				var output bytes.Buffer
				summary := suite.RunTo(&output)
				if summary.Failed != 1 || summary.Success() {
					t.Fatalf("failure lost: %#v", summary)
				}
				var details []TestFailure
				for _, result := range summary.Results {
					if result.Status == "fail" {
						details = result.Failures
					} else if len(result.Failures) != 0 {
						t.Fatalf("non-failure has diagnostics: %#v", result)
					}
				}
				if len(details) != 1 || details[0].Phase != phase || details[0].Message != message {
					t.Fatalf("phase/message changed: %#v", details)
				}
				if (phase == "beforeAll" || phase == "beforeEach") && bodyRan {
					t.Fatal("body ran after failed setup")
				}
				if message == "" && !strings.Contains(output.String(), "["+phase+"] (empty error message)") {
					t.Fatalf("empty error not explained: %s", output.String())
				}
			})
		}
	}
}

func TestFailureDetailsKeepMultipleEmptyErrorsInExecutionOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.jsonl")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VASEST_REPORT_PATH", path)
	suite := NewSuite("ordered diagnostics")
	throw := func(message string) { panic(__kinmokuseiThrown{err: errors.New(message)}) }
	suite.TestWithContext("fails", func(context *TestContext) {
		context.Cleanup(func() { throw("first; cleanup: still one error") })
		context.Cleanup(func() { throw("") })
		throw("")
	})
	suite.AfterEach(func() { throw("") })
	suite.AfterAll(func() { throw("suite cleanup") })
	suite.Skip("disabled", func() { t.Fatal("skip ran") })
	suite.Todo("planned")
	var output bytes.Buffer
	summary := suite.RunTo(&output)
	want := []TestFailure{
		{Phase: "body", Message: ""},
		{Phase: "afterEach", Message: ""},
		{Phase: "cleanup", Message: ""},
		{Phase: "cleanup", Message: "first; cleanup: still one error"},
	}
	if summary.Failed != 2 || len(summary.Results[0].Failures) != len(want) {
		t.Fatalf("multiple errors lost: %#v", summary)
	}
	for index, failure := range want {
		if summary.Results[0].Failures[index] != failure {
			t.Fatalf("failure %d = %#v, want %#v", index, summary.Results[0].Failures[index], failure)
		}
	}
	for _, result := range summary.Results[1:3] {
		if len(result.Failures) != 0 {
			t.Fatalf("inactive case has errors: %#v", result)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var report struct{ Summary Summary }
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	for index, failure := range want {
		if report.Summary.Results[0].Failures[index] != failure {
			t.Fatalf("JSON error %d = %#v", index, report.Summary.Results[0].Failures[index])
		}
	}
	if !strings.Contains(output.String(), "[body] (empty error message)\n  [afterEach] (empty error message)\n  [cleanup] (empty error message)\n  [cleanup] first; cleanup: still one error") {
		t.Fatalf("text diagnostics missing or out of order: %s", output.String())
	}
	// A rerun must not retain the previous run's errors.
	suite.AfterEach(func() {})
	suite.AfterAll(func() {})
	suite.Test("passes", func() {})
	t.Setenv("VASEST_FILTER", "passes")
	second := suite.RunTo(&bytes.Buffer{})
	if !second.Success() || len(second.Results) != 1 || len(second.Results[0].Failures) != 0 {
		t.Fatalf("stale diagnostics on rerun: %#v", second)
	}
}

func TestWithTempDirCleanupMatrix(t *testing.T) {
	assertThrownMessage(t, "withTempDir body must not be nil", func() { withTempDir(nil) })
	var completedPath string
	withTempDir(func(path string) {
		completedPath = path
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			t.Fatalf("temporary directory missing in callback: %q, %v", path, err)
		}
		if err := os.WriteFile(filepath.Join(path, "marker"), []byte("ok"), 0600); err != nil {
			t.Fatal(err)
		}
	})
	if _, err := os.Stat(completedPath); !os.IsNotExist(err) {
		t.Fatalf("temporary directory remained after success: %q, %v", completedPath, err)
	}

	var thrownPath string
	assertThrownMessage(t, "expected true", func() {
		withTempDir(func(path string) {
			thrownPath = path
			expectTrue(false)
		})
	})
	if _, err := os.Stat(thrownPath); !os.IsNotExist(err) {
		t.Fatalf("temporary directory remained after throw: %q, %v", thrownPath, err)
	}

	var panickedPath string
	func() {
		defer func() {
			if recovered := recover(); recovered != "native panic" {
				t.Errorf("recovered = %v", recovered)
			}
		}()
		withTempDir(func(path string) {
			panickedPath = path
			panic("native panic")
		})
	}()
	if _, err := os.Stat(panickedPath); !os.IsNotExist(err) {
		t.Fatalf("temporary directory remained after panic: %q, %v", panickedPath, err)
	}
}

func TestWithTempDirCreationFailure(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	t.Setenv("TMPDIR", missing)
	assertThrownMessage(t, "missing", func() {
		withTempDir(func(string) { t.Fatal("callback ran after creation failure") })
	})
}

func TestWithEnvRestoresPreviousAndAbsentValues(t *testing.T) {
	const name = "VASEST_FIXTURE_TEST"
	t.Setenv(name, "original")
	withEnv(name, "outer", func() {
		if got := os.Getenv(name); got != "outer" {
			t.Fatalf("outer value = %q", got)
		}
		withEnv(name, "inner", func() {
			if got := os.Getenv(name); got != "inner" {
				t.Fatalf("inner value = %q", got)
			}
		})
		if got := os.Getenv(name); got != "outer" {
			t.Fatalf("value after nested fixture = %q", got)
		}
	})
	if got := os.Getenv(name); got != "original" {
		t.Fatalf("restored value = %q", got)
	}

	const absent = "VASEST_FIXTURE_ABSENT_TEST"
	t.Setenv(absent, "reserved")
	if err := os.Unsetenv(absent); err != nil {
		t.Fatal(err)
	}
	withEnv(absent, "temporary", func() {
		if got := os.Getenv(absent); got != "temporary" {
			t.Fatalf("temporary value = %q", got)
		}
	})
	if _, found := os.LookupEnv(absent); found {
		t.Fatal("previously absent variable remained set")
	}
}

func TestWithEnvRestoresAfterThrowAndPanic(t *testing.T) {
	const name = "VASEST_FIXTURE_FAILURE_TEST"
	t.Setenv(name, "original")
	assertThrownMessage(t, "fixture failed", func() {
		withEnv(name, "temporary", func() { panic(__kinmokuseiThrown{err: errors.New("fixture failed")}) })
	})
	if got := os.Getenv(name); got != "original" {
		t.Fatalf("value after throw = %q", got)
	}
	func() {
		defer func() {
			if recovered := recover(); recovered != "native panic" {
				t.Errorf("recovered = %v", recovered)
			}
		}()
		withEnv(name, "temporary", func() { panic("native panic") })
	}()
	if got := os.Getenv(name); got != "original" {
		t.Fatalf("value after panic = %q", got)
	}
}

func TestWithEnvRejectsInvalidInputs(t *testing.T) {
	assertThrownMessage(t, "withEnv body must not be nil", func() { withEnv("VASEST_FIXTURE_TEST", "value", nil) })
	assertThrownMessage(t, "cannot set environment variable", func() {
		withEnv("BAD=NAME", "value", func() { t.Fatal("invalid fixture body ran") })
	})
}

func TestNativePanicPropagatesAfterCleanup(t *testing.T) {
	suite := NewSuite("panic")
	var events []string
	suite.BeforeAll(func() { events = append(events, "beforeAll") })
	suite.BeforeEach(func() { events = append(events, "beforeEach") })
	suite.AfterEach(func() { events = append(events, "afterEach") })
	suite.AfterAll(func() { events = append(events, "afterAll") })
	suite.Test("native panic", func() {
		events = append(events, "body")
		panic("native failure")
	})
	defer func() {
		if recovered := recover(); recovered != "native failure" {
			t.Errorf("recovered = %v", recovered)
		}
		if got := strings.Join(events, ","); got != "beforeAll,beforeEach,body,afterEach,afterAll" {
			t.Errorf("cleanup order = %q", got)
		}
	}()
	suite.RunTo(&bytes.Buffer{})
}

func TestHooksRunInOrderAndAfterEachRunsOnFailure(t *testing.T) {
	suite := NewSuite("hooks")
	var events []string
	suite.BeforeEach(func() { events = append(events, "before") })
	suite.AfterEach(func() { events = append(events, "after") })
	suite.Test("pass", func() { events = append(events, "pass") })
	suite.Test("fail", func() {
		events = append(events, "fail")
		expectTrue(false)
	})
	summary := suite.RunTo(&bytes.Buffer{})
	if summary.Passed != 1 || summary.Failed != 1 || strings.Join(events, ",") != "before,pass,after,before,fail,after" {
		t.Fatalf("summary = %#v; order = %v", summary, events)
	}
}

func TestHookFailureMatrix(t *testing.T) {
	for _, tc := range []struct {
		name      string
		beforeErr bool
		bodyErr   bool
		afterErr  bool
		want      []string
	}{
		{"before fails", true, false, false, []string{"expected 2, got 1", "after"}},
		{"after fails", false, false, true, []string{"body", "afterEach: expected"}},
		{"body and after fail", false, true, true, []string{"expected false", "afterEach: expected"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			suite := NewSuite("hooks")
			var events []string
			suite.BeforeEach(func() {
				events = append(events, "before")
				if tc.beforeErr {
					expectEqual(1, 2)
				}
			})
			suite.AfterEach(func() {
				events = append(events, "after")
				if tc.afterErr {
					expectContains("foo", "bar")
				}
			})
			suite.Test("case", func() {
				events = append(events, "body")
				if tc.bodyErr {
					expectFalse(true)
				}
			})
			summary := suite.RunTo(&bytes.Buffer{})
			if summary.Failed != 1 || summary.Passed != 0 || summary.Success() {
				t.Fatalf("unexpected summary: %#v", summary)
			}
			for _, want := range tc.want {
				if !strings.Contains(summary.Results[0].Message, want) && !strings.Contains(strings.Join(events, ","), want) {
					t.Errorf("missing %q: result=%#v, events=%v", want, summary.Results[0], events)
				}
			}
			if tc.beforeErr && strings.Contains(strings.Join(events, ","), "body") {
				t.Errorf("body ran after beforeEach failure: %v", events)
			}
		})
	}
}

func TestFilterSelectionAndNoMatch(t *testing.T) {
	matchLog := filepath.Join(t.TempDir(), "matches")
	if err := os.WriteFile(matchLog, nil, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VASEST_FILTER", "金木犀")
	t.Setenv("VASEST_MATCH_LOG", matchLog)
	suite := NewSuite("filter")
	var ran []string
	suite.BeforeEach(func() { ran = append(ran, "before") })
	suite.AfterEach(func() { ran = append(ran, "after") })
	suite.Test("not selected", func() { ran = append(ran, "wrong") })
	suite.Test("金木犀 selected", func() { ran = append(ran, "selected") })
	var output bytes.Buffer
	summary := suite.RunTo(&output)
	if summary.Passed != 1 || summary.Skipped != 1 || summary.Failed != 0 || !summary.Success() || strings.Join(ran, ",") != "before,selected,after" {
		t.Fatalf("summary = %#v; order = %v; output = %s", summary, ran, output.String())
	}
	matchData, err := os.ReadFile(matchLog)
	if err != nil || string(matchData) != "1\n" {
		t.Fatalf("match log = %q, err = %v", matchData, err)
	}
	t.Setenv("VASEST_FILTER", "absent")
	output.Reset()
	noMatch := suite.RunTo(&output)
	if noMatch.Total() != 0 || noMatch.Skipped != 2 || !noMatch.Success() || !strings.Contains(output.String(), "no tests match") {
		t.Fatalf("no-match summary = %#v; output = %s", noMatch, output.String())
	}
}

func TestShuffleReordersCasesReproducibly(t *testing.T) {
	t.Setenv("VASEST_SHUFFLE_SEED", "42")
	suite := NewSuite("shuffle")
	for _, name := range []string{"one", "two", "three", "four", "five"} {
		name := name
		suite.Test(name, func() {})
	}
	first := suite.RunTo(&bytes.Buffer{})
	second := suite.RunTo(&bytes.Buffer{})
	if first.Passed != 5 || second.Passed != 5 {
		t.Fatalf("shuffled summaries = %#v, %#v", first, second)
	}
	var firstNames, secondNames []string
	seen := map[string]bool{}
	for _, result := range first.Results {
		firstNames = append(firstNames, result.Name)
		seen[result.Name] = true
	}
	for _, result := range second.Results {
		secondNames = append(secondNames, result.Name)
	}
	if strings.Join(firstNames, ",") == "one,two,three,four,five" || strings.Join(firstNames, ",") != strings.Join(secondNames, ",") || len(seen) != 5 {
		t.Fatalf("shuffle was not a stable permutation: %v, %v", firstNames, secondNames)
	}
	t.Setenv("VASEST_SHUFFLE_SEED", "invalid")
	assertThrownMessage(t, "invalid shuffle seed", func() { suite.RunTo(&bytes.Buffer{}) })
}

func TestSuiteWritesStructuredReports(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.jsonl")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VASEST_REPORT_PATH", path)
	for _, name := range []string{"first suite", "second suite", "failing suite"} {
		suite := NewSuite(name)
		if name == "failing suite" {
			suite.Test("fails", func() { expectTrue(false) })
		} else {
			suite.Test("passes", func() {})
		}
		suite.RunTo(&bytes.Buffer{})
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
	if len(lines) != 3 {
		t.Fatalf("report lines = %d: %s", len(lines), data)
	}
	for index, line := range lines {
		var report struct {
			Success bool
			Summary struct {
				Suite   string
				Passed  int
				Results []struct {
					Name   string
					Status string
				}
			}
		}
		if err := json.Unmarshal(line, &report); err != nil {
			t.Fatalf("invalid report %d: %v", index, err)
		}
		if index < 2 && (!report.Success || report.Summary.Suite != []string{"first suite", "second suite"}[index] || report.Summary.Passed != 1 || len(report.Summary.Results) != 1 || report.Summary.Results[0].Name != "passes" || report.Summary.Results[0].Status != "pass") {
			t.Fatalf("unexpected report %d: %#v", index, report)
		}
		if index == 2 && (report.Success || report.Summary.Suite != "failing suite" || len(report.Summary.Results) != 1 || report.Summary.Results[0].Status != "fail") {
			t.Fatalf("unexpected report %d: %#v", index, report)
		}
	}
}

func TestDurationsIncludeHooksAndBody(t *testing.T) {
	suite := NewSuite("timed")
	suite.BeforeEach(func() { time.Sleep(2 * time.Millisecond) })
	suite.AfterEach(func() { time.Sleep(2 * time.Millisecond) })
	suite.Test("body", func() { time.Sleep(3 * time.Millisecond) })
	suite.Skip("skipped", func() { t.Fatal("skipped body ran") })
	suite.Todo("planned")
	summary := suite.RunTo(&bytes.Buffer{})
	if summary.DurationNs <= 0 || summary.Results[0].DurationNs < int64(7*time.Millisecond) || summary.DurationNs < summary.Results[0].DurationNs {
		t.Fatalf("unexpected measured durations: %#v", summary)
	}
	if summary.Results[1].DurationNs != 0 || summary.Results[2].DurationNs != 0 {
		t.Fatalf("inactive cases have nonzero duration: %#v", summary.Results)
	}
}

func TestFluentMatchersEqualityIdentityAndNegation(t *testing.T) {
	expect([]int{1, 2}).ToEqual([]int{1, 2})
	expect(map[string]int{"value": 1}).ToEqual(map[string]int{"value": 1})
	expect([]int(nil)).Not.ToEqual([]int{})
	expect(true).ToBe(true)
	expect("金木犀").Not.ToBe("銀木犀")
	value := 42
	other := 42
	expect(&value).ToBe(&value)
	expect(&value).Not.ToBe(&other)
	expect(&value).ToEqual(&other)
	expect(math.NaN()).ToBe(math.NaN())
	expect(float32(math.NaN())).ToBe(float32(math.NaN()))
	expect(math.Inf(1)).ToBe(math.Inf(1))
	expect(0.0).Not.ToBe(math.Copysign(0, -1))
	expect[error](nil).ToBe(nil)
	matcher := expect(1)
	matcher.Not.ToBe(2)
	matcher.ToBe(1)
	matcher.Not.ToEqual(2)
	matcher.ToEqual(1)
	assertThrownMessage(t, "strings differ: line 2", func() { expect("first\nactual").ToEqual("first\nexpected") })
	assertThrownMessage(t, "did not expect", func() { expect([]int{1}).Not.ToEqual([]int{1}) })
	assertThrownMessage(t, "expected the same value", func() { expect(&value).ToBe(&other) })
	assertThrownMessage(t, "did not expect the same value", func() { expect(math.NaN()).Not.ToBe(math.NaN()) })
	for _, check := range []func(){
		func() { expect([]int{1}).ToBe([]int{1}) },
		func() { expect([]int(nil)).Not.ToBe(nil) },
		func() { expect(map[string]int{}).ToBe(map[string]int{}) },
		func() { expect[any](nil).ToBe([]int{1}) },
		func() { expect[any]([]int{1}).Not.ToBe(nil) },
		func() { expect(struct{ Value any }{[]int{1}}).ToBe(struct{ Value any }{[]int{1}}) },
	} {
		assertThrownMessage(t, "toBe requires comparable values", check)
	}
}

func TestFluentNilMatchersAcceptTypedNilWithoutTruthiness(t *testing.T) {
	expect[error](nil).ToBeNil()
	expect((*os.PathError)(nil)).ToBeNil()
	expect[error]((*os.PathError)(nil)).ToBeNil()
	expect([]int(nil)).ToBeNil()
	expect(map[string]int(nil)).ToBeNil()
	expect((func())(nil)).ToBeNil()
	expect((chan int)(nil)).ToBeNil()
	expect(0).Not.ToBeNil()
	expect(false).Not.ToBeNil()
	expect("").Not.ToBeNil()
	expect([]int{}).Not.ToBeNil()
	assertThrownMessage(t, "expected a non-nil value", func() { expect([]int(nil)).Not.ToBeNil() })
	assertThrownMessage(t, "expected nil", func() { expect(false).ToBeNil() })
}

func TestTypedStringMatchersFollowNativeTextRules(t *testing.T) {
	matcher := expectString("金木犀\nready")
	matcher.ToContain("木")
	matcher.Not.ToContain("missing")
	matcher.ToMatch("^金.*\\nready$")
	matcher.Not.ToMatch("^missing$")
	matcher.ToHaveLength(len("金木犀\nready"))
	matcher.Not.ToHaveLength(0)
	expectString("").ToContain("")
	expectString("").ToHaveLength(0)
	expectString("金木犀").ToHaveLength(9)
	checks := []struct {
		want string
		body func()
	}{
		{"to contain", func() { matcher.ToContain("missing") }},
		{"not to contain", func() { matcher.Not.ToContain("") }},
		{"to match", func() { matcher.ToMatch("^missing$") }},
		{"not to match", func() { matcher.Not.ToMatch("ready$") }},
		{"invalid regular expression", func() { matcher.ToMatch("[") }},
		{"invalid regular expression", func() { matcher.Not.ToMatch("[") }},
		{"expected length 1, got 15", func() { matcher.ToHaveLength(1) }},
		{"did not expect length 15", func() { matcher.Not.ToHaveLength(15) }},
		{"expected length must not be negative", func() { matcher.ToHaveLength(-1) }},
		{"expected length must not be negative", func() { matcher.Not.ToHaveLength(-1) }},
	}
	for _, check := range checks {
		assertThrownMessage(t, check.want, check.body)
	}
}

func TestTypedSliceMatchersPreserveElementsAndDeepEquality(t *testing.T) {
	type ID int
	matcher := expectSlice[ID]([]ID{1, 2})
	matcher.ToContain(ID(1))
	matcher.Not.ToContain(ID(3))
	matcher.ToHaveLength(2)
	matcher.Not.ToHaveLength(0)
	expectSlice[ID](nil).ToHaveLength(0)
	expectSlice[ID]([]ID{}).Not.ToContain(ID(1))
	expectSlice([][]int{nil, {1, 2}}).ToContain([]int{1, 2})
	expectSlice([][]int{nil}).ToContain(nil)
	expectSlice([][]int{nil}).Not.ToContain([]int{})
	expectSlice([]map[string]int{{"id": 42}}).ToContain(map[string]int{"id": 42})
	type record struct{ Values []int }
	expectSlice([]record{{[]int{42}}}).ToContain(record{[]int{42}})
	expectSlice[any]([]any{int(1)}).Not.ToContain(int64(1))
	expectSlice([]float64{math.NaN()}).Not.ToContain(math.NaN())
	checks := []struct {
		want string
		body func()
	}{
		{"to contain", func() { matcher.ToContain(ID(3)) }},
		{"not to contain", func() { matcher.Not.ToContain(ID(1)) }},
		{"to contain", func() { expectSlice[ID](nil).ToContain(ID(1)) }},
		{"expected length 1, got 2", func() { matcher.ToHaveLength(1) }},
		{"did not expect length 2", func() { matcher.Not.ToHaveLength(2) }},
		{"expected length must not be negative", func() { matcher.ToHaveLength(-1) }},
		{"expected length must not be negative", func() { matcher.Not.ToHaveLength(-1) }},
	}
	for _, check := range checks {
		assertThrownMessage(t, check.want, check.body)
	}
}

func TestOrderedMatchersPreserveTypesAndIntegerPrecision(t *testing.T) {
	type ID int64
	large := ID(1<<60 + 1)
	matcher := expectOrdered[ID](large)
	matcher.ToBeGreaterThan(large - 1)
	matcher.ToBeGreaterThanOrEqual(large)
	matcher.ToBeLessThan(large + 1)
	matcher.ToBeLessThanOrEqual(large)
	matcher.Not.ToBeGreaterThan(large)
	matcher.Not.ToBeGreaterThanOrEqual(large + 1)
	matcher.Not.ToBeLessThan(large)
	matcher.Not.ToBeLessThanOrEqual(large - 1)
	matcher.ToBeGreaterThan(large - 1)
	expectOrdered[int64](-1 << 63).ToBeLessThan(-1<<63 + 1)
	expectOrdered[uint64](1<<64 - 1).ToBeGreaterThan(1<<64 - 2)
	expectOrdered[uint64](0).ToBeLessThan(1)
	expectOrdered[int8](-128).ToBeLessThan(127)
	expectOrdered[uint8](255).ToBeGreaterThan(254)
	expectOrdered[int32](1<<31 - 1).ToBeGreaterThan(1<<31 - 2)
	expectOrdered[time.Duration](2 * time.Second).ToBeGreaterThan(time.Second)
	type label string
	expectOrdered[label]("金木犀").ToBeGreaterThan("木")
	expectOrdered[label]("a\x00b").ToBeLessThan("a\x00c")
	expectOrdered("").ToBeLessThanOrEqual("")
	expectOrdered("a").Not.ToBeLessThan("a")
}

func TestOrderedMatchersNativeFloatSemantics(t *testing.T) {
	adjacent := math.Nextafter32(1, 2)
	expectOrdered[float32](adjacent).ToBeGreaterThan(1)
	expectOrdered[float32](1).ToBeLessThan(adjacent)
	expectOrdered(math.Inf(1)).ToBeGreaterThan(math.MaxFloat64)
	expectOrdered(math.Inf(-1)).ToBeLessThan(-math.MaxFloat64)
	expectOrdered(math.Inf(1)).ToBeGreaterThanOrEqual(math.Inf(1))
	expectOrdered(math.Inf(-1)).ToBeLessThanOrEqual(math.Inf(-1))
	negativeZero := math.Copysign(0, -1)
	expectOrdered(negativeZero).ToBeGreaterThanOrEqual(0)
	expectOrdered(negativeZero).ToBeLessThanOrEqual(0)
	expectOrdered(negativeZero).Not.ToBeGreaterThan(0)
	expectOrdered(negativeZero).Not.ToBeLessThan(0)
	for _, pair := range [][2]float64{{math.NaN(), 1}, {1, math.NaN()}, {math.NaN(), math.NaN()}} {
		matcher := expectOrdered(pair[0])
		matcher.Not.ToBeGreaterThan(pair[1])
		matcher.Not.ToBeGreaterThanOrEqual(pair[1])
		matcher.Not.ToBeLessThan(pair[1])
		matcher.Not.ToBeLessThanOrEqual(pair[1])
		for _, check := range []func(float64){matcher.ToBeGreaterThan, matcher.ToBeGreaterThanOrEqual, matcher.ToBeLessThan, matcher.ToBeLessThanOrEqual} {
			assertThrownMessage(t, "expected", func() { check(pair[1]) })
		}
	}
}

func TestOrderedMatchersRejectMismatchesWithRelations(t *testing.T) {
	matcher := expectOrdered(42)
	checks := []struct {
		want string
		body func()
	}{
		{"42 to be greater than 42", func() { matcher.ToBeGreaterThan(42) }},
		{"42 to be greater than or equal to 43", func() { matcher.ToBeGreaterThanOrEqual(43) }},
		{"42 to be less than 42", func() { matcher.ToBeLessThan(42) }},
		{"42 to be less than or equal to 41", func() { matcher.ToBeLessThanOrEqual(41) }},
		{"42 not to be greater than 41", func() { matcher.Not.ToBeGreaterThan(41) }},
		{"42 not to be greater than or equal to 42", func() { matcher.Not.ToBeGreaterThanOrEqual(42) }},
		{"42 not to be less than 43", func() { matcher.Not.ToBeLessThan(43) }},
		{"42 not to be less than or equal to 42", func() { matcher.Not.ToBeLessThanOrEqual(42) }},
	}
	for _, check := range checks {
		assertThrownMessage(t, check.want, check.body)
	}
}

func TestPredicateMatchersPreserveNativeValuesAndInvokeOnce(t *testing.T) {
	type ID int64
	value := ID(1<<60 + 1)
	matcher := expect[ID](value)
	calls := 0
	positive := func(actual ID) bool { calls++; return actual == value }
	negative := func(actual ID) bool { calls++; return actual < 0 }
	if calls != 0 {
		t.Fatal("construction invoked predicate")
	}
	matcher.ToSatisfy(positive)
	matcher.Not.ToSatisfy(negative)
	matcher.ToSatisfy(positive)
	if calls != 3 {
		t.Fatal("predicate invocation count", calls)
	}
	for _, negated := range []bool{false, true} {
		before := calls
		assertThrownMessage(t, "satisfy predicate", func() {
			predicate := func(actual ID) bool { calls++; return negated }
			if negated {
				matcher.Not.ToSatisfy(predicate)
			} else {
				matcher.ToSatisfy(predicate)
			}
		})
		if calls != before+1 {
			t.Fatal("failing predicate was retried", calls)
		}
	}
	assertThrownMessage(t, "predicate must not be nil", func() { matcher.ToSatisfy(nil) })
	assertThrownMessage(t, "predicate must not be nil", func() { matcher.Not.ToSatisfy(nil) })
	values := []int{42}
	expect(values).ToSatisfy(func(actual []int) bool { actual[0]++; return true })
	if values[0] != 43 {
		t.Fatal("slice was copied", values)
	}
	expect[[]int](nil).ToSatisfy(func(actual []int) bool { return actual == nil })
	expect(map[string]int{"value": 42}).ToSatisfy(func(actual map[string]int) bool { return actual["value"] == 42 })
	type record struct{ Value int }
	object := &record{42}
	expect(object).ToSatisfy(func(actual *record) bool { return actual == object })
	expect[any](int32(42)).ToSatisfy(func(actual any) bool { _, ok := actual.(int32); return ok })
}

func TestPredicateMatchersPropagateThrowsAndPreserveLocations(t *testing.T) {
	sentinel := errors.New("predicate failed")
	for _, negated := range []bool{false, true} {
		func() {
			defer func() {
				thrown, ok := recover().(__kinmokuseiThrown)
				if !ok || thrown.err != sentinel {
					t.Fatal("predicate throw changed", thrown)
				}
			}()
			predicate := func(int) bool { panic(__kinmokuseiThrown{err: sentinel}) }
			if negated {
				expect(42).Not.ToSatisfy(predicate)
			} else {
				expect(42).ToSatisfy(predicate)
			}
		}()
		suite := NewSuite("predicate callback location")
		suite.Test("case", func() {
			predicate := func(int) bool {
				withTestLocation("predicate.km", 42, func() { panic(__kinmokuseiThrown{err: sentinel}) })
				return true
			}
			if negated {
				expect(42).Not.ToSatisfy(predicate)
			} else {
				expect(42).ToSatisfy(predicate)
			}
		})
		result := suite.RunTo(&bytes.Buffer{}).Results[0]
		if result.Status != "fail" || len(result.Failures) != 1 || result.Failures[0].Location != (TestLocation{File: "predicate.km", Line: 42, Source: "explicit"}) {
			t.Fatal(result)
		}
	}
}

func TestPredicateMatchersPreserveRuntimeSkipAndPanicCleanup(t *testing.T) {
	for _, negated := range []bool{false, true} {
		for _, nativePanic := range []bool{false, true} {
			func() {
				var events []string
				if nativePanic {
					defer func() {
						if recovered := recover(); recovered != "predicate panic" || fmt.Sprint(events) != "[afterEach cleanup afterAll]" {
							t.Fatalf("panic=%#v cleanup=%v", recovered, events)
						}
					}()
				}
				suite := NewSuite("predicate cleanup")
				suite.AfterEach(func() { events = append(events, "afterEach") })
				suite.AfterAll(func() { events = append(events, "afterAll") })
				suite.TestWithContext("case", func(context *TestContext) {
					context.Cleanup(func() { events = append(events, "cleanup") })
					predicate := func(int) bool {
						if nativePanic {
							panic("predicate panic")
						}
						skipTest("predicate resource unavailable")
						return true
					}
					if negated {
						expect(42).Not.ToSatisfy(predicate)
					} else {
						expect(42).ToSatisfy(predicate)
					}
				})
				summary := suite.RunTo(&bytes.Buffer{})
				if nativePanic {
					t.Fatal("native panic was swallowed")
				}
				if summary.Skipped != 1 || summary.Failed != 0 || !strings.Contains(summary.Results[0].Message, "predicate resource unavailable") || fmt.Sprint(events) != "[afterEach cleanup afterAll]" {
					t.Fatal(summary, events)
				}
			}()
		}
	}
}

func TestMapMatchersPreserveTypedKeysAndDistinguishMissingEntries(t *testing.T) {
	type ID int
	values := map[ID]int{ID(1): 0, ID(2): 42}
	matcher := expectMap[ID, int](values)
	matcher.ToHaveKey(ID(1))
	matcher.ToHaveEntry(ID(1), 0)
	matcher.ToHaveEntry(ID(2), 42)
	matcher.Not.ToHaveKey(ID(3))
	matcher.Not.ToHaveEntry(ID(3), 0)
	matcher.Not.ToHaveEntry(ID(2), 43)
	matcher.ToHaveLength(2)
	matcher.Not.ToHaveLength(0)
	expectMap[string, int](nil).ToHaveLength(0)
	expectMap[string, int](nil).Not.ToHaveEntry("missing", 0)
	expectMap(map[string]int{}).Not.ToHaveKey("missing")
	values[ID(3)] = 7
	matcher.ToHaveEntry(ID(3), 7)
	matcher.ToHaveLength(3)
	delete(values, ID(3))
	matcher.Not.ToHaveKey(ID(3))
	matcher.ToHaveLength(2)
	checks := []struct {
		want string
		body func()
	}{
		{"to have key", func() { matcher.ToHaveKey(ID(3)) }},
		{"not to have key", func() { matcher.Not.ToHaveKey(ID(1)) }},
		{"key is missing", func() { matcher.ToHaveEntry(ID(3), 0) }},
		{"got 0", func() { matcher.ToHaveEntry(ID(1), 42) }},
		{"not to have entry", func() { matcher.Not.ToHaveEntry(ID(1), 0) }},
		{"expected length 1, got 2", func() { matcher.ToHaveLength(1) }},
		{"did not expect length 2", func() { matcher.Not.ToHaveLength(2) }},
		{"length must not be negative", func() { matcher.ToHaveLength(-1) }},
		{"length must not be negative", func() { matcher.Not.ToHaveLength(-1) }},
		{"key is missing", func() { expectMap[string, int](nil).ToHaveEntry("missing", 0) }},
	}
	for _, check := range checks {
		assertThrownMessage(t, check.want, check.body)
	}
}

func TestMapMatchersUseNativeKeyEqualityAndDeepValueEquality(t *testing.T) {
	matcher := expectMap(map[string][]int{"金木犀": {1, 2}, "nil": nil, "empty": {}})
	matcher.ToHaveEntry("金木犀", []int{1, 2})
	matcher.ToHaveEntry("nil", nil)
	matcher.Not.ToHaveEntry("nil", []int{})
	matcher.Not.ToHaveEntry("empty", nil)
	matcher.Not.ToHaveEntry("missing", nil)
	expectMap(map[string]map[string]int{"nested": {"id": 42}}).ToHaveEntry("nested", map[string]int{"id": 42})
	type record struct{ Values []int }
	expectMap(map[string]record{"struct": {[]int{42}}}).ToHaveEntry("struct", record{[]int{42}})
	first, second := &record{[]int{42}}, &record{[]int{42}}
	expectMap(map[*record]int{first: 1}).ToHaveKey(first)
	expectMap(map[*record]int{first: 1}).Not.ToHaveKey(second)
	expectMap(map[string]*record{"class": first}).ToHaveEntry("class", second)
	expectMap(map[any]int{int32(1): 42}).Not.ToHaveKey(int64(1))
	expectMap(map[string]any{"value": int32(1)}).Not.ToHaveEntry("value", int64(1))
	nan := math.NaN()
	expectMap(map[float64]int{nan: 42}).Not.ToHaveKey(nan)
}

func TestMapMatcherNativeKeyPanicsStillUnwindCleanup(t *testing.T) {
	matcher := expectMap(map[any]int{})
	for _, attempt := range []func(){
		func() { matcher.ToHaveKey([]int{1}) },
		func() { matcher.Not.ToHaveKey([]int{1}) },
		func() { matcher.ToHaveEntry([]int{1}, 1) },
		func() { matcher.Not.ToHaveEntry([]int{1}, 1) },
	} {
		func() {
			var events []string
			defer func() {
				if recovered := recover(); !strings.Contains(fmt.Sprint(recovered), "hash of unhashable type []int") || fmt.Sprint(events) != "[afterEach cleanup afterAll]" {
					t.Fatalf("panic=%#v cleanup=%v", recovered, events)
				}
			}()
			suite := NewSuite("native map key panic")
			suite.AfterEach(func() { events = append(events, "afterEach") })
			suite.AfterAll(func() { events = append(events, "afterAll") })
			suite.TestWithContext("case", func(context *TestContext) {
				context.Cleanup(func() { events = append(events, "cleanup") })
				attempt()
			})
			suite.RunTo(&bytes.Buffer{})
		}()
	}
}

func TestAssertionSuccessAndFailureMatrix(t *testing.T) {
	for _, test := range []struct {
		name string
		body func()
	}{
		{"equal", func() { expectEqual(map[string]int{"a": 1}, map[string]int{"a": 1}) }},
		{"not equal", func() { expectNotEqual("a", "b") }},
		{"true", func() { expectTrue(true) }},
		{"false", func() { expectFalse(false) }},
		{"error", func() { expectError(errors.New("bad")) }},
		{"no error", func() { expectNoError(nil) }},
		{"contains unicode", func() { expectContains("金木犀", "木") }},
		{"not contains", func() { expectNotContains("金木犀", "missing") }},
		{"matches unicode", func() { expectMatches("金木犀", "木") }},
		{"not matches", func() { expectNotMatches("alpha", "^z") }},
	} {
		t.Run(test.name, func(t *testing.T) { test.body() })
	}

	for _, test := range []struct {
		name string
		want string
		body func()
	}{
		{"equal", "expected", func() { expectEqual(1, 2) }},
		{"not equal", "did not expect", func() { expectNotEqual("same", "same") }},
		{"true", "expected true", func() { expectTrue(false) }},
		{"false", "expected false", func() { expectFalse(true) }},
		{"error", "expected an error", func() { expectError(nil) }},
		{"no error", "expected no error", func() { expectNoError(errors.New("bad")) }},
		{"contains", "to contain", func() { expectContains("alpha", "z") }},
		{"not contains", "not to contain", func() { expectNotContains("alpha", "ph") }},
		{"not contains empty", "not to contain", func() { expectNotContains("alpha", "") }},
		{"matches", "to match", func() { expectMatches("alpha", "^z") }},
		{"not matches", "not to match", func() { expectNotMatches("alpha", "ph") }},
		{"invalid matches pattern", "invalid regular expression", func() { expectMatches("alpha", "[") }},
		{"invalid not matches pattern", "invalid regular expression", func() { expectNotMatches("alpha", "[") }},
	} {
		t.Run("failure/"+test.name, func(t *testing.T) { assertThrownMessage(t, test.want, test.body) })
	}
}

func TestFailAndApproxAssertions(t *testing.T) {
	expectApprox(1.125, 1.0, 0.125)
	expectApprox(-2.0, -2.0, 0)
	assertThrownMessage(t, "custom failure", func() { fail("custom failure") })
	assertThrownMessage(t, "test failed", func() { fail("") })
	assertThrownMessage(t, "within 0.1", func() { expectApprox(1.5, 1.0, 0.1) })
	assertThrownMessage(t, "tolerance must be finite", func() { expectApprox(1, 1, -1) })
	assertThrownMessage(t, "tolerance must be finite", func() { expectApprox(1, 1, math.NaN()) })
	assertThrownMessage(t, "tolerance must be finite", func() { expectApprox(1, 1, math.Inf(1)) })
	assertThrownMessage(t, "requires finite values", func() { expectApprox(math.NaN(), 1, 1) })
	assertThrownMessage(t, "requires finite values", func() { expectApprox(1, math.Inf(1), 1) })
}

func TestWaitForRetriesAssertionsAndReportsLastFailure(t *testing.T) {
	attempts := 0
	waitFor(func() {
		attempts++
		expectEqual(attempts, 3)
	}, time.Second, time.Millisecond)
	if attempts != 3 {
		t.Fatalf("waitFor attempts = %d, want 3", attempts)
	}
	waitFor(func() { attempts++ }, time.Second, time.Millisecond)
	if attempts != 4 {
		t.Fatalf("successful callback was retried: %d", attempts)
	}

	attempts = 0
	err := expectThrows(func() {
		waitFor(func() {
			attempts++
			fail("still pending")
		}, 5*time.Millisecond, time.Millisecond)
	})
	if attempts < 1 || !strings.Contains(err.Error(), "waitFor timed out after 5ms") || !strings.Contains(err.Error(), "still pending") {
		t.Fatalf("timeout attempts = %d, error = %q", attempts, err.Error())
	}
}

func TestWaitForValidatesOptionsBeforeRunning(t *testing.T) {
	assertThrownMessage(t, "waitFor body must not be nil", func() { waitFor(nil, time.Second, time.Millisecond) })
	attempts := 0
	body := func() { attempts++ }
	assertThrownMessage(t, "waitFor timeout must be positive", func() { waitFor(body, 0, time.Millisecond) })
	assertThrownMessage(t, "waitFor interval must be positive", func() { waitFor(body, time.Second, 0) })
	assertThrownMessage(t, "waitFor interval must be positive", func() { waitFor(body, time.Second, -time.Millisecond) })
	if attempts != 0 {
		t.Fatalf("invalid waitFor options ran callback %d times", attempts)
	}
}

func TestEqualStringFailureIdentifiesFirstDifferingLine(t *testing.T) {
	assertThrownMessage(t, `strings differ: line 2: expected "金木犀", got "銀木犀"`, func() {
		expectEqual("first\n銀木犀\nlast", "first\n金木犀\nlast")
	})
	assertThrownMessage(t, `line 2: expected "second", got end of text`, func() {
		expectEqual("first", "first\nsecond")
	})
	assertThrownMessage(t, `line 2: expected end of text, got "second"`, func() {
		expectEqual("first\nsecond", "first")
	})
	assertThrownMessage(t, `expected map[string]int`, func() {
		expectEqual(map[string]int{"a": 1}, map[string]int{"a": 2})
	})
}

func TestSnapshotCreationComparisonAndUpdate(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("VASEST_TEST_FILE", filepath.Join(directory, "example.test.km"))
	t.Setenv("VASEST_SNAPSHOT_SCOPE", "")
	t.Setenv("VASEST_UPDATE_SNAPSHOTS", "")
	assertThrownMessage(t, "is missing", func() { expectSnapshot("金木犀 / response", "first\nline") })
	snapshotDirectory := filepath.Join(directory, "__snapshots__", "example.test.km")
	if _, err := os.Stat(snapshotDirectory); !os.IsNotExist(err) {
		t.Fatalf("comparison created snapshot directory: %v", err)
	}
	t.Setenv("VASEST_UPDATE_SNAPSHOTS", "1")
	expectSnapshot("金木犀 / response", "first\nline")
	entries, err := os.ReadDir(snapshotDirectory)
	if err != nil || len(entries) != 1 || !strings.HasSuffix(entries[0].Name(), ".snap") {
		t.Fatalf("snapshot entries = %v, error = %v", entries, err)
	}
	path := filepath.Join(snapshotDirectory, entries[0].Name())
	contents, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(contents), "金木犀 / response") || !strings.HasSuffix(string(contents), "first\nline") {
		t.Fatalf("snapshot contents = %q, error = %v", contents, err)
	}
	t.Setenv("VASEST_UPDATE_SNAPSHOTS", "")
	expectSnapshot("金木犀 / response", "first\nline")
	assertThrownMessage(t, `snapshot "金木犀 / response" mismatch: line 2: expected "line", got "changed"`, func() { expectSnapshot("金木犀 / response", "first\nchanged") })
	contents, err = os.ReadFile(path)
	if err != nil || !strings.HasSuffix(string(contents), "first\nline") {
		t.Fatalf("mismatch modified snapshot: %q, error = %v", contents, err)
	}
	t.Setenv("VASEST_UPDATE_SNAPSHOTS", "1")
	expectSnapshot("金木犀 / response", "second")
	contents, err = os.ReadFile(path)
	if err != nil || !strings.HasSuffix(string(contents), "second") {
		t.Fatalf("updated snapshot = %q, error = %v", contents, err)
	}
	entries, err = os.ReadDir(snapshotDirectory)
	if err != nil || len(entries) != 1 {
		t.Fatalf("update left temporary files: %v, error = %v", entries, err)
	}
}

func TestSnapshotRejectsInvalidContext(t *testing.T) {
	assertThrownMessage(t, "snapshot name must not be empty", func() { expectSnapshot("  ", "value") })
	t.Setenv("VASEST_TEST_FILE", "")
	assertThrownMessage(t, "requires the Vasest runner", func() { expectSnapshot("name", "value") })
}

func TestJSONSnapshotUsesStableReadableEncoding(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("VASEST_TEST_FILE", filepath.Join(directory, "structured.test.km"))
	t.Setenv("VASEST_SNAPSHOT_SCOPE", "")
	t.Setenv("VASEST_UPDATE_SNAPSHOTS", "")
	value := map[string]any{"z": []int{2, 3}, "a": "金木犀"}
	assertThrownMessage(t, `snapshot "object" is missing`, func() { expectJSONSnapshot("object", value) })
	assertThrownMessage(t, "snapshot name must not be empty", func() { expectJSONSnapshot(" ", value) })
	assertThrownMessage(t, `cannot encode snapshot "unsupported"`, func() { expectJSONSnapshot("unsupported", func() {}) })

	t.Setenv("VASEST_UPDATE_SNAPSHOTS", "1")
	expectJSONSnapshot("object", value)
	snapshotDirectory := filepath.Join(directory, "__snapshots__", "structured.test.km")
	entries, err := os.ReadDir(snapshotDirectory)
	if err != nil || len(entries) != 1 {
		t.Fatalf("snapshot entries = %v, error = %v", entries, err)
	}
	path := filepath.Join(snapshotDirectory, entries[0].Name())
	data, err := os.ReadFile(path)
	want := "# \"object\"\n{\n  \"a\": \"金木犀\",\n  \"z\": [\n    2,\n    3\n  ]\n}\n"
	if err != nil || string(data) != want {
		t.Fatalf("JSON snapshot = %q, error = %v", data, err)
	}

	t.Setenv("VASEST_UPDATE_SNAPSHOTS", "")
	expectJSONSnapshot("object", map[string]any{"a": "金木犀", "z": []int{2, 3}})
	assertThrownMessage(t, `snapshot "object" mismatch: line 5: expected "    3", got "    4"`, func() {
		expectJSONSnapshot("object", map[string]any{"a": "金木犀", "z": []int{2, 4}})
	})
	data, err = os.ReadFile(path)
	if err != nil || string(data) != want {
		t.Fatalf("mismatch changed JSON snapshot: %q, error = %v", data, err)
	}
}

func TestSnapshotsUseSuiteAndCaseNamesAsScope(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("VASEST_TEST_FILE", filepath.Join(directory, "scoped.test.km"))
	t.Setenv("VASEST_UPDATE_SNAPSHOTS", "1")
	t.Setenv("VASEST_SNAPSHOT_SCOPE", "")
	expectSnapshot("shared", "legacy value")
	t.Setenv("VASEST_SNAPSHOT_SCOPE", "previous scope")
	firstValue := "first value"
	suite := NewSuite("same suite")
	suite.Test("first case", func() { expectSnapshot("shared", firstValue) })
	suite.Test("second case", func() { expectSnapshot("shared", "second value") })
	t.Setenv("VASEST_UPDATE_SNAPSHOTS", "")
	if summary := suite.RunTo(&bytes.Buffer{}); summary.Failed != 2 || !strings.Contains(summary.Results[0].Message, "is missing") {
		t.Fatalf("legacy snapshot was used for scoped cases: %#v", summary)
	}
	t.Setenv("VASEST_UPDATE_SNAPSHOTS", "1")
	if summary := suite.RunTo(&bytes.Buffer{}); !summary.Success() {
		t.Fatalf("initial scoped snapshot run failed: %#v", summary)
	}
	if got := os.Getenv("VASEST_SNAPSHOT_SCOPE"); got != "previous scope" {
		t.Fatalf("snapshot scope was not restored: %q", got)
	}
	other := NewSuite("other suite")
	other.Test("first case", func() { expectSnapshot("shared", "third value") })
	if summary := other.RunTo(&bytes.Buffer{}); !summary.Success() {
		t.Fatalf("other suite snapshot run failed: %#v", summary)
	}
	snapshotDirectory := filepath.Join(directory, "__snapshots__", "scoped.test.km")
	entries, err := os.ReadDir(snapshotDirectory)
	if err != nil || len(entries) != 4 {
		t.Fatalf("scoped snapshot entries = %v, error = %v", entries, err)
	}
	contents := map[string]string{}
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(snapshotDirectory, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		parts := strings.SplitN(string(data), "\n", 2)
		if len(parts) != 2 {
			t.Fatalf("invalid snapshot: %q", data)
		}
		contents[parts[0]] = parts[1]
	}
	for header, want := range map[string]string{
		`# "shared"`: "legacy value",
		`# "same suite" / "first case" / "shared"`:  "first value",
		`# "same suite" / "second case" / "shared"`: "second value",
		`# "other suite" / "first case" / "shared"`: "third value",
	} {
		if got := contents[header]; got != want {
			t.Errorf("snapshot %s = %q, want %q", header, got, want)
		}
	}
	t.Setenv("VASEST_UPDATE_SNAPSHOTS", "")
	if summary := suite.RunTo(&bytes.Buffer{}); !summary.Success() {
		t.Fatalf("scoped snapshot comparison failed: %#v", summary)
	}
	firstValue = "changed"
	summary := suite.RunTo(&bytes.Buffer{})
	if summary.Failed != 1 || summary.Passed != 1 || !strings.Contains(summary.Results[0].Message, `snapshot "shared" mismatch`) {
		t.Fatalf("one scoped snapshot should fail: %#v", summary)
	}
	if got := os.Getenv("VASEST_SNAPSHOT_SCOPE"); got != "previous scope" {
		t.Fatalf("snapshot scope was not restored after failure: %q", got)
	}
}

func TestExpectThrowsMatrix(t *testing.T) {
	thrown := expectThrows(func() { expectTrue(false) })
	if !strings.Contains(thrown.Error(), "expected true") {
		t.Fatalf("returned error = %q", thrown.Error())
	}
	expectThrowsMessage(func() { expectContains("金木犀", "missing") }, "金木犀")
	for _, tc := range []struct {
		name string
		want string
		body func()
	}{
		{"nil body", "expectThrows body must not be nil", func() { expectThrows(nil) }},
		{"no throw", "expected function to throw", func() { expectThrows(func() {}) }},
		{"empty fragment", "expected error message fragment must not be empty", func() { expectThrowsMessage(func() {}, "") }},
		{"wrong fragment", "expected error message", func() { expectThrowsMessage(func() { expectTrue(false) }, "missing") }},
	} {
		t.Run(tc.name, func(t *testing.T) { assertThrownMessage(t, tc.want, tc.body) })
	}
}

func TestRunSuiteAndExitHelper(t *testing.T) {
	assertThrownMessage(t, "suite registration body must not be nil", func() { runSuiteAndExit("sample", nil) })
	var ran bool
	runSuiteAndExit("sample", func(suite *Suite) {
		suite.Test("works", func() { ran = true })
	})
	if !ran {
		t.Fatal("suite registration callback was not executed")
	}
}

func assertThrownMessage(t *testing.T, want string, body func()) {
	t.Helper()
	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatalf("expected a thrown error containing %q", want)
		}
		thrown, ok := recovered.(__kinmokuseiException)
		if !ok || !strings.Contains(thrown.KinmokuseiExceptionError().Error(), want) {
			t.Fatalf("thrown value = %v, want %q", recovered, want)
		}
	}()
	body()
}

func TestAssertionLocationsIdentifyActualGoCaller(t *testing.T) {
	t.Setenv("VASEST_TEST_FILE", "")
	var want TestLocation
	record := func() {
		_, file, line, ok := runtime.Caller(1)
		if !ok {
			t.Fatal("test caller unavailable")
		}
		want = TestLocation{File: file, Line: line + 1, Source: "go"}
	}
	checks := []struct {
		name string
		body func()
	}{
		{"predicate mismatch", func() {
			record()
			expect(42).ToSatisfy(func(int) bool { return false })
		}},
		{"mock called", func() {
			mock := NewMockAction[int](func(int) {})
			record()
			expectMock[int](mock).ToHaveBeenCalled()
		}},
		{"mock called times", func() {
			mock := NewMockAction[int](func(int) {})
			record()
			expectMock[int](mock).ToHaveBeenCalledTimes(1)
		}},
		{"mock called with", func() {
			mock := NewMockAction[int](func(int) {})
			record()
			expectMock[int](mock).ToHaveBeenCalledWith(42)
		}},
		{"mock nth call", func() {
			mock := NewMockAction[int](func(int) {})
			record()
			expectMock[int](mock).ToHaveBeenNthCalledWith(1, 42)
		}},
		{"mock last call", func() {
			mock := NewMockAction[int](func(int) {})
			record()
			expectMock[int](mock).ToHaveBeenLastCalledWith(42)
		}},
		{"negated mock called", func() {
			mock := NewMockAction[int](func(int) {})
			mock.Invoke(42)
			record()
			expectMock[int](mock).Not.ToHaveBeenCalled()
		}},
		{"negated mock called times", func() {
			mock := NewMockAction[int](func(int) {})
			record()
			expectMock[int](mock).Not.ToHaveBeenCalledTimes(0)
		}},
		{"negated mock called with", func() {
			mock := NewMockAction[int](func(int) {})
			mock.Invoke(42)
			record()
			expectMock[int](mock).Not.ToHaveBeenCalledWith(42)
		}},
		{"negated mock nth call", func() {
			mock := NewMockAction[int](func(int) {})
			mock.Invoke(42)
			record()
			expectMock[int](mock).Not.ToHaveBeenNthCalledWith(1, 42)
		}},
		{"negated mock last call", func() {
			mock := NewMockAction[int](func(int) {})
			mock.Invoke(42)
			record()
			expectMock[int](mock).Not.ToHaveBeenLastCalledWith(42)
		}},
		{"mock invalid count", func() {
			mock := NewMockAction[int](func(int) {})
			record()
			expectMock[int](mock).ToHaveBeenCalledTimes(-1)
		}},
		{"negated mock invalid count", func() {
			mock := NewMockAction[int](func(int) {})
			record()
			expectMock[int](mock).Not.ToHaveBeenCalledTimes(-1)
		}},
		{"mock invalid index", func() {
			mock := NewMockAction[int](func(int) {})
			record()
			expectMock[int](mock).ToHaveBeenNthCalledWith(0, 42)
		}},
		{"negated mock invalid index", func() {
			mock := NewMockAction[int](func(int) {})
			record()
			expectMock[int](mock).Not.ToHaveBeenNthCalledWith(0, 42)
		}},
		{"nil mock", func() {
			record()
			expectMock[int](nil).ToHaveBeenCalled()
		}},
		{"negated typed nil mock", func() {
			var mock *MockFn[int, int]
			record()
			expectMock[int](mock).Not.ToHaveBeenCalled()
		}},
		{"ordered greater than", func() {
			record()
			expectOrdered(42).ToBeGreaterThan(42)
		}},
		{"ordered greater than or equal", func() {
			record()
			expectOrdered(42).ToBeGreaterThanOrEqual(43)
		}},
		{"ordered less than", func() {
			record()
			expectOrdered(42).ToBeLessThan(42)
		}},
		{"ordered less than or equal", func() {
			record()
			expectOrdered(42).ToBeLessThanOrEqual(41)
		}},
		{"negated ordered greater than", func() {
			record()
			expectOrdered(42).Not.ToBeGreaterThan(41)
		}},
		{"negated ordered greater than or equal", func() {
			record()
			expectOrdered(42).Not.ToBeGreaterThanOrEqual(42)
		}},
		{"negated ordered less than", func() {
			record()
			expectOrdered(42).Not.ToBeLessThan(43)
		}},
		{"negated ordered less than or equal", func() {
			record()
			expectOrdered(42).Not.ToBeLessThanOrEqual(42)
		}},
		{"negated predicate mismatch", func() {
			record()
			expect(42).Not.ToSatisfy(func(int) bool { return true })
		}},
		{"nil predicate", func() {
			record()
			expect(42).ToSatisfy(nil)
		}},
		{"negated nil predicate", func() {
			record()
			expect(42).Not.ToSatisfy(nil)
		}},
		{"fluent equal", func() {
			record()
			expect(1).ToEqual(2)
		}},
		{"fluent negated equal", func() {
			record()
			expect(1).Not.ToEqual(1)
		}},
		{"fluent identity", func() {
			record()
			expect(1).ToBe(2)
		}},
		{"fluent negated identity", func() {
			record()
			expect(1).Not.ToBe(1)
		}},
		{"saved fluent matcher", func() {
			matcher := expect("actual")
			record()
			matcher.ToEqual("expected")
		}},
		{"fluent invalid identity", func() {
			record()
			expect([]int{1}).Not.ToBe([]int{2})
		}},
		{"fluent nil", func() {
			record()
			expect(1).ToBeNil()
		}},
		{"fluent negated nil", func() {
			record()
			expect[error](nil).Not.ToBeNil()
		}},
		{"string containment", func() {
			record()
			expectString("actual").ToContain("missing")
		}},
		{"negated string containment", func() {
			record()
			expectString("actual").Not.ToContain("actual")
		}},
		{"string pattern", func() {
			record()
			expectString("actual").ToMatch("^missing$")
		}},
		{"invalid negated string pattern", func() {
			record()
			expectString("actual").Not.ToMatch("[")
		}},
		{"string length", func() {
			record()
			expectString("actual").ToHaveLength(1)
		}},
		{"negated string length", func() {
			record()
			expectString("actual").Not.ToHaveLength(6)
		}},
		{"slice containment", func() {
			record()
			expectSlice([]int{1}).ToContain(2)
		}},
		{"negated slice containment", func() {
			record()
			expectSlice([]int{1}).Not.ToContain(1)
		}},
		{"slice length", func() {
			record()
			expectSlice([]int{1}).ToHaveLength(2)
		}},
		{"invalid negated slice length", func() {
			record()
			expectSlice([]int{1}).Not.ToHaveLength(-1)
		}},
		{"equal", func() {
			record()
			expectEqual(1, 2)
		}},
		{"fail", func() {
			record()
			fail("deliberate failure")
		}},
		{"snapshot", func() {
			record()
			expectSnapshot("missing", "value")
		}},
		{"JSON snapshot wrapper", func() {
			record()
			expectJSONSnapshot("missing", 42)
		}},
		{"invalid skip reason", func() {
			record()
			skipTest(" ")
		}},
		{"error cause", func() {
			record()
			expectErrorValue(errors.New("actual")).ToMatchCause(errors.New("expected"))
		}},
		{"negated error cause", func() {
			record()
			expectErrorValue(nil).Not.ToMatchCause(nil)
		}},
		{"error nil matcher", func() {
			record()
			expectErrorValue(errors.New("actual")).ToBeNil()
		}},
		{"negated error nil matcher", func() {
			record()
			expectErrorValue(nil).Not.ToBeNil()
		}},
		{"error type matcher", func() {
			var found *os.PathError
			record()
			ErrorExpectationToMatchType(expectErrorValue(errors.New("actual")), &found)
		}},
		{"negated error type matcher", func() {
			record()
			NegatedErrorExpectationToMatchType[error](expectErrorValue(errors.New("actual")).Not)
		}},
		{"invalid negated error type", func() {
			record()
			NegatedErrorExpectationToMatchType[int](expectErrorValue(nil).Not)
		}},
		{"map key", func() {
			record()
			expectMap(map[string]int{}).ToHaveKey("missing")
		}},
		{"negated map key", func() {
			record()
			expectMap(map[string]int{"key": 0}).Not.ToHaveKey("key")
		}},
		{"map entry", func() {
			record()
			expectMap(map[string]int{}).ToHaveEntry("missing", 0)
		}},
		{"negated map entry", func() {
			record()
			expectMap(map[string]int{"key": 0}).Not.ToHaveEntry("key", 0)
		}},
		{"map length", func() {
			record()
			expectMap(map[string]int{}).ToHaveLength(1)
		}},
		{"invalid negated map length", func() {
			record()
			expectMap(map[string]int{}).Not.ToHaveLength(-1)
		}},
		{"function throw", func() {
			record()
			expectFunction(func() {}).ToThrow()
		}},
		{"negated function throw", func() {
			matcher := expectFunction(func() { panic(__kinmokuseiThrown{err: errors.New("actual")}) })
			record()
			matcher.Not.ToThrow()
		}},
		{"function message", func() {
			record()
			expectFunction(func() { panic(__kinmokuseiThrown{err: errors.New("actual")}) }).ToThrowMessage("missing")
		}},
		{"negated function message", func() {
			record()
			expectFunction(func() { panic(__kinmokuseiThrown{err: errors.New("actual")}) }).Not.ToThrowMessage("actual")
		}},
		{"function cause", func() {
			record()
			expectFunction(func() { panic(__kinmokuseiThrown{err: errors.New("actual")}) }).ToThrowCause(os.ErrNotExist)
		}},
		{"negated function cause", func() {
			record()
			expectFunction(func() { panic(__kinmokuseiThrown{err: nil}) }).Not.ToThrowCause(nil)
		}},
		{"function type", func() {
			var found *os.PathError
			record()
			FunctionExpectationToThrowType(expectFunction(func() {}), &found)
		}},
		{"negated function type", func() {
			record()
			NegatedFunctionExpectationToThrowType[error](expectFunction(func() { panic(__kinmokuseiThrown{err: errors.New("actual")}) }).Not)
		}},
		{"invalid function type", func() {
			var found int
			record()
			FunctionExpectationToThrowType(expectFunction(func() {}), &found)
		}},
		{"invalid negated function type", func() {
			record()
			NegatedFunctionExpectationToThrowType[int](expectFunction(func() {}).Not)
		}},
		{"legacy error cause", func() {
			record()
			expectErrorIs(errors.New("actual"), errors.New("expected"))
		}},
		{"error type", func() {
			var target *os.PathError
			record()
			expectErrorAs(errors.New("actual"), &target)
		}},
		{"invalid error target", func() {
			var target int
			record()
			expectErrorAs(errors.New("actual"), &target)
		}},
		{"nil error target", func() {
			var target **os.PathError
			record()
			expectErrorAs(errors.New("actual"), target)
		}},
		{"throws wrapper", func() {
			record()
			expectThrowsMessage(func() {}, "missing")
		}},
		{"mock lookup", func() {
			mock := NewMockFn[int, int](func(value int) int { return value })
			record()
			mock.ExpectNthReturnedWith(0, 1)
		}},
		{"mock thrown lookup", func() {
			mock := NewMockFn[int, int](func(value int) int { return value })
			record()
			mock.ExpectNthThrownMessage(2, "failure")
		}},
		{"action wrapper", func() {
			mock := NewMockAction[int](func(int) {})
			record()
			mock.ExpectCalledTimes(1)
		}},
		{"retry assertion", func() {
			waitFor(func() {
				record()
				expectTrue(false)
			}, time.Millisecond, time.Millisecond)
		}},
		{"retry unlocated error", func() {
			record()
			waitFor(func() { panic(__kinmokuseiThrown{err: errors.New("retry failure")}) }, time.Millisecond, time.Millisecond)
		}},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			suite := NewSuite("locations")
			suite.Test("fails", check.body)
			var output bytes.Buffer
			summary := suite.RunTo(&output)
			if summary.Failed != 1 || len(summary.Results[0].Failures) != 1 {
				t.Fatalf("failure lost: %#v", summary)
			}
			location := summary.Results[0].Failures[0].Location
			if location != want {
				t.Fatalf("location=%#v, want %#v", location, want)
			}
			if !strings.Contains(output.String(), fmt.Sprintf("at %s:%d (go)", want.File, want.Line)) {
				t.Fatalf("location not rendered: %s", output.String())
			}
		})
	}
}

func TestExplicitLocationsPreserveErrorChainsAndAllLifecyclePhases(t *testing.T) {
	for _, phase := range []string{"beforeAll", "beforeEach", "fixture", "body", "afterEach", "cleanup", "afterAll"} {
		t.Run(phase, func(t *testing.T) {
			sentinel := errors.New("")
			throw := func() {
				withTestLocation("cases/<&>.test.km", 27, func() {
					panic(__kinmokuseiThrown{err: sentinel})
				})
			}
			// Metadata wrappers remain compatible with errors.Is and errors.As,
			// including wrappers and joined errors created by application code.
			err := expectThrows(throw)
			if !errors.Is(err, sentinel) || err.Error() != "" {
				t.Fatalf("error chain changed: %v", err)
			}
			want := TestLocation{File: "cases/<&>.test.km", Line: 27, Source: "explicit"}
			if got := failureLocation(errors.Join(errors.New("other"), fmt.Errorf("wrapped: %w", err))); got != want {
				t.Fatalf("wrapped origin=%#v", got)
			}
			suite := NewSuite("explicit lifecycle")
			switch phase {
			case "beforeAll":
				suite.BeforeAll(throw)
			case "beforeEach":
				suite.BeforeEach(throw)
			case "afterEach":
				suite.AfterEach(throw)
			case "afterAll":
				suite.AfterAll(throw)
			}
			if phase == "fixture" {
				SuiteTestWithFixture(suite, "case", func(*TestContext) int { throw(); return 0 }, func(int, *TestContext) { t.Fatal("fixture failure ran body") })
			} else {
				suite.TestWithContext("case", func(context *TestContext) {
					if phase == "body" {
						throw()
					}
					if phase == "cleanup" {
						context.Cleanup(throw)
					}
				})
			}
			var output bytes.Buffer
			summary := suite.RunTo(&output)
			found := false
			for _, result := range summary.Results {
				for _, failure := range result.Failures {
					found = true
					if failure.Phase != phase || failure.Message != "" || failure.Location != want {
						t.Fatalf("metadata changed: %#v", failure)
					}
				}
			}
			if !found || summary.Failed != 1 {
				t.Fatalf("missing failure: %#v", summary)
			}
			if !strings.Contains(output.String(), "["+phase+"] (empty error message)") || !strings.Contains(output.String(), "at cases/<&>.test.km:27 (explicit)") {
				t.Fatalf("missing text location: %s", output.String())
			}
		})
	}
}

func TestExplicitLocationNestingValidationAndNativePanic(t *testing.T) {
	err := expectThrows(func() {
		withTestLocation("outer.test.km", 7, func() {
			withTestLocation("inner.test.km", 11, func() { expectFalse(true) })
		})
	})
	if got := failureLocation(err); got != (TestLocation{File: "outer.test.km", Line: 7, Source: "explicit"}) {
		t.Fatalf("outer annotation lost: %#v", got)
	}
	for _, file := range []string{"", " \t", "line\nbreak", "line\rbreak", "NUL\x00byte"} {
		assertThrownMessage(t, "test location file must be non-empty and contain no line breaks or NUL", func() { withTestLocation(file, 1, func() { t.Fatal("invalid file ran body") }) })
	}
	for _, line := range []int{0, -1} {
		assertThrownMessage(t, "test location line must be positive", func() { withTestLocation("case.test.km", line, func() { t.Fatal("invalid line ran body") }) })
	}
	assertThrownMessage(t, "withTestLocation body must not be nil", func() { withTestLocation("case.test.km", 1, nil) })
	passed := false
	withTestLocation("case.test.km", 1, func() { passed = true })
	if !passed {
		t.Fatal("annotation did not run body")
	}
	func() {
		defer func() {
			if value := recover(); value != "native panic" {
				t.Fatalf("native panic converted: %#v", value)
			}
		}()
		withTestLocation("case.test.km", 1, func() { panic("native panic") })
	}()
	if got := failureLocation(errors.New("ordinary error")); got != (TestLocation{}) {
		t.Fatalf("invented origin: %#v", got)
	}
}

func TestSourceLineDirectivesAreReportedAsKinmokusei(t *testing.T) {
	err := expectThrows(mappedAssertionFailure)
	location := failureLocation(err)
	if location.Source != "kinmokusei" || location.Line != 41 || !strings.HasSuffix(location.File, "mapped/source.test.km") {
		t.Fatalf("mapped location=%#v", location)
	}
}

func TestRerunSelectionUsesExactSuiteAndCaseAndSkipsUnselectedHooks(t *testing.T) {
	log := filepath.Join(t.TempDir(), "matches")
	if err := os.WriteFile(log, nil, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VASEST_MATCH_LOG", log)
	t.Setenv("VASEST_RERUN_SELECTION", `{"all":false,"suites":[{"name":"選択","wholeSuite":false,"cases":["case"]}]}`)
	var bodies, hooks int
	selected := NewSuite("選択")
	selected.BeforeAll(func() { hooks++ })
	selected.AfterAll(func() { hooks++ })
	selected.Test("case", func() { bodies++ })
	selected.Test("case extra", func() { t.Fatal("substring match") })
	other := NewSuite("other")
	other.BeforeAll(func() { t.Fatal("unselected setup") })
	other.AfterAll(func() { t.Fatal("unselected cleanup") })
	other.Test("case", func() { t.Fatal("other suite body") })
	summary := selected.RunTo(&bytes.Buffer{})
	ignored := other.RunTo(&bytes.Buffer{})
	data, err := os.ReadFile(log)
	if bodies != 1 || hooks != 2 || summary.Passed != 1 || summary.Skipped != 1 || ignored.Skipped != 1 || err != nil || string(data) != "1\n" {
		t.Fatalf("selection bodies=%d hooks=%d summaries=%#v %#v log=%q err=%v", bodies, hooks, summary, ignored, data, err)
	}
	t.Setenv("VASEST_FILTER", "absent")
	if result := selected.RunTo(&bytes.Buffer{}); result.Passed != 0 || result.Skipped != 2 || bodies != 1 {
		t.Fatalf("filter intersection: %#v", result)
	}
}

func TestRerunWholeSuiteAndFilePreserveTodoSkipAndBail(t *testing.T) {
	for _, selection := range []string{`{"all":true,"suites":[]}`, `{"all":false,"suites":[{"name":"probe","wholeSuite":true,"cases":[]}]}`} {
		t.Setenv("VASEST_RERUN_SELECTION", selection)
		t.Setenv("VASEST_BAIL", "1")
		suite := NewSuite("probe")
		suite.Skip("paused", func() { t.Fatal("skip ran") })
		suite.Todo("future")
		suite.Test("failure", func() { expectTrue(false) })
		suite.Test("later", func() { t.Fatal("bail failed") })
		result := suite.RunTo(&bytes.Buffer{})
		if result.Failed != 1 || result.Todo != 1 || result.Skipped != 2 {
			t.Fatalf("whole selection: %#v", result)
		}
	}
	t.Setenv("VASEST_RERUN_SELECTION", "invalid")
	if err := expectThrows(func() { NewSuite("bad selection").RunTo(&bytes.Buffer{}) }); !strings.Contains(err.Error(), "invalid rerun selection") {
		t.Fatal(err)
	}
}

func TestFailedSuiteExitMarkerWritesOnlyToRunnerProvidedPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "exit")
	t.Setenv("VASEST_SUITE_EXIT_PATH", "")
	markFailedSuiteExit()
	t.Setenv("VASEST_SUITE_EXIT_PATH", path)
	markFailedSuiteExit()
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "1\n" {
		t.Fatalf("exit marker: %q %v", data, err)
	}
	t.Setenv("VASEST_SUITE_EXIT_PATH", filepath.Join(path, "missing"))
	if err := expectThrows(markFailedSuiteExit); !strings.Contains(err.Error(), "cannot record suite exit") {
		t.Fatal(err)
	}
}

// Error assertions must retain standard Go error-chain semantics, including
// application-defined matching and extraction, rather than compare messages.
type assertionCodeError struct{ Code int }

func (err *assertionCodeError) Error() string  { return fmt.Sprintf("code %d", err.Code) }
func (err *assertionCodeError) String() string { return err.Error() }

type assertionMappedError struct{ code *assertionCodeError }

func (err assertionMappedError) Error() string { return "mapped error" }
func (err assertionMappedError) Is(target error) bool {
	code, ok := target.(*assertionCodeError)
	return ok && code != nil && code.Code == err.code.Code
}
func (err assertionMappedError) As(target any) bool {
	if code, ok := target.(**assertionCodeError); ok {
		*code = err.code
		return true
	}
	return false
}

// errors.As gives custom matchers the actual destination, including when
// they mutate it and subsequently decline the match.
type assertionMutatingAsError struct{}

func (assertionMutatingAsError) Error() string { return "mutating matcher" }
func (assertionMutatingAsError) As(target any) bool {
	if code, ok := target.(**assertionCodeError); ok {
		*code = &assertionCodeError{Code: 37}
	}
	return false
}

type assertionSliceError []int

func (err assertionSliceError) Error() string { return "slice error" }

func TestExpectErrorIsUsesIdentityAndWrappedJoinedChains(t *testing.T) {
	sentinel := errors.New("same message")
	expectErrorIs(sentinel, sentinel)
	expectErrorIs(fmt.Errorf("wrapped twice: %w", fmt.Errorf("wrapped: %w", sentinel)), sentinel)
	expectErrorIs(errors.Join(errors.New("unrelated"), fmt.Errorf("wrapped: %w", sentinel)), sentinel)
	expectErrorIs(nil, nil)
	for _, tc := range []struct {
		name        string
		err, target error
	}{
		{"equal messages differ in identity", errors.New("same message"), sentinel},
		{"nil error", nil, sentinel}, {"nil target", sentinel, nil},
		{"non-comparable error", assertionSliceError{1}, assertionSliceError{1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertThrownMessage(t, "(errors.Is)", func() { expectErrorIs(tc.err, tc.target) })
		})
	}
	code := &assertionCodeError{Code: 42}
	mapped := fmt.Errorf("outer: %w", assertionMappedError{code: code})
	expectErrorIs(mapped, &assertionCodeError{Code: 42})
	assertThrownMessage(t, "(errors.Is)", func() { expectErrorIs(mapped, &assertionCodeError{Code: 43}) })
	// Explicit failure-location wrappers retain the original cause.
	located := expectThrows(func() {
		withTestLocation("errors.test.km", 10, func() { panic(__kinmokuseiThrown{err: sentinel}) })
	})
	expectErrorIs(located, sentinel)
}

func TestFunctionMatchersExecuteOncePerAssertionAndPreserveErrors(t *testing.T) {
	cause := &assertionCodeError{Code: 42}
	wrapped := errors.Join(errors.New("other"), fmt.Errorf("load: %w", cause))
	calls := 0
	matcher := expectFunction(func() { calls++; panic(__kinmokuseiThrown{err: wrapped}) })
	if calls != 0 {
		t.Fatal("constructing a matcher invoked the function")
	}
	if caught := matcher.ToThrow(); caught != wrapped || calls != 1 {
		t.Fatal(caught, calls)
	}
	matcher.ToThrowMessage("code 42")
	matcher.ToThrowCause(cause)
	var found *assertionCodeError
	FunctionExpectationToThrowType[*assertionCodeError](matcher, &found)
	if found != cause || calls != 4 {
		t.Fatal(found, calls)
	}
	matcher.Not.ToThrowMessage("missing")
	matcher.Not.ToThrowCause(errors.New("code 42"))
	NegatedFunctionExpectationToThrowType[*os.PathError](matcher.Not)
	if calls != 7 {
		t.Fatal("matcher cached or repeated a call", calls)
	}
	mapped := expectFunction(func() { panic(__kinmokuseiThrown{err: assertionMappedError{code: cause}}) })
	mapped.ToThrowCause(&assertionCodeError{Code: 42})
	FunctionExpectationToThrowType(mapped, &found)
	if found != cause {
		t.Fatal("custom As result lost")
	}
	assertThrownMessage(t, "not to throw a value", func() { NegatedFunctionExpectationToThrowType[*assertionCodeError](mapped.Not) })
	assertThrownMessage(t, "(errors.As)", func() {
		FunctionExpectationToThrowType(expectFunction(func() { panic(__kinmokuseiThrown{err: assertionMutatingAsError{}}) }), &found)
	})
	if found == nil || found.Code != 37 {
		t.Fatal("custom As destination mutation lost")
	}
	var typedNil *os.PathError
	FunctionExpectationToThrowType(expectFunction(func() { panic(__kinmokuseiThrown{err: typedNil}) }), &typedNil)
	if typedNil != nil {
		t.Fatal("typed nil extraction lost")
	}
	noThrow := expectFunction(func() { calls++ })
	noThrow.Not.ToThrow()
	noThrow.Not.ToThrowMessage("missing")
	noThrow.Not.ToThrowCause(cause)
	NegatedFunctionExpectationToThrowType[*os.PathError](noThrow.Not)
	if calls != 11 {
		t.Fatal(calls)
	}
	// A thrown control value is separate from its possibly nil error payload.
	nilThrow := expectFunction(func() { panic(__kinmokuseiThrown{err: nil}) })
	if nilThrow.ToThrow() != nil {
		t.Fatal("nil payload changed")
	}
	nilThrow.ToThrowCause(nil)
	nilThrow.Not.ToThrowMessage("missing")
	assertThrownMessage(t, "not to throw", nilThrow.Not.ToThrow)
	assertThrownMessage(t, "to contain", func() { nilThrow.ToThrowMessage("missing") })
}

func TestFunctionMatchersValidateBeforeCallingAndReportMismatches(t *testing.T) {
	calls := 0
	noThrow := expectFunction(func() { calls++ })
	var target *os.PathError
	var nilTarget **os.PathError
	var invalid int
	checks := []struct {
		want string
		body func()
	}{
		{"fragment must not be empty", func() { noThrow.ToThrowMessage("") }},
		{"fragment must not be empty", func() { noThrow.Not.ToThrowMessage("") }},
		{"target must not be nil", func() { FunctionExpectationToThrowType(noThrow, nilTarget) }},
		{"got int", func() { FunctionExpectationToThrowType(noThrow, &invalid) }},
		{"got int", func() { NegatedFunctionExpectationToThrowType[int](noThrow.Not) }},
		{"got fs.PathError", func() { NegatedFunctionExpectationToThrowType[os.PathError](noThrow.Not) }},
	}
	for _, check := range checks {
		assertThrownMessage(t, check.want, check.body)
	}
	if calls != 0 {
		t.Fatal("invalid assertion invoked function", calls)
	}
	for _, attempt := range []func(){
		func() { noThrow.ToThrow() },
		func() { noThrow.ToThrowMessage("missing") },
		func() { noThrow.ToThrowCause(os.ErrNotExist) },
		func() { FunctionExpectationToThrowType(noThrow, &target) },
	} {
		assertThrownMessage(t, "expected function to throw an error", attempt)
	}
	if calls != 4 {
		t.Fatal(calls)
	}
	cause := errors.New("actual")
	throwing := expectFunction(func() { calls++; panic(__kinmokuseiThrown{err: cause}) })
	assertThrownMessage(t, "not to throw, got actual", throwing.Not.ToThrow)
	assertThrownMessage(t, "to contain", func() { throwing.ToThrowMessage("missing") })
	assertThrownMessage(t, "not to throw containing", func() { throwing.Not.ToThrowMessage("actual") })
	assertThrownMessage(t, "(errors.Is)", func() { throwing.ToThrowCause(errors.New("actual")) })
	assertThrownMessage(t, "not to throw matching", func() { throwing.Not.ToThrowCause(cause) })
	assertThrownMessage(t, "(errors.As)", func() { FunctionExpectationToThrowType(throwing, &target) })
	assertThrownMessage(t, "not to throw a value", func() { NegatedFunctionExpectationToThrowType[error](throwing.Not) })
	if calls != 11 {
		t.Fatal(calls)
	}
	missing := expectFunction(nil)
	for _, attempt := range []func(){
		func() { missing.ToThrow() }, missing.Not.ToThrow,
		func() { missing.ToThrowMessage("message") }, func() { missing.Not.ToThrowMessage("message") },
		func() { missing.ToThrowCause(cause) }, func() { missing.Not.ToThrowCause(cause) },
		func() { FunctionExpectationToThrowType(missing, &target) },
		func() { NegatedFunctionExpectationToThrowType[*os.PathError](missing.Not) },
	} {
		assertThrownMessage(t, "function expectation body must not be nil", attempt)
	}
}

func TestFunctionMatcherNativePanicsStillUnwindCleanup(t *testing.T) {
	bodyPanic := expectFunction(func() { panic("native function matcher panic") })
	matchingPanic := expectFunction(func() { panic(__kinmokuseiThrown{err: assertionPanickingMatcherError{}}) })
	checks := []struct {
		want    string
		attempt func()
	}{
		{"native function matcher panic", func() { bodyPanic.ToThrow() }},
		{"native function matcher panic", bodyPanic.Not.ToThrow},
		{"native function matcher panic", func() { bodyPanic.ToThrowMessage("missing") }},
		{"native error matcher panic", func() { matchingPanic.ToThrowCause(os.ErrNotExist) }},
		{"native error matcher panic", func() { matchingPanic.Not.ToThrowCause(os.ErrNotExist) }},
		{"native error matcher panic", func() { var found *os.PathError; FunctionExpectationToThrowType(matchingPanic, &found) }},
		{"native error matcher panic", func() { NegatedFunctionExpectationToThrowType[*os.PathError](matchingPanic.Not) }},
	}
	for _, check := range checks {
		func() {
			var events []string
			defer func() {
				if recovered := recover(); recovered != check.want || fmt.Sprint(events) != "[afterEach cleanup afterAll]" {
					t.Fatalf("panic=%#v cleanup=%v", recovered, events)
				}
			}()
			suite := NewSuite("function matcher panic")
			suite.AfterEach(func() { events = append(events, "afterEach") })
			suite.AfterAll(func() { events = append(events, "afterAll") })
			suite.TestWithContext("case", func(context *TestContext) {
				context.Cleanup(func() { events = append(events, "cleanup") })
				check.attempt()
			})
			suite.RunTo(&bytes.Buffer{})
		}()
	}
}

func TestErrorMatchersPreserveCauseTypeAndCustomMatching(t *testing.T) {
	sentinel := errors.New("same message")
	wrapped := errors.Join(errors.New("other"), fmt.Errorf("wrapped: %w", sentinel))
	matcher := expectErrorValue(wrapped)
	matcher.ToMatchCause(sentinel)
	matcher.Not.ToMatchCause(errors.New("same message"))
	matcher.Not.ToBeNil()
	expectErrorValue(nil).ToBeNil()
	expectErrorValue(nil).ToMatchCause(nil)
	expectErrorValue(nil).Not.ToMatchCause(sentinel)
	NegatedErrorExpectationToMatchType[*os.PathError](expectErrorValue(nil).Not)
	NegatedErrorExpectationToMatchType[*os.PathError](matcher.Not)
	var found error
	ErrorExpectationToMatchType(matcher, &found)
	if found != wrapped {
		t.Fatal("interface extraction changed error identity")
	}
	code := &assertionCodeError{Code: 42}
	mapped := expectErrorValue(assertionMappedError{code: code})
	mapped.ToMatchCause(&assertionCodeError{Code: 42})
	mapped.Not.ToMatchCause(&assertionCodeError{Code: 43})
	var extracted *assertionCodeError
	ErrorExpectationToMatchType[*assertionCodeError](mapped, &extracted)
	if extracted != code {
		t.Fatal("custom As extraction lost")
	}
	assertThrownMessage(t, "not to contain", func() { NegatedErrorExpectationToMatchType[*assertionCodeError](mapped.Not) })
	var stringer fmt.Stringer
	ErrorExpectationToMatchType(expectErrorValue(fmt.Errorf("outer: %w", code)), &stringer)
	if stringer != code {
		t.Fatal("non-error interface extraction lost")
	}
}

func TestErrorMatchersHandleTypedNilAndValidateTypesBeforeMatching(t *testing.T) {
	var empty *os.PathError
	typedNil := expectErrorValue(error(empty))
	typedNil.Not.ToBeNil() // The error interface is non-nil, as with err != nil.
	found := &os.PathError{Path: "previous"}
	ErrorExpectationToMatchType(typedNil, &found)
	if found != nil {
		t.Fatal("typed nil extraction lost")
	}
	assertThrownMessage(t, "not to contain", func() { NegatedErrorExpectationToMatchType[*os.PathError](typedNil.Not) })
	assertThrownMessage(t, "expected no error", typedNil.ToBeNil)
	for _, source := range []error{nil, errors.New("actual")} {
		matcher := expectErrorValue(source)
		var integer int
		var value os.PathError
		var nilTarget **os.PathError
		assertThrownMessage(t, "target must not be nil", func() { ErrorExpectationToMatchType(matcher, nilTarget) })
		assertThrownMessage(t, "got int", func() { ErrorExpectationToMatchType(matcher, &integer) })
		assertThrownMessage(t, "got fs.PathError", func() { ErrorExpectationToMatchType(matcher, &value) })
		assertThrownMessage(t, "got int", func() { NegatedErrorExpectationToMatchType[int](matcher.Not) })
		assertThrownMessage(t, "got fs.PathError", func() { NegatedErrorExpectationToMatchType[os.PathError](matcher.Not) })
	}
	assertThrownMessage(t, "expected an error", expectErrorValue(nil).Not.ToBeNil)
	assertThrownMessage(t, "not to match", func() { expectErrorValue(nil).Not.ToMatchCause(nil) })
	assertThrownMessage(t, "(errors.Is)", func() { expectErrorValue(nil).ToMatchCause(os.ErrNotExist) })
	var unchanged *os.PathError = &os.PathError{Path: "previous"}
	previous := unchanged
	assertThrownMessage(t, "(errors.As)", func() { ErrorExpectationToMatchType(expectErrorValue(errors.New("other")), &unchanged) })
	if unchanged != previous {
		t.Fatal("mismatch changed ordinary destination")
	}
	var updated *assertionCodeError
	assertThrownMessage(t, "(errors.As)", func() { ErrorExpectationToMatchType(expectErrorValue(assertionMutatingAsError{}), &updated) })
	if updated == nil || updated.Code != 37 {
		t.Fatal("custom As destination update lost")
	}
	NegatedErrorExpectationToMatchType[*assertionCodeError](expectErrorValue(assertionMutatingAsError{}).Not)
}

type assertionPanickingMatcherError struct{}

func (assertionPanickingMatcherError) Error() string { return "panicking matcher" }
func (assertionPanickingMatcherError) Is(error) bool { panic("native error matcher panic") }
func (assertionPanickingMatcherError) As(any) bool   { panic("native error matcher panic") }

func TestErrorMatchersNativePanicsStillUnwindCleanup(t *testing.T) {
	matcher := expectErrorValue(assertionPanickingMatcherError{})
	for _, attempt := range []func(){
		func() { matcher.ToMatchCause(os.ErrNotExist) },
		func() { matcher.Not.ToMatchCause(os.ErrNotExist) },
		func() { var found *os.PathError; ErrorExpectationToMatchType(matcher, &found) },
		func() { NegatedErrorExpectationToMatchType[*os.PathError](matcher.Not) },
	} {
		func() {
			var events []string
			defer func() {
				if recovered := recover(); recovered != "native error matcher panic" || fmt.Sprint(events) != "[afterEach cleanup afterAll]" {
					t.Fatalf("panic=%#v cleanup=%v", recovered, events)
				}
			}()
			suite := NewSuite("native matcher panic")
			suite.AfterEach(func() { events = append(events, "afterEach") })
			suite.AfterAll(func() { events = append(events, "afterAll") })
			suite.TestWithContext("case", func(context *TestContext) {
				context.Cleanup(func() { events = append(events, "cleanup") })
				attempt()
			})
			suite.RunTo(&bytes.Buffer{})
		}()
	}
}

func TestExpectErrorAsExtractsTypedValuesAndInterfaces(t *testing.T) {
	original := &os.PathError{Op: "open", Path: "missing", Err: os.ErrNotExist}
	wrapped := errors.Join(errors.New("unrelated"), fmt.Errorf("wrapped: %w", original))
	var found *os.PathError
	expectErrorAs(wrapped, &found)
	if found != original || found.Path != "missing" {
		t.Fatalf("extraction changed identity: %#v", found)
	}
	var anyError error
	expectErrorAs(wrapped, &anyError)
	if anyError != wrapped {
		t.Fatalf("interface extraction=%v", anyError)
	}
	code := &assertionCodeError{Code: 27}
	var stringer fmt.Stringer
	expectErrorAs(fmt.Errorf("wrapped: %w", code), &stringer)
	if stringer != code {
		t.Fatalf("non-error interface extraction=%v", stringer)
	}
	var empty any
	expectErrorAs(wrapped, &empty)
	if empty != wrapped {
		t.Fatalf("empty interface extraction=%v", empty)
	}
	// Custom As may supply a value absent from the chain's concrete types.
	var mapped *assertionCodeError
	expectErrorAs(errors.Join(errors.New("other"), assertionMappedError{code: code}), &mapped)
	if mapped != code {
		t.Fatalf("custom As result=%#v", mapped)
	}
	assertThrownMessage(t, "(errors.As)", func() { expectErrorAs(assertionMutatingAsError{}, &mapped) })
	if mapped == nil || mapped.Code != 37 {
		t.Fatalf("custom As destination update lost: %#v", mapped)
	}
	// A typed nil error still matches its dynamic type and assigns nil.
	found = original
	var typedNil *os.PathError
	expectErrorAs(error(typedNil), &found)
	if found != nil {
		t.Fatal("typed nil was not assigned")
	}
	// A failed match must leave an existing destination unchanged.
	found = original
	assertThrownMessage(t, "*fs.PathError (errors.As)", func() { expectErrorAs(errors.New("other"), &found) })
	if found != original {
		t.Fatal("mismatch modified destination")
	}
	assertThrownMessage(t, "expected error <nil>", func() { expectErrorAs(nil, &found) })
	located := expectThrows(func() {
		withTestLocation("errors.test.km", 11, func() { panic(__kinmokuseiThrown{err: original}) })
	})
	expectErrorAs(located, &found)
	if found != original {
		t.Fatal("location wrapper hid concrete error")
	}
}

func TestExpectErrorAsInvalidTargetsFailAsAssertions(t *testing.T) {
	for _, source := range []error{nil, errors.New("actual")} {
		var integer int
		var concrete os.PathError // Error is implemented by *PathError, not PathError.
		var nilTarget **os.PathError
		for _, tc := range []struct {
			name, want string
			body       func()
		}{
			{"nil target", "target must not be nil", func() { expectErrorAs(source, nilTarget) }},
			{"primitive target", "got int", func() { expectErrorAs(source, &integer) }},
			{"value with pointer receiver", "got fs.PathError", func() { expectErrorAs(source, &concrete) }},
		} {
			t.Run(fmt.Sprintf("%v/%s", source, tc.name), func(t *testing.T) { assertThrownMessage(t, tc.want, tc.body) })
		}
	}
}

func TestErrorAssertionFailuresContinueAndRunCleanup(t *testing.T) {
	var phases []string
	suite := NewSuite("error assertions")
	suite.AfterEach(func() { phases = append(phases, "afterEach") })
	suite.TestWithContext("wrong cause", func(ctx *TestContext) {
		ctx.Cleanup(func() { phases = append(phases, "cleanup") })
		expectErrorIs(errors.New("actual"), errors.New("expected"))
	})
	suite.Test("invalid type", func() { var wrong int; expectErrorAs(errors.New("actual"), &wrong) })
	suite.Test("later case", func() { phases = append(phases, "later") })
	summary := suite.RunTo(&bytes.Buffer{})
	if summary.Passed != 1 || summary.Failed != 2 || fmt.Sprint(phases) != "[afterEach cleanup afterEach later afterEach]" {
		t.Fatalf("summary=%#v phases=%v", summary, phases)
	}
	for _, result := range summary.Results[:2] {
		if len(result.Failures) != 1 || result.Failures[0].Phase != "body" || result.Failures[0].Location.Source != "go" {
			t.Fatalf("failure metadata=%#v", result)
		}
	}
}

func TestRuntimeSkipUnwindsBodyFixturesAndContinuesWithBail(t *testing.T) {
	t.Setenv("VASEST_BAIL", "1")
	t.Setenv("VASEST_FAIL_ON_TODO", "1")
	t.Setenv("VASEST_DYNAMIC_RESOURCE", "original")
	for _, phase := range []string{"body", "beforeEach", "fixture"} {
		t.Run(phase, func(t *testing.T) {
			var events []string
			var path string
			suite := NewSuite("runtime skip")
			suite.BeforeEach(func() {
				if phase == "beforeEach" {
					skipTest("dependency unavailable")
				}
			})
			suite.AfterEach(func() {
				events = append(events, "afterEach")
				if path != "" {
					if _, err := os.Stat(path); err != nil || os.Getenv("VASEST_DYNAMIC_RESOURCE") != "temporary" {
						t.Fatal("resource disappeared before afterEach", err)
					}
				}
				time.Sleep(2 * time.Millisecond)
			})
			setup := func(ctx *TestContext) string {
				path = ctx.TempDir()
				ctx.SetEnv("VASEST_DYNAMIC_RESOURCE", "temporary")
				ctx.Cleanup(func() { events = append(events, "first cleanup") })
				ctx.Cleanup(func() { events = append(events, "second cleanup") })
				if phase == "fixture" {
					skipTest("dependency unavailable")
				}
				return path
			}
			if phase == "fixture" {
				SuiteTestWithFixture(suite, "conditional", setup, func(string, *TestContext) { t.Fatal("body after skipped fixture") })
			} else {
				suite.TestWithContext("conditional", func(ctx *TestContext) {
					if phase == "beforeEach" {
						t.Fatal("body after skipped hook")
					}
					setup(ctx)
					skipTest("dependency unavailable")
					t.Fatal("body continued after skip")
				})
			}
			// Clear only the per-case setup hook so the next case can execute.
			calls := 0
			if phase == "beforeEach" {
				suite.BeforeEach(func() {
					calls++
					if calls == 1 {
						skipTest("dependency unavailable")
					}
				})
			}
			suite.Test("later", func() {
				events = append(events, "later")
				if os.Getenv("VASEST_DYNAMIC_RESOURCE") != "original" {
					t.Fatal("skip leaked environment")
				}
				if path != "" {
					if _, err := os.Stat(path); !os.IsNotExist(err) {
						t.Fatal("skip leaked directory", path, err)
					}
				}
				path = ""
			})
			var output bytes.Buffer
			summary := suite.RunTo(&output)
			if !summary.Success() || summary.Skipped != 1 || summary.Passed != 1 || summary.Failed != 0 || summary.Results[0].Status != "skip" || summary.Results[0].Message != "dependency unavailable" || len(summary.Results[0].Failures) != 0 || summary.Results[0].DurationNs < int64(2*time.Millisecond) {
				t.Fatalf("summary=%#v %s", summary, output.String())
			}
			want := "[afterEach second cleanup first cleanup later afterEach]"
			if phase == "beforeEach" {
				want = "[afterEach later afterEach]"
			}
			if fmt.Sprint(events) != want {
				t.Fatal(events)
			}
		})
	}
}

func TestRuntimeSkipSharedSetupReleasesPartialResources(t *testing.T) {
	for _, typed := range []bool{false, true} {
		t.Run(fmt.Sprint(typed), func(t *testing.T) {
			var path string
			var events []string
			setup := func(ctx *TestContext) string {
				path = ctx.TempDir()
				ctx.Cleanup(func() { events = append(events, "cleanup") })
				skipTest("shared dependency unavailable")
				return path
			}
			var summary Summary
			if typed {
				suite := NewFixtureSuite("shared skip", setup)
				suite.BeforeAll(func(string) { t.Fatal("typed beforeAll without a value") })
				suite.AfterAll(func(string) { t.Fatal("typed afterAll without a value") })
				suite.Test("first", func(string, *TestContext) { t.Fatal("shared skipped body") })
				suite.Test("second", func(string, *TestContext) { t.Fatal("shared skipped body") })
				suite.Skip("disabled", func(string, *TestContext) {})
				suite.Todo("planned")
				summary = suite.RunTo(&bytes.Buffer{})
			} else {
				suite := NewSuite("shared skip")
				suite.BeforeAllWithContext(func(ctx *TestContext) { setup(ctx) })
				suite.AfterAll(func() {
					if _, err := os.Stat(path); err != nil {
						t.Fatal(err)
					}
					events = append(events, "afterAll")
				})
				suite.Test("first", func() { t.Fatal("shared skipped body") })
				suite.Test("second", func() { t.Fatal("shared skipped body") })
				suite.Skip("disabled", func() {})
				suite.Todo("planned")
				summary = suite.RunTo(&bytes.Buffer{})
			}
			if summary.Failed != 0 || summary.Skipped != 3 || summary.Todo != 1 || summary.DurationNs <= 0 || len(summary.Results) != 4 {
				t.Fatalf("summary=%#v", summary)
			}
			for _, result := range summary.Results[:2] {
				if result.Status != "skip" || result.Message != "shared dependency unavailable" || result.DurationNs != 0 {
					t.Fatal(result)
				}
			}
			if summary.Results[2].Message != "" || summary.Results[3].Status != "todo" {
				t.Fatal("static skip/todo changed", summary)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("partial shared resource leaked", path, err)
			}
			want := "[cleanup]"
			if !typed {
				want = "[afterAll cleanup]"
			}
			if fmt.Sprint(events) != want {
				t.Fatal(events)
			}
		})
	}
}

func TestRuntimeSkipCannotHideCleanupErrorsOrJoinedFailures(t *testing.T) {
	for _, phase := range []string{"afterEach", "cleanup", "afterAll", "joined", "ordinary error"} {
		t.Run(phase, func(t *testing.T) {
			suite := NewSuite("skip failures")
			if phase == "afterEach" {
				suite.AfterEach(func() { skipTest("invalid teardown skip") })
			}
			if phase == "afterAll" {
				suite.AfterAll(func() { skipTest("invalid teardown skip") })
			}
			suite.TestWithContext("conditional", func(ctx *TestContext) {
				if phase == "cleanup" {
					ctx.Cleanup(func() { skipTest("invalid teardown skip") })
				}
				if phase == "joined" {
					signal := expectThrows(func() { skipTest("unavailable") })
					panic(__kinmokuseiThrown{err: errors.Join(signal, errors.New("genuine failure"))})
				}
				if phase == "ordinary error" {
					panic(__kinmokuseiThrown{err: errors.New("unavailable")})
				}
				skipTest("unavailable")
			})
			summary := suite.RunTo(&bytes.Buffer{})
			if summary.Success() || summary.Failed != 1 {
				t.Fatal(summary)
			}
			failed := summary.Results[0]
			if phase == "afterAll" {
				failed = summary.Results[1]
				if summary.Skipped != 1 {
					t.Fatal(summary)
				}
			}
			if failed.Status != "fail" || len(failed.Failures) != 1 {
				t.Fatal(failed)
			}
			wantPhase := phase
			if phase == "joined" || phase == "ordinary error" {
				wantPhase = "body"
			}
			if failed.Failures[0].Phase != wantPhase {
				t.Fatal(failed)
			}
			if phase == "joined" && !strings.Contains(failed.Message, "genuine failure") {
				t.Fatal(failed)
			}
		})
	}
	// A genuine cleanup error overrides an earlier request to skip and bails.
	t.Setenv("VASEST_BAIL", "1")
	suite := NewSuite("cleanup overrides skip")
	suite.TestWithContext("conditional", func(ctx *TestContext) {
		ctx.Cleanup(func() { fail("cleanup failed") })
		skipTest("unavailable")
	})
	suite.Test("later", func() { t.Fatal("cleanup failure did not bail") })
	summary := suite.RunTo(&bytes.Buffer{})
	if summary.Failed != 1 || summary.Skipped != 1 || summary.Results[0].Failures[0].Phase != "cleanup" {
		t.Fatal(summary)
	}
}

func TestRuntimeSkipFilteringRerunsAndWrappedReasons(t *testing.T) {
	t.Setenv("VASEST_FILTER", "row")
	t.Setenv("VASEST_SHUFFLE_SEED", "42")
	log := filepath.Join(t.TempDir(), "matches")
	if err := os.WriteFile(log, nil, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VASEST_MATCH_LOG", log)
	suite := NewSuite("skip table")
	setups, cleanups := 0, 0
	SuiteTestEachWithFixture(suite, []int{1, 2, 3}, func(row int) string { return fmt.Sprintf("row %d", row) }, func(row int, ctx *TestContext) int {
		setups++
		ctx.Cleanup(func() { cleanups++ })
		if row == 2 {
			withTestLocation("skip.test.km", 42, func() { skipTest("環境が未設定") })
		}
		return row
	}, func(row, value int, ctx *TestContext) {
		if row != value {
			t.Fatal(row, value)
		}
	})
	suite.Test("excluded", func() { t.Fatal("filter ignored") })
	for run := 0; run < 2; run++ {
		summary := suite.RunTo(&bytes.Buffer{})
		if summary.Passed != 2 || summary.Skipped != 2 || summary.Failed != 0 {
			t.Fatal(summary)
		}
		for _, result := range summary.Results {
			if result.Name == "row 2" && (result.Status != "skip" || result.Message != "環境が未設定") {
				t.Fatal(result)
			}
		}
	}
	data, err := os.ReadFile(log)
	if err != nil || string(data) != strings.Repeat("1\n", 6) || setups != 6 || cleanups != 6 {
		t.Fatal(string(data), err, setups, cleanups)
	}
	for _, reason := range []string{"", "  ", "\n\t"} {
		assertThrownMessage(t, "skipTest reason must not be empty", func() { skipTest(reason) })
	}
	// The public helper throws a control signal: an explicit catch can intercept it.
	signal := expectThrows(func() { skipTest("caught intentionally") })
	if signal.Error() != "caught intentionally" {
		t.Fatal(signal)
	}
}

func TestRuntimeSkipStillPropagatesNativeCleanupPanics(t *testing.T) {
	var events []string
	suite := NewSuite("skip and panic")
	suite.AfterAll(func() { events = append(events, "afterAll") })
	suite.TestWithContext("conditional", func(ctx *TestContext) {
		ctx.Cleanup(func() { events = append(events, "remaining cleanup") })
		ctx.Cleanup(func() { panic("native cleanup panic") })
		skipTest("unavailable")
	})
	func() {
		defer func() {
			if got := recover(); got != "native cleanup panic" {
				t.Fatalf("panic changed/swallowed: %v", got)
			}
		}()
		suite.RunTo(&bytes.Buffer{})
	}()
	if fmt.Sprint(events) != "[remaining cleanup afterAll]" {
		t.Fatal(events)
	}
}

func TestRuntimeSkipTypedBeforeAllKeepsReturnedFixtureThroughAfterAll(t *testing.T) {
	var events []string
	var path string
	suite := NewFixtureSuite("typed hook skip", func(ctx *TestContext) string {
		path = ctx.TempDir()
		ctx.Cleanup(func() { events = append(events, "cleanup") })
		return path
	})
	suite.BeforeAll(func(value string) { skipTest("typed hook unavailable") })
	suite.AfterAll(func(value string) {
		if value != path {
			t.Fatal("shared fixture changed", value, path)
		}
		if _, err := os.Stat(value); err != nil {
			t.Fatal("fixture removed before afterAll", err)
		}
		events = append(events, "afterAll")
	})
	suite.Test("conditional", func(string, *TestContext) { t.Fatal("body ran after shared skip") })
	for run := 0; run < 2; run++ {
		summary := suite.RunTo(&bytes.Buffer{})
		if !summary.Success() || summary.Skipped != 1 || summary.Failed != 0 || summary.Results[0].Message != "typed hook unavailable" {
			t.Fatal(summary)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("shared skip leaked fixture", err)
		}
	}
	if fmt.Sprint(events) != "[afterAll cleanup afterAll cleanup]" {
		t.Fatal(events)
	}
}
