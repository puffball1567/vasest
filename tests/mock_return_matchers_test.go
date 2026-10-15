package vasest

import (
	"bytes"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"testing"
)

type matcherReturnSource[R any] struct{ snapshot func() []MockResult[R] }

func (source *matcherReturnSource[R]) Results() []MockResult[R] { return source.snapshot() }

func TestMockReturnMatchersPreserveTypedValuesAndLiveHistory(t *testing.T) {
	type ID int64
	calls := 0
	mock := NewMockFn[int, ID](func(value int) ID { calls++; return ID(value) })
	mock.ReturnValueOnce(ID(1<<60 + 1))
	matcher := expectMockReturn[ID](mock)
	matcher.Not.ToHaveReturned()
	matcher.ToHaveReturnedTimes(0)
	matcher.Not.ToHaveReturnedWith(0)
	matcher.Not.ToHaveNthReturnedWith(1, 0)
	matcher.Not.ToHaveLastReturnedWith(0)
	if calls != 0 || mock.Invoke(42) != ID(1<<60+1) {
		t.Fatal("matcher consumed queued behavior")
	}
	matcher.ToHaveReturned()
	matcher.ToHaveReturnedTimes(1)
	matcher.ToHaveReturnedWith(ID(1<<60 + 1))
	matcher.ToHaveNthReturnedWith(1, ID(1<<60+1))
	matcher.ToHaveLastReturnedWith(ID(1<<60 + 1))
	mock.Invoke(43)
	matcher.Not.ToHaveReturnedTimes(1)
	matcher.Not.ToHaveReturnedWith(44)
	matcher.Not.ToHaveNthReturnedWith(1, 43)
	matcher.Not.ToHaveNthReturnedWith(3, 0)
	matcher.Not.ToHaveLastReturnedWith(ID(1<<60 + 1))
	matcher.ToHaveLastReturnedWith(43)
	NewMockReturnExpectation[ID](mock).ToHaveReturnedTimes(2)
	mock.ClearCalls()
	matcher.Not.ToHaveReturned()
	matcher.ToHaveReturnedTimes(0)
	if calls != 1 {
		t.Fatal("matcher invoked implementation", calls)
	}
	slices := NewMockFn[int, []int](func(int) []int { return nil })
	slices.Invoke(1)
	expectMockReturn[[]int](slices).ToHaveReturnedWith(nil)
	expectMockReturn[[]int](slices).Not.ToHaveReturnedWith([]int{})
	type record struct{ IDs []int }
	object := &record{[]int{42}}
	objects := NewMockFn[int, *record](func(int) *record { return object })
	objects.Invoke(1)
	expectMockReturn[*record](objects).ToHaveLastReturnedWith(&record{[]int{42}})
	object.IDs[0] = 43
	expectMockReturn[*record](objects).ToHaveReturnedWith(&record{[]int{43}})
	interfaces := NewMockFn[int, any](func(int) any { return int32(42) })
	interfaces.Invoke(1)
	expectMockReturn[any](interfaces).Not.ToHaveReturnedWith(int64(42))
}

func TestMockReturnMatchersDistinguishReturnFromThrowAndPanic(t *testing.T) {
	mock := NewMockFn[int, int](func(value int) int { return value })
	matcher := expectMockReturn[int](mock)
	mock.ImplementOnce(func(int) int { panic(__kinmokuseiThrown{err: errors.New("return mock throw")}) })
	assertThrownMessage(t, "return mock throw", func() { mock.Invoke(1) })
	matcher.Not.ToHaveReturned()
	matcher.ToHaveReturnedTimes(0)
	matcher.Not.ToHaveReturnedWith(0)
	matcher.Not.ToHaveNthReturnedWith(1, 0)
	matcher.Not.ToHaveLastReturnedWith(0)
	assertThrownMessage(t, `status "throw"`, func() { matcher.ToHaveNthReturnedWith(1, 0) })
	mock.Invoke(0)
	matcher.ToHaveReturnedWith(0)
	matcher.ToHaveNthReturnedWith(2, 0)
	mock.ImplementOnce(func(int) int { panic("return mock panic") })
	func() {
		defer func() {
			if recover() != "return mock panic" {
				t.Fatal("panic changed")
			}
		}()
		mock.Invoke(3)
	}()
	matcher.ToHaveReturnedTimes(1)
	matcher.Not.ToHaveLastReturnedWith(0)
	assertThrownMessage(t, `status "panic"`, func() { matcher.ToHaveLastReturnedWith(0) })
	assertThrownMessage(t, `status "panic"`, func() { matcher.ToHaveNthReturnedWith(3, 0) })
}

