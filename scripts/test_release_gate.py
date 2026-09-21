"""Release barriers: real policy/metadata, injected tool failures, and workflow wiring."""
import copy
from concurrent.futures import ThreadPoolExecutor
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

import release_metadata
from require_quality import require_success

ROOT = Path(__file__).resolve().parents[1]
SHA = "a" * 40
CANDIDATE = "b" * 40


def run(*args, cwd=None, env=None):
    return subprocess.run(args, cwd=cwd, env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)


def initialize(path):
    for args in [("init", "-q"), ("config", "user.name", "Test"), ("config", "user.email", "test@example.invalid")]:
        result = run("git", *args, cwd=path)
        assert result.returncode == 0, result.stdout


def commit(path, message):
    assert run("git", "add", ".", cwd=path).returncode == 0
    result = run("git", "commit", "-qm", message, cwd=path)
    assert result.returncode == 0, result.stdout
    return run("git", "rev-parse", "HEAD", cwd=path).stdout.strip()


class PolicyTests(unittest.TestCase):
    def setUp(self):
        self.needs = {key: {"result": "success", "outputs": {"audited_sha": SHA}} for key in ("quality", "security")}

    def test_only_two_exact_successes_pass(self):
        self.assertEqual(require_success(self.needs, SHA), SHA)

    def test_every_non_success_is_blocked(self):
        for job in self.needs:
            for status in ("failure", "cancelled", "skipped", "", None, True, "in_progress"):
                with self.subTest(job=job, status=status), self.assertRaises(ValueError):
                    needs = copy.deepcopy(self.needs)
                    needs[job]["result"] = status
                    require_success(needs, SHA)

    def test_wrong_or_missing_sha_is_blocked(self):
        for job in self.needs:
            for outputs in ({}, None, {"audited_sha": CANDIDATE}, {"audited_sha": ""}):
                with self.subTest(job=job, outputs=outputs), self.assertRaises(ValueError):
                    needs = copy.deepcopy(self.needs)
                    needs[job]["outputs"] = outputs
                    require_success(needs, SHA)

    def test_missing_malformed_extra_jobs_are_blocked(self):
        for needs in ({}, [], None, {"quality": self.needs["quality"]}, {**self.needs, "other": {}}, {"quality": None, "security": {}}):
            with self.subTest(needs=needs), self.assertRaises(ValueError):
                require_success(needs, SHA)
        for sha in ("main", "", "a" * 39, None):
            with self.assertRaises(ValueError):
                require_success(self.needs, sha)

    def test_cli_does_not_emit_approval_on_invalid_json(self):
        with tempfile.TemporaryDirectory() as tmp:
            out = Path(tmp) / "output"
            env = dict(os.environ, NEEDS_JSON="not-json", GITHUB_SHA=SHA, GITHUB_OUTPUT=str(out))
            result = run("python3", str(ROOT / "scripts/require_quality.py"), env=env)
            self.assertNotEqual(result.returncode, 0)
            self.assertFalse(out.exists())


