package main

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func specialDiscoveryFiles(t *testing.T, directory string) []string {
	t.Helper()
	var paths []string
	if mkfifo, err := exec.LookPath("mkfifo"); err == nil {
		path := filepath.Join(directory, "pipe.test.km")
		if output, err := exec.Command(mkfifo, path).CombinedOutput(); err != nil {
			t.Fatalf("create named pipe: %v %s", err, output)
		}
		paths = append(paths, path)
		t.Cleanup(func() { os.Remove(path) })
	}
	path := filepath.Join(directory, "socket.test.km")
	if listener, err := net.Listen("unix", path); err == nil {
		t.Cleanup(func() { listener.Close() })
		paths = append(paths, path)
	} else {
		t.Logf("Unix socket unavailable: %v", err)
	}
	return paths
}

func discoveryLink(t *testing.T, target, path string) {
	t.Helper()
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("symbolic links unavailable: %v", err)
	}
}

func TestDiscoveryKeepsRegularFilesAndFileLinks(t *testing.T) {
	root, external := t.TempDir(), t.TempDir()
	writeWatchFile(t, filepath.Join(root, "a.test.km"), "a")
	writeWatchFile(t, filepath.Join(root, "nested", "z.test.km"), "z")
	for _, directory := range []string{".hidden", ".kinmokusei", "node_modules", "vendor", "build", "dist"} {
		writeWatchFile(t, filepath.Join(root, directory, "ignored.test.km"), "ignored")
	}
	writeWatchFile(t, filepath.Join(root, "other.km"), "other")
	target := filepath.Join(external, "source.km")
	writeWatchFile(t, target, "linked source")
	link := filepath.Join(root, "link.test.km")
	discoveryLink(t, target, link)
	discoveryLink(t, filepath.Join(external, "absent.km"), filepath.Join(root, "broken.test.km"))
	discoveryLink(t, root, filepath.Join(root, "cycle.test.km"))
	discoveryLink(t, external, filepath.Join(root, "directory.test.km"))
	specialDiscoveryFiles(t, root)
	files, err := discover(root)
	want := []string{filepath.Join(root, "a.test.km"), link, filepath.Join(root, "nested", "z.test.km")}
	if err != nil || !reflect.DeepEqual(files, want) {
		t.Fatalf("discovery=%v err=%v want=%v", files, err, want)
	}
	files, err = discover(link)
	if err != nil || !reflect.DeepEqual(files, []string{link}) {
		t.Fatalf("explicit file link=%v err=%v", files, err)
	}
	status, output := captureRunner(t, []string{"--no-config", "--list", root})
	if status != 0 || strings.TrimSpace(output) != strings.Join(want, "\n") {
		t.Fatalf("list status=%d %s", status, output)
	}
}

func TestDiscoveryRejectsExplicitSpecialFiles(t *testing.T) {
	root := t.TempDir()
	paths := specialDiscoveryFiles(t, root)
	if len(paths) == 0 {
		t.Skip("no special file types available")
	}
	t.Setenv("KEIKA", filepath.Join(root, "compiler-must-not-run"))
	for _, path := range paths {
		for _, selected := range []string{path, path + ".link.test.km"} {
			if selected != path {
				discoveryLink(t, path, selected)
			}
			if _, err := discover(selected); err == nil || !strings.Contains(err.Error(), "regular file") {
				t.Fatalf("special input accepted: %s err=%v", selected, err)
			}
			for _, reporter := range []string{"text", "json", "junit"} {
				history := filepath.Join(t.TempDir(), "failures.json")
				original := []byte("{\"version\":1,\"files\":[]}\n")
				if err := os.WriteFile(history, original, 0600); err != nil {
					t.Fatal(err)
				}
				status, output := captureRunner(t, []string{"--no-config", "--reporter", reporter, "--save-failures", history, selected})
				after, err := os.ReadFile(history)
				if status != 2 || !strings.Contains(output, "regular file") || err != nil || string(after) != string(original) {
					t.Fatalf("special input status=%d err=%v history=%s %s", status, err, after, output)
				}
			}
		}
	}
}

