package vasest

import (
	"bytes"
	"fmt"
	"math"
	"runtime"
	"testing"
)

func TestFloatMatchersPreserveWidthsAndNominalTypes(t *testing.T) {
	type Distance float32
	expectFloat[Distance](Distance(1.125)).ToBeCloseTo(Distance(1), Distance(0.125))
	NewFloatExpectation[Distance](Distance(-1.125)).ToBeCloseTo(Distance(-1), Distance(0.125))
	expectFloat(Distance(1.5)).Not.ToBeCloseTo(Distance(1), Distance(0.125))
	// Widening these operands before subtraction would change the result.
	actual, expected, tolerance := float32(1), float32(0.1), float32(0.9)
	if float64(actual)-float64(expected) <= float64(tolerance) {
		t.Fatal("test no longer distinguishes native float32 rounding")
	}
	expectFloat(actual).ToBeCloseTo(expected, tolerance)
	assertThrownMessage(t, "not to be within", func() { expectFloat(actual).Not.ToBeCloseTo(expected, tolerance) })
	expectFloat(1.125).ToBeCloseTo(1, 0.125)
	expectFloat(-1.125).ToBeCloseTo(-1, 0.125)
	expectFloat(math.Nextafter(1.125, math.Inf(1))).Not.ToBeCloseTo(1, 0.125)
	expectFloat(0.0).ToBeCloseTo(math.Copysign(0, -1), 0)
	expectFloat(math.Copysign(0, -1)).ToBeCloseTo(0, math.Copysign(0, -1))
	expectFloat(math.SmallestNonzeroFloat64).ToBeCloseTo(0, math.SmallestNonzeroFloat64)
	expectFloat(float32(math.SmallestNonzeroFloat32)).ToBeCloseTo(0, float32(math.SmallestNonzeroFloat32))
	expectFloat(math.MaxFloat64).Not.ToBeCloseTo(-math.MaxFloat64, math.MaxFloat64)
	expectFloat(float32(math.MaxFloat32)).Not.ToBeCloseTo(-float32(math.MaxFloat32), float32(math.MaxFloat32))
	value := 1.0
	matcher := expectFloat(value)
	value = math.NaN()
	matcher.ToBeFinite()
	matcher.Not.ToBeNaN()
	matcher.ToBeCloseTo(1, 0)
	if !math.IsNaN(value) {
		t.Fatal("local value changed")
	}
}

func TestFloatMatchersClassifySpecialValues(t *testing.T) {
	for _, value := range []float64{0, math.Copysign(0, -1), math.SmallestNonzeroFloat64, math.MaxFloat64, -math.MaxFloat64} {
		expectFloat(value).ToBeFinite()
		expectFloat(value).Not.ToBeNaN()
		assertThrownMessage(t, "not to be finite", func() { expectFloat(value).Not.ToBeFinite() })
		assertThrownMessage(t, "to be NaN", func() { expectFloat(value).ToBeNaN() })
	}
	for _, value := range []float64{math.Inf(-1), math.Inf(1)} {
		expectFloat(value).Not.ToBeFinite()
		expectFloat(value).Not.ToBeNaN()
		assertThrownMessage(t, "to be finite", func() { expectFloat(value).ToBeFinite() })
	}
	for _, value := range []float64{math.NaN(), math.Float64frombits(0x7ff0000000000001), math.Float64frombits(0xfff8000000000042)} {
		expectFloat(value).ToBeNaN()
		expectFloat(value).Not.ToBeFinite()
		assertThrownMessage(t, "not to be NaN", func() { expectFloat(value).Not.ToBeNaN() })
	}
	type Reading float32
	for _, value := range []Reading{0, Reading(math.SmallestNonzeroFloat32), Reading(math.MaxFloat32)} {
		NewFloatExpectation[Reading](value).ToBeFinite()
		expectFloat(value).Not.ToBeNaN()
	}
	expectFloat(Reading(math.NaN())).ToBeNaN()
	expectFloat(Reading(math.NaN())).Not.ToBeFinite()
	expectFloat(Reading(math.Inf(1))).Not.ToBeFinite()
	expectFloat(Reading(math.Inf(-1))).Not.ToBeNaN()
}

