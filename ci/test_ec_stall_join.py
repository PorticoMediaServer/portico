import unittest

import ec_stall_join as j

ORIGIN = 1_700_000_000 * 10**9


def stamp(offset_ns):
    ns = ORIGIN + offset_ns
    import datetime
    base = datetime.datetime.fromtimestamp(ns // 10**9, datetime.timezone.utc).strftime('%Y-%m-%dT%H:%M:%S')
    return f'{base}.{ns % 10**9:09d}Z'


def sample(at_ms, **kw):
    s = dict(at_ns=at_ms * 10**6, tick_late_ns=0, read_ns=1000, psi_cpu_some_us=0, psi_io_some_us=0, psi_io_full_us=0,
             steal_jiffies=0, iowait_jiffies=0, cg_nr_periods=0, cg_nr_throttled=0, cg_throttled_usec=0, cg_psi_io_some_us=0,
             disk_reads_completed=0, disk_writes_completed=0, disk_in_flight=0, disk_io_ticks_ms=0, disk_weighted_ms=0,
             nr_dirty=0, nr_writeback=0)
    s.update(kw)
    return s


def records(samples, ops, passives=()):
    out = [{'kind': 'host_sampler_start', 'origin_utc_ns': ORIGIN}, {'kind': 'host_samples', 'samples': samples}]
    for start_ms, end_ms in passives:
        out.append({'kind': 'ec2_checkpoint', 'checkpoint': {'result': {'NativeStarted': stamp(start_ms * 10**6), 'NativeEnded': stamp(end_ms * 10**6), 'Recovery': False}}})
    for phase, start_ms, end_ms in ops:
        out.append({'kind': 'foreground', 'observation': {'Phase': phase, 'CallStarted': stamp(start_ms * 10**6), 'CallReturned': stamp(end_ms * 10**6)}})
    return out


class StallJoin(unittest.TestCase):
    def run_case(self, per_ms, ops, passives=((10, 90),)):
        samples = [sample(t, **per_ms(t)) for t in range(0, 200, 10)]
        result = j.analyse(records(samples, ops, passives), 25.0)
        return {(o['operation'], o['passive']): o for o in result['operations']}

    def test_device_stall_is_attributed_to_io_pressure_and_passive_overlap(self):
        table = self.run_case(lambda t: dict(psi_io_full_us=max(0, t - 20) * 1000 if t < 90 else 70_000), [('BROWSE', 20, 80), ('BROWSE', 100, 102)])
        self.assertEqual(table[('read BROWSE', 'overlapped')]['slow_by_cause'], {'device': 1})
        self.assertEqual(table[('read BROWSE', 'clear')]['slow'], 0)

    def test_late_sampler_tick_means_process_pause(self):
        table = self.run_case(lambda t: dict(tick_late_ns=60 * 10**6 if t == 50 else 0, psi_io_full_us=t * 1000), [('BROWSE', 20, 80)])
        self.assertEqual(table[('read BROWSE', 'overlapped')]['slow_by_cause'], {'process_pause': 1})

    def test_throttling_and_cpu_pressure(self):
        table = self.run_case(lambda t: dict(cg_nr_throttled=1 if t >= 50 else 0), [('BROWSE', 20, 80)])
        self.assertEqual(table[('read BROWSE', 'overlapped')]['slow_by_cause'], {'cpu_throttle': 1})
        table = self.run_case(lambda t: dict(psi_cpu_some_us=t * 1000), [('BROWSE', 20, 80)])
        self.assertEqual(table[('read BROWSE', 'overlapped')]['slow_by_cause'], {'cpu_starved': 1})

    def test_slow_with_no_signal_is_unexplained_and_no_samples_is_explicit(self):
        table = self.run_case(lambda t: {}, [('BROWSE', 20, 80)])
        self.assertEqual(table[('read BROWSE', 'overlapped')]['slow_by_cause'], {'unexplained': 1})
        result = j.analyse([{'kind': 'foreground', 'observation': {'Phase': 'BROWSE', 'CallStarted': stamp(0), 'CallReturned': stamp(50 * 10**6)}}], 25.0)
        self.assertEqual(result['operations'][0]['slow_by_cause'], {'no_samples': 1})


if __name__ == '__main__':
    unittest.main()
