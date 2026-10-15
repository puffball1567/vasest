package main

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSliceElementsRealKinmokusei(t *testing.T) {
	t.Setenv("VASEST_SLICE_ELEMENT_FIXED", "0")
	runner, consumer := os.Getenv("VASEST_INTEGRATION_RUNNER"), os.Getenv("VASEST_INTEGRATION_CONSUMER")
	if runner == "" || consumer == "" {
		t.Skip("requires the external Kinmokusei project")
	}
	invoke := func(args ...string) (int, string) {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, runner, append([]string{"--no-config"}, args...)...)
		command.Dir = consumer
		output, err := command.CombinedOutput()
		if err == nil {
			return 0, string(output)
		}
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("selected element integration: %v %s", err, output)
		}
		return exit.ExitCode(), string(output)
	}
	for _, reporter := range []string{"text", "json", "junit"} {
		t.Run(reporter, func(t *testing.T) {
			code, output := invoke("--reporter", reporter, "slice_elements/passing.test.km")
			if code != 0 || !strings.Contains(output, "typed selected slice elements") || !strings.Contains(output, "slice-element-skip-cleanup") || !strings.Contains(output, "selected element resource unavailable") || !strings.Contains(output, "slice-element-passing-later") || strings.Contains(output, "unreachable-") || !strings.Contains(output, "6 passed, 0 failed, 2 skipped, 0 todo") {
				t.Fatal(code, output)
			}
			historyPath := filepath.Join(t.TempDir(), "history.json")
			code, output = invoke("--reporter", reporter, "--save-failures", historyPath, "slice_elements/failures.test.km")
			if code != 1 || !strings.Contains(output, "slice-element-afterEach") || !strings.Contains(output, "slice-element-cleanup") || !strings.Contains(output, "slice-element-failing-later") || !strings.Contains(output, "selected element callback failed") || !strings.Contains(output, "domain element failed") || strings.Contains(output, "missing element callback ran") {
				t.Fatal(code, output)
			}
			history, err := readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 1 || history.Files[0].WholeFile || len(history.Files[0].Suites) != 1 || history.Files[0].Suites[0].WholeSuite || len(history.Files[0].Suites[0].Cases) != 12 {
				t.Fatal(history, err)
			}
			if reporter == "json" {
				failed := 0
				for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
					var event struct {
						Type   string
						Report SuiteReport
					}
					if json.Unmarshal([]byte(line), &event) != nil {
						t.Fatal(output)
					}
					if event.Type != "suite" {
						continue
					}
					for _, result := range event.Report.Summary.Results {
						if result.Status != "fail" {
							continue
						}
						failed++
						if len(result.Failures) != 1 || result.Failures[0].Phase != "body" {
							t.Fatal(result)
						}
						location := result.Failures[0].Location
						switch result.Name {
						case "thrown predicate":
							if location != (LocationReport{}) {
								t.Fatal("invented callback location", result)
							}
						case "explicit callback location":
							if location.File != "domain-check.km" || location.Line != 42 || location.Source != "explicit" {
								t.Fatal(result)
							}
						default:
							if !strings.HasSuffix(filepath.ToSlash(location.File), "/generated.go") || location.Line <= 0 || location.Source != "go" {
								t.Fatal(result)
							}
						}
					}
				}
				if failed != 12 {
					t.Fatal(failed, output)
				}
			} else if reporter == "junit" {
				var doc struct {
					Tests    int `xml:"tests,attr"`
					Failures int `xml:"failures,attr"`
				}
				if xml.Unmarshal([]byte(output), &doc) != nil || doc.Tests != 13 || doc.Failures != 12 {
					t.Fatal(output)
				}
			}
			code, output = invoke("--reporter", reporter, "--rerun-failed", historyPath, "--save-failures", historyPath, "--filter", "element value mismatch", "slice_elements")
			if code != 1 || !strings.Contains(output, "element value mismatch") || !strings.Contains(output, "slice-element-cleanup") || strings.Contains(output, "slice-element-failing-later") || strings.Contains(output, "unreachable-") {
				t.Fatal(code, output)
			}
			history, err = readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 1 || len(history.Files[0].Suites) != 1 || fmt.Sprint(history.Files[0].Suites[0].Cases) != "[element value mismatch]" {
				t.Fatal(history, err)
			}
			t.Setenv("VASEST_SLICE_ELEMENT_FIXED", "1")
			code, output = invoke("--reporter", reporter, "--rerun-failed", historyPath, "--save-failures", historyPath, "slice_elements")
			if code != 0 || !strings.Contains(output, "slice-element-fixed") || !strings.Contains(output, "slice-element-cleanup") || strings.Contains(output, "slice-element-failing-later") || strings.Contains(output, "slice-element-passing-later") || strings.Contains(output, "unreachable-") {
				t.Fatal(code, output)
			}
			history, err = readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 0 {
				t.Fatal(history, err)
			}
		})
	}
	code, output := invoke("--bail", "slice_elements/failures.test.km")
	if code != 1 || !strings.Contains(output, "slice-element-cleanup") || strings.Contains(output, "slice-element-failing-later") || !strings.Contains(output, "0 passed, 1 failed, 12 skipped") {
		t.Fatal(code, output)
	}
	historyPath := filepath.Join(t.TempDir(), "panic.json")
	code, output = invoke("--save-failures", historyPath, "slice_elements/panic.test.km")
	if code != 1 || !strings.Contains(output, "reflect: call of reflect.Value.Index on int Value") || strings.Contains(output, "unreachable-") {
		t.Fatal(code, output)
	}
	last := -1
	for _, marker := range []string{"slice-element-panic-afterEach", "slice-element-panic-cleanup", "slice-element-panic-afterAll"} {
		index := strings.Index(output, marker)
		if index <= last {
			t.Fatal("native selected element panic cleanup order changed", output)
		}
		last = index
	}
	history, err := readFailureHistory(historyPath)
	if err != nil || len(history.Files) != 1 || !history.Files[0].WholeFile {
		t.Fatal(history, err)
	}
	keika := os.Getenv("KEIKA")
	if keika == "" {
		keika = "keika"
	}
	for _, file := range []string{"value_width", "value_nominal", "index", "constructor", "binding", "negated_binding", "predicate_input", "predicate_result", "predicate_nominal", "predicate_width", "predicate_index", "predicate_arity", "predicate_binding", "predicate_callback"} {
		t.Run("compile rejection/"+file, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, keika, "check", "slice_elements/type_errors/"+file+".km")
			command.Dir = consumer
			output, err := command.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || !strings.Contains(string(output), "cannot use") {
				t.Fatalf("compiler did not reject mismatched selected element types: %v %s", err, output)
			}
		})
	}
}
