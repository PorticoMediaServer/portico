// The phone's one profile PIN screen (design/ui-v1 final3-auth-05, frame 14) and the kit number pad it draws.
import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

const t = (key: string) => key;
const theme = {colors: {ink: '#000', text: '#fff', textSecondary: '#ccc', raised: '#111', raisedHigh: '#222', lineStrong: '#333'},
  type: {title: {}, body: {}, captionStrong: {}}, textScale: 1};
const text = (node: any) => [node.props.children].flat().join('');
const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];

async function screen(props: Record<string, unknown> = {}) {
  const host = hooks(), submitted: string[] = [];
  let value = '';
  const {PhoneProfilePIN} = await screenModule(new URL('../src-next/screens/accounts/PhoneProfilePIN.tsx', import.meta.url), {
    react: host.react, 'react-native': {ScrollView: 'ScrollView', Text: 'Text', View: 'View'},
    '../../../../../packages/i18n/src': {defaultI18n: {t}}, '../../kit/artwork': {ProfileFace: 'Face', StandardBackdrop: 'Backdrop'},
    '../../kit/controls': {Button: 'Button', NumberPad: 'Pad'}, '../../kit/layout/TopBar': {TopBar: 'TopBar'},
    '../../kit/theme': {useTheme: () => theme}, './PhonePINEntry': {PhonePINEntry: 'PINEntry'},
  });
  const draw = () => nodes(host.render(() => PhoneProfilePIN({testID: 'pin', profile: {name: 'Alex', face: {initial: 'A'}}, busy: false, rejected: false,
    onBack: () => {}, onForgot: () => {}, ...props, value, onChange: (next: string) => {value = next;}, onSubmit: (pin: string) => {submitted.push(pin);}})));
  return {draw, submitted, value: () => value, find: (type: string) => draw().find(node => node.type === type)};
}

test('the fourth digit on the pad opens the profile once; there is no Continue, and delete takes one digit back', async () => {
  const s = await screen();
  for (const digit of ['1', '2', '3']) s.find('Pad').props.onDigit(digit);
  assert.equal(s.value(), '123'); assert.deepEqual(s.submitted, []);
  s.find('Pad').props.onDelete(); assert.equal(s.value(), '12');
  s.find('Pad').props.onDigit('3'); s.find('Pad').props.onDigit('4');
  assert.deepEqual(s.submitted, ['1234']);
  s.find('Pad').props.onDigit('5');
  assert.deepEqual(s.submitted, ['1234'], 'a fifth press neither grows the PIN nor sends it again');
  assert.equal(s.draw().some(node => node.type === 'Button' && text(node) === 'action.continue'), false);
});

test('after a failed attempt that leaves the digits typed, deleting one and typing it again sends the same PIN again', async () => {
  const s = await screen();
  for (const digit of ['1', '2', '3', '4']) s.find('Pad').props.onDigit(digit);
  assert.deepEqual(s.submitted, ['1234']);
  // The attempt failed (a network refusal) and the screen kept the four digits.
  s.find('Pad').props.onDelete(); s.find('Pad').props.onDigit('4');
  assert.deepEqual(s.submitted, ['1234', '1234'], 'the pad sends it again');
  s.find('PINEntry').props.onChange('123'); s.find('PINEntry').props.onChange('1234');
  assert.deepEqual(s.submitted, ['1234', '1234', '1234'], 'and so does a hardware keyboard');
});

test('four presses quicker than a render still make four digits', async () => {
  const s = await screen();
  const pad = s.find('Pad');
  for (const digit of ['5', '6', '7', '8']) pad.props.onDigit(digit);
  assert.equal(s.value(), '5678'); assert.deepEqual(s.submitted, ['5678']);
});

test('a hardware keyboard or code autofill still types into the field, with the on-screen keyboard kept down', async () => {
  const s = await screen();
  const field = s.find('PINEntry');
  assert.equal(field.props.softKeyboard, false);
  field.props.onChange('98a76');
  assert.equal(s.value(), '9876'); assert.deepEqual(s.submitted, ['9876']);
});

