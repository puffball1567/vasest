package vasest

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

type inactiveID int64
type inactiveTable struct {
	suite           *Suite
	register        func([]inactiveID, func(inactiveID, int) string, func())
	registerNilBody func([]inactiveID, func(inactiveID, int) string)
}

func newInactiveTable(mode string, unexpected func()) inactiveTable {
	suite := NewSuite("inactive rows")
	var shared *FixtureSuite[string]
	if mode[:6] == "shared" {
		shared = NewFixtureSuite("inactive rows", func(*TestContext) string { unexpected(); return "fixture" })
		shared.BeforeAll(func(string) { unexpected() })
		shared.AfterAll(func(string) { unexpected() })
		shared.BeforeEachWithContext(func(string, *TestContext) { unexpected() })
		shared.AfterEachWithContext(func(string, *TestContext) { unexpected() })
		suite = shared.suite
	} else {
		suite.BeforeAll(unexpected)
		suite.AfterAll(unexpected)
		suite.BeforeEachWithContext(func(*TestContext) { unexpected() })
		suite.AfterEachWithContext(func(*TestContext) { unexpected() })
	}
	result := inactiveTable{suite: suite}
	switch mode {
	case "plain-skip":
		result.register = func(rows []inactiveID, name func(inactiveID, int) string, body func()) {
			SuiteSkipEach(suite, rows, func(value inactiveID) string { return name(value, -1) }, func(inactiveID) { body() })
		}
		result.registerNilBody = func(rows []inactiveID, name func(inactiveID, int) string) {
			SuiteSkipEach(suite, rows, func(value inactiveID) string { return name(value, -1) }, nil)
		}
	case "plain-todo":
		result.register = func(rows []inactiveID, name func(inactiveID, int) string, body func()) {
			SuiteTodoEach(suite, rows, func(value inactiveID) string { return name(value, -1) })
		}
	case "indexed-skip":
		result.register = func(rows []inactiveID, name func(inactiveID, int) string, body func()) {
			SuiteSkipEachIndexed(suite, rows, name, func(inactiveID, int) { body() })
		}
		result.registerNilBody = func(rows []inactiveID, name func(inactiveID, int) string) {
			SuiteSkipEachIndexed(suite, rows, name, nil)
		}
	case "indexed-todo":
		result.register = func(rows []inactiveID, name func(inactiveID, int) string, body func()) {
			SuiteTodoEachIndexed(suite, rows, name)
		}
	case "shared-skip":
		result.register = func(rows []inactiveID, name func(inactiveID, int) string, body func()) {
			FixtureSuiteSkipEach(shared, rows, func(value inactiveID) string { return name(value, -1) }, func(inactiveID, string, *TestContext) { body() })
		}
		result.registerNilBody = func(rows []inactiveID, name func(inactiveID, int) string) {
			FixtureSuiteSkipEach(shared, rows, func(value inactiveID) string { return name(value, -1) }, nil)
		}
	case "shared-todo":
		result.register = func(rows []inactiveID, name func(inactiveID, int) string, body func()) {
			FixtureSuiteTodoEach(shared, rows, func(value inactiveID) string { return name(value, -1) })
		}
	case "shared-indexed-skip":
		result.register = func(rows []inactiveID, name func(inactiveID, int) string, body func()) {
			FixtureSuiteSkipEachIndexed(shared, rows, name, func(inactiveID, int, string, *TestContext) { body() })
		}
		result.registerNilBody = func(rows []inactiveID, name func(inactiveID, int) string) {
			FixtureSuiteSkipEachIndexed(shared, rows, name, nil)
		}
	case "shared-indexed-todo":
		result.register = func(rows []inactiveID, name func(inactiveID, int) string, body func()) {
			FixtureSuiteTodoEachIndexed(shared, rows, name)
		}
	default:
		panic(mode)
	}
	return result
}

var inactiveModes = []string{"plain-skip", "plain-todo", "indexed-skip", "indexed-todo", "shared-skip", "shared-todo", "shared-indexed-skip", "shared-indexed-todo"}

