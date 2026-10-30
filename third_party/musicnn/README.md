The original musicnn project publishes its pretrained models in its ISC-licensed
Python distribution. The pinned source includes `LICENSE.md`, declares ISC in
`setup.py`, and explicitly includes the model directories in `MANIFEST.in`:

- https://github.com/jordipons/musicnn/tree/516acb2a0ff5ef73f64547898e018e793152c506
- https://pypi.org/project/musicnn/0.1.0/

`model.lock.json` identifies only the original MTT checkpoint files. It is not an
Essentia model or an inherited licence assertion about a converted model. The
offline preparer verifies the complete publisher wheel, package licence,
original licence text, and every model file before publishing a staged directory:

```
python3 scripts/prepare-musicnn-model.py OUTPUT --wheel musicnn-0.1.0-py3-none-any.whl
```

No weights or recordings are committed here. Staging does not enable Sonic or
establish ONNX conversion, embedding parity, CPU support, or analysis scheduling.
Those require their actual runtime and fixture checks before shipping.

## Durable pinned model export

`model.lock.json` also names `releaseTag`. The manual `sonic-toolchain.yml` job
exports the model once and publishes `musicnn-mtt-<version>.tar.xz` under that
non-latest tag with a `checksums.sha256`. The archive holds only the fixed trio
(`model.onnx`, `model.json`, `LICENSE.md`), the original-checkpoint CPU
conformance reference and the reviewed pin. Later workflow runs reuse those exact
bytes instead of re-running TensorFlow, so published bundles cannot drift from
the committed pin:

```
python3 scripts/sonic-bundle.py model-fetch OUTPUT
python3 scripts/sonic-bundle.py model-package OUTPUT --model MODEL_DIRECTORY --conformance REFERENCE
python3 scripts/sonic-bundle.py model-verify MODEL_DIRECTORY
```

`model-fetch` verifies the release checksum, the trio and descriptor identity, the
conformance reference and the embedded pin before publishing a directory atomically.
A pin change publishes a new model export; an existing one is never replaced.
Installing or loading a model still requires its real conformance run.

`model.lock.json` also names the non-latest GitHub release that holds the one
reviewed ONNX export for this pin (`releaseTag`). The release carries the model
trio, the original-checkpoint CPU conformance reference and the pin, so bundle
builds and later packagers reuse exact verified bytes instead of re-running the
TensorFlow conversion:

```
python3 scripts/sonic-bundle.py model-fetch .sonic-model
python3 scripts/sonic-bundle.py model-verify .sonic-model/musicnn-mtt
```
