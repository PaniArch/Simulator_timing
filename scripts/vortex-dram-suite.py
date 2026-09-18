#!/usr/bin/env python3
"""Snapshot and run paired native RTLSim/DramSim benchmarks on Slurm."""
import argparse
import collections
import csv
import datetime
import gzip
import hashlib
import importlib.util
import json
import math
import os
from pathlib import Path
import re
import shutil
import signal
import subprocess
import tempfile
import threading
import time

EXPANDED = {
    "async_barrier": "-n32 -t4", "conv3": "-n8 -l", "demo": "-n32 -x4 -y4",
    "diverge": "-n4 -d4", "dogfood": "-n16 -s0 -e21 -c", "dotproduct": "-n1024",
    "dotproduct2": "-n1024", "dropout": "-n1024", "fence": "-n32", "io_addr": "-n32",
    "jacobi": "-n16", "madmax": "-n4", "mstress": "-n32", "multikernel": "-n256",
    "occupancy": "-c8", "pathfinder": "-n32", "raycast": "-n2 -w8 -h8 -s1 -d1",
    "relu": "-n1024", "sgemm": "-n32", "sgemm2": "-n32 -t4 -c8", "sgemmx": "-n32",
    "sgemv": "-m32 -n32", "softmax": "-n8", "sort": "-n4", "stencil3d": "-n8",
    "vecadd": "-n1024", "wgather": "-n8 -t4", "basic": "-n1024", "wsync": "-i128",
    "bfs": "-n256",
}

def digest(path):
    h = hashlib.sha256()
    with path.open("rb") as f:
        for chunk in iter(lambda: f.read(1024 * 1024), b""):
            h.update(chunk)
    return h.hexdigest()

def save(path, obj):
    tmp = path.with_suffix(path.suffix + ".tmp")
    tmp.write_text(json.dumps(obj, indent=2) + "\n")
    tmp.replace(path)

def prepare(repo, timeout):
    previous = repo / ".cache/timing-stress-micro-20260917"
    inputs = previous / "startup-fixed-ref"
    bridge = repo / ".cache/dramsim-integration"
    cases = json.loads((inputs / "manifest.json").read_text())["cases"]
    assert len(cases) == 31 and set(EXPANDED) == {c["benchmark"] for c in cases} - {"packld"}
    parent = repo / ".cache/dram-expanded"
    parent.mkdir(exist_ok=True)
    stamp = datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%dT%H%M%SZ-")
    root = Path(tempfile.mkdtemp(prefix=stamp, dir=parent))
    files = {
        "runner.py": Path(__file__).resolve(),
        "audit.py": repo / "scripts/vortex-supported.py",
        "run.sbatch": repo / "integration/dramsim/run-suite.sbatch",
        "lib/libramulator.so": repo.parent / "vortex/third_party/ramulator/libramulator.so",
        "lib/libstdc++.so.6": previous / "host-runtime/libstdc++.so.6",
        "lib/libgcc_s.so.1": previous / "host-runtime/libgcc_s.so.1",
    }
    for name in ("libvortex.so", "libvortex-rtlsim.so", "librtlsim.so"):
        files["lib/" + name] = previous / "serial-lib" / name
    for name in ("libsimtiminggo.so", "libvortex-simtiming.so", "libsimtiming-dram.so"):
        files["lib/" + name] = bridge / "lib" / name
    # Require the already-tested production artifacts, not arbitrary stale builds.
    old = json.loads((bridge / "final/manifest.json").read_text())
    for key, src in files.items():
        if str(src) in old["libraries"] and digest(src) != old["libraries"][str(src)]:
            raise ValueError("tested binary changed: " + str(src))
    if digest(files["lib/libramulator.so"]) != old["ramulator_sha256"]:
        raise ValueError("Ramulator changed since smoke validation")
    for case in cases:
        for f in (inputs / "inputs" / case["benchmark"]).iterdir():
            if f.is_file():
                files["inputs/" + case["benchmark"] + "/" + f.name] = f
    hashes = {}
    for name, src in files.items():
        dst = root / name
        dst.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(src, dst)
        hashes[name] = digest(dst)
    suite = []
    for scale in ("small", "expanded"):
        for case in cases:
            name = case["benchmark"]
            if scale == "expanded" and name not in EXPANDED:
                continue
            argv = case["argv"] if scale == "small" else EXPANDED[name].split()
            suite.append(dict(index=len(suite), benchmark=name, scale=scale, argv=argv))
    save(root / "manifest.json", dict(cases=suite, timeout_seconds=timeout, sha256=hashes,
        backend="rtlsim-dram", reference="STD FPU and serial DIV; same frozen RTL profile as prior smoke",
        bridge_queue=dict(accepts=1, inflight=16, returns=1), channels=2, bytes=64, clock_ratio=1,
        comparison="raw final cumulative PERF; no subtraction or latency replay",
        source_sha256=digest(repo.parent / "vortex/sim/common/dram_sim.cpp"),
        frozen_config_sha256=digest(repo / "Vortex_rtl/VX_config.toml"),
        baseline_head=subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=repo, text=True).strip()))
    print(root)

