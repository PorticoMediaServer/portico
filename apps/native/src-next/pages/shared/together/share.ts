import {Share} from 'react-native';

/** The system share sheet with one line of text (a group's invite code). */
export const shareText = (message: string) => Share.share({message});
