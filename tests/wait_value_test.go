package vasest

import (
	"bytes"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestWaitForValuePreservesTypedResultsAndAttempts(t *testing.T) {
	type ID int64
	reads, checks := 0, 0
	want := ID(1<<60 + 1)
	got := waitForValue[ID](func() ID { reads++; return ID(reads) }, func(value ID) bool { checks++; return value == 3 }, time.Second, time.Millisecond)
	if got != 3 || reads != 3 || checks != 3 {
		t.Fatal(got, reads, checks)
	}
	if got := waitForValue(func() ID { return want }, func(value ID) bool { return value == want }, time.Second, time.Millisecond); got != want {
		t.Fatal("native integer precision changed", got)
	}
	type record struct{ Value int }
	object := &record{42}
	if result := waitForValue(func() *record { return object }, func(value *record) bool { value.Value++; return value == object }, time.Second, time.Millisecond); result != object || result.Value != 43 {
		t.Fatal("reference semantics changed")
	}
	for _, values := range [][]int{nil, {}, {1, 2}} {
		got := waitForValue[[]int](func() []int { return values }, func(value []int) bool { return (value == nil) == (values == nil) && len(value) == len(values) }, time.Second, time.Millisecond)
		if (got == nil) != (values == nil) || len(got) != len(values) {
			t.Fatal("nil/empty result changed")
		}
		if len(got) > 0 {
			got[0] = 42
			if values[0] != 42 {
				t.Fatal("slice backing storage copied")
			}
		}
	}
	values := map[string]int{"native": 42}
	gotMap := waitForValue(func() map[string]int { return values }, func(value map[string]int) bool { value["native"]++; return true }, time.Second, time.Millisecond)
	if gotMap["native"] != 43 || values["native"] != 43 {
		t.Fatal("map reference changed")
	}
	reads, checks = 0, 0
	transient := errors.New("transient")
	gotInt := waitForValue(func() int {
		reads++
		if reads == 1 {
			panic(__kinmokuseiThrown{err: transient})
		}
		return reads
	}, func(value int) bool {
		checks++
		if value == 3 {
			panic(__kinmokuseiThrown{err: transient})
		}
		return value == 4
	}, time.Second, time.Millisecond)
	if gotInt != 4 || reads != 4 || checks != 3 {
		t.Fatal("read throw, false result and predicate throw were not retried once each", gotInt, reads, checks)
	}
	nilThrows := 0
	waitForValue(func() int {
		nilThrows++
		if nilThrows == 1 {
			panic(__kinmokuseiThrown{err: nil})
		}
		return 42
	}, func(int) bool { return true }, time.Second, time.Millisecond)
	if nilThrows != 2 {
		t.Fatal("nil throw was not retried")
	}
}

func TestWaitForValueValidatesBeforeCallbacksAndReportsCaller(t *testing.T) {
	reads, checks := 0, 0
	read := func() int { reads++; return 42 }
	predicate := func(int) bool { checks++; return true }
	var want TestLocation
	record := func() {
		_, file, line, _ := runtime.Caller(1)
		want = TestLocation{File: file, Line: line, Source: "go"}
	}
	for _, check := range []func(){
		func() { record(); waitForValue[int](nil, predicate, time.Second, time.Millisecond) },
		func() { record(); waitForValue(read, nil, time.Second, time.Millisecond) },
		func() { record(); waitForValue(read, predicate, 0, time.Millisecond) },
		func() { record(); waitForValue(read, predicate, -time.Second, time.Millisecond) },
		func() { record(); waitForValue(read, predicate, time.Second, 0) },
		func() { record(); waitForValue(read, predicate, time.Second, -time.Millisecond) },
	} {
		func() {
			defer func() {
				thrown, ok := recover().(__kinmokuseiThrown)
				if !ok || !strings.Contains(thrown.err.Error(), "waitForValue") || failureLocation(thrown.err) != want {
					t.Fatal(thrown, want)
				}
			}()
			check()
		}()
	}
	if reads != 0 || checks != 0 {
		t.Fatal("invalid arguments executed callbacks")
	}
}

func TestWaitForValueTimeoutKeepsLastErrorAndLocation(t *testing.T) {
	for _, fromRead := range []bool{false, true} {
		for _, located := range []bool{false, true} {
			reads, checks := 0, 0
			cause := errors.New("last callback failure")
			var last error = cause
			want := TestLocation{File: "domain.km", Line: 42, Source: "explicit"}
			if located {
				last = NewFailureError(cause, want)
			}
			read := func() int {
				reads++
				if fromRead {
					time.Sleep(3 * time.Millisecond)
					panic(__kinmokuseiThrown{err: last})
				}
				return 42
			}
			predicate := func(int) bool { checks++; time.Sleep(3 * time.Millisecond); panic(__kinmokuseiThrown{err: last}) }
			_, file, line, _ := runtime.Caller(0)
			err := expectThrows(func() { waitForValue(read, predicate, time.Millisecond, time.Hour) })
			if !located {
				want = TestLocation{File: file, Line: line + 1, Source: "go"}
			}
			if !errors.Is(err, cause) || failureLocation(err) != want || !strings.Contains(err.Error(), "waitForValue timed out after 1ms (1 attempts)") || reads != 1 || checks != map[bool]int{false: 1, true: 0}[fromRead] {
				t.Fatal("last error or location changed", err, failureLocation(err), want, reads, checks)
			}
		}
	}
	var want TestLocation
	read := func() int { time.Sleep(3 * time.Millisecond); return 42 }
	_, file, line, _ := runtime.Caller(0)
	err := expectThrows(func() { waitForValue(read, func(int) bool { return false }, time.Millisecond, time.Hour) })
	want = TestLocation{File: file, Line: line + 1, Source: "go"}
	if failureLocation(err) != want || !strings.Contains(err.Error(), "predicate returned false") {
		t.Fatal(err, failureLocation(err), want)
	}
	// A long interval is clipped to the remaining timeout, rather than slept in full.
	started := time.Now()
	assertThrownMessage(t, "timed out", func() {
		waitForValue(func() int { return 0 }, func(int) bool { return false }, 3*time.Millisecond, time.Hour)
	})
	if time.Since(started) > time.Second {
		t.Fatal("interval exceeded remaining timeout")
	}
	reads := 0
	err = expectThrows(func() {
		waitForValue(func() int {
			reads++
			if reads == 1 {
				panic(__kinmokuseiThrown{err: errors.New("obsolete error")})
			}
			time.Sleep(110 * time.Millisecond)
			panic(__kinmokuseiThrown{err: errors.New("latest error")})
		}, func(int) bool { t.Fatal("predicate invoked after read throw"); return true }, 100*time.Millisecond, time.Nanosecond)
	})
	if reads < 2 || !strings.Contains(err.Error(), "latest error") || strings.Contains(err.Error(), "obsolete error") {
		t.Fatal("timeout retained an older error", err, reads)
	}
}

func TestWaitHelpersPropagateSkipIdentityImmediately(t *testing.T) {
	skip := expectThrows(func() { skipTest("resource unavailable") })
	for _, exception := range []error{skip, fmt.Errorf("wrapped: %w", skip), NewFailureError(skip, TestLocation{File: "skip.km", Line: 42, Source: "explicit"})} {
		for _, mode := range []string{"waitFor", "read", "predicate"} {
			func() {
				reads, checks := 0, 0
				defer func() {
					thrown, ok := recover().(__kinmokuseiThrown)
					wantChecks := 0
					if mode == "predicate" {
						wantChecks = 1
					}
					if !ok || thrown.err != exception || reads != 1 || checks != wantChecks {
						t.Fatal("skip was retried or wrapped", mode, thrown, reads, checks)
					}
				}()
				if mode == "waitFor" {
					waitFor(func() { reads++; panic(__kinmokuseiThrown{err: exception}) }, time.Second, time.Millisecond)
				} else {
					waitForValue(func() int {
						reads++
						if mode == "read" {
							panic(__kinmokuseiThrown{err: exception})
						}
						return 42
					}, func(int) bool { checks++; panic(__kinmokuseiThrown{err: exception}) }, time.Second, time.Millisecond)
				}
				t.Fatal("skip swallowed")
			}()
		}
	}
	joined := errors.Join(skip, errors.New("real failure"))
	reads := 0
	err := expectThrows(func() {
		waitForValue(func() int { reads++; time.Sleep(3 * time.Millisecond); panic(__kinmokuseiThrown{err: joined}) }, func(int) bool { return true }, time.Millisecond, time.Millisecond)
	})
	if testSkipReason(err) != "" || !errors.Is(err, joined) || reads != 1 || !strings.Contains(err.Error(), "timed out") {
		t.Fatal("joined failure was treated as skip", err)
	}
}

func TestWaitHelpersDoNotRetryAfterDeadline(t *testing.T) {
	for _, mode := range []string{"waitFor", "value"} {
		reads, checks := 0, 0
		assertThrownMessage(t, "timed out", func() {
			if mode == "waitFor" {
				waitFor(func() {
					reads++
					if reads == 1 {
						fail("first failure")
					}
				}, 5*time.Millisecond, time.Hour)
			} else {
				waitForValue(func() int { reads++; return reads }, func(value int) bool { checks++; return value > 1 }, 5*time.Millisecond, time.Hour)
			}
		})
		if reads != 1 || checks != map[string]int{"waitFor": 0, "value": 1}[mode] {
			t.Fatal("callback executed after the retry deadline", mode, reads, checks)
		}
	}
}

func TestWaitHelpersPreserveSkipAndPanicCleanup(t *testing.T) {
	for _, mode := range []string{"waitFor", "read", "predicate"} {
		for _, native := range []bool{false, true} {
			func() {
				calls := 0
				var events []string
				if native {
					defer func() {
						if recover() != "native wait panic" || calls != 1 || fmt.Sprint(events) != "[afterEach cleanup afterAll]" {
							t.Fatal("panic cleanup changed", events, calls)
						}
					}()
				}
				raise := func() {
					calls++
					if native {
						panic("native wait panic")
					}
					skipTest("wait unavailable")
				}
				suite := NewSuite("wait cleanup")
				suite.AfterEach(func() { events = append(events, "afterEach") })
				suite.AfterAll(func() { events = append(events, "afterAll") })
				suite.TestWithContext("waiting", func(context *TestContext) {
					context.Cleanup(func() { events = append(events, "cleanup") })
					switch mode {
					case "waitFor":
						waitFor(raise, time.Second, time.Millisecond)
					case "read":
						waitForValue(func() int { raise(); return 42 }, func(int) bool { t.Fatal("predicate after read skip"); return true }, time.Second, time.Millisecond)
					case "predicate":
						waitForValue(func() int { return 42 }, func(int) bool { raise(); return true }, time.Second, time.Millisecond)
					}
					t.Fatal("control flow swallowed")
				})
				suite.Test("later", func() { events = append(events, "later") })
				summary := suite.RunTo(&bytes.Buffer{})
				if native {
					t.Fatal("native panic swallowed")
				}
				if calls != 1 || summary.Skipped != 1 || summary.Passed != 1 || summary.Failed != 0 || summary.Results[0].Message != "wait unavailable" || len(summary.Results[0].Failures) != 0 || fmt.Sprint(events) != "[afterEach cleanup later afterEach afterAll]" {
					t.Fatal(summary, events, calls)
				}
			}()
		}
	}
}
