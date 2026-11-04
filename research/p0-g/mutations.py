"""Required negative controls. Each corrupted model must fail its stated gate."""
import os
from pathlib import Path
import shutil
import subprocess
import tempfile

root = Path(__file__).resolve().parent
cases = [
    ('cancel-credit', 'fake.go', 'h.cancel = true', 'for k, n := range h.request.Need { g.Used[h.request.Class][k] -= n }; h.cancel = true', '^TestSeeds$'),
    ('borrow-foreground', 'fake.go', 'g.Used[r.Class][k]', '0', '^TestSeeds$'),
    ('stale-source', 'adapter_test.go', 'a.Fence != s.fence || a.Fence != l.Fence ||', '', '^TestLateSameCursorSourceChange$'),
    ('lease-handoff', 'simulation_test.go', 'a.prep.ready = true', 'a.prep.ready = true; a.prep.end = a.now', '^TestPreparationHandoff$'),
    ('starve-during-foreground-R04', 'simulation_test.go', 'chooseScan := scanRunnable &&', 'chooseScan := scanRunnable && now >= 3000 &&', '^TestSeeds$'),
]
for name, file, old, new, test in cases:
    with tempfile.TemporaryDirectory(prefix='p0-g-mutant-') as temp:
        target = Path(temp)
        for src in root.glob('*.go'):
            shutil.copy(src, target / src.name)
        shutil.copy(root / 'go.mod', target / 'go.mod')
        source = target / file
        text = source.read_text()
        assert old in text, (name, 'mutation anchor missing')
        source.write_text(text.replace(old, new, 1))
        env = dict(os.environ, GOWORK='off', SEEDS='8')
        env.pop('RESULT_JSON', None)
        run = subprocess.run(['go', 'test', '-count=1', '-run', test, './...'], cwd=target, env=env, capture_output=True, text=True)
        output = run.stdout + run.stderr
        # A compile/setup failure does not count as killing a mutation.
        if run.returncode == 0 or '[build failed]' in output or '[setup failed]' in output:
            raise SystemExit(f'{name}: control failed\n{output}')
        print(f'{name}: invariant detected corrupted model')
        print(output)
