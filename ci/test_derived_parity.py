from pathlib import Path
import re
import unittest


ROOT = Path(__file__).resolve().parents[1]


def parity_contract(text):
    """A required context must exist and run the real checks on both events."""
    events = text.split('on:\n', 1)[1].split('\npermissions:', 1)[0]
    for event in ('pull_request', 'merge_group'):
        if not re.search(r'^  ' + event + r':\s*$', events, re.M):
            raise ValueError('missing unconditional ' + event)
    if re.search(r'(?m)^\s+(paths|paths-ignore|branches|branches-ignore|types):', events):
        raise ValueError('filtered event can omit the required context')
    job = text.split('  parity:\n', 1)[1]
    if not re.search(r'^    name: Derived data parity$', job, re.M):
        raise ValueError('unstable required context')
    if re.search(r'(?m)^\s+(if|continue-on-error):', job):
        raise ValueError('parity can skip or hide a failure')
    commands = [
        "go test -v -p 1 -run '^TestDerived(TableWriters|WriterGuard)' ./internal/persistence",
        'go test -p 1 -v ./internal/derivedtest',
        "go test -p 1 -run '^Test(NoArbitrarySQLCallbackSurfaces|SQLCallbackGuardCatchesSyntheticViolations|SearchMatchPureSQLCallback)$' ./internal/dbwork",
        "go test -p 1 -run '^TestCatalogRebuildCommand' ./cmd/server",
    ]
    for command in commands:
        if 'run: ' + command + '\n' not in job:
            raise ValueError('missing or masked parity command: ' + command)


class DerivedParityWorkflow(unittest.TestCase):
    def test_required_context_runs_on_every_pr_and_merge_group(self):
        parity_contract((ROOT / '.github/workflows/derived-parity.yml').read_text())

    def test_filters_skip_and_masked_failures_are_rejected(self):
        original = (ROOT / '.github/workflows/derived-parity.yml').read_text()
        mutations = [
            original.replace('  pull_request:', '  push:'),
            original.replace('  merge_group:', '  push:'),
            original.replace('  pull_request:', '  pull_request:\n    paths: [server/**]'),
            original.replace('  merge_group:', '  merge_group:\n    branches: [main]'),
            original.replace('    name: Derived data parity', '    name: parity'),
            original.replace('    runs-on:', '    if: false\n    runs-on:'),
            original.replace('    steps:', '    continue-on-error: true\n    steps:'),
            original.replace('run: go test -p 1 -v ./internal/derivedtest',
                             'run: go test -p 1 -v ./internal/derivedtest || true'),
        ]
        for text in mutations:
            with self.subTest(text=text), self.assertRaises(ValueError):
                parity_contract(text)


if __name__ == '__main__':
    unittest.main()