func TestInactiveTablesStatusesAndLifecycle(t *testing.T) {
	for _, mode := range inactiveModes {
		t.Run(mode, func(t *testing.T) {
			unexpected := func() { t.Fatal("inactive row ran a body, hook or shared setup") }
			entry := newInactiveTable(mode, unexpected)
			base := inactiveID(1<<60 + 1)
			rows := []inactiveID{base, base + 1, base + 2}
			named := 0
			entry.register(rows, func(value inactiveID, index int) string {
				if value != base+inactiveID(named) || (index >= 0 && index != named) {
					t.Fatal("native value or index changed", value, index)
				}
				named++
				return fmt.Sprintf("row %d", named-1)
			}, unexpected)
			rows[0] = 0
			for run := 0; run < 2; run++ {
				result := entry.suite.RunTo(&bytes.Buffer{})
				if result.Total() != 0 || result.Failed != 0 || !result.Success() || named != 3 || len(result.Results) != 3 {
					t.Fatal(result, named)
				}
				want := "skip"
				if mode[len(mode)-4:] == "todo" {
					want = "todo"
					if result.Todo != 3 {
						t.Fatal(result)
					}
				} else if result.Skipped != 3 {
					t.Fatal(result)
				}
				for index, row := range result.Results {
					if row.Name != fmt.Sprintf("row %d", index) || row.Status != want || row.DurationNs != 0 || len(row.Failures) != 0 {
						t.Fatal(row)
					}
				}
			}
		})
	}
}

func TestInactiveTablesAtomicNaming(t *testing.T) {
	for _, mode := range inactiveModes {
		for _, problem := range []string{"blank", "duplicate", "existing", "throw", "panic", "reentrant", "reentrant-valid"} {
			t.Run(mode+"/"+problem, func(t *testing.T) {
				entry := newInactiveTable(mode, func() { t.Fatal("inactive callback ran") })
				entry.suite.Todo("existing")
				cause := errors.New("naming failed")
				calls := 0
				name := func(value inactiveID, index int) string {
					calls++
					if calls == 2 {
						switch problem {
						case "blank":
							return " "
						case "duplicate":
							return "row 0"
						case "existing":
							return "existing"
						case "throw":
							panic(__kinmokuseiThrown{err: cause})
						case "panic":
							panic(cause)
						case "reentrant":
							entry.suite.Todo("row 0")
						case "reentrant-valid":
							entry.suite.Todo("from callback")
						}
					}
					return fmt.Sprintf("row %d", calls-1)
				}
				var caught any
				func() {
					defer func() { caught = recover() }()
					entry.register([]inactiveID{42, 43, 44}, name, func() {})
				}()
				if problem == "reentrant-valid" {
					if caught != nil || len(entry.suite.cases) != 5 {
						t.Fatal(caught, entry.suite.cases)
					}
					return
				}
				if caught == nil {
					t.Fatal("invalid batch accepted")
				}
				if problem == "throw" {
					thrown, ok := caught.(__kinmokuseiThrown)
					if !ok || thrown.err != cause {
						t.Fatal("original throw changed", caught)
					}
				}
				if problem == "panic" && caught != cause {
					t.Fatal("native panic changed", caught)
				}
				want := []string{"existing"}
				if problem == "reentrant" {
					want = append(want, "row 0")
				}
				var actual []string
				for _, row := range entry.suite.cases {
					actual = append(actual, row.name)
				}
				if !reflect.DeepEqual(actual, want) {
					t.Fatal("partially committed batch", actual, want)
				}
			})
		}
	}
}

func TestInactiveTablesValidateBeforeNaming(t *testing.T) {
	for _, mode := range inactiveModes {
		t.Run(mode, func(t *testing.T) {
			entry := newInactiveTable(mode, func() { t.Fatal("inactive callback ran") })
			names := 0
			name := func(inactiveID, int) string { names++; return "row" }
			assertThrownMessage(t, modeMethod(mode)+" requires at least one value", func() { entry.register(nil, name, func() {}) })
			assertThrownMessage(t, modeMethod(mode)+" requires at least one value", func() { entry.register([]inactiveID{}, name, func() {}) })
			if entry.registerNilBody != nil {
				assertThrownMessage(t, modeMethod(mode)+" body must not be nil", func() { entry.registerNilBody([]inactiveID{42}, name) })
			}
			if names != 0 || len(entry.suite.cases) != 0 {
				t.Fatal(names, entry.suite.cases)
			}
		})
	}
	plain, shared := NewSuite("nil names"), NewFixtureSuite("nil names", func(*TestContext) string { return "" })
	for _, register := range []func(){
		func() { SuiteSkipEach[inactiveID](plain, []inactiveID{1}, nil, func(inactiveID) {}) },
		func() { SuiteTodoEach[inactiveID](plain, []inactiveID{1}, nil) },
		func() { SuiteSkipEachIndexed[inactiveID](plain, []inactiveID{1}, nil, func(inactiveID, int) {}) },
		func() { SuiteTodoEachIndexed[inactiveID](plain, []inactiveID{1}, nil) },
		func() {
			FixtureSuiteSkipEach[inactiveID, string](shared, []inactiveID{1}, nil, func(inactiveID, string, *TestContext) {})
		},
		func() { FixtureSuiteTodoEach[inactiveID, string](shared, []inactiveID{1}, nil) },
		func() {
			FixtureSuiteSkipEachIndexed[inactiveID, string](shared, []inactiveID{1}, nil, func(inactiveID, int, string, *TestContext) {})
		},
		func() { FixtureSuiteTodoEachIndexed[inactiveID, string](shared, []inactiveID{1}, nil) },
	} {
		if err := expectThrows(register); err == nil {
			t.Fatal("nil name callback accepted")
		}
	}
	if len(plain.cases) != 0 || len(shared.suite.cases) != 0 {
		t.Fatal("nil callback committed rows")
	}
}

