import React, { useEffect, useRef, useState } from 'react';
import {
  ActivityIndicator,
  Pressable,
  SafeAreaView,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from 'react-native';
import { presentError } from '../../../packages/client-core/src/presentation/errors';
import type { ManagedProfile } from '../../../packages/client-core/src/profile-management';
import type { ServerPin } from '../../../packages/client-core/src/route-identity';
import { NativeSignInSetup } from './native-session';

type Stage = 'address' | 'confirm' | 'credentials' | 'profiles' | 'complete';

/** Separately labelled setup UI only. Never exported by the product App or
 * followed by Home: stop this process before launching the pinned normal entry. */
export function SignInOnly() {
  const owner = useRef<NativeSignInSetup | undefined>(undefined);
  const [ready, setReady] = useState(false),
    [busy, setBusy] = useState(false),
    [stage, setStage] = useState<Stage>('address');
  const [address, setAddress] = useState(''),
    [username, setUsername] = useState(''),
    [password, setPassword] = useState(''),
    [pin, setPin] = useState('');
  const [identity, setIdentity] = useState<ServerPin>(),
    [profiles, setProfiles] = useState<readonly ManagedProfile[]>([]),
    [profile, setProfile] = useState<ManagedProfile>();
  const [error, setError] = useState('');
  useEffect(() => {
    const setup = new NativeSignInSetup();
    owner.current = setup;
    setup.prepare().then(
      () => {
        if (owner.current === setup) setReady(true);
      },
      failure => {
        if (owner.current === setup)
          setError(presentError(failure, 'sign-in').body);
      },
    );
    return () => {
      owner.current = undefined;
      setup.dispose();
    };
  }, []);
  const run = async (work: (setup: NativeSignInSetup) => Promise<void>) => {
    const setup = owner.current;
    if (!ready || busy || !setup) return;
    setBusy(true);
    setError('');
    try {
      await work(setup);
    } catch (failure) {
      if (owner.current === setup)
        setError(presentError(failure, 'sign-in').body);
    } finally {
      if (owner.current === setup) setBusy(false);
    }
  };
  const complete = () => {
    setAddress('');
    setUsername('');
    setPassword('');
    setPin('');
    setProfiles([]);
    setProfile(undefined);
    setIdentity(undefined);
    setStage('complete');
  };
  const choose = (value: ManagedProfile, code?: string) => {
    run(async setup => {
      await setup.choose(value.id, code);
      if (owner.current === setup) complete();
    });
  };
  const button = (label: string, action: () => void, disabled = false) => (
    <Pressable
      accessibilityRole="button"
      accessibilityLabel={label}
      disabled={!ready || busy || disabled}
      onPress={action}
      style={({ pressed }) => [
        styles.button,
        { opacity: !ready || busy || disabled ? 0.45 : pressed ? 0.8 : 1 },
      ]}
    >
      <Text style={styles.buttonText}>{label}</Text>
    </Pressable>
  );
  const field = (
    label: string,
    value: string,
    change: (value: string) => void,
    secret = false,
  ) => (
    <View style={styles.field}>
      <Text style={styles.label}>{label}</Text>
      <TextInput
        accessibilityLabel={label}
        value={value}
        onChangeText={change}
        editable={ready && !busy}
        secureTextEntry={secret}
        autoCapitalize="none"
        autoCorrect={false}
        style={styles.input}
      />
    </View>
  );
  return (
    <SafeAreaView style={styles.page}>
      <ScrollView
        contentContainerStyle={styles.content}
        keyboardShouldPersistTaps="handled"
      >
        <View style={styles.card}>
          <Text style={styles.eyebrow}>SIGN-IN SETUP ONLY</Text>
          <Text style={styles.title}>Sign in to your server</Text>
          {stage === 'address' && (
            <>
              {field('Server address', address, setAddress)}
              {button(
                'Verify server',
                () => {
                  run(async setup => {
                    const found = await setup.inspect(address);
                    if (owner.current === setup) {
                      setIdentity(found);
                      setStage('confirm');
                    }
                  });
                },
                !address.trim(),
              )}
            </>
          )}
          {stage === 'confirm' && identity && (
            <>
              <Text style={styles.body}>
                Confirm this server before entering your credentials.
              </Text>
              <Text style={styles.body}>{address}</Text>
              <Text
                selectable
                accessibilityLabel="Server fingerprint"
                style={styles.fingerprint}
              >
                {identity.fingerprint}
              </Text>
              {button('Trust this server', () => {
                run(async setup => {
                  await setup.confirm();
                  if (owner.current === setup) setStage('credentials');
                });
              })}
            </>
          )}
          {stage === 'credentials' && (
            <>
              {field('Username', username, setUsername)}
              {field('Password', password, setPassword, true)}
              {button(
                'Sign in',
                () => {
                  const submitted = password;
                  setPassword('');
                  run(async setup => {
                    const values = await setup.signIn(username, submitted);
                    if (owner.current !== setup) return;
                    if (values) {
                      setProfiles(values);
                      setStage('profiles');
                    } else complete();
                  });
                },
                !username || !password,
              )}
            </>
          )}
          {stage === 'profiles' && (
            <>
              <Text style={styles.body}>Choose your profile</Text>
              {profiles.map(value => (
                <View key={value.id}>
                  {button(value.name, () => {
                    setProfile(value);
                    setPin('');
                    if (!value.pinRequired) choose(value);
                  })}
                </View>
              ))}
              {profile?.pinRequired && (
                <>
                  {field('Profile PIN', pin, setPin, true)}
                  {button(
                    'Continue with profile',
                    () => {
                      const submitted = pin;
                      setPin('');
                      choose(profile, submitted);
                    },
                    !/^\d{4}$/.test(pin),
                  )}
                </>
              )}
            </>
          )}
          {stage === 'complete' && (
            <Text
              accessibilityLabel="Sign-in setup complete"
              style={styles.body}
            >
              Sign-in saved. Close setup before opening the normal app.
            </Text>
          )}
          {busy || (!ready && !error) ? (
            <ActivityIndicator
              accessibilityLabel="Signing in"
              color="#72d5ff"
            />
          ) : null}
          {error ? (
            <Text accessibilityRole="alert" style={styles.error}>
              {error}
            </Text>
          ) : null}
        </View>
      </ScrollView>
    </SafeAreaView>
  );
}
const styles = StyleSheet.create({
  page: { flex: 1, backgroundColor: '#071018' },
  content: { flexGrow: 1, justifyContent: 'center', padding: 24 },
  card: { alignSelf: 'center', width: '100%', maxWidth: 520, gap: 20 },
  eyebrow: {
    color: '#72d5ff',
    fontSize: 12,
    fontWeight: '700',
    letterSpacing: 1.2,
  },
  title: { color: '#edf5fa', fontSize: 28, fontWeight: '700' },
  body: { color: '#c4d3dd', fontSize: 16, lineHeight: 24 },
  field: { gap: 8 },
  label: { color: '#edf5fa', fontSize: 15 },
  input: {
    minHeight: 52,
    paddingHorizontal: 14,
    color: '#edf5fa',
    backgroundColor: '#152a38',
    borderRadius: 10,
    borderWidth: 1,
    borderColor: '#365265',
    fontSize: 17,
  },
  button: {
    minHeight: 52,
    paddingHorizontal: 18,
    justifyContent: 'center',
    alignItems: 'center',
    backgroundColor: '#72d5ff',
    borderRadius: 10,
  },
  buttonText: { color: '#071018', fontSize: 17, fontWeight: '700' },
  fingerprint: { color: '#edf5fa', fontSize: 14, lineHeight: 22 },
  error: { color: '#ffb8ab', fontSize: 16, lineHeight: 24 },
});