class MetadataTests(unittest.TestCase):
    def test_release_skip_markers(self):
        for message in ("fix: x [skip release]", "fix: x [NO-RELEASE]", "fix: x [norelease]", "chore: bump version to v0.0.22 [skip ci]"):
            self.assertFalse(release_metadata.eligible(message))
        self.assertTrue(release_metadata.eligible("fix: release checks"))

    def test_version_selection(self):
        tags = ["v0.0.9", "v0.0.22", "other", "v100.0.0-beta"]
        self.assertEqual(release_metadata.next_version(tags, "fix: x"), "v0.0.23")
        self.assertEqual(release_metadata.next_version(tags, "feat: x"), "v0.1.0")
        self.assertEqual(release_metadata.next_version(tags, "feat(core)!: x"), "v1.0.0")
        self.assertEqual(release_metadata.next_version([], "fix: first"), "v0.0.1")

    def test_candidate_is_local_exact_child_with_only_metadata_changes(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            initialize(root)
            (root / "version.go").write_text('package mlog\nconst Version = "0.0.22"\n')
            (root / "go.mod").write_text('module github.com/PinableAgents/mlog\n')
            (root / "README.md").write_text('go get github.com/PinableAgents/mlog@v0.0.22\n')
            source = commit(root, "fix: gates")
            self.assertEqual(run("git", "tag", "v0.0.22", cwd=root).returncode, 0)
            env = dict(os.environ, GITHUB_OUTPUT=str(root / "outputs"))
            result = run("python3", str(ROOT / "scripts/release_metadata.py"), "prepare", "--source", source, "--notes", str(root / "notes"), cwd=root, env=env)
            self.assertEqual(result.returncode, 0, result.stdout)
            self.assertEqual(run("git", "rev-parse", "HEAD^", cwd=root).stdout.strip(), source)
            self.assertEqual(set(run("git", "diff", "--name-only", "HEAD^", "HEAD", cwd=root).stdout.splitlines()), {"version.go", "README.md"})
            self.assertIn('Version = "0.0.23"', (root / "version.go").read_text())
            self.assertEqual(run("git", "tag", "--list", cwd=root).stdout.strip(), "v0.0.22")
            self.assertIn("candidate_sha=", (root / "outputs").read_text())
            dirty = root / "version.go"
            dirty.write_text(dirty.read_text() + "// dirty\n")
            source = run("git", "rev-parse", "HEAD", cwd=root).stdout.strip()
            result = run("python3", str(ROOT / "scripts/release_metadata.py"), "prepare", "--source", source, "--notes", str(root / "notes"), cwd=root, env=env)
            self.assertNotEqual(result.returncode, 0)


# These are explicitly fake tools, used only in isolated temporary repositories.
FAKE_GO = '''#!/usr/bin/env python3
import os,sys
from pathlib import Path
a=sys.argv[1:]
bench=Path.cwd().name == "benchmarks"
if a[0] == "version": print("go version test-fixture");sys.exit(0)
if a[0] == "env": print("fixture");sys.exit(0)
if a[0] == "mod": stage="benchmark-modules" if bench else "modules"
elif a[0] == "install": stage="scanner-install"
elif a[0] == "tool": stage="cover-report"
elif a[0] == "vet": stage="benchmark-vet" if bench else "vet"
elif any(x.startswith("-coverprofile=") for x in a): stage="coverage"
elif "-fuzz" in a: stage="fuzz"
elif "-bench" in a: stage="benchmarks"
else: stage="benchmark-correctness" if bench else "race"
with open(os.environ["TRACE"],"a") as f: f.write(stage+"\\n")
if stage == os.environ.get("FAIL_STAGE"): sys.exit(37)
if stage == "coverage":
 p=next(x.split("=",1)[1] for x in a if x.startswith("-coverprofile="))
 count="0" if os.environ.get("FAIL_STAGE")=="coverage-gap" else "1"
 Path(p).write_text("mode: atomic\\nexample/x.go:1.1,2.1 1 "+count+"\\n")
if stage == "scanner-install":
 p=Path(os.environ["GOBIN"])/"govulncheck"
 p.write_text('#!/bin/sh\\necho scanner >> "$TRACE"\\n[ "$FAIL_STAGE" != scanner ]\\n')
 p.chmod(0o755)
'''


class FailClosedCommandTests(unittest.TestCase):
    def check_script(self, name, fail_stage):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp) / "repo"
            root.mkdir()
            initialize(root)
            (root / "x.go").write_text("package example\n")
            (root / "benchmarks").mkdir()
            (root / "benchmarks/go.mod").write_text("module example/benchmarks\n")
            (root / "scripts").mkdir()
            (root / "scripts/test_fixture.py").write_text("import unittest\nclass Fixture(unittest.TestCase):\n def test_ok(self): self.assertTrue(True)\n")
            (root / "scripts/check_coverage.py").write_bytes((ROOT / "scripts/check_coverage.py").read_bytes())
            commit(root, "fixture")
            tools = Path(tmp) / "tools"
            tools.mkdir()
            for n, text in {"go": FAKE_GO, "gofmt": '#!/bin/sh\n[ "$FAIL_STAGE" != format ] || echo unformatted.go\n'}.items():
                p = tools / n
                p.write_text(text)
                p.chmod(0o755)
            out = Path(tmp) / "results"
            trace = Path(tmp) / "trace"
            env = dict(os.environ, PATH=str(tools) + os.pathsep + os.environ["PATH"], FAIL_STAGE=fail_stage, TRACE=str(trace))
            result = run("bash", str(ROOT / "scripts" / name), str(out), cwd=root, env=env)
            if fail_stage:
                self.assertNotEqual(result.returncode, 0, (fail_stage, result.stdout))
                self.assertFalse((out / "validated-sha.txt").exists(), result.stdout)
            else:
                self.assertEqual(result.returncode, 0, result.stdout)
                self.assertTrue((out / "validated-sha.txt").exists())

    def test_quality_pass_and_every_failure_stage(self):
        stages = ("", "format", "modules", "coverage", "coverage-gap", "cover-report", "race", "vet", "fuzz", "benchmark-modules", "benchmark-correctness", "benchmark-vet", "benchmarks")
        with ThreadPoolExecutor(max_workers=6) as pool:
            futures = [(stage, pool.submit(self.check_script, "quality_gate.sh", stage)) for stage in stages]
            for stage, future in futures:
                with self.subTest(stage=stage):
                    future.result()

    def test_security_pass_install_scan_and_race_failures(self):
        with ThreadPoolExecutor(max_workers=4) as pool:
            futures = [(stage, pool.submit(self.check_script, "security_gate.sh", stage)) for stage in ("", "race", "scanner-install", "scanner")]
            for stage, future in futures:
                with self.subTest(stage=stage):
                    future.result()


