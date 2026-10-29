package predictions

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"runtime"
)

// All artifacts are immutable, checksum-pinned official releases. Preparing a
// runtime never executes model repository code or resolves unpinned dependencies.
//
//go:embed runtime_assets/*
var runtimeAssets embed.FS

type runtimeArtifact struct {
	Name    string `json:"name"`
	URL     string `json:"url"`
	SHA256  string `json:"sha256"`
	Size    int64  `json:"size"`
	Package string `json:"package,omitempty"`
	Version string `json:"version,omitempty"`
}

type runtimeModel struct {
	ID, Name, Revision string
	Files              []runtimeArtifact
}

var runtimeModels = []runtimeModel{
	modelManifest("4m", "4M · smallest", "8306a9801cf98c0f5ffe4b2dcc8f496e616d84d9", 592, "7a926d130e401ab0c5fdb3564f46c8d917bd05c7b3ae26b9c22d2da2ef01d2d8", 16582848, "316660d5afb47943e531f39242e0b02ca0b8bb73be5709dfe07ca80dfce9805e"),
	modelManifest("22m", "22M · small", "685e4ae3e2be8d8998025e53dd98e7fdcb296a89", 593, "abeaf0fcd54aaac66757fde69ec3ddb4d3bfdcf96e0c8f767aefd03ab4c9e8d9", 87669368, "9cd503d82df3aa71747862688f47a31c1d0a4b80f898df6e046189016eaa21dd"),
	modelManifest("313m", "313M · default", "a7bab288f5e95f8606f8306f86659357e1c001ef", 598, "9cd42ebab5167752f4382f9304a26c05a44e20eb13b71eea8810f729fed0ffd7", 1250763568, "5ee6066497963cb904bf1324ab850661c3201bbb3406052459f1f792386aa2f9"),
	modelManifest("1B", "1B · large", "1604e1a5242884fb9848f88c4ced14f4dc62d9d3", 598, "c8aa025e7155b78e01e257f79ec1cf610205b720007bc7369f7fc6acf2328eed", 4164169600, "eabb9dfae622fdb2f19744a46f1e9c4e5ed6ee0d3c6cba7c227865d39974658f"),
	modelManifest("2.5B", "2.5B · largest", "51a2812bbe449437c01b79c0e425ed578f335f5b", 598, "172904c65bb5af77a95e1a81cdd9214f4c9d983651d6dceef1375d3d09f47a36", 9817176960, "dc08942b20751ac906167194d4ca4aa06b4367e80aabe5f1d5153b30b874bdb9"),
}

func modelManifest(suffix, name, revision string, configSize int64, configHash string, weightSize int64, weightHash string) runtimeModel {
	id := "Datadog/Toto-2.0-" + suffix
	base := "https://huggingface.co/" + id + "/resolve/" + revision + "/"
	return runtimeModel{ID: id, Name: "Toto 2.0 " + name, Revision: revision, Files: []runtimeArtifact{
		{Name: "config.json", URL: base + "config.json", Size: configSize, SHA256: configHash},
		{Name: "model.safetensors", URL: base + "model.safetensors", Size: weightSize, SHA256: weightHash},
	}}
}

func Models() []ModelOption {
	result := make([]ModelOption, 0, len(runtimeModels))
	for _, m := range runtimeModels {
		result = append(result, ModelOption{ID: m.ID, Name: m.Name})
	}
	return result
}
func ValidModel(id string) bool { _, ok := findRuntimeModel(id); return ok }
func findRuntimeModel(id string) (runtimeModel, bool) {
	for _, m := range runtimeModels {
		if m.ID == id {
			return m, true
		}
	}
	return runtimeModel{}, false
}

const runtimePythonVersion = "3.12.16"
const runtimeRelease = "20261026"

func pythonArtifact(platform string) (runtimeArtifact, error) {
	var target, hash string
	var size int64
	switch platform {
	case "linux":
		target = "x86_64-unknown-linux-gnu"
		hash = "ce4f6d0a6cfdb3bed6e44c2f9affd85943c76a42e25ad3a259002f31d6fb8a20"
		size = 34505639
	case "windows":
		target = "x86_64-pc-windows-msvc"
		hash = "1f2ab53880fc67ea323398c6388e05af28addc99a8e389209f08a51fdff4b149"
		size = 22014416
	default:
		return runtimeArtifact{}, fmt.Errorf("local predictions support Linux and Windows on x86-64; this system is %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	name := "cpython-" + runtimePythonVersion + "+" + runtimeRelease + "-" + target + "-install_only_stripped.tar.gz"
	return runtimeArtifact{Name: name, URL: "https://github.com/astral-sh/python-build-standalone/releases/download/" + runtimeRelease + "/cpython-" + runtimePythonVersion + "%2B" + runtimeRelease + "-" + target + "-install_only_stripped.tar.gz", Size: size, SHA256: hash}, nil
}
func wheelArtifacts(platform string) ([]runtimeArtifact, error) {
	b, err := runtimeAssets.ReadFile("runtime_assets/wheels_" + platform + ".json")
	if err != nil {
		return nil, err
	}
	var a []runtimeArtifact
	err = json.Unmarshal(b, &a)
	return a, err
}
func runtimeDigest(platform string) string {
	h := sha256.New()
	for _, name := range []string{"worker.py", "wheels_" + platform + ".json"} {
		b, err := runtimeAssets.ReadFile("runtime_assets/" + name)
		if err != nil {
			return ""
		}
		h.Write(b)
	}
	h.Write([]byte(runtimePythonVersion + runtimeRelease))
	return hex.EncodeToString(h.Sum(nil))
}
