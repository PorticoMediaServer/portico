import * as playbackQueries from '../../../../packages/client-core/src/query/playback.ts';
import * as receiverQueries from '../../../../packages/client-core/src/query/receiver.ts';
import * as subtitleUploads from '../../../../packages/client-core/src/playback-v1/subtitle-upload.ts';
import * as shellMedia from '../../../../packages/client-core/src/shell-media.ts';
import {readFile} from 'node:fs/promises';
import {stripTypeScriptTypes} from 'node:module';
import {SourceTextModule, SyntheticModule, createContext} from 'node:vm';

// Offline validation adapter (Node 24, --experimental-vm-modules). The real source is transformed by Node's
// own TypeScript compiler. Only explicit imported boundaries are supplied by the
// caller; this does not implement or rewrite any behavior under test.
export async function sourceModule(file, modules, globals = {}) {
  const code = stripTypeScriptTypes(await readFile(file, 'utf8'), {mode: 'transform', sourceUrl: String(file)});
  const context = createContext({
    console, setTimeout, clearTimeout, setInterval, clearInterval, queueMicrotask,
    URL, URLSearchParams, AbortController, AbortSignal, TextEncoder, TextDecoder,
    performance, structuredClone, atob, btoa, ...globals,
  });
  const module = new SourceTextModule(code, {context, identifier: String(file)});
  const linked = new Map();
  await module.link(specifier => {
    const builtIn = /client-core\/src\/shell-media(?:\.ts)?$/.test(specifier) ? shellMedia
      : /client-core\/src\/playback-v1\/subtitle-upload(?:\.ts)?$/.test(specifier) ? subtitleUploads
      : /client-core\/src\/query\/receiver(?:\.ts)?$/.test(specifier) ? receiverQueries : /client-core\/src\/query\/playback(?:\.ts)?$/.test(specifier) ? playbackQueries : undefined;
    if (!Object.hasOwn(modules, specifier) && !builtIn) throw new Error(`Missing offline boundary: ${specifier}`);
    if (linked.has(specifier)) return linked.get(specifier);
    const namespace = Object.hasOwn(modules, specifier) ? modules[specifier] : builtIn;
    const dependency = new SyntheticModule(Object.keys(namespace), function () {
      for (const [name, value] of Object.entries(namespace)) this.setExport(name, value);
    }, {context, identifier: specifier});
    linked.set(specifier, dependency);
    return dependency;
  });
  await module.evaluate();
  return module.namespace;
}
