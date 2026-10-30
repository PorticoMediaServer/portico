#!/usr/bin/env python3
import copy
import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location('hdr_encode', Path(__file__).with_name('verify-hdr10plus-encode.py'))
gate = importlib.util.module_from_spec(spec)
spec.loader.exec_module(gate)


class FrameMetadataGateTests(unittest.TestCase):
    def frames(self):
        return [{'side_data_list': [{'side_data_type': 'HDR Dynamic Metadata SMPTE2094-40 (HDR10+)',
                                    'scene': index // 12}]} for index in range(24)]

    def test_each_frame_metadata_must_survive(self):
        frames = self.frames()
        gate.compare_frames(frames, copy.deepcopy(frames))
        with self.assertRaises(ValueError):
            gate.compare_frames(frames, [{} for _ in frames])

    def test_one_repeated_scene_cannot_qualify(self):
        with self.assertRaises(ValueError):
            gate.compare_frames(self.frames(), [copy.deepcopy(self.frames()[0]) for _ in range(24)])

    def test_wrong_count_and_unknown_input_cannot_qualify(self):
        with self.assertRaises(ValueError):
            gate.compare_frames(self.frames(), self.frames()[:-1])
        with self.assertRaises(ValueError):
            gate.compare_frames([{} for _ in range(24)], self.frames())

    def test_disabled_carry_rejects_implicit_metadata(self):
        gate.check_disabled([{} for _ in range(24)])
        with self.assertRaises(ValueError):
            gate.check_disabled(self.frames())
        with self.assertRaises(ValueError):
            gate.check_disabled([{} for _ in range(23)])


if __name__ == '__main__':
    unittest.main()
