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

func TestSliceIndexedPredicatesRealKinmokusei(t *testing.T) {
	t.Setenv("VASEST_INDEXED_PREDICATE_FIXED", "0")
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
			t.Fatalf("slice indexed predicate integration: %v %s", err, output)
		}
		return exit.ExitCode(), string(output)
	}
	for _, reporter := range []string{"text", "json", "junit"} {
		t.Run(reporter, func(t *testing.T) {
			code, output := invoke("--reporter", reporter, "slice_indexed_predicates/passing.test.km")
			if code != 0 || !strings.Contains(output, "typed slice indexed predicate matchers") || !strings.Contains(output, "slice-indexed-predicate-skip-cleanup") || !strings.Contains(output, "slice indexed predicate resource unavailable") || !strings.Contains(output, "slice-indexed-predicate-passing-later") || strings.Contains(output, "unreachable-") || !strings.Contains(output, "5 passed, 0 failed, 2 skipped, 0 todo") {
				t.Fatal(code, output)
			}
			historyPath := filepath.Join(t.TempDir(), "history.json")
			code, output = invoke("--reporter", reporter, "--save-failures", historyPath, "slice_indexed_predicates/failures.test.km")
			if code != 1 || !strings.Contains(output, "slice-indexed-predicate-afterEach") || !strings.Contains(output, "slice-indexed-predicate-cleanup") || !strings.Contains(output, "slice-indexed-predicate-failing-later") || !strings.Contains(output, "predicate callback failed") || !strings.Contains(output, "domain predicate failed") {
				t.Fatal(code, output)
			}
			history, err := readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 1 || history.Files[0].WholeFile || len(history.Files[0].Suites) != 1 || history.Files[0].Suites[0].WholeSuite || len(history.Files[0].Suites[0].Cases) != 10 {
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
				if failed != 10 {
					t.Fatal(failed, output)
				}
			} else if reporter == "junit" {
				var doc struct {
					Tests    int `xml:"tests,attr"`
					Failures int `xml:"failures,attr"`
				}
				if xml.Unmarshal([]byte(output), &doc) != nil || doc.Tests != 11 || doc.Failures != 10 {
					t.Fatal(output)
				}
			}
			code, output = invoke("--reporter", reporter, "--rerun-failed", historyPath, "--save-failures", historyPath, "--filter", "negated all mismatch", "slice_indexed_predicates")
			if code != 1 || !strings.Contains(output, "negated all mismatch") || strings.Contains(output, "slice-indexed-predicate-cleanup") || strings.Contains(output, "slice-indexed-predicate-failing-later") || strings.Contains(output, "unreachable-") {
				t.Fatal(code, output)
			}
			history, err = readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 1 || len(history.Files[0].Suites) != 1 || fmt.Sprint(history.Files[0].Suites[0].Cases) != "[negated all mismatch]" {
				t.Fatal(history, err)
			}
			t.Setenv("VASEST_INDEXED_PREDICATE_FIXED", "1")
			code, output = invoke("--reporter", reporter, "--rerun-failed", historyPath, "--save-failures", historyPath, "slice_indexed_predicates")
			if code != 0 || !strings.Contains(output, "slice-indexed-predicate-fixed") || strings.Contains(output, "slice-indexed-predicate-failing-later") || strings.Contains(output, "slice-indexed-predicate-passing-later") || strings.Contains(output, "unreachable-") {
				t.Fatal(code, output)
			}
			history, err = readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 0 {
				t.Fatal(history, err)
			}
		})
	}
	code, output := invoke("--bail", "slice_indexed_predicates/failures.test.km")
	if code != 1 || !strings.Contains(output, "slice-indexed-predicate-cleanup") || strings.Contains(output, "slice-indexed-predicate-failing-later") || !strings.Contains(output, "0 passed, 1 failed, 10 skipped") {
		t.Fatal(code, output)
	}
	historyPath := filepath.Join(t.TempDir(), "panic.json")
	code, output = invoke("--save-failures", historyPath, "slice_indexed_predicates/panic.test.km")
	if code != 1 || !strings.Contains(output, "reflect: call of reflect.Value.Index on int Value") || strings.Contains(output, "unreachable-") {
		t.Fatal(code, output)
	}
	last := -1
	for _, marker := range []string{"slice-indexed-predicate-panic-afterEach", "slice-indexed-predicate-panic-cleanup", "slice-indexed-predicate-panic-afterAll"} {
		index := strings.Index(output, marker)
		if index <= last {
			t.Fatal("native slice indexed predicate panic cleanup order changed", output)
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
	for _, file := range []string{"input", "result", "nominal", "width", "callback", "constructor", "index", "arity", "binding", "negated_binding"} {
		t.Run("compile rejection/"+file, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, keika, "check", "slice_indexed_predicates/type_errors/"+file+".km")
			command.Dir = consumer
			output, err := command.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || !strings.Contains(string(output), "cannot use") {
				t.Fatalf("compiler did not reject mismatched slice indexed predicate types: %v %s", err, output)
			}
		})
	}
}