func TestMockReturnMatchersTrackInvocationOrderAndClearedPendingResults(t *testing.T) {
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	mock := NewMockFn[int, int](func(value int) int {
		if value == 1 {
			close(entered)
			<-release
		}
		return value
	})
	matcher := expectMockReturn[int](mock)
	go func() { defer close(done); mock.Invoke(1) }()
	<-entered
	matcher.Not.ToHaveReturned()
	matcher.Not.ToHaveReturnedWith(0)
	matcher.Not.ToHaveLastReturnedWith(0)
	assertThrownMessage(t, `status "incomplete"`, func() { matcher.ToHaveNthReturnedWith(1, 0) })
	mock.Invoke(2)
	matcher.ToHaveReturnedTimes(1)
	matcher.ToHaveLastReturnedWith(2)
	close(release)
	<-done
	matcher.ToHaveReturnedTimes(2)
	matcher.ToHaveNthReturnedWith(1, 1)
	matcher.ToHaveLastReturnedWith(2)
	entered, release, done = make(chan struct{}), make(chan struct{}), make(chan struct{})
	mock.Implement(func(value int) int { close(entered); <-release; return value })
	go func() { defer close(done); mock.Invoke(3) }()
	<-entered
	mock.ClearCalls()
	matcher.Not.ToHaveReturned()
	close(release)
	<-done
	matcher.ToHaveReturnedTimes(0)
	matcher.Not.ToHaveLastReturnedWith(3)
}

func TestMockReturnMatchersValidateArgumentsAndReadOneSnapshot(t *testing.T) {
	reads := 0
	var results []MockResult[int]
	source := &matcherReturnSource[int]{func() []MockResult[int] { reads++; return results }}
	matcher := expectMockReturn[int](source)
	checks := []func(){matcher.ToHaveReturned, func() { matcher.ToHaveReturnedTimes(1) },
		func() { matcher.ToHaveReturnedWith(42) }, func() { matcher.ToHaveNthReturnedWith(1, 42) }, func() { matcher.ToHaveLastReturnedWith(42) },
		matcher.Not.ToHaveReturned, func() { matcher.Not.ToHaveReturnedTimes(1) }, func() { matcher.Not.ToHaveReturnedWith(42) },
		func() { matcher.Not.ToHaveNthReturnedWith(1, 42) }, func() { matcher.Not.ToHaveLastReturnedWith(42) }}
	for i, check := range checks {
		reads, results = 0, nil
		if i < 5 {
			results = []MockResult[int]{{Status: "return", Value: 42}}
		}
		check()
		if reads != 1 {
			t.Fatal("snapshot read count", reads)
		}
	}
	reads = 0
	for _, attempt := range []func(){func() { matcher.ToHaveReturnedTimes(-1) }, func() { matcher.Not.ToHaveReturnedTimes(-1) },
		func() { matcher.ToHaveNthReturnedWith(0, 42) }, func() { matcher.Not.ToHaveNthReturnedWith(-1, 42) }} {
		assertThrownMessage(t, "must", attempt)
	}
	if reads != 0 {
		t.Fatal("invalid arguments read source", reads)
	}
	for _, source := range []MockReturnSource[int]{nil, (*MockFn[int, int])(nil)} {
		invalid := expectMockReturn[int](source)
		for _, attempt := range []func(){invalid.ToHaveReturned, invalid.Not.ToHaveReturned,
			func() { invalid.ToHaveReturnedTimes(0) }, func() { invalid.Not.ToHaveReturnedTimes(1) },
			func() { invalid.ToHaveReturnedWith(42) }, func() { invalid.Not.ToHaveReturnedWith(42) },
			func() { invalid.ToHaveNthReturnedWith(1, 42) }, func() { invalid.Not.ToHaveNthReturnedWith(1, 42) },
			func() { invalid.ToHaveLastReturnedWith(42) }, func() { invalid.Not.ToHaveLastReturnedWith(42) }} {
			assertThrownMessage(t, "return source must not be nil", attempt)
		}
	}
}

func TestMockReturnMatchersReportMismatches(t *testing.T) {
	mock := NewMockFn[int, int](func(value int) int { return value })
	matcher := expectMockReturn[int](mock)
	assertThrownMessage(t, "got no returns", matcher.ToHaveReturned)
	assertThrownMessage(t, "got 0 call(s)", func() { matcher.ToHaveLastReturnedWith(42) })
	mock.Invoke(42)
	checks := []struct {
		want string
		body func()
	}{
		{"not to have returned", matcher.Not.ToHaveReturned},
		{"returned 2 time(s), got 1", func() { matcher.ToHaveReturnedTimes(2) }},
		{"not to have returned 1 time(s)", func() { matcher.Not.ToHaveReturnedTimes(1) }},
		{"to have returned 43", func() { matcher.ToHaveReturnedWith(43) }},
		{"not to have returned 42", func() { matcher.Not.ToHaveReturnedWith(42) }},
		{"call 1 to return 43, got 42", func() { matcher.ToHaveNthReturnedWith(1, 43) }},
		{"call 2 to return 42, got 1 call(s)", func() { matcher.ToHaveNthReturnedWith(2, 42) }},
		{"call 1 not to have returned 42", func() { matcher.Not.ToHaveNthReturnedWith(1, 42) }},
		{"last call to return 43, got 42", func() { matcher.ToHaveLastReturnedWith(43) }},
		{"last call not to have returned 42", func() { matcher.Not.ToHaveLastReturnedWith(42) }},
	}
	for _, check := range checks {
		assertThrownMessage(t, check.want, check.body)
	}
}

