from collections import Counter
import unittest

from race_plan import make_cohorts, matrix, select


def test(name, package='server/internal/httpapi'):
    return {'module': package.split('/')[0], 'package': 'github.com/PorticoMediaServer/portico/' + package, 'name': name}


class RacePlanTests(unittest.TestCase):
    def plan(self):
        return {'tier': 'nightly', 'inventory': [test('TestA'), test('TestB'), test('TestFast'), test('TestOther', 'apikit/test')],
                'shards': [[test('TestA'), test('TestOther', 'apikit/test')], [test('TestB'), test('TestFast')]]}

    def test_each_selected_parent_has_one_owner_and_no_cohort_mixes_packages(self):
        plan = self.plan()
        weights = {t['package'] + '.' + t['name']: 20 for t in plan['inventory']}
        race = make_cohorts(plan, weights, budget=300)
        parents = [t for c in race['cohorts'] for t in c['tests']]
        key = lambda t: (t['module'], t['package'], t['name'])
        self.assertEqual(Counter(map(key, parents)), Counter(map(key, plan['inventory'])))
        self.assertEqual(len(race['cohorts']), 4)
        for cohort in race['cohorts']:
            self.assertEqual(len({t['package'] for t in cohort['tests']}), 1)
            self.assertLessEqual(cohort['predicted_seconds'], 300)
        self.assertEqual(race, make_cohorts(plan, weights, budget=300))

    def test_exact_source_plan_and_selection_are_required(self):
        plan = self.plan(); race = make_cohorts(plan, {})
        self.assertEqual(select(plan, race, 0), race['cohorts'][0]['tests'])
        changed = dict(plan, tier='merge')
        with self.assertRaises(ValueError): select(changed, race, 0)
        race['cohorts'][0]['tests'][0] = test('TestUnknown')
        with self.assertRaises(ValueError): select(plan, race, 0)

    def test_zero_unknown_oversized_and_duplicate_parents_never_disappear(self):
        plan = self.plan(); first = plan['inventory'][0]
        costs = {first['package'] + '.' + first['name']: 90}
        race = make_cohorts(plan, costs, budget=300)
        oversized = [c for c in race['cohorts'] if c['predicted_seconds'] > 300]
        self.assertEqual(len(oversized), 1)
        self.assertEqual(oversized[0]['tests'], [first])
        duplicate = dict(plan, inventory=plan['inventory'] * 2)
        with self.assertRaises(ValueError): make_cohorts(duplicate, {})

    def test_node_dependencies_follow_the_reading_boundary_cohort(self):
        plan = {'inventory': [test('TestReadingHTTPClientPublication')]}
        self.assertEqual(matrix(make_cohorts(plan, {})), {'include': [{'index': 0, 'reading_client': True}]})


if __name__ == '__main__': unittest.main()

class RaceWorkflow(unittest.TestCase):
    def test_observer_uses_own_matrix_and_stays_outside_required_gate(self):
        from pathlib import Path
        import re
        text = (Path(__file__).resolve().parents[1] / '.github/workflows/nightly.yml').read_text()
        job = re.search(r'^  go_race:\n.*?(?=^  [\w-]+:\n|\Z)', text, re.M | re.S)[0]
        self.assertIn('fromJSON(needs.race_plan.outputs.race_matrix)', job)
        self.assertIn('continue-on-error: true', job)
        self.assertIn('--cohort-plan ci-race-plan.json', job)
        self.assertIn('name: playback-helper', job)
        self.assertNotIn('run.py ffmpeg --index', job)
        gate = re.search(r'^  merge:\n.*', text, re.M | re.S)[0]
        needs = next(line for line in gate.splitlines() if 'needs: [' in line)
        self.assertNotIn('race_plan', needs)
        self.assertNotIn('go_race', needs)