func modeMethod(mode string) string {
	method := "skipEach"
	if mode[len(mode)-4:] == "todo" {
		method = "todoEach"
	}
	if mode == "indexed-skip" || mode == "indexed-todo" || mode == "shared-indexed-skip" || mode == "shared-indexed-todo" {
		method += "Indexed"
	}
	return method
}

func TestInactiveTablesIndexedSnapshotAndNativeReferences(t *testing.T) {
	for _, mode := range []string{"indexed-skip", "indexed-todo", "shared-indexed-skip", "shared-indexed-todo"} {
		entry := newInactiveTable(mode, func() { t.Fatal("inactive callback ran") })
		rows := []inactiveID{42, 42, 42}
		entry.register(rows, func(value inactiveID, index int) string {
			if value != 42 {
				t.Fatal("naming observed mutated future row")
			}
			rows[2] = 99
			return fmt.Sprint(index)
		}, func() {})
	}
	object := &struct{ Value int }{42}
	suite := NewSuite("native rows")
	SuiteTodoEachIndexed(suite, []*struct{ Value int }{object, object}, func(value *struct{ Value int }, index int) string {
		if value != object {
			t.Fatal("reference copied")
		}
		value.Value++
		return fmt.Sprintf("ref %d", index)
	})
	SuiteSkipEachIndexed(suite, [][]int{nil, {}, {42}}, func(value []int, index int) string {
		if (value == nil) != (index == 0) || (index == 2 && value[0] != 42) {
			t.Fatal("native collection changed")
		}
		return fmt.Sprintf("slice %d", index)
	}, func([]int, int) { t.Fatal("inactive body ran") })
	if object.Value != 44 || len(suite.cases) != 5 {
		t.Fatal(object, suite.cases)
	}
}

func TestInactiveTablesStrictSelectionAndShuffle(t *testing.T) {
	for _, selection := range []string{"strict", "filter", "rerun", "shuffle"} {
		t.Run(selection, func(t *testing.T) {
			t.Setenv("VASEST_FAIL_ON_TODO", "1")
			switch selection {
			case "filter":
				t.Setenv("VASEST_FILTER", "skip 1")
			case "rerun":
				t.Setenv("VASEST_RERUN_SELECTION", `{"all":false,"suites":[{"name":"inactive rows","wholeSuite":false,"cases":["todo 2"]}]}`)
			case "shuffle":
				t.Setenv("VASEST_SHUFFLE_SEED", "42")
			}
			entry := newInactiveTable("indexed-skip", func() { t.Fatal("inactive callback ran") })
			rows := []inactiveID{42, 42, 42}
			entry.register(rows, func(_ inactiveID, index int) string { return fmt.Sprintf("skip %d", index) }, func() {})
			SuiteTodoEachIndexed(entry.suite, rows, func(_ inactiveID, index int) string { return fmt.Sprintf("todo %d", index) })
			first, second := entry.suite.RunTo(&bytes.Buffer{}), entry.suite.RunTo(&bytes.Buffer{})
			if !reflect.DeepEqual(first.Results, second.Results) {
				t.Fatal("rerun changed rows", first, second)
			}
			switch selection {
			case "filter":
				if !first.Success() || first.Todo != 0 || len(first.Results) != 1 || first.Results[0].Name != "skip 1" {
					t.Fatal(first)
				}
			case "rerun":
				if first.Success() || first.Todo != 1 || len(first.Results) != 1 || first.Results[0].Name != "todo 2" {
					t.Fatal(first)
				}
			default:
				if first.Success() || first.Todo != 3 || first.Skipped != 3 || len(first.Results) != 6 {
					t.Fatal(first)
				}
			}
		})
	}
}
