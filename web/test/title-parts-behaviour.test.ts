import test from 'node:test';
import assert from 'node:assert/strict';
import {componentModule} from './helpers/component-harness.mjs';
import {all, createRenderer, one, textOf, tree} from './helpers/interaction-harness.mjs';

async function loadTitleParts() {
  const h = createRenderer();
  const styles = new Proxy({}, {get: (_, key) => String(key)});
  const mod = await componentModule(new URL('../src/kit/title/TitleParts.tsx', import.meta.url), {
    react: h.react,
    '@i18n': {defaultI18n: {t: (key: string) => key}},
    '../controls': {Button: 'Button'},
    '../artwork': {Artwork: 'Artwork'},
    './title-parts.module.css': {default: styles},
  });
  return {h, mod};
}

test('SearchResultHero parses synopsis inline markup via TitleProse instead of showing raw tags', async () => {
  const {h, mod} = await loadTitleParts();
  const rendered = tree(h.render(() => mod.SearchResultHero({
    title: 'Arrival', eyebrow: 'Top result', artwork: null, facts: null,
    synopsis: 'A <b>bold</b> and <i>quiet</i><br>story.', actions: null,
  })));
  assert.equal(textOf(one(rendered, 'strong')), 'bold');
  assert.equal(textOf(one(rendered, 'em')), 'quiet');
  assert.equal(all(rendered, 'br').length, 1);
  const prose = all(rendered, 'p').find(node => node.props['data-variant'] === 'hero');
  assert.ok(prose);
  assert.equal(textOf(prose).includes('<b>'), false);
  assert.equal(textOf(prose).includes('<br>'), false);
});

test('EpisodeDetailsLayout parses synopsis inline markup via TitleProse instead of showing raw tags', async () => {
  const {h, mod} = await loadTitleParts();
  const rendered = tree(h.render(() => mod.EpisodeDetailsLayout({
    title: 'Pilot', context: null, facts: null, actions: null, onClose() {},
    synopsis: 'First <b>contact</b><br/>with <em>unknown</em> signals.',
  })));
  assert.equal(textOf(one(rendered, 'strong')), 'contact');
  assert.equal(textOf(one(rendered, 'em')), 'unknown');
  assert.equal(all(rendered, 'br').length, 1);
  const prose = one(rendered, 'p');
  assert.equal(textOf(prose).includes('<b>'), false);
  assert.equal(textOf(prose).includes('<em>'), false);
});

test('SearchResultHero renders a navigable title link supporting onOpen and native modifier-click behavior', async () => {
  const {h, mod} = await loadTitleParts();
  let opened = 0;
  const rendered = tree(h.render(() => mod.SearchResultHero({
    title: 'Arrival', eyebrow: 'Top result', artwork: null, facts: null, actions: null,
    href: '/item/arrival', onOpen: () => { opened++; },
  })));
  const links = all(rendered, 'a');
  assert.equal(links.length, 2);
  // First link is the visible heading link so keyboard / ArrowDown focuses it, not the aria-hidden backdrop link.
  const [titleLink, backdropLink] = links;
  assert.equal(titleLink.props.href, '/item/arrival');
  assert.equal(textOf(titleLink), 'Arrival');
  assert.equal(titleLink.props['aria-hidden'], undefined);
  assert.equal(backdropLink.props.href, '/item/arrival');
  assert.equal(backdropLink.props['aria-hidden'], 'true');
  assert.equal(backdropLink.props.tabIndex, -1);

  let prevented = 0;
  titleLink.props.onClick({defaultPrevented: false, button: 0, metaKey: false, ctrlKey: false, shiftKey: false, altKey: false, preventDefault() { prevented++; }});
  assert.equal(prevented, 1);
  assert.equal(opened, 1);

  backdropLink.props.onClick({defaultPrevented: false, button: 0, metaKey: false, ctrlKey: false, shiftKey: false, altKey: false, preventDefault() { prevented++; }});
  assert.equal(prevented, 2);
  assert.equal(opened, 2);

  // Middle-click and modifier clicks keep native browser navigation (new tab / window).
  for (const event of [
    {button: 1, metaKey: false, ctrlKey: false, shiftKey: false, altKey: false},
    {button: 0, metaKey: true, ctrlKey: false, shiftKey: false, altKey: false},
    {button: 0, metaKey: false, ctrlKey: true, shiftKey: false, altKey: false},
    {button: 0, metaKey: false, ctrlKey: false, shiftKey: true, altKey: false},
    {button: 0, metaKey: false, ctrlKey: false, shiftKey: false, altKey: true},
  ]) {
    titleLink.props.onClick({defaultPrevented: false, ...event, preventDefault() { prevented++; }});
  }
  assert.equal(prevented, 2);
  assert.equal(opened, 2);
});

test('SearchResultHero falls back to a button heading when onOpen is provided without href, and plain heading without either', async () => {
  const {h, mod} = await loadTitleParts();
  let opened = 0;
  const buttonRendered = tree(h.render(() => mod.SearchResultHero({
    title: 'Arrival', eyebrow: 'Top result', artwork: null, facts: null, actions: null,
    onOpen: () => { opened++; },
  })));
  const titleButton = one(buttonRendered, 'button');
  assert.equal(titleButton.props.type, 'button');
  assert.equal(textOf(titleButton), 'Arrival');
  titleButton.props.onClick();
  assert.equal(opened, 1);

  const plainRendered = tree(h.render(() => mod.SearchResultHero({
    title: 'Arrival', eyebrow: 'Top result', artwork: null, facts: null, actions: null,
  })));
  assert.equal(all(plainRendered, 'a').length, 0);
  assert.equal(all(plainRendered, 'button').length, 0);
  assert.equal(textOf(one(plainRendered, 'h3')), 'Arrival');
});
