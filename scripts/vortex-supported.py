#!/usr/bin/env python3
"""Frozen native-runtime suite inputs, per-case execution and complete aggregation."""
import argparse
import datetime
import hashlib
import json
import os
from pathlib import Path
import shutil
import signal
import subprocess
import tempfile
import time

# Same supported cases and medium-sized arguments as Simulator_dev's suite.
CASES = [
    ("async_barrier", "-n32 -t4"), ("conv3", "-n32 -l"),
    ("demo", "-n64 -x4 -y4"), ("diverge", "-n64 -d8"),
    ("dogfood", "-n64 -s0 -e21 -c"), ("dotproduct", "-n1024"),
    ("dotproduct2", "-n1024"), ("dropout", "-n1024"),
    ("fence", "-n64"), ("io_addr", "-n64"), ("jacobi", "-n64"),
    ("madmax", "-n32"), ("mstress", "-n64"), ("multikernel", "-n1024"),
    ("occupancy", "-c8"), ("packld", ""), ("pathfinder", "-n32"),
    ("raycast", "-n3 -w32 -h24 -s1 -d1"), ("relu", "-n1024"),
    ("sgemm", "-n32"), ("sgemm2", "-n32 -t4 -c8"), ("sgemmx", "-n32"),
    ("sgemv", "-m64 -n64"), ("softmax", "-n32"), ("sort", "-n32"),
    ("stencil3d", "-n16"), ("vecadd", "-n1024"), ("wgather", "-n8 -t4"),
]


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def save(path, data):
    temporary = path.with_suffix(path.suffix + ".tmp")
    temporary.write_text(json.dumps(data, indent=2) + "\n")
    temporary.replace(path)


