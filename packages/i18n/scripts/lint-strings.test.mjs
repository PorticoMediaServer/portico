import test from 'node:test';
import assert from 'node:assert/strict';
import {countLiterals} from './lint-strings.mjs';

const texts = (source, options) => countLiterals(source, options).hits.map(hit => hit.text);

test('date formatter machine options do not become visible strings through a local display helper', () => {
  const source = `
    const dayName = day => i18n.formatDate(new Date(day), {weekday: 'long', timeZone: 'UTC'});
    const view = <Text>{dayName(0)}{new Intl.DateTimeFormat(locale, {timeZone: 'America/Halifax'}).format(now)}</Text>;
  `;
  assert.deepEqual(texts(source), []);
});

test('date-option handling preserves visible values, named copy properties and ordinary interpolations', () => {
  const source = `
    const view = <Text>{'UTC'}{i18n.t('next.test', {timeZone: 'Visible interpolation'})}
      {i18n.formatDate(now, {label: 'Visible named copy'})}
      {formatMessage('next.test', {time: '4:02 PM'})}
    </Text>;
  `;
  assert.deepEqual(texts(source), ['UTC', 'Visible interpolation', 'Visible named copy', '4:02 PM']);
});

test('TS generics, intersections and comparisons never become JSX text', () => {
  const source = `
    const scheduler = useMemo(() => new ArtworkScheduler<ArtworkImage>({load}), []);
    const owner = useMemo(() => new PlaybackMediaOwner<HTMLVideoElement>(), []);
    type Skeleton = Readonly<Props> & Pick<Layout, 'size'>;
    const accept = <Value extends object,>(value: Value): Value => value;
    const valid = count > 0 || preview?.blocked.length || acrossLibraries ? ready : blocked;
    const later = count < limit && count > first;
    const view = <Dialog>{count > 0 || preview?.blocked.length || acrossLibraries ? allowed : denied}</Dialog>;
  `;
  assert.deepEqual(texts(source), []);
});

test('actual JSX text still counts code-looking words and punctuation', () => {
  assert.deepEqual(texts(`const view = <><p>new ArtworkScheduler</p><p>new PlaybackMediaOwner</p><p>Pick a library</p><p>Ready (2); continue?</p><p>Save &amp; continue</p></>;`),
    ['new ArtworkScheduler', 'new PlaybackMediaOwner', 'Pick a library', 'Ready (2); continue?', 'Save &amp; continue']); // Diagnostics retain JSX source spelling.
});

test('copy props, accessibility names, object values and assignment positions remain checked once', () => {
  const source = `
    const view = <Button title="Open details" aria-label={'Play movie'} accessibilityHint={"Starts playback"} label={ready ? 'Play now' : 'Try again'} />;
    const options = {title: 'Actual title', detail: '4:10 PM', action: ready ? 'Save changes' : 'Try again'};
    const message = 'Something happened';
    target.caption = 'Episode one';
    class Panel { help = 'Choose a library'; }
  `;
  assert.deepEqual(texts(source), ['Open details', 'Play movie', 'Starts playback', 'Play now', 'Try again', 'Actual title', '4:10 PM', 'Save changes', 'Try again', 'Something happened', 'Episode one', 'Choose a library']);
});

test('nested JSX expressions, multiline state objects and template substitutions stay visible', () => {
  const source = [
    'const view = <Panel',
    '  title={ready ? `Ready for ${name}` : "Try again"}',
    '  body={ready ? {message: "Saved successfully"} : {message: "Could not save"}}',
    '>',
    '  Choose a library',
    '  {ready ? <b>Play now</b> : render({label: "Try again"})}',
    '  {`Playing ${ready ? "Movie title" : "Another title"}`}',
    '</Panel>;',
  ].join('\n');
  const result = countLiterals(source);
  assert.deepEqual(result.hits, [
    {line: 2, text: 'Ready for ${x}'}, {line: 2, text: 'Try again'},
    {line: 3, text: 'Saved successfully'}, {line: 3, text: 'Could not save'},
    {line: 5, text: 'Choose a library'}, {line: 6, text: 'Play now'}, {line: 6, text: 'Try again'},
    {line: 7, text: 'Playing ${x}'}, {line: 7, text: 'Movie title'}, {line: 7, text: 'Another title'},
  ]);
});

