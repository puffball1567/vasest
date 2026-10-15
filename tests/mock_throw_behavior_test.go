package vasest

import (
	"bytes"
	"errors"
	"reflect"
	"testing"
)

func TestMockThrowBehaviorFunctionMixesQueueAndDefault(t *testing.T) {
	type ID int64
	first, repeated := errors.New("first"), errors.New("default")
	defaults, queued := 0, 0
	mock := NewMockFn[ID, ID](func(value ID) ID { defaults++; return value + 1 })
	call := mock.Invoke
	mock.ReturnValueOnce(ID(1<<60 + 1))
	mock.ThrowErrorOnce(first)
	mock.ImplementOnce(func(value ID) ID { queued++; return value + 2 })
	mock.ThrowError(repeated)
	if mock.CallCount() != 0 || defaults != 0 || queued != 0 {
		t.Fatal("configuration invoked mock")
	}
	if got := call(ID(40)); got != 1<<60+1 {
		t.Fatal("lost native return width", got)
	}
	if got := expectThrows(func() { call(ID(41)) }); got != first {
		t.Fatal("one-shot error replaced", got)
	}
	if got := call(ID(42)); got != 44 || queued != 1 {
		t.Fatal(got, queued)
	}
	for _, value := range []ID{43, 44} {
		if got := expectThrows(func() { call(value) }); got != repeated {
			t.Fatal(got)
		}
	}
	if defaults != 0 || !reflect.DeepEqual(mock.Calls(), []ID{40, 41, 42, 43, 44}) {
		t.Fatal(defaults, mock.Calls())
	}
	results := mock.Results()
	if len(results) != 5 || results[1].Status != "throw" || results[1].Exception != first || results[1].Value != 0 || results[3].Exception != repeated {
		t.Fatal(results)
	}
	mock.ReturnValue(ID(1<<60 + 2))
	if got := call(45); got != 1<<60+2 {
		t.Fatal(got)
	}
	mock.Implement(func(value ID) ID { return value + 3 })
	if got := call(46); got != 49 {
		t.Fatal(got)
	}
	if len(results) != 5 {
		t.Fatal("old snapshot changed")
	}
}

func TestMockThrowBehaviorActionMixesQueueDefaultAndBindings(t *testing.T) {
	first, repeated := errors.New("one-shot"), errors.New("default")
	total := 0
	mock := NewMockAction[int](func(value int) { total += value })
	call := mock.Invoke
	mock.ImplementOnce(func(value int) { total += value * 10 })
	mock.ThrowErrorOnce(first)
	mock.ThrowError(repeated)
	call(2)
	if got := expectThrows(func() { call(3) }); got != first {
		t.Fatal(got)
	}
	if got := expectThrows(func() { call(4) }); got != repeated {
		t.Fatal(got)
	}
	if total != 20 || mock.CallCount() != 3 {
		t.Fatal(total, mock.CallCount())
	}
	mock.Implement(func(value int) { total += value })
	call(5)
	if total != 25 {
		t.Fatal(total)
	}
	mock.ExpectReturnedTimes(2)
	mock.ExpectThrownTimes(2)
	results := mock.Results()
	if results[0].Status != "return" || results[1].Exception != first || results[2].Exception != repeated || results[3].Status != "return" {
		t.Fatal(results)
	}
}

func TestMockThrowBehaviorPreservesErrorReferencesNilAndReturnDistinction(t *testing.T) {
	original := &predicateDomainError{Code: 1<<60 + 1}
	function := NewMockFn[int, error](func(int) error { return original })
	action := NewMockAction[int](func(int) {})
	for _, configure := range []func(error){function.ThrowError, function.ThrowErrorOnce, action.ThrowError, action.ThrowErrorOnce} {
		configure(original)
	}
	original.Code++
	for _, call := range []func(){func() { function.Invoke(1) }, func() { function.Invoke(2) }, func() { action.Invoke(1) }, func() { action.Invoke(2) }} {
		if got := expectThrows(call); got != original || original.Code != 1<<60+2 {
			t.Fatal("error identity/native fields changed")
		}
	}
	if original.Reads != 0 {
		t.Fatal("helper inspected error", original.Reads)
	}
	for _, cause := range []error{nil, (*predicateDomainError)(nil)} {
		for _, once := range []bool{false, true} {
			function := NewMockFn[int, *int](func(int) *int { return nil })
			action := NewMockAction[int](func(int) {})
			if once {
				function.ThrowErrorOnce(cause)
				action.ThrowErrorOnce(cause)
			} else {
				function.ThrowError(cause)
				action.ThrowError(cause)
			}
			if got := expectThrows(func() { function.Invoke(1) }); got != cause {
				t.Fatal("function changed nil payload", got)
			}
			if got := expectThrows(func() { action.Invoke(1) }); got != cause {
				t.Fatal("action changed nil payload", got)
			}
			if function.Results()[0].Status != "throw" || function.Results()[0].Value != nil || action.Results()[0].Status != "throw" {
				t.Fatal("nil throw became return")
			}
			if once {
				function.Invoke(2)
				action.Invoke(2)
			} else {
				expectThrows(func() { function.Invoke(2) })
				expectThrows(func() { action.Invoke(2) })
			}
			want := "throw"
			if once {
				want = "return"
			}
			if function.Results()[1].Status != want || action.Results()[1].Status != want {
				t.Fatal("one-shot behavior repeated")
			}
		}
	}
	function.ReturnValue(original)
	if got := function.Invoke(3); got != original || function.Results()[2].Status != "return" {
		t.Fatal("returned error treated as throw")
	}
}

