"""Branch-only instrumentation; never merge this diagnostic into main."""
from pathlib import Path
import subprocess
import sys

root = Path(__file__).resolve().parents[1]
path = root / 'server/internal/compactcatalog/derive.go'
variant = sys.argv[1]
if variant == 'baseline':
    path.write_text(subprocess.check_output([
        'git', 'show', '0d2ceeb87a8d3f3f53df689ae64d7dc8dcae8dd2:server/internal/compactcatalog/derive.go'
    ], cwd=root, text=True))
    (root / 'server/internal/compactcatalog/visibility_source_batch_test.go').unlink()
elif variant != 'fixed':
    raise ValueError(variant)

source = path.read_text()
def replace(old, new):
    global source
    if source.count(old) != 1:
        raise ValueError('diagnostic anchor is not unique: ' + old)
    source = source.replace(old, new, 1)

replace('func (w *Worker) Run(ctx context.Context) {', '''func (w *Worker) Run(ctx context.Context) {
	if pressureCostEnabled {
		ctx = context.WithValue(ctx, pressureCostKey{}, true)
		defer dumpPressureCosts()
	}''')
replace('''	if err := w.prepare(ctx); err != nil {
		return 0, err
	}
	w.mu.Lock()''', '''	stepStarted := time.Now()
	if activePressureCost(ctx) {
		ctx, _ = dbwork.Measure(ctx)
	}
	prepareStarted, prepareBefore := time.Now(), dbwork.CostFrom(ctx)
	if err := w.prepare(ctx); err != nil {
		return 0, err
	}
	notePressureCost("prepare", prepareStarted, prepareBefore, ctx, 0, 0)
	w.mu.Lock()''')
replace('''	var rows int64
	cleaningSearch := false''', '''	var rows int64
	defer func() {
		notePressureCost(fmt.Sprintf("domain-%02d-step-%02d", domain, step), stepStarted, dbwork.Cost{}, ctx, n, rows)
	}()
	cleaningSearch := false''')
replace('''		n, err := steps[kind](ctx, tx, stepLimit)''', '''		stepStarted, stepBefore := time.Now(), dbwork.CostFrom(ctx)
		n, err := steps[kind](ctx, tx, stepLimit)
		notePressureCost(fmt.Sprintf("family-%02d-empty-%t", kind, n == 0), stepStarted, stepBefore, ctx, n, 0)''')
path.write_text(source)
