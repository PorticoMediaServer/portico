import unittest

from inventory_benchmark import reconcile


def receipt(size, operation):
    return (f'BenchmarkInventoryUnchangedDatabasePage/{size}/{operation}-4 1 '
            '1000 ns/op 22 counted-vm/object 8 ns/object 1 stmts/object 1 tx/page\n')


def complete():
    return ''.join(f'    fixture_test.go:1: INVENTORY_FIXTURE objects={size} distinct_assets={size} physical_sample=128 construction_seconds=1 (excluded)\n'
                   + ''.join(receipt(size, operation) for operation in ('lookup128', 'page128'))
                   for size in ('1000', '1000000'))


class InventoryBenchmarkReceipts(unittest.TestCase):
    def test_exact_measurements_and_fixture_counts_pass(self):
        reconcile('BenchmarkInventoryUnchangedDatabasePage/1000/lookup128\n' + complete() + 'PASS\n')

    def test_missing_skipped_duplicate_and_malformed_results_block(self):
        good = complete()
        result = receipt('1000000', 'page128')
        for invalid in (good.replace(result, ''), good.replace(result, '--- SKIP: BenchmarkInventoryUnchangedDatabasePage/1000000/page128\n'),
                        good + result, good.replace('page128-4 1 ', 'page128-4 2 '), good.replace('counted-vm/object', 'unknown-unit')):
            with self.subTest(invalid=invalid), self.assertRaises(ValueError):
                reconcile(invalid)

    def test_missing_or_incorrect_normalized_fixture_blocks(self):
        for invalid in (complete().replace('objects=1000000', 'objects=999999'),
                        complete().replace('distinct_assets=1000000', 'distinct_assets=1'),
                        complete().replace('physical_sample=128', 'physical_sample=127'),
                        complete() + 'INVENTORY_FIXTURE objects=1000 distinct_assets=1000 physical_sample=128 construction_seconds=1\n'):
            with self.subTest(invalid=invalid), self.assertRaises(ValueError):
                reconcile(invalid)

    def test_missing_nonfinite_bogus_and_out_of_range_metric_values_block(self):
        for size in ('1000', '1000000'):
            for operation in ('lookup128', 'page128'):
                good = receipt(size, operation)
                for pair in ('1000 ns/op', '22 counted-vm/object', '8 ns/object',
                             '1 stmts/object', '1 tx/page'):
                    unit = pair.split()[1]
                    for value in ('', 'NaN ', 'Inf ', '-1 ', 'bogus ', '1e999 ', '1e100 '):
                        with self.subTest(size=size, operation=operation, unit=unit, value=value), self.assertRaises(ValueError):
                            reconcile(complete().replace(good, good.replace(pair, value + unit)))
        for metric in ('3 stmts/object', '41 counted-vm/object', '2 tx/page'):
            original = next(pair for pair in ('1 stmts/object', '22 counted-vm/object', '1 tx/page') if pair.split()[1] == metric.split()[1])
            with self.subTest(metric=metric), self.assertRaises(ValueError):
                reconcile(complete().replace(original, metric))
        with self.assertRaises(ValueError):
            reconcile(complete().replace('1000 ns/op', '1000 ns/op 1000 ns/op'))

    def test_missing_nonfinite_bogus_and_out_of_range_fixture_duration_blocks(self):
        for duration in ('', 'NaN', 'Inf', '-1', 'bogus', '1e999', '3901'):
            with self.subTest(duration=duration), self.assertRaises(ValueError):
                reconcile(complete().replace('construction_seconds=1 ', 'construction_seconds=' + duration + ' '))

    def test_numeric_bounds_reuse_the_existing_hosted_timeout(self):
        from pathlib import Path
        from inventory_benchmark import MAX_ELAPSED_SECONDS
        workflow = (Path(__file__).resolve().parents[1] / '.github/workflows/nightly.yml').read_text()
        self.assertIn('-timeout=' + str(MAX_ELAPSED_SECONDS // 60) + 'm ./internal/catalog', workflow)