func TestMockThrowBehaviorClearResetAndReentrantConfiguration(t *testing.T) {
	first, repeated := errors.New("queued"), errors.New("default")
	mock := NewMockFn[int, int](func(value int) int { return value + 1 })
	mock.Invoke(0)
	old := mock.Results()
	mock.ThrowErrorOnce(first)
	mock.ThrowError(repeated)
	mock.ClearCalls()
	if len(old) != 1 || mock.CallCount() != 0 {
		t.Fatal("configuration changed snapshot/history")
	}
	if expectThrows(func() { mock.Invoke(1) }) != first || expectThrows(func() { mock.Invoke(2) }) != repeated {
		t.Fatal("clear discarded throw behavior")
	}
	mock.ThrowErrorOnce(first)
	mock.Reset(func(value int) int { return value + 2 })
	if mock.CallCount() != 0 || mock.Invoke(3) != 5 {
		t.Fatal("reset retained throw behavior")
	}
	mock.Implement(func(value int) int {
		mock.ThrowErrorOnce(first)
		mock.ThrowError(repeated)
		if expectThrows(func() { mock.Invoke(value + 1) }) != first {
			t.Fatal("reentrant queue replaced")
		}
		return value + 3
	})
	if mock.Invoke(4) != 7 || expectThrows(func() { mock.Invoke(6) }) != repeated {
		t.Fatal("active callback replaced")
	}
	action := NewMockAction[int](func(int) {})
	action.ThrowErrorOnce(first)
	action.ThrowError(repeated)
	action.ClearCalls()
	if expectThrows(func() { action.Invoke(1) }) != first || expectThrows(func() { action.Invoke(2) }) != repeated {
		t.Fatal("action clear changed behavior")
	}
	action.ThrowErrorOnce(first)
	action.Reset(func(int) {})
	action.Invoke(3)
	if action.CallCount() != 1 || action.Results()[0].Status != "return" {
		t.Fatal("action reset retained throw behavior")
	}
}

func TestMockThrowBehaviorActiveCallsFinishCapturedImplementation(t *testing.T) {
	first, repeated := errors.New("queued"), errors.New("default")
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	mock := NewMockFn[int, int](func(value int) int { close(entered); <-release; return value + 1 })
	go func() {
		defer close(done)
		if mock.Invoke(40) != 41 {
			t.Error("active result replaced")
		}
	}()
	<-entered
	mock.ThrowError(repeated)
	mock.ThrowErrorOnce(first)
	if expectThrows(func() { mock.Invoke(42) }) != first || expectThrows(func() { mock.Invoke(43) }) != repeated {
		t.Fatal("new invocation behavior wrong")
	}
	close(release)
	<-done
	results := mock.Results()
	if results[0].Status != "return" || results[0].Value != 41 || results[1].Exception != first || results[2].Exception != repeated {
		t.Fatal("completion order changed invocation order", results)
	}
}

func TestMockThrowBehaviorRuntimeSkipAndCleanup(t *testing.T) {
	signal := expectThrows(func() { skipTest("unavailable") })
	for _, once := range []bool{false, true} {
		for _, action := range []bool{false, true} {
			cleaned := 0
			function := NewMockFn[int, int](func(int) int { return 42 })
			notify := NewMockAction[int](func(int) {})
			if once {
				function.ThrowErrorOnce(signal)
				notify.ThrowErrorOnce(signal)
			} else {
				function.ThrowError(signal)
				notify.ThrowError(signal)
			}
			suite := NewSuite("skip")
			suite.TestWithContext("first", func(context *TestContext) {
				context.Cleanup(func() { cleaned++ })
				if action {
					notify.Invoke(1)
				} else {
					function.Invoke(1)
				}
				t.Fatal("continued after skip")
			})
			suite.TestWithContext("second", func(context *TestContext) {
				context.Cleanup(func() { cleaned++ })
				if action {
					notify.Invoke(2)
				} else {
					function.Invoke(2)
				}
			})
			result := suite.RunTo(&bytes.Buffer{})
			wantSkip, wantPass := 2, 0
			if once {
				wantSkip, wantPass = 1, 1
			}
			if result.Skipped != wantSkip || result.Passed != wantPass || result.Failed != 0 || cleaned != 2 {
				t.Fatal(result, cleaned)
			}
		}
	}
}
