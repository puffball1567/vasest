package vasest

import (
	"bytes"
	"errors"
	"reflect"
	"sync"
	"testing"
)

func TestMockResetReplacesDefaultDiscardsQueueAndClearsAllHistory(t *testing.T) {
	type ID int64
	type record struct{ Value int64 }
	old, fresh := &record{1<<60 + 1}, &record{1<<60 + 2}
	mock := NewMockFn(func(value ID) *record { return old })
	mock.Invoke(ID(42))
	mock.ReturnValueOnce(old)
	queuedCalls := 0
	mock.ImplementOnce(func(ID) *record { queuedCalls++; return old })
	calls, results, outcomes := mock.Calls(), mock.Results(), mock.Outcomes()
	callMatcher, returnMatcher, outcomeMatcher := expectMock[ID](mock), expectMockReturn[*record](mock), expectMockOutcome(mock)
	mock.Reset(func(value ID) *record {
		if value != ID(1<<60+1) {
			t.Fatal("native argument changed")
		}
		return fresh
	})
	if mock.CallCount() != 0 || len(mock.Calls()) != 0 || len(mock.Results()) != 0 || len(mock.Outcomes()) != 0 {
		t.Fatal("reset retained history")
	}
	callMatcher.Not.ToHaveBeenCalled()
	returnMatcher.Not.ToHaveReturned()
	outcomeMatcher.Not.ToHaveReturned()
	if mock.Invoke(ID(1<<60+1)) != fresh || queuedCalls != 0 {
		t.Fatal("reset kept queued/default behavior")
	}
	callMatcher.ToHaveBeenCalledWith(ID(1<<60 + 1))
	returnMatcher.ToHaveReturnedWith(fresh)
	outcomeMatcher.ToHaveReturnedTimes(1)
	if len(calls) != 1 || calls[0] != 42 || len(results) != 1 || results[0].Value != old || len(outcomes) != 1 || outcomes[0].Status != "return" {
		t.Fatal("reset changed prior snapshots")
	}
	// Queueing, default replacement and clearCalls still work after a reset.
	mock.ReturnValueOnce(nil)
	mock.ClearCalls()
	if mock.Invoke(ID(1<<60+1)) != nil || mock.Invoke(ID(1<<60+1)) != fresh {
		t.Fatal("clearCalls no longer preserves behavior")
	}
	mock.Reset(func(ID) *record { return nil })
	if mock.Invoke(0) != nil || mock.Results()[0].Status != "return" {
		t.Fatal("nil return changed")
	}
}

func TestMockResetRejectsNilWithoutChangingState(t *testing.T) {
	mock := NewMockFn(func(value int) int { return value + 1 })
	mock.Invoke(1)
	mock.ReturnValueOnce(42)
	assertThrownMessage(t, "must not be nil", func() { mock.Reset(nil) })
	if mock.CallCount() != 1 || mock.Invoke(2) != 42 || mock.Invoke(3) != 4 {
		t.Fatal("invalid reset changed function state")
	}
	var values []int
	action := NewMockAction(func(value int) { values = append(values, value) })
	action.Invoke(1)
	action.ImplementOnce(func(value int) { values = append(values, value*10) })
	assertThrownMessage(t, "must not be nil", func() { action.Reset(nil) })
	if action.CallCount() != 1 {
		t.Fatal("invalid reset cleared action history")
	}
	action.Invoke(2)
	action.Invoke(3)
	if !reflect.DeepEqual(values, []int{1, 20, 3}) {
		t.Fatal("invalid reset changed action behavior", values)
	}
}

func TestMockResetInsideActiveCallsDiscardsTheirEventualOutcomes(t *testing.T) {
	for _, status := range []string{"return", "throw", "panic"} {
		t.Run(status, func(t *testing.T) {
			cause := errors.New("old implementation threw")
			var mock *MockFn[int, int]
			mock = NewMockFn(func(value int) int {
				mock.Reset(func(value int) int { return value * 2 })
				if got := mock.Invoke(value + 1); got != 22 {
					t.Fatal(got)
				}
				if status == "throw" {
					panic(__kinmokuseiThrown{err: cause})
				}
				if status == "panic" {
					panic("old native panic")
				}
				return 23
			})
			mock.ReturnValueOnce(99)
			// Consume the one-shot before the active default callback resets itself.
			if mock.Invoke(0) != 99 {
				t.Fatal("one-shot")
			}
			func() {
				defer func() {
					got := recover()
					if status == "return" && got != nil {
						t.Fatal(got)
					}
					if status == "throw" {
						thrown, ok := got.(__kinmokuseiThrown)
						if !ok || thrown.err != cause {
							t.Fatal(got)
						}
					}
					if status == "panic" && got != "old native panic" {
						t.Fatal(got)
					}
				}()
				if got := mock.Invoke(10); status != "return" || got != 23 {
					t.Fatal(status, got)
				}
			}()
			if mock.CallCount() != 1 || !reflect.DeepEqual(mock.Calls(), []int{11}) || len(mock.Results()) != 1 || mock.Results()[0].Value != 22 || mock.Results()[0].Status != "return" {
				t.Fatal("old completion re-entered history", mock.Calls(), mock.Results())
			}
		})
	}
	var action *MockAction[int]
	var values []int
	action = NewMockAction(func(value int) {
		action.Reset(func(value int) { values = append(values, value) })
		action.Invoke(value + 1)
	})
	action.Invoke(10)
	if !reflect.DeepEqual(values, []int{11}) || !reflect.DeepEqual(action.Calls(), []int{11}) || len(action.Outcomes()) != 1 || action.Outcomes()[0].Status != "return" {
		t.Fatal(values, action.Calls(), action.Results())
	}
}

