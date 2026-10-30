import test from 'node:test';
import assert from 'node:assert/strict';
import {registerHooks} from 'node:module';
import {Children, createElement, isValidElement, type ReactNode} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {transformTsx} from '../transform-tsx.mjs';
import type {ActivityPanelView} from '../../src/kit/shell/Activity';

registerHooks({load(url, context, next) {
  if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
  if (url.endsWith('.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
  return next(url, context);
}});
const {ActivityPanel, TopBarActivity} = await import('../../src/kit/shell/Activity.tsx');
const {CardFrame} = await import('../../src/kit/cards/CardFrame.tsx');
const {SessionCard} = await import('../../src/kit/cards/FeatureCards.tsx');
const {PickerAddedToast} = await import('../../src/kit/selection/SelectionPicker.tsx');
const {ToastHost} = await import('../../src/kit/overlays/Feedback.tsx');
const {SongRow} = await import('../../src/kit/browse/TrackTable.tsx');
const idle = (): ActivityPanelView => ({serverName: 'Actual server', phase: 'loading', streams: [], scans: [], tasks: [], onRetry() {}, onDashboard() {}});

test('cold Activity has no guessed health, stream count, quiet state or update dot', () => {
  for (const phase of ['loading', 'error'] as const) {
    const view = {...idle(), phase, health: 'normal' as const, streamCount: 0, taskCount: 0, update: {available: true, onOpen() {}}};
    const html = renderToStaticMarkup(createElement(ActivityPanel, {viewerRole: 'owner', view}));
    assert.doesNotMatch(html, /Running normally|No streams|0 streams|Nothing playing or scanning|Server update available/);
    const trigger = renderToStaticMarkup(createElement(TopBarActivity, {viewerRole: 'owner', view, open: false, onOpenChange() {}}));
    assert.match(trigger, /aria-label="Server activity"/);
    assert.doesNotMatch(trigger, /streamCount|updateDot|No streams|0 streams|data-busy/);
  }
});

test('known ready zero remains a real zero; missing ready facts remain absent', () => {
  const known = {...idle(), phase: 'ready' as const, health: 'normal' as const, streamCount: 0, taskCount: 0};
  const html = renderToStaticMarkup(createElement(ActivityPanel, {viewerRole: 'owner', view: known}));
  assert.match(html, /Running normally/); assert.match(html, /No streams/); assert.match(html, /Nothing playing or scanning/);
  const unknown = renderToStaticMarkup(createElement(ActivityPanel, {viewerRole: 'owner', view: {...idle(), phase: 'ready'}}));
  assert.doesNotMatch(unknown, /Running normally|No streams|Nothing playing or scanning/);
  assert.equal(renderToStaticMarkup(createElement(TopBarActivity, {viewerRole: 'member', view: known, open: false, onOpenChange() {}})), '');
});

test('a card has one real More button with the supplied expanded state', () => {
  const html = renderToStaticMarkup(createElement(CardFrame, {model: {title: 'Actual title', caption: []}, kind: 'poster', onNavigate() {}, onMore() {}, moreExpanded: true, children: createElement('span', {}, 'Actual artwork')}));
  assert.equal((html.match(/aria-haspopup="menu"/g) ?? []).length, 1);
  assert.match(html, /aria-expanded="true"/);
  assert.equal((html.match(/<button/g) ?? []).length, 2);
});

test('SessionCard forwards the More button currentTarget and owns its expanded menu state', () => {
  const calls: unknown[] = [];
  const props = {model: {title: 'Actual title', caption: []}, viewer: {name: 'Actual viewer', device: 'Actual device'}, facts: [], onNavigate() {}, onMore: (anchor: HTMLButtonElement) => calls.push(anchor), moreExpanded: true};
  const html = renderToStaticMarkup(createElement(SessionCard, props));
  assert.equal((html.match(/aria-label="More: Actual title"/g) ?? []).length, 1);
  assert.equal((html.match(/aria-haspopup="menu"/g) ?? []).length, 1);
  assert.match(html, /aria-expanded="true"/);
  assert.equal((html.match(/<button/g) ?? []).length, 2);
  type ActionProps = {children?: ReactNode; icon?: string; onClick?: (event: {currentTarget: object; target: object}) => void};
  function moreAction(node: ReactNode): ActionProps | undefined {
    for (const child of Children.toArray(node)) {
      if (!isValidElement<ActionProps>(child)) continue;
      if (child.props.icon === 'i-more') return child.props;
      const nested = moreAction(child.props.children);
      if (nested) return nested;
    }
  }
  const action = moreAction(SessionCard(props).props.overlay);
  assert.ok(action?.onClick);
  // Dispatch contract only: the event target can be the glyph; the anchor is currentTarget.
  const currentTarget = {tagName: 'BUTTON'}, target = {tagName: 'svg'};
  action.onClick({currentTarget, target});
  assert.deepEqual(calls, [currentTarget]);
});

test('the search SongRow owns its search geometry and omits the ordinal', () => {
  const row = {id: 'song', number: 42, text: {id: 'song', title: 'Actual song', caption: [], detail: []}, artwork: createElement('img', {alt: '', src: '/actual-art'})};
  const search = renderToStaticMarkup(createElement(SongRow, {row, context: 'search', onPlay() {}}));
  assert.match(search, /data-context="search"/);
  assert.match(search, /songArtworkPlay/);
  assert.doesNotMatch(search, /songNumber|>42</);
  const list = renderToStaticMarkup(createElement(SongRow, {row, onPlay() {}}));
  assert.match(list, /songNumber/); assert.match(list, />42</);
});

test('the card owns one named selection control around a decorative circle', () => {
  const html = renderToStaticMarkup(createElement(CardFrame, {model: {title: 'Actual title', caption: []}, kind: 'poster', onNavigate() {}, onMore() {}, selection: {active: true, checked: true, control: createElement('span', {'aria-hidden': true}, 'circle'), onToggle() {}, onExit() {}}, children: createElement('span', {}, 'Actual artwork')}));
  assert.equal((html.match(/<button/g) ?? []).length, 3);
  assert.match(html, /class="selection"[^>]*aria-label="[^"]*Actual title/);
  assert.match(html, /class="selection"[^>]*aria-pressed="true"/);
  assert.doesNotMatch(html, /<button[^>]*>(?:(?!<\/button>).)*<button/s);
});

test('picker success binds its exact data and dismissal to the actual ToastHost', () => {
  const calls: unknown[] = [];
  const props = {open: true, onOpenChange: (...args: unknown[]) => calls.push(args), selection: {kind: 'movie' as const, count: 200}, destination: 'playlist' as const, result: {destination: {id: 'actual-playlist', name: 'Actual playlist'}, inserted: 3, alreadyIn: 197, failed: 0, complete: true, failures: []}, onView: (id: string) => calls.push(id), bottomInset: 156};
  const element = PickerAddedToast(props);
  assert.equal(element.type, ToastHost);
  assert.equal(element.props.bottomInset, 156);
  assert.equal(element.props.toastClassName, 'pickerToast');
  const copy = renderToStaticMarkup(element.props.toast!.message);
  assert.match(copy, /Added 3 movies to Actual playlist/);
  assert.match(copy, /197 were already there/);
  assert.doesNotMatch(copy, /Added 200/);
  element.props.toast!.action!.onAction();
  element.props.onDismiss('actual-playlist', 'gesture');
  assert.deepEqual(calls, ['actual-playlist', [false, 'gesture']]);
  assert.equal(PickerAddedToast({...props, open: false}).props.toast, null);
  assert.equal(PickerAddedToast({...props, result: {...props.result, failed: 1}}).props.toast, null);
});

test('qualified Activity issue replaces normal health, preserves streams and updates, and is role/phase guarded', () => {
  const majorIssue = {title: 'Qualified source issue', description: 'Remote source', onOpen() {}};
  const view: ActivityPanelView = {...idle(), phase: 'ready', health: 'normal', streamCount: 24, majorIssue, update: {available: true, onOpen() {}}};
  const html = renderToStaticMarkup(createElement(ActivityPanel, {viewerRole: 'owner', view}));
  assert.match(html, /A problem to look at/); assert.doesNotMatch(html, /Running normally/);
  assert.match(html, /data-major-issue="true"/); assert.match(html, /Qualified source issue/);
  assert.match(html, /24 streams/); assert.match(html, /Server update available/);
  const trigger = renderToStaticMarkup(createElement(TopBarActivity, {viewerRole: 'owner', view, open: false, onOpenChange() {}}));
  assert.match(trigger, /class="majorDot"/); assert.doesNotMatch(trigger, /class="updateDot"/);
  assert.match(trigger, /24 streams, update available, a major issue: Qualified source issue/);
  assert.match(trigger, /Qualified source issue. Server update available/);
  for (const phase of ['loading', 'error'] as const) {
    const guarded = {...view, phase};
    assert.doesNotMatch(renderToStaticMarkup(createElement(ActivityPanel, {viewerRole: 'owner', view: guarded})), /data-major-issue|A problem to look at|Qualified source issue/);
    assert.doesNotMatch(renderToStaticMarkup(createElement(TopBarActivity, {viewerRole: 'owner', view: guarded, open: false, onOpenChange() {}})), /majorDot|Qualified source issue/);
  }
  for (const viewerRole of ['member', 'administrator'] as const) {
    assert.equal(renderToStaticMarkup(createElement(ActivityPanel, {viewerRole, view})), '');
    assert.equal(renderToStaticMarkup(createElement(TopBarActivity, {viewerRole, view, open: true, onOpenChange() {}})), '');
  }
  const resolved = renderToStaticMarkup(createElement(TopBarActivity, {viewerRole: 'owner', view: {...view, majorIssue: undefined}, open: false, onOpenChange() {}}));
  assert.match(resolved, /updateDot/); assert.doesNotMatch(resolved, /majorDot|Qualified source issue/);
  const attention = renderToStaticMarkup(createElement(TopBarActivity, {viewerRole: 'owner', view: {...view, majorIssue: undefined, update: undefined, health: 'attention'}, open: false, onOpenChange() {}}));
  assert.doesNotMatch(attention, /majorDot|updateDot/);
});
