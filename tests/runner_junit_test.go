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

func TestJUnitRetainsFileFailuresAlongsideFailedCases(t *testing.T) {
	failed := JUnitCase{name: "assertion", classname: "suite", status: "fail", message: "assertion mismatch"}
	passed := JUnitCase{name: "later", classname: "suite", status: "pass"}
	for _, tc := range []struct {
		name, report, message string
		process               error
	}{
		{"process", "suite reported failed tests", "exit status 2", errors.New("exit status 2")},
		{"invalid report", "invalid suite report: broken JSON", "invalid suite report: broken JSON", nil},
		{"both", "invalid suite report: broken JSON", "exit status 2\ninvalid suite report: broken JSON", errors.New("exit status 2")},
		{"empty process message", "suite reported failed tests", "", errors.New("")},
		{"empty process and invalid report", "invalid suite report: broken JSON", "invalid suite report: broken JSON", errors.New("")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file := junitFile("file.test.km", "retained output", 42, []JUnitCase{failed, passed}, tc.process, tc.report)
			if len(file.cases) != 3 || file.cases[0].message != failed.message || file.cases[1].status != "pass" {
				t.Fatalf("independent file error missing or cases changed: %#v", file)
			}
			additional := file.cases[2]
			if additional.name != "<file>" || additional.classname != "file.test.km" || additional.status != "fail" || additional.message != tc.message || additional.durationNs != 0 || len(additional.failures) != 0 || file.output != "retained output" || file.durationNs != 42 {
				t.Fatalf("file error changed: %#v", file)
			}
		})
	}
}

func TestFileFailureMessagesPreserveIndependentCauses(t *testing.T) {
	for _, tc := range []struct {
		process      error
		report, want string
	}{
		{nil, "", ""},
		{nil, "suite reported failed tests", "suite reported failed tests"},
		{nil, "invalid suite report: broken JSON", "invalid suite report: broken JSON"},
		{errors.New("exit status 2"), "", "exit status 2"},
		{errors.New("exit status 2"), "suite reported failed tests", "exit status 2"},
		{errors.New("exit status 2"), "invalid suite report: broken JSON", "exit status 2\ninvalid suite report: broken JSON"},
		{errors.New("timed out after 500ms"), "invalid suite report: broken JSON", "timed out after 500ms\ninvalid suite report: broken JSON"},
		{errors.New(""), "", ""},
		{errors.New(""), "suite reported failed tests", ""},
		{errors.New(""), "invalid suite report: broken JSON", "invalid suite report: broken JSON"},
		{errors.New("金木犀 <&>\nsecond line"), "report <&>\nthird line", "金木犀 <&>\nsecond line\nreport <&>\nthird line"},
	} {
		if got := fileFailureMessage(tc.process, tc.report); got != tc.want {
			t.Fatalf("process=%v report=%q got=%q want=%q", tc.process, tc.report, got, tc.want)
		}
		file := junitFile("file.test.km", "retained output", 42, nil, tc.process, tc.report)
		if tc.process == nil && tc.report == "" {
			if len(file.cases) != 0 {
				t.Fatalf("passing file gained failure: %#v", file)
			}
		} else if len(file.cases) != 1 || file.cases[0].message != tc.want || file.cases[0].status != "fail" {
			t.Fatalf("JUnit disagrees with the file diagnostic: %#v want=%q", file, tc.want)
		}
	}
}

func TestJUnitDoesNotDuplicateReportedCaseFailures(t *testing.T) {
	failed := JUnitCase{name: "assertion", classname: "suite", status: "fail", message: "assertion mismatch"}
	file := junitFile("file.test.km", "", 0, []JUnitCase{failed}, nil, "suite reported failed tests")
	if len(file.cases) != 1 || file.cases[0].message != failed.message {
		t.Fatalf("duplicated assertion failure: %#v", file)
	}
	for _, report := range []string{"", "suite reported failed tests", "invalid suite report"} {
		file := junitFile("file.test.km", "", 0, nil, errors.New("process failure"), report)
		if len(file.cases) != 1 || file.cases[0].name != "<file>" || !strings.Contains(file.cases[0].message, "process failure") {
			t.Fatalf("missing standalone file failure: %#v", file)
		}
	}
}

