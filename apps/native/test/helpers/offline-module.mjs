import {registerHooks} from 'node:module';

// Runs the actual TypeScript module with explicit boundary ports, without a
// bundler, generated packages or a native runtime. Only the named imports of
// this one module are replaced; its implementation is never rewritten.
let sequence = 0;
export async function offlineModule(file, modules = {}) {
  const key = '__porticoOfflineTest' + ++sequence;
  globalThis[key] = modules;
  const target = new URL(file);
  target.searchParams.set('offlineTest', String(sequence));
  const hook = registerHooks({
    resolve(specifier, context, next) {
      if (context.parentURL === target.href && Object.hasOwn(modules, specifier)) {
        const names = Object.keys(modules[specifier]);
        const source = `const ports = globalThis[${JSON.stringify(key)}][${JSON.stringify(specifier)}];\n`
          + names.map(name => name === 'default' ? 'export default ports.default;'
            : `export const ${name} = ports[${JSON.stringify(name)}];`).join('\n');
        return {url: 'data:text/javascript,' + encodeURIComponent(source), shortCircuit: true};
      }
      try {return next(specifier, context);} catch (error) {
        if (!['ERR_MODULE_NOT_FOUND', 'ERR_UNSUPPORTED_DIR_IMPORT'].includes(error.code) || !specifier.startsWith('.')) throw error;
        for (const suffix of ['.ts', '/index.ts', '.mjs', '.js']) {
          try {return next(specifier + suffix, context);} catch (failure) {
            if (!['ERR_MODULE_NOT_FOUND', 'ERR_UNSUPPORTED_DIR_IMPORT'].includes(failure.code)) throw failure;
          }
        }
        throw error;
      }
    },
  });
  try {return await import(target.href);} finally {hook.deregister(); delete globalThis[key];}
}
