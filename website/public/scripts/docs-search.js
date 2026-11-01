const dialog = document.querySelector('[data-docs-search]');
const input = dialog?.querySelector('[data-docs-search-input]');
const results = dialog?.querySelector('[data-docs-search-results]');
let index = null;
let selected = 0;

const escapeHtml = (value) => value.replace(/[&<>"']/g, (char) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[char]);

const loadIndex = async () => {
  if (index) return index;
  const response = await fetch('/docs/search.json');
  index = await response.json();
  return index;
};

const snippet = (text, terms) => {
  const lower = text.toLowerCase();
  const at = terms.map((term) => lower.indexOf(term)).filter((position) => position >= 0).sort((a, b) => a - b)[0];
  if (at === undefined) return '';
  let start = Math.max(0, at - 60);
  if (start > 0) start = text.indexOf(' ', start) + 1 || start;
  let end = Math.min(text.length, at + 110);
  const space = text.lastIndexOf(' ', end);
  if (end < text.length && space > at) end = space;
  let excerpt = (start > 0 ? '…' : '') + text.slice(start, end) + (end < text.length ? '…' : '');
  excerpt = escapeHtml(excerpt);
  for (const term of terms) {
    excerpt = excerpt.replace(new RegExp(`(${term.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')})`, 'gi'), '<mark>$1</mark>');
  }
  return excerpt;
};

const score = (doc, terms) => {
  let total = 0;
  const title = doc.title.toLowerCase();
  const description = doc.description.toLowerCase();
  const headings = doc.headings.join(' ').toLowerCase();
  const text = doc.text.toLowerCase();
  for (const term of terms) {
    let termScore = 0;
    if (title.includes(term)) termScore += 12;
    if (headings.includes(term)) termScore += 6;
    if (description.includes(term)) termScore += 4;
    if (text.includes(term)) termScore += 1 + Math.min(4, text.split(term).length - 2);
    if (termScore === 0) return 0;
    total += termScore;
  }
  return total;
};

const render = async () => {
  const docs = await loadIndex();
  const query = input.value.trim().toLowerCase();
  selected = 0;
  if (!query) {
    input.removeAttribute('aria-activedescendant');
    results.innerHTML = '<li class="docs-search-empty" role="presentation">Try "remote access", "subtitles" or "backup".</li>';
    return;
  }
  const terms = query.split(/\s+/).filter(Boolean);
  const matches = docs
    .map((doc) => ({ doc, score: score(doc, terms) }))
    .filter((match) => match.score > 0)
    .sort((a, b) => b.score - a.score)
    .slice(0, 12);
  if (matches.length === 0) {
    input.removeAttribute('aria-activedescendant');
    results.innerHTML = `<li class="docs-search-empty" role="presentation">Nothing matches "${escapeHtml(input.value.trim())}".</li>`;
    return;
  }
  results.innerHTML = matches
    .map(({ doc }, position) => `<li role="presentation"><a id="docs-search-option-${position}" href="${doc.url}" role="option" aria-selected="${position === 0}"><small>${escapeHtml(doc.category)}</small><strong>${escapeHtml(doc.title)}</strong><span>${snippet(doc.text, terms) || escapeHtml(doc.description)}</span></a></li>`)
    .join('');
  input.setAttribute('aria-activedescendant', 'docs-search-option-0');
};

const open = () => {
  if (!dialog || dialog.open) return;
  dialog.showModal();
  input.select();
  render();
};

document.querySelectorAll('[data-docs-search-open]').forEach((button) => button.addEventListener('click', open));

document.addEventListener('keydown', (event) => {
  const typing = /^(INPUT|TEXTAREA|SELECT)$/.test(document.activeElement?.tagName ?? '');
  if ((event.key === 'k' && (event.metaKey || event.ctrlKey)) || (event.key === '/' && !typing)) {
    event.preventDefault();
    open();
  }
});

input?.addEventListener('input', render);

input?.addEventListener('keydown', (event) => {
  const links = [...results.querySelectorAll('a')];
  if (links.length === 0) {
    if (event.key === 'Enter') event.preventDefault();
    return;
  }
  if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
    event.preventDefault();
    selected = (selected + (event.key === 'ArrowDown' ? 1 : -1) + links.length) % links.length;
    links.forEach((link, position) => link.setAttribute('aria-selected', String(position === selected)));
    links[selected].scrollIntoView({ block: 'nearest' });
    input.setAttribute('aria-activedescendant', links[selected].id);
  } else if (event.key === 'Enter') {
    event.preventDefault();
    window.location.href = links[selected].href;
  }
});

dialog?.addEventListener('click', (event) => {
  if (event.target === dialog) dialog.close();
});
