#!/usr/bin/env python3
import contextlib
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


class SuiteTests(unittest.TestCase):
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
                self.assertEqual(suite.aggregate(root), 0)
                suite.save(result, dict(cases[-1], classification="TIMEOUT", exit_code=124))
                self.assertEqual(suite.aggregate(root), 1)
            summary = json.loads((root / "summary.json").read_text())
            self.assertEqual(summary["counts"]["functional:TIMEOUT"], 1)


if __name__ == "__main__":
    unittest.main()
