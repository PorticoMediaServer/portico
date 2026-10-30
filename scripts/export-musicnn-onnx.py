#!/usr/bin/env python3
"""Export only the verified original ISC checkpoint; never enable analysis.

TensorFlow and the converter are build-time dependencies. The server runtime
must use the separately checked ONNX CPU helper, without TensorFlow or Python.
"""
import argparse
import hashlib
import importlib.metadata
import importlib.util
import json
import math
import os
from pathlib import Path
import shutil
import tempfile
import sys
import types


ROOT = Path(__file__).resolve().parent.parent
VERSIONS = {"tensorflow-cpu": "2.15.1", "tf2onnx": "1.16.1",
            "onnx": "1.16.2", "onnxruntime": "1.20.1", "numpy": "1.26.4"}


def verify_checkpoint(directory):
    lock = json.loads((ROOT / "third_party/musicnn/model.lock.json").read_text())
    manifest = json.loads((directory / "model.json").read_text())
    if (manifest.get("id"), manifest.get("version"), manifest.get("licence"), manifest.get("format")) != (
            lock["modelId"], lock["modelVersion"], "ISC", "tensorflow-checkpoint"):
        raise ValueError("checkpoint identity or original licence mismatch")
    for expected in lock["files"]:
        path = directory / Path(expected["path"]).name
        if path.is_symlink() or not path.is_file() or path.stat().st_size != expected["bytes"]:
            raise ValueError("checkpoint file identity mismatch")
        if hashlib.sha256(path.read_bytes()).hexdigest() != expected["sha256"]:
            raise ValueError("checkpoint file SHA256 mismatch")
    if hashlib.sha256((directory / "LICENSE.md").read_bytes()).hexdigest() != lock["licenseSHA256"]:
        raise ValueError("checkpoint licence mismatch")
    return lock


def original_graph(directory):
    # Parse the original training graph before importing any operations. Its old
    # gradient shape annotations do not import into current TensorFlow; only the
    # forward inference closure is needed and none of its weights are retrained.
    import tensorflow as tf
    meta = tf.compat.v1.MetaGraphDef()
    meta.ParseFromString((directory / ".meta").read_bytes())
    return meta.graph_def


def shape(node):
    if "shape" in node.attr:
        return [d.size for d in node.attr["shape"].shape.dim]
    if "_output_shapes" in node.attr and node.attr["_output_shapes"].list.shape:
        return [d.size for d in node.attr["_output_shapes"].list.shape[0].dim]
    return None


def inspect(directory):
    verify_checkpoint(directory)
    for node in original_graph(directory).node:
        dims = shape(node)
        if node.op == "Placeholder" or (dims and len(dims) == 2 and dims[-1] in (50, 200)
                and not node.name.startswith(("gradients/", "Adam", "save/"))):
            print(json.dumps({"name": node.name, "op": node.op, "shape": dims}))


