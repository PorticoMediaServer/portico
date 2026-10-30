import test from 'node:test';
import assert from 'node:assert/strict';
import {Frame, elements} from './choice-handlers.ts';
import {navigateMenuArrow} from '../src/kit/overlays/menu-focus.ts';
const {SortMenu, CardSizeButton} = await import('../src/kit/browse/BrowseToolbar.tsx');
const {Menu, MenuRow} = await import('../src/kit/overlays/Popover.tsx');

test('sort and size menu arrows move focus without selecting or closing; activation selects', () => {
  for (const kind of ['sort', 'size']) {
    const calls: unknown[] = [], frame = new Frame();
    const sortProps = {sorts: ['title', 'date'].map(id => ({id, labelKey: 'next.browse.sort.dateAdded' as const, directions: ['asc', 'desc'] as const, defaultDirection: 'asc' as const, expensive: false, applicableKinds: []})),
      value: {field: 'title', direction: 'asc' as const, flippable: true}, label: (sort: {id: string}) => sort.id,
      open: true, onOpenChange: (open: boolean) => calls.push(open), onValueChange: (value: unknown) => calls.push(value)};
    const render = () => kind === 'sort' ? SortMenu(sortProps) : CardSizeButton({value: 'large', options: [{value: 'large', label: 'Large'}, {value: 'small', label: 'Small'}], onValueChange: value => calls.push(value)});
    const tree = frame.render(render) as any;
    assert.equal(tree.type, Menu, 'choices use the menu keyboard owner');
    const rows = elements(tree).filter(row => row.type === MenuRow);
    assert.equal(rows.length, 2);
    for (const row of rows) assert.equal(row.props.role, 'menuitemradio');
    assert.deepEqual(rows.map(row => row.props['aria-checked']), [true, false]);
    const doc = {activeElement: null as any};
    const nodes = rows.map(row => ({focus() {doc.activeElement = this;}, click: row.props.onClick}));
    doc.activeElement = nodes[0];
    const root = {ownerDocument: doc, querySelectorAll: () => nodes} as unknown as HTMLElement;
    for (const key of ['ArrowDown', 'ArrowUp', 'End', 'Home']) {
      let prevented = false;
      assert.equal(navigateMenuArrow({key, defaultPrevented: false, altKey: false, ctrlKey: false, metaKey: false, currentTarget: root, preventDefault() {prevented = true;}}), true);
      assert.equal(prevented, true); assert.deepEqual(calls, []);
    }
    nodes[1].click();
    assert.equal(calls.length, kind === 'sort' ? 2 : 1);
    if (kind === 'sort') assert.equal(calls[1], false);
    else assert.equal((frame.render(render) as any).props.open, false);
  }
});
