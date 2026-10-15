package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestFileSchedulerOverlapsWithoutExceedingLimit(t *testing.T) {
	files := []string{"first", "second", "third", "fourth", "fifth"}
	started := make(chan string, len(files))
	release := make(chan struct{})
	var mutex sync.Mutex
	active, peak := 0, 0
	scheduler := NewFileScheduler(files, func(file string) FileExecution {
		mutex.Lock()
		active++
		if active > peak {
			peak = active
		}
		mutex.Unlock()
		started <- file
		<-release
		mutex.Lock()
		active--
		mutex.Unlock()
		return FileExecution{executed: true, output: file}
	}, false)
	done := make(chan []FileExecution, 1)
	go func() { done <- scheduler.Run(2) }()
	for index := 0; index < 2; index++ {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			close(release)
			t.Fatal("files did not execute concurrently")
		}
	}
	select {
	case file := <-started:
		close(release)
		t.Fatalf("extra file started while workers were occupied: %s", file)
	default:
	}
	close(release)
	results := <-done
	for index, result := range results {
		if !result.executed || result.output != files[index] {
			t.Fatalf("results lost discovery order: %#v", results)
		}
	}
	if peak != 2 || active != 0 {
		t.Fatalf("peak=%d active=%d", peak, active)
	}
}

func TestFileSchedulerBailWaitsForActiveFilesAndStopsNewFiles(t *testing.T) {
	for _, failure := range []string{"process", "suite", "report", "fatal"} {
		t.Run(failure, func(t *testing.T) {
			started := make(chan struct{})
			release := make(chan struct{})
			scheduler := NewFileScheduler([]string{"fails", "active", "not run"}, func(file string) FileExecution {
				result := FileExecution{executed: true, output: file}
				if file == "active" {
					close(started)
					<-release
					return result
				}
				if file == "not run" {
					return result
				}
				<-started
				switch failure {
				case "process":
					result.runError = errors.New("compile failed")
				case "suite":
					result.reportFailure = "suite reported failed tests"
				case "report":
					result.reportFailure = "invalid suite report"
				case "fatal":
					result.fatalError = errors.New("cannot read report")
				}
				return result
			}, failure != "fatal")
			done := make(chan []FileExecution, 1)
			go func() { done <- scheduler.Run(2) }()
			deadline := time.Now().Add(5 * time.Second)
			for {
				scheduler.mutex.Lock()
				stopped := scheduler.stopped
				scheduler.mutex.Unlock()
				if stopped {
					break
				}
				if time.Now().After(deadline) {
					close(release)
					t.Fatal("failure did not stop scheduling")
				}
				time.Sleep(time.Millisecond)
			}
			select {
			case <-done:
				close(release)
				t.Fatal("scheduler returned before active file finished")
			default:
			}
			close(release)
			results := <-done
			if !results[0].executed || !results[1].executed || results[2].executed {
				t.Fatalf("unexpected bail results: %#v", results)
			}
		})
	}
}

func TestFileSchedulerContinuesFailuresWithoutBailAndHandlesEmptyShard(t *testing.T) {
	scheduler := NewFileScheduler([]string{"fails", "passes"}, func(file string) FileExecution {
		return FileExecution{executed: true, runError: errors.New("failure")}
	}, false)
	results := scheduler.Run(100)
	if len(results) != 2 || !results[0].executed || !results[1].executed {
		t.Fatalf("files were lost: %#v", results)
	}
	empty := NewFileScheduler(nil, func(string) FileExecution { t.Fatal("empty shard executed a file"); return FileExecution{} }, true)
	if len(empty.Run(3)) != 0 {
		t.Fatal("empty shard produced results")
	}
}

func shellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }

// Fake compiler and test processes let the runner exercise real process I/O,
// report validation, isolation, and timeouts without invoking a compiler.
func fakeKeika(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	t.Setenv("VASEST_RUNNER_PROBE", directory)
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("VASEST_RUNNER_TEST_BINARY", program)
	path := filepath.Join(directory, "keika")
	script := "#!/bin/sh\nexec " + shellQuote(program) + " -test.run=^TestFakeCompilerProcess$ -- \"$@\"\n"
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestFakeCompilerProcess(t *testing.T) {
	if os.Getenv("VASEST_RUNNER_HELPER") != "1" {
		return
	}
	arguments := os.Args
	for len(arguments) > 0 && arguments[0] != "--" {
		arguments = arguments[1:]
	}
	arguments = arguments[1:]
	if len(arguments) != 4 || arguments[0] != "build" || arguments[1] != "-o" {
		os.Exit(20)
	}
	if probe := os.Getenv("VASEST_ARTIFACT_PROBE"); probe != "" {
		if strings.Contains(arguments[3], "compile_error") {
			os.Stderr.WriteString("compiler rejected input\n")
			os.Exit(1)
		}
		generated, err := generatedSourceDirectory(arguments[3])
		if err != nil || os.MkdirAll(generated, 0700) != nil {
			os.Exit(27)
		}
		source, err := os.ReadFile(os.Getenv("VASEST_ARTIFACT_SOURCE"))
		if err != nil {
			os.Exit(28)
		}
		source = append(source, []byte("\n// source for "+filepath.Base(arguments[3])+"\n")...)
		if os.WriteFile(filepath.Join(generated, "generated.go"), source, 0600) != nil {
			os.Exit(29)
		}
		program, err := os.ReadFile(probe)
		if err != nil || os.WriteFile(arguments[2], program, 0700) != nil {
			os.Exit(30)
		}
		os.Exit(0)
	}
	if probe := os.Getenv("VASEST_COVERAGE_PROBE"); probe != "" {
		if !strings.HasSuffix(os.Getenv("GOFLAGS"), " -cover -covermode=atomic") || os.Getenv("GOCOVERDIR") != "" {
			os.Exit(25)
		}
		data, err := os.ReadFile(probe)
		if err != nil || os.WriteFile(arguments[2], data, 0700) != nil {
			os.Exit(26)
		}
		os.Exit(0)
	}
	// Overlapping builds would overwrite a shared compiler output directory.
	lock := filepath.Join(os.Getenv("VASEST_RUNNER_PROBE"), "building")
	if err := os.Mkdir(lock, 0700); err != nil {
		os.Exit(21)
	}
	time.Sleep(5 * time.Millisecond)
	file, pathError := filepath.Abs(arguments[3])
	if pathError != nil {
		os.Exit(24)
	}
	script := "#!/bin/sh\nexec " + shellQuote(os.Getenv("VASEST_RUNNER_TEST_BINARY")) + " -test.run=^TestFakeCaseProcess$ -- " + shellQuote(file) + "\n"
	if os.Getenv("VASEST_DISABLE_COVERAGE") == "1" {
		script = "#!/bin/sh\nunset GOCOVERDIR\n" + strings.TrimPrefix(script, "#!/bin/sh\n")
	}
	err := os.WriteFile(arguments[2], []byte(script), 0700)
	os.Remove(lock)
	if err != nil {
		os.Exit(22)
	}
	os.Exit(0)
}

func TestFakeCaseProcess(t *testing.T) {
	if os.Getenv("VASEST_RUNNER_HELPER") != "1" {
		return
	}
	file := os.Args[len(os.Args)-1]
	if file != os.Getenv("VASEST_TEST_FILE") {
		os.Exit(23)
	}
	if strings.Contains(filepath.Base(file), "pipe_") && os.Getenv("VASEST_PIPE_PROBE") != "" {
		startHeldOutputChild(t)
	}
	if strings.Contains(file, "slow") {
		time.Sleep(5 * time.Second)
	}
	repeatFailure := false
	if os.Getenv("VASEST_REPEAT_PROBE") != "" {
		fmt.Printf("repeat-marker:%s/%s\n", os.Getenv("VASEST_REPEAT_INDEX"), os.Getenv("VASEST_REPEAT_COUNT"))
	}
	if os.Getenv("VASEST_REPEAT_MATCH_FIRST") == "1" && os.Getenv("VASEST_REPEAT_INDEX") == "1" {
		if log, err := os.OpenFile(os.Getenv("VASEST_MATCH_LOG"), os.O_APPEND|os.O_WRONLY, 0600); err == nil {
			log.WriteString("1\n")
			log.Close()
		}
	}
	if strings.Contains(file, "invalid") {
		os.WriteFile(os.Getenv("VASEST_REPORT_PATH"), []byte("not JSON\n"), 0600)
	} else if !strings.Contains(file, "missing") {
		report := `{"success":true,"summary":{"suite":"probe","passed":1,"failed":0,"skipped":0,"todo":0,"results":[{"name":"case","passed":true,"status":"pass"}]}}` + "\n"
		if override := os.Getenv("VASEST_RUNNER_REPORT"); override != "" {
			report = override + "\n"
		}
		if os.Getenv("VASEST_REPEAT_PROBE") == "fail-first" && os.Getenv("VASEST_REPEAT_INDEX") == "1" {
			report = `{"success":false,"summary":{"suite":"probe","passed":0,"failed":1,"skipped":0,"todo":0,"results":[{"name":"case","passed":false,"status":"fail","message":"first iteration"}]}}` + "\n"
			repeatFailure = true
		}
		os.WriteFile(os.Getenv("VASEST_REPORT_PATH"), []byte(report), 0600)
	}
	if os.Getenv("VASEST_CONFIG_PROBE") == "1" {
		fmt.Printf("policy:%s:%s:%s\n", os.Getenv("VASEST_FILTER"), os.Getenv("VASEST_BAIL"), os.Getenv("VASEST_FAIL_ON_TODO"))
	}
	os.Stdout.WriteString("stdout:" + filepath.Base(file) + "\n")
	os.Stderr.WriteString("stderr:" + filepath.Base(file) + "\n")
	if strings.Contains(filepath.Base(file), "pipe_timeout") && os.Getenv("VASEST_PIPE_PROBE") != "" {
		time.Sleep(5 * time.Second)
	}
	if repeatFailure {
		os.WriteFile(os.Getenv("VASEST_SUITE_EXIT_PATH"), []byte("1\n"), 0600)
		os.Exit(1)
	}
	os.Exit(0)
}

func TestParallelFileProcessesCaptureAndValidateReports(t *testing.T) {
	t.Setenv("VASEST_RUNNER_HELPER", "1")
	keika := fakeKeika(t)
	files := []string{"passing.test.km", "invalid.test.km", "missing.test.km", "second.test.km"}
	for index := range files {
		files[index] = filepath.Join(t.TempDir(), files[index])
	}
	var buildMutex sync.Mutex
	scheduler := NewFileScheduler(files, func(file string) FileExecution {
		return executeTestFile(keika, file, "", "", false, false, false, 0, "", "json", &buildMutex, "", "", "", nil)
	}, false)
	results := scheduler.Run(3)
	for index, result := range results {
		if result.runError != nil || result.fatalError != nil || result.durationNs <= 0 || !strings.Contains(result.output, "stdout:"+filepath.Base(files[index])) || !strings.Contains(result.output, "stderr:"+filepath.Base(files[index])) {
			t.Fatalf("file %d = %#v", index, result)
		}
	}
	if len(results[0].suites) != 1 || len(results[3].suites) != 1 || !strings.Contains(results[1].reportFailure, "invalid suite report") || results[2].reportFailure != "no test suites ran" {
		t.Fatalf("reports misclassified: %#v", results)
	}
}

func TestParallelTimeoutContinuesOtherFiles(t *testing.T) {
	t.Setenv("VASEST_RUNNER_HELPER", "1")
	keika := fakeKeika(t)
	var buildMutex sync.Mutex
	files := []string{filepath.Join(t.TempDir(), "slow.test.km"), filepath.Join(t.TempDir(), "passing.test.km")}
	scheduler := NewFileScheduler(files, func(file string) FileExecution {
		return executeTestFile(keika, file, "", "", false, false, false, 100*time.Millisecond, "", "text", &buildMutex, "", "", "", nil)
	}, false)
	results := scheduler.Run(2)
	if results[0].runError == nil || results[0].runError.Error() != "timed out after 100ms" || results[1].runError != nil || len(results[1].suites) != 1 {
		t.Fatalf("timeout affected another file: %#v", results)
	}
}

func captureRunner(t *testing.T, args []string) (int, string) {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	previousOut, previousErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = writer, writer
	defer func() { os.Stdout, os.Stderr = previousOut, previousErr; writer.Close() }()
	done := make(chan string, 1)
	go func() {
		defer reader.Close()
		data, _ := io.ReadAll(reader)
		done <- string(data)
	}()
	status := run(args)
	writer.Close()
	return status, <-done
}

func TestParallelReportersPreserveFileOrderAndFailures(t *testing.T) {
	t.Setenv("VASEST_RUNNER_HELPER", "1")
	t.Setenv("KEIKA", fakeKeika(t))
	directory := t.TempDir()
	names := []string{"a_pass.test.km", "b_invalid.test.km", "c_missing.test.km", "d_pass.test.km"}
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(directory, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, reporter := range []string{"text", "json", "junit"} {
		t.Run(reporter, func(t *testing.T) {
			status, output := captureRunner(t, []string{"--jobs", "3", "--reporter", reporter, directory})
			if status != 1 {
				t.Fatalf("status = %d; output = %s", status, output)
			}
			switch reporter {
			case "text":
				position := -1
				for _, name := range names {
					next := strings.Index(output, "FILE "+filepath.Join(directory, name)+"\n")
					if next <= position {
						t.Fatalf("file output reordered: %s", output)
					}
					position = next
				}
				if !strings.Contains(output, "2 file(s) passed, 2 failed, 0 not run") {
					t.Fatalf("bad file counts: %s", output)
				}
			case "json":
				var paths []string
				for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
					var event struct {
						Type, Path, Status, Output                  string
						Jobs, PassedFiles, FailedFiles, NotRunFiles int
					}
					if err := json.Unmarshal([]byte(line), &event); err != nil {
						t.Fatalf("non-JSON stdout: %s", output)
					}
					if event.Type == "file" {
						paths = append(paths, filepath.Base(event.Path))
						if !strings.Contains(event.Output, "stdout:"+filepath.Base(event.Path)) || !strings.Contains(event.Output, "stderr:"+filepath.Base(event.Path)) {
							t.Fatalf("output mixed between files: %#v", event)
						}
					}
					if event.Type == "summary" && (event.Jobs != 3 || event.PassedFiles != 2 || event.FailedFiles != 2 || event.NotRunFiles != 0) {
						t.Fatalf("bad summary: %#v", event)
					}
				}
				if strings.Join(paths, ",") != strings.Join(names, ",") {
					t.Fatalf("JSON file order = %v", paths)
				}
			case "junit":
				var document struct {
					Tests    int `xml:"tests,attr"`
					Failures int `xml:"failures,attr"`
					Suites   []struct {
						Name   string `xml:"name,attr"`
						Output string `xml:"system-out"`
					} `xml:"testsuite"`
				}
				if err := xml.Unmarshal([]byte(output), &document); err != nil {
					t.Fatalf("invalid XML: %v\n%s", err, output)
				}
				if document.Tests != 4 || document.Failures != 2 || len(document.Suites) != 4 {
					t.Fatalf("bad JUnit totals: %#v", document)
				}
				for index, suite := range document.Suites {
					if filepath.Base(suite.Name) != names[index] || !strings.Contains(suite.Output, "stdout:"+names[index]) {
						t.Fatalf("JUnit mixed files: %#v", document)
					}
				}
			}
		})
	}
	status, output := captureRunner(t, []string{"--jobs", "2", "--reporter", "json", "--filter", "absent", filepath.Join(directory, names[0])})
	if status != 1 || !strings.Contains(output, `"noMatch":true`) {
		t.Fatalf("no-match selection = %d: %s", status, output)
	}
}

func TestBuildFileSerializesCompilerCommands(t *testing.T) {
	t.Setenv("VASEST_RUNNER_HELPER", "1")
	keika := fakeKeika(t)
	var mutex sync.Mutex
	var workers sync.WaitGroup
	for index := 0; index < 4; index++ {
		command := exec.Command(keika, "build", "-o", filepath.Join(t.TempDir(), "test.exe"), "input.test.km")
		workers.Add(1)
		go func() {
			defer workers.Done()
			if err := buildFile(command, &mutex); err != nil {
				t.Errorf("concurrent compiler command: %v", err)
			}
		}()
	}
	workers.Wait()
}

func TestJobsArgumentsRejectInvalidValues(t *testing.T) {
	for _, args := range [][]string{{"--jobs"}, {"--jobs", ""}, {"--jobs", "0"}, {"--jobs", "-1"}, {"--jobs", "1.5"}, {"--jobs", "no"}, {"--jobs", "99999999999999999999999999"}, {"--list", "--jobs", "1"}} {
		if status := run(args); status != 2 {
			t.Errorf("run(%q) = %d", args, status)
		}
	}
}

func TestParallelFilterEmptyShardDoesNotInvokeCompiler(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "case.test.km"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KEIKA", filepath.Join(directory, "not-installed"))
	if status := run([]string{"--jobs", "3", "--shard", "2/2", "--filter", "absent", directory}); status != 0 {
		t.Fatalf("empty shard status = %d", status)
	}
}

func writeWatchFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func replaceWatchConfig(t *testing.T, path string, data []byte) {
	t.Helper()
	// Publish one complete edit; truncation must not create a second watch event.
	pending := path + ".next"
	if err := os.WriteFile(pending, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(pending, path); err != nil {
		t.Fatal(err)
	}
}

func signatureFor(t *testing.T, root string, snapshots bool) string {
	t.Helper()
	signature, err := watchSignature([]string{root}, snapshots)
	if err != nil {
		t.Fatal(err)
	}
	return signature
}

func TestWatchSignatureTracksContentAdditionsDeletionAndRename(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "src", "app.km")
	writeWatchFile(t, path, "first")
	initial := signatureFor(t, root, true)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	writeWatchFile(t, path, "other")
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	changed := signatureFor(t, root, true)
	if changed == initial {
		t.Fatal("same-size, same-timestamp edit was missed")
	}
	renamed := filepath.Join(root, "src", "renamed.km")
	if err := os.Rename(path, renamed); err != nil {
		t.Fatal(err)
	}
	if signatureFor(t, root, true) == changed {
		t.Fatal("rename was missed")
	}
	if err := os.Remove(renamed); err != nil {
		t.Fatal(err)
	}
	empty := signatureFor(t, root, true)
	writeWatchFile(t, filepath.Join(root, "new", "added.test.km"), "test")
	if signatureFor(t, root, true) == empty {
		t.Fatal("new directory and test file were missed")
	}
}

func TestWatchSignatureIgnoresGeneratedDirectoriesAndFollowsFixturePolicy(t *testing.T) {
	root := t.TempDir()
	initial := signatureFor(t, root, true)
	for _, directory := range []string{".git", ".kinmokusei", ".hidden", "node_modules", "vendor", "build", "dist"} {
		writeWatchFile(t, filepath.Join(root, directory, "generated.go"), "generated")
	}
	writeWatchFile(t, filepath.Join(root, "report.xml"), "report")
	writeWatchFile(t, filepath.Join(root, "results.json"), "report")
	if signatureFor(t, root, true) != initial {
		t.Fatal("generated output caused a watch change")
	}
	if err := os.Symlink(root, filepath.Join(root, "cycle")); err != nil {
		t.Fatal(err)
	}
	if signatureFor(t, root, true) != initial {
		t.Fatal("watch traversed a symlink")
	}
	for _, name := range []string{"go.mod", "go.sum", "kinmokusei.toml", "kinmokusei.lock", "helper.go", "other.km"} {
		before := signatureFor(t, root, true)
		writeWatchFile(t, filepath.Join(root, name), "changed")
		if signatureFor(t, root, true) == before {
			t.Fatalf("%s was not watched", name)
		}
	}
	before := signatureFor(t, root, true)
	writeWatchFile(t, filepath.Join(root, "__snapshots__", "case", "value.snap"), "snapshot")
	if signatureFor(t, root, true) == before || signatureFor(t, root, false) != before {
		t.Fatal("snapshot update policy was not respected")
	}
	if _, err := watchSignature([]string{filepath.Join(root, "not-created")}, true); err != nil {
		t.Fatalf("missing root should be recoverable: %v", err)
	}
}

func TestWatchChangesDebouncesBurstsAndDoesNotRepeatStableRuns(t *testing.T) {
	changes := NewWatchChanges("initial")
	for _, signature := range []string{"initial", "edit1", "edit2", "edit3"} {
		if changes.Ready(signature) {
			t.Fatalf("ran before changes settled at %s", signature)
		}
	}
	if !changes.Ready("edit3") || changes.Ready("edit3") {
		t.Fatal("settled changes should trigger exactly one run")
	}
	if changes.Ready("initial") || !changes.Ready("initial") {
		t.Fatal("reverting content did not trigger a run")
	}
}

func TestWatchLoopRecoversAfterFailureAndKeepsChangesDuringExecution(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "app.km")
	writeWatchFile(t, path, "initial")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var iterations []int
	runs := 0
	status := watchLoop(ctx, []string{root}, 10*time.Millisecond, true, func() int {
		runs++
		if runs == 1 {
			writeWatchFile(t, path, "edited while running")
			return 1
		}
		cancel()
		return 0
	}, func(iteration int) { iterations = append(iterations, iteration) })
	if status != 0 || runs != 2 || len(iterations) != 2 || iterations[0] != 1 || iterations[1] != 2 {
		t.Fatalf("status=%d runs=%d iterations=%v", status, runs, iterations)
	}
}

func TestWatchLoopCancellationAndErrors(t *testing.T) {
	t.Run("last failure", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		status := watchLoop(ctx, []string{t.TempDir()}, time.Millisecond, true, func() int { cancel(); return 1 }, func(int) {})
		if status != 1 {
			t.Fatalf("lost last exit status: %d", status)
		}
	})
	t.Run("cancelled before start", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if status := watchLoop(ctx, []string{t.TempDir()}, time.Millisecond, true, func() int { t.Fatal("cancelled watch ran a test"); return 0 }, func(int) {}); status != 0 {
			t.Fatalf("status = %d", status)
		}
	})
	t.Run("scan error", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "not-a-directory")
		writeWatchFile(t, file, "content")
		if status := watchLoop(context.Background(), []string{file}, time.Millisecond, true, func() int { t.Fatal("scan error ran a test"); return 0 }, func(int) {}); status != 2 {
			t.Fatalf("status = %d", status)
		}
	})
	t.Run("later scan error", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "root")
		if err := os.Mkdir(root, 0700); err != nil {
			t.Fatal(err)
		}
		status := watchLoop(context.Background(), []string{root}, time.Millisecond, true, func() int {
			if err := os.Remove(root); err != nil {
				t.Fatal(err)
			}
			writeWatchFile(t, root, "became a file")
			return 0
		}, func(int) {})
		if status != 2 {
			t.Fatalf("scan error status = %d", status)
		}
	})
}

func TestWatchRootsIncludeExternalInputs(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	inside, err := watchRoots("tests")
	if err != nil || len(inside) != 1 || inside[0] != cwd {
		t.Fatalf("inside roots=%v error=%v", inside, err)
	}
	external := t.TempDir()
	for _, input := range []string{external, filepath.Join(external, "case.test.km")} {
		roots, err := watchRoots(input)
		if err != nil || len(roots) != 2 || roots[1] != external {
			t.Fatalf("external roots=%v error=%v", roots, err)
		}
	}
}

func TestWatchArgumentsRejectInvalidCombinations(t *testing.T) {
	for _, args := range [][]string{
		{"--watch", "--list"}, {"--watch", "--reporter", "junit"}, {"--watch-interval", "10ms"},
		{"--watch", "--watch-interval"}, {"--watch", "--watch-interval", ""}, {"--watch", "--watch-interval", "0"},
		{"--watch", "--watch-interval", "-1s"}, {"--watch", "--watch-interval", "invalid"}, {"--filter", "--watch"},
	} {
		if status := run(args); status != 2 {
			t.Errorf("run(%q)=%d", args, status)
		}
	}
}

func TestWatchRunnerProcess(t *testing.T) {
	if os.Getenv("VASEST_WATCH_RUNNER_HELPER") != "1" {
		return
	}
	arguments := os.Args
	for len(arguments) > 0 && arguments[0] != "--" {
		arguments = arguments[1:]
	}
	os.Exit(run(arguments[1:]))
}

func TestWatchCLIReexecutesOnChangesAndStopsOnSignal(t *testing.T) {
	t.Setenv("VASEST_RUNNER_HELPER", "1")
	t.Setenv("KEIKA", fakeKeika(t))
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, reporter := range []string{"text", "json"} {
		t.Run(reporter, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "tests", "case.test.km")
			writeWatchFile(t, path, "initial")
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, program, "-test.run=^TestWatchRunnerProcess$", "--", "--watch", "--watch-interval", "20ms", "--jobs", "2", "--reporter", reporter)
			command.Dir = root
			command.Env = append(os.Environ(), "VASEST_WATCH_RUNNER_HELPER=1")
			stdout, err := command.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			var stderr strings.Builder
			command.Stderr = &stderr
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { command.Process.Kill(); command.Wait() }()
			scanner := bufio.NewScanner(stdout)
			completed := 0
			for scanner.Scan() {
				line := scanner.Text()
				summary := strings.Contains(line, "file(s) passed")
				if reporter == "json" {
					var event struct {
						Type      string
						Iteration int
					}
					if err := json.Unmarshal([]byte(line), &event); err != nil {
						t.Fatalf("watch stdout was not JSON: %s", line)
					}
					if event.Type == "watch" && event.Iteration != completed+1 {
						t.Fatalf("bad watch iteration: %s", line)
					}
					summary = event.Type == "summary"
				}
				if !summary {
					continue
				}
				completed++
				if completed == 1 {
					writeWatchFile(t, path, "edited")
					continue
				}
				if err := command.Process.Signal(syscall.SIGTERM); err != nil {
					t.Fatal(err)
				}
				break
			}
			// Drain the pipe before waiting so shutdown cannot block on output.
			io.Copy(io.Discard, stdout)
			if err := command.Wait(); err != nil || completed != 2 {
				t.Fatalf("watch cycles=%d error=%v stderr=%s", completed, err, stderr.String())
			}
		})
	}
}

func TestWatchRealKinmokusei(t *testing.T) {
	runner := os.Getenv("VASEST_INTEGRATION_RUNNER")
	consumer := os.Getenv("VASEST_INTEGRATION_CONSUMER")
	if runner == "" || consumer == "" {
		t.Skip("requires the external Kinmokusei project")
	}
	path := filepath.Join(consumer, "watch_integration", "changes.test.km")
	source := `import { Suite, expectEqual } from "vasest";
function main(): void {
  const suite = new Suite("watch integration");
  suite.test("source changes", (): void => { expectEqual(1, 2); });
  suite.runAndExit();
}
`
	writeWatchFile(t, path, source)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	coverageDirectory := t.TempDir()
	artifactDirectory := filepath.Join(consumer, "watch_artifacts")
	historyPath := filepath.Join(consumer, "watch_integration", "failures.json")
	command := exec.CommandContext(ctx, runner, "--watch", "--watch-interval", "20ms", "--save-failures", historyPath, "--artifacts", artifactDirectory, "--coverage", coverageDirectory, "--jobs", "2", "--filter", "changes", "--timeout", "5s", "watch_integration")
	command.Dir = consumer
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { command.Process.Kill(); command.Wait() }()
	scanner := bufio.NewScanner(stdout)
	completed := 0
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.Contains(line, "file(s) passed") {
			continue
		}
		completed++
		history, historyError := readFailureHistory(historyPath)
		if historyError != nil {
			t.Fatalf("watch failure history: %v", historyError)
		}
		if completed == 1 {
			if len(history.Files) != 1 || history.Files[0].WholeFile || len(history.Files[0].Suites) != 1 || fmt.Sprint(history.Files[0].Suites[0].Cases) != "[source changes]" {
				t.Errorf("first watch history = %#v", history)
			}
			if line != "0 file(s) passed, 1 failed, 0 not run" {
				t.Errorf("first run = %s", line)
			}
			writeWatchFile(t, path, strings.Replace(source, "expectEqual(1, 2)", "expectEqual(1, 1)", 1))
			continue
		}
		if len(history.Files) != 0 {
			t.Errorf("passing watch did not clear failures: %#v", history)
		}
		if line != "1 file(s) passed, 0 failed, 0 not run" {
			t.Errorf("edited run = %s", line)
		}
		if err := command.Process.Signal(syscall.SIGTERM); err != nil {
			t.Fatal(err)
		}
		break
	}
	io.Copy(io.Discard, stdout)
	if err := command.Wait(); err != nil || completed != 2 {
		t.Fatalf("watch cycles=%d error=%v stderr=%s", completed, err, stderr.String())
	}
	entries, err := os.ReadDir(coverageDirectory)
	if err != nil || len(entries) != 2 {
		t.Fatalf("watch coverage runs=%v error=%v", entries, err)
	}
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(coverageDirectory, entry.Name(), "manifest.json"))
		var manifest CoverageManifest
		if err != nil || json.Unmarshal(data, &manifest) != nil || len(manifest.Files) != 1 {
			t.Fatalf("watch coverage manifest: %v %s", err, data)
		}
		assertCoverageProfile(t, manifest.Files[0])
	}
	entries, err = os.ReadDir(artifactDirectory)
	if err != nil || len(entries) != 2 {
		t.Fatalf("watch artifact runs=%v error=%v", entries, err)
	}
	failedSource, passingSource := false, false
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(artifactDirectory, entry.Name(), "manifest.json"))
		var manifest ArtifactManifest
		if err != nil || json.Unmarshal(data, &manifest) != nil || len(manifest.Files) != 1 {
			t.Fatalf("watch artifact manifest: %v %s", err, data)
		}
		source := string(assertSourceArchive(t, manifest.Files[0]))
		failedSource = failedSource || strings.Contains(source, "expectEqual(1, 2)")
		passingSource = passingSource || strings.Contains(source, "expectEqual(1, 1)")
		if manifest.Files[0].Html == "" {
			t.Fatalf("watch coverage HTML missing: %#v", manifest)
		}
	}
	if !failedSource || !passingSource {
		t.Fatal("watch iterations lost their source versions")
	}
}

// A real instrumented Go executable exercises covdata conversion. The fake
// compiler still verifies the build environment used by the runner.
func coverageProbe(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	source := `package main
import ("os"; "strings"; "time")
func main() {
  file := os.Getenv("VASEST_TEST_FILE")
  if strings.Contains(file, "slow") { time.Sleep(5*time.Second) }
  if strings.Contains(file, "panic") { panic("coverage probe") }
  success, status := "true", "pass"
  if strings.Contains(file, "fail") { success, status = "false", "fail" }
  report := "{\"success\":"+success+",\"summary\":{\"suite\":\"coverage probe\",\"passed\":1,\"failed\":0,\"skipped\":0,\"todo\":0,\"results\":[{\"name\":\"case\",\"passed\":true,\"status\":\"pass\"}]}}\n"
  if status == "fail" { report = "{\"success\":false,\"summary\":{\"suite\":\"coverage probe\",\"passed\":0,\"failed\":1,\"skipped\":0,\"todo\":0,\"results\":[{\"name\":\"case\",\"passed\":false,\"status\":\"fail\",\"message\":\"expected failure\"}]}}\n" }
  if err := os.WriteFile(os.Getenv("VASEST_REPORT_PATH"), []byte(report), 0600); err != nil { panic(err) }
  if status == "fail" { os.Exit(1) }
}
`
	path := filepath.Join(directory, "main.go")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	program := filepath.Join(directory, "probe")
	command := exec.Command("go", "build", "-cover", "-covermode=atomic", "-o", program, path)
	command.Env = append(os.Environ(), "GOFLAGS=", "GOCOVERDIR=")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build coverage probe: %v\n%s", err, output)
	}
	return program
}

func readCoverageManifest(t *testing.T, directory string) (string, CoverageManifest) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 {
		t.Fatalf("coverage runs: %v, %v", entries, err)
	}
	run := filepath.Join(directory, entries[0].Name())
	data, err := os.ReadFile(filepath.Join(run, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest CoverageManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Version != 1 || manifest.Source != "generated-go" {
		t.Fatalf("unexpected coverage format: %#v", manifest)
	}
	return run, manifest
}

func assertCoverageProfile(t *testing.T, entry CoverageFile) {
	t.Helper()
	if !entry.Executed || !entry.Available || entry.Error != "" || !filepath.IsAbs(entry.Data) {
		t.Fatalf("missing coverage: %#v", entry)
	}
	data, err := os.ReadFile(entry.Profile)
	if err != nil || !strings.HasPrefix(string(data), "mode: atomic\n") || len(strings.Split(string(data), "\n")) < 3 {
		t.Fatalf("invalid profile: %v\n%s", err, data)
	}
	command := exec.Command("go", "tool", "covdata", "percent", "-i="+entry.Data)
	if output, err := command.CombinedOutput(); err != nil || !strings.Contains(string(output), "coverage:") {
		t.Fatalf("unusable coverage profile: %v\n%s", err, output)
	}
}

func TestCoverageProcessesAndReporters(t *testing.T) {
	t.Setenv("VASEST_RUNNER_HELPER", "1")
	t.Setenv("KEIKA", fakeKeika(t))
	t.Setenv("VASEST_COVERAGE_PROBE", coverageProbe(t))
	// Keep existing build flags; replace the inherited runtime output directory.
	t.Setenv("GOFLAGS", "-buildvcs=false")
	inherited := t.TempDir()
	t.Setenv("GOCOVERDIR", inherited)
	input := t.TempDir()
	for _, name := range []string{"a_pass.test.km", "b_fail.test.km"} {
		os.WriteFile(filepath.Join(input, name), nil, 0600)
	}
	for _, reporter := range []string{"text", "json", "junit"} {
		t.Run(reporter, func(t *testing.T) {
			outputDirectory := t.TempDir()
			status, output := captureRunner(t, []string{"--coverage", outputDirectory, "--jobs", "2", "--reporter", reporter, input})
			if status != 1 {
				t.Fatalf("status=%d output=%s", status, output)
			}
			run, manifest := readCoverageManifest(t, outputDirectory)
			if len(manifest.Files) != 2 || manifest.Files[0].Data == manifest.Files[1].Data {
				t.Fatalf("coverage mixed files: %#v", manifest)
			}
			for _, entry := range manifest.Files {
				assertCoverageProfile(t, entry)
			}
			if reporter == "text" && !strings.Contains(output, "COVERAGE "+filepath.Join(run, "manifest.json")) {
				t.Fatalf("missing text artifact: %s", output)
			}
			if reporter == "json" {
				found := false
				for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
					var event struct{ Type, Directory, Manifest, Error string }
					if err := json.Unmarshal([]byte(line), &event); err != nil {
						t.Fatal(err)
					}
					if event.Type == "coverage" {
						found = event.Directory == run && event.Manifest == filepath.Join(run, "manifest.json") && event.Error == ""
					}
				}
				if !found {
					t.Fatalf("missing JSON artifact: %s", output)
				}
			}
			if reporter == "junit" {
				var document struct {
					XMLName xml.Name `xml:"testsuites"`
				}
				if err := xml.Unmarshal([]byte(output), &document); err != nil {
					t.Fatalf("polluted JUnit: %s", output)
				}
			}
		})
	}
	if entries, err := os.ReadDir(inherited); err != nil || len(entries) != 0 {
		t.Fatalf("runtime data leaked into inherited directory: %v %v", entries, err)
	}
}

