import {Paginator} from '../../src/epub/vendor/paginator.js';

// Source regression for Release's browser composition. This isolated synthetic
// section tests renderer layout under a strict parent style policy; it does not
// establish the public Reading route, fixture visibility or EPUB acceptance.
const assert = (value, message) => { if (!value) throw new Error(message); };
const frame = () => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
async function run() {
  const surface = document.getElementById('surface');
  const paginator = new Paginator();
  const url = URL.createObjectURL(new Blob([
    '<!doctype html><html><head><meta name="portico-style-nonce" content="publication-is-not-the-app"></head>' +
    '<body><p id="publication-text">A real section text range must occupy visible space inside the publication frame.</p></body></html>',
  ], {type: 'text/html'}));
  try {
    surface.append(paginator);
    paginator.open({dir: 'ltr', sections: [{linear: 'yes', load: () => url}]});
    paginator.setStyles(['body { --reader-before-style: admitted; }', 'body { font-size: 23px; color: rgb(12, 34, 56); }']);
    await paginator.goTo({index: 0});
    await frame();
    const doc = paginator.getContents()[0]?.doc;
    assert(doc?.body, 'opened publication document');
    const view = doc.defaultView;
    const iframe = view.frameElement;
    const iframeRect = iframe.getBoundingClientRect();
    assert(iframeRect.width > 0 && iframeRect.height > 0, 'publication iframe has positive layout dimensions');
    const text = doc.getElementById('publication-text');
    const range = doc.createRange();
    range.selectNodeContents(text);
    const rects = [...range.getClientRects()];
    assert(rects.some(rect => rect.width > 0 && rect.height > 0 && rect.right > 0 && rect.bottom > 0
      && rect.left < view.innerWidth && rect.top < view.innerHeight), 'publication text itself intersects the visible frame');
    const computed = view.getComputedStyle(doc.body);
    assert(computed.fontSize === '23px', 'renderer-owned after style admitted by strict policy');
    assert(computed.getPropertyValue('--reader-before-style').trim() === 'admitted', 'renderer-owned before style admitted by strict policy');
    assert([...doc.head.querySelectorAll('style')].every(style => style.nonce === 'reader-layout-case'), 'section renderer styles use the app nonce, never the publication meta');
    assert(iframe.ownerDocument.defaultView.getComputedStyle(iframe).visibility !== 'hidden' && view.getComputedStyle(doc.body).visibility !== 'hidden', 'publication is not hidden');
    return {status: 'pass', iframe: {width: iframeRect.width, height: iframeRect.height}, visibleTextRects: rects.length,
      qualification: 'Synthetic strict-CSP renderer layout only; public Reading visibility and no-egress acceptance remain separate.'};
  } finally {
    paginator.destroy();
    paginator.remove();
    URL.revokeObjectURL(url);
    window.readerCleanup = {surfaceChildren: surface.children.length};
  }
}
window.snapshotProof = run();
