import {readFile} from 'node:fs/promises';
import ts from 'typescript';
const debug = new URL('../src/debug.tsx', import.meta.url).href;
export async function load(url, context, next) {
  if (url === debug) {
    const source = await readFile(new URL(url), 'utf8');
    return {format: 'module', shortCircuit: true, source: ts.transpileModule(source, {compilerOptions: {module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022, jsx: ts.JsxEmit.React}}).outputText};
  }
  return next(url, context);
}
