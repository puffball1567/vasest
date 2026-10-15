package main

import (
	"bytes"
	"context"
	"crypto/sha256"
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

// Decode the on-disk format independently of the runner's internal structs.
type artifactResultDocument struct {
	Version, RepeatIndex, RepeatCount int
	Files                             []struct {
		File, Status, Failure, Error, Output, Report string
		Executed, SourceAvailable                    bool
		DurationNs                                   int64
	}
}

func readArtifactResults(t *testing.T, path string) artifactResultDocument {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document artifactResultDocument
	if err := json.Unmarshal(data, &document); err != nil || document.Version != 1 {
		t.Fatalf("invalid result manifest: %v %s", err, data)
	}
	return document
}

func TestArtifactManifestRetainsExecutionOutcomes(t *testing.T) {
	archiveError := errors.New("cannot archive generated source")
	for _, tc := range []struct {
		name, report, want string
		process, fatal     error
	}{
		{"pass", "", "", nil, nil},
		{"assertion", "suite reported failed tests", "suite reported failed tests", nil, nil},
		{"process", "", "exit status 2", errors.New("exit status 2"), nil},
		{"framework exit", "suite reported failed tests", "exit status 1", errors.New("exit status 1"), nil},
		{"invalid report", "invalid suite report: broken JSON", "invalid suite report: broken JSON", nil, nil},
		{"process and report", "invalid suite report: broken JSON", "exit status 2\ninvalid suite report: broken JSON", errors.New("exit status 2"), nil},
		{"timeout after passing report", "", "timed out after 500ms", errors.New("timed out after 500ms"), nil},
		{"unreadable report", "", "exit status 2\ncannot read test report", errors.New("exit status 2"), errors.New("cannot read test report")},
		{"shared archive error", "", archiveError.Error(), archiveError, archiveError},
		{"empty process error", "", "", errors.New(""), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file := "金木犀.test.km"
			artifacts, err := prepareArtifacts(t.TempDir(), []string{file, "not-run.test.km"}, "")
			if err != nil {
				t.Fatal(err)
			}
			// A valid archived source isolates result persistence from source capture.
			directory := artifacts.FileDirectory(file)
			sourcePath := filepath.Join(directory, ".source", "generated.go")
			if err := os.Mkdir(filepath.Dir(sourcePath), 0700); err != nil {
				t.Fatal(err)
			}
			source := []byte("package main\nfunc main() {}\n")
			if err := os.WriteFile(sourcePath, source, 0600); err != nil {
				t.Fatal(err)
			}
			metadata, err := json.Marshal(SourceSnapshot{Original: "original/generated.go", Archived: sourcePath, ImportPath: "example.com/generated", Sha256: fmt.Sprintf("%x", sha256.Sum256(source))})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(directory, "source.json"), metadata, 0600); err != nil {
				t.Fatal(err)
			}
			execution := FileExecution{executed: true, output: "金木犀 <&>\ncompleted output\n", rawReport: "original report\nnot JSON\n", durationNs: 1234567, runError: tc.process, fatalError: tc.fatal, reportFailure: tc.report, expectedExit: tc.name == "framework exit"}
			if err := artifacts.Collect(file, execution, ""); err != nil {
				t.Fatal(err)
			}
			if err := artifacts.Finish(); err != nil {
				t.Fatal(err)
			}
			document := readArtifactResults(t, filepath.Join(artifacts.Directory, "manifest.json"))
			entry, notRun := document.Files[0], document.Files[1]
			wantStatus := "fail"
			if tc.name == "pass" {
				wantStatus = "pass"
			}
			if entry.File != file || entry.Status != wantStatus || entry.Failure != tc.want || entry.DurationNs != execution.durationNs || !entry.Executed || !entry.SourceAvailable || entry.Error != "" {
				t.Fatalf("execution result lost: %#v want=%s/%q", entry, wantStatus, tc.want)
			}
			output, outputError := os.ReadFile(entry.Output)
			report, reportError := os.ReadFile(entry.Report)
			if outputError != nil || reportError != nil || string(output) != execution.output || string(report) != execution.rawReport {
				t.Fatalf("raw artifacts changed: %q %q %v %v", output, report, outputError, reportError)
			}
			if notRun.Executed || notRun.Status != "not-run" || notRun.Failure != "" || notRun.DurationNs != 0 || notRun.Error != "" || notRun.SourceAvailable {
				t.Fatalf("unexecuted file gained a result: %#v", notRun)
			}
		})
	}
}

