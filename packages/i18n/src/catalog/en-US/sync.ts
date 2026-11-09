/** Changes made while the server is out of reach, kept on the device and sent later. */
export const syncMessages = {
 'sync.waiting':'{count, plural, one {# change waiting to sync} other {# changes waiting to sync}}',
 'sync.kept':'Saved on this device. It will sync when your server can be reached.',
 'sync.notSaved.personal':'A change you made earlier to what you have watched, rated or saved could not be saved on your server.',
 'sync.notSaved.list':'A change you made earlier to a list or playlist could not be saved on your server.',
} as const;
