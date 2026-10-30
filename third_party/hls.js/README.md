# hls.js 1.7.2-portico.1

Private source-built dependency based on official hls.js v1.7.2, commit dd669ec53b601aa9fab7a2a78c913e25018cdf18. `provenance.json` records source, patch and output hashes. Upstream licensing is retained in LICENSE and the package.

The narrow patch preserves anchored first-fragment coverage only for measured continuous boundaries in the same continuity counter. It does not change fragment lookup tolerances or playback controllers. A TypeScript non-null assertion is erased during compilation.

The application dependency points to the local `.tgz`; imports remain `hls.js`. The package contains freshly built UMD/ESM, full/light, minified/nonminified variants, worker, types, maps and source. It is marked private and has no lifecycle scripts. Internal validation evidence is not packaged.

To rebuild without modifying application dependencies, run from the project root:

```sh
python3 third_party/hls.js/rebuild.py runtime/hls-dependency-rebuild-UNIQUE
```

The output directory must not exist. The script verifies the pinned official archive, original lock and source hashes, applies the patch, installs locked build tools with lifecycle scripts disabled, then runs the official build/type generation. It never installs the result into the application or publishes it. Compare the resulting artifact and distribution hashes with provenance before an explicit dependency update. Node/npm versions used for qualification are recorded there.
