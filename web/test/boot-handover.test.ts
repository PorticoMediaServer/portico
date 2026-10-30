import test from 'node:test';
import assert from 'node:assert/strict';
import {bootMotion, handoverDocumentBoot, revealBootDestination} from '../src/shell/boot-handover.ts';

function fixture(reduced = false) {
  const element = () => {
    const attributes = new Map<string, string>();
    return {attributes, removed: false, setAttribute(name: string, value: string) {attributes.set(name, value);},
      hasAttribute: (name: string) => attributes.has(name), removeAttribute: (name: string) => attributes.delete(name), remove() {this.removed = true;}};
  };
  const boot = element(), root = element(), page = element(), navigation = element();
  const document = {documentElement: {dataset: {}}, body: {dataset: {}}, defaultView: {matchMedia: () => ({matches: reduced})},
    getElementById: (id: string) => id === 'boot' ? boot.removed ? null : boot : id === 'root' ? root : null,
    querySelector: (selector: string) => selector === '[data-route-content]' ? page : selector === '[data-shell-navigation]' ? navigation : null} as unknown as Document;
  return {document, boot, root, page, navigation};
}

test('the cold picture retires once at 320ms and content completes sixty milliseconds later', t => {
  t.mock.timers.enable({apis: ['setTimeout']});
  const f = fixture();
  assert.equal(handoverDocumentBoot(f.document), true);
  assert.equal(f.boot.attributes.get('aria-hidden'), 'true');
  assert.equal(f.root.hasAttribute('data-boot-arriving'), true);
  assert.equal(handoverDocumentBoot(f.document), false, 'a React remount does not start a second fade');
  t.mock.timers.tick(319);
  assert.equal(f.boot.removed, false);
  t.mock.timers.tick(1);
  assert.equal(f.boot.removed, true);
  assert.equal(f.root.hasAttribute('data-boot-arriving'), true);
  t.mock.timers.tick(60);
  assert.equal(f.root.hasAttribute('data-boot-arriving'), false);
  assert.equal(handoverDocumentBoot(f.document), false, 'a warm resume does not resurrect the boot layer');
});

test('system or saved reduced-motion preference removes the content delay', t => {
  t.mock.timers.enable({apis: ['setTimeout']});
  for (const fromSystem of [true, false]) {
    const f = fixture(fromSystem);
    if (!fromSystem) f.document.documentElement.dataset.reduceMotion = 'true';
    assert.deepEqual(bootMotion(f.document), {duration: 160, contentDelay: 0});
    handoverDocumentBoot(f.document);
    t.mock.timers.tick(159);
    assert.equal(f.boot.removed, false);
    t.mock.timers.tick(1);
    assert.equal(f.boot.removed, true);
    assert.equal(f.root.hasAttribute('data-boot-arriving'), false);
  }
});

test('the resolved destination and live navigation have separate approved arrival clocks', t => {
  t.mock.timers.enable({apis: ['setTimeout']});
  const f = fixture();
  revealBootDestination(f.document);
  assert.equal(f.page.hasAttribute('data-boot-content-arriving'), true);
  assert.equal(f.navigation.hasAttribute('data-boot-navigation-arriving'), true);
  t.mock.timers.tick(320);
  assert.equal(f.navigation.hasAttribute('data-boot-navigation-arriving'), false);
  assert.equal(f.page.hasAttribute('data-boot-content-arriving'), true);
  t.mock.timers.tick(60);
  assert.equal(f.page.hasAttribute('data-boot-content-arriving'), false);
});
