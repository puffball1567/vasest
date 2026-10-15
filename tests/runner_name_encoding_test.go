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

func TestRunnerJSONUnicodeEscapesPreserveIdentifiers(t *testing.T) {
	for _, probe := range []struct{ encoded, decoded string }{
		{`\ufffd`, "�"}, {`�`, "�"}, {`\uD83D\uDE00`, "😀"},
		{`\ud800\udc00`, "𐀀"}, {`\udbff\udfff`, "\U0010ffff"},
		{`\\ud800`, `\ud800`}, {`\\\ud83d\ude00`, "\\😀"},
		{`\"\\ud800\/`, `"\ud800/`}, {`\u0061\ud83d\ude00\ufffd`, "a😀�"},
	} {
		line := `{"success":true,"summary":{"suite":"suite` + probe.encoded + `","passed":1,"failed":0,"skipped":0,"todo":0,"results":[{"name":"case` + probe.encoded + `","passed":true,"status":"pass"}]}}`
		report, err := readSuiteReport(line)
		if err != nil || report.Summary.Suite != "suite"+probe.decoded || len(report.Summary.Results) != 1 || report.Summary.Results[0].Name != "case"+probe.decoded {
			t.Fatalf("valid Unicode report changed names: %s %#v %v", line, report, err)
		}
		path := filepath.Join(t.TempDir(), "failures.json")
		data := `{"version":1,"files":[{"file":"probe.test.km","suites":[{"name":"suite` + probe.encoded + `","cases":["case` + probe.encoded + `"]}]}]}`
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		history, err := readFailureHistory(path)
		if err != nil || len(history.Files) != 1 || history.Files[0].Suites[0].Name != "suite"+probe.decoded || history.Files[0].Suites[0].Cases[0] != "case"+probe.decoded {
			t.Fatalf("valid Unicode history changed names: %s %#v %v", data, history, err)
		}
		if err := os.WriteFile(path, []byte(`{"version":1,"filter":"case`+probe.encoded+`"}`), 0600); err != nil {
			t.Fatal(err)
		}
		config, err := readRunnerConfig(path)
		if err != nil || config.Filter == nil || *config.Filter != "case"+probe.decoded {
			t.Fatalf("valid Unicode configuration changed filter: %#v %v", config, err)
		}
	}
}

