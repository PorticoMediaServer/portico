import test from 'node:test';
import assert from 'node:assert/strict';
import {componentModule} from './helpers/component-harness.mjs';
import {all, createRenderer, tree} from './helpers/interaction-harness.mjs';

test('title tabs follow the actual shell navigation, including changes without a width change', async t => {
  let width = 1280, navigation = 'sidebar', compact = false;
  let readNavigation: (() => void) | undefined;
  const intersections: {callback: (entries: unknown[]) => void; options: IntersectionObserverInit; disconnected: boolean}[] = [];
  const shell = {getAttribute: () => navigation}, anchor = {closest: () => shell};
  const h = createRenderer({element: anchor});
  t.after(() => h.dispose());
  const originalMutation = Object.getOwnPropertyDescriptor(globalThis, 'MutationObserver'), originalIntersection = Object.getOwnPropertyDescriptor(globalThis, 'IntersectionObserver');
  Object.defineProperty(globalThis, 'MutationObserver', {configurable: true, value: class {
    constructor(callback: () => void) {readNavigation = callback;}
    observe(target: unknown, options: MutationObserverInit) {assert.strictEqual(target, shell); assert.deepEqual(options.attributeFilter, ['data-navigation']);}
    disconnect() {readNavigation = undefined;}
  }});
  Object.defineProperty(globalThis, 'IntersectionObserver', {configurable: true, value: class {
    state: typeof intersections[number];
    constructor(callback: (entries: unknown[]) => void, options: IntersectionObserverInit) {this.state = {callback, options, disconnected: false}; intersections.push(this.state);}
    observe(target: any) {assert.strictEqual(target.closest('[data-navigation]'), shell);}
    disconnect() {this.state.disconnected = true;}
  }});
  t.after(() => {
    if (originalMutation) Object.defineProperty(globalThis, 'MutationObserver', originalMutation); else Reflect.deleteProperty(globalThis, 'MutationObserver');
    if (originalIntersection) Object.defineProperty(globalThis, 'IntersectionObserver', originalIntersection); else Reflect.deleteProperty(globalThis, 'IntersectionObserver');
  });
  const app = await componentModule(new URL('../src/screens/detail/TitleTabs.tsx', import.meta.url), {
    react: h.react,
    '../../shell/viewport': {useShellWidth: () => width}, '../../kit/layout/motion': {useLayoutMotion: () => ({reduced: true}), scrollLayout() {}},
    '../../kit/controls': {Button: 'Button'}, './text': {t: (key: string) => key},
    './detail.module.css': {default: {sticky: 'sticky', tabAnchor: 'tabAnchor', pinnedPhone: 'pinnedPhone'}},
  });
  const render = () => tree(h.render(() => app.TitleTabs({title: 'Title', compact, children: 'Tabs'})));
  const top = (view: any[]) => all(view, 'div').find((node: any) => node.props.className === 'sticky').props.style.top;
  render(); let view = render();
  assert.equal(top(view), 0, 'wide sidebar shell has no top bar');
  navigation = 'topbar'; readNavigation!(); view = render();
  assert.equal(top(view), 76);
  assert.equal(intersections.at(-1)?.options.rootMargin, '-76px 0px 0px 0px');
  navigation = 'rail'; readNavigation!(); view = render();
  assert.equal(top(view), 0, 'a rail toggle at the same width updates the sticky geometry');
  assert.equal(intersections.at(-1)?.options.rootMargin, '-0px 0px 0px 0px');
  navigation = 'tabs'; width = 390; readNavigation!(); render(); view = render();
  assert.equal(top(view), 44);
  intersections.at(-1)!.callback([{isIntersecting: false, boundingClientRect: {top: -1}}]); view = render();
  assert.equal(all(view, 'div').filter((node: any) => node.props.className === 'pinnedPhone').length, 1);
  navigation = 'rail'; width = 800; readNavigation!(); render(); view = render();
  assert.equal(top(view), 0);
  assert.equal(all(view, 'div').filter((node: any) => node.props.className === 'pinnedPhone').length, 0);
  navigation = 'topbar'; readNavigation!(); compact = true; view = render();
  assert.equal(top(view), 0, 'the embedded Watch body remains flush with its own container');
  assert.ok(intersections.slice(0, -1).every(observer => observer.disconnected));
});