func TestCoverageBailMissingCountersAndEmptyShard(t *testing.T) {
	t.Setenv("VASEST_RUNNER_HELPER", "1")
	t.Setenv("KEIKA", fakeKeika(t))
	t.Setenv("VASEST_COVERAGE_PROBE", coverageProbe(t))
	input := t.TempDir()
	for _, name := range []string{"a_fail.test.km", "b_pass.test.km"} {
		os.WriteFile(filepath.Join(input, name), nil, 0600)
	}
	outputDirectory := t.TempDir()
	status, output := captureRunner(t, []string{"--coverage", outputDirectory, "--bail", "--reporter", "json", input})
	if status != 1 {
		t.Fatalf("bail=%d: %s", status, output)
	}
	_, manifest := readCoverageManifest(t, outputDirectory)
	assertCoverageProfile(t, manifest.Files[0])
	if manifest.Files[1].Executed || manifest.Files[1].Available {
		t.Fatalf("not-run file measured: %#v", manifest)
	}
	for _, name := range []string{"slow", "panic"} {
		t.Run(name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), name+".test.km")
			os.WriteFile(file, nil, 0600)
			outputDirectory := t.TempDir()
			status, output := captureRunner(t, []string{"--coverage", outputDirectory, "--timeout", "100ms", "--reporter", "json", file})
			if status != 1 {
				t.Fatalf("missing counters changed failure status=%d: %s", status, output)
			}
			_, manifest := readCoverageManifest(t, outputDirectory)
			if !manifest.Files[0].Executed || manifest.Files[0].Available || manifest.Files[0].Error == "" {
				t.Fatalf("missing counters hidden: %#v", manifest)
			}
		})
	}
	outputDirectory = t.TempDir()
	status, output = captureRunner(t, []string{"--coverage", outputDirectory, "--shard", "3/3", "--reporter", "json", input})
	if status != 0 {
		t.Fatalf("empty shard=%d: %s", status, output)
	}
	_, manifest = readCoverageManifest(t, outputDirectory)
	if len(manifest.Files) != 0 {
		t.Fatalf("empty shard measured files: %#v", manifest)
	}
}

func TestCoverageArgumentsAndInfrastructureFailures(t *testing.T) {
	for _, args := range [][]string{{"--coverage"}, {"--coverage", ""}, {"--coverage", "--watch"}, {"--list", "--coverage", "coverage"}} {
		if status, output := captureRunner(t, args); status != 2 {
			t.Fatalf("run(%q)=%d: %s", args, status, output)
		}
	}
	t.Setenv("VASEST_RUNNER_HELPER", "1")
	t.Setenv("VASEST_DISABLE_COVERAGE", "1")
	t.Setenv("KEIKA", fakeKeika(t))
	file := filepath.Join(t.TempDir(), "pass.test.km")
	os.WriteFile(file, nil, 0600)
	// A compiler that ignores instrumentation must not produce silent success.
	outputDirectory := t.TempDir()
	status, output := captureRunner(t, []string{"--coverage", outputDirectory, "--reporter", "json", file})
	if status != 2 || !strings.Contains(output, "process did not emit coverage counters") {
		t.Fatalf("ignored instrumentation=%d: %s", status, output)
	}
	_, manifest := readCoverageManifest(t, outputDirectory)
	if manifest.Files[0].Available || manifest.Files[0].Error == "" {
		t.Fatalf("missing data not recorded: %#v", manifest)
	}
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	os.WriteFile(blocked, nil, 0600)
	if status, output := captureRunner(t, []string{"--coverage", blocked, file}); status != 2 || !strings.Contains(output, "coverage error:") {
		t.Fatalf("unwritable output=%d: %s", status, output)
	}
	coverage := NewCoverageRun(t.TempDir(), []string{file})
	if err := coverage.Collect(file, nil); err == nil {
		t.Fatal("missing data directory succeeded")
	}
	if err := coverage.Collect("not-selected", nil); err == nil {
		t.Fatal("unselected file succeeded")
	}
	if err := os.Mkdir(coverage.DataDirectory(file), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(coverage.DataDirectory(file), "covcounters.invalid"), []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := coverage.Collect(file, nil); err == nil {
		t.Fatal("corrupt coverage succeeded")
	}
	if err := coverage.Finish(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(coverage.Directory, "manifest.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(coverage.Directory, "manifest.json"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := coverage.Finish(); err == nil {
		t.Fatal("manifest write failure hidden")
	}
}

func TestCoverageRealKinmokusei(t *testing.T) {
	runner, consumer := os.Getenv("VASEST_INTEGRATION_RUNNER"), os.Getenv("VASEST_INTEGRATION_CONSUMER")
	if runner == "" || consumer == "" {
		t.Skip("requires the external Kinmokusei project")
	}
	for _, reporter := range []string{"text", "json", "junit"} {
		t.Run(reporter, func(t *testing.T) {
			outputDirectory := t.TempDir()
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, runner, "--coverage", outputDirectory, "--jobs", "2", "--shuffle", "42", "--reporter", reporter, "coverage")
			command.Dir = consumer
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("real coverage run: %v\n%s", err, output)
			}
			_, manifest := readCoverageManifest(t, outputDirectory)
			if len(manifest.Files) != 2 {
				t.Fatalf("files: %#v", manifest)
			}
			for _, entry := range manifest.Files {
				assertCoverageProfile(t, entry)
				command := exec.Command("go", "tool", "covdata", "func", "-i="+entry.Data)
				functions, err := command.CombinedOutput()
				if err != nil || !strings.Contains(string(functions), "\tclassify\t") || !strings.Contains(string(functions), "\tunused\t") {
					t.Fatalf("consumer code not measured: %v\n%s", err, functions)
				}
				for _, line := range strings.Split(string(functions), "\n") {
					if strings.Contains(line, "\tunused\t") && !strings.HasSuffix(line, "0.0%") {
						t.Fatalf("unused function measured as executed: %s", line)
					}
					if strings.Contains(line, "\tclassify\t") && (strings.HasSuffix(line, "\t0.0%") || strings.HasSuffix(line, "100.0%")) {
						t.Fatalf("partial consumer branch not measured: %s", line)
					}
				}
			}
			if reporter == "junit" {
				var document struct {
					XMLName xml.Name `xml:"testsuites"`
				}
				if err := xml.Unmarshal(output, &document); err != nil {
					t.Fatalf("invalid JUnit: %v\n%s", err, output)
				}
			}
		})
	}
}

func failureLocationReport(t *testing.T, location any) string {
	t.Helper()
	failure := map[string]any{"phase": "body", "message": "failure <&>"}
	if location != nil {
		failure["location"] = location
	}
	data, err := json.Marshal(map[string]any{"success": false, "summary": map[string]any{
		"suite": "locations", "passed": 0, "failed": 1, "skipped": 0, "todo": 0,
		"results": []any{map[string]any{"name": "case", "passed": false, "status": "fail", "message": "failure <&>", "failures": []any{failure}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestFailureLocationReportValidationAndCompatibility(t *testing.T) {
	for _, source := range []string{"go", "kinmokusei", "explicit"} {
		want := LocationReport{File: "tests/<&>.test.km", Line: 17, Source: source}
		report, err := readSuiteReport(failureLocationReport(t, want))
		if err != nil || report.Summary.Results[0].Failures[0].Location != want {
			t.Fatalf("valid location rejected: %v %#v", err, report)
		}
	}
	for _, location := range []any{nil, map[string]any{}, LocationReport{}} {
		report, err := readSuiteReport(failureLocationReport(t, location))
		if err != nil || report.Summary.Results[0].Failures[0].Location != (LocationReport{}) {
			t.Fatalf("legacy location rejected: %v %#v", err, report)
		}
	}
	for _, location := range []any{
		LocationReport{File: "file.test.km", Line: 0, Source: "explicit"},
		LocationReport{File: "file.test.km", Line: -1, Source: "explicit"},
		LocationReport{Line: 1, Source: "explicit"},
		LocationReport{File: " \t", Line: 1, Source: "explicit"},
		LocationReport{File: "file\n.test.km", Line: 1, Source: "explicit"},
		LocationReport{File: "file\x00.test.km", Line: 1, Source: "explicit"},
		LocationReport{File: "file.test.km", Line: 1},
		LocationReport{File: "file.test.km", Line: 1, Source: "guessed"},
		map[string]any{"file": "file.test.km", "line": "1", "source": "explicit"},
	} {
		if _, err := readSuiteReport(failureLocationReport(t, location)); err == nil {
			t.Fatalf("invalid location accepted: %#v", location)
		}
	}
}

func TestFailureLocationJSONAndJUnitRendering(t *testing.T) {
	t.Setenv("VASEST_RUNNER_HELPER", "1")
	t.Setenv("KEIKA", fakeKeika(t))
	location := LocationReport{File: "tests/<&>.test.km", Line: 17, Source: "explicit"}
	t.Setenv("VASEST_RUNNER_REPORT", failureLocationReport(t, location))
	file := filepath.Join(t.TempDir(), "locations.test.km")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, reporter := range []string{"json", "junit"} {
		status, output := captureRunner(t, []string{"--jobs", "2", "--reporter", reporter, file})
		if status != 1 {
			t.Fatalf("status=%d output=%s", status, output)
		}
		if reporter == "json" {
			found := false
			for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
				var event struct {
					Type   string
					Report SuiteReport
				}
				if err := json.Unmarshal([]byte(line), &event); err != nil {
					t.Fatal(err)
				}
				if event.Type == "suite" {
					found = event.Report.Summary.Results[0].Failures[0].Location == location
				}
			}
			if !found {
				t.Fatalf("JSON lost location: %s", output)
			}
		} else {
			var document struct {
				Suite struct {
					Case struct {
						Failure string `xml:"failure"`
					} `xml:"testcase"`
				} `xml:"testsuite"`
			}
			if err := xml.Unmarshal([]byte(output), &document); err != nil {
				t.Fatal(err)
			}
			if document.Suite.Case.Failure != "[body] failure <&>\n    at tests/<&>.test.km:17 (explicit)" {
				t.Fatalf("JUnit lost/changed location: %s", output)
			}
		}
	}
}

func TestFailureLocationsRealKinmokusei(t *testing.T) {
	runner, consumer := os.Getenv("VASEST_INTEGRATION_RUNNER"), os.Getenv("VASEST_INTEGRATION_CONSUMER")
	if runner == "" || consumer == "" {
		t.Skip("requires the external Kinmokusei project")
	}
	for _, reporter := range []string{"text", "json", "junit"} {
		t.Run(reporter, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, runner, "--jobs", "2", "--reporter", reporter, "locations")
			command.Dir = consumer
			output, err := command.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 1 {
				t.Fatalf("location suite status=%v\n%s", err, output)
			}
			if reporter == "json" {
				found := false
				for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
					var event struct {
						Type   string
						Report SuiteReport
					}
					if err := json.Unmarshal([]byte(line), &event); err != nil {
						t.Fatal(err)
					}
					if event.Type != "suite" {
						continue
					}
					found = true
					results := event.Report.Summary.Results
					if len(results) != 5 || event.Report.Summary.Failed != 4 || event.Report.Summary.Passed != 1 {
						t.Fatalf("failure counts=%#v", event.Report)
					}
					want := LocationReport{File: "locations/locations.test.km", Line: 8, Source: "explicit"}
					if results[0].Failures[0].Location != want {
						t.Fatalf("manual origin lost: %#v", results[0])
					}
					location := results[1].Failures[0].Location
					if location.Source != "go" || !strings.HasSuffix(location.File, "generated.go") || location.Line <= 0 {
						t.Fatalf("invented Kinmokusei origin: %#v", location)
					}
					data, err := os.ReadFile(location.File)
					lines := strings.Split(string(data), "\n")
					if err != nil || location.Line > len(lines) || !strings.Contains(lines[location.Line-1], "expectEqual(1, 2)") {
						t.Fatalf("Go position is not the actual assertion: %#v %v", location, err)
					}
					want.Line = 17
					if results[2].Failures[0].Phase != "cleanup" || results[2].Failures[0].Location != want {
						t.Fatalf("cleanup origin lost: %#v", results[2])
					}
					if results[3].Failures[0].Location != (LocationReport{}) || len(results[4].Failures) != 0 {
						t.Fatalf("unlocated or passing case got invented metadata: %#v", results)
					}
				}
				if !found {
					t.Fatalf("missing suite: %s", output)
				}
			} else if reporter == "junit" {
				var document struct {
					Suite struct {
						Cases []struct {
							Failure string `xml:"failure"`
						} `xml:"testcase"`
					} `xml:"testsuite"`
				}
				if err := xml.Unmarshal(output, &document); err != nil {
					t.Fatal(err)
				}
				if len(document.Suite.Cases) != 5 || !strings.Contains(document.Suite.Cases[0].Failure, "at locations/locations.test.km:8 (explicit)") || !strings.Contains(document.Suite.Cases[1].Failure, "(go)") || !strings.Contains(document.Suite.Cases[2].Failure, "at locations/locations.test.km:17 (explicit)") {
					t.Fatalf("JUnit origins lost: %s", output)
				}
			} else if !strings.Contains(string(output), "at locations/locations.test.km:8 (explicit)") || !strings.Contains(string(output), "(go)") || !strings.Contains(string(output), "at locations/locations.test.km:17 (explicit)") || !strings.Contains(string(output), "PASS source locations / later case passes") {
				t.Fatalf("text origins lost: %s", output)
			}
		})
	}
}

func readArtifactManifest(t *testing.T, directory string) (string, ArtifactManifest) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 {
		t.Fatalf("artifact runs: %v %v", entries, err)
	}
	run := filepath.Join(directory, entries[0].Name())
	data, err := os.ReadFile(filepath.Join(run, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest ArtifactManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Version != 1 {
		t.Fatalf("unknown artifact format: %#v", manifest)
	}
	return run, manifest
}

func assertSourceArchive(t *testing.T, entry ArtifactFile) []byte {
	t.Helper()
	if !entry.Executed || !entry.SourceAvailable || !filepath.IsAbs(entry.Source.Archived) || entry.Source.ImportPath == "" {
		t.Fatalf("source unavailable: %#v", entry)
	}
	data, err := os.ReadFile(entry.Source.Archived)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	if fmt.Sprintf("%x", digest) != entry.Source.Sha256 {
		t.Fatalf("source digest mismatch: %#v", entry)
	}
	return data
}

func setupArtifactCompiler(t *testing.T) {
	t.Helper()
	t.Setenv("VASEST_RUNNER_HELPER", "1")
	t.Setenv("KEIKA", fakeKeika(t))
	program := coverageProbe(t)
	t.Setenv("VASEST_ARTIFACT_PROBE", program)
	t.Setenv("VASEST_ARTIFACT_SOURCE", filepath.Join(filepath.Dir(program), "main.go"))
}

func TestArtifactsPreservePerFileBuildSourcesAndReports(t *testing.T) {
	setupArtifactCompiler(t)
	input := t.TempDir()
	os.WriteFile(filepath.Join(input, "go.mod"), []byte("module example.com/archive-fixture\n"), 0600)
	for _, name := range []string{"a_pass.test.km", "b_fail.test.km", "c_pass.test.km"} {
		os.WriteFile(filepath.Join(input, name), nil, 0600)
	}
	for _, reporter := range []string{"text", "json", "junit"} {
		t.Run(reporter, func(t *testing.T) {
			outputDirectory := t.TempDir()
			status, output := captureRunner(t, []string{"--artifacts", outputDirectory, "--jobs", "3", "--reporter", reporter, input})
			if status != 1 {
				t.Fatalf("status=%d output=%s", status, output)
			}
			run, manifest := readArtifactManifest(t, outputDirectory)
			if len(manifest.Files) != 3 || manifest.Coverage != "" {
				t.Fatalf("bad manifest: %#v", manifest)
			}
			for _, entry := range manifest.Files {
				source := assertSourceArchive(t, entry)
				wantStatus := "pass"
				if strings.Contains(entry.File, "fail") {
					wantStatus = "fail"
				}
				if entry.Status != wantStatus || entry.DurationNs <= 0 || (entry.Failure == "") != (wantStatus == "pass") {
					t.Fatalf("archived execution result incorrect: %#v", entry)
				}
				if !strings.Contains(string(source), "// source for "+filepath.Base(entry.File)) || entry.Error != "" {
					t.Fatalf("source belongs to another file: %#v", entry)
				}
				data, err := os.ReadFile(entry.Report)
				if err != nil {
					t.Fatal(err)
				}
				report, err := readSuiteReport(strings.TrimSpace(string(data)))
				if err != nil || report.Success == strings.Contains(entry.File, "fail") {
					t.Fatalf("wrong report: %v %s", err, data)
				}
				if _, err := os.ReadFile(entry.Output); err != nil {
					t.Fatal(err)
				}
			}
			if reporter == "text" && (!strings.Contains(output, "ARTIFACTS "+filepath.Join(run, "manifest.json")) || strings.Count(output, "FILE "+input+string(os.PathSeparator)) != 3) {
				t.Fatalf("text artifact output missing/duplicated: %s", output)
			}
			if reporter == "json" {
				found := false
				for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
					var event struct{ Type, Directory, Manifest, Error string }
					if err := json.Unmarshal([]byte(line), &event); err != nil {
						t.Fatal(err)
					}
					if event.Type == "artifacts" {
						found = event.Directory == run && event.Manifest == filepath.Join(run, "manifest.json") && event.Error == ""
					}
				}
				if !found {
					t.Fatalf("missing JSON artifacts: %s", output)
				}
			}
			if reporter == "junit" {
				var document struct {
					XMLName xml.Name `xml:"testsuites"`
				}
				if err := xml.Unmarshal([]byte(output), &document); err != nil {
					t.Fatalf("polluted JUnit: %s", output)
				}
			}
			// A later compiler write must not change any retained version.
			os.WriteFile(filepath.Join(input, ".kinmokusei", "gen", "generated.go"), []byte("overwritten"), 0600)
			for _, entry := range manifest.Files {
				assertSourceArchive(t, entry)
			}
		})
	}
}

func TestArtifactFailuresBailAndEmptyShard(t *testing.T) {
	setupArtifactCompiler(t)
	input := t.TempDir()
	os.WriteFile(filepath.Join(input, "a_fail.test.km"), nil, 0600)
	os.WriteFile(filepath.Join(input, "b_pass.test.km"), nil, 0600)
	outputDirectory := t.TempDir()
	status, output := captureRunner(t, []string{"--artifacts", outputDirectory, "--bail", "--reporter", "json", input})
	if status != 1 {
		t.Fatalf("bail=%d: %s", status, output)
	}
	_, manifest := readArtifactManifest(t, outputDirectory)
	assertSourceArchive(t, manifest.Files[0])
	if manifest.Files[1].Executed || manifest.Files[1].SourceAvailable || manifest.Files[1].Status != "not-run" || manifest.Files[1].Failure != "" || manifest.Files[1].DurationNs != 0 {
		t.Fatalf("unexecuted file archived: %#v", manifest)
	}
	for _, name := range []string{"compile_error", "panic", "slow"} {
		t.Run(name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), name+".test.km")
			os.WriteFile(file, nil, 0600)
			outputDirectory := t.TempDir()
			status, output := captureRunner(t, []string{"--artifacts", outputDirectory, "--reporter", "json", "--timeout", "100ms", file})
			if status != 1 {
				t.Fatalf("status=%d output=%s", status, output)
			}
			_, manifest := readArtifactManifest(t, outputDirectory)
			entry := manifest.Files[0]
			if entry.Status != "fail" || entry.Failure == "" || entry.DurationNs <= 0 {
				t.Fatalf("compile/panic/timeout result missing: %#v", entry)
			}
			if name == "compile_error" {
				if entry.SourceAvailable || entry.Error == "" {
					t.Fatalf("stale source archived: %#v", entry)
				}
				data, _ := os.ReadFile(entry.Output)
				if !strings.Contains(string(data), "compiler rejected input") {
					t.Fatalf("compile output missing: %s", data)
				}
			} else {
				assertSourceArchive(t, entry)
			}
		})
	}
	outputDirectory = t.TempDir()
	status, output = captureRunner(t, []string{"--artifacts", outputDirectory, "--shard", "3/3", "--reporter", "json", input})
	if status != 0 {
		t.Fatalf("empty shard=%d: %s", status, output)
	}
	_, manifest = readArtifactManifest(t, outputDirectory)
	if len(manifest.Files) != 0 {
		t.Fatalf("empty shard has files: %#v", manifest)
	}
}

func TestArtifactArgumentsAndWriteFailures(t *testing.T) {
	for _, args := range [][]string{{"--artifacts"}, {"--artifacts", ""}, {"--artifacts", "--watch"}, {"--list", "--artifacts", ".artifacts"}} {
		if status, output := captureRunner(t, args); status != 2 {
			t.Fatalf("run(%q)=%d: %s", args, status, output)
		}
	}
	setupArtifactCompiler(t)
	file := filepath.Join(t.TempDir(), "pass.test.km")
	os.WriteFile(file, nil, 0600)
	blocked := filepath.Join(t.TempDir(), "blocked")
	os.WriteFile(blocked, nil, 0600)
	if status, output := captureRunner(t, []string{"--artifacts", blocked, file}); status != 2 || !strings.Contains(output, "artifact error:") {
		t.Fatalf("blocked output=%d: %s", status, output)
	}
	// Source archival failures stop execution, retain the manifest, and return 2.
	t.Setenv("VASEST_ARTIFACT_PROBE", "")
	outputDirectory := t.TempDir()
	status, output := captureRunner(t, []string{"--artifacts", outputDirectory, "--reporter", "json", file})
	if status != 2 || !strings.Contains(output, "cannot archive generated source") {
		t.Fatalf("unsupported compiler output=%d: %s", status, output)
	}
	_, manifest := readArtifactManifest(t, outputDirectory)
	if manifest.Files[0].SourceAvailable || manifest.Files[0].Error == "" || manifest.Files[0].Status != "fail" || !strings.Contains(manifest.Files[0].Failure, "cannot archive generated source") {
		t.Fatalf("archive failure hidden: %#v", manifest)
	}
	artifacts := NewArtifactRun(t.TempDir(), []string{file}, "")
	if err := artifacts.Collect(file, FileExecution{}, ""); err == nil {
		t.Fatal("missing output directory accepted")
	}
	if err := artifacts.Collect("not-selected", FileExecution{}, ""); err == nil {
		t.Fatal("unknown file accepted")
	}
	os.Mkdir(artifacts.FileDirectory(file), 0700)
	if err := artifacts.Collect(file, FileExecution{}, ""); err == nil {
		t.Fatal("missing source metadata accepted")
	}
	os.WriteFile(filepath.Join(artifacts.FileDirectory(file), "source.json"), []byte("bad JSON"), 0600)
	if err := artifacts.Collect(file, FileExecution{}, ""); err == nil {
		t.Fatal("corrupt metadata accepted")
	}
	if err := artifacts.Finish(); err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(artifacts.Directory, "manifest.json"))
	os.Mkdir(filepath.Join(artifacts.Directory, "manifest.json"), 0700)
	if err := artifacts.Finish(); err == nil {
		t.Fatal("manifest write failure hidden")
	}
}

func TestArchivedSourcesDoNotRetriggerWatch(t *testing.T) {
	root := t.TempDir()
	writeWatchFile(t, filepath.Join(root, "test.test.km"), "source")
	before, err := watchSignature([]string{root}, true)
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "artifacts", "run-one", "file-0001")
	writeWatchFile(t, filepath.Join(output, ".source", "generated.go"), "generated code")
	for _, name := range []string{"source.json", "output.txt", "suites.jsonl", "coverage.out", "coverage.html"} {
		writeWatchFile(t, filepath.Join(output, name), "artifact contents")
	}
	after, err := watchSignature([]string{root}, true)
	if err != nil || before != after {
		t.Fatalf("artifacts triggered watch: %q -> %q, %v", before, after, err)
	}
}

func TestArchiveCoverageUsesRetainedSourcesAfterCompilerOverwrite(t *testing.T) {
	root := t.TempDir()
	source := []byte("package main\nfunc classify(value int) int { if value > 0 { return 1 }; return 0 }\nfunc main() { classify(1) }\n")
	originalSource := filepath.Join(root, "generated.go")
	os.WriteFile(originalSource, source, 0600)
	os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/retained-source\n\ngo 1.23\n"), 0600)
	executable := filepath.Join(root, "program")
	command := exec.Command("go", "build", "-buildvcs=false", "-cover", "-covermode=atomic", "-o", executable, ".")
	command.Dir = root
	command.Env = append(os.Environ(), "GOFLAGS=", "GOWORK=off", "GOCOVERDIR=")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	raw := t.TempDir()
	command = exec.Command(executable)
	command.Env = append(os.Environ(), "GOCOVERDIR="+raw)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("run: %v\n%s", err, output)
	}
	originalProfile := filepath.Join(raw, "coverage.out")
	command = exec.Command("go", "tool", "covdata", "textfmt", "-i="+raw, "-o="+originalProfile)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("profile: %v\n%s", err, output)
	}
	before, _ := os.ReadFile(originalProfile)
	archive := filepath.Join(t.TempDir(), "saved sources with spaces")
	os.Mkdir(archive, 0700)
	archivedSource := filepath.Join(archive, "generated.go")
	os.WriteFile(archivedSource, source, 0600)
	snapshot := SourceSnapshot{Original: originalSource, Archived: archivedSource, ImportPath: "example.com/retained-source"}
	// The live generated file now belongs to another test and is no longer Go.
	os.WriteFile(originalSource, []byte("overwritten by another build"), 0600)
	profile, html := filepath.Join(archive, "coverage.out"), filepath.Join(archive, "coverage.html")
	if err := archiveCoverage(originalProfile, profile, html, snapshot); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(originalProfile)
	if string(before) != string(after) {
		t.Fatal("raw coverage profile was modified")
	}
	mapped, _ := os.ReadFile(profile)
	if !strings.Contains(string(mapped), archivedSource+":") || strings.Contains(string(mapped), "example.com/retained-source/generated.go:") {
		t.Fatalf("profile not remapped: %s", mapped)
	}
	page, err := os.ReadFile(html)
	if err != nil || !strings.Contains(string(page), "classify") || strings.Contains(string(page), "overwritten by another build") {
		t.Fatalf("HTML uses incorrect source: %v\n%s", err, page)
	}
	command = exec.Command("go", "tool", "cover", "-func="+profile)
	if output, err := command.CombinedOutput(); err != nil || !strings.Contains(string(output), "classify") {
		t.Fatalf("retained function coverage unusable: %v\n%s", err, output)
	}
	if err := archiveCoverage(originalProfile, profile, html, SourceSnapshot{ImportPath: "wrong-package"}); err == nil {
		t.Fatal("unmatched profile silently remapped")
	}
	if err := archiveCoverage("missing-profile", profile, html, snapshot); err == nil {
		t.Fatal("missing profile accepted")
	}
}

func TestArtifactsRealKinmokusei(t *testing.T) {
	runner, consumer := os.Getenv("VASEST_INTEGRATION_RUNNER"), os.Getenv("VASEST_INTEGRATION_CONSUMER")
	if runner == "" || consumer == "" {
		t.Skip("requires the external Kinmokusei project")
	}
	for _, reporter := range []string{"text", "json", "junit"} {
		t.Run(reporter, func(t *testing.T) {
			outputDirectory, coverageDirectory := t.TempDir(), t.TempDir()
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, runner, "--artifacts", outputDirectory, "--coverage", coverageDirectory, "--jobs", "2", "--shuffle", "42", "--reporter", reporter, "coverage")
			command.Dir = consumer
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("real artifact run: %v\n%s", err, output)
			}
			_, manifest := readArtifactManifest(t, outputDirectory)
			coverageRun, _ := readCoverageManifest(t, coverageDirectory)
			if len(manifest.Files) != 2 || manifest.Coverage != coverageRun {
				t.Fatalf("missing artifacts or coverage link: %#v", manifest)
			}
			for _, entry := range manifest.Files {
				code := assertSourceArchive(t, entry)
				if entry.Status != "pass" || entry.Failure != "" || entry.DurationNs <= 0 {
					t.Fatalf("successful instrumented result missing: %#v", entry)
				}
				name := "positive coverage"
				if strings.HasSuffix(entry.File, "negative.test.km") {
					name = "negative coverage"
				}
				if !strings.Contains(string(code), name) || entry.Error != "" {
					t.Fatalf("archive belongs to another executable: %#v", entry)
				}
				data, err := os.ReadFile(entry.Report)
				if err != nil {
					t.Fatal(err)
				}
				report, err := readSuiteReport(strings.TrimSpace(string(data)))
				if err != nil || !report.Success {
					t.Fatalf("raw suite report: %v %s", err, data)
				}
				page, err := os.ReadFile(entry.Html)
				if err != nil || !strings.Contains(string(page), name) || !strings.Contains(string(page), "classify") {
					t.Fatalf("HTML uses wrong source: %v %s", err, page)
				}
				// Another build overwrites the compiler's shared source. The
				// retained profile must still work against its own version.
				os.WriteFile(entry.Source.Original, []byte("source overwritten after tests"), 0600)
				command := exec.Command("go", "tool", "cover", "-func="+entry.Profile)
				functions, err := command.CombinedOutput()
				if err != nil || !strings.Contains(string(functions), "classify") {
					t.Fatalf("archived profile lost source: %v\n%s", err, functions)
				}
			}
			if reporter == "junit" {
				var document struct {
					XMLName xml.Name `xml:"testsuites"`
				}
				if err := xml.Unmarshal(output, &document); err != nil {
					t.Fatalf("polluted JUnit: %s", output)
				}
			}
		})
	}
}

func TestFailureHistoryRecordsExactCasesSharedFailuresAndProcessFallback(t *testing.T) {
	for _, tc := range []struct {
		name, report          string
		process               bool
		wholeFile, wholeSuite bool
		wantCase              string
	}{
		{"body", `{"success":false,"summary":{"suite":"suite","passed":1,"failed":1,"skipped":0,"todo":0,"results":[{"name":"pass","passed":true,"status":"pass"},{"name":"case","passed":false,"status":"fail"}]}}`, false, false, false, "case"},
		{"todo", `{"success":false,"summary":{"suite":"suite","failOnTodo":true,"passed":0,"failed":0,"skipped":0,"todo":1,"results":[{"name":"future","passed":false,"status":"todo"}]}}`, false, false, false, "future"},
		{"shared", `{"success":false,"summary":{"suite":"suite","passed":0,"failed":1,"skipped":0,"todo":0,"results":[{"name":"<beforeAll>","passed":false,"status":"fail","failures":[{"phase":"beforeAll","message":"setup"}],"message":"setup"}]}}`, false, false, true, ""},
		{"panic after report", `{"success":true,"summary":{"suite":"suite","passed":1,"failed":0,"skipped":0,"todo":0,"results":[{"name":"case","passed":true,"status":"pass"}]}}`, true, true, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report, err := readSuiteReport(tc.report)
			if err != nil {
				t.Fatal(err)
			}
			execution := FileExecution{executed: true, suites: []ReportedSuite{{raw: tc.report, report: report}}}
			if tc.process {
				execution.runError = errors.New("panic")
			}
			entry := failedFile("tests/probe.test.km", execution)
			if entry.WholeFile != tc.wholeFile || (!entry.WholeFile && (len(entry.Suites) != 1 || entry.Suites[0].WholeSuite != tc.wholeSuite)) {
				t.Fatalf("entry=%#v", entry)
			}
			if tc.wantCase != "" && fmt.Sprint(entry.Suites[0].Cases) != "["+tc.wantCase+"]" {
				t.Fatalf("cases=%#v", entry)
			}
		})
	}
	entry := failedFile("empty.test.km", FileExecution{reportFailure: "no test suites ran"})
	if !entry.WholeFile {
		t.Fatal("missing report did not select file")
	}
}

func TestFailureHistoryIsPortableValidatedAndAtomicallyReplaced(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "failures.json")
	file := filepath.Join(dir, "tests", "金木犀.test.km")
	entry := FailedFile{File: file, Suites: []FailedSuite{{Name: "suite", Cases: []string{"case"}}}}
	if err := saveFailureHistory(path, []FailedFile{entry}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), dir) {
		t.Fatalf("absolute history path: %s", data)
	}
	history, err := readFailureHistory(path)
	if err != nil {
		t.Fatal(err)
	}
	selection, err := rerunSelection(history, path, file)
	if err != nil || !strings.Contains(selection, `"cases":["case"]`) {
		t.Fatalf("selection=%s %v", selection, err)
	}
	if err := saveFailureHistory(path, nil); err != nil {
		t.Fatal(err)
	}
	history, err = readFailureHistory(path)
	if err != nil || len(history.Files) != 0 {
		t.Fatalf("clear history: %#v %v", history, err)
	}
	if err := saveFailureHistory(filepath.Join(dir, "absent", "history.json"), nil); err == nil {
		t.Fatal("write failure accepted")
	}
	for _, invalid := range []string{`null`, `{}`, `{"version":2,"files":[]}`, `{"version":1,"files":null}`, `{"version":1,"files":[],"extra":true}`, `{"version":1,"files":[]} {}`, `{"version":1,"files":[{"file":"/private/path.test.km","wholeFile":true}]}`, `{"version":1,"files":[{"file":"a.test.km"}]}`, `{"version":1,"files":[{"file":"a.test.km","wholeFile":true},{"file":"./a.test.km","wholeFile":true}]}`, `{"version":1,"files":[{"file":"a.test.km","suites":[{"name":"suite","cases":[""]}]}]}`} {
		os.WriteFile(path, []byte(invalid), 0600)
		if _, err := readFailureHistory(path); err == nil {
			t.Fatalf("invalid history accepted: %s", invalid)
		}
	}
	// A failed rename must preserve the previous destination and remove temp files.
	os.Mkdir(filepath.Join(dir, "blocked.json"), 0700)
	if err := saveFailureHistory(filepath.Join(dir, "blocked.json"), nil); err == nil {
		t.Fatal("rename to directory succeeded")
	}
	leftovers, _ := filepath.Glob(filepath.Join(dir, ".vasest-failures-*"))
	if len(leftovers) != 0 {
		t.Fatalf("temporary history leaked: %v", leftovers)
	}
}

func TestFailureHistoryRunnerOptionsEmptyAndStaleSelections(t *testing.T) {
	for _, args := range [][]string{{"--save-failures"}, {"--rerun-failed"}, {"--save-failures", "--watch"}, {"--rerun-failed", "history.km"}, {"--list", "--save-failures", "history.json"}, {"--watch", "--rerun-failed", "history.json"}} {
		if code, _ := captureRunner(t, args); code != 2 {
			t.Fatalf("accepted %v status %d", args, code)
		}
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "probe.test.km"), nil, 0600)
	path := filepath.Join(dir, "failures.json")
	if err := saveFailureHistory(path, nil); err != nil {
		t.Fatal(err)
	}
	for _, reporter := range []string{"text", "json", "junit"} {
		code, output := captureRunner(t, []string{"--rerun-failed", path, "--reporter", reporter, dir})
		if code != 0 || strings.Contains(output, "FILE ") {
			t.Fatalf("empty history status=%d output=%s", code, output)
		}
	}
	os.WriteFile(path, []byte(`{"version":1,"files":[{"file":"removed.test.km","wholeFile":true}]}`), 0600)
	before, _ := os.ReadFile(path)
	code, output := captureRunner(t, []string{"--rerun-failed", path, "--save-failures", path, "--reporter", "json", dir})
	after, _ := os.ReadFile(path)
	if code != 1 || !strings.Contains(output, `"noMatch":true`) || string(after) != string(before) {
		t.Fatalf("stale selection status=%d output=%s history=%s", code, output, after)
	}
	code, _ = captureRunner(t, []string{"--rerun-failed", path, "--shard", "2/2", dir})
	if code != 0 {
		t.Fatalf("empty shard status=%d", code)
	}
	// A shard with ordinary discovered files but no recorded failures succeeds.
	os.WriteFile(filepath.Join(dir, "z_pass.test.km"), nil, 0600)
	os.WriteFile(path, []byte(`{"version":1,"files":[{"file":"probe.test.km","wholeFile":true}]}`), 0600)
	code, output = captureRunner(t, []string{"--rerun-failed", path, "--shard", "2/2", "--reporter", "json", dir})
	if code != 0 || !strings.Contains(output, `"noMatch":false`) || !strings.Contains(output, `"passedFiles":0`) {
		t.Fatalf("shard without recorded failures: status=%d output=%s", code, output)
	}
	os.WriteFile(path, []byte("bad JSON"), 0600)
	if code, _ := captureRunner(t, []string{"--rerun-failed", path, dir}); code != 2 {
		t.Fatal("bad history accepted")
	}
}

