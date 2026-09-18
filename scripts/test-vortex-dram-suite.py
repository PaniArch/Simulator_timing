#!/usr/bin/env python3
"""Regression tests for expanded-suite parsing and accuracy eligibility."""
import contextlib
import gzip
import importlib.util
import io
import json
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("dram_suite", Path(__file__).with_name("vortex-dram-suite.py"))
suite = importlib.util.module_from_spec(spec)
spec.loader.exec_module(suite)

class SuiteTests(unittest.TestCase):
    def test_last_perf_is_cumulative_not_sum(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "stdout.log.gz"
            with gzip.open(path, "wt") as f:
                f.write("PERF: instrs=10, cycles=20\nPERF: instrs=30, cycles=45\nPASSED!\n")
            result = suite.parse_stdout(path)
            self.assertTrue(result["host_pass"])
            self.assertEqual(result["perf"][-1], dict(instructions=30, cycles=45))

    def test_ineligible_cases_do_not_improve_mape(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            cases = [dict(index=i, scale="small", benchmark="case"+str(i), argv=[]) for i in range(5)]
            suite.save(root / "manifest.json", dict(cases=cases))
            for i in range(4):
                d = root / "results" / str(i)
                d.mkdir(parents=True)
                a = dict(passed=i != 3, seconds=1, perf=[dict(instructions=1 if i != 2 else 2, cycles=110 if i == 0 else 100)], timing_issues=["mixed"] if i == 1 else [])
                b = dict(passed=True, seconds=2, perf=[dict(instructions=1, cycles=100)])
                suite.save(d / "result.json", dict(backends=dict(simtiming=a, rtlsim=b)))
            with contextlib.redirect_stdout(io.StringIO()):
                suite.aggregate(root)
            summary = json.loads((root / "summary.json").read_text())
            g = summary["groups"]["small"]
            self.assertEqual((g["total"], g["passed"], g["pending"], g["eligible"]), (5, 3, 1, 1))
            self.assertAlmostEqual(g["mape_percent"], 10)
            self.assertAlmostEqual(g["weighted_abs_percent"], 10)
            self.assertAlmostEqual(g["p95_abs_percent"], 10)
            self.assertEqual(g["within_5"], 0)

    def test_expansion_is_not_fake_packld_duplicate(self):
        self.assertEqual(len(suite.EXPANDED), 30)
        self.assertNotIn("packld", suite.EXPANDED)
        self.assertEqual(suite.EXPANDED["wsync"], "-i128")

if __name__ == "__main__":
    unittest.main()