def prepare(repo, build, timeout):
    if not 1 <= timeout <= 1500:
        raise ValueError("per-case timeout must be 1..1500 seconds for debug jobs")
    sources = {
        "lib/libvortex.so": build / "sw/runtime/libvortex.so",
        "lib/libvortex-simtiming.so": repo / ".cache/vortex-runtime/libvortex-simtiming.so",
        "lib/libsimtiminggo.so": repo / ".cache/vortex-runtime/libsimtiminggo.so",
        "runner.py": Path(__file__).resolve(),
        "run.sbatch": repo / "integration/vortex-runtime/run-supported.sbatch",
    }
    for library in ("libstdc++.so.6", "libgcc_s.so.1"):
        location = subprocess.check_output(["g++", "-print-file-name=" + library], text=True).strip()
        if not Path(location).is_absolute():
            raise ValueError("load the GCC runtime before preparing: " + library)
        sources["lib/" + library] = Path(location)
    for name, _ in CASES:
        for file in (name, "kernel.vxbin"):
            sources["inputs/" + name + "/" + file] = build / "tests/regression" / name / file
    for relative, source in sources.items():
        if not source.is_file() or not source.stat().st_size:
            raise ValueError("missing artifact: " + str(source))
        if relative.startswith("inputs/") and not relative.endswith(".vxbin") and not os.access(source, os.X_OK):
            raise ValueError("benchmark is not executable: " + str(source))
    parent = repo / ".cache/runtime-supported"
    parent.mkdir(parents=True, exist_ok=True)
    stamp = datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%dT%H%M%SZ-")
    root = Path(tempfile.mkdtemp(prefix=stamp, dir=parent))
    hashes = {}
    for relative, source in sources.items():
        target = root / relative
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(source, target)
        hashes[relative] = digest(target)
    cases = []
    for name, arguments in CASES:
        for mode in ("timing", "functional"):
            cases.append(dict(index=len(cases), benchmark=name, mode=mode, argv=arguments.split()))
    chains = [[c["index"] for c in cases if (c["index"] // 2 + c["index"] % 2) % 2 == chain] for chain in range(2)]
    save(root / "manifest.json", dict(cases=cases, chains=chains, timeout_seconds=timeout,
         external_latency_cycles=100, sha256=hashes, repository=str(repo), build=str(build),
         baseline_head=subprocess.check_output(["git", "-C", str(repo), "rev-parse", "HEAD"], text=True).strip()))
    print(root)


def submit_chain(root, chain, position, parent=None):
    manifest = json.loads((root / "manifest.json").read_text())
    indices = manifest["chains"][chain]
    if position >= len(indices):
        print("CHAIN_COMPLETE=" + str(chain), flush=True)
        return
    registration = root / ("chain-%d-%d.submission.json" % (chain, position))
    if registration.exists():
        raise ValueError("chain position already submitted: " + str(registration))
    argv = ["/opt/slurm/bin/sbatch", "--parsable", "--partition=debug", "--time=00:30:00",
            "--signal=B:TERM@60", "--chdir=" + str(root),
            "--export=ALL",
            "--output=" + str(root / "slurm-%j.out"), "--error=" + str(root / "slurm-%j.err")]
    if parent:
        argv.append("--dependency=afterany:" + parent)
    argv.append(str(root / "run.sbatch"))
    # Set both initial and child values in the submitting environment. With
    # --export=ALL, inherited values can take precedence over inline assignments.
    env = os.environ.copy()
    env.update(SIMTIMING_SUITE_ROOT=str(root), SIMTIMING_CHAIN=str(chain),
               SIMTIMING_POSITION=str(position), SIMTIMING_CASE_INDEX=str(indices[position]))
    submission = subprocess.check_output(argv, text=True, env=env).strip()
    job = submission.split(";")[0]
    save(registration, dict(job_id=job, chain=chain, position=position, index=indices[position], parent=parent))
    print("JOB_ID=%s CHAIN=%d POSITION=%d INDEX=%d" % (job, chain, position, indices[position]), flush=True)


def execute(root, index):
    manifest = json.loads((root / "manifest.json").read_text())
    case = manifest["cases"][index]
    if index != case["index"]:
        raise ValueError("manifest index mismatch")
    directory = root / "results" / str(index)
    directory.mkdir(parents=True, exist_ok=False)
    result = dict(case, job_id=os.getenv("SLURM_JOB_ID"), node=os.uname().nodename,
                  classification="EXTERNAL_CONNECTION", exit_code=None)
    start = time.monotonic()
    try:
        selected = ["lib/libvortex.so", "lib/libvortex-simtiming.so", "lib/libsimtiminggo.so",
                    "lib/libstdc++.so.6", "lib/libgcc_s.so.1",
                    "inputs/" + case["benchmark"] + "/" + case["benchmark"],
                    "inputs/" + case["benchmark"] + "/kernel.vxbin"]
        for relative in selected:
            if digest(root / relative) != manifest["sha256"][relative]:
                raise ValueError("artifact digest changed: " + relative)
        executable = root / "inputs" / case["benchmark"] / case["benchmark"]
        shutil.copy2(executable.parent / "kernel.vxbin", directory / "kernel.vxbin")
        env = os.environ.copy()
        env.update(VORTEX_DRIVER="simtiming", SIMTIMING_MODE=case["mode"],
                   SIMTIMING_EVENT_LOG=str(directory / "events.jsonl"),
                   GOMAXPROCS=os.getenv("SLURM_CPUS_PER_TASK", "4"),
                   LD_LIBRARY_PATH=str(root / "lib"))
        timed_out = False
        with (directory / "stdout.log").open("wb") as out, (directory / "stderr.log").open("wb") as err:
            process = subprocess.Popen([str(executable)] + case["argv"], cwd=directory,
                                       env=env, stdout=out, stderr=err, start_new_session=True)
            try:
                code = process.wait(timeout=manifest["timeout_seconds"])
            except subprocess.TimeoutExpired:
                timed_out = True
                os.killpg(process.pid, signal.SIGTERM)
                try:
                    process.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    os.killpg(process.pid, signal.SIGKILL)
                    process.wait()
                code = 124
        result["exit_code"] = code
        records = []
        malformed = False
        events = directory / "events.jsonl"
        if events.exists():
            for line in events.read_text().splitlines():
                if not line.strip():
                    continue
                try:
                    records.append(json.loads(line))
                except json.JSONDecodeError:
                    malformed = True
        errors = (directory / "stderr.log").read_text(errors="replace") + json.dumps(records)
        launches = [r for r in records if r.get("event") == "launch-start"]
        finishes = [r["summary"] for r in records if r.get("event") == "launch-finish"]
        flushes = [r["summary"] for r in records if r.get("event") == "cache-flush"]
        result.update(launches=len(launches), finishes=len(finishes),
                      execution_cycles=sum(s.get("execution_cycles", 0) for s in finishes),
                      flush_cycles=sum(s.get("flush_cycles", 0) for s in flushes))
        visible = {s["sequence"] for s in finishes + flushes if s.get("backing_visible")}
        if timed_out:
            result["classification"] = "TIMEOUT"
        elif "simulator-internal" in errors:
            result["classification"] = "SIMULATOR_INTERNAL"
        elif "external-connection" in errors:
            result["classification"] = "EXTERNAL_CONNECTION"
        elif any(marker in errors for marker in ("GLIBCXX_", "GLIBC_", "error while loading shared libraries")):
            result["classification"] = "EXTERNAL_CONNECTION"
            result["runner_error"] = "dynamic-loader environment failure; chain paused"
        elif code != 0:
            result["classification"] = "HOST_OR_UNKNOWN"
        elif malformed or not launches or len(launches) != len(finishes) or any(s["sequence"] not in visible or s.get("mode") != case["mode"] for s in finishes):
            result["classification"] = "INCOMPLETE_EVIDENCE"
        else:
            result["classification"] = "PASS"
        for relative in selected:
            if digest(root / relative) != manifest["sha256"][relative]:
                raise ValueError("artifact changed during execution: " + relative)
    except Exception as error:
        result["classification"] = "EXTERNAL_CONNECTION"
        result["runner_error"] = repr(error)
    result["elapsed_seconds"] = round(time.monotonic() - start, 3)
    save(directory / "result.json", result)
    print(json.dumps(result), flush=True)
    if result.get("runner_error"):
        return 2
    return 0 if result["classification"] == "PASS" else 1


def aggregate(root):
    manifest = json.loads((root / "manifest.json").read_text())
    rows = []
    counts = {}
    for case in manifest["cases"]:
        file = root / "results" / str(case["index"]) / "result.json"
        row = json.loads(file.read_text()) if file.exists() else dict(case, classification="NOT_REPORTED")
        rows.append(row)
        key = row["mode"] + ":" + row["classification"]
        counts[key] = counts.get(key, 0) + 1
    save(root / "summary.json", dict(expected=len(rows), counts=counts, results=rows))
    fields = ["index", "mode", "benchmark", "classification", "exit_code", "elapsed_seconds", "execution_cycles", "flush_cycles"]
    with (root / "summary.tsv").open("w") as output:
        output.write("\t".join(fields) + "\n")
        for row in rows:
            output.write("\t".join(str(row.get(f, "")) for f in fields) + "\n")
    print(json.dumps(counts, indent=2))
    print("SUMMARY=" + str(root / "summary.tsv"))
    return 0 if all(row["classification"] == "PASS" for row in rows) else 1


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("command", choices=["prepare", "run", "aggregate", "submit", "advance"])
    parser.add_argument("--root", type=Path)
    parser.add_argument("--repository", type=Path)
    parser.add_argument("--build", type=Path)
    parser.add_argument("--timeout", type=int, default=1500)
    parser.add_argument("--index", type=int)
    parser.add_argument("--chain", type=int)
    parser.add_argument("--position", type=int)
    parser.add_argument("--parent")
    args = parser.parse_args()
    if args.command == "prepare":
        prepare(args.repository.resolve(), args.build.resolve(), args.timeout)
    elif args.command == "run":
        raise SystemExit(execute(args.root.resolve(), args.index))
    elif args.command == "submit":
        for chain in range(2):
            submit_chain(args.root.resolve(), chain, 0)
    elif args.command == "advance":
        submit_chain(args.root.resolve(), args.chain, args.position + 1, args.parent)
    else:
        raise SystemExit(aggregate(args.root.resolve()))
