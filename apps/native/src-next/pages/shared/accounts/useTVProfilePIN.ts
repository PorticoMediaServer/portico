import {useEffect, useRef} from 'react';
import {useTVKeyEvent} from '../../../platform/tv-keys';
import {useTVPlayPauseOwner} from '../../../platform/tv-play-pause-owner';
import type {TVProfilePINFormProps} from '../../tv/accounts/ProfilePINPage';

export function useTVProfilePIN(props: TVProfilePINFormProps) {
  const id = `tv.auth.profile.${JSON.stringify(props.profile.id)}.pin`, submitted = useRef<string | undefined>(undefined);
  useEffect(() => {if (!props.code) submitted.current = undefined;}, [props.code]);
  const remove = () => {if (!props.busy) {submitted.current = undefined; props.onCodeChange(props.code.slice(0, -1));}};
  // F-PROFILE-30: Play/Pause deletes a digit here, so playback never takes it.
  const ownsPlayPause = useTVPlayPauseOwner('overlay');
  useTVKeyEvent(event => {if (event.key === 'playPause' && event.phase === 'press' && ownsPlayPause()) remove();});
  const digit = (value: string) => {
    if (props.busy || !/^\d$/.test(value)) return;
    // A refused PIN is still in the boxes for 400 ms: a digit pressed then starts the next PIN at once (never lost).
    if (props.error && props.code.length >= 4) {submitted.current = undefined; props.onCodeChange(value); return;}
    if (props.code.length >= 4) return;
    const next = props.code + value;
    props.onCodeChange(next);
    if (next.length === 4 && submitted.current !== next) {submitted.current = next; props.onSubmit(next);}
  };
  return {id, digit, remove};
}