test('escaped quotes and operator characters inside real copy are not code exclusions', () => {
  const source = String.raw`const view = <Panel title={'Owner\'s library'} body={"Say \"Play now\""}><p>{ready ? 'Try again && continue' : 'Ready === yes'}</p></Panel>;`;
  assert.deepEqual(texts(source), ["Owner's library", 'Say "Play now"', 'Try again && continue', 'Ready === yes']);
});

test('comments, type literals, identifiers and catalog keys remain inert', () => {
  const source = `
    // title: 'Comment only'
    /* <p>Comment only</p> */
    type View = {title: 'Type only'; label: string};
    const source = "<p>String containing markup</p>";
    const view = <Panel title={t('next.dock.nowPlaying')} aria-label={identity.title} />;
    const options = {title: 'saved.tab.favorites', help: '/v1/items'};
  `;
  assert.deepEqual(texts(source), []);
  assert.equal(countLiterals("const options = {title: 'Actual title'};", {jsx: false}).count, 1);
  assert.deepEqual(texts("namespace Screen { export const View = () => <p>Actual copy</p>; }"), ['Actual copy']);
});

test('the existing independent snippet contract keeps its substantive count', () => {
  const source = [
    '<Button variant="ghost" label="Try again" onClick={retry} />',
    '<Text>Nothing here yet</Text>',
    "{id: 'a', label: 'Add to Watchlist'}",
    "<Button label={i18n.t('action.refresh')} />",
    '<Text>{count > 1 ? a : b}</Text>',
    '<Text>Deliberate</Text> // lint-strings-allow',
  ].join('\n');
  assert.equal(countLiterals(source).count, 3);
});

test('malformed multiline source fails closed instead of losing checked positions', () => {
  assert.throws(() => countLiterals('const view = <Panel\n title="Actual title"'), /cannot parse source/);
});

test('named copy positions check their values without promoting DOM selectors or lookup predicates', () => {
  const source = `
    const subtitle = card.querySelector('.on-art small')!, detail = card.querySelector(':scope > .s');
    const model = {title: card.querySelector('.on-art b')!.textContent!};
    const title = (JSON.parse(manifestJson) as {tv: {title: string}[]}).tv.find(item => item.title.startsWith('The Last'))!.title;
    model.caption = card.querySelector('.on-art small')!.textContent!;
    const message = readInput('Developer input only');
    const visible = {title: 'Actual title', subtitle: 'Actual subtitle', detail: 'Actual detail'};
    model.caption = 'Actual caption';
  `;
  assert.deepEqual(texts(source), ['Actual title', 'Actual subtitle', 'Actual detail', 'Actual caption']);
});

test('JSX callback returns stay checked while local declarations and thrown diagnostics stay inputs', () => {
  const source = `
    const view = <>{parseMessage(enUS[messageId]).map((node, index) => {
      const scratch = 'Not returned from this callback';
      const subtitle = card.querySelector('.on-art small')!, detail = card.querySelector(':scope > .s');
      const model = {title: card.querySelector('.on-art b')!.textContent!};
      const title = manifest.tv.find(item => item.title.startsWith('The Last'))!.title;
      if (node.kind === 'text') return <Fragment key={index}>{node.value}</Fragment>;
      if (node.kind === 'arg') return emphasis.includes(node.name) ? <strong>{values[node.name]}</strong> : <Fragment>{values[node.name]}</Fragment>;
      throw new TypeError('State sentences require text and named arguments.');
    })}</>;
    const visible = <>{items.map(item => {
      const ignored = 'This local is never rendered';
      const helper = () => 'This helper is never returned';
      if (item.blocked) throw new Error('This diagnostic is never rendered');
      const message = 'Named message remains checked';
      if (item.ready) return 'Play now';
      return item.retry ? 'Try again' : \`Waiting for \${item.name}\`;
    })}</>;
  `;
  assert.deepEqual(texts(source), ['Named message remains checked', 'Play now', 'Try again', 'Waiting for ${x}']);
});

