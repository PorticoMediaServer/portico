import {readFile} from 'node:fs/promises';
import {transformSync} from 'rolldown/utils';

// Exercises the actual component callbacks without a browser or authenticated
// server. This models hook storage, not rendering, focus or React scheduling.
// A source's rules that live in client-core (`@core/…`) are headless TypeScript, so a test that
// does not stub one gets the real module: the web file and its tests keep working when a rule
// moves there and the web file re-exports it.
const coreRoot = new URL('../../../packages/client-core/src/', import.meta.url);
async function withCore(code, given) {
  const modules = {...given};
  for (const match of code.matchAll(/(?:import|export)\s+[\s\S]*?\s+from\s+["'](@core\/[^"']+)["'];/g)) {
    const source = match[1];
    let real;
    try { real = await import(new URL(source.slice('@core/'.length).replace(/(\.ts)?$/, '.ts'), coreRoot).href); } catch { continue; }
    // A test's stub overrides the names it gives; every other name is the real rule.
    modules[source] = modules[source] === undefined ? real : {...real, ...modules[source]};
  }
  return modules;
}

// A module the test did not stub is inert: every name on it is a function that does nothing.
// A test names the modules whose behaviour it relies on; a new import in the source file (a hook
// the helpers under test never call) should not fail every test of that file.
const inert = new Proxy(function () {}, {get: (_, key) => (key === 'then' ? undefined : inert), apply: () => undefined});
const inertWhereUnstubbed = modules => new Proxy(modules, {get: (target, key) => (key in target ? target[key] : inert)});

export async function componentModule(file, given) {
  const transformed = transformSync(file.pathname, await readFile(file, 'utf8'), {jsx: {runtime: 'classic'}});
  if (transformed.errors.length) throw new Error(JSON.stringify(transformed.errors));
  const modules = await withCore(transformed.code, given);
  const exported = [];
  // `export {a, b as c} from 'x'`: the names come from the module and are exported under their own.
  const reexported = transformed.code.replace(/export\s*\{([^}]*)\}\s*from\s*["']([^"']+)["'];/g, (_, names, source) => {
    // Bound under a private name: the file may also import the same name for its own use.
    const binds = names.split(',').map(n => n.trim()).filter(Boolean).map(n => { const [from, to] = n.split(/\s+as\s+/); const local = `__re_${to ?? from}`; exported.push(`${to ?? from}: ${local}`); return `${from}: ${local}`; });
    return `const {${binds.join(', ')}} = modules[${JSON.stringify(source)}];`;
  });
  const code = reexported.replace(/import\s+([\s\S]*?)\s+from\s+["']([^"']+)["'];/g, (_, names, source) => {
    let bind = names.trim().replace(/\bas\b/g, ':');
    if (!bind.startsWith('{')) {
      const comma = bind.indexOf(',');
      if (comma < 0) return `const ${bind} = modules[${JSON.stringify(source)}].default;`;
      return `const ${bind.slice(0, comma)} = modules[${JSON.stringify(source)}].default; const ${bind.slice(comma + 1)} = modules[${JSON.stringify(source)}];`;
    }
    return `const ${bind} = modules[${JSON.stringify(source)}];`;
  }).replace(/export (const|function|async function|class) (\w+)/g, (_, kind, name) => { exported.push(name); return `${kind} ${name}`; })
    // A bare re-export of a local binding (`export {hasReminder};`) has no meaning here.
    .replace(/export\s*\{[^}]*\};/g, '')
    .replaceAll('import.meta.env', '({DEV:false,VITE_SERVER_URL:"https://server.example"})');
  return new Function('modules', (/const React\s*=/.test(code) ? '' : 'const React = modules.react.default;\n') + code + '\nreturn {' + exported.join(',') + '};')(inertWhereUnstubbed(modules));
}

export function hooks() {
  const slots = []; let cursor = 0;
  const ref = value => { const n = cursor++; return slots[n] ??= {current: value}; };
  const react = {
    createContext: value => ({value, Provider: 'provider'}),
    createElement: (type, props, ...children) => ({type, props: {...props, children}}),
    useRef: ref,
    useState: initial => { const n=cursor++; if (!(n in slots)) slots[n]=typeof initial==='function'?initial():initial; return [slots[n], value=>{slots[n]=typeof value==='function'?value(slots[n]):value;}]; },
    useMemo: (fn, deps) => { const slot=ref(undefined); if (!slot.current || deps.some((v,i)=>v!==slot.current.deps[i])) slot.current={deps,value:fn()}; return slot.current.value; },
    useCallback: fn => fn,
    useEffect: () => {},
    useSyncExternalStore: (_, get) => get(),
    useContext: context => context.value,
  };
  return {react: {...react, default: react}, render: fn => {cursor=0;return fn();}};
}
