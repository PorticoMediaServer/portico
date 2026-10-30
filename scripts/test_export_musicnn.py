import importlib.util
from pathlib import Path
import unittest

import numpy as np
import onnx
from onnx import helper, numpy_helper, TensorProto
import onnxruntime as ort


spec = importlib.util.spec_from_file_location("export_musicnn", Path(__file__).with_name("export-musicnn-onnx.py"))
exporter = importlib.util.module_from_spec(spec)
spec.loader.exec_module(exporter)


class CanonicalConversionTests(unittest.TestCase):
    def graph(self, suffix, reverse):
        weights = numpy_helper.from_array(np.array([1., -2.], np.float32), "weights" + suffix)
        add = helper.make_node("Add", ["input", weights.name], ["add" + suffix], name="temporary" + suffix)
        multiply = helper.make_node("Mul", ["input", weights.name], ["multiply" + suffix], name="another" + suffix)
        join = helper.make_node("Concat", ["add" + suffix, "multiply" + suffix], ["out"], axis=1)
        graph = helper.make_graph(([multiply, add] if reverse else [add, multiply]) + [join], "converter-" + suffix,
            [helper.make_tensor_value_info("input", TensorProto.FLOAT, [1, 2])],
            [helper.make_tensor_value_info("out", TensorProto.FLOAT, [1, 4])], [weights])
        return helper.make_model(graph, opset_imports=[helper.make_opsetid("", 15)])

    def test_identifiers_and_independent_node_order_do_not_change_published_bytes(self):
        original = self.graph("__173", False)
        canonical = exporter.canonical_graph(self.graph("__173", False))
        renamed = exporter.canonical_graph(self.graph("__9842", True))
        onnx.checker.check_model(canonical, full_check=True)
        self.assertEqual(canonical.SerializeToString(deterministic=True), renamed.SerializeToString(deterministic=True))
        options = ort.SessionOptions()
        options.intra_op_num_threads = options.inter_op_num_threads = 1
        data = {"input": np.array([[3., -4.]], np.float32)}
        reference = ort.InferenceSession(original.SerializeToString(), options, providers=["CPUExecutionProvider"]).run(None, data)
        actual = ort.InferenceSession(canonical.SerializeToString(), options, providers=["CPUExecutionProvider"]).run(None, data)
        np.testing.assert_array_equal(reference[0], actual[0])

    def test_stateful_or_custom_model_is_not_canonicalized(self):
        for kind in ("random", "custom"):
            with self.subTest(kind=kind):
                model = self.graph("_test", False)
                if kind == "custom":
                    model.graph.node[0].domain = "unverified.custom"
                else:
                    model.graph.node[0].op_type = "RandomUniform"
                with self.assertRaises(ValueError):
                    exporter.canonical_graph(model)


if __name__ == "__main__":
    unittest.main()
