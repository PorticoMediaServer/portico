/*
 * The browser floor: Chrome and Edge 111, Firefox 113, Safari 16.4 (iOS and iPadOS 16.4). The
 * build targets exactly these (vite.config.ts `browserFloor`), and README.md states them.
 *
 * This runs before the app as a classic script in the oldest syntax, so an engine below the floor
 * says so plainly instead of drawing an empty page or failing half way through. It tests what the
 * floor stands for, not a version number: colour mixing in CSS (every surface uses it), the
 * language features of that generation, and module scripts. The words come from the catalogue
 * when the build writes this file (`__PORTICO_*__`).
 */
(function () {
  var title = __PORTICO_UNSUPPORTED_TITLE__;
  var body = __PORTICO_UNSUPPORTED_BODY__;
  function supported() {
    try {
      return typeof CSS !== 'undefined' && typeof CSS.supports === 'function' && CSS.supports('color', 'color-mix(in srgb, red, blue)')
        && typeof Array.prototype.at === 'function' && typeof Object.hasOwn === 'function' && typeof structuredClone === 'function'
        && typeof String.prototype.replaceAll === 'function' && typeof ResizeObserver === 'function'
        && 'noModule' in document.createElement('script');
    } catch (e) {
      return false;
    }
  }
  if (supported()) return;
  window.__porticoUnsupportedBrowser = true;
  function show() {
    var root = document.getElementById('root');
    if (!root) return;
    while (root.firstChild) root.removeChild(root.firstChild);
    var box = document.createElement('main');
    box.setAttribute('role', 'alert');
    // CSSOM, not a style attribute: the page's content security policy refuses inline styles.
    box.style.maxWidth = '32em';
    box.style.margin = '15vh auto 0';
    box.style.padding = '0 24px';
    box.style.fontFamily = 'system-ui, sans-serif';
    box.style.lineHeight = '1.5';
    box.style.color = '#e8eef4';
    document.body.style.background = '#071018';
    var heading = document.createElement('h1');
    heading.style.fontSize = '1.5em';
    heading.appendChild(document.createTextNode(title));
    var text = document.createElement('p');
    text.appendChild(document.createTextNode(body));
    box.appendChild(heading);
    box.appendChild(text);
    root.appendChild(box);
    // Unsupported engines never mount React, so their real error page owns
    // this hand-off too. Keep the first-paint layer until the message exists.
    var boot = document.getElementById('boot');
    if (boot && boot.parentNode) boot.parentNode.removeChild(boot);
  }
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', show);
  else show();
})();
