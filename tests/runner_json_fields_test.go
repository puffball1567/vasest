package main

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestJSONFieldReportsRejectNativeOverrides(t *testing.T) {
	valid := `{"success":true,"summary":{"suite":"suite","passed":1,"failed":0,"skipped":0,"todo":0,"results":[{"name":"case","passed":true,"status":"pass"}]}}`
	for _, replace := range [][2]string{
		{`"success":true`, `"success":false,"success":true`},
		{`"success":true`, `"success":false,"SUCCESS":true`},
		{`"success":true`, `"success":false,"\u0073uccess":true`},
		{`"success":true`, `"success":false,"ſuccess":true`},
		{`"summary":`, `"summary":{"suite":"hidden","failed":1,"results":[{"name":"hidden","status":"fail","passed":false}]},"Summary":`},
		{`"suite":"suite"`, `"suite":"hidden","suite":"suite"`},
		{`"passed":1`, `"passed":0,"Passed":1`},
		{`"name":"case"`, `"name":"hidden","na\u006de":"case"`},
		{`"passed":true`, `"passed":false,"PASSED":true`},
		{`"status":"pass"`, `"status":"fail","ſtatus":"pass"`},
	} {
		data := strings.Replace(valid, replace[0], replace[1], 1)
		if !json.Valid([]byte(data)) {
			t.Fatal("probe must isolate fields from JSON syntax", data)
		}
		if _, err := readSuiteReport(data); err == nil || !strings.Contains(err.Error(), "duplicate JSON field") {
			t.Fatalf("report override accepted: %s %v", data, err)
		}
	}
	failed := `{"success":false,"summary":{"suite":"suite","passed":0,"failed":1,"results":[{"name":"case","passed":false,"status":"fail","failures":[{"phase":"body","message":"failure","location":{"file":"domain.km","line":42,"source":"explicit"}}]}]}}`
	for _, replace := range [][2]string{
		{`"phase":"body"`, `"phase":"beforeAll","phase":"body"`},
		{`"message":"failure"`, `"message":"hidden","Message":"failure"`},
		{`"file":"domain.km"`, `"file":"hidden.km","file":"domain.km"`},
		{`"line":42`, `"line":1,"Line":42`},
		{`"source":"explicit"`, `"source":"go","ſource":"explicit"`},
	} {
		data := strings.Replace(failed, replace[0], replace[1], 1)
		if _, err := readSuiteReport(data); err == nil || !strings.Contains(err.Error(), "duplicate JSON field") {
			t.Fatalf("failure detail override accepted: %s %v", data, err)
		}
	}
}

func TestJSONFieldHistoriesRejectOverriddenSelections(t *testing.T) {
	valid := `{"version":1,"files":[{"file":"probe.test.km","suites":[{"name":"suite","cases":["case"]}]}]}`
	for _, replace := range [][2]string{
		{`"version":1`, `"version":2,"Version":1`},
		{`"files":`, `"files":[],"fi\u006ces":`},
		{`"file":"probe.test.km"`, `"file":"hidden.test.km","File":"probe.test.km"`},
		{`"suites":`, `"suites":[],"ſuites":`},
		{`"name":"suite"`, `"name":"hidden","NAME":"suite"`},
		{`"cases":["case"]`, `"cases":["hidden"],"caſes":["case"]`},
	} {
		path := filepath.Join(t.TempDir(), "failures.json")
		data := strings.Replace(valid, replace[0], replace[1], 1)
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := readFailureHistory(path); err == nil || !strings.Contains(err.Error(), "duplicate JSON field") {
			t.Fatalf("history override accepted: %s %v", data, err)
		}
	}
}