func TestArtifactManifestKeepsExecutionResultWhenOutputCannotBeSaved(t *testing.T) {
	for _, tc := range []struct {
		status, failure string
		process         error
	}{
		{"pass", "", nil},
		{"fail", "timed out after 500ms", errors.New("timed out after 500ms")},
	} {
		t.Run(tc.status, func(t *testing.T) {
			artifacts, err := prepareArtifacts(t.TempDir(), []string{"file.test.km"}, "")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(filepath.Join(artifacts.FileDirectory("file.test.km"), "output.txt"), 0700); err != nil {
				t.Fatal(err)
			}
			execution := FileExecution{executed: true, durationNs: 42, runError: tc.process}
			if err := artifacts.Collect("file.test.km", execution, ""); err == nil {
				t.Fatal("blocked output accepted")
			}
			if err := artifacts.Finish(); err != nil {
				t.Fatal(err)
			}
			entry := readArtifactResults(t, filepath.Join(artifacts.Directory, "manifest.json")).Files[0]
			if !entry.Executed || entry.Status != tc.status || entry.Failure != tc.failure || entry.DurationNs != 42 || entry.Error == "" {
				t.Fatalf("artifact write error erased the execution result: %#v", entry)
			}
		})
	}
}

func TestArtifactResultsRealKinmokusei(t *testing.T) {
	runner, consumer := os.Getenv("VASEST_INTEGRATION_RUNNER"), os.Getenv("VASEST_INTEGRATION_CONSUMER")
	if runner == "" || consumer == "" {
		t.Skip("requires the external Kinmokusei project")
	}
	for _, reporter := range []string{"text", "json", "junit"} {
		for _, mode := range []string{"framework", "both", "timeout", "unreadable-exit2", "unreadable-timeout", "fixed", "bail"} {
			t.Run(reporter+"/"+mode, func(t *testing.T) {
				outputDirectory := t.TempDir()
				ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
				defer cancel()
				args := []string{"--no-config", "--artifacts", outputDirectory, "--reporter", reporter, "--timeout", "500ms", "--repeat", "2"}
				activeMode := mode
				if mode == "bail" {
					activeMode = "both"
					args = append(args, "--bail")
				} else if mode == "both" {
					args = append(args, "--jobs", "2")
				}
				command := exec.CommandContext(ctx, runner, append(args, "junit_file_errors")...)
				command.Dir = consumer
				fixed := "0"
				if mode == "fixed" {
					fixed = "1"
				}
				command.Env = append(os.Environ(), "VASEST_JUNIT_FILE_MODE="+activeMode, "VASEST_JUNIT_FILE_FIXED="+fixed)
				command.WaitDelay = time.Second
				var stdout, stderr bytes.Buffer
				command.Stdout, command.Stderr = &stdout, &stderr
				err := command.Run()
				status := 0
				if err != nil {
					var exit *exec.ExitError
					if !errors.As(err, &exit) {
						t.Fatalf("artifact results: %v %s %s", err, stdout.String(), stderr.String())
					}
					status = exit.ExitCode()
				}
				fatal := strings.HasPrefix(mode, "unreadable")
				wantStatus, wantRuns := 1, 2
				if fatal {
					wantStatus, wantRuns = 2, 1
				}
				if mode == "bail" {
					wantRuns = 1
				}
				if mode == "fixed" {
					wantStatus = 0
				}
				if status != wantStatus {
					t.Fatalf("status=%d want=%d %s %s", status, wantStatus, stdout.String(), stderr.String())
				}
				// Machine-readable reporting stays valid even if archival diagnostics go to stderr.
				type fileEvent struct {
					Type, Path, Status, Error string
					DurationNs                int64
					RepeatIndex               int
				}
				events := map[string]fileEvent{}
				if reporter == "json" {
					for _, line := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
						var event fileEvent
						if err := json.Unmarshal([]byte(line), &event); err != nil {
							t.Fatalf("polluted JSON: %v %s", err, stdout.String())
						}
						if event.Type == "file" {
							events[fmt.Sprintf("%d/%s", event.RepeatIndex, event.Path)] = event
						}
					}
				}
				if reporter == "junit" {
					var document fileErrorJUnitDocument
					if err := xml.Unmarshal(stdout.Bytes(), &document); err != nil {
						t.Fatalf("polluted JUnit: %v %s", err, stdout.String())
					}
				}
				runs, err := os.ReadDir(outputDirectory)
				if err != nil || len(runs) != wantRuns {
					t.Fatalf("runs=%v err=%v want=%d", runs, err, wantRuns)
				}
				seen := map[int]bool{}
				for _, run := range runs {
					manifestPath := filepath.Join(outputDirectory, run.Name(), "manifest.json")
					document := readArtifactResults(t, manifestPath)
					if len(document.Files) != 2 || document.RepeatCount != 2 || document.RepeatIndex < 1 || document.RepeatIndex > wantRuns || seen[document.RepeatIndex] {
						t.Fatalf("repeat result isolation lost: %#v", document)
					}
					seen[document.RepeatIndex] = true
					for index, entry := range document.Files {
						if (fatal || mode == "bail") && index == 1 {
							if entry.Executed || entry.Status != "not-run" || entry.Failure != "" || entry.DurationNs != 0 || entry.SourceAvailable || entry.Error != "" {
								t.Fatalf("unexecuted file gained result: %#v", entry)
							}
							continue
						}
						wantFileStatus := "pass"
						if index == 0 && mode != "fixed" {
							wantFileStatus = "fail"
						}
						if entry.Status != wantFileStatus || !entry.Executed || !entry.SourceAvailable || entry.DurationNs <= 0 || entry.Error != "" {
							t.Fatalf("lost result/source or confused execution and archive errors: %#v", entry)
						}
						if wantFileStatus == "pass" && entry.Failure != "" || wantFileStatus == "fail" && entry.Failure == "" {
							t.Fatalf("wrong failure diagnostic: %#v", entry)
						}
						if index == 0 {
							for _, cause := range []struct {
								required bool
								message  string
							}{
								{mode == "framework", "exit status 1"},
								{activeMode == "both" || mode == "unreadable-exit2", "exit status 2"},
								{activeMode == "both", "invalid suite report:"},
								{mode == "timeout" || mode == "unreadable-timeout", "timed out after 500ms"},
								{fatal, "cannot read test report:"},
							} {
								if cause.required && !strings.Contains(entry.Failure, cause.message) {
									t.Fatalf("lost independent cause %q: %#v", cause.message, entry)
								}
							}
						}
						if reporter == "json" {
							event, found := events[fmt.Sprintf("%d/%s", document.RepeatIndex, entry.File)]
							if !found || event.Status != entry.Status || event.Error != entry.Failure || event.DurationNs != entry.DurationNs {
								t.Fatalf("manifest disagrees with file event: %#v %#v", entry, event)
							}
						}
						output, outputError := os.ReadFile(entry.Output)
						report, reportError := os.ReadFile(entry.Report)
						if outputError != nil || reportError != nil || (index == 0 && !strings.Contains(string(output), "junit-file-cleanup")) {
							t.Fatalf("raw artifacts lost: %v %v %s", outputError, reportError, output)
						}
						if index == 0 && (fatal && len(report) != 0 || activeMode == "both" && !strings.Contains(string(report), "not JSON")) {
							t.Fatalf("raw report reconstructed or trimmed: %q", report)
						}
					}
				}
			})
		}
	}
}
