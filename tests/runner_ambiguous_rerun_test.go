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

func ambiguousSuiteReport(t *testing.T, suite, name string, success bool) ReportedSuite {
	t.Helper()
	status := "pass"
	passed, failed := 1, 0
	if !success {
		status, passed, failed = "fail", 0, 1
	}
	data, err := json.Marshal(map[string]any{
		"success": success,
		"summary": map[string]any{
			"suite": suite, "passed": passed, "failed": failed, "skipped": 0, "todo": 0,
			"results": []map[string]any{{"name": name, "passed": success, "status": status}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	report, err := readSuiteReport(string(data))
	if err != nil {
		t.Fatal(err)
	}
	return ReportedSuite{raw: string(data), report: report}
}

func TestFailureHistoryFallsBackForFailedAndPassingDuplicateSuites(t *testing.T) {
	for _, names := range []struct{ suite, failedCase, passedCase string }{
		{"same suite", "same case", "same case"},
		{"same suite", "failed case", "other case"},
		{" 金木犀\n<&> ", " failed\ncase ", "passed case"},
	} {
		failed := ambiguousSuiteReport(t, names.suite, names.failedCase, false)
		passed := ambiguousSuiteReport(t, names.suite, names.passedCase, true)
		for _, reports := range [][]ReportedSuite{{failed, passed}, {passed, failed}, {failed, passed, failed}} {
			execution := FileExecution{executed: true, reportFailure: "suite reported failed tests", suites: reports}
			entry := failedFile("file.test.km", execution)
			if !entry.WholeFile || len(entry.Suites) != 0 || entry.File != "file.test.km" {
				t.Fatalf("ambiguous failure retained case selection: %#v reports=%#v", entry, reports)
			}
		}
	}
}

func TestFailureHistoryIgnoresUnrelatedPassingDuplicateSuites(t *testing.T) {
	passed := ambiguousSuiteReport(t, "passing duplicate", "case", true)
	failed := ambiguousSuiteReport(t, "unique failed suite", "failed case", false)
	for _, reports := range [][]ReportedSuite{{passed, passed}, {passed, failed, passed}, {failed, passed, passed}} {
		entry := failedFile("file.test.km", FileExecution{executed: true, suites: reports})
		if entry.WholeFile {
			t.Fatalf("unrelated passing duplicates selected the file: %#v", entry)
		}
		if len(reports) == 2 {
			if len(entry.Suites) != 0 {
				t.Fatalf("passing file produced failures: %#v", entry)
			}
		} else if len(entry.Suites) != 1 || entry.Suites[0].Name != "unique failed suite" || entry.Suites[0].WholeSuite || fmt.Sprint(entry.Suites[0].Cases) != "[failed case]" {
			t.Fatalf("unique failure lost exact selection: %#v", entry)
		}
	}
}

func TestAmbiguousRerunRealKinmokusei(t *testing.T) {
	runner, consumer := os.Getenv("VASEST_INTEGRATION_RUNNER"), os.Getenv("VASEST_INTEGRATION_CONSUMER")
	if runner == "" || consumer == "" {
		t.Skip("requires the external Kinmokusei project")
	}
	invoke := func(t *testing.T, renamed, fixed bool, args ...string) (int, string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, runner, append([]string{"--no-config", "--timeout", "2s"}, args...)...)
		command.Dir = consumer
		renamedFlag, fixedFlag := "0", "0"
		if renamed {
			renamedFlag = "1"
		}
		if fixed {
			fixedFlag = "1"
		}
		command.Env = append(os.Environ(), "VASEST_AMBIGUOUS_RENAMED="+renamedFlag, "VASEST_AMBIGUOUS_FIXED="+fixedFlag)
		command.WaitDelay = time.Second
		output, err := command.CombinedOutput()
		if err == nil {
			return 0, string(output)
		}
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("ambiguous rerun integration: %v %s", err, output)
		}
		return exit.ExitCode(), string(output)
	}
	for _, reporter := range []string{"text", "json", "junit"} {
		for _, order := range []string{"normal", "reversed"} {
			t.Run(reporter+"/"+order, func(t *testing.T) {
				t.Setenv("VASEST_AMBIGUOUS_REVERSED", "0")
				if order == "reversed" {
					t.Setenv("VASEST_AMBIGUOUS_REVERSED", "1")
				}
				historyPath := filepath.Join(t.TempDir(), "failures.json")
				status, output := invoke(t, false, false, "--reporter", reporter, "--save-failures", historyPath, "ambiguous_rerun")
				if status != 1 || !strings.Contains(output, "ambiguous-later-file") {
					t.Fatalf("initial status=%d output=%s", status, output)
				}
				initial, err := readFailureHistory(historyPath)
				if err != nil || len(initial.Files) != 1 || filepath.Base(initial.Files[0].File) != "a_suites.test.km" {
					t.Fatalf("initial history=%#v err=%v %s", initial, err, output)
				}
				// Keep a passing case with the old name in the other suite, while
				// renaming the still-failing original. Case-only history would run
				// that passing case, return success and erase the recorded failure.
				status, output = invoke(t, true, false, "--jobs", "2", "--repeat", "2", "--reporter", reporter, "--rerun-failed", historyPath, "--save-failures", historyPath, "ambiguous_rerun")
				if status != 1 || !strings.Contains(output, "ambiguous-failed-body renamed failing case") || !strings.Contains(output, "ambiguous-shared-body") || !strings.Contains(output, "ambiguous-shared-cleanup") || !strings.Contains(output, "ambiguous-unrelated-body") || strings.Contains(output, "ambiguous-later-file") {
					t.Fatalf("renamed failure was missed: status=%d initial=%#v output=%s", status, initial, output)
				}
				history, err := readFailureHistory(historyPath)
				if !initial.Files[0].WholeFile || len(initial.Files[0].Suites) != 0 || err != nil || len(history.Files) != 1 || !history.Files[0].WholeFile || len(history.Files[0].Suites) != 0 {
					t.Fatalf("ambiguous failure lost file scope: initial=%#v history=%#v err=%v", initial, history, err)
				}
				if reporter == "text" && !strings.Contains(output, "0 file(s) passed, 2 failed, 0 not run") {
					t.Fatal("wrong repeat counts", output)
				}
				if reporter == "json" {
					failedCases, files := 0, 0
					for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
						var event struct {
							Type, Status             string
							Report                   SuiteReport
							PassedFiles, FailedFiles int
						}
						if err := json.Unmarshal([]byte(line), &event); err != nil {
							t.Fatalf("polluted JSON: %v %s", err, output)
						}
						if event.Type == "file" {
							files++
							if event.Status != "fail" {
								t.Fatalf("failed rerun reported success: %#v", event)
							}
						}
						if event.Type == "summary" && (event.PassedFiles != 0 || event.FailedFiles != 2) {
							t.Fatalf("wrong repeat summary: %#v", event)
						}
						if event.Type == "suite" && !event.Report.Success {
							failedCases++
							first := event.Report.Summary.Results[0]
							if event.Report.Summary.Suite != "金木犀 <&> suite" || first.Name != "renamed failing case" || len(first.Failures) != 1 || first.Failures[0].Phase != "body" || first.Failures[0].Location != (LocationReport{File: "domain-check.km", Line: 42, Source: "explicit"}) {
								t.Fatalf("failure details changed: %#v", event.Report)
							}
						}
					}
					if files != 2 || failedCases != 2 {
						t.Fatal("missing rerun failures", output)
					}
				}
				if reporter == "junit" {
					var document fileErrorJUnitDocument
					if err := xml.Unmarshal([]byte(output), &document); err != nil || document.Tests != 8 || document.Failures != 2 || len(document.Suites) != 2 {
						t.Fatalf("wrong repeated JUnit: %#v %v %s", document, err, output)
					}
				}
				status, output = invoke(t, true, false, "--bail", "--repeat", "2", "--reporter", reporter, "--rerun-failed", historyPath, "ambiguous_rerun")
				if status != 1 || !strings.Contains(output, "ambiguous-failed-body renamed failing case") || !strings.Contains(output, "ambiguous-failed-cleanup") || strings.Contains(output, "ambiguous-later-file") {
					t.Fatalf("bail missed the renamed failure: status=%d %s", status, output)
				}
				if reporter == "junit" {
					var document fileErrorJUnitDocument
					if err := xml.Unmarshal([]byte(output), &document); err != nil || document.Tests != 4 || document.Failures != 1 || len(document.Suites) != 1 {
						t.Fatalf("bail scheduled another iteration: %#v %v %s", document, err, output)
					}
				}
				status, output = invoke(t, true, true, "--reporter", reporter, "--rerun-failed", historyPath, "--save-failures", historyPath, "ambiguous_rerun")
				history, err = readFailureHistory(historyPath)
				if status != 0 || err != nil || len(history.Files) != 0 || !strings.Contains(output, "ambiguous-failed-body renamed failing case") || !strings.Contains(output, "ambiguous-shared-body") || !strings.Contains(output, "ambiguous-unrelated-body") || strings.Contains(output, "ambiguous-later-file") {
					t.Fatalf("fixed file did not clear history: status=%d history=%#v err=%v %s", status, history, err, output)
				}
			})
		}
		t.Run(reporter+"/unique failure", func(t *testing.T) {
			t.Setenv("VASEST_AMBIGUOUS_UNIQUE", "1")
			historyPath := filepath.Join(t.TempDir(), "failures.json")
			status, output := invoke(t, false, false, "--reporter", reporter, "--save-failures", historyPath, "ambiguous_rerun")
			history, err := readFailureHistory(historyPath)
			if status != 1 || err != nil || len(history.Files) != 1 || history.Files[0].WholeFile || len(history.Files[0].Suites) != 1 || history.Files[0].Suites[0].Name != "unique failed suite" || fmt.Sprint(history.Files[0].Suites[0].Cases) != "[case <&>]" {
				t.Fatalf("passing duplicates broadened unique failure: status=%d history=%#v err=%v %s", status, history, err, output)
			}
			status, output = invoke(t, false, true, "--reporter", reporter, "--rerun-failed", historyPath, "--save-failures", historyPath, "ambiguous_rerun")
			history, err = readFailureHistory(historyPath)
			if status != 0 || err != nil || len(history.Files) != 0 || !strings.Contains(output, "ambiguous-failed-body case") || strings.Contains(output, "ambiguous-shared-setup") || strings.Contains(output, "ambiguous-duplicate-body") || strings.Contains(output, "ambiguous-unrelated-setup") || strings.Contains(output, "ambiguous-later-file") {
				t.Fatalf("unrelated hooks ran for unique failure: status=%d history=%#v err=%v %s", status, history, err, output)
			}
		})
	}
}