test('the frame\'s words, a centred Forgot PIN, ‹ Profiles, and a pad that rests while the PIN is checked', async () => {
  let back = 0;
  const s = await screen({busy: true, onBack: () => {back++;}});
  const texts = s.draw().filter(node => node.type === 'Text').map(text);
  assert.deepEqual(texts, ['auth.enterProfilePin', 'Alex', 'pin.fourDigit']);
  const forgot = s.find('Button');
  assert.equal(forgot.props.align, 'center'); assert.equal(text(forgot), 'pin.forgot'); assert.equal(forgot.props.disabled, true);
  assert.equal(s.find('Pad').props.disabled, true);
  const bar = s.find('TopBar');
  assert.equal(bar.props.back.label, 'profiles.title'); bar.props.back.onPress(); assert.equal(back, 1);
});

test('the kit number pad: 1 to 9, then a blank, 0 and Delete, as the frame draws it; at rest it takes no presses', async () => {
  const host = hooks(), pressed: string[] = [];
  const {NumberPad} = await screenModule(new URL('../src-next/kit/controls/NumberPad.tsx', import.meta.url), {
    react: host.react, 'react-native': {Text: 'Text', View: 'View'}, 'react-native-safe-area-context': {useSafeAreaInsets: () => ({bottom: 34})},
    '../../../../../packages/i18n/src': {defaultI18n: {t}}, '../Icon': {Icon: 'Icon'}, '../theme': {nativeType: () => ({}), useTheme: () => theme}, './Focusable': {Focusable: 'Key'},
  });
  const keys = (disabled: boolean) => nodes(host.render(() => NumberPad({disabled, onDigit: (d: string) => pressed.push(d), onDelete: () => pressed.push('⌫')}))).filter(node => node.type === 'Key');
  assert.deepEqual(keys(false).map(key => key.props.accessibilityLabel), ['1', '2', '3', '4', '5', '6', '7', '8', '9', '0', 'action.delete']);
  for (const key of keys(false)) key.props.onPress();
  assert.deepEqual(pressed, ['1', '2', '3', '4', '5', '6', '7', '8', '9', '0', '⌫']);
  for (const key of keys(true)) {assert.equal(key.props.disabled, true); key.props.onPress();}
  assert.equal(pressed.length, 11);
});

test('with the screen\'s own pad the field asks for no on-screen keyboard (inputMode none, or React Native ignores showSoftInputOnFocus); elsewhere it keeps the number pad', async () => {
  const host = hooks();
  const {PhonePINEntry} = await screenModule(new URL('../src-next/screens/accounts/PhonePINEntry.tsx', import.meta.url), {
    react: host.react, 'react-native': {StyleSheet: {absoluteFill: {}}, TextInput: 'TextInput', View: 'View'},
    '../../../../../packages/i18n/src': {defaultI18n: {t}}, '../../kit/theme': {useTheme: () => theme},
  });
  const field = (softKeyboard?: boolean) => nodes(host.render(() => PhonePINEntry({value: '', onChange: () => {}, ...(softKeyboard === undefined ? {} : {softKeyboard})}))).find(node => node.type === 'TextInput').props;
  assert.deepEqual([field(false).inputMode, field(false).showSoftInputOnFocus], ['none', false]);
  assert.deepEqual([field().inputMode, field().showSoftInputOnFocus], ['numeric', true]);
  assert.equal(field(false).autoComplete, 'one-time-code', 'code autofill still reaches it');
});

test('the shared four-box field sends a refused PIN again once a digit is retyped, and never twice on its own', async () => {
  const host = hooks(), completed: string[] = [];
  const {PhonePINEntry} = await screenModule(new URL('../src-next/screens/accounts/PhonePINEntry.tsx', import.meta.url), {
    react: host.react, 'react-native': {StyleSheet: {absoluteFill: {}}, TextInput: 'TextInput', View: 'View'},
    '../../../../../packages/i18n/src': {defaultI18n: {t}}, '../../kit/theme': {useTheme: () => theme},
  });
  const field = () => nodes(host.render(() => PhonePINEntry({value: '', onChange: () => {}, onComplete: (pin: string) => completed.push(pin)}))).find(node => node.type === 'TextInput').props;
  field().onChangeText('9876'); field().onChangeText('9876');
  assert.deepEqual(completed, ['9876'], 'four digits are sent once');
  field().onChangeText('987'); field().onChangeText('9876');
  assert.deepEqual(completed, ['9876', '9876']);
});
