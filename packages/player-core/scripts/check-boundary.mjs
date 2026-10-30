import {readdirSync, readFileSync} from 'node:fs';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../src');
let count = 0;
function check(dir) {
  for (const entry of readdirSync(dir,{withFileTypes:true})) {
    const file = path.join(dir,entry.name);
    if (entry.isDirectory()) { check(file); continue; }
    if (!file.endsWith('.ts') || file.endsWith('.tsx')) throw new Error(`player-core boundary: non-headless source ${file}`);
    const source = readFileSync(file,'utf8').replace(/\/\*[\s\S]*?\*\/|\/\/[^\n]*/g,'');
    const imports = [...source.matchAll(/(?:\bfrom\s*|\bimport\s*(?:\(\s*)?)["']([^"']+)["']/g)];
    for (const [,specifier] of imports) if (!specifier.startsWith('./') || !specifier.endsWith('.ts') || specifier.includes('..')) throw new Error(`player-core boundary: external import ${specifier}`);
    if (/\b(?:window|document|localStorage|sessionStorage|navigator|process|require)\b|\bDate\.now\s*\(|\b(?:setTimeout|setInterval|requestAnimationFrame)\s*\(|\/v[0-9]+\//.test(source)) throw new Error(`player-core boundary: runtime or transport dependency ${file}`);
    count++;
  }
}
check(root);
console.log(`player-core: ${count} headless sources; only internal TypeScript imports; no UI, storage, timers, globals or backend paths`);