func TestWatchTracksFileLinksAndSkipsSpecialFiles(t *testing.T) {
	root, external := t.TempDir(), t.TempDir()
	target := filepath.Join(external, "source.km")
	writeWatchFile(t, target, "first")
	link := filepath.Join(root, "linked.test.km")
	discoveryLink(t, target, link)
	discoveryLink(t, root, filepath.Join(root, "cycle.test.km"))
	discoveryLink(t, filepath.Join(external, "absent.km"), filepath.Join(root, "broken.test.km"))
	specialDiscoveryFiles(t, root)
	files, err := watchEntries(root, true)
	if err != nil || !reflect.DeepEqual(files, []string{link}) {
		t.Fatalf("watched files=%v err=%v", files, err)
	}
	initial := signatureFor(t, root, true)
	writeWatchFile(t, target, "other")
	changed := signatureFor(t, root, true)
	if initial == changed {
		t.Fatal("linked source edit was missed")
	}
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	missing := signatureFor(t, root, true)
	if missing == changed {
		t.Fatal("linked source deletion was missed")
	}
	writeWatchFile(t, target, "other")
	if signatureFor(t, root, true) != changed {
		t.Fatal("linked source restoration was missed")
	}
	snapshot := filepath.Join(root, "__snapshots__", "linked.snap")
	if err := os.MkdirAll(filepath.Dir(snapshot), 0700); err != nil {
		t.Fatal(err)
	}
	discoveryLink(t, target, snapshot)
	if signatureFor(t, root, false) != changed || signatureFor(t, root, true) == changed {
		t.Fatal("linked snapshot ignored includeSnapshots policy")
	}
}

func TestRegularDiscoveryRealKinmokusei(t *testing.T) {
	runner, consumer := os.Getenv("VASEST_INTEGRATION_RUNNER"), os.Getenv("VASEST_INTEGRATION_CONSUMER")
	if runner == "" || consumer == "" {
		t.Skip("requires the external Kinmokusei project")
	}
	root := filepath.Join(consumer, "regular_discovery")
	specialDiscoveryFiles(t, root)
	link := filepath.Join(root, "linked.test.km")
	discoveryLink(t, filepath.Join(root, "a_passing.test.km"), link)
	t.Cleanup(func() { os.Remove(link) })
	broken := filepath.Join(root, "broken.test.km")
	discoveryLink(t, filepath.Join(root, "absent.km"), broken)
	t.Cleanup(func() { os.Remove(broken) })
	cycle := filepath.Join(root, "cycle.test.km")
	discoveryLink(t, root, cycle)
	t.Cleanup(func() { os.Remove(cycle) })
	invoke := func(t *testing.T, args ...string) (int, string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, runner, append([]string{"--no-config", "--timeout", "2s"}, args...)...)
		command.Dir, command.WaitDelay = consumer, time.Second
		output, err := command.CombinedOutput()
		if err == nil {
			return 0, string(output)
		}
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("discovery integration: %v %s", err, output)
		}
		return exit.ExitCode(), string(output)
	}
	for _, reporter := range []string{"text", "json", "junit"} {
		t.Run(reporter, func(t *testing.T) {
			historyPath := filepath.Join(t.TempDir(), "failures.json")
			status, output := invoke(t, "--reporter", reporter, "--jobs", "2", "--repeat", "2", "--shuffle", "42", "--save-failures", historyPath, root)
			history, err := readFailureHistory(historyPath)
			if status != 0 || err != nil || len(history.Files) != 0 || strings.Contains(output, "pipe.test.km") || strings.Contains(output, "socket.test.km") || strings.Contains(output, "broken.test.km") || strings.Contains(output, "cycle.test.km") {
				t.Fatalf("nonregular discovery status=%d history=%#v err=%v %s", status, history, err, output)
			}
			if reporter == "text" && !strings.Contains(output, "6 file(s) passed, 0 failed, 0 not run") {
				t.Fatal("wrong regular file count", output)
			}
			if reporter == "json" {
				fileCount, summaries := 0, 0
				for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
					var event struct {
						Type, Status             string
						PassedFiles, FailedFiles int
					}
					if err := json.Unmarshal([]byte(line), &event); err != nil {
						t.Fatalf("invalid JSON: %v %s", err, output)
					}
					if event.Type == "file" {
						fileCount++
						if event.Status != "pass" {
							t.Fatal("regular file failed", output)
						}
					}
					if event.Type == "summary" {
						summaries++
						if event.PassedFiles != 6 || event.FailedFiles != 0 {
							t.Fatal("wrong JSON counts", output)
						}
					}
				}
				if fileCount != 6 || summaries != 1 {
					t.Fatal("missing regular file reports", output)
				}
			}
			if reporter == "junit" {
				var document fileErrorJUnitDocument
				if err := xml.Unmarshal([]byte(output), &document); err != nil || document.Tests != 6 || document.Failures != 0 || len(document.Suites) != 6 {
					t.Fatalf("wrong JUnit counts: %#v err=%v %s", document, err, output)
				}
			}
			status, output = invoke(t, "--reporter", reporter, link)
			if status != 0 || !strings.Contains(output, "regular-discovery-body") {
				t.Fatalf("explicit regular file link failed: status=%d %s", status, output)
			}
		})
	}
}