class WorkflowTests(unittest.TestCase):
    def test_dependency_permissions_and_checked_ref(self):
        release = (ROOT / ".github/workflows/release.yml").read_text()
        audit = (ROOT / ".github/workflows/quality-audit.yml").read_text()
        self.assertIn("workflow_call:", audit)
        self.assertIn("needs: [quality, security]", audit)
        self.assertIn("python3 scripts/require_quality.py", audit)
        self.assertIn("needs: [preflight, audit]", release)
        self.assertIn("needs.audit.result == 'success'", release)
        self.assertIn("needs.audit.outputs.audited_sha == github.sha", release)
        self.assertEqual(release.count("contents: write"), 1)
        self.assertNotIn("contents: write", audit)
        self.assertNotIn("continue-on-error:", release + audit)
        self.assertNotIn("secrets: inherit\n", release)
        self.assertIn("uses: ./.github/workflows/quality-audit.yml", release)
        self.assertIn("cancel-in-progress: false", release)
        self.assertEqual(audit.count("ref: ${{ github.sha }}"), 3)

    def test_final_candidate_rechecked_before_any_publication(self):
        text = (ROOT / ".github/workflows/release.yml").read_text()
        self.assertLess(text.index("scripts/release_metadata.py prepare"), text.index("scripts/quality_gate.sh"))
        self.assertLess(text.index("scripts/quality_gate.sh"), text.index("scripts/security_gate.sh"))
        self.assertLess(text.index("scripts/security_gate.sh"), text.index("git tag -a"))
        self.assertIn("git push --atomic", text)
        self.assertNotIn("--force", text)
        self.assertIn("--verify-tag", text)
        self.assertNotIn("go fmt", text)
        self.assertNotIn("actions/create-release", text)

    def test_manual_release_has_no_bypass(self):
        text = (ROOT / "release.sh").read_text()
        main = text[text.index("main() {"):]
        self.assertLess(main.index('git commit -m'), main.index("    run_tests\n"))
        self.assertLess(main.index("    run_tests\n"), main.index('    create_release'))
        self.assertIn("scripts/quality_gate.sh", text)
        self.assertIn("scripts/security_gate.sh", text)
        self.assertNotIn("go fmt ./...", text)
        self.assertIn("git push --atomic", text)

    def test_publish_shell_aborts_on_stale_dirty_mismatch_or_push_failure(self):
        text = (ROOT / ".github/workflows/release.yml").read_text()
        block = text.split("      - name: Publish only the exact fully validated candidate\n", 1)[1]
        block = block.split("        run: |\n", 1)[1].split("      - uses:", 1)[0]
        shell = "\n".join(line[10:] for line in block.splitlines()) + "\n"
        for failure in ("", "stale", "dirty", "wrong-quality", "wrong-security", "push"):
            with self.subTest(failure=failure), tempfile.TemporaryDirectory() as tmp:
                p = Path(tmp)
                for folder in ("candidate-quality", "candidate-security"):
                    (p / folder).mkdir()
                    wrong = failure == "wrong-" + folder.removeprefix("candidate-")
                    (p / folder / "validated-sha.txt").write_text(SHA if wrong else CANDIDATE)
                trace = p / "trace"
                fake_git = '''#!/usr/bin/env python3
import os,sys
a=sys.argv[1:]
if a[0] == "rev-parse": print(os.environ["SOURCE_SHA"] if a[1]=="HEAD^" else os.environ["CANDIDATE_SHA"])
elif a[0]=="diff": sys.exit(1 if os.environ["FAIL"]=="dirty" else 0)
elif a[0]=="ls-remote": print(("c"*40 if os.environ["FAIL"]=="stale" else os.environ["SOURCE_SHA"])+"\\trefs/heads/main")
elif a[0]=="push":
 with open(os.environ["TRACE"],"a") as f:f.write("push\\n")
 sys.exit(1 if os.environ["FAIL"]=="push" else 0)
elif a[0]=="tag":
 with open(os.environ["TRACE"],"a") as f:f.write("tag\\n")
else: sys.exit(98)
'''
                for name, body in {"git": fake_git, "gh": '#!/bin/sh\necho release >> "$TRACE"\n'}.items():
                    f = p / name
                    f.write_text(body)
                    f.chmod(0o755)
                env = dict(os.environ, PATH=tmp + os.pathsep + os.environ["PATH"], RUNNER_TEMP=tmp, SOURCE_SHA=SHA, CANDIDATE_SHA=CANDIDATE, RELEASE_BRANCH="main", RELEASE_VERSION="v0.0.23", FAIL=failure, TRACE=str(trace))
                result = run("bash", "-c", shell, env=env)
                actions = trace.read_text().splitlines() if trace.exists() else []
                if failure:
                    self.assertNotEqual(result.returncode, 0, result.stdout)
                    self.assertNotIn("release", actions)
                    if failure != "push":
                        self.assertEqual(actions, [])
                else:
                    self.assertEqual(result.returncode, 0, result.stdout)
                    self.assertEqual(actions, ["tag", "push", "release"])


if __name__ == "__main__":
    unittest.main()