func TestFloatMatchersRejectInvalidInputsEvenWhenNegated(t *testing.T) {
	for _, negated := range []bool{false, true} {
		check := func(actual, expected, tolerance float64) {
			matcher := expectFloat(actual)
			if negated {
				matcher.Not.ToBeCloseTo(expected, tolerance)
			} else {
				matcher.ToBeCloseTo(expected, tolerance)
			}
		}
		for _, tolerance := range []float64{-math.SmallestNonzeroFloat64, -1, math.NaN(), math.Inf(-1), math.Inf(1)} {
			assertThrownMessage(t, "tolerance must be finite and non-negative", func() { check(1, 1, tolerance) })
		}
		for _, value := range []float64{math.NaN(), math.Inf(-1), math.Inf(1)} {
			assertThrownMessage(t, "requires finite values", func() { check(value, 1, 0) })
			assertThrownMessage(t, "requires finite values", func() { check(1, value, 0) })
		}
		check32 := func(actual, expected, tolerance float32) {
			matcher := expectFloat(actual)
			if negated {
				matcher.Not.ToBeCloseTo(expected, tolerance)
			} else {
				matcher.ToBeCloseTo(expected, tolerance)
			}
		}
		for _, tolerance := range []float32{-1, float32(math.NaN()), float32(math.Inf(1))} {
			assertThrownMessage(t, "tolerance must", func() { check32(1, 1, tolerance) })
		}
		assertThrownMessage(t, "requires finite values", func() { check32(float32(math.Inf(1)), 1, 0) })
		assertThrownMessage(t, "requires finite values", func() { check32(1, float32(math.NaN()), 0) })
	}
	assertThrownMessage(t, "expected 1.5 to be within 0.125 of 1 (difference 0.5)", func() { expectFloat(1.5).ToBeCloseTo(1, 0.125) })
	assertThrownMessage(t, "expected -1.125 not to be within 0.125 of -1 (difference 0.125)", func() { expectFloat(-1.125).Not.ToBeCloseTo(-1, 0.125) })
}

func TestFloatMatchersRetainCallerLocationsAndCleanup(t *testing.T) {
	matcher, nan := expectFloat(1.5), expectFloat(math.NaN())
	var want TestLocation
	record := func() {
		_, file, line, _ := runtime.Caller(1)
		want = TestLocation{File: file, Line: line, Source: "go"}
	}
	for _, check := range []func(){
		func() { record(); matcher.ToBeCloseTo(1, 0.125) }, func() { record(); matcher.Not.ToBeCloseTo(1, 0.5) },
		func() { record(); matcher.ToBeNaN() }, func() { record(); nan.Not.ToBeNaN() },
		func() { record(); nan.ToBeFinite() }, func() { record(); matcher.Not.ToBeFinite() },
		func() { record(); matcher.ToBeCloseTo(1, -1) }, func() { record(); matcher.Not.ToBeCloseTo(1, math.NaN()) },
		func() { record(); nan.ToBeCloseTo(1, 0) }, func() { record(); nan.Not.ToBeCloseTo(1, 0) },
	} {
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
	var events []string
	suite := NewSuite("float cleanup")
	suite.AfterEach(func() { events = append(events, "afterEach") })
	suite.AfterAll(func() { events = append(events, "afterAll") })
	suite.TestWithContext("mismatch", func(context *TestContext) {
		context.Cleanup(func() { events = append(events, "cleanup") })
		matcher.ToBeCloseTo(1, 0.125)
	})
	suite.Test("later", func() { events = append(events, "later") })
	summary := suite.RunTo(&bytes.Buffer{})
	if summary.Failed != 1 || summary.Passed != 1 || fmt.Sprint(events) != "[afterEach cleanup later afterEach afterAll]" || summary.Results[0].Failures[0].Phase != "body" {
		t.Fatal(summary, events)
	}
}
