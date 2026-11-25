import test from 'node:test';
import assert from 'node:assert/strict';
import {componentModule} from '../helpers/component-harness.mjs';
import {all, createRenderer, one, textOf, tree} from '../helpers/interaction-harness.mjs';

async function loadButton() {
  const h = createRenderer();
  const styles = new Proxy({}, {get: (_, key) => String(key)});
  const mod = await componentModule(new URL('../../src/kit/controls/Button.tsx', import.meta.url), {
    react: h.react,
    '../Icon': {Icon: 'Icon'},
    './model': {progressFraction: (value: number) => value},
    './controls.module.css': {default: styles},
  });
  return {h, Button: mod.Button};
}

test('Button renders a spinner when loading even without an icon prop', async () => {
  const {h, Button} = await loadButton();
  const rendered = tree(h.render(() => Button({loading: true, children: 'Save'})));
  const button = one(rendered, 'button');
  assert.equal(button.props.disabled, true);
  assert.equal(button.props['aria-busy'], true);
  const spinner = all(rendered, 'span').find(node => node.props.className === 'spinner');
  assert.ok(spinner, 'expected spinner span for iconless loading button');
  assert.equal(spinner.props['aria-hidden'], 'true');
  assert.equal(all(rendered, 'Icon').length, 0);
  const label = all(rendered, 'span').find(node => node.props.className === 'buttonLabel');
  assert.equal(textOf(label), 'Save');
});

test('Button replaces its icon with a spinner when loading and honors iconSize', async () => {
  const {h, Button} = await loadButton();
  const withIcon = tree(h.render(() => Button({loading: true, icon: 'i-play', iconSize: 16, children: 'Play'})));
  assert.equal(all(withIcon, 'Icon').length, 0);
  const spinner = all(withIcon, 'span').find(node => node.props.className === 'spinner');
  assert.ok(spinner);
  assert.deepEqual(spinner.props.style, {width: 16, height: 16});

  const iconOnly = tree(h.render(() => Button({loading: true, icon: 'i-x', iconOnly: true, 'aria-label': 'Close'})));
  assert.equal(one(iconOnly, 'button').props['data-icon-only'], true);
  assert.equal(all(iconOnly, 'Icon').length, 0);
  assert.equal(all(iconOnly, 'span').filter(node => node.props.className === 'spinner').length, 1);
  assert.equal(all(iconOnly, 'span').filter(node => node.props.className === 'buttonLabel').length, 0);
});

test('Button renders its icon when not loading and renders neither icon nor spinner when idle and iconless', async () => {
  const {h, Button} = await loadButton();
  const idleWithIcon = tree(h.render(() => Button({icon: 'i-check', children: 'Saved'})));
  assert.equal(one(idleWithIcon, 'Icon').props.name, 'i-check');
  assert.equal(all(idleWithIcon, 'span').some(node => node.props.className === 'spinner'), false);
  assert.equal(one(idleWithIcon, 'button').props['aria-busy'], undefined);

  const idlePlain = tree(h.render(() => Button({children: 'Save'})));
  assert.equal(all(idlePlain, 'Icon').length, 0);
  assert.equal(all(idlePlain, 'span').some(node => node.props.className === 'spinner'), false);
  assert.equal(one(idlePlain, 'button').props.disabled, false);
  assert.equal(one(idlePlain, 'button').props['aria-busy'], undefined);
});
