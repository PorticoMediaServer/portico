import json
import unittest
from checkpoint_trace import epoch_ns, records, reduce, trace_calls


class CheckpointTraceTests(unittest.TestCase):
    def test_fragmented_output_and_exact_nanoseconds(self):
        marker = 'GV_KEEPER_CONCURRENT_V1 {"Cell":"64MiB"}\n'
        text = '\n'.join(json.dumps(dict(Test='x', Output=s)) for s in [marker[:25], marker[25:]])
        self.assertEqual(list(records(text)), [dict(Cell='64MiB')])
        self.assertEqual(epoch_ns('1970-01-01T00:00:01.123456789Z'), 1123456789)

    def test_complete_resumed_and_partial_window(self):
        text = '\n'.join([
            '42 1.000000 pwrite64(3</x/db>, ""..., 4096, 0) = 4096 <0.001000>',
            '43 1.001000 fsync(4</x/db-wal> <unfinished ...>',
            '43 1.003000 <... fsync resumed>) = 0 <0.002000>',
            '42 1.003900 fdatasync(3</x/db>) = 0 <0.001000>',
        ])
        calls, incomplete = trace_calls(text)
        self.assertEqual(incomplete, 0)
        rec = dict(Cell='x', Kind='discard', Keeper=True, DatabasePath='/x/db',
                   OriginUTC='1970-01-01T00:00:01Z', Writer=dict(Closes=[dict(connection_id=1, started_ns=0, returned_ns=4000000)]))
        out = reduce(rec, calls, incomplete)['windows'][0]['syscall_envelope']
        self.assertEqual(out['database']['write_bytes'], 4096)
        self.assertEqual(out['database']['partial'], 1)
        self.assertEqual(out['wal']['sync_ns'], 2000000)

    def test_unknown_trace_keeps_incomplete_count(self):
        calls, count = trace_calls('42 1.0 fsync(3</x/db> <unfinished ...>')
        self.assertEqual(calls, [])
        self.assertEqual(count, 1)


if __name__ == '__main__':
    unittest.main()
