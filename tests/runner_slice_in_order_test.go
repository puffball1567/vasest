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

func TestSliceInOrderRealKinmokusei(t *testing.T) {
	runner, consumer := os.Getenv("VASEST_INTEGRATION_RUNNER"), os.Getenv("VASEST_INTEGRATION_CONSUMER")
	if runner == "" || consumer == "" {
		t.Skip("requires the external Kinmokusei project")
	}
	invoke := func(fixed bool, args ...string) (int, string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, runner, append([]string{"--no-config"}, args...)...)
		command.Dir = consumer
		flag := "0"
		if fixed {
			flag = "1"
		}
		command.Env = append(os.Environ(), "VASEST_SLICE_IN_ORDER_FIXED="+flag)
		command.WaitDelay = time.Second
		output, err := command.CombinedOutput()
		if err == nil {
			return 0, string(output)
		}
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("slice order integration: %v %s", err, output)
		}
		return exit.ExitCode(), string(output)
	}
	for _, reporter := range []string{"text", "json", "junit"} {
		t.Run(reporter, func(t *testing.T) {
			historyPath := filepath.Join(t.TempDir(), "history.json")
			code, output := invoke(false, "--reporter", reporter, "--save-failures", historyPath, "slice_in_order/passing.test.km")
			if code != 0 || !strings.Contains(output, "slice-in-order-passing-later") || !strings.Contains(output, "6 passed, 0 failed, 0 skipped, 0 todo") {
				t.Fatal(code, output)
			}
			history, err := readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 0 {
				t.Fatal(history, err)
			}
			code, output = invoke(false, "--reporter", reporter, "--save-failures", historyPath, "slice_in_order/failures.test.km")
			if code != 1 || !strings.Contains(output, "slice-in-order-afterEach") || !strings.Contains(output, "slice-in-order-cleanup") || !strings.Contains(output, "slice-in-order-afterAll") || !strings.Contains(output, "slice-in-order-failing-later") || strings.Contains(output, "stack overflow") {
				t.Fatal(code, output)
			}
			history, err = readFailureHistory(historyPath)
			wantCases := "[missing ordered elements negated ordered elements missing repeated elements negated empty order NaN order cyclic order explicit order location]"
			if err != nil || len(history.Files) != 1 || history.Files[0].WholeFile || len(history.Files[0].Suites) != 1 || history.Files[0].Suites[0].WholeSuite || fmt.Sprint(history.Files[0].Suites[0].Cases) != wantCases {
				t.Fatal("lost exact failure selection", history, err)
			}
			if reporter == "json" {
				failures := 0
				for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
					var event struct {
						Type   string
						Report SuiteReport
					}
					if err := json.Unmarshal([]byte(line), &event); err != nil {
						t.Fatal(err, output)
					}
					if event.Type != "suite" {
						continue
					}
					for _, result := range event.Report.Summary.Results {
						if result.Status != "fail" {
							continue
						}
						failures++
						if len(result.Failures) != 1 || result.Failures[0].Phase != "body" || len(result.Failures[0].Message) > 1000 {
							t.Fatal(result)
						}
						if !strings.Contains(result.Failures[0].Message, "in order") {
							t.Fatal("lost ordered containment diagnostic", result)
						}
						location := result.Failures[0].Location
						if result.Name == "explicit order location" {
							if location != (LocationReport{File: "domain-check.km", Line: 42, Source: "explicit"}) {
								t.Fatal("lost explicit location", result)
							}
						} else if !strings.HasSuffix(filepath.ToSlash(location.File), "/generated.go") || location.Line <= 0 || location.Source != "go" {
							t.Fatal("lost fluent location", result)
						}
					}
				}
				if failures != 7 {
					t.Fatal(failures, output)
				}
			} else if reporter == "junit" {
				var document struct {
					Tests    int `xml:"tests,attr"`
					Failures int `xml:"failures,attr"`
				}
				if err := xml.Unmarshal([]byte(output), &document); err != nil || document.Tests != 8 || document.Failures != 7 {
					t.Fatal(document, err, output)
				}
			} else if !strings.Contains(output, "1 passed, 7 failed, 0 skipped") {
				t.Fatal(output)
			}
			code, output = invoke(false, "--reporter", reporter, "--rerun-failed", historyPath, "--save-failures", historyPath, "--filter", "missing ordered elements", "slice_in_order")
			if code != 1 || !strings.Contains(output, "missing ordered elements") || !strings.Contains(output, "slice-in-order-cleanup") || strings.Contains(output, "slice-in-order-failing-later") || strings.Contains(output, "slice-in-order-passing-later") {
				t.Fatal(code, output)
			}
			history, err = readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 1 || fmt.Sprint(history.Files[0].Suites[0].Cases) != "[missing ordered elements]" {
				t.Fatal(history, err)
			}
			code, output = invoke(true, "--reporter", reporter, "--rerun-failed", historyPath, "--save-failures", historyPath, "slice_in_order")
			if code != 0 || !strings.Contains(output, "slice-in-order-fixed") || !strings.Contains(output, "slice-in-order-cleanup") || strings.Contains(output, "slice-in-order-failing-later") || strings.Contains(output, "slice-in-order-passing-later") {
				t.Fatal(code, output)
			}
			history, err = readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 0 {
				t.Fatal(history, err)
			}
			code, output = invoke(false, "--reporter", reporter, "--bail", "slice_in_order/failures.test.km")
			if code != 1 || !strings.Contains(output, "slice-in-order-cleanup") || !strings.Contains(output, "slice-in-order-afterAll") || strings.Contains(output, "slice-in-order-failing-later") || !strings.Contains(output, "0 passed, 1 failed, 7 skipped") {
				t.Fatal(code, output)
			}
		})
	}
	keika := os.Getenv("KEIKA")
	if keika == "" {
		keika = "keika"
	}
	for _, file := range []string{"width", "negated", "nominal", "scalar", "explicit", "constructor", "negated_constructor", "binding", "negated_binding", "result"} {
		t.Run("compile_rejection/"+file, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, keika, "check", "slice_in_order/type_errors/"+file+".km")
			command.Dir = consumer
			output, err := command.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || !strings.Contains(string(output), "cannot use") {
				t.Fatalf("compiler accepted incompatible slice order types: %v %s", err, output)
			}
		})
	}
}
