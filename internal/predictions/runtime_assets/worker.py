"""CryptoDesk's local-only, pinned Toto 2.0 inference protocol.

The parent owns preparation and validates every artifact. Nothing in this worker
fetches packages, model weights, arbitrary repository code, or network resources.
"""
import contextlib
import importlib.metadata
import json
import math
import os
import sys
import threading

_output = sys.stdout
_phase = "runtime"


def send(value):
    _output.write(json.dumps(value, allow_nan=False, separators=(",", ":")) + "\n")
    _output.flush()


def owner_watch():
    # An abrupt application exit closes the pipe even while native Torch is busy.
    # Do not leave a many-GB model process running without its owning application.
    while os.read(sys.stdin.fileno(), 1):
        pass
    os._exit(71)


def run(request):
    global _phase
    if sys.version_info[:2] != (3, 12):
        raise RuntimeError("The private prediction runtime requires Python 3.12")
    for package, expected in {"toto-2": "2.0.0", "torch": "2.8.0+cpu", "dd-unit-scaling": "0.1.0"}.items():
        if importlib.metadata.version(package) != expected:
            raise RuntimeError("The private prediction dependency versions have changed; prepare it again")
    send({"type": "progress", "phase": "loading", "progress": 5, "message": "Loading the local CPU prediction runtime"})
    # Third-party informational prints belong on bounded stderr, never stdout.
    with contextlib.redirect_stdout(sys.stderr):
        import torch
        from toto2 import Toto2Model
        if request.get("action") == "probe":
            send({"type": "ready"})
            return
        closes = request["closes"]
        horizon = request["horizon"]
        if len(closes) not in (512, 1024) or not isinstance(horizon, int) or not 1 <= horizon <= 256:
            raise ValueError("Invalid context or forecast horizon")
        if any(not isinstance(x, (int, float)) or not math.isfinite(x) for x in closes):
            raise ValueError("The forecast requires finite close prices")
        torch.set_num_threads(max(1, min(8, os.cpu_count() or 1)))
        _phase = "model"
        model = Toto2Model.from_pretrained(request["modelDirectory"], local_files_only=True, trust_remote_code=False, map_location="cpu").to(device="cpu", dtype=torch.float32).eval()
        if model.config.patch_size != 32:
            raise RuntimeError("Unexpected pinned model patch size")
        _phase = "forecasting"
        send({"type": "progress", "phase": "forecasting", "progress": 25, "message": "Forecasting closed-bar prices locally on CPU"})
        target = torch.tensor(closes, dtype=torch.float32).reshape(1, 1, -1)
        inputs = {"target": target, "target_mask": torch.ones_like(target, dtype=torch.bool), "series_ids": torch.zeros((1, 1), dtype=torch.long)}
        with torch.inference_mode():
            quantiles = model.forecast(inputs, horizon=horizon, decode_block_size=None, has_missing_values=False)
        if tuple(quantiles.shape) != (9, 1, 1, horizon):
            raise RuntimeError("Unexpected pinned model output shape")
        values = quantiles[:, 0, 0, :].cpu().tolist()
        points = [{"step": i + 1, "lower": values[0][i], "median": values[4][i], "upper": values[8][i]} for i in range(horizon)]
        for point in points:
            if not all(math.isfinite(point[k]) for k in ("lower", "median", "upper")) or not point["lower"] <= point["median"] <= point["upper"]:
                raise RuntimeError("The model returned invalid prediction quantiles")
        send({"type": "result", "points": points})


def main():
    line = sys.stdin.buffer.readline(131073)
    if not line or len(line) > 131072 or not line.endswith(b"\n"):
        raise ValueError("Invalid prediction request")
    request = json.loads(line)
    threading.Thread(target=owner_watch, daemon=True).start()
    run(request)


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        send({"type": "error", "phase": _phase, "message": str(error)[:2000]})
        sys.exit(1)