func TestRunnerJSONUnicodeEscapesCannotAliasReplacementIdentifiers(t *testing.T) {
	valid := `{"success":true,"summary":{"suite":"suite�","passed":1,"failed":0,"skipped":0,"todo":0,"results":[{"name":"case�","passed":true,"status":"pass"}]}}`
	for _, escaped := range []string{`\ud800`, `\udbff`, `\udc00`, `\udfff`, `\uD800\u0041`, `\ud800\ud800`, `\ud800\\udc00`, `\udc00\ud800`} {
		for _, field := range []string{"suite", "case"} {
			line := strings.Replace(valid, field+"�", field+escaped, 1)
			if !json.Valid([]byte(line)) {
				t.Fatal("probe must have valid JSON syntax", line)
			}
			if _, err := readSuiteReport(line); err == nil || !strings.Contains(err.Error(), "paired Unicode surrogate escapes") {
				t.Fatalf("lossy Unicode report accepted: %s %v", line, err)
			}
		}
		for _, data := range []string{
			`{"version":1,"files":[{"file":"probe` + escaped + `.test.km","wholeFile":true}]}`,
			`{"version":1,"files":[{"file":"probe.test.km","suites":[{"name":"suite` + escaped + `","cases":["case"]}]}]}`,
			`{"version":1,"files":[{"file":"probe.test.km","suites":[{"name":"suite","cases":["case` + escaped + `"]}]}]}`,
		} {
			path := filepath.Join(t.TempDir(), "failures.json")
			if err := os.WriteFile(path, []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := readFailureHistory(path); err == nil || !strings.Contains(err.Error(), "paired Unicode surrogate escapes") {
				t.Fatalf("lossy Unicode history accepted: %s %v", data, err)
			}
		}
	}
}

func TestUnpairedJSONSurrogatesDoNotLaunchTestsOrReplaceHistory(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "probe.test.km"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	historyPath, configPath := filepath.Join(directory, "failures.json"), filepath.Join(directory, "config.json")
	history := []byte(`{"version":1,"files":[{"file":"probe.test.km","suites":[{"name":"suite","cases":["case\ud800"]}]}]}`)
	if err := os.WriteFile(historyPath, history, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(`{"version":1,"filter":"case\udc00"}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KEIKA", filepath.Join(directory, "compiler-must-not-run"))
	for _, reporter := range []string{"text", "json", "junit"} {
		for _, mode := range []string{"history", "configuration"} {
			args := []string{"--reporter", reporter, "--save-failures", historyPath}
			if mode == "history" {
				args = append(args, "--no-config", "--rerun-failed", historyPath)
			} else {
				args = append(args, "--config", configPath)
			}
			status, output := captureRunner(t, append(args, directory))
			after, err := os.ReadFile(historyPath)
			if status != 2 || !strings.Contains(output, "paired Unicode surrogate escapes") || strings.Contains(output, "FILE ") || err != nil || string(after) != string(history) {
				t.Fatalf("invalid Unicode input reached execution or replaced history: %s/%s status=%d err=%v %s", reporter, mode, status, err, output)
			}
		}
	}
}

func TestUnpairedJSONReportKeepsValidEarlierReportsAndRawEscapes(t *testing.T) {
	t.Setenv("VASEST_RUNNER_HELPER", "1")
	keika := fakeKeika(t)
	valid := `{"success":true,"summary":{"suite":"valid�","passed":1,"failed":0,"skipped":0,"todo":0,"results":[{"name":"case�","passed":true,"status":"pass"}]}}`
	data := valid + "\n" + strings.Replace(valid, "case�", `case\ud800`, 1)
	t.Setenv("VASEST_RUNNER_REPORT", data)
	file := filepath.Join(t.TempDir(), "probe.test.km")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	execution := executeTestFile(keika, file, "", "", false, false, false, 2*time.Second, "", "json", nil, "", "", "", nil)
	if execution.runError != nil || execution.fatalError != nil || len(execution.suites) != 1 || execution.suites[0].raw != valid || execution.rawReport != data+"\n" || !strings.Contains(execution.reportFailure, "paired Unicode surrogate escapes") {
		t.Fatalf("invalid Unicode report changed names or lost earlier/raw reports: %#v", execution)
	}
	entry := failedFile(file, execution)
	if !entry.WholeFile || len(entry.Suites) != 0 {
		t.Fatalf("invalid Unicode report retained a lossy exact selection: %#v", entry)
	}
}

func TestRunnerRejectsInvalidUTF8BeforeJSONReplacement(t *testing.T) {
	valid := `{"success":true,"summary":{"suite":"suite�","passed":1,"failed":0,"skipped":0,"todo":0,"results":[{"name":"case�","passed":true,"status":"pass"}]}}`
	if _, err := readSuiteReport(valid); err != nil {
		t.Fatal(err)
	}
	for _, data := range []string{
		strings.Replace(valid, "suite�", "suite\xff", 1),
		strings.Replace(valid, "case�", "case\xff", 1),
	} {
		if !json.Valid([]byte(data)) {
			t.Fatal("probe must isolate encoding from JSON syntax")
		}
		if _, err := readSuiteReport(data); err == nil || !strings.Contains(err.Error(), "valid UTF-8") {
			t.Fatalf("lossy suite report accepted: %v", err)
		}
	}
	for _, data := range []string{
		`{"version":1,"files":[{"file":"bad` + "\xff" + `.test.km","wholeFile":true}]}`,
		`{"version":1,"files":[{"file":"probe.test.km","suites":[{"name":"suite` + "\xff" + `","cases":["case"]}]}]}`,
		`{"version":1,"files":[{"file":"probe.test.km","suites":[{"name":"suite","cases":["case` + "\xff" + `"]}]}]}`,
	} {
		path := filepath.Join(t.TempDir(), "failures.json")
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := readFailureHistory(path); err == nil || !strings.Contains(err.Error(), "valid UTF-8") {
			t.Fatalf("lossy history accepted: %v", err)
		}
	}
	config := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(config, []byte(`{"version":1,"filter":"case`+"\xff"+`"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readRunnerConfig(config); err == nil || !strings.Contains(err.Error(), "valid UTF-8") {
		t.Fatalf("lossy configuration accepted: %v", err)
	}
}

func TestInvalidUTF8InputsDoNotLaunchTestsOrReplaceHistory(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "probe.test.km"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	historyPath, configPath := filepath.Join(directory, "failures.json"), filepath.Join(directory, "config.json")
	history := []byte(`{"version":1,"files":[{"file":"probe.test.km","suites":[{"name":"suite","cases":["case` + "\xff" + `"]}]}]}`)
	if err := os.WriteFile(historyPath, history, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(`{"version":1,"filter":"case`+"\xff"+`"}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KEIKA", filepath.Join(directory, "compiler-must-not-run"))
	for _, reporter := range []string{"text", "json", "junit"} {
		for _, mode := range []string{"history", "configuration"} {
			args := []string{"--reporter", reporter, "--save-failures", historyPath}
			if mode == "history" {
				args = append(args, "--no-config", "--rerun-failed", historyPath)
			} else {
				args = append(args, "--config", configPath)
			}
			status, output := captureRunner(t, append(args, directory))
			after, err := os.ReadFile(historyPath)
			if status != 2 || !strings.Contains(output, "valid UTF-8") || strings.Contains(output, "FILE ") || err != nil || string(after) != string(history) {
				t.Fatalf("invalid input reached execution or replaced history: %s/%s status=%d err=%v %s", reporter, mode, status, err, output)
			}
		}
	}
}

func TestInvalidUTF8ReportKeepsValidEarlierReportsAndRawBytes(t *testing.T) {
	t.Setenv("VASEST_RUNNER_HELPER", "1")
	keika := fakeKeika(t)
	valid := `{"success":true,"summary":{"suite":"valid�","passed":1,"failed":0,"skipped":0,"todo":0,"results":[{"name":"case�","passed":true,"status":"pass"}]}}`
	data := valid + "\n" + strings.Replace(valid, "case�", "case\xff", 1)
	t.Setenv("VASEST_RUNNER_REPORT", data)
	file := filepath.Join(t.TempDir(), "probe.test.km")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	execution := executeTestFile(keika, file, "", "", false, false, false, 2*time.Second, "", "json", nil, "", "", "", nil)
	if execution.runError != nil || execution.fatalError != nil || len(execution.suites) != 1 || execution.suites[0].raw != valid || execution.rawReport != data+"\n" || !strings.Contains(execution.reportFailure, "suite report must be valid UTF-8") {
		t.Fatalf("invalid report replaced names or lost earlier/raw reports: %#v", execution)
	}
	entry := failedFile(file, execution)
	if !entry.WholeFile || len(entry.Suites) != 0 {
		t.Fatalf("invalid report retained a lossy exact selection: %#v", entry)
	}
}

func TestInvalidUTF8TestPathsCannotAliasValidReplacementPaths(t *testing.T) {
	directory := t.TempDir()
	bad := filepath.Join(directory, "test\xff.test.km")
	good := filepath.Join(directory, "test�.test.km")
	for _, path := range []string{bad, good} {
		if err := os.WriteFile(path, nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	historyPath := filepath.Join(directory, "failures.json")
	original := []byte(`{"version":1,"files":[]}`)
	if err := os.WriteFile(historyPath, original, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KEIKA", filepath.Join(directory, "compiler-must-not-run"))
	for _, reporter := range []string{"text", "json", "junit"} {
		for _, path := range []string{bad, directory} {
			status, output := captureRunner(t, []string{"--no-config", "--reporter", reporter, "--save-failures", historyPath, path})
			after, err := os.ReadFile(historyPath)
			if status != 2 || !strings.Contains(output, "test file paths must be valid UTF-8") || strings.Contains(output, "FILE ") || err != nil || string(after) != string(original) {
				t.Fatalf("bad path reached execution or changed history: status=%d err=%v %s", status, err, output)
			}
		}
	}
	status, output := captureRunner(t, []string{"--no-config", "--list", "--include", "test�.test.km", directory})
	if status != 0 || strings.TrimSpace(output) != good {
		t.Fatalf("excluded invalid path blocked valid selection: status=%d %s", status, output)
	}
	if err := saveFailureHistory(historyPath, []FailedFile{{File: bad, WholeFile: true}}); err == nil || !strings.Contains(err.Error(), "file paths must be valid UTF-8") {
		t.Fatalf("lossy relative path was saved: %v", err)
	}
	after, err := os.ReadFile(historyPath)
	if err != nil || string(after) != string(original) {
		t.Fatalf("rejected save replaced earlier history: %v %s", err, after)
	}
}

func TestNameEncodingRealKinmokusei(t *testing.T) {
	runner, consumer := os.Getenv("VASEST_INTEGRATION_RUNNER"), os.Getenv("VASEST_INTEGRATION_CONSUMER")
	if runner == "" || consumer == "" {
		t.Skip("requires the external Kinmokusei project")
	}
	for _, reporter := range []string{"text", "json", "junit"} {
		t.Run(reporter, func(t *testing.T) {
			historyPath := filepath.Join(t.TempDir(), "failures.json")
			invoke := func(fixed bool, args ...string) (int, string) {
				t.Helper()
				ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
				defer cancel()
				command := exec.CommandContext(ctx, runner, append([]string{"--no-config", "--reporter", reporter, "--save-failures", historyPath}, args...)...)
				command.Dir = consumer
				flag := "0"
				if fixed {
					flag = "1"
				}
				command.Env = append(os.Environ(), "VASEST_ENCODING_FIXED="+flag)
				command.WaitDelay = time.Second
				output, err := command.CombinedOutput()
				if err == nil {
					return 0, string(output)
				}
				var exit *exec.ExitError
				if !errors.As(err, &exit) {
					t.Fatalf("name encoding: %v %s", err, output)
				}
				return exit.ExitCode(), string(output)
			}
			status, output := invoke(false, "--jobs", "2", "--repeat", "2", "name_encoding")
			if status != 1 || !strings.Contains(output, "encoding-values-preserved") || strings.Contains(output, "encoding-uncommitted") {
				t.Fatalf("validation changed values or partially registered rows: status=%d %s", status, output)
			}
			history, err := readFailureHistory(historyPath)
			wantName := " � 名前\n<&> 😀 \\ud800 "
			if err != nil || len(history.Files) != 1 || history.Files[0].WholeFile || len(history.Files[0].Suites) != 1 || history.Files[0].Suites[0].Name != " 金木犀 � <&> 😀 " || history.Files[0].Suites[0].WholeSuite || len(history.Files[0].Suites[0].Cases) != 1 || history.Files[0].Suites[0].Cases[0] != wantName {
				t.Fatalf("valid replacement/unicode name changed in history: %#v %v", history, err)
			}
			if reporter == "json" {
				for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
					var event map[string]json.RawMessage
					if err := json.Unmarshal([]byte(line), &event); err != nil {
						t.Fatalf("polluted JSON: %v %s", err, output)
					}
				}
			}
			if reporter == "junit" {
				var document fileErrorJUnitDocument
				if err := xml.Unmarshal([]byte(output), &document); err != nil || document.Failures != 2 {
					t.Fatalf("invalid JUnit: %#v %v %s", document, err, output)
				}
			}
			original, err := os.ReadFile(historyPath)
			if err != nil {
				t.Fatal(err)
			}
			for _, escaped := range []string{`\ud800`, `\udc00`} {
				invalid := strings.Replace(string(original), "�", escaped, 1)
				if err := os.WriteFile(historyPath, []byte(invalid), 0600); err != nil {
					t.Fatal(err)
				}
				status, output = invoke(true, "--rerun-failed", historyPath, "name_encoding")
				after, readError := os.ReadFile(historyPath)
				if status != 2 || readError != nil || string(after) != invalid || !strings.Contains(output, "paired Unicode surrogate escapes") || strings.Contains(output, "encoding-exact-rerun") || strings.Contains(output, "encoding-values-preserved") {
					t.Fatalf("invalid Unicode history selected tests or changed history: %d %v %s", status, readError, output)
				}
			}
			// A legitimate replacement character, emoji pair, and literal backslash-u
			// must still select precisely the originally failed case.
			escaped := strings.ReplaceAll(strings.ReplaceAll(string(original), "😀", `\uD83D\uDE00`), "�", `\ufffd`)
			if err := os.WriteFile(historyPath, []byte(escaped), 0600); err != nil {
				t.Fatal(err)
			}
			status, output = invoke(true, "--rerun-failed", historyPath, "name_encoding")
			history, err = readFailureHistory(historyPath)
			if status != 0 || err != nil || len(history.Files) != 0 || !strings.Contains(output, "encoding-exact-rerun") || strings.Contains(output, "encoding-values-preserved") {
				t.Fatalf("exact unicode rerun failed: status=%d history=%#v err=%v %s", status, history, err, output)
			}
		})
	}
}