func TestRerunRealKinmokusei(t *testing.T) {
	runner, consumer := os.Getenv("VASEST_INTEGRATION_RUNNER"), os.Getenv("VASEST_INTEGRATION_CONSUMER")
	if runner == "" || consumer == "" {
		t.Skip("requires the external Kinmokusei project")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "failures.json")
	invoke := func(reporter string, fixed bool, args ...string) (int, string) {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, runner, append([]string{"--reporter", reporter}, args...)...)
		command.Dir = consumer
		value := "0"
		if fixed {
			value = "1"
		}
		command.Env = append(os.Environ(), "VASEST_RERUN_FIXED="+value)
		output, err := command.CombinedOutput()
		if err == nil {
			return 0, string(output)
		}
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("run error: %v %s", err, output)
		}
		return exit.ExitCode(), string(output)
	}
	code, output := invoke("json", false, "--jobs", "2", "--save-failures", path, "rerun")
	if code != 1 {
		t.Fatalf("initial status=%d %s", code, output)
	}
	history, err := readFailureHistory(path)
	if err != nil || len(history.Files) != 1 || history.Files[0].WholeFile || len(history.Files[0].Suites) != 1 || fmt.Sprint(history.Files[0].Suites[0].Cases) != "[case row 1]" {
		t.Fatalf("exact history=%#v %v output=%s", history, err, output)
	}
	for _, reporter := range []string{"text", "json", "junit"} {
		t.Run(reporter, func(t *testing.T) {
			artifacts, coverage := t.TempDir(), t.TempDir()
			code, output := invoke(reporter, true, "--rerun-failed", path, "--jobs", "2", "--shuffle", "42", "--artifacts", artifacts, "--coverage", coverage, "rerun")
			if code != 0 || strings.Contains(output, "OTHER_SETUP") || strings.Contains(output, "OTHER_BODY") || strings.Contains(output, "EXTRA_BODY") || strings.Contains(output, "z.test.km") {
				t.Fatalf("rerun status=%d %s", code, output)
			}
			_, manifest := readArtifactManifest(t, artifacts)
			if len(manifest.Files) != 1 {
				t.Fatalf("selected artifacts=%#v", manifest)
			}
			data, err := os.ReadFile(manifest.Files[0].Report)
			if err != nil {
				t.Fatal(err)
			}
			var selected SummaryReport
			for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
				report, err := readSuiteReport(line)
				if err != nil {
					t.Fatal(err)
				}
				if report.Summary.Suite == "選択 suite" {
					selected = report.Summary
				}
			}
			if selected.Passed != 2 || selected.Skipped != 2 || len(selected.Results) != 2 {
				t.Fatalf("selected summary=%#v", selected)
			}
			if reporter == "junit" {
				var document struct {
					XMLName xml.Name `xml:"testsuites"`
				}
				if err := xml.Unmarshal([]byte(output), &document); err != nil {
					t.Fatalf("polluted XML: %s", output)
				}
			}
		})
	}
	// Exact selections with renamed cases fail and preserve the existing history.
	stale := filepath.Join(dir, "stale.json")
	history.Files[0].Suites[0].Cases = []string{"removed case"}
	history.Files[0].File = filepath.Join(consumer, "rerun", "a.test.km")
	if err := saveFailureHistory(stale, history.Files); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(stale)
	code, output = invoke("json", true, "--rerun-failed", stale, "--save-failures", stale, "rerun")
	after, _ := os.ReadFile(stale)
	if code != 1 || !strings.Contains(output, `"noMatch":true`) || string(before) != string(after) {
		t.Fatalf("stale cases status=%d output=%s", code, output)
	}
	// A restricted rerun can atomically replace its own history after passing.
	code, output = invoke("json", true, "--rerun-failed", path, "--save-failures", path, "--filter", "case", "rerun")
	history, err = readFailureHistory(path)
	if code != 0 || err != nil || len(history.Files) != 0 {
		t.Fatalf("history refresh status=%d history=%#v %v %s", code, history, err, output)
	}
	// Shared setup failure selects the entire suite, including previously skipped bodies.
	t.Setenv("VASEST_RERUN_SHARED", "1")
	code, output = invoke("json", true, "--save-failures", path, "rerun")
	history, err = readFailureHistory(path)
	if code != 1 || err != nil || len(history.Files) != 1 || !history.Files[0].Suites[0].WholeSuite {
		t.Fatalf("shared selection=%#v %v %s", history, err, output)
	}
	t.Setenv("VASEST_RERUN_SHARED", "0")
	code, output = invoke("text", true, "--rerun-failed", path, "rerun")
	if code != 0 || !strings.Contains(output, "EXTRA_BODY") || strings.Contains(output, "OTHER_SETUP") {
		t.Fatalf("shared rerun status=%d %s", code, output)
	}
}

