import test, {type TestContext} from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {registerHooks} from 'node:module';

type Effect = {deps: readonly unknown[]; cleanup?: () => void};
const bridgeKey = '__porticoProfilePINFeedbackHooks';
const bridge = {current: undefined as HookFrame | undefined};
(globalThis as Record<string, unknown>)[bridgeKey] = bridge;
const hookModule = 'data:text/javascript,' + encodeURIComponent(`const bridge = globalThis.${bridgeKey}; ${['useRef', 'useState', 'useEffect'].map(name => `export const ${name} = (...args) => bridge.current.${name}(...args);`).join('\n')}`);
const loader = registerHooks({load(url, context, next) {
  if (url.endsWith('/kit/accounts/pin-feedback.ts')) return {format: 'module-typescript', shortCircuit: true,
    source: readFileSync(new URL(url), 'utf8').replace("from 'react'", `from ${JSON.stringify(hookModule)}`)};
  return next(url, context);
}});
const {usePINFeedback} = await import('../src/kit/accounts/pin-feedback.ts');
test.after(() => {loader.deregister(); delete (globalThis as Record<string, unknown>)[bridgeKey];});

// Executes the real hook with dependency equality and effect cleanup. This is
// deterministic timer/lifetime evidence, not React DOM or visual acceptance.
class HookFrame {
  private slots: any[] = [];
  private cursor = 0;
  private mounted = true;
  private queued: {effect: Effect; run: () => void | (() => void)}[] = [];
  useState(initial: unknown) {
    const index = this.cursor++;
    this.slots[index] ??= {value: initial};
    return [this.slots[index].value, (value: unknown) => {assert.ok(this.mounted, 'no writes after unmount'); this.slots[index].value = value;}];
  }
  useRef(value: unknown) {const index = this.cursor++; return this.slots[index] ??= {current: value};}
  useEffect(run: () => void | (() => void), deps: readonly unknown[]) {
    const index = this.cursor++, previous: Effect | undefined = this.slots[index];
    if (!previous || deps.length !== previous.deps.length || deps.some((value, at) => !Object.is(value, previous.deps[at]))) {
      const effect = {deps, cleanup: previous?.cleanup}; this.slots[index] = effect; this.queued.push({effect, run});
    }
  }
  render(rejection?: unknown) {
    bridge.current = this; this.cursor = 0; this.queued = [];
    const result = usePINFeedback('', rejection);
    this.queued.forEach(job => job.effect.cleanup?.());
    this.queued.forEach(job => {job.effect.cleanup = job.run() ?? undefined;});
    return result;
  }
  unmount() {if (!this.mounted) return; this.mounted = false; for (const slot of this.slots) slot?.cleanup?.();}
}
function fixture(t: TestContext) {
  t.mock.timers.enable({apis: ['setTimeout']});
  const hooks = new HookFrame(); t.after(() => hooks.unmount());
  return hooks;
}

test('the fourth digit stays during the request and clears exactly 400ms after rejection', t => {
  const hooks = fixture(t);
  hooks.render().change('1234');
  t.mock.timers.tick(5000);
  assert.equal(hooks.render().pin, '1234', 'pending alone never clears the submitted digits');
  const rejection = {};
  assert.equal(hooks.render(rejection).pin, '1234');
  t.mock.timers.tick(399);
  assert.equal(hooks.render(rejection).pin, '1234');
  t.mock.timers.tick(1);
  assert.equal(hooks.render(rejection).pin, '');
});

test('typing retires the old clear and a second rejection receives its own full interval', t => {
  const hooks = fixture(t), first = {}, second = {};
  hooks.render().change('1234'); hooks.render(first);
  t.mock.timers.tick(200);
  hooks.render(first).change('5');
  t.mock.timers.tick(200);
  assert.equal(hooks.render(first).pin, '5', 'a late clear cannot erase newly typed input');
  hooks.render(first).change('5678'); hooks.render(second);
  t.mock.timers.tick(399);
  assert.equal(hooks.render(second).pin, '5678');
  t.mock.timers.tick(1);
  assert.equal(hooks.render(second).pin, '');
});

test('dismissal cancels a rejected PIN clear before it can update a retired component', t => {
  const hooks = fixture(t);
  hooks.render().change('1234'); hooks.render({}); hooks.unmount();
  assert.doesNotThrow(() => t.mock.timers.tick(400));
});
