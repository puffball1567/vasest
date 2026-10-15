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
	"sync"
	"testing"
	"time"
)

func TestFileTimeoutErrorsRecognizeDrainExpiryBeforeContextDeadline(t *testing.T) {
	process := errors.New("exit status 2")
	for _, tc := range []struct {
		name             string
		process, context error
		timedOut         bool
	}{
		{"success", nil, nil, false},
		{"ordinary process failure", process, nil, false},
		{"drain timer first", exec.ErrWaitDelay, context.Background().Err(), true},
		{"wrapped drain timer", fmt.Errorf("output: %w", exec.ErrWaitDelay), nil, true},
		{"context timer first", process, context.DeadlineExceeded, true},
		{"context after success", nil, context.DeadlineExceeded, true},
		{"both timers", exec.ErrWaitDelay, context.DeadlineExceeded, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := fileTimeoutError(500*time.Millisecond, tc.process, tc.context)
			if tc.timedOut {
				if got == nil || got.Error() != "timed out after 500ms" {
					t.Fatalf("deadline/WaitDelay ordering changed diagnosis: %v", got)
				}
			} else if got != tc.process {
				t.Fatalf("normal outcome or original error identity changed: %v", got)
			}
		})
	}
}

// A separate process keeps the inherited output descriptor open after its
// parent exits or is killed. Release files let tests clean it up themselves.
func TestHeldOutputChildProcess(t *testing.T) {
	if os.Getenv("VASEST_PIPE_CHILD") != "1" {
		return
	}
	dir := os.Getenv("VASEST_PIPE_PROBE")
	stream := os.Getenv("VASEST_PIPE_STREAM")
	if stream != "stderr" {
		os.Stdout.WriteString("held-child-stdout\n")
	}
	if stream != "stdout" {
		os.Stderr.WriteString("held-child-stderr\n")
	}
	if os.WriteFile(filepath.Join(dir, "ready"), nil, 0600) != nil {
		os.Exit(31)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(dir, "release")); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	os.WriteFile(filepath.Join(dir, "done"), nil, 0600)
	os.Exit(0)
}