type nonComparableFileError []string

func (e nonComparableFileError) Error() string { return strings.Join(e, ": ") }

func TestFileExecutionErrorsPreserveCausesWithoutDuplicatingSharedErrors(t *testing.T) {
	process := errors.New("exit status 2")
	fatal := &os.PathError{Op: "read", Path: "report", Err: os.ErrPermission}
	for _, tc := range []struct {
		name           string
		process, fatal error
		want           string
	}{
		{"process only", process, nil, process.Error()},
		{"fatal only", nil, fatal, fatal.Error()},
		{"both", process, fatal, process.Error() + "\n" + fatal.Error()},
		{"shared archive error", fatal, fatal, fatal.Error()},
		{"wrapped process", fmt.Errorf("build: %w", fatal), fatal, "build: " + fatal.Error()},
		{"wrapped fatal", process, fmt.Errorf("read: %w", process), "read: " + process.Error()},
		{"additional fatal", process, errors.Join(process, fatal), process.Error() + "\n" + fatal.Error()},
		{"equal messages independent causes", process, errors.New(process.Error()), process.Error() + "\n" + process.Error()},
		{"non-comparable errors", nonComparableFileError{"process", "failed"}, nonComparableFileError{"report", "unreadable"}, "process: failed\nreport: unreadable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := fileExecutionError(FileExecution{runError: tc.process, fatalError: tc.fatal})
			if got == nil || got.Error() != tc.want {
				t.Fatalf("lost or duplicated cause: got=%v want=%q", got, tc.want)
			}
			if tc.name != "non-comparable errors" && (!errors.Is(got, tc.process) && tc.process != nil || !errors.Is(got, tc.fatal) && tc.fatal != nil) {
				t.Fatalf("lost underlying error identity: %v", got)
			}
			if errors.Is(tc.fatal, os.ErrPermission) {
				var path *os.PathError
				if !errors.As(got, &path) || path != fatal {
					t.Fatalf("lost typed error: %v", got)
				}
			}
		})
	}
	if got := fileExecutionError(FileExecution{}); got != nil {
		t.Fatalf("passing execution gained an error: %v", got)
	}
}

type fileErrorJUnitDocument struct {
	XMLName  xml.Name `xml:"testsuites"`
	Tests    int      `xml:"tests,attr"`
	Failures int      `xml:"failures,attr"`
	Skipped  int      `xml:"skipped,attr"`
	Suites   []struct {
		Name  string `xml:"name,attr"`
		Cases []struct {
			Name      string `xml:"name,attr"`
			Classname string `xml:"classname,attr"`
			Failure   *struct {
				Message string `xml:"message,attr"`
				Text    string `xml:",chardata"`
			} `xml:"failure"`
			Skipped *struct{} `xml:"skipped"`
		} `xml:"testcase"`
		Output string `xml:"system-out"`
	} `xml:"testsuite"`
}

