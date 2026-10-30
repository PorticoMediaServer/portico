#!/usr/bin/env python3
"""Offline CI guard: every official build, test and vet command keeps the readers.

The CBR/CB7 readers only exist in a server compiled with the capability tags
`portico_rar7z,portico_cb7`. A command that quietly drops them still builds,
still signs, and still installs -- it just never admits a CBR or CB7. This checks
the commands, not a line count: it asks the real scripts for their tag list
(`--print-tags` and the shared helper's `--check`) and then audits the files that
build, test, vet or seal a server, including the checked-in QA runner and the
Makefile, not only the release and container paths.

The audit is a pure function over file text, so the negative controls mutate
copies in memory instead of the repository. Run directly:

    python3 scripts/test-official-build-tags.py
"""
import importlib.util
import pathlib
import re
import runpy
import subprocess
import sys
import unittest

ROOT = pathlib.Path(__file__).resolve().parent.parent
HELPER = ROOT / "scripts/release-build-tags.py"
BUILDER = ROOT / "scripts/build-server-release.py"
SEAL = ROOT / "scripts/seal-macos-payload.py"
MAKEFILE = ROOT / "Makefile"
RUNNER = ROOT / "scripts/runner-test.sh"
AFFECTED = ROOT / "scripts/ci-affected.py"
VET = ROOT / "scripts/vet-go.sh"
LICENCES = ROOT / "web/scripts/licences.ts"
CAPABILITY = ROOT / "server/internal/readingcap/capability.go"
CLASSIC = ROOT / "server/internal/reading/archive_classic.go"

OFFICIAL = (
    "Dockerfile",
    "ci/plan.py",
    "ci/run.py",
    "ci/release-staged/server-release.yml",
    "ci/release-staged/server-build-validation.yml",
    "Makefile",
    "scripts/runner-test.sh",
    "scripts/ci-affected.py",
    "scripts/vet-go.sh",
    "scripts/build-server-release.py",
    "scripts/seal-macos-payload.py",
    "web/scripts/licences.ts",
)
# A bare `release` in a -tags argument is the pre-capability form; the one
# remaining legitimate use is a comment or the helper's own definition.
STALE_TAG = re.compile(r"""-tags[\s"'=]+release(?![,\w])""")


def helper():
    return runpy.run_path(str(HELPER))


def job(text, name):
    """The YAML block for one workflow job, by indentation at the job level."""
    match = re.search(r"^  %s:\n(.*?)(?=^  [A-Za-z0-9_-]+:\s*$|\Z)" % re.escape(name), text, re.M | re.S)
    return match.group(1) if match else ""


