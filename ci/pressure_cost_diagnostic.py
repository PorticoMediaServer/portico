"""Branch-only instrumentation; never merge this diagnostic into main."""
from pathlib import Path
import subprocess
import sys

root = Path(__file__).resolve().parents[1]
path = root / 'server/internal/compactcatalog/derive.go'
variant = sys.argv[1]
refs = {'baseline': '0d2ceeb87a8d3f3f53df689ae64d7dc8dcae8dd2',
        'fixed': '27ef10d40e69dfae3c4ac6588606ebfe4d59a771'}
if variant not in refs:
    raise ValueError(variant)
path.write_text(subprocess.check_output([
    'git', 'show', refs[variant] + ':server/internal/compactcatalog/derive.go'
], cwd=root, text=True))
test_path = root / 'server/internal/compactcatalog/visibility_source_batch_test.go'
if variant == 'baseline':
    test_path.unlink(missing_ok=True)
else:
    test_path.write_text(subprocess.check_output([
        'git', 'show', refs[variant] + ':server/internal/compactcatalog/visibility_source_batch_test.go'
    ], cwd=root, text=True))

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
		ctx = pressureDomain(ctx, "dispatch")
		ctx, _ = dbwork.Measure(ctx)
	}
	var sqlTrace func() []string
	if pressureNativeEnabled && activePressureCost(ctx) {
		ctx, sqlTrace = dbwork.TraceQueryPlans(ctx)
	}
	prepareStarted, prepareBefore := time.Now(), dbwork.CostFrom(ctx)
	if err := w.prepare(pressureDomain(ctx, "prepare")); err != nil {
		return 0, err
	}
	notePressureCost("prepare", prepareStarted, prepareBefore, ctx, 0, 0)
	w.mu.Lock()''')
replace('''	var rows int64
	cleaningSearch := false''', '''	var rows int64
	defer func() {
		notePressureCost(fmt.Sprintf("domain-%02d-step-%02d", domain, step), stepStarted, dbwork.Cost{}, ctx, n, rows)
		notePressureSQL(fmt.Sprintf("domain-%02d-step-%02d", domain, step), sqlTrace, dbwork.CostFrom(ctx).Statements)
	}()
	cleaningSearch := false''')
replace('''		n, err := steps[kind](ctx, tx, stepLimit)''', '''		stepStarted, stepBefore := time.Now(), dbwork.CostFrom(ctx)
		n, err := steps[kind](pressureDomain(ctx, fmt.Sprintf("family-%02d", kind)), tx, stepLimit)
		notePressureCost(fmt.Sprintf("family-%02d-empty-%t", kind, n == 0), stepStarted, stepBefore, ctx, n, 0)''')
replace('w.drainDomain(ctx, tx, domain, rebuilding, batch, retried)',
        'w.drainDomain(pressureDomain(ctx, fmt.Sprintf("domain-%02d", domain)), tx, domain, rebuilding, batch, retried)')
if variant == 'baseline':
    replace('n, err = StepVisibilitySourceFanout(ctx, tx, min(limit, upstreamQuantum))',
            'n, err = StepVisibilitySourceFanout(pressureDomain(ctx, "source"), tx, min(limit, upstreamQuantum))')
else:
    replace('n, err = drainVisibilitySources(ctx, tx, min(limit, upstreamQuantum),',
            'n, err = drainVisibilitySources(pressureDomain(ctx, "source"), tx, min(limit, upstreamQuantum),')
path.write_text(source)
