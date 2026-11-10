package chartimage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"cryptodesk/internal/screener"
)

func TestSignalChartDirectionValidation(t *testing.T) {
	for _, direction := range []string{"", "long", "short"} {
		request := sampleRequest()
		request.Chart.Direction = direction
		if err := validate(request); err != nil {
			t.Fatalf("valid direction %q rejected: %v", direction, err)
		}
	}
	for _, direction := range []string{"buy", "sell", "Long", " short ", strings.Repeat("x", 1024)} {
		request := sampleRequest()
		request.Chart.Direction = direction
		if err := validate(request); err == nil {
			t.Fatalf("invalid direction %q accepted", direction)
		}
	}
}

// Exercise the exact built Telegram entry point and production worker with
// test-only canvas observation. It changes neither plot data nor rendering,
// and all production network/closed-bar checks remain in place. No Telegram
// configuration, recipient, server or provider is involved.
func TestHeadlessDirectionalEntryLabels(t *testing.T) {
	if os.Getenv("CRYPTODESK_TEST_CHART_RENDER") != "1" {
		t.Skip("requires built frontend and private Chromium runtime")
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("local smoke runtime is Linux amd64")
	}
	repository, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	runtimeDirectory := os.Getenv("CRYPTODESK_CHART_RUNTIME")
	if runtimeDirectory == "" {
		runtimeDirectory = filepath.Join(repository, ".tools", "screener-runtime-linux-amd64")
	}
	t.Setenv("CRYPTODESK_SCREENER_RUNTIME", runtimeDirectory)
	manager := screener.New(t.TempDir())
	defer manager.Close()
	node, runtimeDirectory, browser, err := manager.SignalChartRuntime()
	if err != nil {
		t.Fatal(err)
	}
	for _, sample := range []struct {
		name, direction, expected string
		entry, stop, target       float64
	}{
		{"long", "long", "Long at 90,000", 90000, 88000, 94000},
		{"short", "short", "Short at 90,000", 90000, 92000, 86000},
		{"small-price", "long", "Long at 0.0000002", 0.0000002, 0.0000001, 0.0000003},
		{"unknown-direction", "", "Match", 90000, 88000, 94000},
	} {
		t.Run(sample.name, func(t *testing.T) {
			request := sampleRequest()
			request.Chart.Direction = sample.direction
			request.Chart.Signal = &screener.AnalysisSignalLevels{Entry: sample.entry, StopLoss: &sample.stop, TakeProfit: &sample.target}
			if err := validate(request); err != nil {
				t.Fatal(err)
			}
			image := renderWithObservedCanvas(t, Runtime{Node: node, Directory: runtimeDirectory, Browser: browser}, request, sample.expected)
			if target := os.Getenv("CRYPTODESK_CHART_LABEL_SAMPLES"); target != "" {
				if err := os.MkdirAll(target, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(target, sample.name+".png"), image, 0600); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func renderWithObservedCanvas(t *testing.T, paths Runtime, request Request, expected string) []byte {
	t.Helper()
	private, err := os.MkdirTemp("/tmp", "cd-label-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(private) })
	worker, err := assets.ReadFile("assets/worker.cjs")
	if err != nil {
		t.Fatal(err)
	}
	const start = "    await page.evaluate(request => window.__renderSignalChart(request), request);"
	const screenshot = "    const image = await chart.screenshot({type:'png',animations:'disabled',timeout:12000});"
	if strings.Count(string(worker), start) != 1 || strings.Count(string(worker), screenshot) != 1 {
		t.Fatal("production worker observation points changed")
	}
	observe := `    await page.evaluate(() => {
      const original = CanvasRenderingContext2D.prototype.fillText;
      window.__observedChartTexts = [];
      CanvasRenderingContext2D.prototype.fillText = function(text, ...args) {
        window.__observedChartTexts.push(String(text));
        return original.call(this, text, ...args);
      };
    });
` + start
	encodedExpected, _ := json.Marshal(expected)
	assert := fmt.Sprintf(`    const texts = await page.evaluate(() => window.__observedChartTexts);
    const expected = %s;
    const obsolete = texts.filter(text => /^(Entry|SL|TP)(?: |$)/.test(text) || expected !== 'Match' && text === 'Match');
    if (!texts.includes(expected) || obsolete.length || expected === 'Match' && texts.some(text => /^(Long|Short) at /.test(text))) {
      process.stderr.write(JSON.stringify({expected, obsolete, observed:texts}) + '\n');
      throw Error('Directional marker text did not match');
    }
`, encodedExpected) + screenshot
	worker = []byte(strings.Replace(strings.Replace(string(worker), start, observe, 1), screenshot, assert, 1))
	for _, name := range []string{"chart.js", "chart.css", "worker.cjs"} {
		contents := worker
		if name != "worker.cjs" {
			contents, err = assets.ReadFile("assets/" + name)
			if err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(private, name), contents, 0600); err != nil {
			t.Fatal(err)
		}
	}
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), renderTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, paths.Node, filepath.Join(private, "worker.cjs"), paths.Directory, paths.Browser)
	command.Dir, command.Env, command.Stdin = private, renderEnvironment(private), bytes.NewReader(payload)
	output, diagnostics := &limitedBuffer{max: maxImage}, &limitedBuffer{max: 4096}
	command.Stdout, command.Stderr, command.WaitDelay = output, diagnostics, time.Second
	configureProcess(command)
	if err := command.Run(); err != nil {
		t.Fatalf("observed production image worker failed: %v: %s", err, diagnostics.String())
	}
	image := output.Bytes()
	config, err := png.DecodeConfig(bytes.NewReader(image))
	if err != nil || config.Width != 1440 || config.Height < 420 || config.Height > 8500 {
		t.Fatalf("directional marker did not render a bounded chart PNG: %+v %v", config, err)
	}
	return image
}
