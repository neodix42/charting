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
	executable, executableErr := os.Executable()
	return discoverRuntimeAt(executable, executableErr, runtime.GOOS)
}

// Prefer the bridge shipped with the actual executable, never a working-directory
// checkout. This keeps source builds and extracted releases self-contained.
func discoverRuntimeAt(executable string, executableErr error, platform string) bridgeRuntime {
	name, systemJava := "java", ""
	if platform == "windows" {
		name += ".exe"
	} else if platform == "linux" {
		systemJava = "/usr/bin/java"
	}
	result := bridgeRuntime{java: findJava(name, systemJava, os.Getenv("JAVA_HOME"))}
	if explicit := strings.TrimSpace(os.Getenv("CRYPTODESK_JFOREX_RUNTIME")); explicit != "" {
		result.directory, result.err = filepath.Abs(explicit)
	} else if executableErr != nil || executable == "" {
		result.err = errors.New("Could not locate the CryptoDesk executable")
	} else {
		if resolved, err := filepath.EvalSymlinks(executable); err == nil {
			executable = resolved
		}
		bundled := filepath.Join(filepath.Dir(executable), "jforex-runtime")
		_, bundleErr := os.Lstat(bundled)
		if platform == "windows" || !errors.Is(bundleErr, os.ErrNotExist) {
			result.directory = bundled
		} else {
			result.directory, result.err = installedRuntimeDirectory()
		}
	}
	if result.err != nil {
		return result
	}
	if info, err := os.Stat(filepath.Join(result.directory, "cryptodesk-jforex.jar")); err != nil || !info.Mode().IsRegular() {
		result.err = errors.New("The JForex bridge is missing; keep the complete jforex-runtime folder beside CryptoDesk, rebuild the full bundle with ./build.sh, or run ./install-user.sh from an extracted release")
		return result
	}
	if _, err := readSDKManifest(filepath.Join(result.directory, "SDK-ARTIFACTS.json")); err != nil {
		result.err = err
		return result
	}
	if result.java == "" {
		result.err = errors.New("Java 17 or newer is required for Dukascopy charts; install a Java runtime and restart CryptoDesk")
	}
	return result
}

func installedRuntimeDirectory() (string, error) {
	// Match install-user.sh, including an explicitly configured absolute XDG
	// location. A relative XDG value must never depend on the working directory.
	base := os.Getenv("XDG_DATA_HOME")
	if !filepath.IsAbs(base) {
		home, err := os.UserHomeDir()
		if err != nil || !filepath.IsAbs(home) {
			return "", errors.New("Could not locate your home directory")
		}
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "cryptodesk-app", "jforex-runtime"), nil
}

func findJava(name, systemPath, javaHome string) string {
	// IDEs commonly export their own JAVA_HOME. Prefer the system runtime on
	// Linux, then PATH, retaining JAVA_HOME as a fallback for other installations.
	path := ""
	if info, err := os.Stat(systemPath); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 {
		path = systemPath
	}
	if path == "" {
		path, _ = exec.LookPath(name)
	}
	if path == "" && strings.TrimSpace(javaHome) != "" {
		candidate := filepath.Join(strings.TrimSpace(javaHome), "bin", name)
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			path = candidate
		}
	}
	if path != "" {
		path, _ = filepath.Abs(path)
	}
	return path
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