func TestMockReturnMatchersPreserveSourceErrorsLocationsAndPanicCleanup(t *testing.T) {
	sentinel := errors.New("return source failed")
	for _, negated := range []bool{false, true} {
		func() {
			defer func() {
				thrown, ok := recover().(__kinmokuseiThrown)
				if !ok || thrown.err != sentinel {
					t.Fatal("source throw changed", thrown)
				}
			}()
			source := &matcherReturnSource[int]{func() []MockResult[int] { panic(__kinmokuseiThrown{err: sentinel}) }}
			if negated {
				expectMockReturn[int](source).Not.ToHaveReturned()
			} else {
				expectMockReturn[int](source).ToHaveReturned()
			}
		}()
		for _, native := range []bool{false, true} {
			func() {
				var events []string
				if native {
					defer func() {
						if recovered := recover(); recovered != "return source panic" || fmt.Sprint(events) != "[afterEach cleanup afterAll]" {
							t.Fatal(recovered, events)
						}
					}()
				}
				suite := NewSuite("return source cleanup")
				suite.AfterEach(func() { events = append(events, "afterEach") })
				suite.AfterAll(func() { events = append(events, "afterAll") })
				suite.TestWithContext("case", func(context *TestContext) {
					context.Cleanup(func() { events = append(events, "cleanup") })
					source := &matcherReturnSource[int]{func() []MockResult[int] {
						if native {
							panic("return source panic")
						}
						withTestLocation("return-spy.km", 42, func() { panic(__kinmokuseiThrown{err: sentinel}) })
						return nil
					}}
					if negated {
						expectMockReturn[int](source).Not.ToHaveReturned()
					} else {
						expectMockReturn[int](source).ToHaveReturned()
					}
				})
				summary := suite.RunTo(&bytes.Buffer{})
				if native {
					t.Fatal("native panic swallowed")
				}
				if summary.Failed != 1 || summary.Results[0].Failures[0].Location != (TestLocation{File: "return-spy.km", Line: 42, Source: "explicit"}) || fmt.Sprint(events) != "[afterEach cleanup afterAll]" {
					t.Fatal(summary, events)
				}
			}()
		}
	}
}

func TestMockReturnMatchersConcurrentInvocationsAndClear(t *testing.T) {
	mock := NewMockFn[int, int](func(value int) int { return value })
	matcher := expectMockReturn[int](mock)
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
		matcher.Not.ToHaveReturnedWith(-1)
		matcher.Not.ToHaveNthReturnedWith(1, -1)
		matcher.Not.ToHaveLastReturnedWith(-1)
		matcher.Not.ToHaveReturnedTimes(201)
	}
	group.Wait()
}

func TestMockReturnMatchersIdentifyActualCaller(t *testing.T) {
	mock := NewMockFn[int, int](func(value int) int { return value })
	mock.Invoke(42)
	matcher := expectMockReturn[int](mock)
	empty := expectMockReturn[int](NewMockFn[int, int](func(value int) int { return value }))
	var want TestLocation
	record := func() {
		_, file, line, _ := runtime.Caller(1)
		want = TestLocation{File: file, Line: line, Source: "go"}
	}
	checks := []func(){
		func() { record(); empty.ToHaveReturned() },
		func() { record(); matcher.ToHaveReturnedTimes(2) },
		func() { record(); matcher.ToHaveReturnedWith(43) },
		func() { record(); matcher.ToHaveNthReturnedWith(1, 43) },
		func() { record(); matcher.ToHaveLastReturnedWith(43) },
		func() { record(); matcher.Not.ToHaveReturned() },
		func() { record(); matcher.Not.ToHaveReturnedTimes(1) },
		func() { record(); matcher.Not.ToHaveReturnedWith(42) },
		func() { record(); matcher.Not.ToHaveNthReturnedWith(1, 42) },
		func() { record(); matcher.Not.ToHaveLastReturnedWith(42) },
		func() { record(); matcher.ToHaveReturnedTimes(-1) },
		func() { record(); matcher.Not.ToHaveReturnedTimes(-1) },
		func() { record(); matcher.ToHaveNthReturnedWith(0, 42) },
		func() { record(); matcher.Not.ToHaveNthReturnedWith(0, 42) },
		func() { record(); expectMockReturn[int](nil).ToHaveReturned() },
		func() { record(); expectMockReturn[int]((*MockFn[int, int])(nil)).Not.ToHaveReturned() },
	}
	for _, check := range checks {
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