def inference_graph(directory):
    import tensorflow as tf
    lock = verify_checkpoint(directory)
    original = original_graph(directory)
    nodes = {node.name: node for node in original.node}

    def tensor(key, op, dimensions):
        node = nodes[lock["graph"][key]]
        if node.op != op or shape(node) != dimensions:
            raise ValueError("different original graph boundary: " + key)
        return node

    source = tensor("input", "Placeholder", [-1, 187, 96])
    training = tensor("training", "Placeholder", [])
    if training.attr["dtype"].type != tf.bool.as_datatype_enum:
        raise ValueError("different training switch type")
    embedding = tensor("embedding", "Add", [-1, 200])
    logits = tensor("logits", "BiasAdd", [-1, 50])
    graph = tf.compat.v1.graph_util.extract_sub_graph(original, [embedding.name, logits.name])
    # The original checkpoint has an empty basename. Its historical `checkpoint`
    # state file names the author's absolute training directory, which must
    # never be followed. Open only the verified local .index/.data prefix.
    reader = tf.compat.v1.train.NewCheckpointReader(str(directory) + "/")
    replaced = 0
    for node in graph.node:
        if node.op in ("Variable", "VariableV2"):
            value = reader.get_tensor(node.name)
            dtype = node.attr["dtype"].type
            node.op = "Const"
            del node.input[:]
            node.attr.clear()
            node.attr["dtype"].type = dtype
            node.attr["value"].tensor.CopyFrom(tf.make_tensor_proto(value, dtype=dtype))
            replaced += 1
        elif node.name == training.name:
            node.op = "Const"
            node.attr.clear()
            node.attr["dtype"].type = tf.bool.as_datatype_enum
            node.attr["value"].tensor.CopyFrom(tf.make_tensor_proto(False, dtype=tf.bool))
        # TensorFlow recomputes forward shapes. Legacy training annotations are
        # not model weights and are incompatible across FusedBatchNorm versions.
        if "_output_shapes" in node.attr:
            del node.attr["_output_shapes"]
        node.device = ""
    if replaced == 0:
        raise ValueError("no original checkpoint weights in inference closure")
    with tf.Graph().as_default() as public:
        features = tf.compat.v1.placeholder(tf.float32, [None, 187, 96], name="log_mel")
        tf.import_graph_def(graph, input_map={source.name + ":0": features}, name="original")
        tf.identity(public.get_tensor_by_name("original/" + embedding.name + ":0"), name="embedding")
        tf.math.sigmoid(public.get_tensor_by_name("original/" + logits.name + ":0"), name="tags")
    return public


def original_inference_definition(directory):
    """Build the publisher's exact architecture with literal training=False.

    Avoid Grappler's nondeterministic training-graph rewrites. Original variables
    become original checkpoint constants; the separately imported saved graph
    remains the numeric reference. No architecture is guessed or retrained.
    """
    import tensorflow as tf
    lock = verify_checkpoint(directory)
    names = ("musicnn", "musicnn.configuration", "musicnn.models")
    previous = {name: sys.modules.get(name) for name in names}
    try:
        package = types.ModuleType("musicnn")
        package.__path__ = [str(directory)]
        sys.modules["musicnn"] = package
        for name in ("configuration", "models"):
            spec = importlib.util.spec_from_file_location("musicnn." + name, directory / (name + ".py"))
            module = importlib.util.module_from_spec(spec)
            sys.modules["musicnn." + name] = module
            # The original wheel uses TensorFlow 1's public spelling. Bind that
            # import to TF's supported v1 compatibility API, without changing a
            # byte of the verified publisher source or its architecture.
            original_tf = sys.modules["tensorflow"]
            try:
                sys.modules["tensorflow"] = tf.compat.v1
                spec.loader.exec_module(module)
            finally:
                sys.modules["tensorflow"] = original_tf
            setattr(package, name, module)
        if (package.configuration.SR, package.configuration.FFT_SIZE, package.configuration.FFT_HOP, package.configuration.N_MELS) != (16000, 512, 256, 96):
            raise ValueError("different original model preprocessing")
        tf.compat.v1.disable_resource_variables()
        with tf.Graph().as_default() as graph:
            features = tf.compat.v1.placeholder(tf.float32, [None, 187, 96], name="log_mel")
            with tf.name_scope("model"):
                outputs = package.models.define_model(features, False, lock["sourceModel"], 50)
            tf.identity(outputs[-1], name="embedding")
            tf.math.sigmoid(outputs[0], name="tags")
        frozen = tf.compat.v1.graph_util.extract_sub_graph(graph.as_graph_def(), ["embedding", "tags"])
        reader = tf.compat.v1.train.NewCheckpointReader(str(directory) + "/")
        for node in frozen.node:
            if node.op in ("Variable", "VariableV2", "VarHandleOp"):
                value = reader.get_tensor(node.name)
                dtype = node.attr["dtype"].type
                node.op = "Const"
                del node.input[:]
                node.attr.clear()
                node.attr["dtype"].type = dtype
                node.attr["value"].tensor.CopyFrom(tf.make_tensor_proto(value, dtype=dtype))
            elif node.op == "ReadVariableOp":
                dtype = node.attr["dtype"].type
                node.op = "Identity"
                node.attr.clear()
                node.attr["T"].type = dtype
            node.device = ""
        return frozen
    finally:
        for name, module in previous.items():
            if module is None:
                sys.modules.pop(name, None)
            else:
                sys.modules[name] = module


