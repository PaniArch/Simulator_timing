#!/usr/bin/env python3
import contextlib
import copy
import importlib.util
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest import mock

spec = importlib.util.spec_from_file_location("suite", Path(__file__).with_name("vortex-supported.py"))
suite = importlib.util.module_from_spec(spec)
spec.loader.exec_module(suite)


def evidence(mode="timing", seq=1):
    summary = dict(sequence=seq, mode=mode, launch={"StartupPC": 256}, outcome=0,
                   backing_visible=mode == "functional", execution_cycles=123 if mode == "timing" else 0,
                   flush_cycles=0, generated=1, admitted=1, completed=1)
    rows = [dict(event="launch-start", summary=dict(summary)),
            dict(event="launch-finish", summary=dict(summary))]
    if mode == "timing":
        rows.append(dict(event="cache-flush", summary=dict(summary, backing_visible=True, flush_cycles=17)))
    return rows


def write_evidence(path, mode):
    path.write_text("".join(json.dumps(r) + "\n" for r in evidence(mode)))


class SuiteTests(unittest.TestCase):
    def test_evidence_pairs_and_unknown_cycles(self):
        for mode in ("timing", "functional"):
            records = evidence(mode)
            good = suite.audit_evidence(records, mode)
            self.assertTrue(good["evidence_complete"])
            self.assertEqual(good["execution_cycles"], 123 if mode == "timing" else None)
            self.assertTrue(suite.audit_evidence(records + evidence(mode, 2), mode)["evidence_complete"])
            bad = [[], records[:-1], records[1:], records + [records[1]],
                   [None], [dict(event="launch-finish")], records[::-1],
                   records + [dict(event="error", error="failure")]]
            for key, value in (("sequence", 2), ("sequence", True), ("mode", "wrong"),
                               ("outcome", 1), ("outcome", False), ("launch", {}),
                               ("execution_cycles", None), ("execution_cycles", -1),
                               ("backing_visible", "true"), ("completed", 0), ("error", "failed"), ("error", []), ("error_origin", False)):
                changed = copy.deepcopy(records)
                changed[1]["summary"][key] = value
                bad.append(changed)
            missing = copy.deepcopy(records)
            del missing[1]["summary"]["flush_cycles"]
            bad.append(missing)
            for rows in bad:
                with self.subTest(mode=mode, rows=rows):
                    result = suite.audit_evidence(rows, mode)
                    self.assertFalse(result["evidence_complete"])
                    self.assertIsNone(result["execution_cycles"])
                    self.assertIsNone(result["flush_cycles"])
            self.assertFalse(suite.audit_evidence(records, mode, True)["evidence_complete"])
        rows = evidence()
        rows[-1]["summary"]["execution_cycles"] += 1
        self.assertFalse(suite.audit_evidence(rows, "timing")["evidence_complete"])

    def test_execute_uses_event_validation(self):
        for mode, rows, classification in (("timing", evidence(), "PASS"),
                                           ("functional", evidence("functional"), "PASS"),
                                           ("timing", evidence()[:-1], "INCOMPLETE_EVIDENCE"),
                                           ("timing", [None], "INCOMPLETE_EVIDENCE")):
            with self.subTest(mode=mode, rows=rows), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                (root / "results").mkdir()
                case = dict(index=0, mode=mode, benchmark="vecadd", argv=[])
                paths = ["lib/" + name for name in ("libvortex.so", "libvortex-simtiming.so", "libsimtiminggo.so",
                                                    "libstdc++.so.6", "libgcc_s.so.1")]
                paths += ["inputs/vecadd/vecadd", "inputs/vecadd/kernel.vxbin"]
                suite.save(root / "manifest.json", dict(cases=[case], timeout_seconds=1,
                           sha256=dict.fromkeys(paths, "digest")))
                def launch(*args, **kwargs):
                    (kwargs["cwd"] / "events.jsonl").write_text("".join(json.dumps(r) + "\n" for r in rows))
                    return mock.Mock(wait=mock.Mock(return_value=0))
                with mock.patch.object(suite, "digest", return_value="digest"), \
                     mock.patch.object(suite.shutil, "copy2"), \
                     mock.patch.object(suite.subprocess, "Popen", side_effect=launch), \
                     contextlib.redirect_stdout(io.StringIO()):
                    self.assertEqual(suite.execute(root, 0), 0 if classification == "PASS" else 1)
                result = json.loads((root / "results/0/result.json").read_text())
                self.assertEqual(result["classification"], classification)

    def test_aggregate_rechecks_pass_and_metadata(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            case = dict(index=0, mode="timing", benchmark="vecadd", argv=[])
            suite.save(root / "manifest.json", dict(cases=[case]))
            result = root / "results/0/result.json"
            result.parent.mkdir(parents=True)
            for content in ("", "null\n", "{partial", '{"event":"launch-start","event":"launch-finish"}\n', '{"sequence":NaN}\n', json.dumps(evidence()[0])):
                suite.save(result, dict(case, classification="PASS", exit_code=0, execution_cycles=0))
                (result.parent / "events.jsonl").write_text(content)
                with contextlib.redirect_stdout(io.StringIO()):
                    self.assertEqual(suite.aggregate(root), 1)
                row = json.loads((root / "summary.json").read_text())["results"][0]
                self.assertIsNone(row["execution_cycles"])
                self.assertIn("unknown", (root / "summary.tsv").read_text())
            write_evidence(result.parent / "events.jsonl", "timing")
            for bad_result in (None, [], dict(case, classification=None), dict(case, classification=[])):
                suite.save(result, bad_result)
                with contextlib.redirect_stdout(io.StringIO()):
                    self.assertEqual(suite.aggregate(root), 1)
            suite.save(result, dict(case, classification="PASS", exit_code=0, benchmark="wrong"))
            with contextlib.redirect_stdout(io.StringIO()):
                self.assertEqual(suite.aggregate(root), 1)

    def test_chains_cover_every_case_and_submit_afterany(self):
        chains = [[i for i in range(56) if (i // 2 + i % 2) % 2 == chain] for chain in range(2)]
        self.assertEqual(sorted(chains[0] + chains[1]), list(range(56)))
        for chain in chains:
            self.assertEqual(len(chain), 28)
            self.assertEqual(sum(i % 2 == 0 for i in chain), 14)
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            suite.save(root / "manifest.json", dict(chains=chains))
            with mock.patch.object(suite.subprocess, "check_output", return_value="12345\n") as submit:
                with contextlib.redirect_stdout(io.StringIO()):
                    suite.submit_chain(root, 0, 1, "12344")
                argv = submit.call_args[0][0]
                self.assertIn("--dependency=afterany:12344", argv)
                self.assertIn("--partition=debug", argv)
                self.assertEqual(submit.call_args[1]["env"]["SIMTIMING_POSITION"], "1")
                self.assertEqual(submit.call_args[1]["env"]["SIMTIMING_CASE_INDEX"], "3")
                with self.assertRaises(ValueError):
                    suite.submit_chain(root, 0, 1, "12344")
                self.assertEqual(submit.call_count, 1)

    def test_all_supported_cases_once(self):
        names = [name for name, _ in suite.CASES]
        self.assertEqual(len(names), 28)
        self.assertEqual(len(set(names)), 28)
        launcher = Path(__file__).with_name("run-vortex-benchmark.sh").read_text()
        allowlist = next(line.strip().split(")")[0].split("|") for line in launcher.splitlines() if "async_barrier|conv3" in line)
        self.assertEqual(set(names), set(allowlist))

    def test_aggregate_never_passes_missing_or_failed_results(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            cases = [dict(index=i, mode=mode, benchmark="vecadd", argv=["-n1024"])
                     for i, mode in enumerate(["timing", "functional"])]
            suite.save(root / "manifest.json", dict(cases=cases))
            with contextlib.redirect_stdout(io.StringIO()):
                self.assertEqual(suite.aggregate(root), 1)
                for case in cases:
                    result = root / "results" / str(case["index"]) / "result.json"
                    result.parent.mkdir(parents=True)
                    suite.save(result, dict(case, classification="PASS", exit_code=0))
                    write_evidence(result.parent / "events.jsonl", case["mode"])
                self.assertEqual(suite.aggregate(root), 0)
                suite.save(result, dict(cases[-1], classification="TIMEOUT", exit_code=124))
                self.assertEqual(suite.aggregate(root), 1)
            summary = json.loads((root / "summary.json").read_text())
            self.assertEqual(summary["counts"]["functional:TIMEOUT"], 1)


if __name__ == "__main__":
    unittest.main()
