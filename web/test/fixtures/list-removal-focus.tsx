import {useLayoutEffect, useRef, useState} from 'react';
import {createRoot} from 'react-dom/client';
import {Shelf} from '../../src/kit/layout/Row';
import {Dialog} from '../../src/kit/overlays/Dialog';
import {Menu} from '../../src/kit/overlays/Popover';

type Entry = Readonly<{id: string}>;
declare global {
  interface Window {
    listRemovalHarness?: {remove: (...ids: string[]) => void; replaceScope: () => void};
    disposeListRemoval?: () => void;
  }
}
/** Real Shelf -> presence.beforeLeave -> DOM owner -> real Radix return-focus composition.
 * A fixture publication is not an API, queue or catalogue model. Browser vectors remain UNRUN. */
export function mountListRemovalHarness() {
  const host = document.createElement('div');document.body.append(host);
  const root = createRoot(host);root.render(<Harness/>);
  return () => {root.unmount();host.remove();delete window.listRemovalHarness;};
}
function Harness() {
  const [items, setItems] = useState<Entry[]>(['alpha','bravo','charlie'].map(id => ({id})));
  const [scope, setScope] = useState<object>({});
  const [externalMenu, setExternalMenu] = useState<{entry: Entry; anchor: HTMLButtonElement}>();
  const shown = externalMenu && items.includes(externalMenu.entry) ? externalMenu : undefined;
  useLayoutEffect(() => {window.listRemovalHarness = {remove: (...ids) => setItems(previous => previous.filter(item => !ids.includes(item.id))), replaceScope: () => setScope({})};return () => {delete window.listRemovalHarness;};}, []);
  return <div data-list-owner-harness>
    <button data-outside>Outside</button>
    <Shelf heading="Original list" focusScope={scope} items={items} getKey={item => item.id} itemWidth={220}
      renderItem={item => <Item item={item} onExternalMenu={anchor => setExternalMenu({entry: item, anchor})}/>}/>
    {shown && <Menu open title={`External ${shown.entry.id}`} anchorRef={{current: shown.anchor}} onOpenChange={open => {if (!open) setExternalMenu(undefined);}}>
      <button data-external-action={shown.entry.id}>External original action</button>
    </Menu>}
  </div>;
}
function Item({item, onExternalMenu}: {item: Entry; onExternalMenu: (anchor: HTMLButtonElement) => void}) {
  const opener = useRef<HTMLButtonElement>(null), cancel = useRef<HTMLButtonElement>(null);
  const [menu, setMenu] = useState(false), [dialog, setDialog] = useState(false);
  return <article data-original-item={item.id}>
    <button data-layout-primary data-primary={item.id}>Open {item.id}</button>
    <Menu open={menu} title={`Menu ${item.id}`} onOpenChange={setMenu} trigger={<button data-menu-opener={item.id}>More</button>}>
      <button data-menu-action={item.id}>Original action</button>
    </Menu>
    <button ref={opener} data-dialog-opener={item.id} onClick={() => setDialog(true)}>Stop</button>
    <button data-external-opener={item.id} onClick={event => onExternalMenu(event.currentTarget)}>External menu</button>
    <Dialog open={dialog} onOpenChange={setDialog} title={`Original ${item.id}`} initialFocusRef={cancel} returnFocusRef={opener}
      footer={<button ref={cancel} data-dialog-cancel={item.id} onClick={() => setDialog(false)}>Cancel</button>}>Original dialog</Dialog>
  </article>;
}