def canonical_graph(model):
    """Alpha-rename and topologically order the pure inference DAG.

    Converter-generated identifiers/order are not model semantics. Preserve
    every operator, attribute, input order and numeric value; coalesce only
    byte-identical pure nodes/constants. Parity is checked after this step.
    """
    import onnx
    graph = model.graph
    known = {value.name: value.name for value in graph.input}
    constants = {}
    for original in graph.initializer:
        value = onnx.TensorProto()
        value.CopyFrom(original)
        value.name = ""
        value.doc_string = ""
        if value.external_data:
            raise ValueError("external model data is forbidden")
        name = "w_" + hashlib.sha256(value.SerializeToString(deterministic=True)).hexdigest()
        known[original.name] = name
        value.name = name
        constants[name] = value
    pending = list(graph.node)
    nodes = {}
    ordered = []
    while pending:
        ready = []
        later = []
        for original in pending:
            if any(name and name not in known for name in original.input):
                later.append(original)
                continue
            if original.domain or original.op_type in ("RandomUniform", "RandomNormal", "Multinomial", "Bernoulli", "Dropout"):
                raise ValueError("stateful or custom inference operation is forbidden")
            node = onnx.NodeProto()
            node.CopyFrom(original)
            node.name = node.doc_string = ""
            attributes = sorted(node.attribute, key=lambda a: a.name)
            del node.attribute[:]
            for attribute in attributes:
                if attribute.type in (onnx.AttributeProto.GRAPH, onnx.AttributeProto.GRAPHS):
                    raise ValueError("unresolved training/control-flow subgraph")
                if attribute.type == onnx.AttributeProto.TENSOR:
                    attribute.t.name = attribute.t.doc_string = ""
                for value in attribute.tensors:
                    value.name = value.doc_string = ""
                node.attribute.append(attribute)
            node.input[:] = [known.get(name, "") for name in original.input]
            del node.output[:]
            key = hashlib.sha256(node.SerializeToString(deterministic=True) + str(len(original.output)).encode()).hexdigest()
            name = "n_" + key
            node.name = name
            node.output[:] = [name + "_" + str(i) if output else "" for i, output in enumerate(original.output)]
            ready.append((key, node, original))
        if not ready:
            raise ValueError("inference graph is cyclic or has unbound inputs")
        for key, node, original in sorted(ready, key=lambda item: item[0]):
            if key not in nodes:
                nodes[key] = node
                ordered.append(node)
            for old, new in zip(original.output, node.output):
                if old:
                    known[old] = new
        pending = later
    for output in graph.output:
        ordered.append(onnx.helper.make_node("Identity", [known[output.name]], [output.name], name="export_" + output.name))
    del graph.node[:]
    graph.node.extend(ordered)
    del graph.initializer[:]
    graph.initializer.extend(constants[name] for name in sorted(constants))
    del graph.value_info[:]
    graph.name = "musicnn_mtt"
    graph.doc_string = ""
    for value in [*graph.input, *graph.output]:
        value.doc_string = ""
        for dimension in value.type.tensor_type.shape.dim:
            if dimension.dim_param:
                dimension.dim_param = "batch"
    model.producer_name = "Portico musicnn conversion"
    model.producer_version = "1"
    model.doc_string = "Original ISC musicnn MTT weights; no retraining."
    model.model_version = 1
    imports = sorted(model.opset_import, key=lambda op: op.domain)
    del model.opset_import[:]
    model.opset_import.extend(imports)
    return model


def validate_export_pin(lock, version, data, candidate=False):
    if version != lock["onnx"]["version"]:
        raise ValueError("unapproved converted-model version")
    if not candidate and (len(data) != lock["onnx"]["bytes"] or hashlib.sha256(data).hexdigest() != lock["onnx"]["sha256"]):
        raise ValueError("ONNX bytes differ from the checked original-model conversion; no output published")