func TestJUnitFileErrorsRealKinmokusei(t *testing.T) {
	runner, consumer := os.Getenv("VASEST_INTEGRATION_RUNNER"), os.Getenv("VASEST_INTEGRATION_CONSUMER")
	if runner == "" || consumer == "" {
		t.Skip("requires the external Kinmokusei project")
	}
	invoke := func(t *testing.T, mode string, args ...string) (int, string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, runner, append([]string{"--no-config", "--timeout", "500ms"}, args...)...)
		command.Dir = consumer
		command.Env = append(os.Environ(), "VASEST_JUNIT_FILE_MODE="+mode)
		command.WaitDelay = time.Second
		output, err := command.CombinedOutput()
		if err == nil {
			return 0, string(output)
		}
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("file error integration: %v %s", err, output)
		}
		return exit.ExitCode(), string(output)
	}
	for _, reporter := range []string{"text", "json", "junit"} {
		for _, mode := range []string{"framework", "unchecked", "panic", "exit1", "exit2", "invalid", "both", "timeout", "invalid-panic", "invalid-timeout"} {
			t.Run(reporter+"/"+mode, func(t *testing.T) {
				historyPath := filepath.Join(t.TempDir(), "failures.json")
				jobs := "1"
				if mode == "panic" || mode == "both" {
					jobs = "2"
				}
				status, output := invoke(t, mode, "--reporter", reporter, "--jobs", jobs, "--save-failures", historyPath, "junit_file_errors")
				if status != 1 || !strings.Contains(output, "junit-file-cleanup") || !strings.Contains(output, "junit-file-later-case") || !strings.Contains(output, "junit-file-later-file") {
					t.Fatalf("status=%d output=%s", status, output)
				}
				independent := mode != "framework" && mode != "unchecked"
				invalidReport := mode == "invalid" || mode == "both" || mode == "invalid-panic" || mode == "invalid-timeout"
				exitTwo := mode == "panic" || mode == "exit2" || mode == "both" || mode == "invalid-panic"
				timedOut := mode == "timeout" || mode == "invalid-timeout"
				checkMessage := func(message string) {
					t.Helper()
					if invalidReport && !strings.Contains(message, "invalid suite report:") || exitTwo && !strings.Contains(message, "exit status 2") || timedOut && !strings.Contains(message, "timed out after 500ms") {
						t.Fatalf("file diagnostic lost an independent cause: mode=%s message=%q", mode, message)
					}
				}
				history, err := readFailureHistory(historyPath)
				if err != nil || len(history.Files) != 1 || history.Files[0].WholeFile != independent || filepath.Base(history.Files[0].File) != "a_failure.test.km" {
					t.Fatalf("wrong history: %#v %v %s", history, err, output)
				}
				if !independent && (len(history.Files[0].Suites) != 1 || fmt.Sprint(history.Files[0].Suites[0].Cases) != "[early assertion]") {
					t.Fatalf("case failure lost exact selection: %#v", history)
				}
				if independent && len(history.Files[0].Suites) != 0 {
					t.Fatalf("file failure retained case-only selection: %#v", history)
				}
				if reporter == "text" && !strings.Contains(output, "1 file(s) passed, 1 failed, 0 not run") {
					t.Fatal("wrong text counts", output)
				}
				if reporter == "text" {
					checkMessage(output)
				}
				if reporter == "json" {
					files, suites := 0, 0
					for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
						var event struct {
							Type, Status, Path, Error string
							Report                    SuiteReport
							PassedFiles, FailedFiles  int
						}
						if err := json.Unmarshal([]byte(line), &event); err != nil {
							t.Fatalf("polluted JSON: %v %s", err, output)
						}
						if event.Type == "suite" {
							suites++
							if !event.Report.Success {
								first := event.Report.Summary.Results[0]
								if first.Name != "early assertion" || len(first.Failures) != 1 || first.Failures[0].Phase != "body" || first.Failures[0].Location != (LocationReport{File: "domain-check.km", Line: 42, Source: "explicit"}) {
									t.Fatalf("assertion details changed: %#v", first)
								}
							}
						}
						if event.Type == "file" {
							files++
							if strings.Contains(event.Path, "a_failure") && event.Status != "fail" || strings.Contains(event.Path, "z_passing") && event.Status != "pass" {
								t.Fatalf("wrong file status: %#v", event)
							}
							if strings.Contains(event.Path, "a_failure") {
								checkMessage(event.Error)
							}
						}
						if event.Type == "summary" && (event.PassedFiles != 1 || event.FailedFiles != 1) {
							t.Fatalf("wrong file summary: %#v", event)
						}
					}
					if files != 2 || suites != 2 {
						t.Fatal("missing valid reports", output)
					}
				}
				if reporter == "junit" {
					var document fileErrorJUnitDocument
					if err := xml.Unmarshal([]byte(output), &document); err != nil {
						t.Fatalf("polluted JUnit: %v %s", err, output)
					}
					wantTests, wantFailures := 3, 1
					if independent {
						wantTests, wantFailures = 4, 2
					}
					if document.Tests != wantTests || document.Failures != wantFailures || len(document.Suites) != 2 || len(document.Suites[0].Cases) != wantTests-1 {
						t.Fatalf("lost or duplicated failures: %#v %s", document, output)
					}
					first := document.Suites[0].Cases[0]
					if first.Name != "early assertion" || first.Failure == nil || !strings.Contains(first.Failure.Text, "[body]") || !strings.Contains(first.Failure.Text, "domain-check.km:42 (explicit)") {
						t.Fatalf("assertion details changed: %#v", first)
					}
					if independent {
						additional := document.Suites[0].Cases[2]
						if additional.Name != "<file>" || additional.Classname != "junit_file_errors/a_failure.test.km" || additional.Failure == nil || additional.Failure.Text != additional.Failure.Message {
							t.Fatalf("file error missing: %#v", additional)
						}
						message := additional.Failure.Message
						checkMessage(message)
						if mode == "exit1" && message != "exit status 1" || mode == "timeout" && message != "timed out after 500ms" {
							t.Fatalf("file diagnostic changed: %q", message)
						}
					}
				}
				if (mode == "panic" || mode == "invalid-panic") && !strings.Contains(output, "reflect: call of reflect.Value.Index on int Value") {
					t.Fatal("lost native panic output", output)
				}
				if mode == "both" || mode == "invalid-panic" || mode == "invalid-timeout" {
					t.Setenv("VASEST_JUNIT_FILE_FIXED", "1")
					status, output = invoke(t, mode, "--reporter", reporter, "--rerun-failed", historyPath, "--save-failures", historyPath, "junit_file_errors")
					history, err = readFailureHistory(historyPath)
					if status != 0 || err != nil || len(history.Files) != 0 || !strings.Contains(output, "junit-file-fixed") || strings.Contains(output, "junit-file-later-file") {
						t.Fatalf("file rerun failed: status=%d history=%#v err=%v %s", status, history, err, output)
					}
				}
			})
		}
	}
	t.Run("bail", func(t *testing.T) {
		status, output := invoke(t, "both", "--bail", "--reporter", "junit", "junit_file_errors")
		var document fileErrorJUnitDocument
		if err := xml.Unmarshal([]byte(output), &document); status != 1 || err != nil || document.Tests != 3 || document.Failures != 2 || document.Skipped != 1 || len(document.Suites) != 1 || strings.Contains(output, "junit-file-later-case") || strings.Contains(output, "junit-file-later-file") || !strings.Contains(output, "junit-file-cleanup") {
			t.Fatalf("bail lost file/case errors: status=%d document=%#v err=%v %s", status, document, err, output)
		}
	})
	t.Run("parallel repeat", func(t *testing.T) {
		status, output := invoke(t, "both", "--jobs", "2", "--repeat", "2", "--reporter", "junit", "junit_file_errors")
		var document fileErrorJUnitDocument
		if err := xml.Unmarshal([]byte(output), &document); status != 1 || err != nil || document.Tests != 8 || document.Failures != 4 || len(document.Suites) != 4 {
			t.Fatalf("repeat lost file/case errors: status=%d document=%#v err=%v %s", status, document, err, output)
		}
		for _, suite := range document.Suites {
			if strings.Contains(suite.Name, "a_failure") && (len(suite.Cases) != 3 || suite.Cases[2].Name != "<file>" || suite.Cases[2].Failure == nil || !strings.Contains(suite.Cases[2].Failure.Message, "invalid suite report:")) {
				t.Fatalf("missing repeated file failure: %#v", suite)
			}
		}
	})
}

