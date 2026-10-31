import unittest
from datetime import datetime, timezone
from timings import capacity_snapshot, concurrency_timeline, event_stats, job_timing, pages, summarize


def job(status='completed', **kwargs):
    return dict(id=1, name='js (web)', status=status, conclusion='success' if status == 'completed' else None,
                created_at='2026-10-31T19:00:00Z', started_at='2026-10-31T19:01:00Z',
                completed_at='2026-10-31T19:03:00Z' if status == 'completed' else None,
                runner_id=12, **kwargs)


class Timings(unittest.TestCase):
    def test_queued_placeholder_is_not_a_start(self):
        j = job('queued'); j['started_at'] = j['created_at']
        self.assertIsNone(job_timing(j)['started_at'])
        self.assertIsNone(job_timing(j)['queue_seconds'])
        self.assertIsNone(job_timing(j)['run_seconds'])
        j.update(status='completed', conclusion='cancelled', runner_id=0)
        self.assertIsNone(job_timing(j)['started_at'])
        j.update(conclusion='skipped', runner_id=12)
        self.assertIsNone(job_timing(j)['started_at'])

    def test_queue_runtime_and_intervals(self):
        j = job()
        self.assertEqual(job_timing(j)['queue_seconds'], 60)
        self.assertEqual(job_timing(j)['run_seconds'], 120)
        rows = concurrency_timeline([j], datetime(2026, 10, 31, 19, 4, tzinfo=timezone.utc))
        self.assertEqual([(r['queued'], r['in_progress']) for r in rows], [(1, 0), (0, 1), (0, 0)])

    def test_incomplete_and_mixed_events_do_not_bias_quantiles(self):
        run = dict(id=9, event='merge_group', status='in_progress', conclusion=None,
                   head_sha='abc', html_url='https://example.invalid/run/9', created_at='2026-10-31T19:00:00Z')
        report = summarize(run, [job()])
        self.assertIsNone(report['wall_seconds'])
        run.update(status='completed', conclusion='success')
        complete = summarize(run, [job()])
        self.assertEqual(complete['wall_seconds'], 180)
        self.assertEqual(complete['runner_minutes'], 2)
        stats = event_stats([report, complete])
        self.assertEqual(stats['merge_group']['successful_completed'], 1)
        self.assertEqual(stats['merge_group']['p90_seconds'], 180)
        self.assertIsNone(stats['pull_request']['p50_seconds'])
        run.update(run_attempt=2, run_started_at='2026-10-31T19:01:00Z')
        self.assertEqual(summarize(run, [job()])['wall_seconds'], 120)
        self.assertIsNone(summarize(run, [])['wall_seconds'])

    def test_pages_and_live_snapshot_deduplicate_runs(self):
        calls = []
        def fetch(path):
            calls.append(path)
            if '/attempts/' in path:
                return {'jobs': [job('queued')]}
            return {'workflow_runs': [{'id': 42, 'run_attempt': 2}]}
        sample = capacity_snapshot('owner/repo', fetch)
        self.assertEqual((sample['in_progress'], sample['queued']), (0, 1))
        self.assertIsNone(sample['jobs'][0]['started_at'])
        self.assertEqual(sum('/attempts/2/jobs' in p for p in calls), 1)
        responses = iter([{'jobs': list(range(100))}, {'jobs': [100]}])
        self.assertEqual(len(pages('path?filter=latest', 'jobs', lambda _: next(responses))), 101)