func startHeldOutputChild(t *testing.T) {
	t.Helper()
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command(program, "-test.run=^TestHeldOutputChildProcess$")
	child.Env = append(os.Environ(), "VASEST_PIPE_CHILD=1")
	stream := os.Getenv("VASEST_PIPE_STREAM")
	if stream != "stderr" {
		child.Stdout = os.Stdout
	}
	if stream != "stdout" {
		child.Stderr = os.Stderr
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	child.Process.Release()
	deadline := time.Now().Add(time.Second)
	for {
		if _, err := os.Stat(filepath.Join(os.Getenv("VASEST_PIPE_PROBE"), "ready")); err == nil {
			if os.Getenv("VASEST_PIPE_RELEASE") == "1" {
				if err := os.WriteFile(filepath.Join(os.Getenv("VASEST_PIPE_PROBE"), "release"), nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("output-holding child did not start")
		}
		time.Sleep(time.Millisecond)
	}
}

func heldOutputProbe(t *testing.T, stream string) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("VASEST_PIPE_PROBE", dir)
	t.Setenv("VASEST_PIPE_STREAM", stream)
	t.Setenv("VASEST_PIPE_RELEASE", "")
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	t.Cleanup(func() {
		if err := os.WriteFile(filepath.Join(dir, "release"), nil, 0600); err != nil {
			t.Error(err)
		}
		if _, err := os.Stat(filepath.Join(dir, "ready")); err != nil {
			return
		}
		deadline := time.Now().Add(time.Second)
		for {
			if _, err := os.Stat(filepath.Join(dir, "done")); err == nil {
				return
			}
			if time.Now().After(deadline) {
				t.Error("output-holding child did not stop after release")
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	})
	return dir
}

func TestTimeoutDoesNotWaitForInheritedOutput(t *testing.T) {
	t.Setenv("VASEST_RUNNER_HELPER", "1")
	keika := fakeKeika(t)
	for _, mode := range []string{"pipe_timeout", "pipe_exit"} {
		for _, stream := range []string{"stdout", "stderr", "both"} {
			t.Run(mode+"/"+stream, func(t *testing.T) {
				probe := heldOutputProbe(t, stream)
				files := []string{filepath.Join(t.TempDir(), mode+".test.km"), filepath.Join(t.TempDir(), "passing.test.km")}
				var buildMutex sync.Mutex
				scheduler := NewFileScheduler(files, func(file string) FileExecution {
					return executeTestFile(keika, file, "", "", false, false, false, 200*time.Millisecond, "", "json", &buildMutex, "", "", "", nil)
				}, false)
				results := scheduler.Run(2)
				failed, passed := results[0], results[1]
				if failed.runError == nil || failed.runError.Error() != "timed out after 200ms" || failed.fatalError != nil || failed.expectedExit || len(failed.suites) != 1 {
					t.Fatalf("held output misclassified: %#v", failed)
				}
				if time.Duration(failed.durationNs) >= 2*time.Second {
					t.Fatalf("timeout waited for child: %s", time.Duration(failed.durationNs))
				}
				if _, err := os.Stat(filepath.Join(probe, "done")); !os.IsNotExist(err) {
					t.Fatal("test did not return while child's output remained open")
				}
				for _, output := range []string{"stdout:", "stderr:"} {
					if !strings.Contains(failed.output, output+filepath.Base(files[0])) || !strings.Contains(passed.output, output+filepath.Base(files[1])) {
						t.Fatalf("lost parent output: %#v", results)
					}
				}
				if stream != "stderr" && !strings.Contains(failed.output, "held-child-stdout") || stream != "stdout" && !strings.Contains(failed.output, "held-child-stderr") {
					t.Fatalf("lost output emitted before timeout: %s", failed.output)
				}
				if passed.runError != nil || passed.reportFailure != "" || passed.fatalError != nil || len(passed.suites) != 1 || strings.Contains(passed.output, "held-child-") {
					t.Fatalf("other file affected: %#v", passed)
				}
			})
		}
	}
}

func TestTimeoutAllowsInheritedOutputToClose(t *testing.T) {
	t.Setenv("VASEST_RUNNER_HELPER", "1")
	keika := fakeKeika(t)
	for _, reporter := range []string{"text", "json", "junit"} {
		t.Run(reporter, func(t *testing.T) {
			heldOutputProbe(t, "both")
			t.Setenv("VASEST_PIPE_RELEASE", "1")
			file := filepath.Join(t.TempDir(), "pipe_closed.test.km")
			result := executeTestFile(keika, file, "", "", false, false, false, time.Second, "", reporter, nil, "", "", "", nil)
			if result.runError != nil || result.fatalError != nil || result.reportFailure != "" || len(result.suites) != 1 || !strings.Contains(result.output, "held-child-stdout") || !strings.Contains(result.output, "held-child-stderr") || !strings.Contains(result.output, "stdout:pipe_closed.test.km") || !strings.Contains(result.output, "stderr:pipe_closed.test.km") {
				t.Fatalf("normally closed output failed or was lost: %#v", result)
			}
		})
	}
}

func TestTimeoutInheritedOutputReportersHistoryAndBail(t *testing.T) {
	t.Setenv("VASEST_RUNNER_HELPER", "1")
	t.Setenv("KEIKA", fakeKeika(t))
	for _, reporter := range []string{"text", "json", "junit"} {
		for _, jobs := range []string{"1", "2"} {
			t.Run(reporter+"/jobs"+jobs, func(t *testing.T) {
				probe := heldOutputProbe(t, "both")
				input := t.TempDir()
				for _, file := range []string{"a_pipe_timeout.test.km", "z_pass.test.km"} {
					if err := os.WriteFile(filepath.Join(input, file), nil, 0600); err != nil {
						t.Fatal(err)
					}
				}
				historyPath := filepath.Join(t.TempDir(), "failures.json")
				started := time.Now()
				status, output := captureRunner(t, []string{"--no-config", "--timeout", "200ms", "--jobs", jobs, "--reporter", reporter, "--save-failures", historyPath, input})
				if status != 1 || time.Since(started) >= 2*time.Second || !strings.Contains(output, "timed out after 200ms") || !strings.Contains(output, "stdout:z_pass.test.km") || !strings.Contains(output, "held-child-stdout") || !strings.Contains(output, "held-child-stderr") {
					t.Fatalf("status=%d elapsed=%s output=%s", status, time.Since(started), output)
				}
				if _, err := os.Stat(filepath.Join(probe, "done")); !os.IsNotExist(err) {
					t.Fatal("reporter waited for inherited output to close")
				}
				history, err := readFailureHistory(historyPath)
				if err != nil || len(history.Files) != 1 || !history.Files[0].WholeFile || len(history.Files[0].Suites) != 0 || filepath.Base(history.Files[0].File) != "a_pipe_timeout.test.km" {
					t.Fatalf("timeout lost whole-file history: %#v %v", history, err)
				}
				switch reporter {
				case "text":
					if !strings.Contains(output, "1 file(s) passed, 1 failed, 0 not run") || strings.Count(output, "FILE "+filepath.Join(input, "a_pipe_timeout.test.km")) != 1 || strings.Count(output, "FILE "+filepath.Join(input, "z_pass.test.km")) != 1 {
						t.Fatalf("text output duplicated or lost: %s", output)
					}
				case "json":
					files := 0
					for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
						var event struct {
							Type, Path, Status, Output, Error string
							PassedFiles, FailedFiles          int
						}
						if err := json.Unmarshal([]byte(line), &event); err != nil {
							t.Fatalf("polluted JSON: %v %s", err, output)
						}
						if event.Type == "file" {
							files++
							if strings.Contains(event.Path, "pipe_timeout") && (event.Status != "fail" || event.Error != "timed out after 200ms") || strings.Contains(event.Path, "z_pass") && (event.Status != "pass" || strings.Contains(event.Output, "held-child")) {
								t.Fatalf("file status or output leaked: %#v", event)
							}
						}
						if event.Type == "summary" && (event.PassedFiles != 1 || event.FailedFiles != 1) {
							t.Fatalf("wrong summary: %#v", event)
						}
					}
					if files != 2 {
						t.Fatal("missing file events", output)
					}
				case "junit":
					var document struct {
						XMLName  xml.Name `xml:"testsuites"`
						Failures int      `xml:"failures,attr"`
						Suites   []struct {
							Output string `xml:"system-out"`
						} `xml:"testsuite"`
					}
					if err := xml.Unmarshal([]byte(output), &document); err != nil || document.Failures != 1 || len(document.Suites) != 2 || strings.Contains(document.Suites[1].Output, "held-child") {
						t.Fatalf("wrong JUnit: %#v %v %s", document, err, output)
					}
				}
			})
		}
	}
	t.Run("bail", func(t *testing.T) {
		heldOutputProbe(t, "both")
		input := t.TempDir()
		for _, file := range []string{"a_pipe_timeout.test.km", "z_pass.test.km"} {
			if err := os.WriteFile(filepath.Join(input, file), nil, 0600); err != nil {
				t.Fatal(err)
			}
		}
		status, output := captureRunner(t, []string{"--no-config", "--timeout", "200ms", "--bail", input})
		if status != 1 || strings.Contains(output, "stdout:z_pass.test.km") || !strings.Contains(output, "0 file(s) passed, 1 failed, 1 not run") {
			t.Fatalf("bail status=%d output=%s", status, output)
		}
	})
}

func TestTimeoutPipesRealKinmokusei(t *testing.T) {
	runner, consumer := os.Getenv("VASEST_INTEGRATION_RUNNER"), os.Getenv("VASEST_INTEGRATION_CONSUMER")
	if runner == "" || consumer == "" {
		t.Skip("requires the external Kinmokusei project")
	}
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("VASEST_PIPE_CHILD_BINARY", program)
	invoke := func(args ...string) (int, string) {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, runner, append([]string{"--no-config"}, args...)...)
		command.Dir = consumer
		// Also bound the verification process if a regression leaves a descriptor
		// inherited from the runner itself open.
		command.WaitDelay = time.Second
		output, err := command.CombinedOutput()
		if err == nil {
			return 0, string(output)
		}
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("timeout integration: %v %s", err, output)
		}
		return exit.ExitCode(), string(output)
	}
	for _, reporter := range []string{"text", "json", "junit"} {
		for _, mode := range []string{"timeout", "exit"} {
			t.Run(reporter+"/"+mode, func(t *testing.T) {
				probe := heldOutputProbe(t, "both")
				jobs, parentExit := "1", "0"
				if mode == "exit" {
					jobs, parentExit = "2", "1"
				}
				t.Setenv("VASEST_PIPE_PARENT_EXIT", parentExit)
				historyPath := filepath.Join(t.TempDir(), "failures.json")
				status, output := invoke("--timeout", "500ms", "--jobs", jobs, "--reporter", reporter, "--save-failures", historyPath, "timeout_pipes")
				if status != 1 || !strings.Contains(output, "timed out after 500ms") || !strings.Contains(output, "pipe-parent-ready") || !strings.Contains(output, "held-child-stdout") || !strings.Contains(output, "held-child-stderr") || !strings.Contains(output, "pipe-later-file") {
					t.Fatalf("status=%d output=%s", status, output)
				}
				if _, err := os.Stat(filepath.Join(probe, "done")); !os.IsNotExist(err) {
					t.Fatal("real runner waited for inherited output to close")
				}
				history, err := readFailureHistory(historyPath)
				if err != nil || len(history.Files) != 1 || !history.Files[0].WholeFile || len(history.Files[0].Suites) != 0 || filepath.Base(history.Files[0].File) != "a_inherited.test.km" {
					t.Fatalf("wrong timeout history: %#v %v %s", history, err, output)
				}
				if reporter == "json" {
					files, suites := 0, 0
					for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
						var event struct {
							Type, Path, Status, Output string
							PassedFiles, FailedFiles   int
						}
						if err := json.Unmarshal([]byte(line), &event); err != nil {
							t.Fatalf("polluted JSON: %v %s", err, output)
						}
						if event.Type == "file" {
							files++
							if strings.Contains(event.Path, "a_inherited") && event.Status != "fail" || strings.Contains(event.Path, "z_passing") && (event.Status != "pass" || strings.Contains(event.Output, "held-child")) {
								t.Fatalf("file status or output leaked: %#v", event)
							}
						}
						if event.Type == "suite" {
							suites++
						}
						if event.Type == "summary" && (event.PassedFiles != 1 || event.FailedFiles != 1) {
							t.Fatalf("wrong summary: %#v", event)
						}
					}
					wantedSuites := 1
					if mode == "exit" {
						wantedSuites = 2
					}
					if files != 2 || suites != wantedSuites {
						t.Fatalf("missing reports: %s", output)
					}
				}
				if reporter == "junit" {
					var document struct {
						XMLName  xml.Name `xml:"testsuites"`
						Failures int      `xml:"failures,attr"`
					}
					if err := xml.Unmarshal([]byte(output), &document); err != nil || document.Failures != 1 {
						t.Fatalf("wrong JUnit: %#v %v %s", document, err, output)
					}
				}
				if reporter == "json" && mode == "exit" {
					t.Setenv("VASEST_PIPE_FIXED", "1")
					status, output = invoke("--timeout", "500ms", "--reporter", "json", "--rerun-failed", historyPath, "--save-failures", historyPath, "timeout_pipes")
					history, err = readFailureHistory(historyPath)
					if status != 0 || err != nil || len(history.Files) != 0 || !strings.Contains(output, "pipe-fixed") || strings.Contains(output, "pipe-later-file") {
						t.Fatalf("whole-file rerun status=%d history=%#v err=%v output=%s", status, history, err, output)
					}
				}
			})
		}
	}
	t.Run("closed", func(t *testing.T) {
		heldOutputProbe(t, "both")
		t.Setenv("VASEST_PIPE_RELEASE", "1")
		status, output := invoke("--timeout", "500ms", "--reporter", "json", "timeout_pipes")
		if status != 0 || !strings.Contains(output, `"passedFiles":2`) || !strings.Contains(output, `"failedFiles":0`) || !strings.Contains(output, "held-child-stdout") || !strings.Contains(output, "held-child-stderr") || !strings.Contains(output, "pipe-later-file") {
			t.Fatalf("normally closed output status=%d output=%s", status, output)
		}
	})
}