func TestFailureHistoryRunnerFallbackBailAndSaveErrors(t *testing.T) {
	t.Setenv("VASEST_RUNNER_HELPER", "1")
	t.Setenv("KEIKA", fakeKeika(t))
	input := t.TempDir()
	for _, name := range []string{"a_invalid.test.km", "b_missing.test.km", "c_slow.test.km", "z_pass.test.km"} {
		if err := os.WriteFile(filepath.Join(input, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(t.TempDir(), "failures.json")
	code, output := captureRunner(t, []string{"--jobs", "2", "--timeout", "1s", "--save-failures", path, "--reporter", "json", input})
	history, err := readFailureHistory(path)
	if code != 1 || err != nil || len(history.Files) != 3 {
		t.Fatalf("fallback status=%d history=%#v error=%v output=%s", code, history, err, output)
	}
	for _, file := range history.Files {
		if !file.WholeFile {
			t.Fatalf("incomplete failure narrowed: %#v", file)
		}
	}
	code, output = captureRunner(t, []string{"--bail", "--timeout", "1s", "--save-failures", path, "--reporter", "json", input})
	history, err = readFailureHistory(path)
	if code != 1 || err != nil || len(history.Files) != 1 || !strings.Contains(output, `"notRunFiles":3`) {
		t.Fatalf("bail history=%#v error=%v output=%s", history, err, output)
	}
	for _, reporter := range []string{"text", "json", "junit"} {
		code, output = captureRunner(t, []string{"--timeout", "1s", "--save-failures", filepath.Join(t.TempDir(), "missing", "history.json"), "--reporter", reporter, filepath.Join(input, "z_pass.test.km")})
		if code != 2 || !strings.Contains(output, "cannot save failure history") {
			t.Fatalf("save error reporter=%s status=%d output=%s", reporter, code, output)
		}
	}
}

func useWorkingDirectory(t *testing.T, directory string) {
	t.Helper()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(directory); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(previous); err != nil {
			t.Error(err)
		}
	})
}

func TestRunnerConfigurationValidationAndOptionBoundaries(t *testing.T) {
	dir := t.TempDir()
	useWorkingDirectory(t, dir)
	path := filepath.Join(dir, "custom.json")
	for _, invalid := range []string{`null`, `{}`, `{"version":2}`, `{"version":1,"extra":true}`, `{"version":1} {}`, `{"version":1,"jobs":0}`, `{"version":1,"jobs":1.5}`, `{"version":1,"jobs":"2"}`, `{"version":1,"bail":"false"}`, `{"version":1,"timeout":"0s"}`, `{"version":1,"timeout":"bad"}`, `{"version":1,"reporter":"html"}`, `{"version":1,"filter":""}`, `{"version":1,"testPath":""}`, `{"version":1,"testPath":"a\nb"}`} {
		os.WriteFile(path, []byte(invalid), 0600)
		if _, err := readRunnerConfig(path); err == nil {
			t.Fatalf("accepted config %s", invalid)
		}
		if code, _ := captureRunner(t, []string{"--config", path}); code != 2 {
			t.Fatalf("config status=%d %s", code, invalid)
		}
	}
	os.WriteFile(path, []byte(`{"version":1,"jobs":null,"bail":false,"testPath":"../tests"}`), 0600)
	input, err := prepareRunnerInput([]string{"--config", path, "--coverage", "--no-config", "--watch-interval", "--config", "--filter", "--list"})
	if err != nil || input.listOnly || input.path != filepath.Join(filepath.Dir(dir), "tests") || input.config.Bail == nil || *input.config.Bail {
		t.Fatalf("option boundaries input=%#v error=%v", input, err)
	}
	input, err = prepareRunnerInput([]string{"--config", path, "--filter", "needle", "explicit.test.km"})
	if err != nil || input.path != "explicit.test.km" || fmt.Sprint(input.args) != "[--filter needle explicit.test.km]" {
		t.Fatalf("path override %#v %v", input, err)
	}
	for _, args := range [][]string{{"--config"}, {"--config", ""}, {"--config", "settings.toml"}, {"--config", "--watch"}, {"--config", path, "--no-config"}, {"--config", "missing.json"}} {
		if code, _ := captureRunner(t, args); code != 2 {
			t.Fatalf("accepted %v status=%d", args, code)
		}
	}
	os.WriteFile("vasest.config.json", []byte("broken"), 0600)
	for _, args := range [][]string{{"--help"}, {"--version"}, {"--config", "missing.json", "--help"}} {
		if code, _ := captureRunner(t, args); code != 0 {
			t.Fatalf("information depends on configuration: %v %d", args, code)
		}
	}
	if _, err := prepareRunnerInput([]string{"--no-config"}); err != nil {
		t.Fatal(err)
	}
}

func TestRunnerConfigurationDefaultsOverridesListingAndAutomaticLoading(t *testing.T) {
	dir := t.TempDir()
	useWorkingDirectory(t, dir)
	os.Mkdir("selected", 0700)
	for _, name := range []string{"a.test.km", "b.test.km"} {
		os.WriteFile(filepath.Join("selected", name), nil, 0600)
	}
	t.Setenv("VASEST_RUNNER_HELPER", "1")
	t.Setenv("VASEST_CONFIG_PROBE", "1")
	t.Setenv("KEIKA", fakeKeika(t))
	path := filepath.Join(dir, "vasest.config.json")
	os.WriteFile(path, []byte(`{"version":1,"testPath":"selected","jobs":2,"timeout":"2s","reporter":"json","filter":"needle","bail":true,"failOnTodo":true}`), 0600)
	code, output := captureRunner(t, nil)
	if code != 1 || !strings.Contains(output, `"jobs":2`) || !strings.Contains(output, "policy:needle:1:1") || !strings.Contains(output, `"noMatch":true`) {
		t.Fatalf("defaults status=%d output=%s", code, output)
	}
	code, output = captureRunner(t, []string{"--no-filter", "--no-bail", "--allow-todo", "--no-timeout", "--jobs", "3"})
	if code != 0 || !strings.Contains(output, `"jobs":3`) || !strings.Contains(output, "policy:::") || !strings.Contains(output, `"failOnTodo":false`) {
		t.Fatalf("overrides status=%d output=%s", code, output)
	}
	code, output = captureRunner(t, []string{"--list"})
	if code != 0 || strings.Contains(output, "policy:") || len(strings.Split(strings.TrimSpace(output), "\n")) != 2 {
		t.Fatalf("listing inherited execution settings: status=%d %s", code, output)
	}
	code, output = captureRunner(t, []string{"--no-config", "--jobs", "2", "selected"})
	if code != 0 || strings.Contains(output, `"type"`) || !strings.Contains(output, "policy:::") {
		t.Fatalf("disabled defaults status=%d output=%s", code, output)
	}
	code, output = captureRunner(t, []string{"--reporter", "junit", "--no-filter", "--allow-todo", "selected/a.test.km"})
	var document struct {
		XMLName xml.Name `xml:"testsuites"`
	}
	if code != 0 || xml.Unmarshal([]byte(output), &document) != nil || strings.Contains(output, "b.test.km") {
		t.Fatalf("CLI path/reporter override status=%d output=%s", code, output)
	}
	// A timeout is inherited until explicitly cleared.
	os.WriteFile(filepath.Join("selected", "slow.test.km"), nil, 0600)
	os.WriteFile(path, []byte(`{"version":1,"testPath":"selected/slow.test.km","timeout":"100ms","reporter":"json"}`), 0600)
	code, output = captureRunner(t, nil)
	if code != 1 || !strings.Contains(output, "timed out after 100ms") {
		t.Fatalf("configured timeout status=%d output=%s", code, output)
	}
}

func TestConfigurationSignatureTracksExplicitFilesOnly(t *testing.T) {
	root, external := t.TempDir(), t.TempDir()
	config := filepath.Join(external, "settings.json")
	roots := []string{root, config}
	initial, err := watchSignature(roots, true)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(config, []byte(`{"version":1}`), 0600)
	created, err := watchSignature(roots, true)
	if err != nil || created == initial {
		t.Fatalf("creation not tracked: %v", err)
	}
	os.WriteFile(config, []byte("invalid config"), 0600)
	changed, err := watchSignature(roots, true)
	if err != nil || changed == created {
		t.Fatalf("invalid edit not tracked: %v", err)
	}
	os.WriteFile(filepath.Join(root, "failures.json"), []byte(`{"version":1}`), 0600)
	unchanged, err := watchSignature(roots, true)
	if err != nil || unchanged != changed {
		t.Fatalf("unrelated JSON retriggers: %v", err)
	}
	os.Remove(config)
	deleted, err := watchSignature(roots, true)
	if err != nil || deleted != initial {
		t.Fatalf("deletion not tracked: %v", err)
	}
}

func TestConfigurationRealKinmokusei(t *testing.T) {
	runner, consumer := os.Getenv("VASEST_INTEGRATION_RUNNER"), os.Getenv("VASEST_INTEGRATION_CONSUMER")
	if runner == "" || consumer == "" {
		t.Skip("requires the external Kinmokusei project")
	}
	directory := filepath.Join(consumer, "configuration")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	custom := filepath.Join(directory, "settings.json")
	if err := os.WriteFile(custom, []byte(`{"version":1,"testPath":"../coverage","jobs":2,"timeout":"5s","reporter":"json","filter":"positive","bail":true,"failOnTodo":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	invoke := func(args ...string) (int, string) {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, runner, args...)
		command.Dir = consumer
		output, err := command.CombinedOutput()
		if err == nil {
			return 0, string(output)
		}
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("configuration run: %v %s", err, output)
		}
		return exit.ExitCode(), string(output)
	}
	code, output := invoke("--config", custom)
	if code != 0 || !strings.Contains(output, `"jobs":2`) || !strings.Contains(output, `"noMatch":false`) || !strings.Contains(output, "PASS positive coverage / positive value") || strings.Contains(output, "PASS negative coverage") {
		t.Fatalf("configured run status=%d %s", code, output)
	}
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if !json.Valid([]byte(line)) {
			t.Fatalf("polluted configured JSON: %s", output)
		}
	}
	code, output = invoke("--config", custom, "--list")
	if code != 0 || !strings.Contains(output, "positive.test.km") || !strings.Contains(output, "negative.test.km") {
		t.Fatalf("configured list status=%d %s", code, output)
	}
	code, output = invoke("--config", custom, "--no-filter", "tests/statuses.test.km")
	if code != 1 || !strings.Contains(output, `"failOnTodo":true`) {
		t.Fatalf("strict todo default status=%d %s", code, output)
	}
	code, output = invoke("--config", custom, "--jobs", "1", "--reporter", "junit", "--no-filter", "--no-timeout", "--no-bail", "--allow-todo", "tests/statuses.test.km")
	var document struct {
		XMLName  xml.Name `xml:"testsuites"`
		Tests    int      `xml:"tests,attr"`
		Failures int      `xml:"failures,attr"`
	}
	if code != 0 || xml.Unmarshal([]byte(output), &document) != nil || document.Tests != 3 || document.Failures != 0 {
		t.Fatalf("CLI overrides status=%d %s", code, output)
	}
	automatic := filepath.Join(consumer, "vasest.config.json")
	t.Cleanup(func() { os.Remove(automatic) })
	os.WriteFile(automatic, []byte(`{"version":1,"testPath":"coverage","jobs":2,"reporter":"json"}`), 0600)
	code, output = invoke()
	if code != 0 || !strings.Contains(output, `"passedFiles":2`) {
		t.Fatalf("automatic config status=%d %s", code, output)
	}
	code, output = invoke("--no-config", "--jobs", "2", "coverage")
	if code != 0 || !strings.Contains(output, "2 file(s) passed") {
		t.Fatalf("config opt-out status=%d %s", code, output)
	}
	os.WriteFile(automatic, []byte("invalid JSON"), 0600)
	code, output = invoke()
	if code != 2 || !strings.Contains(output, "configuration error") {
		t.Fatalf("broken config status=%d %s", code, output)
	}
	for _, args := range [][]string{{"--help"}, {"--version"}, {"--no-config", "--list", "coverage"}} {
		if code, output := invoke(args...); code != 0 {
			t.Fatalf("recovery command %v status=%d %s", args, code, output)
		}
	}
}

func TestConfigurationWatchRealKinmokusei(t *testing.T) {
	runner, consumer := os.Getenv("VASEST_INTEGRATION_RUNNER"), os.Getenv("VASEST_INTEGRATION_CONSUMER")
	if runner == "" || consumer == "" {
		t.Skip("requires the external Kinmokusei project")
	}
	first, second := filepath.Join(consumer, "config_watch_first"), filepath.Join(consumer, "config_watch_second")
	for _, entry := range []struct{ directory, name string }{{first, "first"}, {second, "second"}} {
		writeWatchFile(t, filepath.Join(entry.directory, "case.test.km"), fmt.Sprintf(`import { Suite } from "vasest";
function main(): void {
  const suite = new Suite("configuration watch");
  suite.test(%q, (): void => {});
  suite.runAndExit();
}
`, entry.name))
	}
	config := filepath.Join(t.TempDir(), "settings.json")
	writeConfig := func(path, filter, reporter string, jobs int) {
		relative, err := filepath.Rel(filepath.Dir(config), path)
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(map[string]any{"version": 1, "testPath": relative, "filter": filter, "reporter": reporter, "jobs": jobs, "timeout": "5s", "repeat": jobs + 1})
		if err != nil {
			t.Fatal(err)
		}
		replaceWatchConfig(t, config, data)
	}
	writeConfig(first, "first", "json", 1)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, runner, "--watch", "--watch-interval", "20ms", "--config", config)
	command.Dir = consumer
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { command.Process.Kill(); command.Wait() }()
	type streamLine struct {
		text   string
		stderr bool
	}
	lines := make(chan streamLine, 100)
	var readers sync.WaitGroup
	for _, stream := range []struct {
		reader io.Reader
		stderr bool
	}{{stdout, false}, {stderr, true}} {
		readers.Add(1)
		go func(reader io.Reader, stderr bool) {
			defer readers.Done()
			scanner := bufio.NewScanner(reader)
			for scanner.Scan() {
				lines <- streamLine{scanner.Text(), stderr}
			}
		}(stream.reader, stream.stderr)
	}
	go func() { readers.Wait(); close(lines) }()
	stage, summaries, iterations := 0, 0, 0
	firstFiles, secondFiles := 0, 0
	var diagnostics strings.Builder
	for line := range lines {
		if line.stderr {
			diagnostics.WriteString(line.text + "\n")
			if stage == 2 && strings.Contains(line.text, "configuration error") {
				writeConfig(second, "second", "junit", 2)
				stage = 3
			}
			continue
		}
		var event struct {
			Type, Path                                string
			PassedFiles, FailedFiles, Jobs, Iteration int
			NoMatch                                   bool
		}
		if err := json.Unmarshal([]byte(line.text), &event); err != nil {
			t.Fatalf("watch changed output protocol: %s", line.text)
		}
		if event.Type == "watch" {
			iterations = event.Iteration
		}
		if event.Type == "file" {
			if strings.Contains(event.Path, "config_watch_first") {
				firstFiles++
			}
			if strings.Contains(event.Path, "config_watch_second") {
				secondFiles++
			}
		}
		if event.Type != "summary" {
			continue
		}
		summaries++
		switch stage {
		case 0:
			if event.PassedFiles != 2 || event.NoMatch || event.Jobs != 1 {
				t.Fatalf("initial configuration event=%#v", event)
			}
			writeConfig(second, "absent", "text", 2)
			stage = 1
		case 1:
			if !event.NoMatch || event.Jobs != 2 {
				t.Fatalf("edited path/filter/jobs not applied: %#v", event)
			}
			replaceWatchConfig(t, config, []byte("invalid JSON"))
			stage = 2
		case 3:
			if event.PassedFiles != 3 || event.NoMatch || event.Jobs != 2 {
				t.Fatalf("repaired configuration event=%#v", event)
			}
			if err := command.Process.Signal(syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}
			stage = 4
		default:
			t.Fatalf("unexpected watch summary stage=%d event=%#v", stage, event)
		}
	}
	if err := command.Wait(); err != nil || stage != 4 || summaries != 3 || iterations != 4 || firstFiles != 2 || secondFiles != 4 {
		t.Fatalf("configuration watch stage=%d summaries=%d iterations=%d files=%d/%d error=%v stderr=%s", stage, summaries, iterations, firstFiles, secondFiles, err, diagnostics.String())
	}
}

func TestFilePatternsMatchDepthClassesEscapesAndUnicode(t *testing.T) {
	for _, tc := range []struct {
		pattern, path string
		want          bool
	}{
		{"**/*.test.km", "root.test.km", true}, {"**/*.test.km", "a/b/c.test.km", true}, {"*.test.km", "a/b.test.km", false},
		{"unit/**", "unit/a.test.km", true}, {"unit/**", "unit/deep/a.test.km", true}, {"unit/**", "units/a.test.km", false},
		{"**/unit/**", "unit/a.test.km", true}, {"**/unit/**", "app/unit/a.test.km", true}, {"**/unit/**", "app/units/a.test.km", false},
		{"**/a/**/b.test.km", "a/b.test.km", true}, {"**/a/**/b.test.km", "x/a/y/z/b.test.km", true},
		{"**/**/**/b.test.km", "b.test.km", true}, {"**/**/b.test.km", "a/b.test.km", true},
		{"a/?[0-9].test.km", "a/x2.test.km", true}, {"a/?[0-9].test.km", "a/xz.test.km", false},
		{"[^a]*.test.km", "b.test.km", true}, {"[^a]*.test.km", "a.test.km", false},
		{"???.test.km", "金木犀.test.km", true}, {"?.test.km", "金木犀.test.km", false},
		{`literal\[x\].test.km`, "literal[x].test.km", true}, {"./**/*.test.km", "a/b.test.km", true},
		{"**", "any/deep.test.km", true}, {"a/*.test.km", "a/B.test.km", true}, {"a/b.test.km", "a/B.test.km", false},
	} {
		normalized, err := validateFilePattern(tc.pattern)
		if err != nil {
			t.Fatalf("valid pattern %q: %v", tc.pattern, err)
		}
		got, err := matchesFilePattern(normalized, tc.path)
		if err != nil || got != tc.want {
			t.Errorf("match(%q,%q)=%v,%v want %v", tc.pattern, tc.path, got, err, tc.want)
		}
	}
	for _, pattern := range []string{"", "/absolute/**", "../*.test.km", "a/../b", "a/./b", "a//b", "a/", "[", "prefix[", "a/**b", "***", "a\nb", "a\x00b", `abc\`} {
		if _, err := validateFilePattern(pattern); err == nil {
			t.Errorf("accepted invalid pattern %q", pattern)
		}
	}
	pattern := strings.Repeat("**/", 40) + "target.test.km"
	name := strings.Repeat("deep/", 80) + "other.test.km"
	got, err := matchesFilePattern(pattern, name)
	if err != nil || got {
		t.Fatalf("deep nonmatch=%v,%v", got, err)
	}
}

func TestFilePatternSelectionCombinesIncludesExcludesAndSingleFiles(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a.test.km", "unit/b.test.km", "unit/deep/c.test.km", "integration/d.test.km", "空 間/金木犀.test.km", ".hidden/e.test.km", "build/f.test.km", "helper.km"} {
		writeWatchFile(t, filepath.Join(root, filepath.FromSlash(name)), "fixture")
	}
	files, err := discover(root)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := selectTestFiles(root, files, []string{"unit/**", "空 間/**"}, []string{"**/deep/**"})
	if err != nil || len(selected) != 2 || !strings.HasSuffix(selected[0], "b.test.km") || !strings.HasSuffix(selected[1], "金木犀.test.km") {
		t.Fatalf("selection=%v error=%v", selected, err)
	}
	all, err := selectTestFiles(root, files, []string{"**"}, nil)
	if err != nil || len(all) != 5 {
		t.Fatalf("discovery policies bypassed: %v %v", all, err)
	}
	single := filepath.Join(root, "unit", "b.test.km")
	selected, err = selectTestFiles(single, []string{single}, []string{"*.test.km"}, nil)
	if err != nil || len(selected) != 1 {
		t.Fatalf("single base path: %v %v", selected, err)
	}
	selected, err = selectTestFiles(single, []string{single}, nil, []string{"b.test.km"})
	if err != nil || len(selected) != 0 {
		t.Fatalf("single exclusion: %v %v", selected, err)
	}
	if _, err := selectTestFiles(root, files, []string{"["}, nil); err == nil {
		t.Fatal("invalid include accepted")
	}
	if _, err := selectTestFiles(root, files, nil, []string{"["}); err == nil {
		t.Fatal("invalid exclude accepted")
	}
}

func TestFilePatternConfigurationCLIOverridesAndShardOrder(t *testing.T) {
	root := t.TempDir()
	useWorkingDirectory(t, root)
	// Getwd may resolve a temporary-directory alias, such as /var on macOS.
	// Compare listed paths against the same working-directory spelling.
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.test.km", "nested/b.test.km", "nested/z_invalid.test.km", "skip/c.test.km"} {
		writeWatchFile(t, filepath.Join("selected", filepath.FromSlash(name)), "fixture")
	}
	os.WriteFile("vasest.config.json", []byte(`{"version":1,"testPath":"selected","include":["nested/**"],"exclude":["**/z_*"],"jobs":2,"reporter":"json"}`), 0600)
	for _, tc := range []struct {
		args []string
		want []string
	}{
		{nil, []string{"nested/b.test.km"}},
		{[]string{"--include", "skip/**"}, []string{"skip/c.test.km"}},
		{[]string{"--include", "*.test.km", "--include", "skip/**"}, []string{"a.test.km", "skip/c.test.km"}},
		{[]string{"--exclude", "nested/b.test.km"}, []string{"nested/z_invalid.test.km"}},
		{[]string{"--no-exclude"}, []string{"nested/b.test.km", "nested/z_invalid.test.km"}},
		{[]string{"--no-include"}, []string{"a.test.km", "nested/b.test.km", "skip/c.test.km"}},
		{[]string{"--include", "absent", "--no-include"}, []string{"a.test.km", "nested/b.test.km", "skip/c.test.km"}},
		{[]string{"--no-include", "--no-exclude"}, []string{"a.test.km", "nested/b.test.km", "nested/z_invalid.test.km", "skip/c.test.km"}},
		{[]string{"--no-include", "--shard", "1/2"}, []string{"a.test.km", "skip/c.test.km"}},
		{[]string{"--no-include", "--shard", "2/2"}, []string{"nested/b.test.km"}},
		{[]string{"--no-include", "--shard", "4/4"}, nil},
	} {
		code, output := captureRunner(t, append([]string{"--list"}, tc.args...))
		if code != 0 {
			t.Fatalf("list %v status=%d %s", tc.args, code, output)
		}
		var got []string
		for _, file := range strings.Split(strings.TrimSpace(output), "\n") {
			if file != "" {
				relative, _ := filepath.Rel(filepath.Join(root, "selected"), file)
				got = append(got, filepath.ToSlash(relative))
			}
		}
		if fmt.Sprint(got) != fmt.Sprint(tc.want) {
			t.Fatalf("list %v = %v want %v", tc.args, got, tc.want)
		}
	}
	t.Setenv("VASEST_RUNNER_HELPER", "1")
	t.Setenv("KEIKA", fakeKeika(t))
	code, output := captureRunner(t, []string{"--no-include", "--shuffle", "42", "--shard", "1/2"})
	if code != 0 || !strings.Contains(output, `"passedFiles":2`) || !strings.Contains(output, `"excludedFiles":1`) || strings.Contains(output, "z_invalid.test.km") || strings.Contains(output, "b.test.km") {
		t.Fatalf("filtered shuffle shard: status=%d %s", code, output)
	}
	input, err := prepareRunnerInput([]string{"--no-config", "--include", "--config", "--exclude", "--no-config", "selected"})
	if err != nil || input.path != "selected" || len(input.args) != 5 {
		t.Fatalf("pattern argument boundaries: %#v %v", input, err)
	}
}

func TestFilePatternNoMatchesReportersArtifactsAndHistoryPreservation(t *testing.T) {
	root := t.TempDir()
	writeWatchFile(t, filepath.Join(root, "case.test.km"), "fixture")
	t.Setenv("KEIKA", filepath.Join(root, "missing-compiler"))
	history := filepath.Join(t.TempDir(), "failures.json")
	original := `{"version":1,"files":[]}`
	os.WriteFile(history, []byte(original), 0600)
	for _, reporter := range []string{"text", "json", "junit"} {
		coverage, artifacts := t.TempDir(), t.TempDir()
		code, output := captureRunner(t, []string{"--include", "absent/**", "--reporter", reporter, "--coverage", coverage, "--artifacts", artifacts, "--save-failures", history, root})
		if code != 1 {
			t.Fatalf("no matches reporter=%s status=%d %s", reporter, code, output)
		}
		if reporter == "json" && (!strings.Contains(output, `"noMatch":true`) || !strings.Contains(output, `"excludedFiles":1`)) {
			t.Fatalf("no match summary: %s", output)
		}
		if reporter == "junit" {
			var doc struct {
				XMLName  xml.Name `xml:"testsuites"`
				Failures int      `xml:"failures,attr"`
			}
			if xml.Unmarshal([]byte(output), &doc) != nil || doc.Failures != 1 || !strings.Contains(output, "&lt;files&gt;") {
				t.Fatalf("no match XML: %s", output)
			}
		}
		_, cover := readCoverageManifest(t, coverage)
		_, archive := readArtifactManifest(t, artifacts)
		if len(cover.Files) != 0 || len(archive.Files) != 0 {
			t.Fatal("excluded files were scheduled")
		}
		data, _ := os.ReadFile(history)
		if string(data) != original {
			t.Fatalf("no match replaced history: %s", data)
		}
	}
	if code, _ := captureRunner(t, []string{"--list", "--exclude", "**", root}); code != 1 {
		t.Fatalf("empty list status=%d", code)
	}
	if code, output := captureRunner(t, []string{"--include", "**/*.test.km", "--shard", "2/2", "--reporter", "json", root}); code != 0 || !strings.Contains(output, `"noMatch":false`) {
		t.Fatalf("empty assigned shard: status=%d %s", code, output)
	}
	for _, args := range [][]string{{"--include"}, {"--exclude"}, {"--include", "--watch"}, {"--exclude", "["}, {"--include", "/absolute/**"}} {
		if code, _ := captureRunner(t, args); code != 2 {
			t.Fatalf("accepted %v status=%d", args, code)
		}
	}
	config := filepath.Join(root, "settings.json")
	for _, invalid := range []string{`{"version":1,"include":"**"}`, `{"version":1,"exclude":[2]}`, `{"version":1,"include":[""]}`, `{"version":1,"exclude":["["]}`} {
		os.WriteFile(config, []byte(invalid), 0600)
		if _, err := readRunnerConfig(config); err == nil {
			t.Fatalf("invalid selection config %s", invalid)
		}
	}
	os.WriteFile(config, []byte(`{"version":1,"include":null,"exclude":[]}`), 0600)
	if _, err := readRunnerConfig(config); err != nil {
		t.Fatal(err)
	}
}

func TestFilePatternsRealKinmokusei(t *testing.T) {
	runner, consumer := os.Getenv("VASEST_INTEGRATION_RUNNER"), os.Getenv("VASEST_INTEGRATION_CONSUMER")
	if runner == "" || consumer == "" {
		t.Skip("requires the external Kinmokusei project")
	}
	invoke := func(args ...string) (int, string) {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, runner, args...)
		cmd.Dir = consumer
		output, err := cmd.CombinedOutput()
		if err == nil {
			return 0, string(output)
		}
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("file selection run: %v %s", err, output)
		}
		return exit.ExitCode(), string(output)
	}
	for _, reporter := range []string{"text", "json", "junit"} {
		t.Run(reporter, func(t *testing.T) {
			coverage, artifacts := t.TempDir(), t.TempDir()
			code, output := invoke("--include", "**/coverage/*.test.km", "--jobs", "2", "--shuffle", "42", "--reporter", reporter, "--coverage", coverage, "--artifacts", artifacts, ".")
			if code != 0 {
				t.Fatalf("selected coverage reporter=%s status=%d %s", reporter, code, output)
			}
			_, cover := readCoverageManifest(t, coverage)
			_, archive := readArtifactManifest(t, artifacts)
			if len(cover.Files) != 2 || len(archive.Files) != 2 {
				t.Fatalf("selected artifacts=%#v coverage=%#v", archive, cover)
			}
			for _, file := range cover.Files {
				assertCoverageProfile(t, file)
				if !strings.Contains(filepath.ToSlash(file.File), "coverage/") {
					t.Fatalf("excluded source compiled: %#v", file)
				}
			}
			if reporter == "json" {
				var summary struct {
					Type                       string
					Include, Exclude           []string
					ExcludedFiles, PassedFiles int
				}
				for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
					var event struct{ Type string }
					if json.Unmarshal([]byte(line), &event) != nil {
						t.Fatalf("polluted JSON: %s", output)
					}
					if event.Type == "summary" {
						json.Unmarshal([]byte(line), &summary)
					}
				}
				if summary.PassedFiles != 2 || summary.ExcludedFiles < 1 || fmt.Sprint(summary.Include) != "[**/coverage/*.test.km]" || len(summary.Exclude) != 0 {
					t.Fatalf("selection metadata=%#v", summary)
				}
			}
			if reporter == "junit" {
				var doc struct {
					XMLName  xml.Name `xml:"testsuites"`
					Tests    int      `xml:"tests,attr"`
					Failures int      `xml:"failures,attr"`
				}
				if xml.Unmarshal([]byte(output), &doc) != nil || doc.Tests != 2 || doc.Failures != 0 {
					t.Fatalf("selected XML: %s", output)
				}
			}
		})
	}
	code, output := invoke("--list", "--include", "coverage/**", "--include", "**/positive*.test.km", "--exclude", "**/negative*.test.km", ".")
	if code != 0 || len(strings.Split(strings.TrimSpace(output), "\n")) != 1 || !strings.HasSuffix(strings.TrimSpace(output), "positive.test.km") {
		t.Fatalf("overlap/exclusion list status=%d %s", code, output)
	}
	code, output = invoke("--include", "coverage/**", "--shard", "1/2", "--shuffle", "42", "--jobs", "2", "--reporter", "json", ".")
	if code != 0 || !strings.Contains(output, "negative.test.km") || strings.Contains(output, "positive.test.km") || !strings.Contains(output, `"passedFiles":1`) {
		t.Fatalf("filtered shard status=%d %s", code, output)
	}
	code, output = invoke("--include", "coverage/**", "--filter", "positive", "--jobs", "2", "--reporter", "json", ".")
	if code != 0 || !strings.Contains(output, `"noMatch":false`) || strings.Contains(output, "PASS negative") {
		t.Fatalf("file/case intersection status=%d %s", code, output)
	}
	history := filepath.Join(t.TempDir(), "failures.json")
	code, output = invoke("--include", "reporting/unchecked_failure.test.km", "--save-failures", history, "--reporter", "json", ".")
	if code != 1 {
		t.Fatalf("selected failure status=%d %s", code, output)
	}
	stored, err := readFailureHistory(history)
	if err != nil || len(stored.Files) != 1 || stored.Files[0].WholeFile {
		t.Fatalf("case history=%#v error=%v", stored, err)
	}
	code, output = invoke("--include", "reporting/**", "--exclude", "**/no_suite.test.km", "--rerun-failed", history, "--filter", "fails", "--jobs", "2", "--reporter", "json", ".")
	if code != 1 || !strings.Contains(output, `"failedFiles":1`) || strings.Contains(output, `"noMatch":true`) || strings.Contains(output, "string_diff.test.km") {
		t.Fatalf("history intersection status=%d %s", code, output)
	}
	before, _ := os.ReadFile(history)
	code, output = invoke("--include", "coverage/**", "--rerun-failed", history, "--save-failures", history, "--reporter", "json", ".")
	after, _ := os.ReadFile(history)
	if code != 1 || !strings.Contains(output, `"noMatch":true`) || string(before) != string(after) {
		t.Fatalf("excluded history status=%d %s", code, output)
	}
	code, output = invoke("--include", "**/*.test.km", "--bail", "--save-failures", history, "--reporter", "json", "rerun")
	if code != 1 || !strings.Contains(output, `"notRunFiles":1`) {
		t.Fatalf("selected bail status=%d %s", code, output)
	}
	code, output = invoke("--include", "coverage/**", "--shard", "3/3", "--reporter", "json", ".")
	if code != 0 || !strings.Contains(output, `"noMatch":false`) || !strings.Contains(output, `"passedFiles":0`) {
		t.Fatalf("empty selected shard status=%d %s", code, output)
	}
}

func TestFilePatternsWatchRealKinmokusei(t *testing.T) {
	runner, consumer := os.Getenv("VASEST_INTEGRATION_RUNNER"), os.Getenv("VASEST_INTEGRATION_CONSUMER")
	if runner == "" || consumer == "" {
		t.Skip("requires the external Kinmokusei project")
	}
	root := filepath.Join(consumer, "file_patterns_watch")
	source := `import { Suite } from "vasest";
function main(): void {
  const suite = new Suite("file selection watch");
  suite.test("passes", (): void => {});
  suite.runAndExit();
}
`
	writeWatchFile(t, filepath.Join(root, "first.test.km"), source)
	writeWatchFile(t, filepath.Join(root, "second.test.km"), source)
	config := filepath.Join(t.TempDir(), "settings.json")
	writeConfig := func(includes, excludes []string) {
		relative, _ := filepath.Rel(filepath.Dir(config), root)
		data, err := json.Marshal(map[string]any{"version": 1, "testPath": relative, "include": includes, "exclude": excludes, "reporter": "json", "jobs": 2, "timeout": "5s"})
		if err != nil {
			t.Fatal(err)
		}
		replaceWatchConfig(t, config, data)
	}
	writeConfig([]string{"first.test.km"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, runner, "--config", config, "--watch", "--watch-interval", "20ms")
	cmd.Dir = consumer
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	stage, iterations := 0, 0
	var files []string
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		var event struct {
			Type, Path                            string
			PassedFiles, ExcludedFiles, Iteration int
			NoMatch                               bool
		}
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatalf("watch output: %s", scanner.Text())
		}
		if event.Type == "watch" {
			iterations = event.Iteration
		}
		if event.Type == "file" {
			files = append(files, filepath.Base(event.Path))
		}
		if event.Type != "summary" {
			continue
		}
		stage++
		switch stage {
		case 1:
			if event.PassedFiles != 1 || event.NoMatch || event.ExcludedFiles != 1 {
				t.Fatalf("initial selection %#v", event)
			}
			writeConfig([]string{"**/*.test.km"}, []string{"first.test.km"})
		case 2:
			if event.PassedFiles != 1 || event.NoMatch || event.ExcludedFiles != 1 {
				t.Fatalf("edited selection %#v", event)
			}
			writeConfig([]string{"added*.test.km"}, nil)
		case 3:
			if !event.NoMatch || event.PassedFiles != 0 || event.ExcludedFiles != 2 {
				t.Fatalf("empty selection %#v", event)
			}
			writeWatchFile(t, filepath.Join(root, "added.test.km"), source)
		case 4:
			if event.PassedFiles != 1 || event.NoMatch || event.ExcludedFiles != 2 {
				t.Fatalf("new matching file %#v", event)
			}
			if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}
		default:
			t.Fatalf("unexpected watch run %d", stage)
		}
		if stage == 4 {
			break
		}
	}
	io.Copy(io.Discard, stdout)
	if err := cmd.Wait(); err != nil || stage != 4 || iterations != 4 || fmt.Sprint(files) != "[first.test.km second.test.km added.test.km]" {
		t.Fatalf("pattern watch stage=%d iterations=%d files=%v error=%v stderr=%s", stage, iterations, files, err, stderr.String())
	}
}

// Repetition tests count executions, not distinct source files: every iteration
// must use fresh processes and its own report/coverage/artifact destinations.
func TestRepeatRunnerReportsAndEnvironment(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.test.km", "b.test.km"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("VASEST_RUNNER_HELPER", "1")
	t.Setenv("VASEST_REPEAT_PROBE", "pass")
	t.Setenv("VASEST_REPEAT_INDEX", "999") // Parent values must be overridden.
	t.Setenv("VASEST_REPEAT_COUNT", "999")
	t.Setenv("KEIKA", fakeKeika(t))
	for _, jobs := range []string{"1", "2"} {
		for _, reporter := range []string{"text", "json", "junit"} {
			code, output := captureRunner(t, []string{"--no-config", "--repeat", "3", "--timeout", "2s", "--jobs", jobs, "--shuffle", "42", "--reporter", reporter, dir})
			if code != 0 {
				t.Fatalf("%s jobs=%s status=%d %s", reporter, jobs, code, output)
			}
			for index := 1; index <= 3; index++ {
				if strings.Count(output, fmt.Sprintf("repeat-marker:%d/3", index)) != 2 {
					t.Fatalf("lost iteration %d: %s", index, output)
				}
			}
			if reporter == "text" && (!strings.Contains(output, "REPEAT 3/3") || !strings.Contains(output, "6 file(s) passed, 0 failed, 0 not run")) {
				t.Fatal(output)
			}
			if reporter == "json" {
				var summary struct{ PassedFiles, FailedFiles, NotRunFiles, RepeatCount, RepeatsStarted int }
				var order [4][]string
				for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
					var event struct {
						Type, Path               string
						RepeatIndex, RepeatCount int
					}
					if err := json.Unmarshal([]byte(line), &event); err != nil {
						t.Fatal(err, output)
					}
					if event.Type == "summary" {
						json.Unmarshal([]byte(line), &summary)
						continue
					}
					if event.RepeatCount != 3 || event.RepeatIndex < 1 || event.RepeatIndex > 3 {
						t.Fatalf("missing iteration metadata: %s", line)
					}
					if event.Type == "file" {
						order[event.RepeatIndex] = append(order[event.RepeatIndex], event.Path)
					}
				}
				if summary.PassedFiles != 6 || summary.FailedFiles != 0 || summary.NotRunFiles != 0 || summary.RepeatCount != 3 || summary.RepeatsStarted != 3 {
					t.Fatalf("summary=%#v", summary)
				}
				if len(order[1]) != 2 || fmt.Sprint(order[1]) != fmt.Sprint(order[2]) || fmt.Sprint(order[2]) != fmt.Sprint(order[3]) {
					t.Fatalf("shuffle changed across repeats: %v", order)
				}
			}
			if reporter == "junit" {
				var doc struct {
					XMLName xml.Name
					Suites  []struct {
						Name string `xml:"name,attr"`
					} `xml:"testsuite"`
				}
				// Attribute names are lower-case in JUnit.
				var counts struct {
					Tests    int `xml:"tests,attr"`
					Failures int `xml:"failures,attr"`
				}
				if xml.Unmarshal([]byte(output), &doc) != nil || xml.Unmarshal([]byte(output), &counts) != nil || doc.XMLName.Local != "testsuites" || counts.Tests != 6 || counts.Failures != 0 || len(doc.Suites) != 6 {
					t.Fatalf("JUnit=%#v %s", doc, output)
				}
				for index, suite := range doc.Suites {
					if !strings.Contains(suite.Name, fmt.Sprintf("(repeat %d/3)", index/2+1)) {
						t.Fatalf("unlabeled repeat: %#v", doc)
					}
				}
			}
		}
	}
}

func TestRepeatStickyFailureBailAndHistory(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.test.km"), nil, 0600)
	os.WriteFile(filepath.Join(dir, "b.test.km"), nil, 0600)
	t.Setenv("VASEST_RUNNER_HELPER", "1")
	t.Setenv("VASEST_REPEAT_PROBE", "fail-first")
	t.Setenv("KEIKA", fakeKeika(t))
	for _, bail := range []bool{false, true} {
		historyPath := filepath.Join(t.TempDir(), "history.json")
		args := []string{"--no-config", "--repeat", "3", "--timeout", "2s", "--reporter", "json", "--save-failures", historyPath, dir}
		if bail {
			args = append(args, "--bail")
		}
		code, output := captureRunner(t, args)
		history, err := readFailureHistory(historyPath)
		if code != 1 || err != nil {
			t.Fatalf("status=%d error=%v %s", code, err, output)
		}
		if bail {
			if len(history.Files) != 1 || !strings.Contains(output, `"repeatsStarted":1`) || !strings.Contains(output, `"notRunFiles":5`) || strings.Contains(output, "repeat-marker:2/3") {
				t.Fatal(output, history)
			}
		} else {
			if len(history.Files) != 2 || !strings.Contains(output, `"repeatsStarted":3`) || !strings.Contains(output, `"failedFiles":2`) || !strings.Contains(output, `"passedFiles":4`) {
				t.Fatal(output, history)
			}
		}
		for _, entry := range history.Files {
			if entry.WholeFile || len(entry.Suites) != 1 || fmt.Sprint(entry.Suites[0].Cases) != "[case]" {
				t.Fatalf("failure scope lost: %#v", entry)
			}
		}
	}
	// Bail with multiple workers may finish other assigned files, but no new
	// iteration may be scheduled after their failure.
	code, output := captureRunner(t, []string{"--no-config", "--repeat", "3", "--jobs", "2", "--bail", "--reporter", "json", dir})
	if code != 1 || !strings.Contains(output, `"repeatsStarted":1`) || strings.Contains(output, "repeat-marker:2/3") {
		t.Fatal(code, output)
	}
}

func TestRepeatMergeFailureScopes(t *testing.T) {
	entries := mergeFailure(nil, FailedFile{File: "a.test.km", Suites: []FailedSuite{{Name: "suite", Cases: []string{"first"}}}})
	entries = mergeFailure(entries, FailedFile{File: "a.test.km", Suites: []FailedSuite{{Name: "suite", Cases: []string{"first", "second"}}, {Name: "other", Cases: []string{"third"}}}})
	entries = mergeFailure(entries, FailedFile{File: "b.test.km", WholeFile: true})
	if len(entries) != 2 || fmt.Sprint(entries[0].Suites[0].Cases) != "[first second]" || len(entries[0].Suites) != 2 {
		t.Fatalf("union=%#v", entries)
	}
	entries = mergeFailure(entries, FailedFile{File: "a.test.km", Suites: []FailedSuite{{Name: "suite", WholeSuite: true}}})
	entries = mergeFailure(entries, FailedFile{File: "a.test.km", Suites: []FailedSuite{{Name: "suite", Cases: []string{"ignored"}}}})
	if !entries[0].Suites[0].WholeSuite || len(entries[0].Suites[0].Cases) != 0 {
		t.Fatalf("suite dominance=%#v", entries)
	}
	entries = mergeFailure(entries, FailedFile{File: "a.test.km", WholeFile: true})
	entries = mergeFailure(entries, FailedFile{File: "a.test.km", Suites: []FailedSuite{{Name: "ignored", WholeSuite: true}}})
	if !entries[0].WholeFile || len(entries[0].Suites) != 0 {
		t.Fatalf("file dominance=%#v", entries)
	}
	path := filepath.Join(t.TempDir(), "history.json")
	if err := saveFailureHistory(path, entries); err != nil {
		t.Fatal(err)
	}
	if _, err := readFailureHistory(path); err != nil {
		t.Fatalf("union cannot be rerun: %v", err)
	}
}

func TestRepeatNoMatchIsPerIterationAndPreservesHistory(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.test.km"), nil, 0600)
	t.Setenv("VASEST_RUNNER_HELPER", "1")
	t.Setenv("VASEST_REPEAT_PROBE", "pass")
	t.Setenv("VASEST_REPEAT_MATCH_FIRST", "1")
	t.Setenv("KEIKA", fakeKeika(t))
	historyPath := filepath.Join(t.TempDir(), "history.json")
	original := `{"version":1,"files":[]}`
	os.WriteFile(historyPath, []byte(original), 0600)
	code, output := captureRunner(t, []string{"--no-config", "--repeat", "3", "--timeout", "2s", "--filter", "case", "--save-failures", historyPath, "--reporter", "json", dir})
	data, _ := os.ReadFile(historyPath)
	if code != 1 || !strings.Contains(output, `"noMatch":true`) || !strings.Contains(output, `"repeatsStarted":2`) || !strings.Contains(output, `"notRunFiles":1`) || strings.Contains(output, "repeat-marker:3/3") || string(data) != original {
		t.Fatal(code, output, string(data))
	}
	for _, args := range [][]string{
		{"--exclude", "**"}, {"--rerun-failed", historyPath}, {"--shard", "2/2"},
	} {
		args = append(args, "--no-config", "--repeat", "1000000", "--reporter", "json", dir)
		code, output = captureRunner(t, args)
		want := 0
		if args[0] == "--exclude" {
			want = 1
		}
		if code != want || !strings.Contains(output, `"repeatsStarted":1`) || strings.Contains(output, "repeat-marker:") {
			t.Fatal(args, code, output)
		}
	}
}

func TestRepeatConfigurationAndValidation(t *testing.T) {
	dir := t.TempDir()
	useWorkingDirectory(t, dir)
	os.Mkdir("tests", 0700)
	os.WriteFile("tests/a.test.km", nil, 0600)
	t.Setenv("VASEST_RUNNER_HELPER", "1")
	t.Setenv("VASEST_REPEAT_PROBE", "pass")
	t.Setenv("KEIKA", fakeKeika(t))
	for _, invalid := range []string{`0`, `-1`, `1.5`, `"2"`, `true`} {
		os.WriteFile("vasest.config.json", []byte(`{"version":1,"repeat":`+invalid+`}`), 0600)
		if code, _ := captureRunner(t, []string{"--repeat", "1"}); code != 2 {
			t.Fatalf("overridden invalid config accepted: %s", invalid)
		}
	}
	os.WriteFile("vasest.config.json", []byte(`{"version":1,"repeat":2,"jobs":2,"reporter":"json"}`), 0600)
	if code, output := captureRunner(t, nil); code != 0 || !strings.Contains(output, `"passedFiles":2`) || !strings.Contains(output, "repeat-marker:2/2") {
		t.Fatal(code, output)
	}
	if code, output := captureRunner(t, []string{"--repeat", "1"}); code != 0 || strings.Contains(output, `"type":"repeat"`) || !strings.Contains(output, `"passedFiles":1`) || !strings.Contains(output, "repeat-marker:1/1") {
		t.Fatal(code, output)
	}
	if code, output := captureRunner(t, []string{"--list"}); code != 0 || strings.TrimSpace(output) != "tests/a.test.km" {
		t.Fatal(code, output)
	}
	for _, args := range [][]string{{"--repeat"}, {"--repeat", "0"}, {"--repeat", "-1"}, {"--repeat", "x"}, {"--repeat", "1.5"}, {"--repeat", "--watch"}, {"--repeat", "--help"}, {"--repeat", "999999999999999999999999"}, {"--list", "--repeat", "1"}} {
		if code, _ := captureRunner(t, args); code != 2 {
			t.Fatalf("accepted %v status=%d", args, code)
		}
	}
	// Reject overflow before spawning processes, including when bail would stop early.
	os.WriteFile("tests/b.test.km", nil, 0600)
	if code, output := captureRunner(t, []string{"--repeat", "9223372036854775807", "--bail"}); code != 2 || !strings.Contains(output, "too many file executions") {
		t.Fatal(code, output)
	}
	os.WriteFile("vasest.config.json", []byte(`{"version":1,"repeat":null}`), 0600)
	if _, err := readRunnerConfig("vasest.config.json"); err != nil {
		t.Fatal(err)
	}
}

func TestRepeatTimeoutMalformedAndInfrastructureFailure(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("VASEST_RUNNER_HELPER", "1")
	t.Setenv("KEIKA", fakeKeika(t))
	for _, name := range []string{"slow", "invalid"} {
		input := filepath.Join(dir, name+".test.km")
		os.WriteFile(input, nil, 0600)
		historyPath := filepath.Join(t.TempDir(), "history.json")
		code, output := captureRunner(t, []string{"--no-config", "--repeat", "2", "--timeout", "100ms", "--reporter", "json", "--save-failures", historyPath, input})
		history, err := readFailureHistory(historyPath)
		if code != 1 || !strings.Contains(output, `"failedFiles":2`) || !strings.Contains(output, `"repeatsStarted":2`) || err != nil || len(history.Files) != 1 || !history.Files[0].WholeFile {
			t.Fatal(code, output, history, err)
		}
	}
	input := filepath.Join(dir, "pass.test.km")
	os.WriteFile(input, nil, 0600)
	// An otherwise passing executable without coverage counters is a runner
	// failure; remaining repetitions must not be launched.
	coverage := t.TempDir()
	t.Setenv("VASEST_DISABLE_COVERAGE", "1")
	code, output := captureRunner(t, []string{"--no-config", "--repeat", "3", "--coverage", coverage, "--reporter", "json", input})
	if code != 2 || !strings.Contains(output, `"repeatsStarted":1`) || !strings.Contains(output, `"notRunFiles":2`) {
		t.Fatal(code, output)
	}
	_, manifest := readCoverageManifest(t, coverage)
	if len(manifest.Files) != 1 || manifest.Files[0].Available {
		t.Fatal(manifest)
	}
}

func TestRepeatRealKinmokusei(t *testing.T) {
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
			t.Fatalf("repeat integration: %v %s", err, output)
		}
		return exit.ExitCode(), string(output)
	}
	for _, reporter := range []string{"text", "json", "junit"} {
		historyPath := filepath.Join(t.TempDir(), "failures.json")
		code, output := invoke("--repeat", "3", "--jobs", "2", "--shuffle", "42", "--save-failures", historyPath, "--reporter", reporter, "repeat")
		history, err := readFailureHistory(historyPath)
		if code != 1 || err != nil || len(history.Files) != 1 || history.Files[0].WholeFile || len(history.Files[0].Suites) != 1 || fmt.Sprint(history.Files[0].Suites[0].Cases) != "[fails first fails second]" {
			t.Fatal(reporter, code, output, history, err)
		}
		for index := 1; index <= 3; index++ {
			if !strings.Contains(output, fmt.Sprintf("repeat-marker:%d/3 visits=3", index)) {
				t.Fatalf("state/process/report lost: %s", output)
			}
		}
		if reporter == "junit" {
			var doc struct {
				Tests    int `xml:"tests,attr"`
				Failures int `xml:"failures,attr"`
				Suites   []struct {
					Name string `xml:"name,attr"`
				} `xml:"testsuite"`
			}
			if xml.Unmarshal([]byte(output), &doc) != nil || doc.Tests != 9 || doc.Failures != 2 || len(doc.Suites) != 3 {
				t.Fatalf("repeat JUnit: %s", output)
			}
		}
		if reporter == "json" {
			code, output = invoke("--repeat", "3", "--rerun-failed", historyPath, "--save-failures", historyPath, "--reporter", "json", "repeat")
			if code != 1 || strings.Contains(output, `"name":"stable"`) || !strings.Contains(output, "repeat-marker:3/3 visits=2") {
				t.Fatalf("exact repeated rerun: %d %s", code, output)
			}
			history, err = readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 1 || fmt.Sprint(history.Files[0].Suites[0].Cases) != "[fails first fails second]" {
				t.Fatal(history, err)
			}
		}
	}
	// Every repeated executable retains distinct counters, source, raw reports,
	// output and HTML. Include, filter and shard selection stay fixed per batch.
	for _, reporter := range []string{"text", "json", "junit"} {
		coverage, artifacts := t.TempDir(), t.TempDir()
		code, output := invoke("--repeat", "2", "--include", "**/repeat/*.test.km", "--filter", "stable", "--shard", "1/1", "--jobs", "2", "--timeout", "5s", "--coverage", coverage, "--artifacts", artifacts, "--reporter", reporter, ".")
		if code != 0 {
			t.Fatal(reporter, code, output)
		}
		coverRuns, err := os.ReadDir(coverage)
		if err != nil || len(coverRuns) != 2 {
			t.Fatal(coverRuns, err)
		}
		for _, run := range coverRuns {
			data, err := os.ReadFile(filepath.Join(coverage, run.Name(), "manifest.json"))
			var manifest CoverageManifest
			if err != nil || json.Unmarshal(data, &manifest) != nil || len(manifest.Files) != 1 {
				t.Fatal(err, string(data))
			}
			assertCoverageProfile(t, manifest.Files[0])
			if manifest.RepeatCount != 2 || manifest.RepeatIndex < 1 || manifest.RepeatIndex > 2 {
				t.Fatal(manifest)
			}
		}
		archiveRuns, err := os.ReadDir(artifacts)
		if err != nil || len(archiveRuns) != 2 {
			t.Fatal(archiveRuns, err)
		}
		seen := map[string]bool{}
		for _, run := range archiveRuns {
			data, err := os.ReadFile(filepath.Join(artifacts, run.Name(), "manifest.json"))
			var manifest ArtifactManifest
			if err != nil || json.Unmarshal(data, &manifest) != nil || len(manifest.Files) != 1 {
				t.Fatal(err, string(data))
			}
			if manifest.RepeatCount != 2 || manifest.RepeatIndex < 1 || manifest.RepeatIndex > 2 {
				t.Fatal(manifest)
			}
			entry := manifest.Files[0]
			assertSourceArchive(t, entry)
			if entry.Html == "" || entry.Profile == "" || manifest.Coverage == "" {
				t.Fatal(manifest)
			}
			retained, err := os.ReadFile(entry.Output)
			if err != nil {
				t.Fatal(err)
			}
			for _, index := range []string{"1", "2"} {
				if strings.Contains(string(retained), "repeat-marker:"+index+"/2 visits=1") {
					if fmt.Sprint(manifest.RepeatIndex) != index {
						t.Fatal("manifest/output iteration differs", manifest)
					}
					seen[index] = true
				}
			}
		}
		if len(seen) != 2 {
			t.Fatalf("iteration output overwritten: %v", seen)
		}
		if reporter == "json" {
			for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
				var event struct {
					Type                     string
					RepeatIndex, RepeatCount int
				}
				if json.Unmarshal([]byte(line), &event) != nil {
					t.Fatal(output)
				}
				if event.Type == "coverage" || event.Type == "artifacts" {
					if event.RepeatIndex < 1 || event.RepeatIndex > 2 || event.RepeatCount != 2 {
						t.Fatal(line)
					}
				}
			}
		}
	}
}

func TestErrorAssertionsRealKinmokusei(t *testing.T) {
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
			t.Fatalf("error assertion integration: %v %s", err, output)
		}
		return exit.ExitCode(), string(output)
	}
	for _, reporter := range []string{"text", "json", "junit"} {
		t.Run(reporter, func(t *testing.T) {
			historyPath := filepath.Join(t.TempDir(), "failures.json")
			code, output := invoke("--jobs", "2", "--save-failures", historyPath, "--reporter", reporter, "error_assertions")
			if code != 1 || !strings.Contains(output, "error-cleanup") || !strings.Contains(output, "error-later-case") {
				t.Fatal(code, output)
			}
			history, err := readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 1 || history.Files[0].WholeFile || len(history.Files[0].Suites) != 1 || fmt.Sprint(history.Files[0].Suites[0].Cases) != "[wrong cause wrong type invalid target nil target]" {
				t.Fatal(history, err, output)
			}
			messages := []string{"(errors.Is)", "(errors.As)", "target must point to an error type or interface", "target must not be nil"}
			for _, message := range messages {
				if !strings.Contains(output, message) {
					t.Fatalf("lost diagnostic %q: %s", message, output)
				}
			}
			if reporter == "json" {
				var summary struct{ PassedFiles, FailedFiles int }
				cases := 0
				for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
					var event struct {
						Type   string
						Report SuiteReport
					}
					if json.Unmarshal([]byte(line), &event) != nil {
						t.Fatal("polluted JSON", output)
					}
					if event.Type == "summary" {
						json.Unmarshal([]byte(line), &summary)
					}
					if event.Type != "suite" || event.Report.Success {
						continue
					}
					for index, result := range event.Report.Summary.Results {
						if result.Status != "fail" {
							continue
						}
						cases++
						if len(result.Failures) != 1 || result.Failures[0].Phase != "body" || result.Failures[0].Location.Source != "explicit" || result.Failures[0].Location.File != "error-checks.test.km" || result.Failures[0].Location.Line != (index+1)*10 {
							t.Fatalf("metadata lost: %#v", result)
						}
					}
				}
				if summary.PassedFiles != 1 || summary.FailedFiles != 1 || cases != 4 {
					t.Fatal(summary, cases, output)
				}
			} else if reporter == "junit" {
				var doc struct {
					Tests    int `xml:"tests,attr"`
					Failures int `xml:"failures,attr"`
				}
				if xml.Unmarshal([]byte(output), &doc) != nil || doc.Tests != 11 || doc.Failures != 4 {
					t.Fatal(output)
				}
				for _, line := range []string{"10", "20", "30", "40"} {
					if !strings.Contains(output, "error-checks.test.km:"+line+" (explicit)") {
						t.Fatal(output)
					}
				}
			} else if !strings.Contains(output, "6 passed, 0 failed") || !strings.Contains(output, "1 passed, 4 failed") {
				t.Fatal(output)
			}
			// Failed assertions select exact cases even when the file exits via
			// runAndExit; filtering the history must exclude all unrelated bodies.
			code, output = invoke("--rerun-failed", historyPath, "--filter", "wrong type", "--reporter", reporter, "error_assertions")
			if code != 1 || strings.Contains(output, "error-cleanup") || strings.Contains(output, "error-later-case") || !strings.Contains(output, "(errors.As)") {
				t.Fatal(code, output)
			}
		})
	}
	code, output := invoke("--bail", "error_assertions/failures.test.km")
	if code != 1 || !strings.Contains(output, "error-cleanup") || strings.Contains(output, "error-later-case") || !strings.Contains(output, "0 passed, 1 failed, 4 skipped") || strings.Count(output, "error-afterEach") != 1 {
		t.Fatal(code, output)
	}
}

func TestRuntimeSkipReportsRetainReasonsAndMeasuredDuration(t *testing.T) {
	for _, tc := range []struct {
		status, message string
		duration        int64
		valid           bool
	}{
		{"skip", "dependency unavailable", 12345, true}, {"skip", "", 0, true},
		{"skip", "", 12345, false}, {"skip", "  ", 12345, false},
		{"skip", "dependency unavailable", -1, false}, {"todo", "planned", 1, false},
	} {
		skipped, todo := 1, 0
		if tc.status == "todo" {
			skipped, todo = 0, 1
		}
		line := fmt.Sprintf(`{"success":true,"summary":{"suite":"runtime skip","passed":0,"failed":0,"skipped":%d,"todo":%d,"results":[{"name":"conditional","passed":false,"status":%q,"message":%q,"durationNs":%d}]}}`, skipped, todo, tc.status, tc.message, tc.duration)
		report, err := readSuiteReport(line)
		if (err == nil) != tc.valid {
			t.Fatalf("report %s error=%v", line, err)
		}
		if tc.valid && (report.Summary.Results[0].Message != tc.message || report.Summary.Results[0].DurationNs != tc.duration) {
			t.Fatal(report)
		}
	}
}

func TestMockErrorMatchersRealKinmokusei(t *testing.T) {
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
			t.Fatalf("mock error matcher integration: %v %s", err, output)
		}
		return exit.ExitCode(), string(output)
	}
	for _, reporter := range []string{"text", "json", "junit"} {
		t.Run(reporter, func(t *testing.T) {
			code, output := invoke("--reporter", reporter, "mock_error_matchers/passing.test.km")
			if code != 0 || !strings.Contains(output, "mock error chain matchers") {
				t.Fatal(code, output)
			}
			historyPath := filepath.Join(t.TempDir(), "history.json")
			code, output = invoke("--reporter", reporter, "--save-failures", historyPath, "mock_error_matchers/failures.test.km")
			if code != 1 || !strings.Contains(output, "mock-error-afterEach") || !strings.Contains(output, "mock-error-cleanup") || !strings.Contains(output, "mock-error-later") || !strings.Contains(output, "mock outcome source must not be nil") {
				t.Fatal(code, output)
			}
			history, err := readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 1 || history.Files[0].WholeFile || len(history.Files[0].Suites) != 1 || history.Files[0].Suites[0].WholeSuite || len(history.Files[0].Suites[0].Cases) != 20 {
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
						if len(result.Failures) != 1 || result.Failures[0].Phase != "body" || !strings.HasSuffix(filepath.ToSlash(result.Failures[0].Location.File), "/generated.go") || result.Failures[0].Location.Line <= 0 || result.Failures[0].Location.Source != "go" {
							t.Fatal(result)
						}
					}
				}
				if failed != 20 {
					t.Fatal(failed, output)
				}
			} else if reporter == "junit" {
				var doc struct {
					Tests    int `xml:"tests,attr"`
					Failures int `xml:"failures,attr"`
				}
				if xml.Unmarshal([]byte(output), &doc) != nil || doc.Tests != 21 || doc.Failures != 20 {
					t.Fatal(output)
				}
			} else if !strings.Contains(output, "1 passed, 20 failed, 0 skipped") {
				t.Fatal(output)
			}
			code, output = invoke("--reporter", reporter, "--rerun-failed", historyPath, "--save-failures", historyPath, "--filter", "negated last type", "mock_error_matchers")
			if code != 1 || !strings.Contains(output, "negated last type") || strings.Contains(output, "mock-error-cleanup") || strings.Contains(output, "mock-error-later") {
				t.Fatal(code, output)
			}
			history, err = readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 1 || len(history.Files[0].Suites) != 1 || fmt.Sprint(history.Files[0].Suites[0].Cases) != "[negated last type]" {
				t.Fatal(history, err)
			}
		})
	}
	code, output := invoke("--bail", "mock_error_matchers/failures.test.km")
	if code != 1 || !strings.Contains(output, "mock-error-cleanup") || strings.Contains(output, "mock-error-later") || !strings.Contains(output, "0 passed, 1 failed, 20 skipped") {
		t.Fatal(code, output)
	}
	for _, fixture := range []struct{ file, marker string }{{"panic", "source"}, {"is_panic", "is"}} {
		historyPath := filepath.Join(t.TempDir(), "panic.json")
		code, output := invoke("--save-failures", historyPath, "mock_error_matchers/"+fixture.file+".test.km")
		if code != 1 || !strings.Contains(output, "reflect: call of reflect.Value.Index on int Value") || strings.Contains(output, "unreachable-") {
			t.Fatal(code, output)
		}
		last := -1
		for _, phase := range []string{"afterEach", "cleanup", "afterAll"} {
			index := strings.Index(output, "mock-error-"+fixture.marker+"-"+phase)
			if index <= last {
				t.Fatal("native mock error matcher panic cleanup order changed", output)
			}
			last = index
		}
		history, err := readFailureHistory(historyPath)
		if err != nil || len(history.Files) != 1 || !history.Files[0].WholeFile {
			t.Fatal(history, err)
		}
	}
	keika := os.Getenv("KEIKA")
	if keika == "" {
		keika = "keika"
	}
	for _, file := range []string{"index", "cause", "destination", "explicit"} {
		t.Run("compile rejection/"+file, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, keika, "check", "mock_error_matchers/type_errors/"+file+".km")
			command.Dir = consumer
			output, err := command.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || !strings.Contains(string(output), "cannot use") {
				t.Fatalf("compiler did not reject mismatched mock error matcher types: %v %s", err, output)
			}
		})
	}
}

func TestMockOutcomeMatchersRealKinmokusei(t *testing.T) {
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
			t.Fatalf("mock outcome matcher integration: %v %s", err, output)
		}
		return exit.ExitCode(), string(output)
	}
	for _, reporter := range []string{"text", "json", "junit"} {
		t.Run(reporter, func(t *testing.T) {
			code, output := invoke("--reporter", reporter, "mock_outcome_matchers/passing.test.km")
			if code != 0 || !strings.Contains(output, "shared mock outcome matchers") {
				t.Fatal(code, output)
			}
			historyPath := filepath.Join(t.TempDir(), "history.json")
			code, output = invoke("--reporter", reporter, "--save-failures", historyPath, "mock_outcome_matchers/failures.test.km")
			if code != 1 || !strings.Contains(output, "mock-outcome-afterEach") || !strings.Contains(output, "mock-outcome-cleanup") || !strings.Contains(output, "mock-outcome-later") || !strings.Contains(output, "mock outcome source must not be nil") {
				t.Fatal(code, output)
			}
			history, err := readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 1 || history.Files[0].WholeFile || len(history.Files[0].Suites) != 1 || history.Files[0].Suites[0].WholeSuite || len(history.Files[0].Suites[0].Cases) != 24 {
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
						if len(result.Failures) != 1 || result.Failures[0].Phase != "body" || !strings.HasSuffix(filepath.ToSlash(result.Failures[0].Location.File), "/generated.go") || result.Failures[0].Location.Line <= 0 || result.Failures[0].Location.Source != "go" {
							t.Fatal(result)
						}
					}
				}
				if failed != 24 {
					t.Fatal(failed, output)
				}
			} else if reporter == "junit" {
				var doc struct {
					Tests    int `xml:"tests,attr"`
					Failures int `xml:"failures,attr"`
				}
				if xml.Unmarshal([]byte(output), &doc) != nil || doc.Tests != 25 || doc.Failures != 24 {
					t.Fatal(output)
				}
			} else if !strings.Contains(output, "1 passed, 24 failed, 0 skipped") {
				t.Fatal(output)
			}
			code, output = invoke("--reporter", reporter, "--rerun-failed", historyPath, "--save-failures", historyPath, "--filter", "negated last message", "mock_outcome_matchers")
			if code != 1 || !strings.Contains(output, "negated last message") || strings.Contains(output, "mock-outcome-cleanup") || strings.Contains(output, "mock-outcome-later") {
				t.Fatal(code, output)
			}
			history, err = readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 1 || len(history.Files[0].Suites) != 1 || fmt.Sprint(history.Files[0].Suites[0].Cases) != "[negated last message]" {
				t.Fatal(history, err)
			}
		})
	}
	code, output := invoke("--bail", "mock_outcome_matchers/failures.test.km")
	if code != 1 || !strings.Contains(output, "mock-outcome-cleanup") || strings.Contains(output, "mock-outcome-later") || !strings.Contains(output, "0 passed, 1 failed, 24 skipped") {
		t.Fatal(code, output)
	}
	for _, fixture := range []struct{ file, marker string }{{"panic", "source"}, {"message_panic", "error"}} {
		historyPath := filepath.Join(t.TempDir(), "panic.json")
		code, output := invoke("--save-failures", historyPath, "mock_outcome_matchers/"+fixture.file+".test.km")
		if code != 1 || !strings.Contains(output, "reflect: call of reflect.Value.Index on int Value") || strings.Contains(output, "unreachable-") {
			t.Fatal(code, output)
		}
		last := -1
		for _, phase := range []string{"afterEach", "cleanup", "afterAll"} {
			index := strings.Index(output, "mock-outcome-"+fixture.marker+"-"+phase)
			if index <= last {
				t.Fatal("native mock outcome panic cleanup order changed", output)
			}
			last = index
		}
		history, err := readFailureHistory(historyPath)
		if err != nil || len(history.Files) != 1 || !history.Files[0].WholeFile {
			t.Fatal(history, err)
		}
	}
	keika := os.Getenv("KEIKA")
	if keika == "" {
		keika = "keika"
	}
	for _, file := range []string{"count", "throw_count", "index", "fragment", "source", "constructor", "exception"} {
		t.Run("compile rejection/"+file, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, keika, "check", "mock_outcome_matchers/type_errors/"+file+".km")
			command.Dir = consumer
			output, err := command.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || !strings.Contains(string(output), "cannot use") {
				t.Fatalf("compiler did not reject mismatched mock outcome types: %v %s", err, output)
			}
		})
	}
}

func TestMockReturnMatchersRealKinmokusei(t *testing.T) {
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
			t.Fatalf("mock return matcher integration: %v %s", err, output)
		}
		return exit.ExitCode(), string(output)
	}
	for _, reporter := range []string{"text", "json", "junit"} {
		t.Run(reporter, func(t *testing.T) {
			code, output := invoke("--reporter", reporter, "mock_return_matchers/passing.test.km")
			if code != 0 || !strings.Contains(output, "typed mock return matchers") {
				t.Fatal(code, output)
			}
			historyPath := filepath.Join(t.TempDir(), "history.json")
			code, output = invoke("--reporter", reporter, "--save-failures", historyPath, "mock_return_matchers/failures.test.km")
			if code != 1 || !strings.Contains(output, "mock-return-afterEach") || !strings.Contains(output, "mock-return-cleanup") || !strings.Contains(output, "mock-return-later") || !strings.Contains(output, "mock return source must not be nil") {
				t.Fatal(code, output)
			}
			history, err := readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 1 || history.Files[0].WholeFile || len(history.Files[0].Suites) != 1 || history.Files[0].Suites[0].WholeSuite || len(history.Files[0].Suites[0].Cases) != 17 {
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
						if result.Name == "last invocation threw" && (len(result.Failures) != 1 || !strings.Contains(result.Failures[0].Message, `got status "throw"`)) {
							t.Fatal("non-return status diagnostic changed", result)
						}
						if len(result.Failures) != 1 || result.Failures[0].Phase != "body" || !strings.HasSuffix(filepath.ToSlash(result.Failures[0].Location.File), "/generated.go") || result.Failures[0].Location.Line <= 0 || result.Failures[0].Location.Source != "go" {
							t.Fatal(result)
						}
					}
				}
				if failed != 17 {
					t.Fatal(failed, output)
				}
			} else if reporter == "junit" {
				var doc struct {
					Tests    int `xml:"tests,attr"`
					Failures int `xml:"failures,attr"`
				}
				if xml.Unmarshal([]byte(output), &doc) != nil || doc.Tests != 18 || doc.Failures != 17 {
					t.Fatal(output)
				}
			} else if !strings.Contains(output, "1 passed, 17 failed, 0 skipped") {
				t.Fatal(output)
			}
			code, output = invoke("--reporter", reporter, "--rerun-failed", historyPath, "--save-failures", historyPath, "--filter", "negated last value", "mock_return_matchers")
			if code != 1 || !strings.Contains(output, "negated last value") || strings.Contains(output, "mock-return-cleanup") || strings.Contains(output, "mock-return-later") {
				t.Fatal(code, output)
			}
			history, err = readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 1 || len(history.Files[0].Suites) != 1 || fmt.Sprint(history.Files[0].Suites[0].Cases) != "[negated last value]" {
				t.Fatal(history, err)
			}
		})
	}
	code, output := invoke("--bail", "mock_return_matchers/failures.test.km")
	if code != 1 || !strings.Contains(output, "mock-return-cleanup") || strings.Contains(output, "mock-return-later") || !strings.Contains(output, "0 passed, 1 failed, 17 skipped") {
		t.Fatal(code, output)
	}
	historyPath := filepath.Join(t.TempDir(), "panic.json")
	code, output = invoke("--save-failures", historyPath, "mock_return_matchers/panic.test.km")
	if code != 1 || !strings.Contains(output, "reflect: call of reflect.Value.Index on int Value") || strings.Contains(output, "unreachable-") {
		t.Fatal(code, output)
	}
	last := -1
	for _, marker := range []string{"mock-return-source-afterEach", "mock-return-source-cleanup", "mock-return-source-afterAll"} {
		index := strings.Index(output, marker)
		if index <= last {
			t.Fatal("native mock return source panic cleanup order changed", output)
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
	for _, file := range []string{"value", "nominal", "width", "count", "index", "source", "constructor"} {
		t.Run("compile rejection/"+file, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, keika, "check", "mock_return_matchers/type_errors/"+file+".km")
			command.Dir = consumer
			output, err := command.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || !strings.Contains(string(output), "cannot use") {
				t.Fatalf("compiler did not reject mismatched mock return types: %v %s", err, output)
			}
		})
	}
}

func TestMockMatchersRealKinmokusei(t *testing.T) {
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
			t.Fatalf("mock matcher integration: %v %s", err, output)
		}
		return exit.ExitCode(), string(output)
	}
	for _, reporter := range []string{"text", "json", "junit"} {
		t.Run(reporter, func(t *testing.T) {
			code, output := invoke("--reporter", reporter, "mock_matchers/passing.test.km")
			if code != 0 || !strings.Contains(output, "typed mock call matchers") {
				t.Fatal(code, output)
			}
			historyPath := filepath.Join(t.TempDir(), "history.json")
			code, output = invoke("--reporter", reporter, "--save-failures", historyPath, "mock_matchers/failures.test.km")
			if code != 1 || !strings.Contains(output, "mock-matcher-afterEach") || !strings.Contains(output, "mock-matcher-cleanup") || !strings.Contains(output, "mock-matcher-later") || !strings.Contains(output, "mock call source must not be nil") {
				t.Fatal(code, output)
			}
			history, err := readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 1 || history.Files[0].WholeFile || len(history.Files[0].Suites) != 1 || history.Files[0].Suites[0].WholeSuite || len(history.Files[0].Suites[0].Cases) != 16 {
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
						if len(result.Failures) != 1 || result.Failures[0].Phase != "body" || !strings.HasSuffix(filepath.ToSlash(result.Failures[0].Location.File), "/generated.go") || result.Failures[0].Location.Line <= 0 || result.Failures[0].Location.Source != "go" {
							t.Fatal(result)
						}
					}
				}
				if failed != 16 {
					t.Fatal(failed, output)
				}
			} else if reporter == "junit" {
				var doc struct {
					Tests    int `xml:"tests,attr"`
					Failures int `xml:"failures,attr"`
				}
				if xml.Unmarshal([]byte(output), &doc) != nil || doc.Tests != 17 || doc.Failures != 16 {
					t.Fatal(output)
				}
			} else if !strings.Contains(output, "1 passed, 16 failed, 0 skipped") {
				t.Fatal(output)
			}
			code, output = invoke("--reporter", reporter, "--rerun-failed", historyPath, "--save-failures", historyPath, "--filter", "negated last argument", "mock_matchers")
			if code != 1 || !strings.Contains(output, "negated last argument") || strings.Contains(output, "mock-matcher-cleanup") || strings.Contains(output, "mock-matcher-later") {
				t.Fatal(code, output)
			}
			history, err = readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 1 || len(history.Files[0].Suites) != 1 || fmt.Sprint(history.Files[0].Suites[0].Cases) != "[negated last argument]" {
				t.Fatal(history, err)
			}
		})
	}
	code, output := invoke("--bail", "mock_matchers/failures.test.km")
	if code != 1 || !strings.Contains(output, "mock-matcher-cleanup") || strings.Contains(output, "mock-matcher-later") || !strings.Contains(output, "0 passed, 1 failed, 16 skipped") {
		t.Fatal(code, output)
	}
	historyPath := filepath.Join(t.TempDir(), "panic.json")
	code, output = invoke("--save-failures", historyPath, "mock_matchers/panic.test.km")
	if code != 1 || !strings.Contains(output, "reflect: call of reflect.Value.Index on int Value") || strings.Contains(output, "unreachable-") {
		t.Fatal(code, output)
	}
	last := -1
	for _, marker := range []string{"mock-source-afterEach", "mock-source-cleanup", "mock-source-afterAll"} {
		index := strings.Index(output, marker)
		if index <= last {
			t.Fatal("native mock source panic cleanup order changed", output)
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
	for _, file := range []string{"argument", "nominal", "width", "count", "index", "source", "constructor"} {
		t.Run("compile rejection/"+file, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, keika, "check", "mock_matchers/type_errors/"+file+".km")
			command.Dir = consumer
			output, err := command.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || !strings.Contains(string(output), "cannot use") {
				t.Fatalf("compiler did not reject mismatched mock types: %v %s", err, output)
			}
		})
	}
}

func TestFloatMatchersRealKinmokusei(t *testing.T) {
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
			t.Fatalf("float matcher integration: %v %s", err, output)
		}
		return exit.ExitCode(), string(output)
	}
	for _, reporter := range []string{"text", "json", "junit"} {
		t.Run(reporter, func(t *testing.T) {
			code, output := invoke("--reporter", reporter, "float_matchers/passing.test.km")
			if code != 0 || !strings.Contains(output, "typed float matchers") || !strings.Contains(output, "float-passing-later") || !strings.Contains(output, "6 passed, 0 failed, 0 skipped, 0 todo") {
				t.Fatal(code, output)
			}
			historyPath := filepath.Join(t.TempDir(), "history.json")
			code, output = invoke("--reporter", reporter, "--save-failures", historyPath, "float_matchers/failures.test.km")
			if code != 1 || !strings.Contains(output, "float-afterEach") || !strings.Contains(output, "float-cleanup") || !strings.Contains(output, "float-failing-later") || !strings.Contains(output, "not to be finite") {
				t.Fatal(code, output)
			}
			history, err := readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 1 || history.Files[0].WholeFile || len(history.Files[0].Suites) != 1 || history.Files[0].Suites[0].WholeSuite || len(history.Files[0].Suites[0].Cases) != 18 {
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
						if len(result.Failures) != 1 || result.Failures[0].Phase != "body" || !strings.HasSuffix(filepath.ToSlash(result.Failures[0].Location.File), "/generated.go") || result.Failures[0].Location.Line <= 0 || result.Failures[0].Location.Source != "go" {
							t.Fatal(result)
						}
					}
				}
				if failed != 18 {
					t.Fatal(failed, output)
				}
			} else if reporter == "junit" {
				var doc struct {
					Tests    int `xml:"tests,attr"`
					Failures int `xml:"failures,attr"`
				}
				if xml.Unmarshal([]byte(output), &doc) != nil || doc.Tests != 19 || doc.Failures != 18 {
					t.Fatal(output)
				}
			} else if !strings.Contains(output, "1 passed, 18 failed, 0 skipped") {
				t.Fatal(output)
			}
			code, output = invoke("--reporter", reporter, "--rerun-failed", historyPath, "--save-failures", historyPath, "--filter", "negated finite mismatch", "float_matchers")
			if code != 1 || !strings.Contains(output, "negated finite mismatch") || strings.Contains(output, "float-cleanup") || strings.Contains(output, "float-failing-later") {
				t.Fatal(code, output)
			}
			history, err = readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 1 || len(history.Files[0].Suites) != 1 || fmt.Sprint(history.Files[0].Suites[0].Cases) != "[negated finite mismatch]" {
				t.Fatal(history, err)
			}
		})
	}
	code, output := invoke("--bail", "float_matchers/failures.test.km")
	if code != 1 || !strings.Contains(output, "float-cleanup") || strings.Contains(output, "float-failing-later") || !strings.Contains(output, "0 passed, 1 failed, 18 skipped") {
		t.Fatal(code, output)
	}
	keika := os.Getenv("KEIKA")
	if keika == "" {
		keika = "keika"
	}
	for _, file := range []string{"expected", "tolerance", "nominal", "nominal_tolerance", "integer", "string", "boolean", "constructor", "source"} {
		t.Run("compile rejection/"+file, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, keika, "check", "float_matchers/type_errors/"+file+".km")
			command.Dir = consumer
			output, err := command.CombinedOutput()
			var exit *exec.ExitError
			constraint := file == "integer" || file == "string" || file == "boolean" || file == "constructor"
			message := string(output)
			if !errors.As(err, &exit) || (constraint && !strings.Contains(message, "does not satisfy T type parameter constraint")) || (!constraint && !strings.Contains(message, "cannot use")) {
				t.Fatalf("compiler did not reject mismatched float types: %v %s", err, output)
			}
		})
	}
}

func TestOrderedMatchersRealKinmokusei(t *testing.T) {
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
			t.Fatalf("ordered matcher integration: %v %s", err, output)
		}
		return exit.ExitCode(), string(output)
	}
	for _, reporter := range []string{"text", "json", "junit"} {
		t.Run(reporter, func(t *testing.T) {
			code, output := invoke("--reporter", reporter, "ordered_matchers/passing.test.km")
			if code != 0 || !strings.Contains(output, "typed ordered matchers") {
				t.Fatal(code, output)
			}
			historyPath := filepath.Join(t.TempDir(), "history.json")
			code, output = invoke("--reporter", reporter, "--save-failures", historyPath, "ordered_matchers/failures.test.km")
			if code != 1 || !strings.Contains(output, "ordered-afterEach") || !strings.Contains(output, "ordered-cleanup") || !strings.Contains(output, "ordered-later-case") || !strings.Contains(output, "not to be less than or equal to") {
				t.Fatal(code, output)
			}
			history, err := readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 1 || history.Files[0].WholeFile || len(history.Files[0].Suites) != 1 || history.Files[0].Suites[0].WholeSuite || len(history.Files[0].Suites[0].Cases) != 8 {
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
						if len(result.Failures) != 1 || result.Failures[0].Phase != "body" || !strings.HasSuffix(filepath.ToSlash(result.Failures[0].Location.File), "/generated.go") || result.Failures[0].Location.Line <= 0 || result.Failures[0].Location.Source != "go" {
							t.Fatal(result)
						}
					}
				}
				if failed != 8 {
					t.Fatal(failed, output)
				}
			} else if reporter == "junit" {
				var doc struct {
					Tests    int `xml:"tests,attr"`
					Failures int `xml:"failures,attr"`
				}
				if xml.Unmarshal([]byte(output), &doc) != nil || doc.Tests != 9 || doc.Failures != 8 {
					t.Fatal(output)
				}
			} else if !strings.Contains(output, "1 passed, 8 failed, 0 skipped") {
				t.Fatal(output)
			}
			code, output = invoke("--reporter", reporter, "--rerun-failed", historyPath, "--save-failures", historyPath, "--filter", "negated less than or equal", "ordered_matchers")
			if code != 1 || !strings.Contains(output, "negated less than or equal") || strings.Contains(output, "ordered-cleanup") || strings.Contains(output, "ordered-later-case") {
				t.Fatal(code, output)
			}
			history, err = readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 1 || len(history.Files[0].Suites) != 1 || fmt.Sprint(history.Files[0].Suites[0].Cases) != "[negated less than or equal]" {
				t.Fatal(history, err)
			}
		})
	}
	code, output := invoke("--bail", "ordered_matchers/failures.test.km")
	if code != 1 || !strings.Contains(output, "ordered-cleanup") || strings.Contains(output, "ordered-later-case") || !strings.Contains(output, "0 passed, 1 failed, 8 skipped") {
		t.Fatal(code, output)
	}
	keika := os.Getenv("KEIKA")
	if keika == "" {
		keika = "keika"
	}
	for _, file := range []string{"expected", "nominal", "width", "boolean", "slice", "constructor"} {
		t.Run("compile rejection/"+file, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, keika, "check", "ordered_matchers/type_errors/"+file+".km")
			command.Dir = consumer
			output, err := command.CombinedOutput()
			var exit *exec.ExitError
			constraint := file == "boolean" || file == "slice" || file == "constructor"
			message := string(output)
			if !errors.As(err, &exit) || (constraint && !strings.Contains(message, "does not satisfy T type parameter constraint")) || (!constraint && !strings.Contains(message, "cannot use")) {
				t.Fatalf("compiler did not reject mismatched ordered types: %v %s", err, output)
			}
		})
	}
}

func TestErrorTypePredicateRealKinmokusei(t *testing.T) {
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
			t.Fatalf("error type predicate integration: %v %s", err, output)
		}
		return exit.ExitCode(), string(output)
	}
	for _, reporter := range []string{"text", "json", "junit"} {
		t.Run(reporter, func(t *testing.T) {
			historyPath := filepath.Join(t.TempDir(), "history.json")
			code, output := invoke("--reporter", reporter, "--save-failures", historyPath, "error_type_predicate/passing.test.km")
			if code != 0 || strings.Contains(output, "unreachable-") || !strings.Contains(output, "error-type-predicate-passing-later") || !strings.Contains(output, "error-type-predicate-skip-cleanup") || !strings.Contains(output, "predicate unavailable") || !strings.Contains(output, "5 passed, 0 failed, 2 skipped, 0 todo") {
				t.Fatal(code, output)
			}
			history, err := readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 0 {
				t.Fatal(history, err)
			}
			code, output = invoke("--reporter", reporter, "--save-failures", historyPath, "error_type_predicate/failures.test.km")
			if code != 1 || strings.Contains(output, "unreachable-") || !strings.Contains(output, "error-type-predicate-failing-later") || !strings.Contains(output, "error-type-predicate-afterEach") || !strings.Contains(output, "error-type-predicate-cleanup") {
				t.Fatal(code, output)
			}
			history, err = readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 1 || history.Files[0].WholeFile || len(history.Files[0].Suites) != 1 || history.Files[0].Suites[0].WholeSuite || fmt.Sprint(history.Files[0].Suites[0].Cases) != "[positive predicate mismatch negated predicate match missing positive type nil positive predicate nil negative predicate invalid positive type invalid negative type raw predicate throw explicit predicate location]" {
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
						case "explicit predicate location":
							if location.File != "predicate.km" || location.Line != 42 || location.Source != "explicit" {
								t.Fatal(result)
							}
						case "raw predicate throw":
							if location != (LocationReport{}) {
								t.Fatal("invented predicate location", result)
							}
						default:
							if !strings.HasSuffix(filepath.ToSlash(location.File), "/generated.go") || location.Line <= 0 || location.Source != "go" {
								t.Fatal(result)
							}
						}
					}
				}
				if failed != 9 {
					t.Fatal(failed, output)
				}
			} else if reporter == "junit" {
				var doc struct {
					Tests    int `xml:"tests,attr"`
					Failures int `xml:"failures,attr"`
				}
				if xml.Unmarshal([]byte(output), &doc) != nil || doc.Tests != 10 || doc.Failures != 9 {
					t.Fatal(output)
				}
			} else if !strings.Contains(output, "1 passed, 9 failed, 0 skipped") {
				t.Fatal(output)
			}
			code, output = invoke("--reporter", reporter, "--rerun-failed", historyPath, "--save-failures", historyPath, "--filter", "raw predicate throw", "error_type_predicate")
			if code != 1 || !strings.Contains(output, "raw predicate throw") || strings.Contains(output, "error-type-predicate-cleanup") || strings.Contains(output, "error-type-predicate-failing-later") {
				t.Fatal(code, output)
			}
			history, err = readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 1 || len(history.Files[0].Suites) != 1 || fmt.Sprint(history.Files[0].Suites[0].Cases) != "[raw predicate throw]" {
				t.Fatal(history, err)
			}
		})
	}
	code, output := invoke("--bail", "error_type_predicate/passing.test.km")
	if code != 0 || strings.Contains(output, "unreachable-") || !strings.Contains(output, "error-type-predicate-passing-later") || !strings.Contains(output, "5 passed, 0 failed, 2 skipped") {
		t.Fatal("predicate skip triggered bail", code, output)
	}
	code, output = invoke("--bail", "error_type_predicate/failures.test.km")
	if code != 1 || !strings.Contains(output, "error-type-predicate-cleanup") || strings.Contains(output, "error-type-predicate-failing-later") || !strings.Contains(output, "0 passed, 1 failed, 9 skipped") {
		t.Fatal(code, output)
	}
	for _, fixture := range []struct{ file, prefix string }{
		{"predicate_panic", "error-type-predicate-panic"},
	} {
		historyPath := filepath.Join(t.TempDir(), "panic.json")
		code, output := invoke("--save-failures", historyPath, "error_type_predicate/"+fixture.file+".test.km")
		if code != 1 || !strings.Contains(output, "reflect: call of reflect.Value.Index on int Value") || strings.Contains(output, "unreachable-") {
			t.Fatal(code, output)
		}
		last := -1
		for _, phase := range []string{"afterEach", "cleanup", "afterAll"} {
			index := strings.Index(output, fixture.prefix+"-"+phase)
			if index <= last {
				t.Fatal("error type predicate panic cleanup order changed", output)
			}
			last = index
		}
		history, err := readFailureHistory(historyPath)
		if err != nil || len(history.Files) != 1 || !history.Files[0].WholeFile {
			t.Fatal(history, err)
		}
	}
	keika := os.Getenv("KEIKA")
	if keika == "" {
		keika = "keika"
	}
	for _, file := range []string{"parameter", "result", "callback", "negated", "arity", "narrow", "binding", "constructor"} {
		t.Run("compile rejection/"+file, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, keika, "check", "error_type_predicate/type_errors/"+file+".km")
			command.Dir = consumer
			output, err := command.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || !strings.Contains(string(output), "cannot use") {
				t.Fatalf("compiler did not reject error type predicate types: %v %s", err, output)
			}
		})
	}
}

func TestMockTypePredicateRealKinmokusei(t *testing.T) {
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
			t.Fatalf("mock type predicate integration: %v %s", err, output)
		}
		return exit.ExitCode(), string(output)
	}
	for _, reporter := range []string{"text", "json", "junit"} {
		t.Run(reporter, func(t *testing.T) {
			historyPath := filepath.Join(t.TempDir(), "history.json")
			code, output := invoke("--reporter", reporter, "--save-failures", historyPath, "mock_type_predicate/passing.test.km")
			if code != 0 || strings.Contains(output, "unreachable-") || !strings.Contains(output, "mock-type-predicate-passing-later") || !strings.Contains(output, "mock-type-predicate-skip-cleanup") || !strings.Contains(output, "predicate unavailable") || !strings.Contains(output, "7 passed, 0 failed, 3 skipped, 0 todo") {
				t.Fatal(code, output)
			}
			history, err := readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 0 {
				t.Fatal(history, err)
			}
			code, output = invoke("--reporter", reporter, "--save-failures", historyPath, "mock_type_predicate/failures.test.km")
			if code != 1 || strings.Contains(output, "unreachable-") || !strings.Contains(output, "mock-type-predicate-failing-later") || !strings.Contains(output, "mock-type-predicate-afterEach") || !strings.Contains(output, "mock-type-predicate-cleanup") {
				t.Fatal(code, output)
			}
			history, err = readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 1 || history.Files[0].WholeFile || len(history.Files[0].Suites) != 1 || history.Files[0].Suites[0].WholeSuite || fmt.Sprint(history.Files[0].Suites[0].Cases) != "[positive any mismatch negative any match positive nth mismatch negative nth match positive last mismatch negative last match missing positive type missing nth invocation missing last invocation nil any predicate nil negative nth predicate nil last predicate invalid any type invalid negative nth type invalid last type invalid nth index invalid negative nth index nil any source nil negative nth source typed nil last source raw predicate throw explicit predicate location raw source throw]" {
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
						case "explicit predicate location":
							if location.File != "predicate.km" || location.Line != 42 || location.Source != "explicit" {
								t.Fatal(result)
							}
						case "raw predicate throw", "raw source throw":
							if location != (LocationReport{}) {
								t.Fatal("invented predicate location", result)
							}
						default:
							if !strings.HasSuffix(filepath.ToSlash(location.File), "/generated.go") || location.Line <= 0 || location.Source != "go" {
								t.Fatal(result)
							}
						}
					}
				}
				if failed != 23 {
					t.Fatal(failed, output)
				}
			} else if reporter == "junit" {
				var doc struct {
					Tests    int `xml:"tests,attr"`
					Failures int `xml:"failures,attr"`
				}
				if xml.Unmarshal([]byte(output), &doc) != nil || doc.Tests != 24 || doc.Failures != 23 {
					t.Fatal(output)
				}
			} else if !strings.Contains(output, "1 passed, 23 failed, 0 skipped") {
				t.Fatal(output)
			}
			code, output = invoke("--reporter", reporter, "--rerun-failed", historyPath, "--save-failures", historyPath, "--filter", "raw predicate throw", "mock_type_predicate")
			if code != 1 || !strings.Contains(output, "raw predicate throw") || strings.Contains(output, "mock-type-predicate-cleanup") || strings.Contains(output, "mock-type-predicate-failing-later") {
				t.Fatal(code, output)
			}
			history, err = readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 1 || len(history.Files[0].Suites) != 1 || fmt.Sprint(history.Files[0].Suites[0].Cases) != "[raw predicate throw]" {
				t.Fatal(history, err)
			}
		})
	}
	code, output := invoke("--bail", "mock_type_predicate/passing.test.km")
	if code != 0 || strings.Contains(output, "unreachable-") || !strings.Contains(output, "mock-type-predicate-passing-later") || !strings.Contains(output, "7 passed, 0 failed, 3 skipped") {
		t.Fatal("predicate skip triggered bail", code, output)
	}
	code, output = invoke("--bail", "mock_type_predicate/failures.test.km")
	if code != 1 || !strings.Contains(output, "mock-type-predicate-cleanup") || strings.Contains(output, "mock-type-predicate-failing-later") || !strings.Contains(output, "0 passed, 1 failed, 23 skipped") {
		t.Fatal(code, output)
	}
	for _, fixture := range []struct{ file, prefix string }{
		{"source_panic", "mock-type-source-panic"},
		{"predicate_panic", "mock-type-predicate-panic"},
	} {
		historyPath := filepath.Join(t.TempDir(), "panic.json")
		code, output := invoke("--save-failures", historyPath, "mock_type_predicate/"+fixture.file+".test.km")
		if code != 1 || !strings.Contains(output, "reflect: call of reflect.Value.Index on int Value") || strings.Contains(output, "unreachable-") {
			t.Fatal(code, output)
		}
		last := -1
		for _, phase := range []string{"afterEach", "cleanup", "afterAll"} {
			index := strings.Index(output, fixture.prefix+"-"+phase)
			if index <= last {
				t.Fatal("mock type predicate panic cleanup order changed", output)
			}
			last = index
		}
		history, err := readFailureHistory(historyPath)
		if err != nil || len(history.Files) != 1 || !history.Files[0].WholeFile {
			t.Fatal(history, err)
		}
	}
	keika := os.Getenv("KEIKA")
	if keika == "" {
		keika = "keika"
	}
	for _, file := range []string{"input", "result", "arity", "index", "source", "constructor", "callback", "binding", "narrow", "incompatible"} {
		t.Run("compile rejection/"+file, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, keika, "check", "mock_type_predicate/type_errors/"+file+".km")
			command.Dir = consumer
			output, err := command.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || !strings.Contains(string(output), "cannot use") {
				t.Fatalf("compiler did not reject mock type predicate types: %v %s", err, output)
			}
		})
	}
}

func TestMockThrowBehaviorRealKinmokusei(t *testing.T) {
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
			t.Fatalf("mock throw behavior integration: %v %s", err, output)
		}
		return exit.ExitCode(), string(output)
	}
	for _, reporter := range []string{"text", "json", "junit"} {
		t.Run(reporter, func(t *testing.T) {
			historyPath := filepath.Join(t.TempDir(), "history.json")
			code, output := invoke("--reporter", reporter, "--save-failures", historyPath, "mock_throw_behavior/passing.test.km")
			if code != 0 || strings.Contains(output, "unreachable-") || !strings.Contains(output, "mock-throw-behavior-passing-later") || !strings.Contains(output, "mock-throw-behavior-skip-cleanup") || !strings.Contains(output, "mock unavailable") || !strings.Contains(output, "7 passed, 0 failed, 2 skipped, 0 todo") {
				t.Fatal(code, output)
			}
			history, err := readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 0 {
				t.Fatal(history, err)
			}
			code, output = invoke("--reporter", reporter, "--save-failures", historyPath, "mock_throw_behavior/failures.test.km")
			if code != 1 || strings.Contains(output, "unreachable-") || !strings.Contains(output, "mock-throw-behavior-failing-later") || !strings.Contains(output, "mock-throw-behavior-afterEach") || !strings.Contains(output, "mock-throw-behavior-cleanup") {
				t.Fatal(code, output)
			}
			history, err = readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 1 || history.Files[0].WholeFile || len(history.Files[0].Suites) != 1 || history.Files[0].Suites[0].WholeSuite || fmt.Sprint(history.Files[0].Suites[0].Cases) != "[function default throw function queued throw action default throw action queued throw configured error location explicit invoke location]" {
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
						case "configured error location":
							if location.File != "configured.km" || location.Line != 42 || location.Source != "explicit" {
								t.Fatal(result)
							}
						case "explicit invoke location":
							if location.File != "invocation.km" || location.Line != 27 || location.Source != "explicit" {
								t.Fatal(result)
							}
						default:
							if location != (LocationReport{}) {
								t.Fatal("invented configured throw location", result)
							}
						}
					}
				}
				if failed != 6 {
					t.Fatal(failed, output)
				}
			} else if reporter == "junit" {
				var doc struct {
					Tests    int `xml:"tests,attr"`
					Failures int `xml:"failures,attr"`
				}
				if xml.Unmarshal([]byte(output), &doc) != nil || doc.Tests != 7 || doc.Failures != 6 {
					t.Fatal(output)
				}
			} else if !strings.Contains(output, "1 passed, 6 failed, 0 skipped") {
				t.Fatal(output)
			}
			code, output = invoke("--reporter", reporter, "--rerun-failed", historyPath, "--save-failures", historyPath, "--filter", "function queued throw", "mock_throw_behavior")
			if code != 1 || !strings.Contains(output, "function queued throw") || strings.Contains(output, "mock-throw-behavior-cleanup") || strings.Contains(output, "mock-throw-behavior-failing-later") {
				t.Fatal(code, output)
			}
			history, err = readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 1 || len(history.Files[0].Suites) != 1 || fmt.Sprint(history.Files[0].Suites[0].Cases) != "[function queued throw]" {
				t.Fatal(history, err)
			}
		})
	}
	code, output := invoke("--bail", "mock_throw_behavior/passing.test.km")
	if code != 0 || strings.Contains(output, "unreachable-") || !strings.Contains(output, "mock-throw-behavior-passing-later") || !strings.Contains(output, "7 passed, 0 failed, 2 skipped") {
		t.Fatal("predicate skip triggered bail", code, output)
	}
	code, output = invoke("--bail", "mock_throw_behavior/failures.test.km")
	if code != 1 || !strings.Contains(output, "mock-throw-behavior-cleanup") || strings.Contains(output, "mock-throw-behavior-failing-later") || !strings.Contains(output, "0 passed, 1 failed, 6 skipped") {
		t.Fatal(code, output)
	}
	for _, fixture := range []struct{ file, prefix string }{
		{"error_panic", "mock-throw-format-panic"},
	} {
		historyPath := filepath.Join(t.TempDir(), "panic.json")
		code, output := invoke("--save-failures", historyPath, "mock_throw_behavior/"+fixture.file+".test.km")
		if code != 1 || !strings.Contains(output, "reflect: call of reflect.Value.Index on int Value") || strings.Contains(output, "unreachable-") {
			t.Fatal(code, output)
		}
		last := -1
		for _, phase := range []string{"afterEach", "cleanup", "afterAll"} {
			index := strings.Index(output, fixture.prefix+"-"+phase)
			if index <= last {
				t.Fatal("mock throw behavior panic cleanup order changed", output)
			}
			last = index
		}
		history, err := readFailureHistory(historyPath)
		if err != nil || len(history.Files) != 1 || !history.Files[0].WholeFile {
			t.Fatal(history, err)
		}
	}
	keika := os.Getenv("KEIKA")
	if keika == "" {
		keika = "keika"
	}
	for _, file := range []string{"function", "once", "action", "action_once", "value", "binding"} {
		t.Run("compile rejection/"+file, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, keika, "check", "mock_throw_behavior/type_errors/"+file+".km")
			command.Dir = consumer
			output, err := command.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || !strings.Contains(string(output), "cannot use") {
				t.Fatalf("compiler did not reject mock throw behavior types: %v %s", err, output)
			}
		})
	}
}

func TestFunctionTypePredicateRealKinmokusei(t *testing.T) {
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
			t.Fatalf("function type predicate integration: %v %s", err, output)
		}
		return exit.ExitCode(), string(output)
	}
	for _, reporter := range []string{"text", "json", "junit"} {
		t.Run(reporter, func(t *testing.T) {
			historyPath := filepath.Join(t.TempDir(), "history.json")
			code, output := invoke("--reporter", reporter, "--save-failures", historyPath, "function_type_predicate/passing.test.km")
			if code != 0 || strings.Contains(output, "unreachable-") || !strings.Contains(output, "function-type-predicate-passing-later") || !strings.Contains(output, "function-type-predicate-skip-cleanup") || !strings.Contains(output, "predicate unavailable") || !strings.Contains(output, "7 passed, 0 failed, 2 skipped, 0 todo") {
				t.Fatal(code, output)
			}
			history, err := readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 0 {
				t.Fatal(history, err)
			}
			code, output = invoke("--reporter", reporter, "--save-failures", historyPath, "function_type_predicate/failures.test.km")
			if code != 1 || strings.Contains(output, "unreachable-") || !strings.Contains(output, "function-type-predicate-failing-later") || !strings.Contains(output, "function-type-predicate-afterEach") || !strings.Contains(output, "function-type-predicate-cleanup") {
				t.Fatal(code, output)
			}
			history, err = readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 1 || history.Files[0].WholeFile || len(history.Files[0].Suites) != 1 || history.Files[0].Suites[0].WholeSuite || fmt.Sprint(history.Files[0].Suites[0].Cases) != "[positive predicate mismatch negated predicate match normal return requires throw missing positive type nil positive predicate nil negative predicate invalid positive type invalid negative type nil positive body nil negative body raw predicate throw explicit predicate location]" {
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
						case "explicit predicate location":
							if location.File != "predicate.km" || location.Line != 42 || location.Source != "explicit" {
								t.Fatal(result)
							}
						case "raw predicate throw":
							if location != (LocationReport{}) {
								t.Fatal("invented predicate location", result)
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
			} else if !strings.Contains(output, "1 passed, 12 failed, 0 skipped") {
				t.Fatal(output)
			}
			code, output = invoke("--reporter", reporter, "--rerun-failed", historyPath, "--save-failures", historyPath, "--filter", "raw predicate throw", "function_type_predicate")
			if code != 1 || !strings.Contains(output, "raw predicate throw") || strings.Contains(output, "function-type-predicate-cleanup") || strings.Contains(output, "function-type-predicate-failing-later") {
				t.Fatal(code, output)
			}
			history, err = readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 1 || len(history.Files[0].Suites) != 1 || fmt.Sprint(history.Files[0].Suites[0].Cases) != "[raw predicate throw]" {
				t.Fatal(history, err)
			}
		})
	}
	code, output := invoke("--bail", "function_type_predicate/passing.test.km")
	if code != 0 || strings.Contains(output, "unreachable-") || !strings.Contains(output, "function-type-predicate-passing-later") || !strings.Contains(output, "7 passed, 0 failed, 2 skipped") {
		t.Fatal("predicate skip triggered bail", code, output)
	}
	code, output = invoke("--bail", "function_type_predicate/failures.test.km")
	if code != 1 || !strings.Contains(output, "function-type-predicate-cleanup") || strings.Contains(output, "function-type-predicate-failing-later") || !strings.Contains(output, "0 passed, 1 failed, 12 skipped") {
		t.Fatal(code, output)
	}
	for _, fixture := range []struct{ file, prefix string }{
		{"body_panic", "function-type-body-panic"},
		{"predicate_panic", "function-type-predicate-panic"},
	} {
		historyPath := filepath.Join(t.TempDir(), "panic.json")
		code, output := invoke("--save-failures", historyPath, "function_type_predicate/"+fixture.file+".test.km")
		if code != 1 || !strings.Contains(output, "reflect: call of reflect.Value.Index on int Value") || strings.Contains(output, "unreachable-") {
			t.Fatal(code, output)
		}
		last := -1
		for _, phase := range []string{"afterEach", "cleanup", "afterAll"} {
			index := strings.Index(output, fixture.prefix+"-"+phase)
			if index <= last {
				t.Fatal("function type predicate panic cleanup order changed", output)
			}
			last = index
		}
		history, err := readFailureHistory(historyPath)
		if err != nil || len(history.Files) != 1 || !history.Files[0].WholeFile {
			t.Fatal(history, err)
		}
	}
	keika := os.Getenv("KEIKA")
	if keika == "" {
		keika = "keika"
	}
	for _, file := range []string{"parameter", "result", "callback", "negated", "arity", "narrow", "binding", "constructor"} {
		t.Run("compile rejection/"+file, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, keika, "check", "function_type_predicate/type_errors/"+file+".km")
			command.Dir = consumer
			output, err := command.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || !strings.Contains(string(output), "cannot use") {
				t.Fatalf("compiler did not reject function type predicate types: %v %s", err, output)
			}
		})
	}
}

func TestFunctionThrowPredicateRealKinmokusei(t *testing.T) {
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
			t.Fatalf("function throw predicate integration: %v %s", err, output)
		}
		return exit.ExitCode(), string(output)
	}
	for _, reporter := range []string{"text", "json", "junit"} {
		t.Run(reporter, func(t *testing.T) {
			historyPath := filepath.Join(t.TempDir(), "history.json")
			code, output := invoke("--reporter", reporter, "--save-failures", historyPath, "function_throw_predicate/passing.test.km")
			if code != 0 || strings.Contains(output, "unreachable-") || !strings.Contains(output, "function-throw-predicate-passing-later") || !strings.Contains(output, "function-throw-predicate-skip-cleanup") || !strings.Contains(output, "predicate unavailable") || !strings.Contains(output, "6 passed, 0 failed, 2 skipped, 0 todo") {
				t.Fatal(code, output)
			}
			history, err := readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 0 {
				t.Fatal(history, err)
			}
			code, output = invoke("--reporter", reporter, "--save-failures", historyPath, "function_throw_predicate/failures.test.km")
			if code != 1 || strings.Contains(output, "unreachable-") || !strings.Contains(output, "function-throw-predicate-failing-later") || !strings.Contains(output, "function-throw-predicate-afterEach") || !strings.Contains(output, "function-throw-predicate-cleanup") {
				t.Fatal(code, output)
			}
			history, err = readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 1 || history.Files[0].WholeFile || len(history.Files[0].Suites) != 1 || history.Files[0].Suites[0].WholeSuite || fmt.Sprint(history.Files[0].Suites[0].Cases) != "[positive predicate mismatch negated predicate match normal return requires throw nil positive predicate nil negative predicate nil positive body nil negative body raw predicate throw explicit predicate location]" {
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
						case "explicit predicate location":
							if location.File != "predicate.km" || location.Line != 42 || location.Source != "explicit" {
								t.Fatal(result)
							}
						case "raw predicate throw":
							if location != (LocationReport{}) {
								t.Fatal("invented predicate location", result)
							}
						default:
							if !strings.HasSuffix(filepath.ToSlash(location.File), "/generated.go") || location.Line <= 0 || location.Source != "go" {
								t.Fatal(result)
							}
						}
					}
				}
				if failed != 9 {
					t.Fatal(failed, output)
				}
			} else if reporter == "junit" {
				var doc struct {
					Tests    int `xml:"tests,attr"`
					Failures int `xml:"failures,attr"`
				}
				if xml.Unmarshal([]byte(output), &doc) != nil || doc.Tests != 10 || doc.Failures != 9 {
					t.Fatal(output)
				}
			} else if !strings.Contains(output, "1 passed, 9 failed, 0 skipped") {
				t.Fatal(output)
			}
			code, output = invoke("--reporter", reporter, "--rerun-failed", historyPath, "--save-failures", historyPath, "--filter", "raw predicate throw", "function_throw_predicate")
			if code != 1 || !strings.Contains(output, "raw predicate throw") || strings.Contains(output, "function-throw-predicate-cleanup") || strings.Contains(output, "function-throw-predicate-failing-later") {
				t.Fatal(code, output)
			}
			history, err = readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 1 || len(history.Files[0].Suites) != 1 || fmt.Sprint(history.Files[0].Suites[0].Cases) != "[raw predicate throw]" {
				t.Fatal(history, err)
			}
		})
	}
	code, output := invoke("--bail", "function_throw_predicate/passing.test.km")
	if code != 0 || strings.Contains(output, "unreachable-") || !strings.Contains(output, "function-throw-predicate-passing-later") || !strings.Contains(output, "6 passed, 0 failed, 2 skipped") {
		t.Fatal("predicate skip triggered bail", code, output)
	}
	code, output = invoke("--bail", "function_throw_predicate/failures.test.km")
	if code != 1 || !strings.Contains(output, "function-throw-predicate-cleanup") || strings.Contains(output, "function-throw-predicate-failing-later") || !strings.Contains(output, "0 passed, 1 failed, 9 skipped") {
		t.Fatal(code, output)
	}
	for _, fixture := range []struct{ file, prefix string }{
		{"body_panic", "function-predicate-body-panic"},
		{"predicate_panic", "function-predicate-panic"},
	} {
		historyPath := filepath.Join(t.TempDir(), "panic.json")
		code, output := invoke("--save-failures", historyPath, "function_throw_predicate/"+fixture.file+".test.km")
		if code != 1 || !strings.Contains(output, "reflect: call of reflect.Value.Index on int Value") || strings.Contains(output, "unreachable-") {
			t.Fatal(code, output)
		}
		last := -1
		for _, phase := range []string{"afterEach", "cleanup", "afterAll"} {
			index := strings.Index(output, fixture.prefix+"-"+phase)
			if index <= last {
				t.Fatal("function throw predicate panic cleanup order changed", output)
			}
			last = index
		}
		history, err := readFailureHistory(historyPath)
		if err != nil || len(history.Files) != 1 || !history.Files[0].WholeFile {
			t.Fatal(history, err)
		}
	}
	keika := os.Getenv("KEIKA")
	if keika == "" {
		keika = "keika"
	}
	for _, file := range []string{"parameter", "result", "callback", "negated", "arity", "narrow", "binding", "constructor"} {
		t.Run("compile rejection/"+file, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, keika, "check", "function_throw_predicate/type_errors/"+file+".km")
			command.Dir = consumer
			output, err := command.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || !strings.Contains(string(output), "cannot use") {
				t.Fatalf("compiler did not reject function throw predicate types: %v %s", err, output)
			}
		})
	}
}

func TestMockResetRealKinmokusei(t *testing.T) {
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
			t.Fatalf("mock reset integration: %v %s", err, output)
		}
		return exit.ExitCode(), string(output)
	}
	for _, reporter := range []string{"text", "json", "junit"} {
		t.Run(reporter, func(t *testing.T) {
			historyPath := filepath.Join(t.TempDir(), "history.json")
			code, output := invoke("--reporter", reporter, "--save-failures", historyPath, "mock_reset/passing.test.km")
			if code != 0 || strings.Contains(output, "unreachable-") || !strings.Contains(output, "mock-reset-passing-later") || !strings.Contains(output, "6 passed, 0 failed, 0 skipped, 0 todo") {
				t.Fatal(code, output)
			}
			history, err := readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 0 {
				t.Fatal(history, err)
			}
			code, output = invoke("--reporter", reporter, "--save-failures", historyPath, "mock_reset/failures.test.km")
			if code != 1 || strings.Contains(output, "unreachable-") || !strings.Contains(output, "mock-reset-failing-later") || !strings.Contains(output, "mock-reset-afterEach") || !strings.Contains(output, "mock-reset-cleanup") || !strings.Contains(output, "mock-reset-skip-cleanup") || !strings.Contains(output, "replacement unavailable") {
				t.Fatal(code, output)
			}
			history, err = readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 1 || history.Files[0].WholeFile || len(history.Files[0].Suites) != 1 || history.Files[0].Suites[0].WholeSuite || fmt.Sprint(history.Files[0].Suites[0].Cases) != "[new function implementation throws cleared call history fails old expectation cleared result history fails old expectation cleared action history fails old expectation new action implementation throws]" {
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
						case "new function implementation throws":
							if location.File != "domain-check.km" || location.Line != 42 || location.Source != "explicit" {
								t.Fatal(result)
							}
						case "new action implementation throws":
							if location != (LocationReport{}) {
								t.Fatal("invented reset callback location", result)
							}
						default:
							if !strings.HasSuffix(filepath.ToSlash(location.File), "/generated.go") || location.Line <= 0 || location.Source != "go" {
								t.Fatal(result)
							}
						}
					}
				}
				if failed != 5 {
					t.Fatal(failed, output)
				}
			} else if reporter == "junit" {
				var doc struct {
					Tests    int `xml:"tests,attr"`
					Failures int `xml:"failures,attr"`
				}
				if xml.Unmarshal([]byte(output), &doc) != nil || doc.Tests != 7 || doc.Failures != 5 {
					t.Fatal(output)
				}
			} else if !strings.Contains(output, "1 passed, 5 failed, 1 skipped") {
				t.Fatal(output)
			}
			code, output = invoke("--reporter", reporter, "--rerun-failed", historyPath, "--save-failures", historyPath, "--filter", "cleared call history", "mock_reset")
			if code != 1 || !strings.Contains(output, "cleared call history fails old expectation") || strings.Contains(output, "mock-reset-cleanup") || strings.Contains(output, "mock-reset-skip-cleanup") || strings.Contains(output, "mock-reset-failing-later") {
				t.Fatal(code, output)
			}
			history, err = readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 1 || len(history.Files[0].Suites) != 1 || fmt.Sprint(history.Files[0].Suites[0].Cases) != "[cleared call history fails old expectation]" {
				t.Fatal(history, err)
			}
		})
	}
	code, output := invoke("--bail", "mock_reset/failures.test.km")
	if code != 1 || !strings.Contains(output, "mock-reset-cleanup") || strings.Contains(output, "mock-reset-failing-later") || strings.Contains(output, "mock-reset-skip-cleanup") || !strings.Contains(output, "0 passed, 1 failed, 6 skipped") {
		t.Fatal(code, output)
	}
	for _, kind := range []string{"function", "action"} {
		historyPath := filepath.Join(t.TempDir(), "panic.json")
		code, output := invoke("--save-failures", historyPath, "mock_reset/"+kind+"_panic.test.km")
		if code != 1 || !strings.Contains(output, "reflect: call of reflect.Value.Index on int Value") || strings.Contains(output, "unreachable-") {
			t.Fatal(code, output)
		}
		last := -1
		for _, phase := range []string{"afterEach", "cleanup", "afterAll"} {
			index := strings.Index(output, "reset-"+kind+"-panic-"+phase)
			if index <= last {
				t.Fatal("reset callback panic cleanup order changed", output)
			}
			last = index
		}
		history, err := readFailureHistory(historyPath)
		if err != nil || len(history.Files) != 1 || !history.Files[0].WholeFile {
			t.Fatal(history, err)
		}
	}
	keika := os.Getenv("KEIKA")
	if keika == "" {
		keika = "keika"
	}
	for _, file := range []string{"argument", "argument_width", "result_width", "result", "callback", "action_argument", "action_result", "binding"} {
		t.Run("compile rejection/"+file, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, keika, "check", "mock_reset/type_errors/"+file+".km")
			command.Dir = consumer
			output, err := command.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || !strings.Contains(string(output), "cannot use") {
				t.Fatalf("compiler did not reject reset callback types: %v %s", err, output)
			}
		})
	}
}

func TestContextHooksRealKinmokusei(t *testing.T) {
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
			t.Fatalf("context hook integration: %v %s", err, output)
		}
		return exit.ExitCode(), string(output)
	}
	for _, reporter := range []string{"text", "json", "junit"} {
		t.Run(reporter, func(t *testing.T) {
			historyPath := filepath.Join(t.TempDir(), "history.json")
			code, output := invoke("--reporter", reporter, "--save-failures", historyPath, "context_hooks/passing.test.km")
			if code != 0 || strings.Contains(output, "unreachable-") {
				t.Fatal(code, output)
			}
			for _, marker := range []string{"context-hook-passing-later", "context-hook-shared-cleanup", "context-hook-before-cleanup", "context-hook-after-cleanup", "context-hook-ordinary-before", "context-hook-ordinary-after", "5 passed, 0 failed, 1 skipped, 1 todo"} {
				if !strings.Contains(output, marker) {
					t.Fatal("missing", marker, output)
				}
			}
			history, err := readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 0 {
				t.Fatal(history, err)
			}
			if reporter == "junit" {
				var doc struct {
					Tests    int `xml:"tests,attr"`
					Failures int `xml:"failures,attr"`
				}
				if xml.Unmarshal([]byte(output), &doc) != nil || doc.Tests != 10 || doc.Failures != 0 {
					t.Fatal(output)
				}
			}
			code, output = invoke("--reporter", reporter, "--save-failures", historyPath, "context_hooks/failures.test.km")
			if code != 1 || strings.Contains(output, "unreachable-") || !strings.Contains(output, "context-hook-failing-later") || !strings.Contains(output, "hook setup unavailable") || !strings.Contains(output, "context-hook-cleanup-1") {
				t.Fatal(code, output)
			}
			history, err = readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 1 || history.Files[0].WholeFile || len(history.Files[0].Suites) != 1 || history.Files[0].Suites[0].WholeSuite || fmt.Sprint(history.Files[0].Suites[0].Cases) != "[setup teardown and cleanup teardown skip fails teardown failure body failure]" {
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
						var phases []string
						for _, failure := range result.Failures {
							phases = append(phases, failure.Phase)
							location := failure.Location
							file, line := "", 0
							switch failure.Phase {
							case "beforeEach":
								file, line = "setup.km", 7
							case "afterEach":
								file, line = "teardown.km", 9
							case "cleanup":
								file, line = "cleanup.km", 11
							case "body":
								file, line = "domain-check.km", 42
							default:
								t.Fatal(failure)
							}
							if result.Name == "teardown skip fails" {
								if location != (LocationReport{}) {
									t.Fatal("invented teardown location", result)
								}
							} else if location.File != file || location.Line != line || location.Source != "explicit" {
								t.Fatal(result)
							}
						}
						want := "afterEach"
						if result.Name == "setup teardown and cleanup" {
							want = "beforeEach,afterEach,cleanup"
						}
						if result.Name == "body failure" {
							want = "body"
						}
						if strings.Join(phases, ",") != want {
							t.Fatal(result)
						}
					}
				}
				if failed != 4 {
					t.Fatal(failed, output)
				}
			} else if reporter == "junit" {
				var doc struct {
					Tests    int `xml:"tests,attr"`
					Failures int `xml:"failures,attr"`
				}
				if xml.Unmarshal([]byte(output), &doc) != nil || doc.Tests != 6 || doc.Failures != 4 {
					t.Fatal(output)
				}
			} else if !strings.Contains(output, "1 passed, 4 failed, 1 skipped") {
				t.Fatal(output)
			}
			code, output = invoke("--reporter", reporter, "--rerun-failed", historyPath, "--save-failures", historyPath, "--filter", "setup teardown and cleanup", "context_hooks")
			if code != 1 || !strings.Contains(output, "context-hook-cleanup-0") || strings.Contains(output, "context-hook-cleanup-1") || strings.Contains(output, "context-hook-failing-later") || strings.Contains(output, "unreachable-") {
				t.Fatal(code, output)
			}
			history, err = readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 1 || len(history.Files[0].Suites) != 1 || fmt.Sprint(history.Files[0].Suites[0].Cases) != "[setup teardown and cleanup]" {
				t.Fatal(history, err)
			}
		})
	}
	code, output := invoke("--bail", "context_hooks/failures.test.km")
	if code != 1 || !strings.Contains(output, "context-hook-cleanup-0") || strings.Contains(output, "context-hook-cleanup-1") || strings.Contains(output, "context-hook-failing-later") || !strings.Contains(output, "0 passed, 1 failed, 5 skipped") {
		t.Fatal(code, output)
	}
	code, output = invoke("--filter", "row 1", "context_hooks/selection.test.km")
	if code != 0 || !strings.Contains(output, "context-hook-selected-plain-1") || !strings.Contains(output, "context-hook-selected-shared-1") || strings.Contains(output, "context-hook-selected-plain-0") || strings.Contains(output, "context-hook-selected-shared-0") || strings.Contains(output, "context-hook-selected-plain-2") || strings.Contains(output, "context-hook-selected-shared-2") {
		t.Fatal(code, output)
	}
	var firstOrder string
	for run := 0; run < 2; run++ {
		code, output = invoke("--shuffle", "42", "context_hooks/selection.test.km")
		if code != 0 {
			t.Fatal(code, output)
		}
		var markers []string
		seen := map[string]bool{}
		for _, line := range strings.Split(output, "\n") {
			if strings.HasPrefix(line, "context-hook-selected-") {
				markers = append(markers, line)
				seen[line] = true
			}
		}
		order := strings.Join(markers, "\n")
		if len(markers) != 6 || len(seen) != 6 || (run == 1 && order != firstOrder) {
			t.Fatal("hook selection changed shuffle order", output)
		}
		firstOrder = order
	}
	for _, fixture := range []struct {
		file, prefix string
		phases       []string
	}{
		{"before_panic", "context-before-panic", []string{"afterEach", "afterCleanup", "cleanup", "afterAll"}},
		{"after_panic", "context-after-panic", []string{"afterEach", "afterCleanup", "cleanup", "afterAll", "sharedCleanup"}},
	} {
		historyPath := filepath.Join(t.TempDir(), "panic.json")
		code, output := invoke("--save-failures", historyPath, "context_hooks/"+fixture.file+".test.km")
		if code != 1 || !strings.Contains(output, "reflect: call of reflect.Value.Index on int Value") || strings.Contains(output, "unreachable-") {
			t.Fatal(code, output)
		}
		last := -1
		for _, phase := range fixture.phases {
			index := strings.Index(output, fixture.prefix+"-"+phase)
			if index <= last {
				t.Fatal("hook panic cleanup order changed", output)
			}
			last = index
		}
		history, err := readFailureHistory(historyPath)
		if err != nil || len(history.Files) != 1 || !history.Files[0].WholeFile {
			t.Fatal(history, err)
		}
	}
	keika := os.Getenv("KEIKA")
	if keika == "" {
		keika = "keika"
	}
	for _, file := range []string{"before_context", "after_result", "arity", "shared_fixture", "shared_context", "shared_result", "shared_reversed", "shared_width"} {
		t.Run("compile rejection/"+file, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, keika, "check", "context_hooks/type_errors/"+file+".km")
			command.Dir = consumer
			output, err := command.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || !strings.Contains(string(output), "cannot use") {
				t.Fatalf("compiler did not reject hook callback types: %v %s", err, output)
			}
		})
	}
}

func TestStringBoundaryRealKinmokusei(t *testing.T) {
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
			t.Fatalf("string boundary integration: %v %s", err, output)
		}
		return exit.ExitCode(), string(output)
	}
	for _, reporter := range []string{"text", "json", "junit"} {
		t.Run(reporter, func(t *testing.T) {
			historyPath := filepath.Join(t.TempDir(), "history.json")
			code, output := invoke("--reporter", reporter, "--save-failures", historyPath, "string_boundary/passing.test.km")
			if code != 0 || !strings.Contains(output, "string-boundary-passing-later") || !strings.Contains(output, "6 passed, 0 failed, 0 skipped, 0 todo") {
				t.Fatal(code, output)
			}
			history, err := readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 0 {
				t.Fatal(history, err)
			}
			code, output = invoke("--reporter", reporter, "--save-failures", historyPath, "string_boundary/failures.test.km")
			if code != 1 || !strings.Contains(output, "string-boundary-afterEach") || !strings.Contains(output, "string-boundary-cleanup") || !strings.Contains(output, "string-boundary-afterAll") || !strings.Contains(output, "string-boundary-failing-later") || !strings.Contains(output, "start with") || strings.Contains(output, "stack overflow") {
				t.Fatal(code, output)
			}
			history, err = readFailureHistory(historyPath)
			wantCases := "[missing prefix negated matching prefix missing suffix negated matching suffix negated empty prefix negated empty suffix literal regex characters quoted byte diagnostics explicit location]"
			if err != nil || len(history.Files) != 1 || history.Files[0].WholeFile || len(history.Files[0].Suites) != 1 || history.Files[0].Suites[0].WholeSuite || fmt.Sprint(history.Files[0].Suites[0].Cases) != wantCases {
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
						if len(result.Failures) != 1 || result.Failures[0].Phase != "body" || len(result.Failures[0].Message) > 1000 {
							t.Fatal(result)
						}
						relation := "to start with"
						if strings.Contains(result.Name, "suffix") || result.Name == "quoted byte diagnostics" {
							relation = "to end with"
						}
						if !strings.Contains(result.Failures[0].Message, relation) {
							t.Fatal(result)
						}
						if result.Name == "quoted byte diagnostics" && result.Failures[0].Message != `expected "\xff\x00\n" to end with "text\n"` {
							t.Fatal(result)
						}
						location := result.Failures[0].Location
						if result.Name == "explicit location" {
							if location.File != "domain-check.km" || location.Line != 42 || location.Source != "explicit" {
								t.Fatal(result)
							}
						} else if !strings.HasSuffix(filepath.ToSlash(location.File), "/generated.go") || location.Line <= 0 || location.Source != "go" {
							t.Fatal(result)
						}
					}
				}
				if failed != 9 {
					t.Fatal(failed, output)
				}
			} else if reporter == "junit" {
				var doc struct {
					Tests    int `xml:"tests,attr"`
					Failures int `xml:"failures,attr"`
				}
				if xml.Unmarshal([]byte(output), &doc) != nil || doc.Tests != 10 || doc.Failures != 9 {
					t.Fatal(output)
				}
			} else if !strings.Contains(output, "1 passed, 9 failed, 0 skipped") {
				t.Fatal(output)
			}
			code, output = invoke("--reporter", reporter, "--rerun-failed", historyPath, "--save-failures", historyPath, "--filter", "missing suffix", "string_boundary")
			if code != 1 || !strings.Contains(output, "missing suffix") || strings.Contains(output, "string-boundary-cleanup") || strings.Contains(output, "string-boundary-failing-later") {
				t.Fatal(code, output)
			}
			history, err = readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 1 || len(history.Files[0].Suites) != 1 || fmt.Sprint(history.Files[0].Suites[0].Cases) != "[missing suffix]" {
				t.Fatal(history, err)
			}
		})
	}
	code, output := invoke("--bail", "string_boundary/failures.test.km")
	if code != 1 || !strings.Contains(output, "string-boundary-cleanup") || !strings.Contains(output, "string-boundary-afterAll") || strings.Contains(output, "string-boundary-failing-later") || !strings.Contains(output, "0 passed, 1 failed, 9 skipped") {
		t.Fatal(code, output)
	}
	code, output = invoke("--bail", "string_boundary/passing.test.km")
	if code != 0 || !strings.Contains(output, "string-boundary-passing-later") {
		t.Fatal(code, output)
	}
	keika := os.Getenv("KEIKA")
	if keika == "" {
		keika = "keika"
	}
	for _, file := range []string{"prefix", "suffix", "negated_prefix", "negated_suffix", "constructor", "negated_constructor", "binding", "negated_binding", "result", "prefix_nominal", "suffix_nominal", "nil"} {
		t.Run("compile rejection/"+file, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, keika, "check", "string_boundary/type_errors/"+file+".km")
			command.Dir = consumer
			output, err := command.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || !strings.Contains(string(output), "cannot use") {
				t.Fatalf("compiler did not reject string boundary types: %v %s", err, output)
			}
		})
	}
}

func TestMapEntriesRealKinmokusei(t *testing.T) {
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
			t.Fatalf("map entries integration: %v %s", err, output)
		}
		return exit.ExitCode(), string(output)
	}
	for _, reporter := range []string{"text", "json", "junit"} {
		t.Run(reporter, func(t *testing.T) {
			historyPath := filepath.Join(t.TempDir(), "history.json")
			code, output := invoke("--reporter", reporter, "--save-failures", historyPath, "map_entries/passing.test.km")
			if code != 0 || !strings.Contains(output, "map-entries-passing-later") || !strings.Contains(output, "6 passed, 0 failed, 0 skipped, 0 todo") {
				t.Fatal(code, output)
			}
			history, err := readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 0 {
				t.Fatal(history, err)
			}
			code, output = invoke("--reporter", reporter, "--save-failures", historyPath, "map_entries/failures.test.km")
			if code != 1 || !strings.Contains(output, "map-entries-afterEach") || !strings.Contains(output, "map-entries-cleanup") || !strings.Contains(output, "map-entries-afterAll") || !strings.Contains(output, "map-entries-failing-later") || !strings.Contains(output, "have entries") || strings.Contains(output, "stack overflow") {
				t.Fatal(code, output)
			}
			history, err = readFailureHistory(historyPath)
			wantCases := "[missing key wrong value negated matching subset nil actual negated empty requirements negated nil requirements nested nil differs from empty NaN key alias NaN value alias cyclic entry diagnostics explicit location]"
			if err != nil || len(history.Files) != 1 || history.Files[0].WholeFile || len(history.Files[0].Suites) != 1 || history.Files[0].Suites[0].WholeSuite || fmt.Sprint(history.Files[0].Suites[0].Cases) != wantCases {
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
						if len(result.Failures) != 1 || result.Failures[0].Phase != "body" || len(result.Failures[0].Message) > 1000 {
							t.Fatal(result)
						}
						if !strings.Contains(result.Failures[0].Message, "have entries") || (result.Name == "cyclic entry diagnostics" && !strings.Contains(result.Failures[0].Message, "<cyclic value>")) {
							t.Fatal(result)
						}
						location := result.Failures[0].Location
						if result.Name == "explicit location" {
							if location.File != "domain-check.km" || location.Line != 42 || location.Source != "explicit" {
								t.Fatal(result)
							}
						} else if !strings.HasSuffix(filepath.ToSlash(location.File), "/generated.go") || location.Line <= 0 || location.Source != "go" {
							t.Fatal(result)
						}
					}
				}
				if failed != 11 {
					t.Fatal(failed, output)
				}
			} else if reporter == "junit" {
				var doc struct {
					Tests    int `xml:"tests,attr"`
					Failures int `xml:"failures,attr"`
				}
				if xml.Unmarshal([]byte(output), &doc) != nil || doc.Tests != 12 || doc.Failures != 11 {
					t.Fatal(output)
				}
			} else if !strings.Contains(output, "1 passed, 11 failed, 0 skipped") {
				t.Fatal(output)
			}
			code, output = invoke("--reporter", reporter, "--rerun-failed", historyPath, "--save-failures", historyPath, "--filter", "wrong value", "map_entries")
			if code != 1 || !strings.Contains(output, "wrong value") || strings.Contains(output, "map-entries-cleanup") || strings.Contains(output, "map-entries-failing-later") {
				t.Fatal(code, output)
			}
			history, err = readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 1 || len(history.Files[0].Suites) != 1 || fmt.Sprint(history.Files[0].Suites[0].Cases) != "[wrong value]" {
				t.Fatal(history, err)
			}
		})
	}
	code, output := invoke("--bail", "map_entries/failures.test.km")
	if code != 1 || !strings.Contains(output, "map-entries-cleanup") || !strings.Contains(output, "map-entries-afterAll") || strings.Contains(output, "map-entries-failing-later") || !strings.Contains(output, "0 passed, 1 failed, 11 skipped") {
		t.Fatal(code, output)
	}
	code, output = invoke("--bail", "map_entries/passing.test.km")
	if code != 0 || !strings.Contains(output, "map-entries-passing-later") {
		t.Fatal(code, output)
	}
	keika := os.Getenv("KEIKA")
	if keika == "" {
		keika = "keika"
	}
	for _, file := range []string{"key", "value", "key_nominal", "value_nominal", "key_width", "value_width", "negated", "constructor", "explicit", "binding", "scalar"} {
		t.Run("compile rejection/"+file, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, keika, "check", "map_entries/type_errors/"+file+".km")
			command.Dir = consumer
			output, err := command.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || !strings.Contains(string(output), "cannot use") {
				t.Fatalf("compiler did not reject map entry types: %v %s", err, output)
			}
		})
	}
}

func TestCycleDiagnosticsRealKinmokusei(t *testing.T) {
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
			t.Fatalf("cycle diagnostics integration: %v %s", err, output)
		}
		return exit.ExitCode(), string(output)
	}
	for _, reporter := range []string{"text", "json", "junit"} {
		t.Run(reporter, func(t *testing.T) {
			historyPath := filepath.Join(t.TempDir(), "history.json")
			code, output := invoke("--reporter", reporter, "--save-failures", historyPath, "cycle_diagnostics/passing.test.km")
			if code != 0 || !strings.Contains(output, "cycle-diagnostic-passing-later") || !strings.Contains(output, "5 passed, 0 failed, 0 skipped, 0 todo") {
				t.Fatal(code, output)
			}
			history, err := readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 0 {
				t.Fatal(history, err)
			}
			code, output = invoke("--reporter", reporter, "--save-failures", historyPath, "cycle_diagnostics/failures.test.km")
			if code != 1 || !strings.Contains(output, "cycle-diagnostic-afterEach") || !strings.Contains(output, "cycle-diagnostic-cleanup") || !strings.Contains(output, "cycle-diagnostic-afterAll") || !strings.Contains(output, "cycle-diagnostic-failing-later") || !strings.Contains(output, "cyclic value") || strings.Contains(output, "stack overflow") {
				t.Fatal(code, output)
			}
			history, err = readFailureHistory(historyPath)
			wantCases := "[equality negated equality nil predicate slice contains slice subset slice unordered map entry mock calls mock returns legacy mock calls legacy mock returns explicit location diagnostic depth limit diagnostic breadth limit]"
			if err != nil || len(history.Files) != 1 || history.Files[0].WholeFile || len(history.Files[0].Suites) != 1 || history.Files[0].Suites[0].WholeSuite || fmt.Sprint(history.Files[0].Suites[0].Cases) != wantCases {
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
						if len(result.Failures) != 1 || result.Failures[0].Phase != "body" || len(result.Failures[0].Message) > 1000 {
							t.Fatal(result)
						}
						marker := "<cyclic value>"
						if strings.HasPrefix(result.Name, "diagnostic ") {
							marker = "<diagnostic traversal limit>"
						}
						if !strings.Contains(result.Failures[0].Message, marker) {
							t.Fatal(result)
						}
						location := result.Failures[0].Location
						if result.Name == "explicit location" {
							if location.File != "domain-check.km" || location.Line != 42 || location.Source != "explicit" {
								t.Fatal(result)
							}
						} else if !strings.HasSuffix(filepath.ToSlash(location.File), "/generated.go") || location.Line <= 0 || location.Source != "go" {
							t.Fatal(result)
						}
					}
				}
				if failed != 15 {
					t.Fatal(failed, output)
				}
			} else if reporter == "junit" {
				var doc struct {
					Tests    int `xml:"tests,attr"`
					Failures int `xml:"failures,attr"`
				}
				if xml.Unmarshal([]byte(output), &doc) != nil || doc.Tests != 16 || doc.Failures != 15 {
					t.Fatal(output)
				}
			} else if !strings.Contains(output, "1 passed, 15 failed, 0 skipped") {
				t.Fatal(output)
			}
			code, output = invoke("--reporter", reporter, "--rerun-failed", historyPath, "--save-failures", historyPath, "--filter", "slice subset", "cycle_diagnostics")
			if code != 1 || !strings.Contains(output, "slice subset") || strings.Contains(output, "cycle-diagnostic-cleanup") || strings.Contains(output, "cycle-diagnostic-failing-later") {
				t.Fatal(code, output)
			}
			history, err = readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 1 || len(history.Files[0].Suites) != 1 || fmt.Sprint(history.Files[0].Suites[0].Cases) != "[slice subset]" {
				t.Fatal(history, err)
			}
		})
	}
	code, output := invoke("--bail", "cycle_diagnostics/failures.test.km")
	if code != 1 || !strings.Contains(output, "cycle-diagnostic-cleanup") || !strings.Contains(output, "cycle-diagnostic-afterAll") || strings.Contains(output, "cycle-diagnostic-failing-later") || !strings.Contains(output, "0 passed, 1 failed, 15 skipped") {
		t.Fatal(code, output)
	}
	code, output = invoke("--bail", "cycle_diagnostics/passing.test.km")
	if code != 0 || !strings.Contains(output, "cycle-diagnostic-passing-later") {
		t.Fatal(code, output)
	}
	historyPath := filepath.Join(t.TempDir(), "panic.json")
	code, output = invoke("--save-failures", historyPath, "cycle_diagnostics/panic.test.km")
	if code != 1 || !strings.Contains(output, "reflect: call of reflect.Value.Index on int Value") || strings.Contains(output, "unreachable-cycle-diagnostic-panic-later") {
		t.Fatal(code, output)
	}
	last := -1
	for _, phase := range []string{"afterEach", "cleanup", "afterAll"} {
		index := strings.Index(output, "cycle-diagnostic-panic-"+phase)
		if index <= last {
			t.Fatal("native panic cleanup order changed", output)
		}
		last = index
	}
	history, err := readFailureHistory(historyPath)
	if err != nil || len(history.Files) != 1 || !history.Files[0].WholeFile {
		t.Fatal(history, err)
	}
}

func TestSliceContainsAllRealKinmokusei(t *testing.T) {
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
			t.Fatalf("slice containment integration: %v %s", err, output)
		}
		return exit.ExitCode(), string(output)
	}
	for _, reporter := range []string{"text", "json", "junit"} {
		t.Run(reporter, func(t *testing.T) {
			historyPath := filepath.Join(t.TempDir(), "history.json")
			code, output := invoke("--reporter", reporter, "--save-failures", historyPath, "slice_contains_all/passing.test.km")
			if code != 0 || !strings.Contains(output, "typed slice containment") || !strings.Contains(output, "slice-containment-passing-later") || !strings.Contains(output, "6 passed, 0 failed, 0 skipped, 0 todo") {
				t.Fatal(code, output)
			}
			history, err := readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 0 {
				t.Fatal(history, err)
			}
			code, output = invoke("--reporter", reporter, "--save-failures", historyPath, "slice_contains_all/failures.test.km")
			if code != 1 || !strings.Contains(output, "slice-containment-afterEach") || !strings.Contains(output, "slice-containment-cleanup") || !strings.Contains(output, "slice-containment-failing-later") || !strings.Contains(output, "including duplicate counts") {
				t.Fatal(code, output)
			}
			history, err = readFailureHistory(historyPath)
			wantCases := "[different multiplicity negated reordered inclusion too many required missing element negated nil inclusion negated empty inclusion nested order remains significant NaN element explicit failure location]"
			if err != nil || len(history.Files) != 1 || history.Files[0].WholeFile || len(history.Files[0].Suites) != 1 || history.Files[0].Suites[0].WholeSuite || fmt.Sprint(history.Files[0].Suites[0].Cases) != wantCases {
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
						if result.Name == "explicit failure location" {
							if location.File != "domain-check.km" || location.Line != 42 || location.Source != "explicit" {
								t.Fatal(result)
							}
						} else if !strings.HasSuffix(filepath.ToSlash(location.File), "/generated.go") || location.Line <= 0 || location.Source != "go" {
							t.Fatal(result)
						}
					}
				}
				if failed != 9 {
					t.Fatal(failed, output)
				}
			} else if reporter == "junit" {
				var doc struct {
					Tests    int `xml:"tests,attr"`
					Failures int `xml:"failures,attr"`
				}
				if xml.Unmarshal([]byte(output), &doc) != nil || doc.Tests != 10 || doc.Failures != 9 {
					t.Fatal(output)
				}
			} else if !strings.Contains(output, "1 passed, 9 failed, 0 skipped") {
				t.Fatal(output)
			}
			code, output = invoke("--reporter", reporter, "--rerun-failed", historyPath, "--save-failures", historyPath, "--filter", "negated reordered inclusion", "slice_contains_all")
			if code != 1 || !strings.Contains(output, "negated reordered inclusion") || strings.Contains(output, "slice-containment-cleanup") || strings.Contains(output, "slice-containment-failing-later") {
				t.Fatal(code, output)
			}
			history, err = readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 1 || len(history.Files[0].Suites) != 1 || fmt.Sprint(history.Files[0].Suites[0].Cases) != "[negated reordered inclusion]" {
				t.Fatal(history, err)
			}
		})
	}
	code, output := invoke("--bail", "slice_contains_all/failures.test.km")
	if code != 1 || !strings.Contains(output, "slice-containment-cleanup") || strings.Contains(output, "slice-containment-failing-later") || !strings.Contains(output, "0 passed, 1 failed, 9 skipped") {
		t.Fatal(code, output)
	}
	keika := os.Getenv("KEIKA")
	if keika == "" {
		keika = "keika"
	}
	for _, file := range []string{"width", "nominal", "negated", "scalar", "constructor", "explicit", "nested", "constructor_actual", "binding"} {
		t.Run("compile rejection/"+file, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, keika, "check", "slice_contains_all/type_errors/"+file+".km")
			command.Dir = consumer
			output, err := command.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || !strings.Contains(string(output), "cannot use") {
				t.Fatalf("compiler did not reject slice containment types: %v %s", err, output)
			}
		})
	}
}

func TestUnorderedSliceMatchersRealKinmokusei(t *testing.T) {
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
			t.Fatalf("unordered slice integration: %v %s", err, output)
		}
		return exit.ExitCode(), string(output)
	}
	for _, reporter := range []string{"text", "json", "junit"} {
		t.Run(reporter, func(t *testing.T) {
			historyPath := filepath.Join(t.TempDir(), "history.json")
			code, output := invoke("--reporter", reporter, "--save-failures", historyPath, "unordered_slice_matchers/passing.test.km")
			if code != 0 || !strings.Contains(output, "typed unordered slice matchers") || !strings.Contains(output, "unordered-slice-passing-later") || !strings.Contains(output, "6 passed, 0 failed, 0 skipped, 0 todo") {
				t.Fatal(code, output)
			}
			history, err := readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 0 {
				t.Fatal(history, err)
			}
			code, output = invoke("--reporter", reporter, "--save-failures", historyPath, "unordered_slice_matchers/failures.test.km")
			if code != 1 || !strings.Contains(output, "unordered-slice-afterEach") || !strings.Contains(output, "unordered-slice-cleanup") || !strings.Contains(output, "unordered-slice-failing-later") || !strings.Contains(output, "ignoring order") {
				t.Fatal(code, output)
			}
			history, err = readFailureHistory(historyPath)
			wantCases := "[different multiplicity negated reordered equality different length nil versus empty negated nil equality negated empty equality nested order remains significant NaN element explicit failure location]"
			if err != nil || len(history.Files) != 1 || history.Files[0].WholeFile || len(history.Files[0].Suites) != 1 || history.Files[0].Suites[0].WholeSuite || fmt.Sprint(history.Files[0].Suites[0].Cases) != wantCases {
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
						if result.Name == "explicit failure location" {
							if location.File != "domain-check.km" || location.Line != 42 || location.Source != "explicit" {
								t.Fatal(result)
							}
						} else if !strings.HasSuffix(filepath.ToSlash(location.File), "/generated.go") || location.Line <= 0 || location.Source != "go" {
							t.Fatal(result)
						}
					}
				}
				if failed != 9 {
					t.Fatal(failed, output)
				}
			} else if reporter == "junit" {
				var doc struct {
					Tests    int `xml:"tests,attr"`
					Failures int `xml:"failures,attr"`
				}
				if xml.Unmarshal([]byte(output), &doc) != nil || doc.Tests != 10 || doc.Failures != 9 {
					t.Fatal(output)
				}
			} else if !strings.Contains(output, "1 passed, 9 failed, 0 skipped") {
				t.Fatal(output)
			}
			code, output = invoke("--reporter", reporter, "--rerun-failed", historyPath, "--save-failures", historyPath, "--filter", "negated reordered equality", "unordered_slice_matchers")
			if code != 1 || !strings.Contains(output, "negated reordered equality") || strings.Contains(output, "unordered-slice-cleanup") || strings.Contains(output, "unordered-slice-failing-later") {
				t.Fatal(code, output)
			}
			history, err = readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 1 || len(history.Files[0].Suites) != 1 || fmt.Sprint(history.Files[0].Suites[0].Cases) != "[negated reordered equality]" {
				t.Fatal(history, err)
			}
		})
	}
	code, output := invoke("--bail", "unordered_slice_matchers/failures.test.km")
	if code != 1 || !strings.Contains(output, "unordered-slice-cleanup") || strings.Contains(output, "unordered-slice-failing-later") || !strings.Contains(output, "0 passed, 1 failed, 9 skipped") {
		t.Fatal(code, output)
	}
	keika := os.Getenv("KEIKA")
	if keika == "" {
		keika = "keika"
	}
	for _, file := range []string{"width", "nominal", "negated", "scalar", "constructor", "explicit", "nested", "constructor_actual"} {
		t.Run("compile rejection/"+file, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, keika, "check", "unordered_slice_matchers/type_errors/"+file+".km")
			command.Dir = consumer
			output, err := command.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || !strings.Contains(string(output), "cannot use") {
				t.Fatalf("compiler did not reject unordered slice types: %v %s", err, output)
			}
		})
	}
}

func TestInactiveTablesRealKinmokusei(t *testing.T) {
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
			t.Fatalf("inactive table integration: %v %s", err, output)
		}
		return exit.ExitCode(), string(output)
	}
	checkReport := func(reporter, output string, tests, passed, skipped, todo, failures int) {
		t.Helper()
		if strings.Contains(output, "unreachable-") {
			t.Fatal("inactive callback ran", output)
		}
		switch reporter {
		case "json":
			counts := map[string]int{}
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
				for _, row := range event.Report.Summary.Results {
					counts[row.Status]++
					if (row.Status == "skip" || row.Status == "todo") && (row.DurationNs != 0 || len(row.Failures) != 0) {
						t.Fatal(row)
					}
				}
			}
			if counts["pass"] != passed || counts["skip"] != skipped || counts["todo"] != todo || counts["fail"] != 0 {
				t.Fatal(counts, output)
			}
		case "junit":
			var doc struct {
				Tests    int `xml:"tests,attr"`
				Failures int `xml:"failures,attr"`
				Skipped  int `xml:"skipped,attr"`
			}
			if xml.Unmarshal([]byte(output), &doc) != nil || doc.Tests != tests || doc.Failures != failures || doc.Skipped != skipped+todo-failures {
				t.Fatal(doc, output)
			}
		case "text":
			if !strings.Contains(output, "todo") || !strings.Contains(output, "skipped") {
				t.Fatal(output)
			}
		}
	}
	for _, reporter := range []string{"text", "json", "junit"} {
		t.Run(reporter, func(t *testing.T) {
			historyPath := filepath.Join(t.TempDir(), "history.json")
			code, output := invoke("--reporter", reporter, "--save-failures", historyPath, "inactive_tables/passing.test.km")
			if code != 0 || !strings.Contains(output, "inactive-registration-verified") {
				t.Fatal(code, output)
			}
			checkReport(reporter, output, 17, 1, 8, 8, 0)
			history, err := readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 0 {
				t.Fatal(history, err)
			}
			code, output = invoke("--reporter", reporter, "--fail-on-todo", "--save-failures", historyPath, "inactive_tables/selection.test.km")
			if code != 1 {
				t.Fatal(code, output)
			}
			checkReport(reporter, output, 5, 1, 2, 2, 2)
			last := -1
			for _, marker := range []string{"inactive-shared-setup", "inactive-case-before", "inactive-active-body", "inactive-case-after", "inactive-case-cleanup", "inactive-shared-cleanup"} {
				index := strings.Index(output, marker)
				if index <= last || strings.Count(output, marker) != 1 {
					t.Fatal("active lifecycle changed", output)
				}
				last = index
			}
			history, err = readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 1 || history.Files[0].WholeFile || len(history.Files[0].Suites) != 1 || history.Files[0].Suites[0].WholeSuite || fmt.Sprint(history.Files[0].Suites[0].Cases) != "[todo 0 todo 1]" {
				t.Fatal(history, err)
			}
			code, output = invoke("--reporter", reporter, "--fail-on-todo", "--rerun-failed", historyPath, "--filter", "todo 1", "--save-failures", historyPath, "inactive_tables")
			if code != 1 || strings.Contains(output, "inactive-shared-") || strings.Contains(output, "inactive-case-") || strings.Contains(output, "inactive-active-body") {
				t.Fatal("inactive rerun ran fixture", code, output)
			}
			history, err = readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 1 || len(history.Files[0].Suites) != 1 || fmt.Sprint(history.Files[0].Suites[0].Cases) != "[todo 1]" {
				t.Fatal(history, err)
			}
			code, output = invoke("--reporter", reporter, "--allow-todo", "--rerun-failed", historyPath, "--save-failures", historyPath, "inactive_tables")
			if code != 0 || strings.Contains(output, "inactive-shared-") {
				t.Fatal(code, output)
			}
			history, err = readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 0 {
				t.Fatal(history, err)
			}
		})
	}
	for _, args := range [][]string{
		{"--fail-on-todo", "--filter", "skip 1"},
		{"--bail", "--shuffle", "42", "--repeat", "2"},
	} {
		code, output := invoke(append(args, "inactive_tables/selection.test.km")...)
		if code != 0 || strings.Contains(output, "unreachable-") {
			t.Fatal(code, output)
		}
		if args[0] == "--fail-on-todo" {
			if strings.Contains(output, "inactive-shared-") || strings.Contains(output, "inactive-case-") || strings.Contains(output, "inactive-active-body") {
				t.Fatal("filtered skip acquired fixture", output)
			}
		} else if strings.Count(output, "inactive-active-body") != 2 || strings.Count(output, "inactive-shared-cleanup") != 2 {
			t.Fatal("repeat lost active lifecycle", output)
		}
	}
	keika := os.Getenv("KEIKA")
	if keika == "" {
		keika = "keika"
	}
	for _, file := range []string{"skip_row", "todo_name", "skip_index", "todo_index", "skip_width", "shared_fixture", "shared_index", "binding"} {
		t.Run("compile rejection/"+file, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, keika, "check", "inactive_tables/type_errors/"+file+".km")
			command.Dir = consumer
			output, err := command.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || !strings.Contains(string(output), "cannot use") {
				t.Fatalf("compiler accepted inactive row types: %v %s", err, output)
			}
		})
	}
}

func TestIndexedTablesRealKinmokusei(t *testing.T) {
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
			t.Fatalf("indexed table integration: %v %s", err, output)
		}
		return exit.ExitCode(), string(output)
	}
	for _, reporter := range []string{"text", "json", "junit"} {
		t.Run(reporter, func(t *testing.T) {
			historyPath := filepath.Join(t.TempDir(), "history.json")
			code, output := invoke("--reporter", reporter, "--save-failures", historyPath, "indexed_tables/passing.test.km")
			if code != 0 || strings.Contains(output, "unreachable-") {
				t.Fatal(code, output)
			}
			for _, marker := range []string{"indexed-passing-later", "indexed fixture unavailable", "indexed-context-cleanup-1", "indexed-fixture-cleanup-1", "indexed-shared-cleanup", "indexed-shared-row-cleanup-2"} {
				if !strings.Contains(output, marker) {
					t.Fatal("missing", marker, output)
				}
			}
			history, err := readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 0 {
				t.Fatal(history, err)
			}
			if reporter == "junit" {
				var doc struct {
					Tests    int `xml:"tests,attr"`
					Failures int `xml:"failures,attr"`
				}
				if xml.Unmarshal([]byte(output), &doc) != nil || doc.Tests != 13 || doc.Failures != 0 {
					t.Fatal(output)
				}
			}
			code, output = invoke("--reporter", reporter, "--save-failures", historyPath, "indexed_tables/failures.test.km")
			if code != 1 || !strings.Contains(output, "indexed-failing-later") || !strings.Contains(output, "indexed-fixture-cleanup-1") || !strings.Contains(output, "indexed row unavailable") {
				t.Fatal(code, output)
			}
			history, err = readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 1 || history.Files[0].WholeFile || len(history.Files[0].Suites) != 1 || history.Files[0].Suites[0].WholeSuite || fmt.Sprint(history.Files[0].Suites[0].Cases) != "[plain 1 context 0 context 2 fixture 0 fixture 1 fixture 2]" {
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
						phase := "body"
						if result.Name == "fixture 1" {
							phase = "fixture"
						}
						if result.Name == "fixture 2" {
							phase = "cleanup"
						}
						if len(result.Failures) != 1 || result.Failures[0].Phase != phase {
							t.Fatal(result)
						}
						location := result.Failures[0].Location
						switch result.Name {
						case "context 0":
							if location.File != "domain-check.km" || location.Line != 42 || location.Source != "explicit" {
								t.Fatal(result)
							}
						case "context 2":
							if location != (LocationReport{}) {
								t.Fatal("invented body location", result)
							}
						case "fixture 1":
							if location.File != "setup-check.km" || location.Line != 7 || location.Source != "explicit" {
								t.Fatal(result)
							}
						case "fixture 2":
							if location.File != "cleanup-check.km" || location.Line != 9 || location.Source != "explicit" {
								t.Fatal(result)
							}
						default:
							if !strings.HasSuffix(filepath.ToSlash(location.File), "/generated.go") || location.Line <= 0 || location.Source != "go" {
								t.Fatal(result)
							}
						}
					}
				}
				if failed != 6 {
					t.Fatal(failed, output)
				}
			} else if reporter == "junit" {
				var doc struct {
					Tests    int `xml:"tests,attr"`
					Failures int `xml:"failures,attr"`
				}
				if xml.Unmarshal([]byte(output), &doc) != nil || doc.Tests != 10 || doc.Failures != 6 {
					t.Fatal(output)
				}
			} else if !strings.Contains(output, "3 passed, 6 failed, 1 skipped") {
				t.Fatal(output)
			}
			code, output = invoke("--reporter", reporter, "--rerun-failed", historyPath, "--save-failures", historyPath, "--filter", "fixture 1", "indexed_tables")
			if code != 1 || !strings.Contains(output, "indexed-setup-1") || !strings.Contains(output, "indexed-fixture-cleanup-1") || strings.Contains(output, "indexed-setup-0") || strings.Contains(output, "indexed-setup-2") || strings.Contains(output, "indexed-failing-later") {
				t.Fatal(code, output)
			}
			history, err = readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 1 || len(history.Files[0].Suites) != 1 || fmt.Sprint(history.Files[0].Suites[0].Cases) != "[fixture 1]" {
				t.Fatal(history, err)
			}
		})
	}
	code, output := invoke("--bail", "indexed_tables/failures.test.km")
	if code != 1 || !strings.Contains(output, "1 passed, 1 failed, 8 skipped") || strings.Contains(output, "indexed-setup-") || strings.Contains(output, "indexed-context-cleanup-") || strings.Contains(output, "indexed-failing-later") {
		t.Fatal(code, output)
	}
	code, output = invoke("--filter", "1", "indexed_tables/selection.test.km")
	if code != 0 {
		t.Fatal(code, output)
	}
	for _, variant := range []string{"plain", "context", "fixture", "shared"} {
		if !strings.Contains(output, "indexed-selected-"+variant+"-1") || strings.Contains(output, "indexed-selected-"+variant+"-0") || strings.Contains(output, "indexed-selected-"+variant+"-2") {
			t.Fatal(output)
		}
	}
	if !strings.Contains(output, "indexed-selected-setup-1") || strings.Contains(output, "indexed-selected-setup-0") || strings.Contains(output, "indexed-selected-setup-2") {
		t.Fatal(output)
	}
	var firstOrder string
	for run := 0; run < 2; run++ {
		code, output = invoke("--shuffle", "42", "indexed_tables/selection.test.km")
		if code != 0 {
			t.Fatal(code, output)
		}
		var markers []string
		seen := map[string]bool{}
		for _, line := range strings.Split(output, "\n") {
			for _, variant := range []string{"plain", "context", "fixture", "shared"} {
				for index := 0; index < 3; index++ {
					if line == fmt.Sprintf("indexed-selected-%s-%d", variant, index) {
						markers = append(markers, line)
						seen[line] = true
					}
				}
			}
		}
		order := strings.Join(markers, "\n")
		if len(markers) != 12 || len(seen) != 12 || (run == 1 && order != firstOrder) {
			t.Fatal("indexed shuffle changed row identity or order", output)
		}
		firstOrder = order
	}
	for _, fixture := range []struct {
		file, prefix string
		phases       []string
	}{
		{"body_panic", "indexed-body-panic", []string{"afterEach", "cleanup", "afterAll"}},
		{"fixture_panic", "indexed-fixture-panic", []string{"afterEach", "cleanup", "afterAll"}},
		{"shared_panic", "indexed-shared-panic", []string{"afterEach", "cleanup", "afterAll", "sharedCleanup"}},
		{"name_panic", "indexed-name", []string{"0", "1"}},
	} {
		historyPath := filepath.Join(t.TempDir(), "panic.json")
		code, output := invoke("--save-failures", historyPath, "indexed_tables/"+fixture.file+".test.km")
		if code != 1 || !strings.Contains(output, "reflect: call of reflect.Value.Index on int Value") || strings.Contains(output, "unreachable-") {
			t.Fatal(code, output)
		}
		last := -1
		for _, phase := range fixture.phases {
			index := strings.Index(output, fixture.prefix+"-"+phase)
			if index <= last {
				t.Fatal("indexed panic cleanup order changed", output)
			}
			last = index
		}
		history, err := readFailureHistory(historyPath)
		if err != nil || len(history.Files) != 1 || !history.Files[0].WholeFile {
			t.Fatal(history, err)
		}
	}
	keika := os.Getenv("KEIKA")
	if keika == "" {
		keika = "keika"
	}
	for _, file := range []string{"name_value", "name_index", "name_result", "body_value", "body_index", "body_result", "context", "setup_value", "setup_index", "fixture", "shared", "explicit"} {
		t.Run("compile rejection/"+file, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, keika, "check", "indexed_tables/type_errors/"+file+".km")
			command.Dir = consumer
			output, err := command.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || !strings.Contains(string(output), "cannot use") {
				t.Fatalf("compiler did not reject indexed callback types: %v %s", err, output)
			}
		})
	}
}

func TestWaitValueRealKinmokusei(t *testing.T) {
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
			t.Fatalf("waitForValue integration: %v %s", err, output)
		}
		return exit.ExitCode(), string(output)
	}
	for _, reporter := range []string{"text", "json", "junit"} {
		t.Run(reporter, func(t *testing.T) {
			historyPath := filepath.Join(t.TempDir(), "history.json")
			code, output := invoke("--reporter", reporter, "--save-failures", historyPath, "wait_value/passing.test.km")
			if code != 0 || !strings.Contains(output, "typed waitForValue") || !strings.Contains(output, "wait-value-skip-cleanup") || !strings.Contains(output, "wait value resource unavailable") || !strings.Contains(output, "wait-value-passing-later") || strings.Contains(output, "unreachable-") || !strings.Contains(output, "6 passed, 0 failed, 3 skipped, 0 todo") {
				t.Fatal(code, output)
			}
			history, err := readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 0 {
				t.Fatal("runtime skips entered failure history", history, err)
			}
			code, output = invoke("--reporter", reporter, "--save-failures", historyPath, "wait_value/failures.test.km")
			if code != 1 || !strings.Contains(output, "wait-value-afterEach") || !strings.Contains(output, "wait-value-cleanup") || !strings.Contains(output, "wait-value-failing-later") || !strings.Contains(output, "read is unavailable") || !strings.Contains(output, "predicate is unavailable") {
				t.Fatal(code, output)
			}
			history, err = readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 1 || history.Files[0].WholeFile || len(history.Files[0].Suites) != 1 || history.Files[0].Suites[0].WholeSuite || len(history.Files[0].Suites[0].Cases) != 9 {
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
						phase := "body"
						if result.Name == "skip cleanup failure" {
							phase = "cleanup"
						}
						if len(result.Failures) != 1 || result.Failures[0].Phase != phase {
							t.Fatal(result)
						}
						location := result.Failures[0].Location
						switch result.Name {
						case "explicit predicate timeout":
							if location.File != "domain-check.km" || location.Line != 42 || location.Source != "explicit" {
								t.Fatal(result)
							}
						case "skip cleanup failure":
							if location.File != "cleanup-check.km" || location.Line != 9 || location.Source != "explicit" {
								t.Fatal(result)
							}
						default:
							if !strings.HasSuffix(filepath.ToSlash(location.File), "/generated.go") || location.Line <= 0 || location.Source != "go" {
								t.Fatal(result)
							}
						}
					}
				}
				if failed != 9 {
					t.Fatal(failed, output)
				}
			} else if reporter == "junit" {
				var doc struct {
					Tests    int `xml:"tests,attr"`
					Failures int `xml:"failures,attr"`
				}
				if xml.Unmarshal([]byte(output), &doc) != nil || doc.Tests != 10 || doc.Failures != 9 {
					t.Fatal(output)
				}
			}
			code, output = invoke("--reporter", reporter, "--rerun-failed", historyPath, "--save-failures", historyPath, "--filter", "joined skip remains a failure", "wait_value")
			if code != 1 || !strings.Contains(output, "joined skip remains a failure") || strings.Contains(output, "wait-value-cleanup") || strings.Contains(output, "wait-value-failing-later") || strings.Contains(output, "unreachable-") {
				t.Fatal(code, output)
			}
			history, err = readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 1 || len(history.Files[0].Suites) != 1 || fmt.Sprint(history.Files[0].Suites[0].Cases) != "[joined skip remains a failure]" {
				t.Fatal(history, err)
			}
		})
	}
	code, output := invoke("--bail", "wait_value/passing.test.km")
	if code != 0 || !strings.Contains(output, "wait-value-passing-later") || !strings.Contains(output, "6 passed, 0 failed, 3 skipped") || strings.Contains(output, "unreachable-") {
		t.Fatal("skip triggered bail or retried callbacks", code, output)
	}
	code, output = invoke("--bail", "wait_value/failures.test.km")
	if code != 1 || !strings.Contains(output, "wait-value-cleanup") || strings.Contains(output, "wait-value-failing-later") || !strings.Contains(output, "0 passed, 1 failed, 9 skipped") {
		t.Fatal(code, output)
	}
	for _, fixture := range []struct{ file, prefix, message string }{
		{"read_panic", "wait-read-panic", "reflect: call of reflect.Value.Index on int Value"},
		{"predicate_panic", "wait-predicate-panic", "reflect: call of reflect.Value.Index on int Value"},
		{"assertion_panic", "wait-assertion-panic", "reflect: call of reflect.Value.Index on int Value"},
	} {
		historyPath := filepath.Join(t.TempDir(), "panic.json")
		code, output := invoke("--save-failures", historyPath, "wait_value/"+fixture.file+".test.km")
		if code != 1 || !strings.Contains(output, fixture.message) || strings.Contains(output, "unreachable-") {
			t.Fatal(code, output)
		}
		last := -1
		for _, phase := range []string{"afterEach", "cleanup", "afterAll"} {
			index := strings.Index(output, fixture.prefix+"-"+phase)
			if index <= last {
				t.Fatal("native waitForValue panic cleanup order changed", output)
			}
			last = index
		}
		history, err := readFailureHistory(historyPath)
		if err != nil || len(history.Files) != 1 || !history.Files[0].WholeFile {
			t.Fatal(history, err)
		}
	}
	keika := os.Getenv("KEIKA")
	if keika == "" {
		keika = "keika"
	}
	for _, file := range []string{"read", "input", "result", "width", "nominal", "read_callback", "predicate_callback", "timeout", "interval", "explicit"} {
		t.Run("compile rejection/"+file, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, keika, "check", "wait_value/type_errors/"+file+".km")
			command.Dir = consumer
			output, err := command.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || !strings.Contains(string(output), "cannot use") {
				t.Fatalf("compiler did not reject mismatched waitForValue types: %v %s", err, output)
			}
		})
	}
}

func TestMockThrownPredicateMatchersRealKinmokusei(t *testing.T) {
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
			t.Fatalf("mock thrown predicate integration: %v %s", err, output)
		}
		return exit.ExitCode(), string(output)
	}
	for _, reporter := range []string{"text", "json", "junit"} {
		t.Run(reporter, func(t *testing.T) {
			code, output := invoke("--reporter", reporter, "mock_thrown_predicate_matchers/passing.test.km")
			if code != 0 || !strings.Contains(output, "typed mock thrown predicate matchers") || !strings.Contains(output, "throw-predicate-skip-cleanup") || !strings.Contains(output, "throw predicate resource unavailable") || !strings.Contains(output, "throw-predicate-passing-later") || strings.Contains(output, "unreachable-") || !strings.Contains(output, "7 passed, 0 failed, 3 skipped, 0 todo") {
				t.Fatal(code, output)
			}
			historyPath := filepath.Join(t.TempDir(), "history.json")
			code, output = invoke("--reporter", reporter, "--save-failures", historyPath, "mock_thrown_predicate_matchers/failures.test.km")
			if code != 1 || !strings.Contains(output, "throw-predicate-afterEach") || !strings.Contains(output, "throw-predicate-cleanup") || !strings.Contains(output, "throw-predicate-failing-later") || !strings.Contains(output, "predicate callback failed") || !strings.Contains(output, "domain predicate failed") {
				t.Fatal(code, output)
			}
			history, err := readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 1 || history.Files[0].WholeFile || len(history.Files[0].Suites) != 1 || history.Files[0].Suites[0].WholeSuite || len(history.Files[0].Suites[0].Cases) != 22 {
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
				if failed != 22 {
					t.Fatal(failed, output)
				}
			} else if reporter == "junit" {
				var doc struct {
					Tests    int `xml:"tests,attr"`
					Failures int `xml:"failures,attr"`
				}
				if xml.Unmarshal([]byte(output), &doc) != nil || doc.Tests != 23 || doc.Failures != 22 {
					t.Fatal(output)
				}
			}
			code, output = invoke("--reporter", reporter, "--rerun-failed", historyPath, "--save-failures", historyPath, "--filter", "negated last mismatch", "mock_thrown_predicate_matchers")
			if code != 1 || !strings.Contains(output, "negated last mismatch") || strings.Contains(output, "throw-predicate-cleanup") || strings.Contains(output, "throw-predicate-failing-later") || strings.Contains(output, "unreachable-") {
				t.Fatal(code, output)
			}
			history, err = readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 1 || len(history.Files[0].Suites) != 1 || fmt.Sprint(history.Files[0].Suites[0].Cases) != "[negated last mismatch]" {
				t.Fatal(history, err)
			}
		})
	}
	code, output := invoke("--bail", "mock_thrown_predicate_matchers/failures.test.km")
	if code != 1 || !strings.Contains(output, "throw-predicate-cleanup") || strings.Contains(output, "throw-predicate-failing-later") || !strings.Contains(output, "0 passed, 1 failed, 22 skipped") {
		t.Fatal(code, output)
	}
	for _, fixture := range []struct{ file, prefix, message string }{
		{"panic", "throw-predicate-panic", "reflect: call of reflect.Value.Index on int Value"},
		{"source_panic", "throw-predicate-source", "reflect: call of reflect.Value.Index on int Value"},
	} {
		historyPath := filepath.Join(t.TempDir(), "panic.json")
		code, output := invoke("--save-failures", historyPath, "mock_thrown_predicate_matchers/"+fixture.file+".test.km")
		if code != 1 || !strings.Contains(output, fixture.message) || strings.Contains(output, "unreachable-") {
			t.Fatal(code, output)
		}
		last := -1
		for _, phase := range []string{"afterEach", "cleanup", "afterAll"} {
			index := strings.Index(output, fixture.prefix+"-"+phase)
			if index <= last {
				t.Fatal("native mock thrown predicate panic cleanup order changed", output)
			}
			last = index
		}
		history, err := readFailureHistory(historyPath)
		if err != nil || len(history.Files) != 1 || !history.Files[0].WholeFile {
			t.Fatal(history, err)
		}
	}
	keika := os.Getenv("KEIKA")
	if keika == "" {
		keika = "keika"
	}
	for _, file := range []string{"input", "result", "callback", "constructor", "index", "source"} {
		t.Run("compile rejection/"+file, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, keika, "check", "mock_thrown_predicate_matchers/type_errors/"+file+".km")
			command.Dir = consumer
			output, err := command.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || !strings.Contains(string(output), "cannot use") {
				t.Fatalf("compiler did not reject mismatched mock thrown predicate types: %v %s", err, output)
			}
		})
	}
}

func TestMockReturnPredicateMatchersRealKinmokusei(t *testing.T) {
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
			t.Fatalf("mock return predicate integration: %v %s", err, output)
		}
		return exit.ExitCode(), string(output)
	}
	for _, reporter := range []string{"text", "json", "junit"} {
		t.Run(reporter, func(t *testing.T) {
			code, output := invoke("--reporter", reporter, "mock_return_predicate_matchers/passing.test.km")
			if code != 0 || !strings.Contains(output, "typed mock return predicate matchers") || !strings.Contains(output, "return-predicate-skip-cleanup") || !strings.Contains(output, "return predicate resource unavailable") || !strings.Contains(output, "return-predicate-passing-later") || strings.Contains(output, "unreachable-") || !strings.Contains(output, "7 passed, 0 failed, 3 skipped, 0 todo") {
				t.Fatal(code, output)
			}
			historyPath := filepath.Join(t.TempDir(), "history.json")
			code, output = invoke("--reporter", reporter, "--save-failures", historyPath, "mock_return_predicate_matchers/failures.test.km")
			if code != 1 || !strings.Contains(output, "return-predicate-afterEach") || !strings.Contains(output, "return-predicate-cleanup") || !strings.Contains(output, "return-predicate-failing-later") || !strings.Contains(output, "predicate callback failed") || !strings.Contains(output, "domain predicate failed") {
				t.Fatal(code, output)
			}
			history, err := readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 1 || history.Files[0].WholeFile || len(history.Files[0].Suites) != 1 || history.Files[0].Suites[0].WholeSuite || len(history.Files[0].Suites[0].Cases) != 22 {
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
				if failed != 22 {
					t.Fatal(failed, output)
				}
			} else if reporter == "junit" {
				var doc struct {
					Tests    int `xml:"tests,attr"`
					Failures int `xml:"failures,attr"`
				}
				if xml.Unmarshal([]byte(output), &doc) != nil || doc.Tests != 23 || doc.Failures != 22 {
					t.Fatal(output)
				}
			}
			code, output = invoke("--reporter", reporter, "--rerun-failed", historyPath, "--save-failures", historyPath, "--filter", "negated last mismatch", "mock_return_predicate_matchers")
			if code != 1 || !strings.Contains(output, "negated last mismatch") || strings.Contains(output, "return-predicate-cleanup") || strings.Contains(output, "return-predicate-failing-later") || strings.Contains(output, "unreachable-") {
				t.Fatal(code, output)
			}
			history, err = readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 1 || len(history.Files[0].Suites) != 1 || fmt.Sprint(history.Files[0].Suites[0].Cases) != "[negated last mismatch]" {
				t.Fatal(history, err)
			}
		})
	}
	code, output := invoke("--bail", "mock_return_predicate_matchers/failures.test.km")
	if code != 1 || !strings.Contains(output, "return-predicate-cleanup") || strings.Contains(output, "return-predicate-failing-later") || !strings.Contains(output, "0 passed, 1 failed, 22 skipped") {
		t.Fatal(code, output)
	}
	for _, fixture := range []struct{ file, prefix, message string }{
		{"panic", "return-predicate-panic", "reflect: call of reflect.Value.Index on int Value"},
		{"source_panic", "return-predicate-source", "reflect: call of reflect.Value.Index on int Value"},
	} {
		historyPath := filepath.Join(t.TempDir(), "panic.json")
		code, output := invoke("--save-failures", historyPath, "mock_return_predicate_matchers/"+fixture.file+".test.km")
		if code != 1 || !strings.Contains(output, fixture.message) || strings.Contains(output, "unreachable-") {
			t.Fatal(code, output)
		}
		last := -1
		for _, phase := range []string{"afterEach", "cleanup", "afterAll"} {
			index := strings.Index(output, fixture.prefix+"-"+phase)
			if index <= last {
				t.Fatal("native mock return predicate panic cleanup order changed", output)
			}
			last = index
		}
		history, err := readFailureHistory(historyPath)
		if err != nil || len(history.Files) != 1 || !history.Files[0].WholeFile {
			t.Fatal(history, err)
		}
	}
	keika := os.Getenv("KEIKA")
	if keika == "" {
		keika = "keika"
	}
	for _, file := range []string{"input", "result", "nominal", "width", "callback", "constructor", "index"} {
		t.Run("compile rejection/"+file, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, keika, "check", "mock_return_predicate_matchers/type_errors/"+file+".km")
			command.Dir = consumer
			output, err := command.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || !strings.Contains(string(output), "cannot use") {
				t.Fatalf("compiler did not reject mismatched mock return predicate types: %v %s", err, output)
			}
		})
	}
}

func TestMockPredicateMatchersRealKinmokusei(t *testing.T) {
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
			t.Fatalf("mock predicate integration: %v %s", err, output)
		}
		return exit.ExitCode(), string(output)
	}
	for _, reporter := range []string{"text", "json", "junit"} {
		t.Run(reporter, func(t *testing.T) {
			code, output := invoke("--reporter", reporter, "mock_predicate_matchers/passing.test.km")
			if code != 0 || !strings.Contains(output, "typed mock predicate matchers") || !strings.Contains(output, "mock-predicate-skip-cleanup") || !strings.Contains(output, "mock predicate resource unavailable") || !strings.Contains(output, "mock-predicate-passing-later") || strings.Contains(output, "unreachable-") || !strings.Contains(output, "6 passed, 0 failed, 3 skipped, 0 todo") {
				t.Fatal(code, output)
			}
			historyPath := filepath.Join(t.TempDir(), "history.json")
			code, output = invoke("--reporter", reporter, "--save-failures", historyPath, "mock_predicate_matchers/failures.test.km")
			if code != 1 || !strings.Contains(output, "mock-predicate-afterEach") || !strings.Contains(output, "mock-predicate-cleanup") || !strings.Contains(output, "mock-predicate-failing-later") || !strings.Contains(output, "predicate callback failed") || !strings.Contains(output, "domain predicate failed") {
				t.Fatal(code, output)
			}
			history, err := readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 1 || history.Files[0].WholeFile || len(history.Files[0].Suites) != 1 || history.Files[0].Suites[0].WholeSuite || len(history.Files[0].Suites[0].Cases) != 20 {
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
				if failed != 20 {
					t.Fatal(failed, output)
				}
			} else if reporter == "junit" {
				var doc struct {
					Tests    int `xml:"tests,attr"`
					Failures int `xml:"failures,attr"`
				}
				if xml.Unmarshal([]byte(output), &doc) != nil || doc.Tests != 21 || doc.Failures != 20 {
					t.Fatal(output)
				}
			}
			code, output = invoke("--reporter", reporter, "--rerun-failed", historyPath, "--save-failures", historyPath, "--filter", "negated last mismatch", "mock_predicate_matchers")
			if code != 1 || !strings.Contains(output, "negated last mismatch") || strings.Contains(output, "mock-predicate-cleanup") || strings.Contains(output, "mock-predicate-failing-later") || strings.Contains(output, "unreachable-") {
				t.Fatal(code, output)
			}
			history, err = readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 1 || len(history.Files[0].Suites) != 1 || fmt.Sprint(history.Files[0].Suites[0].Cases) != "[negated last mismatch]" {
				t.Fatal(history, err)
			}
		})
	}
	code, output := invoke("--bail", "mock_predicate_matchers/failures.test.km")
	if code != 1 || !strings.Contains(output, "mock-predicate-cleanup") || strings.Contains(output, "mock-predicate-failing-later") || !strings.Contains(output, "0 passed, 1 failed, 20 skipped") {
		t.Fatal(code, output)
	}
	for _, fixture := range []struct{ file, prefix, message string }{
		{"panic", "mock-predicate-panic", "reflect: call of reflect.Value.Index on int Value"},
		{"source_panic", "mock-predicate-source", "reflect: call of reflect.Value.Index on int Value"},
	} {
		historyPath := filepath.Join(t.TempDir(), "panic.json")
		code, output := invoke("--save-failures", historyPath, "mock_predicate_matchers/"+fixture.file+".test.km")
		if code != 1 || !strings.Contains(output, fixture.message) || strings.Contains(output, "unreachable-") {
			t.Fatal(code, output)
		}
		last := -1
		for _, phase := range []string{"afterEach", "cleanup", "afterAll"} {
			index := strings.Index(output, fixture.prefix+"-"+phase)
			if index <= last {
				t.Fatal("native mock predicate panic cleanup order changed", output)
			}
			last = index
		}
		history, err := readFailureHistory(historyPath)
		if err != nil || len(history.Files) != 1 || !history.Files[0].WholeFile {
			t.Fatal(history, err)
		}
	}
	keika := os.Getenv("KEIKA")
	if keika == "" {
		keika = "keika"
	}
	for _, file := range []string{"input", "result", "nominal", "width", "callback", "constructor", "index"} {
		t.Run("compile rejection/"+file, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, keika, "check", "mock_predicate_matchers/type_errors/"+file+".km")
			command.Dir = consumer
			output, err := command.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || !strings.Contains(string(output), "cannot use") {
				t.Fatalf("compiler did not reject mismatched mock predicate types: %v %s", err, output)
			}
		})
	}
}

func TestMapPredicateMatchersRealKinmokusei(t *testing.T) {
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
			t.Fatalf("map predicate integration: %v %s", err, output)
		}
		return exit.ExitCode(), string(output)
	}
	for _, reporter := range []string{"text", "json", "junit"} {
		t.Run(reporter, func(t *testing.T) {
			code, output := invoke("--reporter", reporter, "map_predicate_matchers/passing.test.km")
			if code != 0 || !strings.Contains(output, "typed map predicate matchers") || !strings.Contains(output, "map-predicate-skip-cleanup") || !strings.Contains(output, "map predicate resource unavailable") || !strings.Contains(output, "map-predicate-passing-later") || strings.Contains(output, "unreachable-") || !strings.Contains(output, "6 passed, 0 failed, 2 skipped, 0 todo") {
				t.Fatal(code, output)
			}
			historyPath := filepath.Join(t.TempDir(), "history.json")
			code, output = invoke("--reporter", reporter, "--save-failures", historyPath, "map_predicate_matchers/failures.test.km")
			if code != 1 || !strings.Contains(output, "map-predicate-afterEach") || !strings.Contains(output, "map-predicate-cleanup") || !strings.Contains(output, "map-predicate-failing-later") || !strings.Contains(output, "predicate callback failed") || !strings.Contains(output, "domain predicate failed") {
				t.Fatal(code, output)
			}
			history, err := readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 1 || history.Files[0].WholeFile || len(history.Files[0].Suites) != 1 || history.Files[0].Suites[0].WholeSuite || len(history.Files[0].Suites[0].Cases) != 7 {
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
				if failed != 7 {
					t.Fatal(failed, output)
				}
			} else if reporter == "junit" {
				var doc struct {
					Tests    int `xml:"tests,attr"`
					Failures int `xml:"failures,attr"`
				}
				if xml.Unmarshal([]byte(output), &doc) != nil || doc.Tests != 8 || doc.Failures != 7 {
					t.Fatal(output)
				}
			}
			code, output = invoke("--reporter", reporter, "--rerun-failed", historyPath, "--save-failures", historyPath, "--filter", "negated mismatch", "map_predicate_matchers")
			if code != 1 || !strings.Contains(output, "negated mismatch") || strings.Contains(output, "map-predicate-cleanup") || strings.Contains(output, "map-predicate-failing-later") || strings.Contains(output, "unreachable-") {
				t.Fatal(code, output)
			}
			history, err = readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 1 || len(history.Files[0].Suites) != 1 || fmt.Sprint(history.Files[0].Suites[0].Cases) != "[negated mismatch]" {
				t.Fatal(history, err)
			}
		})
	}
	code, output := invoke("--bail", "map_predicate_matchers/failures.test.km")
	if code != 1 || !strings.Contains(output, "map-predicate-cleanup") || strings.Contains(output, "map-predicate-failing-later") || !strings.Contains(output, "0 passed, 1 failed, 7 skipped") {
		t.Fatal(code, output)
	}
	for _, fixture := range []struct{ file, prefix, message string }{
		{"panic", "map-predicate-panic", "reflect: call of reflect.Value.Index on int Value"},
		{"key_panic", "map-predicate-key-panic", "unhashable type"},
	} {
		historyPath := filepath.Join(t.TempDir(), "panic.json")
		code, output := invoke("--save-failures", historyPath, "map_predicate_matchers/"+fixture.file+".test.km")
		if code != 1 || !strings.Contains(output, fixture.message) || strings.Contains(output, "unreachable-") {
			t.Fatal(code, output)
		}
		last := -1
		for _, phase := range []string{"afterEach", "cleanup", "afterAll"} {
			index := strings.Index(output, fixture.prefix+"-"+phase)
			if index <= last {
				t.Fatal("native map predicate panic cleanup order changed", output)
			}
			last = index
		}
		history, err := readFailureHistory(historyPath)
		if err != nil || len(history.Files) != 1 || !history.Files[0].WholeFile {
			t.Fatal(history, err)
		}
	}
	keika := os.Getenv("KEIKA")
	if keika == "" {
		keika = "keika"
	}
	for _, file := range []string{"key", "input", "result", "nominal", "width", "callback", "constructor"} {
		t.Run("compile rejection/"+file, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, keika, "check", "map_predicate_matchers/type_errors/"+file+".km")
			command.Dir = consumer
			output, err := command.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || !strings.Contains(string(output), "cannot use") {
				t.Fatalf("compiler did not reject mismatched map predicate types: %v %s", err, output)
			}
		})
	}
}

func TestMapCollectionPredicateRealKinmokusei(t *testing.T) {
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
			t.Fatalf("map collection integration: %v %s", err, output)
		}
		return exit.ExitCode(), string(output)
	}
	for _, reporter := range []string{"text", "json", "junit"} {
		t.Run(reporter, func(t *testing.T) {
			code, output := invoke("--reporter", reporter, "map_collection_predicate/passing.test.km")
			if code != 0 || !strings.Contains(output, "typed map collection predicates") || !strings.Contains(output, "map-collection-skip-cleanup") || !strings.Contains(output, "map collection resource unavailable") || !strings.Contains(output, "map-collection-passing-later") || strings.Contains(output, "unreachable-") || !strings.Contains(output, "6 passed, 0 failed, 2 skipped, 0 todo") {
				t.Fatal(code, output)
			}
			historyPath := filepath.Join(t.TempDir(), "history.json")
			code, output = invoke("--reporter", reporter, "--save-failures", historyPath, "map_collection_predicate/failures.test.km")
			if code != 1 || !strings.Contains(output, "map-collection-afterEach") || !strings.Contains(output, "map-collection-cleanup") || !strings.Contains(output, "map-collection-failing-later") || !strings.Contains(output, "predicate callback failed") || !strings.Contains(output, "domain predicate failed") {
				t.Fatal(code, output)
			}
			history, err := readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 1 || history.Files[0].WholeFile || len(history.Files[0].Suites) != 1 || history.Files[0].Suites[0].WholeSuite || fmt.Sprint(history.Files[0].Suites[0].Cases) != "[any mismatch all mismatch negated any mismatch negated all mismatch nil any predicate negated nil any predicate nil all predicate negated nil all predicate thrown predicate explicit callback location]" {
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
			code, output = invoke("--reporter", reporter, "--rerun-failed", historyPath, "--save-failures", historyPath, "--filter", "negated all mismatch", "map_collection_predicate")
			if code != 1 || !strings.Contains(output, "negated all mismatch") || strings.Contains(output, "map-collection-cleanup") || strings.Contains(output, "map-collection-failing-later") || strings.Contains(output, "unreachable-") {
				t.Fatal(code, output)
			}
			history, err = readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 1 || len(history.Files[0].Suites) != 1 || fmt.Sprint(history.Files[0].Suites[0].Cases) != "[negated all mismatch]" {
				t.Fatal(history, err)
			}
		})
	}
	code, output := invoke("--bail", "map_collection_predicate/failures.test.km")
	if code != 1 || !strings.Contains(output, "map-collection-cleanup") || strings.Contains(output, "map-collection-failing-later") || !strings.Contains(output, "0 passed, 1 failed, 10 skipped") {
		t.Fatal(code, output)
	}
	historyPath := filepath.Join(t.TempDir(), "panic.json")
	code, output = invoke("--save-failures", historyPath, "map_collection_predicate/panic.test.km")
	if code != 1 || !strings.Contains(output, "reflect: call of reflect.Value.Index on int Value") || strings.Contains(output, "unreachable-") {
		t.Fatal(code, output)
	}
	last := -1
	for _, marker := range []string{"map-collection-panic-afterEach", "map-collection-panic-cleanup", "map-collection-panic-afterAll"} {
		index := strings.Index(output, marker)
		if index <= last {
			t.Fatal("native map collection panic cleanup order changed", output)
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
	for _, file := range []string{"key", "key_width", "value", "value_width", "nominal_key", "nominal_value", "result", "arity", "callback", "binding", "constructor"} {
		t.Run("compile rejection/"+file, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, keika, "check", "map_collection_predicate/type_errors/"+file+".km")
			command.Dir = consumer
			output, err := command.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || !strings.Contains(string(output), "cannot use") {
				t.Fatalf("compiler did not reject mismatched map collection predicate types: %v %s", err, output)
			}
		})
	}
}

func TestSlicePredicateMatchersRealKinmokusei(t *testing.T) {
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
			t.Fatalf("slice predicate integration: %v %s", err, output)
		}
		return exit.ExitCode(), string(output)
	}
	for _, reporter := range []string{"text", "json", "junit"} {
		t.Run(reporter, func(t *testing.T) {
			code, output := invoke("--reporter", reporter, "slice_predicate_matchers/passing.test.km")
			if code != 0 || !strings.Contains(output, "typed slice predicate matchers") || !strings.Contains(output, "slice-predicate-skip-cleanup") || !strings.Contains(output, "slice predicate resource unavailable") || !strings.Contains(output, "slice-predicate-passing-later") || strings.Contains(output, "unreachable-") || !strings.Contains(output, "5 passed, 0 failed, 2 skipped, 0 todo") {
				t.Fatal(code, output)
			}
			historyPath := filepath.Join(t.TempDir(), "history.json")
			code, output = invoke("--reporter", reporter, "--save-failures", historyPath, "slice_predicate_matchers/failures.test.km")
			if code != 1 || !strings.Contains(output, "slice-predicate-afterEach") || !strings.Contains(output, "slice-predicate-cleanup") || !strings.Contains(output, "slice-predicate-failing-later") || !strings.Contains(output, "predicate callback failed") || !strings.Contains(output, "domain predicate failed") {
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
			code, output = invoke("--reporter", reporter, "--rerun-failed", historyPath, "--save-failures", historyPath, "--filter", "negated all mismatch", "slice_predicate_matchers")
			if code != 1 || !strings.Contains(output, "negated all mismatch") || strings.Contains(output, "slice-predicate-cleanup") || strings.Contains(output, "slice-predicate-failing-later") || strings.Contains(output, "unreachable-") {
				t.Fatal(code, output)
			}
			history, err = readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 1 || len(history.Files[0].Suites) != 1 || fmt.Sprint(history.Files[0].Suites[0].Cases) != "[negated all mismatch]" {
				t.Fatal(history, err)
			}
		})
	}
	code, output := invoke("--bail", "slice_predicate_matchers/failures.test.km")
	if code != 1 || !strings.Contains(output, "slice-predicate-cleanup") || strings.Contains(output, "slice-predicate-failing-later") || !strings.Contains(output, "0 passed, 1 failed, 10 skipped") {
		t.Fatal(code, output)
	}
	historyPath := filepath.Join(t.TempDir(), "panic.json")
	code, output = invoke("--save-failures", historyPath, "slice_predicate_matchers/panic.test.km")
	if code != 1 || !strings.Contains(output, "reflect: call of reflect.Value.Index on int Value") || strings.Contains(output, "unreachable-") {
		t.Fatal(code, output)
	}
	last := -1
	for _, marker := range []string{"slice-predicate-panic-afterEach", "slice-predicate-panic-cleanup", "slice-predicate-panic-afterAll"} {
		index := strings.Index(output, marker)
		if index <= last {
			t.Fatal("native slice predicate panic cleanup order changed", output)
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
	for _, file := range []string{"input", "result", "nominal", "width", "callback", "constructor"} {
		t.Run("compile rejection/"+file, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, keika, "check", "slice_predicate_matchers/type_errors/"+file+".km")
			command.Dir = consumer
			output, err := command.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || !strings.Contains(string(output), "cannot use") {
				t.Fatalf("compiler did not reject mismatched slice predicate types: %v %s", err, output)
			}
		})
	}
}

func TestPredicateMatchersRealKinmokusei(t *testing.T) {
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
			t.Fatalf("predicate integration: %v %s", err, output)
		}
		return exit.ExitCode(), string(output)
	}
	for _, reporter := range []string{"text", "json", "junit"} {
		t.Run(reporter, func(t *testing.T) {
			code, output := invoke("--reporter", reporter, "predicate_matchers/passing.test.km")
			if code != 0 || !strings.Contains(output, "typed predicate matchers") || !strings.Contains(output, "predicate-skip-cleanup") || !strings.Contains(output, "predicate resource unavailable") || !strings.Contains(output, "predicate-passing-later") || strings.Contains(output, "unreachable-") {
				t.Fatal(code, output)
			}
			historyPath := filepath.Join(t.TempDir(), "history.json")
			code, output = invoke("--reporter", reporter, "--save-failures", historyPath, "predicate_matchers/failures.test.km")
			if code != 1 || !strings.Contains(output, "predicate-afterEach") || !strings.Contains(output, "predicate-cleanup") || !strings.Contains(output, "predicate-failing-later") || !strings.Contains(output, "predicate callback failed") || !strings.Contains(output, "domain predicate failed") {
				t.Fatal(code, output)
			}
			history, err := readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 1 || history.Files[0].WholeFile || len(history.Files[0].Suites) != 1 || history.Files[0].Suites[0].WholeSuite || len(history.Files[0].Suites[0].Cases) != 6 {
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
				if failed != 6 {
					t.Fatal(failed, output)
				}
			} else if reporter == "junit" {
				var doc struct {
					Tests    int `xml:"tests,attr"`
					Failures int `xml:"failures,attr"`
				}
				if xml.Unmarshal([]byte(output), &doc) != nil || doc.Tests != 7 || doc.Failures != 6 {
					t.Fatal(output)
				}
			}
			code, output = invoke("--reporter", reporter, "--rerun-failed", historyPath, "--save-failures", historyPath, "--filter", "negated mismatch", "predicate_matchers")
			if code != 1 || !strings.Contains(output, "negated mismatch") || strings.Contains(output, "predicate-cleanup") || strings.Contains(output, "predicate-failing-later") || strings.Contains(output, "unreachable-") {
				t.Fatal(code, output)
			}
			history, err = readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 1 || len(history.Files[0].Suites) != 1 || fmt.Sprint(history.Files[0].Suites[0].Cases) != "[negated mismatch]" {
				t.Fatal(history, err)
			}
		})
	}
	code, output := invoke("--bail", "predicate_matchers/failures.test.km")
	if code != 1 || !strings.Contains(output, "predicate-cleanup") || strings.Contains(output, "predicate-failing-later") || !strings.Contains(output, "0 passed, 1 failed, 6 skipped") {
		t.Fatal(code, output)
	}
	historyPath := filepath.Join(t.TempDir(), "panic.json")
	code, output = invoke("--save-failures", historyPath, "predicate_matchers/panic.test.km")
	if code != 1 || !strings.Contains(output, "reflect: call of reflect.Value.Index on int Value") || strings.Contains(output, "unreachable-") {
		t.Fatal(code, output)
	}
	last := -1
	for _, marker := range []string{"predicate-panic-afterEach", "predicate-panic-cleanup", "predicate-panic-afterAll"} {
		index := strings.Index(output, marker)
		if index <= last {
			t.Fatal("native predicate panic cleanup order changed", output)
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
	for _, file := range []string{"input", "result", "nominal", "width", "callback"} {
		t.Run("compile rejection/"+file, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, keika, "check", "predicate_matchers/type_errors/"+file+".km")
			command.Dir = consumer
			output, err := command.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || !strings.Contains(string(output), "cannot use") {
				t.Fatalf("compiler did not reject mismatched predicate types: %v %s", err, output)
			}
		})
	}
}

func TestMapMatchersRealKinmokusei(t *testing.T) {
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
			t.Fatalf("map matcher integration: %v %s", err, output)
		}
		return exit.ExitCode(), string(output)
	}
	for _, reporter := range []string{"text", "json", "junit"} {
		t.Run(reporter, func(t *testing.T) {
			code, output := invoke("--reporter", reporter, "map_matchers/passing.test.km")
			if code != 0 || !strings.Contains(output, "typed map matchers") {
				t.Fatal(code, output)
			}
			historyPath := filepath.Join(t.TempDir(), "history.json")
			code, output = invoke("--reporter", reporter, "--save-failures", historyPath, "map_matchers/failures.test.km")
			if code != 1 || !strings.Contains(output, "map-matcher-afterEach") || !strings.Contains(output, "map-matcher-cleanup") || !strings.Contains(output, "map-matcher-later-case") || !strings.Contains(output, "key is missing") {
				t.Fatal(code, output)
			}
			history, err := readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 1 || history.Files[0].WholeFile || len(history.Files[0].Suites) != 1 || history.Files[0].Suites[0].WholeSuite || len(history.Files[0].Suites[0].Cases) != 9 {
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
						if len(result.Failures) != 1 || result.Failures[0].Phase != "body" || !strings.HasSuffix(filepath.ToSlash(result.Failures[0].Location.File), "/generated.go") || result.Failures[0].Location.Line <= 0 {
							t.Fatal(result)
						}
					}
				}
				if failed != 9 {
					t.Fatal(failed, output)
				}
			} else if reporter == "junit" {
				var doc struct {
					Tests    int `xml:"tests,attr"`
					Failures int `xml:"failures,attr"`
				}
				if xml.Unmarshal([]byte(output), &doc) != nil || doc.Tests != 10 || doc.Failures != 9 {
					t.Fatal(output)
				}
			}
			code, output = invoke("--reporter", reporter, "--rerun-failed", historyPath, "--save-failures", historyPath, "--filter", "wrong value", "map_matchers")
			if code != 1 || !strings.Contains(output, "wrong value") || strings.Contains(output, "map-matcher-cleanup") || strings.Contains(output, "map-matcher-later-case") || strings.Contains(output, "unreachable-") {
				t.Fatal(code, output)
			}
			history, err = readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 1 || len(history.Files[0].Suites) != 1 || fmt.Sprint(history.Files[0].Suites[0].Cases) != "[wrong value]" {
				t.Fatal(history, err)
			}
		})
	}
	code, output := invoke("--bail", "map_matchers/failures.test.km")
	if code != 1 || !strings.Contains(output, "map-matcher-cleanup") || strings.Contains(output, "map-matcher-later-case") || !strings.Contains(output, "0 passed, 1 failed, 9 skipped") {
		t.Fatal(code, output)
	}
	historyPath := filepath.Join(t.TempDir(), "panic.json")
	code, output = invoke("--save-failures", historyPath, "map_matchers/panic.test.km")
	if code != 1 || !strings.Contains(output, "hash of unhashable type net.IP") || strings.Contains(output, "unreachable-") {
		t.Fatal(code, output)
	}
	last := -1
	for _, marker := range []string{"map-panic-afterEach", "map-panic-cleanup", "map-panic-afterAll"} {
		index := strings.Index(output, marker)
		if index <= last {
			t.Fatal("native map panic cleanup order changed", output)
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
	for _, file := range []string{"key", "value", "nominal", "width", "constraint"} {
		t.Run("compile rejection/"+file, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, keika, "check", "map_matchers/type_errors/"+file+".km")
			command.Dir = consumer
			output, err := command.CombinedOutput()
			var exit *exec.ExitError
			message := string(output)
			if !errors.As(err, &exit) || (file == "constraint" && !strings.Contains(message, "does not satisfy K type parameter constraint")) || (file != "constraint" && !strings.Contains(message, "cannot use")) {
				t.Fatalf("compiler did not reject mismatched map types: %v %s", err, output)
			}
		})
	}
}

func TestFunctionMatchersRealKinmokusei(t *testing.T) {
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
			t.Fatalf("function matcher integration: %v %s", err, output)
		}
		return exit.ExitCode(), string(output)
	}
	for _, reporter := range []string{"text", "json", "junit"} {
		t.Run(reporter, func(t *testing.T) {
			code, output := invoke("--reporter", reporter, "function_matchers/passing.test.km")
			if code != 0 || !strings.Contains(output, "typed function matchers") {
				t.Fatal(code, output)
			}
			historyPath := filepath.Join(t.TempDir(), "history.json")
			code, output = invoke("--reporter", reporter, "--save-failures", historyPath, "function_matchers/failures.test.km")
			if code != 1 || !strings.Contains(output, "function-matcher-afterEach") || !strings.Contains(output, "function-matcher-cleanup") || !strings.Contains(output, "function-matcher-later-case") || strings.Contains(output, "unreachable-function-body") {
				t.Fatal(code, output)
			}
			history, err := readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 1 || history.Files[0].WholeFile || len(history.Files[0].Suites) != 1 || history.Files[0].Suites[0].WholeSuite || len(history.Files[0].Suites[0].Cases) != 14 {
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
						if len(result.Failures) != 1 || result.Failures[0].Phase != "body" || !strings.HasSuffix(filepath.ToSlash(result.Failures[0].Location.File), "/generated.go") || result.Failures[0].Location.Line <= 0 {
							t.Fatal(result)
						}
					}
				}
				if failed != 14 {
					t.Fatal(failed, output)
				}
			} else if reporter == "junit" {
				var doc struct {
					Tests    int `xml:"tests,attr"`
					Failures int `xml:"failures,attr"`
				}
				if xml.Unmarshal([]byte(output), &doc) != nil || doc.Tests != 15 || doc.Failures != 14 {
					t.Fatal(output)
				}
			}
			code, output = invoke("--reporter", reporter, "--rerun-failed", historyPath, "--save-failures", historyPath, "--filter", "wrong type", "function_matchers")
			if code != 1 || !strings.Contains(output, "wrong type") || strings.Contains(output, "function-matcher-cleanup") || strings.Contains(output, "function-matcher-later-case") || strings.Contains(output, "unreachable-") {
				t.Fatal(code, output)
			}
			history, err = readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 1 || len(history.Files[0].Suites) != 1 || fmt.Sprint(history.Files[0].Suites[0].Cases) != "[wrong type]" {
				t.Fatal(history, err)
			}
		})
	}
	code, output := invoke("--bail", "function_matchers/failures.test.km")
	if code != 1 || !strings.Contains(output, "function-matcher-cleanup") || strings.Contains(output, "function-matcher-later-case") || !strings.Contains(output, "0 passed, 1 failed, 14 skipped") {
		t.Fatal(code, output)
	}
	historyPath := filepath.Join(t.TempDir(), "panic.json")
	code, output = invoke("--save-failures", historyPath, "function_matchers/panic.test.km")
	if code != 1 || !strings.Contains(output, "panic: reflect:") || strings.Contains(output, "unreachable-") {
		t.Fatal(code, output)
	}
	last := -1
	for _, marker := range []string{"function-panic-afterEach", "function-panic-cleanup", "function-panic-afterAll"} {
		index := strings.Index(output, marker)
		if index <= last {
			t.Fatal("panic cleanup order changed", output)
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
	for _, file := range []string{"body", "cause", "destination"} {
		t.Run("compile rejection/"+file, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, keika, "check", "function_matchers/type_errors/"+file+".km")
			command.Dir = consumer
			output, err := command.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || !strings.Contains(string(output), "cannot use") {
				t.Fatalf("compiler did not reject mismatched types: %v %s", err, output)
			}
		})
	}
}

func TestErrorMatchersRealKinmokusei(t *testing.T) {
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
			t.Fatalf("error matcher integration: %v %s", err, output)
		}
		return exit.ExitCode(), string(output)
	}
	for _, reporter := range []string{"text", "json", "junit"} {
		t.Run(reporter, func(t *testing.T) {
			code, output := invoke("--reporter", reporter, "error_matchers/passing.test.km")
			if code != 0 || !strings.Contains(output, "typed error matchers") {
				t.Fatal(code, output)
			}
			historyPath := filepath.Join(t.TempDir(), "history.json")
			code, output = invoke("--reporter", reporter, "--save-failures", historyPath, "error_matchers/failures.test.km")
			if code != 1 || !strings.Contains(output, "error-matcher-afterEach") || !strings.Contains(output, "error-matcher-cleanup") || !strings.Contains(output, "error-matcher-later-case") || !strings.Contains(output, "toMatchType requires an error type or interface") {
				t.Fatal(code, output)
			}
			history, err := readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 1 || history.Files[0].WholeFile || len(history.Files[0].Suites) != 1 || history.Files[0].Suites[0].WholeSuite || len(history.Files[0].Suites[0].Cases) != 9 {
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
						if len(result.Failures) != 1 || result.Failures[0].Phase != "body" || !strings.HasSuffix(filepath.ToSlash(result.Failures[0].Location.File), "/generated.go") || result.Failures[0].Location.Line <= 0 {
							t.Fatal(result)
						}
					}
				}
				if failed != 9 {
					t.Fatal(failed, output)
				}
			} else if reporter == "junit" {
				var doc struct {
					Tests    int `xml:"tests,attr"`
					Failures int `xml:"failures,attr"`
				}
				if xml.Unmarshal([]byte(output), &doc) != nil || doc.Tests != 10 || doc.Failures != 9 {
					t.Fatal(output)
				}
			}
			code, output = invoke("--reporter", reporter, "--rerun-failed", historyPath, "--save-failures", historyPath, "--filter", "wrong type", "error_matchers")
			if code != 1 || !strings.Contains(output, "wrong type") || strings.Contains(output, "error-matcher-cleanup") || strings.Contains(output, "error-matcher-later-case") {
				t.Fatal(code, output)
			}
			history, err = readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 1 || len(history.Files[0].Suites) != 1 || fmt.Sprint(history.Files[0].Suites[0].Cases) != "[wrong type]" {
				t.Fatal(history, err)
			}
		})
	}
	code, output := invoke("--bail", "error_matchers/failures.test.km")
	if code != 1 || !strings.Contains(output, "error-matcher-cleanup") || strings.Contains(output, "error-matcher-later-case") || !strings.Contains(output, "0 passed, 1 failed, 9 skipped") {
		t.Fatal(code, output)
	}
	keika := os.Getenv("KEIKA")
	if keika == "" {
		keika = "keika"
	}
	for _, file := range []string{"actual", "cause", "destination"} {
		t.Run("compile rejection/"+file, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, keika, "check", "error_matchers/type_errors/"+file+".km")
			command.Dir = consumer
			output, err := command.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || !strings.Contains(string(output), "cannot use") {
				t.Fatalf("type mismatch did not reach compiler checking: %v %s", err, output)
			}
		})
	}
}

func TestFluentMatchersRealKinmokusei(t *testing.T) {
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
			t.Fatalf("fluent integration: %v %s", err, output)
		}
		return exit.ExitCode(), string(output)
	}
	for _, reporter := range []string{"text", "json", "junit"} {
		t.Run(reporter, func(t *testing.T) {
			code, output := invoke("--reporter", reporter, "fluent/passing.test.km")
			if code != 0 || !strings.Contains(output, "fluent matchers") {
				t.Fatal(code, output)
			}
			code, output = invoke("--reporter", reporter, "fluent/types.test.km")
			if code != 0 || !strings.Contains(output, "native typed matchers") {
				t.Fatal(code, output)
			}
			historyPath := filepath.Join(t.TempDir(), "history.json")
			code, output = invoke("--reporter", reporter, "--save-failures", historyPath, "fluent/failures.test.km")
			if code != 1 || !strings.Contains(output, "fluent-afterEach") || !strings.Contains(output, "fluent-cleanup") || !strings.Contains(output, "fluent-later-case") || !strings.Contains(output, "toBe requires comparable values") {
				t.Fatal(code, output)
			}
			history, err := readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 1 || len(history.Files[0].Suites) != 1 || len(history.Files[0].Suites[0].Cases) != 17 {
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
						if len(result.Failures) != 1 || result.Failures[0].Phase != "body" || !strings.HasSuffix(filepath.ToSlash(result.Failures[0].Location.File), "/generated.go") || result.Failures[0].Location.Line <= 0 {
							t.Fatal(result)
						}
					}
				}
				if failed != 17 {
					t.Fatal(failed, output)
				}
			}
		})
	}
	code, output := invoke("--bail", "fluent/failures.test.km")
	if code != 1 || !strings.Contains(output, "fluent-cleanup") || strings.Contains(output, "fluent-later-case") || !strings.Contains(output, "0 passed, 1 failed, 17 skipped") {
		t.Fatal(code, output)
	}
	// These are native compiler checks, not erased runtime assertions. Keep
	// the invalid source outside *.test.km discovery.
	keika := os.Getenv("KEIKA")
	if keika == "" {
		keika = "keika"
	}
	for _, file := range []string{"element", "nominal", "width", "string", "length"} {
		t.Run("compile rejection/"+file, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, keika, "check", "fluent/type_errors/"+file+".km")
			command.Dir = consumer
			output, err := command.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || !strings.Contains(string(output), "cannot use") || strings.Contains(string(output), "cannot infer") {
				t.Fatalf("type mismatch did not reach compiler checking: %v %s", err, output)
			}
		})
	}
}

func TestRuntimeSkipRealKinmokusei(t *testing.T) {
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
			t.Fatalf("runtime skip integration: %v %s", err, output)
		}
		return exit.ExitCode(), string(output)
	}
	for _, reporter := range []string{"text", "json", "junit"} {
		t.Run(reporter, func(t *testing.T) {
			historyPath := filepath.Join(t.TempDir(), "failures.json")
			code, output := invoke("--exclude", "failures.test.km", "--jobs", "2", "--bail", "--save-failures", historyPath, "--reporter", reporter, "runtime_skip")
			if code != 0 || strings.Contains(output, "unreachable-") || !strings.Contains(output, "body-resource-cleanup") || !strings.Contains(output, "fixture-resource-cleanup") || !strings.Contains(output, "shared-resource-cleanup") || !strings.Contains(output, "conditional-later-case") || !strings.Contains(output, "hook-afterEach") {
				t.Fatal(code, output)
			}
			history, err := readFailureHistory(historyPath)
			if err != nil || len(history.Files) != 0 {
				t.Fatal(history, err)
			}
			if reporter == "json" {
				measured, shared := 0, 0
				for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
					var event struct {
						Type                     string
						Report                   SuiteReport
						NoMatch                  bool
						PassedFiles, FailedFiles int
					}
					if json.Unmarshal([]byte(line), &event) != nil {
						t.Fatal(output)
					}
					if event.Type == "summary" && (event.PassedFiles != 2 || event.FailedFiles != 0 || event.NoMatch) {
						t.Fatal(event)
					}
					if event.Type != "suite" {
						continue
					}
					for _, result := range event.Report.Summary.Results {
						if len(result.Failures) != 0 {
							t.Fatal(result)
						}
						if result.Status != "skip" {
							continue
						}
						if result.DurationNs > 0 {
							measured++
							if result.Message == "" {
								t.Fatal(result)
							}
						}
						if strings.HasPrefix(result.Name, "shared ") {
							shared++
							if result.Message != "database unavailable" || result.DurationNs != 0 {
								t.Fatal(result)
							}
						}
						if result.Name == "body skipped" && result.Message != "service <&> unavailable" {
							t.Fatal(result)
						}
					}
				}
				if measured != 4 || shared != 2 {
					t.Fatal(measured, shared, output)
				}
			} else if reporter == "junit" {
				var doc struct {
					Tests    int `xml:"tests,attr"`
					Failures int `xml:"failures,attr"`
					Skipped  int `xml:"skipped,attr"`
					Suites   []struct {
						Cases []struct {
							Name    string `xml:"name,attr"`
							Skipped *struct {
								Message string `xml:"message,attr"`
							} `xml:"skipped"`
						} `xml:"testcase"`
					} `xml:"testsuite"`
				}
				if xml.Unmarshal([]byte(output), &doc) != nil || doc.Tests != 10 || doc.Failures != 0 || doc.Skipped != 8 {
					t.Fatal(output)
				}
				found := false
				for _, suite := range doc.Suites {
					for _, result := range suite.Cases {
						if result.Name == "body skipped" {
							found = result.Skipped != nil && result.Skipped.Message == "service <&> unavailable"
						}
					}
				}
				if !found {
					t.Fatal("skip reason lost or XML damaged", output)
				}
			} else if !strings.Contains(output, "SKIP conditional tests / body skipped: service <&> unavailable") || !strings.Contains(output, "0 passed, 0 failed, 3 skipped, 1 todo") {
				t.Fatal(output)
			}
			// Runtime skips remain successful matches, including an all-skip
			// filter and a previous failure whose current execution skips.
			code, output = invoke("--filter", "body skipped", "--fail-on-todo", "--reporter", reporter, "runtime_skip/conditional.test.km")
			if code != 0 || strings.Contains(output, "unreachable-") || !strings.Contains(output, "body-resource-cleanup") {
				t.Fatal(code, output)
			}
			if err := saveFailureHistory(historyPath, []FailedFile{{File: filepath.Join(consumer, "runtime_skip/conditional.test.km"), Suites: []FailedSuite{{Name: "conditional tests", Cases: []string{"body skipped"}}}}}); err != nil {
				t.Fatal(err)
			}
			code, output = invoke("--rerun-failed", historyPath, "--save-failures", historyPath, "--reporter", reporter, "runtime_skip")
			history, err = readFailureHistory(historyPath)
			if code != 0 || err != nil || len(history.Files) != 0 || !strings.Contains(output, "body-resource-cleanup") || strings.Contains(output, "conditional-later-case") || strings.Contains(output, "shared-resource-cleanup") {
				t.Fatal(code, output, history, err)
			}
		})
	}
	// Cleanup errors take precedence over skip, retain their phases, and save
	// exact failed cases rather than a whole-file fallback.
	historyPath := filepath.Join(t.TempDir(), "failures.json")
	code, output := invoke("--save-failures", historyPath, "--reporter", "json", "runtime_skip/failures.test.km")
	history, err := readFailureHistory(historyPath)
	if code != 1 || err != nil || len(history.Files) != 1 || history.Files[0].WholeFile || len(history.Files[0].Suites) != 1 || fmt.Sprint(history.Files[0].Suites[0].Cases) != "[cleanup overrides skip invalid reason joined failure cleanup cannot skip]" || !strings.Contains(output, `"phase":"cleanup"`) || !strings.Contains(output, "genuine failure") || !strings.Contains(output, "skip-failures-later-case") {
		t.Fatal(code, output, history, err)
	}
	code, output = invoke("--bail", "runtime_skip/failures.test.km")
	if code != 1 || !strings.Contains(output, "cleanup broke") || !strings.Contains(output, "0 passed, 1 failed, 4 skipped") || strings.Contains(output, "skip-failures-later-case") {
		t.Fatal(code, output)
	}
	// Selected static todo cases retain their strict policy even if shared
	// runnable cases are skipped during setup.
	code, output = invoke("--fail-on-todo", "runtime_skip/shared.test.km")
	if code != 1 || !strings.Contains(output, "todo tests are not allowed") || !strings.Contains(output, "shared-resource-cleanup") {
		t.Fatal(code, output)
	}
	code, output = invoke("--fail-on-todo", "--filter", "shared", "runtime_skip/shared.test.km")
	if code != 0 || !strings.Contains(output, "0 passed, 0 failed, 4 skipped, 0 todo") {
		t.Fatal(code, output)
	}
	// Every repeat retains successful skip reports and coverage/artifact data.
	coverage, artifacts := t.TempDir(), t.TempDir()
	code, output = invoke("--repeat", "2", "--exclude", "failures.test.km", "--jobs", "2", "--shuffle", "42", "--coverage", coverage, "--artifacts", artifacts, "--reporter", "json", "runtime_skip")
	if code != 0 || !strings.Contains(output, `"passedFiles":4`) || !strings.Contains(output, `"noMatch":false`) || strings.Contains(output, "unreachable-") {
		t.Fatal(code, output)
	}
	for _, parent := range []string{coverage, artifacts} {
		entries, err := os.ReadDir(parent)
		if err != nil || len(entries) != 2 {
			t.Fatal(entries, err)
		}
	}
}
