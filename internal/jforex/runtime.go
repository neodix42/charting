package jforex

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type bridgeRuntime struct {
	directory        string
	java             string
	privateDirectory string
	sdkDirectory     string
	err              error
}

type commandFactory func(context.Context) (*exec.Cmd, error)

func discoverRuntime() bridgeRuntime {
	name := "java"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	java := ""
	if home := strings.TrimSpace(os.Getenv("JAVA_HOME")); home != "" {
		candidate := filepath.Join(home, "bin", name)
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			java = candidate
		}
	}
	if java == "" {
		java, _ = exec.LookPath(name)
	}
	if java != "" {
		if absolute, err := filepath.Abs(java); err == nil {
			java = absolute
		}
	}
	candidates := []string{}
	if explicit := strings.TrimSpace(os.Getenv("CRYPTODESK_JFOREX_RUNTIME")); explicit != "" {
		candidates = append(candidates, explicit)
	} else {
		if executable, err := os.Executable(); err == nil {
			if resolved, err := filepath.EvalSymlinks(executable); err == nil {
				executable = resolved
			}
			candidates = append(candidates, filepath.Join(filepath.Dir(executable), "jforex-runtime"))
		}
		if cwd, err := os.Getwd(); err == nil {
			for directory := cwd; ; directory = filepath.Dir(directory) {
				if _, err := os.Stat(filepath.Join(directory, "go.mod")); err == nil {
					candidates = append(candidates, filepath.Join(directory, "jforex-runtime"))
					break
				}
				parent := filepath.Dir(directory)
				if parent == directory {
					break
				}
				directory = parent
			}
		}
	}
	result := bridgeRuntime{java: java}
	for _, directory := range candidates {
		absolute, err := filepath.Abs(directory)
		if err != nil {
			continue
		}
		if result.directory == "" {
			result.directory = absolute
		}
		if info, err := os.Stat(filepath.Join(absolute, "cryptodesk-jforex.jar")); err == nil && info.Mode().IsRegular() {
			result.directory = absolute
			if _, err := readSDKManifest(filepath.Join(absolute, "SDK-ARTIFACTS.json")); err != nil {
				result.err = err
				return result
			}
			if java == "" {
				result.err = errors.New("Java 17 or newer is required for Dukascopy charts; install a Java runtime and restart CryptoDesk")
			}
			return result
		}
	}
	result.err = errors.New("The JForex bridge is missing; restore the complete jforex-runtime folder beside CryptoDesk or run python3 scripts/jforex_bundle.py --output jforex-runtime in a source checkout")
	return result
}

func (r bridgeRuntime) command(ctx context.Context) (*exec.Cmd, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.preparedCommand(ctx), nil
}

func (r bridgeRuntime) prepare(ctx context.Context) error {
	if r.err != nil {
		return r.err
	}
	if err := os.MkdirAll(r.privateDirectory, 0700); err != nil {
		return errors.New("Could not create the private JForex runtime folder")
	}
	if err := checkJava(ctx, r.java); err != nil {
		return err
	}
	manifest, err := readSDKManifest(filepath.Join(r.directory, "SDK-ARTIFACTS.json"))
	if err != nil {
		return err
	}
	return prepareSDK(ctx, r.sdkDirectory, manifest, newSDKClient())
}

func (r bridgeRuntime) preparedCommand(ctx context.Context) *exec.Cmd {
	classpath := filepath.Join(r.directory, "cryptodesk-jforex.jar") + string(os.PathListSeparator) + filepath.Join(r.sdkDirectory, "lib", "*")
	cmd := exec.CommandContext(ctx, r.java, "-Djava.awt.headless=true", "-Duser.home="+r.privateDirectory, "-Djava.util.prefs.userRoot="+filepath.Join(r.privateDirectory, "preferences"), "-cp", classpath, "cryptodesk.jforex.Bridge")
	cmd.Dir = r.privateDirectory
	cmd.WaitDelay = 2 * time.Second
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		switch strings.ToUpper(name) {
		case "JAVA_TOOL_OPTIONS", "JDK_JAVA_OPTIONS", "_JAVA_OPTIONS", "CLASSPATH":
			continue
		}
		cmd.Env = append(cmd.Env, entry)
	}
	configureProcess(cmd)
	return cmd
}

func checkJava(ctx context.Context, path string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "-version")
	configureProcess(cmd)
	data, err := cmd.CombinedOutput()
	if err != nil || len(data) > 16*1024 {
		return errors.New("Java 17 or newer is required for Dukascopy charts; install a supported Java runtime and restart CryptoDesk")
	}
	match := regexp.MustCompile(`(?:version\s+"?|openjdk\s+)(?:1\.)?([0-9]+)`).FindSubmatch(data)
	if len(match) != 2 {
		return errors.New("Could not verify the Java runtime; Java 17 or newer is required for Dukascopy charts")
	}
	major, err := strconv.Atoi(string(match[1]))
	if err != nil || major < 17 {
		return errors.New("Java 17 or newer is required for Dukascopy charts; install a supported Java runtime and restart CryptoDesk")
	}
	return nil
}