def audit(sources, required):
    """Every way an official build, test or vet command can lose the capability."""
    problems = []
    dockerfile = sources["Dockerfile"]
    defaults = re.findall(r"^ARG PORTICO_GO_TAGS=(\S+)\s*$", dockerfile, re.M)
    if len(defaults) != 1:
        problems.append("Dockerfile: expected one ARG PORTICO_GO_TAGS default, found %d" % len(defaults))
    else:
        missing = sorted(required - helper()["tags"](defaults[0]))
        if missing:
            problems.append("Dockerfile: PORTICO_GO_TAGS default %r drops %s" % (defaults[0], ", ".join(missing)))
    server_builds = [line for line in dockerfile.splitlines() if "go build" in line and "./cmd/server" in line]
    if not server_builds:
        problems.append("Dockerfile: no server build command found")
    for line in server_builds:
        if '-tags "$PORTICO_GO_TAGS"' not in line:
            problems.append("Dockerfile: the server build does not pass -tags \"$PORTICO_GO_TAGS\"")

    planner = sources['ci/plan.py']
    runner = sources['ci/run.py']
    if "'scripts/release-build-tags.py','--capability'" not in planner:
        problems.append('ci planner: the tag list does not come from the capability helper')
    if "'go','list','-json','-tags',tags" not in planner:
        problems.append('ci planner: inventory drops capability tags')
    if "'--capability','--check',plan['tags']" not in runner or 'validate_tags(plan)' not in runner:
        problems.append('ci runner: tags do not fail closed')
    for command in ["'go','test','-json','-count=1','-p=4','-timeout=780s','-tags',plan['tags']", "'go','build','-p=2','-tags',tags", "'go','build','-tags',plan['tags']"]:
        if command not in runner:
            problems.append('ci runner: command drops capability tags: '+command)
    if "'sh',str(ROOT/'scripts/vet-go.sh'),'--tags',tags" not in runner:
        problems.append('ci runner: whole-module vet helper drops explicit tags')
    if "for tags in ['',plan['tags']]:" not in runner:
        problems.append('ci runner: default/capability whole-module sets missing')

    release = sources["ci/release-staged/server-release.yml"]
    if "release-build-tags.py --capability" not in release:
        problems.append("release.yml: the release test revision does not read the one tag list")
    for line in release.splitlines():
        if "go build" in line and "./cmd/server" in line and '-tags "$PORTICO_GO_TAGS"' not in line:
            problems.append("release.yml: the release helper build does not pass -tags \"$PORTICO_GO_TAGS\"")
        if "go test" in line and '-tags "$PORTICO_GO_TAGS"' not in line:
            problems.append("release.yml: the release test command does not pass -tags \"$PORTICO_GO_TAGS\"")

    for name in ("ci/release-staged/server-release.yml", "ci/release-staged/server-build-validation.yml"):
        if "scripts/build-server-release.py" not in sources[name]:
            problems.append("%s: no longer builds through the sealed release builder" % name)

    makefile = sources["Makefile"]
    if "PORTICO_GO_TAGS ?= $(shell python3 scripts/release-build-tags.py --capability)" not in makefile:
        problems.append("Makefile: PORTICO_GO_TAGS does not come from scripts/release-build-tags.py --capability")
    make_builds = [line for line in makefile.splitlines() if "go build" in line and "./cmd/server" in line]
    if not make_builds:
        problems.append("Makefile: no server build command found")
    for line in make_builds:
        if '-tags "$(PORTICO_GO_TAGS)"' not in line:
            problems.append("Makefile: the server build does not pass -tags \"$(PORTICO_GO_TAGS)\"")
    make_runs = [line for line in makefile.splitlines() if "go run" in line and "./cmd/server" in line]
    for line in make_runs:
        if '-tags "$(PORTICO_GO_TAGS)"' not in line:
            problems.append("Makefile: the server run command does not pass -tags \"$(PORTICO_GO_TAGS)\"")
    make_tests = [line for line in makefile.splitlines() if "go test" in line and "cd server" in line]
    if not make_tests:
        problems.append("Makefile: no server test command found")
    for line in make_tests:
        if '-tags "$(PORTICO_GO_TAGS)"' not in line:
            problems.append("Makefile: the server test does not pass -tags \"$(PORTICO_GO_TAGS)\"")
    if "scripts/vet-go.sh" not in makefile:
        problems.append("Makefile: the server vet helper is not invoked")

    runner = sources["scripts/runner-test.sh"]
    if "release-build-tags.py" not in runner or "--capability" not in runner:
        problems.append("runner-test.sh: the QA runner does not read the one tag list")
    if runner.count('-tags "$PORTICO_GO_TAGS"') < 2:
        problems.append("runner-test.sh: the helper build and go test do not both carry -tags \"$PORTICO_GO_TAGS\"")

    affected = sources["scripts/ci-affected.py"]
    if "PORTICO_GO_TAGS" not in affected:
        problems.append("ci-affected.py: the shard runner does not read the job's capability tags")
    if affected.count("*tag_args()") < 5:
        problems.append("ci-affected.py: go test, go list and go vet do not all carry tag_args()")

    vet = sources["scripts/vet-go.sh"]
    if "release-build-tags.py" not in vet or "--capability" not in vet or 'go vet -tags "$tags"' not in vet:
        problems.append("vet-go.sh: go vet does not carry the capability tags")

    licences = sources["web/scripts/licences.ts"]
    if "release-build-tags.py" not in licences or "--capability" not in licences:
        problems.append("licences.ts: the Go inventory does not read the one capability tag list")
    if "-tags=${tags}" not in licences:
        problems.append("licences.ts: go list -deps does not carry the capability tags")

    for name in OFFICIAL:
        for number, line in enumerate(sources[name].splitlines(), 1):
            if STALE_TAG.search(line):
                problems.append("%s:%d: a release-only tag list remains" % (name, number))
    return problems


def read_sources():
    return {name: (ROOT / name).read_text() for name in OFFICIAL}


class OfficialBuildTagTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.tags = helper()

    def test_the_checked_tag_set_carries_both_shipped_comic_readers(self):
        self.assertEqual(self.tags["value"](), "release,portico_rar7z,portico_cb7")
        self.assertEqual(self.tags["value"](release=False), "portico_rar7z,portico_cb7")
        self.assertEqual(self.tags["required"](), frozenset({"release", "portico_rar7z", "portico_cb7"}))
        self.assertTrue(self.tags["required"](release=False) <= self.tags["required"]())
        self.assertTrue(self.tags["covers"]("release,portico_rar7z,portico_cb7"))
        self.assertTrue(self.tags["covers"]("portico_rar7z,portico_cb7", False))
        self.assertFalse(self.tags["covers"]("release,portico_rar7z"))
        self.assertFalse(self.tags["covers"]("release"))

    def test_the_real_helper_accepts_the_list_and_refuses_a_stale_one(self):
        def run(*arguments):
            return subprocess.run([sys.executable, str(HELPER), *arguments], capture_output=True, text=True, timeout=60)
        self.assertEqual(run().stdout.strip(), "release,portico_rar7z,portico_cb7")
        self.assertEqual(run("--capability").stdout.strip(), "portico_rar7z,portico_cb7")
        self.assertEqual(run("--check", "release,portico_rar7z,portico_cb7").returncode, 0)
        self.assertEqual(run("--check", "release,portico_rar7z portico_cb7").returncode, 0)
        self.assertEqual(run("--check", "release with,spaces").returncode, 1)
        refused = run("--check", "release")
        self.assertEqual(refused.returncode, 1)
        self.assertIn("portico_rar7z", refused.stderr)

    def test_the_release_builder_dry_run_uses_the_same_list(self):
        result = subprocess.run([sys.executable, str(BUILDER), "--print-tags"],
                                capture_output=True, text=True, timeout=60)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.strip(), self.tags["value"]())

        specification = importlib.util.spec_from_file_location("release_builder_tags", BUILDER)
        builder = importlib.util.module_from_spec(specification)
        specification.loader.exec_module(builder)
        self.assertEqual(builder.build_tags("server"), self.tags["value"]())
        self.assertTrue(builder.tags_are_official("server", self.tags["value"]()))
        self.assertTrue(builder.tags_are_official("server", "portico_rar7z release portico_cb7"))
        self.assertFalse(builder.tags_are_official("server", "release"))

    def test_the_macos_seal_uses_the_same_check(self):
        specification = importlib.util.spec_from_file_location("seal_tags", SEAL)
        seal = importlib.util.module_from_spec(specification)
        specification.loader.exec_module(seal)
        self.assertIs(seal.tags_are_official, seal.BUILDER["tags_are_official"])
        self.assertTrue(seal.tags_are_official("server", self.tags["value"]()))
        self.assertFalse(seal.tags_are_official("server", "release"))

    def test_the_reader_modules_are_direct_pinned_requirements(self):
        # Both readers are imported by server source, so neither may be marked
        # indirect; the strict source pin is the version named here.
        text = (ROOT / "server/go.mod").read_text()
        for module, version in (("github.com/bodgit/sevenzip", "v1.6.5"),
                                ("github.com/nwaples/rardecode/v2", "v2.4.5")):
            line = next((line for line in text.splitlines()
                         if line.strip().startswith(module + " ")), "")
            self.assertIn(version, line, module)
            self.assertNotIn("// indirect", line, module)

    def test_cb7_tag_does_not_open_the_in_process_reader(self):
        # The ruling authorises the tag, not a second reader: CB7 still requires
        # its platform gate and the bounded worker, and the in-process path refuses.
        capability = CAPABILITY.read_text()
        classic = CLASSIC.read_text()
        self.assertIn("func CB7() bool { return buildEnabled() && platformSupportsCB7() }", capability)
        self.assertIn('format == "cbr"', classic)
        self.assertIn("CB7 stays closed", classic)
        self.assertIn("archiveFormatError(format)", classic)

    def test_every_official_command_carries_the_tag(self):
        self.assertEqual(audit(read_sources(), self.tags["required"]()), [])

    def test_negative_control_a_dockerfile_that_drops_the_tag_fails(self):
        sources = read_sources()
        sources["Dockerfile"] = sources["Dockerfile"].replace(
            "ARG PORTICO_GO_TAGS=release,portico_rar7z,portico_cb7", "ARG PORTICO_GO_TAGS=release")
        problems = audit(sources, self.tags["required"]())
        self.assertTrue(any(p.startswith("Dockerfile: PORTICO_GO_TAGS default") for p in problems), problems)

    def test_negative_control_a_build_line_that_drops_the_flag_fails(self):
        sources = read_sources()
        sources["Dockerfile"] = sources["Dockerfile"].replace('-tags "$PORTICO_GO_TAGS"', "-tags release")
        problems = audit(sources, self.tags["required"]())
        self.assertTrue(any("does not pass -tags" in p for p in problems), problems)
        self.assertTrue(any("release-only tag list" in p for p in problems), problems)

    def test_negative_control_ci_that_stops_using_the_helper_fails(self):
        sources = read_sources()
        sources['ci/plan.py'] = sources['ci/plan.py'].replace("'scripts/release-build-tags.py','--capability'", "'hardcoded-tags'")
        sources['ci/run.py'] = sources['ci/run.py'].replace("'go','build','-p=2','-tags',tags", "'go','build'").replace("'--capability','--check',plan['tags']", "'--capability'")
        problems = audit(sources, self.tags['required']())
        self.assertTrue(any(p.startswith('ci planner: the tag list') for p in problems), problems)
        self.assertTrue(any(p.startswith('ci runner: command drops') for p in problems), problems)
        self.assertTrue(any('fail closed' in p for p in problems), problems)

    def test_negative_control_makefile_test_without_the_tags_fails(self):
        sources = read_sources()
        sources["Makefile"] = sources["Makefile"].replace(
            'go test -tags "$(PORTICO_GO_TAGS)" ./...', "go test ./...")
        problems = audit(sources, self.tags["required"]())
        self.assertTrue(any(p.startswith("Makefile: the server test") for p in problems), problems)

    def test_negative_control_shard_runner_that_drops_the_tags_fails(self):
        sources = read_sources()
        sources["scripts/ci-affected.py"] = sources["scripts/ci-affected.py"].replace(
            'argv = ["go", "test", *tag_args(), "-json"', 'argv = ["go", "test", "-json"')
        problems = audit(sources, self.tags["required"]())
        self.assertTrue(any(p.startswith("ci-affected.py:") for p in problems), problems)


if __name__ == "__main__":
    unittest.main()
