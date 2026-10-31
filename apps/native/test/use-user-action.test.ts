// @fabric-react
import test from 'node:test';
import assert from 'node:assert/strict';
import {fabricHost, React} from './helpers/fabric-host.mjs';
import {useUserAction} from '../src-next/kit/states/use-user-action';
import type {UserAction, UserActionContext, UserActionSnapshot} from '../../../packages/client-core/src/user-action';

test('useUserAction renders each phase, uses the latest run, and unmount cancels the attempt', async () => {
  const host = await fabricHost(), contexts: UserActionContext[] = [];
  let finish: (value: string) => void = () => {}, label = 'first', shown: UserActionSnapshot<string> | undefined, action: UserAction<string> | undefined;
  function Consumer({tag}: {tag: string}) {
    [shown, action] = useUserAction<string>({run: context => {contexts.push(context); return new Promise<string>(resolve => {finish = value => resolve(`${tag}:${value}`);});}});
    return null;
  }
  try {
    await host.render(React.createElement(Consumer, {tag: label}));
    assert.equal(shown?.phase, 'idle');
    label = 'second';
    await host.render(React.createElement(Consumer, {tag: label}));
    action!.start();
    await host.settle();
    assert.equal(shown?.phase, 'working');
    finish('ok');
    await host.settle();
    assert.deepEqual(shown, {phase: 'done', value: 'second:ok'});
    action!.start();
    await host.settle();
  } finally {await host.unmount();}
  assert.equal(contexts.length, 2);
  assert.equal(contexts[1]!.signal.aborted, true, 'unmount cancels the attempt in flight');
});
