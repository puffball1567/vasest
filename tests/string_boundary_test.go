package vasest

import (
	"bytes"
	"fmt"
	"runtime"
	"strings"
	"testing"
)

func TestStringBoundaryLiteralByteSemantics(t *testing.T) {
	for _, row := range []struct {
		actual, fragment string
		prefix, suffix   bool
	}{
		{"abc", "a", true, false}, {"abc", "c", false, true}, {"abc", "b", false, false},
		{"abc", "abc", true, true}, {"abc", "abcd", false, false},
		{"abc", "", true, true}, {"", "", true, true}, {"", "x", false, false},
		{"[a]+.json", "[a]+", true, false}, {"[a]+.json", ".json", false, true},
		{"金木犀🌼", "金", true, false}, {"金木犀🌼", "🌼", false, true},
		{"e\u0301", "é", false, false}, {"é", "e\u0301", false, false},
		{"PrefixSuffix", "prefix", false, false},
		{"\x00middle\x00", "\x00", true, true},
		{"\xfftext\xfe", "\xff", true, false}, {"\xfftext\xfe", "\xfe", false, true},
		// Native strings compare bytes, including an incomplete UTF-8 fragment.
		{"金木犀", "\xe9", true, false}, {"金木犀", "\x80", false, true},
	} {
		t.Run(fmt.Sprintf("%q/%q", row.actual, row.fragment), func(t *testing.T) {
			matcher := expectString(row.actual)
			for _, check := range []struct {
				positive, negative func(string)
				matches            bool
				relation           string
			}{
				{matcher.ToStartWith, matcher.Not.ToStartWith, row.prefix, "start with"},
				{matcher.ToEndWith, matcher.Not.ToEndWith, row.suffix, "end with"},
			} {
				if check.matches {
					check.positive(row.fragment)
					assertThrownMessage(t, "not to "+check.relation, func() { check.negative(row.fragment) })
				} else {
					check.negative(row.fragment)
					assertThrownMessage(t, "to "+check.relation, func() { check.positive(row.fragment) })
				}
			}
		})
	}
}

func TestStringBoundaryPreserveCapturedStringsAndBoundMethods(t *testing.T) {
	actual := "service/result.json"
	matcher := NewStringExpectation(actual)
	var prefix func(string) = matcher.ToStartWith
	var suffix func(string) = matcher.Not.ToEndWith
	actual = "replacement.txt"
	prefix("service/")
	suffix(".txt")
	matcher.ToEndWith(".json")
	NewNegatedStringExpectation("service/result.json").ToStartWith("other/")
	NewNegatedStringExpectation("service/result.json").ToEndWith(".txt")
	if actual != "replacement.txt" {
		t.Fatal("matcher changed caller binding", actual)
	}
}

func TestStringBoundaryIdentifyEveryCallerAndExplicitLocation(t *testing.T) {
	var want TestLocation
	record := func() {
		_, file, line, _ := runtime.Caller(1)
		want = TestLocation{File: file, Line: line + 1, Source: "go"}
	}
	for _, check := range []func(){
		func() {
			record()
			expectString("prefix-suffix").ToStartWith("missing")
		},
		func() {
			record()
			expectString("prefix-suffix").Not.ToStartWith("prefix")
		},
		func() {
			record()
			expectString("prefix-suffix").ToEndWith("missing")
		},
		func() {
			record()
			expectString("prefix-suffix").Not.ToEndWith("suffix")
		},
	} {
		caught := expectThrows(check)
		if caught == nil || failureLocation(caught) != want {
			t.Fatal(caught, want)
		}
	}
	caught := expectThrows(func() {
		withTestLocation("domain-check.km", 42, func() { expectString("value").ToEndWith("suffix") })
	})
	if caught == nil || failureLocation(caught) != (TestLocation{File: "domain-check.km", Line: 42, Source: "explicit"}) {
		t.Fatal(caught)
	}
}

func TestStringBoundaryFailuresQuoteValuesAndContinueCleanup(t *testing.T) {
	var events []string
	suite := NewSuite("literal boundaries")
	suite.AfterEach(func() { events = append(events, "afterEach") })
	suite.AfterAll(func() { events = append(events, "afterAll") })
	suite.TestWithContext("mismatch", func(context *TestContext) {
		context.Cleanup(func() { events = append(events, "cleanup") })
		expectString("first\n\x00\xff").ToStartWith("last\n")
	})
	suite.Test("later", func() { events = append(events, "later") })
	result := suite.RunTo(&bytes.Buffer{})
	if result.Failed != 1 || result.Passed != 1 || fmt.Sprint(events) != "[afterEach cleanup later afterEach afterAll]" {
		t.Fatal(result, events)
	}
	failure := result.Results[0].Failures[0]
	if failure.Phase != "body" || failure.Location.Source != "go" || failure.Message != `expected "first\n\x00\xff" to start with "last\n"` || strings.ContainsRune(failure.Message, '\x00') {
		t.Fatal(failure)
	}
}
