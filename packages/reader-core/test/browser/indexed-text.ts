import {indexedNormalize, indexedRawText} from '../../src/epub/indexed-text.ts';

const signal = () => new AbortController().signal;
const parse = (body: string) => new DOMParser().parseFromString(`<html xmlns="http://www.w3.org/1999/xhtml"><body>${body}</body></html>`, 'application/xhtml+xml');
const raw = (body: string) => indexedRawText(parse(body), signal(), Date.now() + 1000);
const normalized = (body: string, starts: readonly number[] = [0], ends: readonly number[] = []) => {
  const value = raw(body);
  if (!value) return null;
  return indexedNormalize(value.raw, starts, ends.length ? ends : [value.raw.length], signal(), Date.now() + 1000);
};
const cases: {name: string; ok: boolean; fact: unknown}[] = [];
const check = (name: string, ok: boolean, fact: unknown) => cases.push({name, ok, fact});
const text = (body: string) => normalized(body)?.text ?? null;

check('leading block space trims', text('<p>abc</p>') === 'abc', text('<p>abc</p>'));
check('block siblings separate', text('<p>a</p><p>b</p>') === 'a b', text('<p>a</p><p>b</p>'));
check('inline siblings concatenate', text('<p><span>a</span><span>b</span></p>') === 'ab', text('<p><span>a</span><span>b</span></p>'));
check('CDATA is text', text('<p><![CDATA[ab]]></p>') === 'ab', text('<p><![CDATA[ab]]></p>'));
check('textBlock is case-sensitive', text('<P>a</P><P>b</P>') === 'ab' && text('<p>a</p><p>b</p>') === 'a b', [text('<P>a</P><P>b</P>'), text('<p>a</p><p>b</p>')]);
check('script/style/template are skipped', text('<p>a<script>x</script>b<style>y</style><template>z</template>c</p>') === 'abc', text('<p>a<script>x</script>b<style>y</style><template>z</template>c</p>'));
check('foreign namespace declines', raw('<p xmlns:foo="urn:x"><foo:x>a</foo:x>b</p>') === null, null);
const doctype = new DOMParser().parseFromString('<!DOCTYPE html><html xmlns="http://www.w3.org/1999/xhtml"><body><p>a</p></body></html>', 'application/xhtml+xml');
check('doctype declines', indexedRawText(doctype, signal(), Date.now() + 1000) === null, doctype.doctype?.name ?? null);
const supplementary = normalized('<p>😀x</p>', [0], [2]);
check('supplementary endpoints', supplementary?.text === '😀x' && supplementary.startRaw.get(0) === 1 && supplementary.endRaw.get(2) === 3, supplementary ? {text: supplementary.text, start: supplementary.startRaw.get(0), end: supplementary.endRaw.get(2)} : null);

const failed = cases.filter(entry => !entry.ok);
const result = document.querySelector('#result')!;
result.textContent = JSON.stringify({status: failed.length ? 'fail' : 'pass', failed, cases}, null, 2);
(window as unknown as {indexedTextResults: unknown}).indexedTextResults = {status: failed.length ? 'fail' : 'pass', cases};