func TestJSONFieldReadersPreserveScopesAndNativeAliases(t *testing.T) {
	data := `{"SUCCESS":true,"SUMMARY":{"SUITE":"suite","PASSED":2,"RESULTS":[{"NAME":"first","PASSED":true,"STATUS":"pass"},{"NAME":"second","PASSED":true,"STATUS":"pass"}],"extension":{"wide":1e999,"items":[{"name":"first"},{"name":"second"}],"message":"{\"name\":1,\"name\":2}"}}}`
	report, err := readSuiteReport(data)
	if err != nil || report.Summary.Passed != 2 || report.Summary.Results[0].Name != "first" || report.Summary.Results[1].Name != "second" {
		t.Fatalf("separate objects or native aliases rejected: %#v %v", report, err)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"VERSION":1,"FILTER":"{\"filter\":1,\"filter\":2}","INCLUDE":["**/*.test.km"],"JOBS":2}`), 0600); err != nil {
		t.Fatal(err)
	}
	config, err := readRunnerConfig(path)
	if err != nil || config.Jobs == nil || *config.Jobs != 2 || config.Filter == nil || *config.Filter != `{"filter":1,"filter":2}` {
		t.Fatalf("valid aliases or literal JSON text rejected: %#v %v", config, err)
	}
	if err := os.WriteFile(path, []byte(`{"VERSION":1,"FILES":[{"FILE":"probe.test.km","SUITES":[{"NAME":"first","CASES":["case"]},{"NAME":"second","CASES":["case"]}]}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	history, err := readFailureHistory(path)
	if err != nil || len(history.Files) != 1 || len(history.Files[0].Suites) != 2 {
		t.Fatalf("separate suites rejected: %#v %v", history, err)
	}
	for _, valid := range []string{`null`, `true`, `42`, `"{\"name\":1,\"name\":2}"`, `[]`, `{}`, `{"objects":[{"key":null},{"key":false}],"empty":{},"numbers":[1e999,1152921504606846977,18446744073709551615]}`} {
		if err := duplicateJSONFieldError(valid); err != nil {
			t.Fatalf("valid token shape rejected: %s %v", valid, err)
		}
	}
	for _, invalid := range []string{`{"name":`, `{"name":1,}`, `{"name" true}`, `]`, `{"items":[}`} {
		if _, err := readSuiteReport(invalid); err == nil {
			t.Fatal("malformed JSON accepted", invalid)
		}
	}
}

func TestJSONFieldInputsDoNotLaunchTestsOrReplaceHistory(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "probe.test.km"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	historyPath, configPath := filepath.Join(directory, "failures.json"), filepath.Join(directory, "config.json")
	history := []byte(`{"version":1,"files":[{"file":"probe.test.km","suites":[{"name":"suite","cases":["hidden"],"Cases":["case"]}]}]}`)
	if err := os.WriteFile(historyPath, history, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KEIKA", filepath.Join(directory, "compiler-must-not-run"))
	for _, reporter := range []string{"text", "json", "junit"} {
		for _, field := range []string{"history", "filter", "jobs", "include"} {
			args := []string{"--reporter", reporter, "--save-failures", historyPath}
			if field == "history" {
				args = append(args, "--no-config", "--rerun-failed", historyPath)
			} else {
				config := map[string]string{
					"filter":  `{"version":1,"filter":"hidden","\u0066ilter":"case"}`,
					"jobs":    `{"version":1,"jobs":1,"jobſ":2}`,
					"include": `{"version":1,"include":["hidden.test.km"],"INCLUDE":["probe.test.km"]}`,
				}[field]
				if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
					t.Fatal(err)
				}
				args = append(args, "--config", configPath)
			}
			status, output := captureRunner(t, append(args, directory))
			after, err := os.ReadFile(historyPath)
			if status != 2 || !strings.Contains(output, "duplicate JSON field") || strings.Contains(output, "FILE ") || err != nil || string(after) != string(history) {
				t.Fatalf("ambiguous input reached tests or replaced history: %s/%s status=%d err=%v %s", reporter, field, status, err, output)
			}
		}
	}
}

func TestJSONFieldReportRetainsEarlierResultsAndRawInput(t *testing.T) {
	t.Setenv("VASEST_RUNNER_HELPER", "1")
	valid := `{"success":true,"summary":{"suite":"suite","passed":1,"results":[{"name":"case","passed":true,"status":"pass"}]}}`
	bad := strings.Replace(valid, `"success":true`, `"success":false,"Success":true`, 1)
	data := valid + "\n" + bad
	t.Setenv("VASEST_RUNNER_REPORT", data)
	file := filepath.Join(t.TempDir(), "probe.test.km")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	execution := executeTestFile(fakeKeika(t), file, "", "", false, false, false, 2*time.Second, "", "json", nil, "", "", "", nil)
	if execution.runError != nil || execution.fatalError != nil || len(execution.suites) != 1 || execution.suites[0].raw != valid || execution.rawReport != data+"\n" || !strings.Contains(execution.reportFailure, "duplicate JSON field") {
		t.Fatalf("ambiguous report erased earlier/raw input: %#v", execution)
	}
	if entry := failedFile(file, execution); !entry.WholeFile || len(entry.Suites) != 0 {
		t.Fatalf("ambiguous report retained an exact selection: %#v", entry)
	}
}

func TestJSONFieldsRealKinmokusei(t *testing.T) {
	runner, consumer := os.Getenv("VASEST_INTEGRATION_RUNNER"), os.Getenv("VASEST_INTEGRATION_CONSUMER")
	if runner == "" || consumer == "" {
		t.Skip("requires the external Kinmokusei project")
	}
	for _, reporter := range []string{"text", "json", "junit"} {
		for _, key := range []string{"success", "SUCCESS", `\u0073uccess`, "ſuccess"} {
			t.Run(reporter+"/"+key, func(t *testing.T) {
				historyPath := filepath.Join(t.TempDir(), "failures.json")
				artifacts := filepath.Join(t.TempDir(), "artifacts")
				invoke := func(fixed bool, args ...string) (int, string) {
					t.Helper()
					ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
					defer cancel()
					command := exec.CommandContext(ctx, runner, append([]string{"--no-config", "--reporter", reporter, "--save-failures", historyPath, "--artifacts", artifacts}, args...)...)
					command.Dir = consumer
					flag := "0"
					if fixed {
						flag = "1"
					}
					command.Env = append(os.Environ(), "VASEST_JSON_FIELDS_FIXED="+flag, "VASEST_JSON_FIELDS_KEY="+key)
					command.WaitDelay = time.Second
					output, err := command.CombinedOutput()
					if err == nil {
						return 0, string(output)
					}
					var exit *exec.ExitError
					if !errors.As(err, &exit) {
						t.Fatalf("JSON fields: %v %s", err, output)
					}
					return exit.ExitCode(), string(output)
				}
				status, output := invoke(false, "--jobs", "2", "--repeat", "2", "json_fields")
				if status != 1 || !strings.Contains(output, "duplicate JSON field") || !strings.Contains(output, "json-fields-completed") || !strings.Contains(output, "json-fields-cleanup") || strings.Contains(output, "json-fields-selection-ran") {
					t.Fatalf("ambiguous JSON report or selection reached wrong outcome: %d %s", status, output)
				}
				history, err := readFailureHistory(historyPath)
				if err != nil || len(history.Files) != 1 || !history.Files[0].WholeFile || len(history.Files[0].Suites) != 0 {
					t.Fatalf("ambiguous report saved exact cases: %#v %v", history, err)
				}
				runs, err := os.ReadDir(artifacts)
				if err != nil || len(runs) != 2 {
					t.Fatalf("repeat artifacts missing: %v %v", runs, err)
				}
				failures := 0
				for _, run := range runs {
					manifest := readArtifactResults(t, filepath.Join(artifacts, run.Name(), "manifest.json"))
					for _, file := range manifest.Files {
						if file.Status != "fail" {
							continue
						}
						failures++
						data, err := os.ReadFile(file.Report)
						if err != nil || !strings.Contains(string(data), `"success":false,"`+key+`":true`) || !strings.Contains(file.Failure, "duplicate JSON field") {
							t.Fatalf("raw duplicate fields or file diagnostics lost: %#v %v %s", file, err, data)
						}
					}
				}
				if failures != 2 {
					t.Fatal("repeat lost ambiguous report failures", failures)
				}
				if reporter == "json" {
					for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
						var event map[string]json.RawMessage
						if err := json.Unmarshal([]byte(line), &event); err != nil || strings.Contains(line, `"hidden report"`) {
							t.Fatalf("invalid report forwarded as event: %v %s", err, line)
						}
					}
				}
				if reporter == "junit" {
					var document fileErrorJUnitDocument
					if err := xml.Unmarshal([]byte(output), &document); err != nil || document.Failures != 2 {
						t.Fatalf("invalid JUnit file failures: %#v %v %s", document, err, output)
					}
				}
				status, output = invoke(true, "--rerun-failed", historyPath, "json_fields")
				history, err = readFailureHistory(historyPath)
				if status != 0 || err != nil || len(history.Files) != 0 || !strings.Contains(output, "json-fields-completed") || strings.Contains(output, "json-fields-unrelated") || strings.Contains(output, "json-fields-selection-ran") {
					t.Fatalf("fixed whole-file rerun failed: %d %#v %v %s", status, history, err, output)
				}
			})
		}
	}
}