func TestUnreadableReportsRealKinmokusei(t *testing.T) {
	runner, consumer := os.Getenv("VASEST_INTEGRATION_RUNNER"), os.Getenv("VASEST_INTEGRATION_CONSUMER")
	if runner == "" || consumer == "" {
		t.Skip("requires the external Kinmokusei project")
	}
	for _, reporter := range []string{"text", "json", "junit"} {
		for _, mode := range []string{"unreadable", "unreadable-exit2", "unreadable-panic", "unreadable-timeout"} {
			t.Run(reporter+"/"+mode, func(t *testing.T) {
				historyPath := filepath.Join(t.TempDir(), "failures.json")
				invoke := func(fixed bool, args ...string) (int, string) {
					t.Helper()
					ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
					defer cancel()
					command := exec.CommandContext(ctx, runner, append([]string{"--no-config", "--reporter", reporter, "--timeout", "500ms", "--save-failures", historyPath}, args...)...)
					command.Dir = consumer
					fixedValue := "0"
					if fixed {
						fixedValue = "1"
					}
					command.Env = append(os.Environ(), "VASEST_JUNIT_FILE_MODE="+mode, "VASEST_JUNIT_FILE_FIXED="+fixedValue)
					command.WaitDelay = time.Second
					output, err := command.CombinedOutput()
					if err == nil {
						return 0, string(output)
					}
					var exit *exec.ExitError
					if !errors.As(err, &exit) {
						t.Fatalf("unreadable report integration: %v %s", err, output)
					}
					return exit.ExitCode(), string(output)
				}
				status, output := invoke(false, "--repeat", "2", "junit_file_errors")
				if status != 2 || !strings.Contains(output, "junit-file-cleanup") || !strings.Contains(output, "junit-file-later-case") || strings.Contains(output, "junit-file-later-file") {
					t.Fatalf("infrastructure failure lost output or scheduled more work: status=%d %s", status, output)
				}
				checkMessage := func(message string) {
					t.Helper()
					if !strings.Contains(message, "cannot read test report:") || ((mode == "unreadable-exit2" || mode == "unreadable-panic") && !strings.Contains(message, "exit status 2")) || (mode == "unreadable-timeout" && !strings.Contains(message, "timed out after 500ms")) {
						t.Fatalf("lost independent cause: mode=%s message=%q", mode, message)
					}
				}
				if reporter == "text" {
					checkMessage(output)
					if strings.Count(output, "FILE FAILED") != 1 || !strings.Contains(output, "0 file(s) passed, 1 failed, 3 not run") {
						t.Fatal("wrong text counts", output)
					}
				}
				if reporter == "json" {
					files, summaries := 0, 0
					for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
						var event struct {
							Type, Status, Error                                   string
							PassedFiles, FailedFiles, NotRunFiles, RepeatsStarted int
						}
						if err := json.Unmarshal([]byte(line), &event); err != nil {
							t.Fatalf("polluted JSON: %v %s", err, output)
						}
						if event.Type == "suite" {
							t.Fatal("unreadable report fabricated case results", output)
						}
						if event.Type == "file" {
							files++
							checkMessage(event.Error)
							if event.Status != "fail" {
								t.Fatal("wrong file status", output)
							}
						}
						if event.Type == "summary" {
							summaries++
							if event.PassedFiles != 0 || event.FailedFiles != 1 || event.NotRunFiles != 3 || event.RepeatsStarted != 1 {
								t.Fatalf("infrastructure error did not stop repeat: %#v", event)
							}
						}
					}
					if files != 1 || summaries != 1 {
						t.Fatal("missing file/summary", output)
					}
				}
				if reporter == "junit" {
					var document fileErrorJUnitDocument
					if err := xml.Unmarshal([]byte(output), &document); err != nil || document.Tests != 1 || document.Failures != 1 || len(document.Suites) != 1 || len(document.Suites[0].Cases) != 1 {
						t.Fatalf("lost file failure or fabricated cases: %#v %v %s", document, err, output)
					}
					failure := document.Suites[0].Cases[0]
					if failure.Name != "<file>" || failure.Failure == nil || failure.Failure.Text != failure.Failure.Message {
						t.Fatalf("wrong file failure: %#v", failure)
					}
					checkMessage(failure.Failure.Message)
				}
				if mode == "unreadable-panic" && !strings.Contains(output, "reflect: call of reflect.Value.Index on int Value") {
					t.Fatal("lost native panic output", output)
				}
				history, err := readFailureHistory(historyPath)
				if err != nil || len(history.Files) != 1 || !history.Files[0].WholeFile || len(history.Files[0].Suites) != 0 || filepath.Base(history.Files[0].File) != "a_failure.test.km" {
					t.Fatalf("unreadable report did not select whole file: %#v %v", history, err)
				}
				status, output = invoke(true, "--rerun-failed", historyPath, "junit_file_errors")
				history, err = readFailureHistory(historyPath)
				if status != 0 || err != nil || len(history.Files) != 0 || !strings.Contains(output, "junit-file-fixed") || strings.Contains(output, "junit-file-later-file") {
					t.Fatalf("failed to recover: status=%d history=%#v err=%v %s", status, history, err, output)
				}
			})
		}
	}
}