def export(directory, output, check_only=False, conformance=None, candidate=False):
    lock = verify_checkpoint(directory)
    if output is not None and output.exists():
        raise ValueError("output already exists; never overwrite an installed model")
    import numpy as np
    import onnx
    import onnxruntime as ort
    import tensorflow as tf
    import tf2onnx
    tf.compat.v1.disable_eager_execution()
    graph = inference_graph(directory)
    # This exact converter version's common entry point skips its public
    # loader's Grappler rewrite. CPU runtime optimization happens at load time,
    # after the distributable graph's original constants/identity are sealed.
    converted, external = tf2onnx.convert._convert_common(
        original_inference_definition(directory), name="original_musicnn",
        input_names=["log_mel:0"], output_names=["embedding:0", "tags:0"],
        opset=15, optimizers={}, continue_on_error=False)
    if external:
        raise ValueError("external weight files are not permitted")
    names = {"log_mel:0": "log_mel", "embedding:0": "embedding", "tags:0": "tags"}
    for node in converted.graph.node:
        for items in (node.input, node.output):
            for i, name in enumerate(items):
                items[i] = names.get(name, name)
    for items in (converted.graph.input, converted.graph.output, converted.graph.value_info):
        for value in items:
            value.name = names.get(value.name, value.name)
    version = lock["modelVersion"] + "-onnx1"
    onnx.helper.set_model_props(converted, {"portico.model.id": lock["modelId"],
        "portico.model.version": version, "portico.model.licence": "ISC",
        "portico.model.source": lock["sourceCommit"], "portico.preprocessing": "musicnn-logmel-1"})
    converted = canonical_graph(converted)
    onnx.checker.check_model(converted, full_check=True)
    data = converted.SerializeToString(deterministic=True)
    if not 0 < len(data) <= 32 << 20:
        raise ValueError("unbounded ONNX model")
    options = ort.SessionOptions()
    options.intra_op_num_threads = options.inter_op_num_threads = 1
    runtime = ort.InferenceSession(data, sess_options=options, providers=["CPUExecutionProvider"])
    if [i.name for i in runtime.get_inputs()] != ["log_mel"] or [i.name for i in runtime.get_outputs()] != ["embedding", "tags"]:
        raise ValueError("converted model boundary differs from the CPU helper")
    rng = np.random.default_rng(20261023)
    cases = [np.zeros((1, 187, 96), np.float32),
             rng.uniform(0, 5, (1, 187, 96)).astype(np.float32),
             np.broadcast_to(np.linspace(0, 6, 96, dtype=np.float32), (2, 187, 96)).copy()]
    config = tf.compat.v1.ConfigProto(intra_op_parallelism_threads=1, inter_op_parallelism_threads=1)
    errors = [0., 0.]
    with tf.compat.v1.Session(graph=graph, config=config) as reference:
        for case in cases:
            expected = reference.run([graph.get_tensor_by_name("embedding:0"), graph.get_tensor_by_name("tags:0")],
                                     {graph.get_tensor_by_name("log_mel:0"): case})
            actual = runtime.run(["embedding", "tags"], {"log_mel": case})
            for i, dimensions in enumerate((200, 50)):
                if actual[i].shape != (len(case), dimensions) or not np.isfinite(actual[i]).all():
                    raise ValueError("non-finite or different-dimension ONNX output")
                error = float(np.max(np.abs(expected[i] - actual[i])))
                errors[i] = max(errors[i], error)
                if error > (5e-4 if i == 0 else 2e-5):
                    raise ValueError("ONNX parity failed: " + str(errors))
        # The native ARM job has no TensorFlow installation. Carry actual
        # original-checkpoint outputs, not a second runtime's guessed values.
        reference_cases = []
        for number in range(3):
            values = [0.0 if number == 0 else 2 + math.sin(i * .013 * number) + math.cos(i * .003)
                      for i in range(187 * 96)]
            patch = np.asarray(values, dtype=np.float32).reshape(1, 187, 96)
            expected = reference.run([graph.get_tensor_by_name("embedding:0"), graph.get_tensor_by_name("tags:0")],
                                     {graph.get_tensor_by_name("log_mel:0"): patch})
            reference_cases.append([float(v) for output_values in expected for v in output_values[0]])
    print(json.dumps({"status": "conversion_parity_validated", "modelId": lock["modelId"], "version": version,
                      "sha256": hashlib.sha256(data).hexdigest(), "bytes": len(data),
                      "patches": 4, "maxAbsoluteError": {"embedding": errors[0], "tags": errors[1]}}))
    if check_only:
        return
    validate_export_pin(lock, version, data, candidate)
    output.parent.mkdir(parents=True, exist_ok=True)
    staging = Path(tempfile.mkdtemp(prefix="musicnn-onnx-", dir=output.parent))
    try:
        (staging / "model.onnx").write_bytes(data)
        shutil.copyfile(directory / "LICENSE.md", staging / "LICENSE.md")
        (staging / "model.json").write_text(json.dumps({"id": lock["modelId"], "version": version,
            "licence": "ISC", "sourceCommit": lock["sourceCommit"], "format": "onnx",
            "sha256": hashlib.sha256(data).hexdigest(), "bytes": len(data),
            "preprocessing": "musicnn-logmel-1", "input": "log_mel", "embedding": "embedding",
            "tags": "tags", "frames": 187, "mels": 96, "dimensions": 200, "tagCount": 50,
            "dependencies": VERSIONS}, indent=2) + "\n")
        staging.rename(output)
    finally:
        if staging.exists():
            shutil.rmtree(staging)
    print(json.dumps({"status": "onnx_parity_passed", "modelId": lock["modelId"], "version": version,
                      "sha256": hashlib.sha256(data).hexdigest(), "bytes": len(data),
                      "patches": 4, "maxAbsoluteError": {"embedding": errors[0], "tags": errors[1]}}))
    if conformance:
        conformance.write_text(json.dumps({"modelSHA256": hashlib.sha256(data).hexdigest(),
                                          "reference": "original-tensorflow-checkpoint", "cases": reference_cases}) + "\n")