def load_audit(root):
    spec = importlib.util.spec_from_file_location("suite_audit", root / "audit.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module

def parse_stdout(path):
    perf, tail = [], collections.deque(maxlen=25)
    passed = False
    with gzip.open(path, "rt", errors="replace") as f:
        for line in f:
            tail.append(line.rstrip())
            passed |= bool(re.search(r"\bPASSED\b|\bPASS!", line))
            match = re.search(r"PERF: instrs=(\d+), cycles=(\d+)", line)
            if match:
                perf.append(dict(instructions=int(match[1]), cycles=int(match[2])))
    return dict(host_pass=passed, perf=perf, tail=list(tail))

def run(root, index):
    manifest = json.loads((root / "manifest.json").read_text())
    case = manifest["cases"][index]
    assert case["index"] == index
    dst = root / "results" / str(index)
    dst.mkdir(parents=True, exist_ok=False)
    state = dict(case, job=os.getenv("SLURM_JOB_ID"), node=os.uname().nodename, started=time.time(), backends={})
    save(dst / "status.json", state)
    selected = ("lib/", "inputs/" + case["benchmark"] + "/")
    for name, expected in manifest["sha256"].items():
        if name.startswith(selected) or name in ("runner.py", "audit.py"):
            if digest(root / name) != expected:
                raise ValueError("artifact changed: " + name)
    audit = load_audit(root)
    for mode in ("rtlsim", "simtiming"):
        d = dst / mode
        d.mkdir()
        inp = root / "inputs" / case["benchmark"]
        for f in inp.iterdir():
            if f.name != case["benchmark"]:
                shutil.copy2(f, d / f.name)
        env = os.environ.copy()
        for key in ("SIMTIMING_TRACE", "DRAM_TRACE", "LD_PRELOAD", "VORTEX_CONFIG"):
            env.pop(key, None)
        env.update(VORTEX_DRIVER=mode, SIMTIMING_MODE="timing", SIMTIMING_MEMORY_BACKEND="rtlsim-dram",
                   SIMTIMING_DRAM_LIBRARY=str(root / "lib/libsimtiming-dram.so"),
                   SIMTIMING_EVENT_LOG=str(d / "events.jsonl"), GOMAXPROCS="2", LD_LIBRARY_PATH=str(root / "lib"))
        begin = time.monotonic()
        stream_errors, raw_bytes = [], [0]
        with (d / "stderr.log").open("wb") as err:
            proc = subprocess.Popen([str(inp / case["benchmark"]), *case["argv"]], cwd=d, env=env,
                                    stdout=subprocess.PIPE, stderr=err, start_new_session=True)
            def compress():
                try:
                    with gzip.open(d / "stdout.log.gz", "wb", compresslevel=1) as out:
                        while True:
                            chunk = proc.stdout.read(1024 * 1024)
                            if not chunk:
                                break
                            raw_bytes[0] += len(chunk)
                            out.write(chunk)
                except Exception as exc:
                    stream_errors.append(str(exc))
            reader = threading.Thread(target=compress, daemon=True)
            reader.start()
            timeout = False
            while proc.poll() is None:
                state.update(active=mode, heartbeat=time.time(), elapsed_seconds=time.monotonic()-begin,
                             stdout_raw_bytes=raw_bytes[0], pid=proc.pid)
                save(dst / "status.json", state)
                if state["elapsed_seconds"] > manifest["timeout_seconds"] or stream_errors:
                    timeout = not stream_errors
                    os.killpg(proc.pid, signal.SIGTERM)
                    try:
                        proc.wait(timeout=5)
                    except subprocess.TimeoutExpired:
                        os.killpg(proc.pid, signal.SIGKILL)
                        proc.wait()
                    break
                try:
                    proc.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    pass
            reader.join(timeout=30)
        result = dict(exit_code=proc.returncode, seconds=time.monotonic()-begin, timeout=timeout,
                      stdout_raw_bytes=raw_bytes[0], stream_errors=stream_errors)
        if reader.is_alive():
            result["stream_errors"].append("stdout reader did not close")
        if not result["stream_errors"]:
            result.update(parse_stdout(d / "stdout.log.gz"))
        result["passed"] = result["exit_code"] == 0 and not timeout and not result["stream_errors"] and result.get("host_pass", False) and bool(result.get("perf"))
        if mode == "simtiming":
            records, malformed = audit.read_events(d / "events.jsonl")
            result.update(audit.audit_evidence(records, "timing", malformed))
            finishes = [e["summary"] for e in records if isinstance(e, dict) and e.get("event") == "launch-finish" and isinstance(e.get("summary"), dict)]
            result["backend_verified"] = bool(finishes) and all(s.get("memory_backend") == "rtlsim-dram" for s in finishes)
            result["timing_issues"] = sorted({s["rtl_timing_issue"] for s in finishes if s.get("rtl_timing_issue")})
            result["hardware_execution_cycles"] = sum(s.get("hardware_execution_cycles", 0) for s in finishes)
            result["passed"] &= result["evidence_complete"] and result["backend_verified"]
        state["backends"][mode] = result
        save(dst / "status.json", state)
        print(index, case["scale"], case["benchmark"], mode, result["passed"], round(result["seconds"], 2), flush=True)
    state.update(active=None, finished=time.time())
    save(dst / "result.json", state)
    save(dst / "status.json", state)
    return 0 if all(r["passed"] for r in state["backends"].values()) else 1

def aggregate(root):
    manifest = json.loads((root / "manifest.json").read_text())
    rows = []
    for case in manifest["cases"]:
        path = root / "results" / str(case["index"]) / "result.json"
        row = dict(case, status="pending")
        if path.exists():
            r = json.loads(path.read_text())["backends"]
            a, b = r["simtiming"], r["rtlsim"]
            row.update(status="pass" if a["passed"] and b["passed"] else "failed", timing_seconds=a["seconds"], rtl_seconds=b["seconds"],
                       timing_issues=a.get("timing_issues", []), launches=a.get("launches"), execution_cycles=a.get("execution_cycles"),
                       flush_cycles=a.get("flush_cycles"), hardware_execution_cycles=a.get("hardware_execution_cycles"))
            if a.get("perf") and b.get("perf"):
                ap, bp = a["perf"][-1], b["perf"][-1]
                row.update(timing_cycles=ap["cycles"], rtl_cycles=bp["cycles"], timing_instructions=ap["instructions"], rtl_instructions=bp["instructions"])
                row["instructions_match"] = ap["instructions"] == bp["instructions"]
                row["eligible"] = row["status"] == "pass" and row["instructions_match"] and not row["timing_issues"] and bp["cycles"] > 0
                row["error_percent"] = 100*(ap["cycles"] / bp["cycles"] - 1) if bp["cycles"] else None
        rows.append(row)
    groups = {}
    for scale in ("small", "expanded", "all"):
        rr = [r for r in rows if scale == "all" or r["scale"] == scale]
        valid = [r for r in rr if r.get("eligible")]
        groups[scale] = dict(total=len(rr), passed=sum(r["status"] == "pass" for r in rr), pending=sum(r["status"] == "pending" for r in rr), eligible=len(valid))
        if valid:
            errors = sorted(abs(r["error_percent"]) for r in valid)
            groups[scale].update(mape_percent=sum(errors)/len(errors), max_abs_percent=max(errors), within_5=sum(e <= 5 for e in errors),
                within_10=sum(e <= 10 for e in errors), weighted_signed_percent=100*(sum(r["timing_cycles"] for r in valid)/sum(r["rtl_cycles"] for r in valid)-1),
                weighted_abs_percent=100*sum(abs(r["timing_cycles"]-r["rtl_cycles"]) for r in valid)/sum(r["rtl_cycles"] for r in valid),
                median_abs_percent=(errors[(len(errors)-1)//2]+errors[len(errors)//2])/2,
                p95_abs_percent=errors[math.ceil(0.95*len(errors))-1],
                timing_wall_seconds=sum(r["timing_seconds"] for r in valid), rtl_wall_seconds=sum(r["rtl_seconds"] for r in valid))
    save(root / "summary.json", dict(groups=groups, rows=rows))
    fields = ["index", "scale", "benchmark", "argv", "status", "eligible", "timing_cycles", "rtl_cycles", "error_percent", "timing_instructions", "rtl_instructions", "launches", "execution_cycles", "hardware_execution_cycles", "flush_cycles", "timing_seconds", "rtl_seconds", "timing_issues"]
    with (root / "summary.csv").open("w", newline="") as f:
        w = csv.DictWriter(f, fieldnames=fields, extrasaction="ignore"); w.writeheader(); w.writerows(rows)
    print(json.dumps(groups, indent=2))

def main():
    p = argparse.ArgumentParser()
    p.add_argument("action", choices=["prepare", "run", "aggregate"])
    p.add_argument("--root", type=Path)
    p.add_argument("--repo", type=Path, default=Path(__file__).resolve().parent.parent)
    p.add_argument("--index", type=int)
    p.add_argument("--timeout", type=int, default=1200)
    args = p.parse_args()
    if args.action == "prepare":
        if not 1 <= args.timeout <= 3600:
            p.error("timeout must be 1..3600 seconds per backend")
        prepare(args.repo.resolve(), args.timeout)
    elif args.action == "run":
        if args.root is None or args.index is None:
            p.error("run requires --root and --index")
        raise SystemExit(run(args.root.resolve(), args.index))
    else:
        if args.root is None:
            p.error("aggregate requires --root")
        aggregate(args.root.resolve())

if __name__ == "__main__":
    main()
