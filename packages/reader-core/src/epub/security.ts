import DOMPurify from 'dompurify';
import {EPUB_LIMITS} from './limits.ts';
import {ReaderError} from '../errors.ts';

// A blob inherits the app's origin. Foliate requires same-origin DOM access,
// and WebKit requires allow-scripts for events: enforce this CSP in the book
// itself BEFORE the browser sees any untrusted element. No book network access.
export const BOOK_CSP = "default-src 'none'; script-src 'none'; connect-src 'none'; img-src blob: data:; style-src 'unsafe-inline' blob:; font-src blob:; media-src 'none'; object-src 'none'; frame-src 'none'; base-uri 'none'; form-action 'none'";
const forbidden = ['script','iframe','frame','frameset','object','embed','applet','form','input','button','textarea','select','meta','base','audio','video','source','track','foreignObject','animate','animateMotion','animateTransform','set'];
function localReference(value: string, transformed: boolean): boolean {
  const v = value.trim();
  if (!v || v.startsWith('#')) return true;
  if (transformed && /^blob:/.test(v)) return true;
  if (/^data:image\/(?:png|jpeg|gif|webp);base64,[a-z0-9+/=]+$/i.test(v) && v.length <= 2*1024*1024) return true;
  return !/[\\\x00-\x20]/.test(v) && !v.startsWith('/') && !/^[a-z][a-z0-9+.-]*:/i.test(v);
}
function decodedCss(value:string):string {
  return value.replace(/\\([0-9a-f]{1,6})\s?|\\(.)/gi,(_all,hex:string|undefined,char:string|undefined)=>hex?String.fromCodePoint(Math.min(0x10ffff,parseInt(hex,16))):char??'');
}
function cleanDeclarations(style:CSSStyleDeclaration,transformed:boolean) {
  for(const property of Array.from(style)) {
    const value=decodedCss(style.getPropertyValue(property));let unsafe=/image-set\s*\(|(?:^|[^a-z-])src\s*\(/i.test(value);
    for(const match of value.matchAll(/url\(\s*(?:"([^"]*)"|'([^']*)'|([^)]*))\s*\)/gi))
      if(!localReference(match[1]??match[2]??match[3]??'',transformed))unsafe=true;
    if(unsafe)style.removeProperty(property);
  }
}
function localImports(raw:string,transformed:boolean):string[] {
  const imports:string[]=[];let depth=0;
  const quoteEnd=(start:number)=>{let end=start+1;for(;end<raw.length;end++){if(raw[end]==='\\')end++;else if(raw[end]===raw[start])return end;}return raw.length;};
  for(let at=0;at<raw.length;at++) {
    if(raw.startsWith('/*',at)){const end=raw.indexOf('*/',at+2);at=end<0?raw.length:end+1;continue;}
    if(raw[at]==='"'||raw[at]==="'"){at=quoteEnd(at);continue;}
    if(raw[at]==='\\'){at++;continue;}
    if(raw[at]==='{'){depth++;continue;}if(raw[at]==='}'){depth=Math.max(0,depth-1);continue;}
    if(depth||!/^@import(?=[\s("'])/i.test(raw.slice(at,at+9)))continue;
    let end=at,parens=0;
    for(;end<raw.length;end++) {
      if(raw[end]==='"'||raw[end]==="'"){end=quoteEnd(end);continue;}
      if(raw[end]==='(')parens++;else if(raw[end]===')')parens--;
      else if((raw[end]===';'&&!parens)||raw[end]==='{'||raw[end]==='}')break;
    }
    const rule=raw.slice(at,end),match=/^@import\s+(?:url\(\s*(?:"((?:\\.|[^"\\])*)"|'((?:\\.|[^'\\])*)'|([^\s()"']+))\s*\)|"((?:\\.|[^"\\])*)"|'((?:\\.|[^'\\])*)')\s*([^{};]*)$/i.exec(rule);
    at=end;if(!match)continue;
    const url=decodedCss(match[1]??match[2]??match[3]??match[4]??match[5]??''),condition=match[6].trim();
    if(!url||/["'()\\]/.test(url)||!localReference(url,transformed)||(transformed&&!/^blob:/.test(url))||!/^[a-z\d_\s():.,-]*$/i.test(condition))continue;
    imports.push(`@import url("${url}")${condition?' '+condition:''};`);
  }
  return imports;
}
/** A detached constructed sheet cannot load @import. Parse CSS with the browser
 * (including escape normalization), then retain only non-network declarations.
 * Local imports are retained separately for the bounded publication rewriter.
 * CSP independently blocks resources if a future CSS feature is introduced. */
export function secureCss(raw:string,transformed=false,maxLength=EPUB_LIMITS.memberBytes):string {
  if(raw.length>maxLength)throw new ReaderError('limit_exceeded');
  const sheet=new CSSStyleSheet();sheet.replaceSync(raw);
  function clean(rules:CSSRuleList) {
    for(const rule of Array.from(rules)) {
      if('style' in rule)cleanDeclarations((rule as CSSStyleRule).style,transformed);
      if('cssRules' in rule)clean((rule as CSSGroupingRule).cssRules);
    }
  }
  clean(sheet.cssRules);return [...localImports(raw,transformed),...Array.from(sheet.cssRules,r=>r.cssText)].join('\n');
}
const quoted = `(?:"[^"<>[\\]]*"|'[^'<>[\\]]*')`;
/** One standard document type: `html` or `svg`, bare or naming its public or system DTD (as EPUB 2
 * and Project Gutenberg's XHTML 1.1 do), never with an internal subset. The parser never fetches the DTD. */
const standardDoctype = new RegExp(`<!DOCTYPE\\s+(?:html|svg)(?:\\s+(?:PUBLIC\\s+${quoted}(?:\\s+${quoted})?|SYSTEM\\s+${quoted}))?\\s*>`, 'i');
/** A document declares no entity and no document type beyond one standard one. */
export function markupDeclarationsSafe(raw: string): boolean {
  return !/<!\s*(?:DOCTYPE|ENTITY)\b/i.test(raw.replace(standardDoctype, ''));
}
export function secureMarkup(raw: string, mime: 'application/xhtml+xml' | 'text/html' | 'image/svg+xml', transformed = false, maxLength = EPUB_LIMITS.memberBytes): string {
  if (raw.length > maxLength || !markupDeclarationsSafe(raw)) throw new ReaderError('invalid_book');
  // Always parse XML first: unlike text/html, inert XML parsing cannot initiate
  // img/iframe loads. DOMPurify then handles namespaces and mutation-XSS.
  const doc = new DOMParser().parseFromString(raw, mime === 'text/html' ? 'application/xhtml+xml' : mime);
  if (doc.querySelector('parsererror') || !['html','svg'].includes(doc.documentElement.localName)) throw new ReaderError('invalid_book');
  for (const node of Array.from(doc.querySelectorAll('*'))) {
    if (forbidden.includes(node.localName)) {node.remove(); continue;}
    if(node.localName.toLowerCase()==='style')node.textContent=secureCss(node.textContent??'',transformed,maxLength);
    for (const attr of Array.from(node.attributes)) {
      const name = attr.localName.toLowerCase();
      if(name==='style') {
        const sheet=new CSSStyleSheet();sheet.replaceSync(`x{${attr.value}}`);
        const style=(sheet.cssRules[0] as CSSStyleRule|undefined)?.style;
        if(style){cleanDeclarations(style,transformed);attr.value=style.cssText;}else node.removeAttributeNode(attr);
        continue;
      }
      if (name.startsWith('on') || ['srcdoc','srcset','ping','action','formaction','target','download','autofocus','http-equiv'].includes(name)) node.removeAttributeNode(attr);
      else if (['href','src','poster','data'].includes(name) && !localReference(attr.value, transformed)) node.removeAttributeNode(attr);
    }
  }
  DOMPurify.sanitize(doc.documentElement, {
    IN_PLACE: true,
    WHOLE_DOCUMENT: true, ADD_TAGS: ['link'], ADD_ATTR: ['epub:type'], FORBID_TAGS: forbidden,
    ALLOW_DATA_ATTR: false, ALLOW_UNKNOWN_PROTOCOLS: false,
    // local archive references and generated blob references only; network
    // references were removed above and CSP is the independent backstop.
    ALLOWED_URI_REGEXP: /^(?:(?:blob:|data:image\/(?:png|jpeg|gif|webp);base64,)|[^a-z]|[a-z+.-]+(?:[^a-z+.:\-]|$))/i,
  });
  bookStyles(doc, transformed);
  if (mime === 'image/svg+xml') return new XMLSerializer().serializeToString(doc.documentElement);
  const ns = 'http://www.w3.org/1999/xhtml';
  let head = doc.querySelector('head');
  if (!head) {head = doc.createElementNS(ns, 'head'); doc.documentElement.prepend(head);}
  const meta = doc.createElementNS(ns, 'meta'); meta.setAttribute('http-equiv', 'Content-Security-Policy'); meta.setAttribute('content', BOOK_CSP);
  head.prepend(meta);
  const policy = doc.createElementNS(ns, 'meta'); policy.setAttribute('name', 'referrer'); policy.setAttribute('content', 'no-referrer'); head.prepend(policy);
  return new XMLSerializer().serializeToString(doc.documentElement);
}
/** The app page's style nonce, where its policy names one. */
export function appStyleNonce(): string | undefined {
  const nonce = globalThis.document?.querySelector<HTMLMetaElement>('meta[name="portico-style-nonce"]')?.content;
  return nonce && nonce !== '__PORTICO_STYLE_NONCE__' ? nonce : undefined;
}
/** A book's page is a blob: document, so it keeps the app page's style policy, which allows only
 * styles carrying the page's nonce. The book's own stylesheets and style elements, already cleaned
 * above, are given that nonce; a nonce the book wrote itself is never kept. A style attribute cannot
 * carry a nonce, so it moves to `data-portico-style` and the renderer applies it through the style
 * object (`applyBookStyleAttributes`), which the policy allows; only in the last pass, the one the page is
 * made from, since the cleaning above drops data attributes. Without a nonce nothing moves. */
function bookStyles(doc: Document, last: boolean): void {
  const nonce = appStyleNonce();
  for (const node of Array.from(doc.querySelectorAll('*'))) {
    node.removeAttribute('nonce');
    if (!nonce) continue;
    const name = node.localName.toLowerCase();
    if (name === 'style' || name === 'link' && /(?:^|\s)stylesheet(?:\s|$)/i.test(node.getAttribute('rel') ?? '')) node.setAttribute('nonce', nonce);
    const inline = last ? node.getAttribute('style') : null;
    if (inline !== null) {node.removeAttribute('style'); if (inline) node.setAttribute('data-portico-style', inline);}
  }
}
/** Applies the style attributes `secureMarkup` moved aside, once the book's page has loaded. */
export function applyBookStyleAttributes(doc: Document): void {
  for (const node of Array.from(doc.querySelectorAll<HTMLElement>('[data-portico-style]'))) {
    if (node.style) node.style.cssText = node.getAttribute('data-portico-style') ?? '';
    node.removeAttribute('data-portico-style');
  }
}
export function rejectXmlEntities(raw: string): string {
  if (/<!\s*(?:DOCTYPE|ENTITY)\b/i.test(raw)) throw new ReaderError('invalid_book');
  return raw;
}
