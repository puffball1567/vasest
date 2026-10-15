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

func TestRerunReportsRejectNewAmbiguityOnlyForSelectedCaseSuites(t *testing.T) {
	passed := ambiguousSuiteReport(t, "金木犀 <&> suite", "old case", true)
	failed := ambiguousSuiteReport(t, "金木犀 <&> suite", "renamed failure", false)
	unrelated := ambiguousSuiteReport(t, "unrelated", "case", true)
	filtered, err := readSuiteReport(`{"success":true,"summary":{"suite":"金木犀 <&> suite","passed":0,"failed":0,"skipped":1,"todo":0,"results":[]}}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name      string
		selection RerunSelection
		reports   []ReportedSuite
		ambiguous bool
	}{
		{"passing then filtered", RerunSelection{Suites: []FailedSuite{{Name: passed.report.Summary.Suite, Cases: []string{"old case"}}}}, []ReportedSuite{passed, {report: filtered}}, true},
		{"filtered then passing", RerunSelection{Suites: []FailedSuite{{Name: passed.report.Summary.Suite, Cases: []string{"old case"}}}}, []ReportedSuite{{report: filtered}, passed}, true},
		{"failed duplicate", RerunSelection{Suites: []FailedSuite{{Name: passed.report.Summary.Suite, Cases: []string{"old case"}}}}, []ReportedSuite{failed, passed}, true},
		{"unique suite", RerunSelection{Suites: []FailedSuite{{Name: passed.report.Summary.Suite, Cases: []string{"old case"}}}}, []ReportedSuite{passed, unrelated}, false},
		{"unrelated duplicates", RerunSelection{Suites: []FailedSuite{{Name: unrelated.report.Summary.Suite, Cases: []string{"case"}}}}, []ReportedSuite{passed, passed, unrelated}, false},
		{"whole suite", RerunSelection{Suites: []FailedSuite{{Name: passed.report.Summary.Suite, WholeSuite: true}}}, []ReportedSuite{passed, passed}, false},
		{"whole file", RerunSelection{All: true}, []ReportedSuite{passed, passed}, false},
		{"no selected suites", RerunSelection{}, []ReportedSuite{passed, passed}, false},
		{"no reports", RerunSelection{Suites: []FailedSuite{{Name: passed.report.Summary.Suite, Cases: []string{"old case"}}}}, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			selection, err := json.Marshal(tc.selection)
			if err != nil {
				t.Fatal(err)
			}
			failure := rerunReportFailure(string(selection), FileExecution{suites: tc.reports})
			if tc.ambiguous {
				if !strings.Contains(failure, "ambiguous rerun suite") || !strings.Contains(failure, "金木犀 <&> suite") || !strings.Contains(failure, "2 reports") {
					t.Fatalf("missing ambiguity diagnosis: %q", failure)
				}
				entry := failedFile("file.test.km", FileExecution{executed: true, reportFailure: failure, suites: tc.reports})
				if !entry.WholeFile || len(entry.Suites) != 0 {
					t.Fatalf("ambiguity did not broaden history: %#v", entry)
				}
			} else if failure != "" {
				t.Fatalf("safe selection was broadened: %q", failure)
			}
		})
	}
	if got := rerunReportFailure("", FileExecution{suites: []ReportedSuite{passed, passed}}); got != "" {
		t.Fatalf("ordinary execution gained a selection failure: %q", got)
	}
	if got := rerunReportFailure("not JSON", FileExecution{}); !strings.Contains(got, "invalid rerun selection:") {
		t.Fatalf("invalid selection accepted: %q", got)
	}
}

func TestRerunReportAmbiguityPreservesCasesAndMalformedReportDiagnostics(t *testing.T) {
	t.Setenv("VASEST_RUNNER_HELPER", "1")
	keika := fakeKeika(t)
	passed := ambiguousSuiteReport(t, "same suite", "old case", true)
	failed := ambiguousSuiteReport(t, "same suite", "failed case", false)
	filtered := `{"success":true,"summary":{"suite":"same suite","passed":0,"failed":0,"skipped":1,"todo":0,"results":[]}}`
	selection, err := json.Marshal(RerunSelection{Suites: []FailedSuite{{Name: "same suite", Cases: []string{"old case"}}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, data          string
		invalid, failedCase bool
	}{
		{"all passed", passed.raw + "\n" + filtered, false, false},
		{"failed case", passed.raw + "\n" + failed.raw, false, true},
		{"malformed report", passed.raw + "\n" + filtered + "\nnot JSON", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("VASEST_RUNNER_REPORT", tc.data)
			file := filepath.Join(t.TempDir(), "probe.test.km")
			if err := os.WriteFile(file, nil, 0600); err != nil {
				t.Fatal(err)
			}
			execution := executeTestFile(keika, file, "", "", false, false, false, 2*time.Second, "", "json", nil, "", "", string(selection), nil)
			if execution.runError != nil || execution.fatalError != nil || execution.expectedExit || len(execution.suites) != 2 || !strings.Contains(execution.reportFailure, "ambiguous rerun suite") || strings.Contains(execution.reportFailure, "invalid suite report:") != tc.invalid {
				t.Fatalf("selection failure lost other results/diagnostics: %#v", execution)
			}
			if execution.rawReport != tc.data+"\n" || execution.suites[0].raw != passed.raw || execution.suites[0].report.Summary.Results[0].Status != "pass" || (execution.suites[1].report.Summary.Failed > 0) != tc.failedCase {
				t.Fatalf("raw/parsed reports changed: %#v", execution)
			}
			entry := failedFile(file, execution)
			if !entry.WholeFile || len(entry.Suites) != 0 {
				t.Fatalf("selection failure did not broaden history: %#v", entry)
			}
		})
	}
}

func TestEvolvingRerunRealKinmokusei(t *testing.T) {
	runner, consumer := os.Getenv("VASEST_INTEGRATION_RUNNER"), os.Getenv("VASEST_INTEGRATION_CONSUMER")
	if runner == "" || consumer == "" {
		t.Skip("requires the external Kinmokusei project")
	}
	for _, reporter := range []string{"text", "json", "junit"} {
		for _, reversed := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/reversed=%t", reporter, reversed), func(t *testing.T) {
				historyPath := filepath.Join(t.TempDir(), "failures.json")
				invoke := func(changed, fixed bool, args ...string) (int, string) {
					t.Helper()
					ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
					defer cancel()
					command := exec.CommandContext(ctx, runner, append([]string{"--no-config", "--reporter", reporter, "--timeout", "2s", "--save-failures", historyPath}, args...)...)
					command.Dir = consumer
					flag := func(value bool) string {
						if value {
							return "1"
						}
						return "0"
					}
					command.Env = append(os.Environ(), "VASEST_EVOLVING_CHANGED="+flag(changed), "VASEST_EVOLVING_FIXED="+flag(fixed), "VASEST_EVOLVING_REVERSED="+flag(reversed))
					command.WaitDelay = time.Second
					output, err := command.CombinedOutput()
					if err == nil {
						return 0, string(output)
					}
					var exit *exec.ExitError
					if !errors.As(err, &exit) {
						t.Fatalf("evolving rerun: %v %s", err, output)
					}
					return exit.ExitCode(), string(output)
				}
				status, output := invoke(false, false, "evolving_rerun")
				initial, err := readFailureHistory(historyPath)
				if status != 1 || err != nil || len(initial.Files) != 1 || initial.Files[0].WholeFile || len(initial.Files[0].Suites) != 1 || initial.Files[0].Suites[0].WholeSuite || fmt.Sprint(initial.Files[0].Suites[0].Cases) != "[original failure]" || !strings.Contains(output, "evolving-later-file") {
					t.Fatalf("initial exact selection missing: status=%d history=%#v err=%v %s", status, initial, err, output)
				}
				args := []string{"--rerun-failed", historyPath, "--repeat", "2"}
				wantFiles := 2
				if reversed {
					args = append(args, "--bail")
					wantFiles = 1
				} else {
					args = append(args, "--jobs", "2")
				}
				artifactDirectory := t.TempDir()
				// JSON runs also verify that the new diagnosis survives artifact capture.
				if reporter == "json" {
					args = append(args, "--artifacts", artifactDirectory)
				}
				status, output = invoke(true, false, append(args, "evolving_rerun")...)
				history, err := readFailureHistory(historyPath)
				if status != 1 || err != nil || len(history.Files) != 1 || !history.Files[0].WholeFile || len(history.Files[0].Suites) != 0 || !strings.Contains(output, "ambiguous rerun suite") || !strings.Contains(output, "evolving-passing-body") || !strings.Contains(output, "evolving-passing-cleanup") || strings.Contains(output, "evolving-original-body") || strings.Contains(output, "evolving-original-setup") || strings.Contains(output, "evolving-original-cleanup") || strings.Contains(output, "evolving-unrelated-body") || strings.Contains(output, "evolving-later-file") {
					t.Fatalf("passing duplicate masked a renamed failure: status=%d history=%#v err=%v %s", status, history, err, output)
				}
				if reporter == "text" && !strings.Contains(output, fmt.Sprintf("0 file(s) passed, %d failed, %d not run", wantFiles, 2-wantFiles)) {
					t.Fatal("wrong file counts", output)
				}
				if reporter == "json" {
					files, reports := 0, 0
					for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
						var event struct {
							Type, Status, Error                   string
							Report                                SuiteReport
							FailedFiles, PassedFiles, NotRunFiles int
						}
						if err := json.Unmarshal([]byte(line), &event); err != nil {
							t.Fatalf("polluted JSON: %v %s", err, output)
						}
						if event.Type == "suite" {
							reports++
							if !event.Report.Success || event.Report.Summary.Failed != 0 {
								t.Fatalf("case results were fabricated: %#v", event.Report)
							}
						}
						if event.Type == "file" {
							files++
							if event.Status != "fail" || !strings.Contains(event.Error, "ambiguous rerun suite") {
								t.Fatalf("missing selection diagnosis: %#v", event)
							}
						}
						if event.Type == "summary" && (event.FailedFiles != wantFiles || event.PassedFiles != 0 || event.NotRunFiles != 2-wantFiles) {
							t.Fatalf("wrong summary: %#v", event)
						}
					}
					if files != wantFiles || reports != 3*wantFiles {
						t.Fatal("missing valid reports", output)
					}
					runs, err := os.ReadDir(artifactDirectory)
					if err != nil || len(runs) != wantFiles {
						t.Fatalf("artifact runs=%v err=%v", runs, err)
					}
					for _, run := range runs {
						document := readArtifactResults(t, filepath.Join(artifactDirectory, run.Name(), "manifest.json"))
						if len(document.Files) != 1 || document.Files[0].Status != "fail" || !strings.Contains(document.Files[0].Failure, "ambiguous rerun suite") {
							t.Fatalf("artifact lost selection failure: %#v", document)
						}
					}
				}
				if reporter == "junit" {
					var document fileErrorJUnitDocument
					if err := xml.Unmarshal([]byte(output), &document); err != nil || document.Tests != 2*wantFiles || document.Failures != wantFiles || len(document.Suites) != wantFiles {
						t.Fatalf("lost independent file error: %#v %v %s", document, err, output)
					}
					for _, suite := range document.Suites {
						if len(suite.Cases) != 2 || suite.Cases[0].Name != "original failure" || suite.Cases[0].Failure != nil || suite.Cases[1].Name != "<file>" || suite.Cases[1].Failure == nil || !strings.Contains(suite.Cases[1].Failure.Message, "ambiguous rerun suite") {
							t.Fatalf("passing case and selection failure not distinguished: %#v", suite)
						}
					}
				}
				// The broadened history must now reach the original renamed failure.
				status, output = invoke(true, false, "--rerun-failed", historyPath, "evolving_rerun")
				history, err = readFailureHistory(historyPath)
				if status != 1 || err != nil || len(history.Files) != 1 || !history.Files[0].WholeFile || !strings.Contains(output, "evolving-original-body renamed failure") || !strings.Contains(output, "evolving-original-cleanup") || !strings.Contains(output, "evolving-unrelated-body") || strings.Contains(output, "ambiguous rerun suite") || strings.Contains(output, "evolving-later-file") || !strings.Contains(output, "evolving-check.km") {
					t.Fatalf("whole-file rerun missed original failure: status=%d history=%#v err=%v %s", status, history, err, output)
				}
				status, output = invoke(true, true, "--rerun-failed", historyPath, "evolving_rerun")
				history, err = readFailureHistory(historyPath)
				if status != 0 || err != nil || len(history.Files) != 0 || !strings.Contains(output, "evolving-original-body renamed failure") || strings.Contains(output, "evolving-later-file") {
					t.Fatalf("fixed whole-file rerun failed: status=%d history=%#v err=%v %s", status, history, err, output)
				}
			})
		}
	}
}