test('render helpers retain JSX, expression callbacks and block returns through branches', () => {
  const source = `
    function renderLabel() { return <b>Helper visible text</b>; }
    const view = <Panel title={items.map(item => item.ready ? 'Ready now' : 'Try again')}>
      {(() => {
        if (ready) return 'Direct helper return';
        switch (state) {
          case 'Not displayed case': return 'Switch return';
          default: try { return 'Try return'; } catch (error) { throw new Error('Developer diagnostic'); }
        }
      })()}
      {items.map(item => <span>{item.ready ? 'Nested JSX return' : 'Other JSX return'}</span>)}
    </Panel>;
  `;
  assert.deepEqual(texts(source), ['Helper visible text', 'Ready now', 'Try again', 'Direct helper return', 'Switch return', 'Try return', 'Nested JSX return', 'Other JSX return']);
});

test('structured helper inputs and catalogue interpolations keep their checked copy', () => {
  const source = [
    'const view = <Panel title={formatMessage("next.test", {time: "4:02 PM"})}',
    '  body={render({value: ready ? "Visible helper value" : "Other helper value", label: "Explicit label"})}>',
    '  {formatMessage("next.test", {time: "8:45 PM"})}',
    '  {["Visible array value"].map(value => <b>{value}</b>)}',
    '</Panel>;',
  ].join('\n');
  assert.deepEqual(countLiterals(source).hits, [
    {line: 1, text: '4:02 PM'}, {line: 2, text: 'Visible helper value'}, {line: 2, text: 'Other helper value'}, {line: 2, text: 'Explicit label'},
    {line: 3, text: '8:45 PM'}, {line: 4, text: 'Visible array value'},
  ]);
});

test('visible locals and local helper results follow lexical values without promoting unused scalar inputs', () => {
  const source = `
    function passthrough(value: string, input: string) { const result = value; return result; }
    const statusText = passthrough('Visible forwarded copy', 'Unused scalar helper input');
    function renderText() {
      const result = ready ? 'Visible helper result' : 'Visible helper alternative';
      return result;
    }
    function unusedHelper() { return 'Unused helper result'; }
    const view = <>
      <p>{statusText}</p><p>{renderText()}</p>
      {items.map(item => {
        const statusText = 'Visible callback local';
        const scratch = 'Unused callback local';
        return statusText;
      })}
      {(() => { let result = 'Initial local value'; result = 'Assigned local value'; return result; })()}
    </>;
  `;
  assert.deepEqual(texts(source), ['Visible forwarded copy', 'Visible helper result', 'Visible helper alternative', 'Visible callback local', 'Initial local value', 'Assigned local value']);
});

test('helper binding traversal terminates on recursive values and keeps later visible siblings', () => {
  const source = `
    function recursive() { return ready ? recursive() : 'Visible recursive fallback'; }
    const cycle = cycle;
    const view = <p>{recursive()}{cycle}{'Visible sibling'}</p>;
  `;
  assert.deepEqual(texts(source), ['Visible recursive fallback', 'Visible sibling']);
});

test('predicate inputs and discarded expressions do not hide conditional, direct or template output', () => {
  const source = [
    'const view = <Panel title={"Not a displayed predicate" === state ? "Displayed title" : "Alternate title"}>',
    '  {lookup("Not a displayed input") ? "Visible success" : "Visible failure"}',
    '  {ready && "Visible logical return"}',
    '  {"Truthy condition only" && "Visible right side"}',
    '  {fallback || "Visible fallback"}',
    '  {("Discarded expression only", "Visible sequence return")}',
    '  {`Visible template ${ready ? "Visible nested value" : "Other nested value"}`}',
    '</Panel>;',
  ].join('\n');
  assert.deepEqual(texts(source), ['Displayed title', 'Alternate title', 'Visible success', 'Visible failure', 'Visible logical return', 'Visible right side', 'Visible fallback', 'Visible sequence return', 'Visible template ${x}', 'Visible nested value', 'Other nested value']);
});