def check_versions():
    if sys.version_info[:3] != (3, 11, 11):
        raise ValueError("export requires the checked CPython 3.11.11 toolchain")
    for name, expected in VERSIONS.items():
        if importlib.metadata.version(name) != expected:
            raise ValueError("export dependency version mismatch: " + name)


def main():
    # tf2onnx traverses sets while assigning internal tensor identifiers. Fix
    # Python's hash seed before interpreter startup so published bytes do not
    # depend on the build process's random seed. No runtime process is re-execed.
    if os.environ.get("PYTHONHASHSEED") != "0":
        env = dict(os.environ, PYTHONHASHSEED="0")
        os.execve(sys.executable, [sys.executable, *sys.argv], env)
    os.environ["CUDA_VISIBLE_DEVICES"] = "-1"
    os.environ["TF_NUM_INTRAOP_THREADS"] = "1"
    os.environ["TF_NUM_INTEROP_THREADS"] = "1"
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("checkpoint", type=Path)
    parser.add_argument("output", nargs="?", type=Path)
    parser.add_argument("--check-only", action="store_true",
                        help="validate conversion parity and print its identity without publishing any model")
    parser.add_argument("--inspect", action="store_true",
                        help="inspect the verified graph's actual inference boundaries")
    parser.add_argument("--conformance", type=Path,
                        help="write original-checkpoint reference outputs for native bundle checks")
    parser.add_argument("--candidate", action="store_true",
                        help="retain a parity-validated build artifact for a reviewed pin update; never enables inference")
    args = parser.parse_args()
    if args.candidate and (not args.output or args.check_only):
        parser.error("--candidate requires an output and the full conversion/parity check")
    check_versions()
    if args.inspect:
        inspect(args.checkpoint)
    if args.check_only or args.output:
        export(args.checkpoint, args.output, args.check_only, args.conformance, args.candidate)
    elif not args.inspect:
        parser.error("an output directory or --inspect is required")


if __name__ == "__main__":
    main()