func TestMockResetDuringConcurrentInvocationsKeepsNewGenerationIsolated(t *testing.T) {
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan int, 1)
	mock := NewMockFn(func(value int) int { close(started); <-release; return value + 100 })
	go func() { done <- mock.Invoke(1) }()
	<-started
	oldCalls, oldResults := mock.Calls(), mock.Results()
	if len(oldResults) != 1 || oldResults[0].Status != "incomplete" {
		t.Fatal(oldResults)
	}
	mock.Reset(func(value int) int { return value * 100 })
	mock.ReturnValueOnce(999)
	if mock.Invoke(3) != 999 || mock.Invoke(4) != 400 {
		t.Fatal("reset retained old queue/default")
	}
	close(release)
	if got := <-done; got != 101 {
		t.Fatal("reset interrupted active callback", got)
	}
	if !reflect.DeepEqual(mock.Calls(), []int{3, 4}) || len(mock.Results()) != 2 || mock.Results()[0].Value != 999 || mock.Results()[1].Value != 400 {
		t.Fatal("old result leaked into new history", mock.Calls(), mock.Results())
	}
	if oldCalls[0] != 1 || oldResults[0].Status != "incomplete" {
		t.Fatal("old snapshots mutated")
	}
	// An active action also finishes its captured implementation after reset.
	actionStarted, actionRelease, actionDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	action := NewMockAction(func(int) { close(actionStarted); <-actionRelease })
	go func() { defer close(actionDone); action.Invoke(1) }()
	<-actionStarted
	action.Reset(func(int) {})
	action.Invoke(2)
	close(actionRelease)
	<-actionDone
	if !reflect.DeepEqual(action.Calls(), []int{2}) || len(action.Results()) != 1 || action.Results()[0].Status != "return" {
		t.Fatal(action.Calls(), action.Results())
	}
}

func TestMockResetConcurrentMutationAndSnapshots(t *testing.T) {
	mock := NewMockFn(func(value int) int { return value })
	var group sync.WaitGroup
	for worker := 0; worker < 6; worker++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for iteration := 0; iteration < 100; iteration++ {
				if iteration%7 == 0 {
					mock.Reset(func(value int) int { return value + 1 })
				}
				if iteration%11 == 0 {
					mock.ReturnValueOnce(42)
				}
				mock.Invoke(iteration)
				mock.Calls()
				mock.Results()
				mock.Outcomes()
			}
		}()
	}
	group.Wait()
	calls, results, outcomes := mock.Calls(), mock.Results(), mock.Outcomes()
	if len(calls) != len(results) || len(results) != len(outcomes) {
		t.Fatal("history/outcome alignment", len(calls), len(results), len(outcomes))
	}
	for _, result := range results {
		if result.Status != "return" {
			t.Fatal("completed invocation left pending", result)
		}
	}
}

func TestMockResetRetainsNativeErrorsSkipsAndCleanup(t *testing.T) {
	cause := errors.New("returned error")
	mock := NewMockFn(func(value int) error { return nil })
	mock.Reset(func(value int) error { return cause })
	if mock.Invoke(42) != cause || mock.Results()[0].Status != "return" || mock.Results()[0].Exception != nil {
		t.Fatal("returned error became a throw")
	}
	for _, skip := range []bool{false, true} {
		suite := NewSuite("reset behavior")
		cleaned, after := false, false
		action := NewMockAction(func(int) { t.Fatal("old implementation") })
		action.ImplementOnce(func(int) { t.Fatal("discarded queued implementation") })
		action.Reset(func(int) {
			if skip {
				skipTest("reset resource unavailable")
			}
			panic(__kinmokuseiThrown{err: cause})
		})
		suite.AfterEach(func() { after = true })
		suite.TestWithContext("row", func(context *TestContext) { context.Cleanup(func() { cleaned = true }); action.Invoke(42) })
		result := suite.RunTo(&bytes.Buffer{})
		if !cleaned || !after || action.Results()[0].Status != "throw" {
			t.Fatal(result, action.Results())
		}
		if skip {
			if result.Skipped != 1 || result.Failed != 0 {
				t.Fatal(result)
			}
		} else if result.Failed != 1 || action.Results()[0].Exception != cause {
			t.Fatal(result)
		}
	}
}
