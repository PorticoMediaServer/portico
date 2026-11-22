import React from 'react';
import {Platform} from 'react-native';
import type {NativeSearchScreenProps} from '../pages/shared/search/useSearch';
import {SearchPage} from '../pages/touch/search/SearchPage';
import {TVSearchPage} from '../pages/tv/search/SearchPage';

/** Search on every form: the TV page, or the touch page (phones either way up, tablets). */
export function SearchPageFor(props: NativeSearchScreenProps) {
  if (Platform.isTV) return <TVSearchPage {...props} copy={props.tvCopy}/>;
  return <SearchPage {...props}/>;
}
