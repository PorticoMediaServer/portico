import {registerHooks} from 'node:module';
import {Children, isValidElement, type ReactNode} from 'react';
import {transformTsx} from './transform-tsx.mjs';

// Inspect the real components' handlers with deterministic state/ref slots.
// Browser native buttons own Enter/Space activation; this fixture drives click.
const bridge = {current: undefined as Frame | undefined};
(globalThis as any).__choiceActivationHooks = bridge;
const react = import.meta.resolve('react');
const hooks = 'data:text/javascript,' + encodeURIComponent(`export * from ${JSON.stringify(react)};
const b=globalThis.__choiceActivationHooks;
export const useState=v=>b.current.useState(v);
export const useRef=v=>b.current.useRef(v);`);
registerHooks({load(url, context, next) {
  if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
  if (url.endsWith('.tsx')) {
    let source = transformTsx(url);
    if (/\/(BrowseToolbar|EndCard)\.tsx$/.test(url)) source = source.replace(/from (['"])react\1/g, `from ${JSON.stringify(hooks)}`);
    return {format: 'module', source, shortCircuit: true};
  }
  return next(url, context);
}});
export class Frame {
  slots: any[] = []; cursor = 0;
  useState(value: any) {const i = this.cursor++; if (!(i in this.slots)) this.slots[i] = typeof value === 'function' ? value() : value;
    return [this.slots[i], (next: any) => {this.slots[i] = typeof next === 'function' ? next(this.slots[i]) : next;}];}
  useRef(value: any) {return this.slots[this.cursor++] ??= {current: value};}
  render(run: () => ReactNode) {this.cursor = 0; bridge.current = this; try {return run();} finally {bridge.current = undefined;}}
}
export function elements(node: ReactNode): any[] {
  return Children.toArray(node).flatMap(child => isValidElement<{children?: ReactNode}>(child) ? [child, ...elements(child.props.children)] : []);
}
