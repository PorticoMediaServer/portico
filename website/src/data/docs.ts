export type DocCategory = {
  id: string;
  title: string;
  description: string;
  order: number;
};

export const docCategories: DocCategory[] = [
  { id: 'start', title: 'Start here', description: 'Understand Portico and get a server ready for its first library.', order: 1 },
  { id: 'install', title: 'Install Portico Server', description: 'Choose a server, install Portico, and complete first-run setup.', order: 2 },
  { id: 'media', title: 'Organize and add media', description: 'Prepare folders, create libraries, scan files, and correct matches.', order: 3 },
  { id: 'watch', title: 'Watch with Portico', description: 'Browse, search, play, manage progress, and choose audio or subtitles.', order: 4 },
  { id: 'remote', title: 'Remote access', description: 'Connect securely to a Portico Server from outside its local network.', order: 5 },
  { id: 'people', title: 'People and access', description: 'Create accounts, assign library access, and manage household permissions.', order: 6 },
  { id: 'live-tv', title: 'Live TV and DVR', description: 'Configure channels, guide data, recordings, schedules, and conflicts.', order: 7 },
  { id: 'administration', title: 'Server administration', description: 'Run, monitor, update, back up, and maintain a Portico Server.', order: 8 },
  { id: 'clients', title: 'Apps and devices', description: 'Connect Portico on the web, phones, tablets, televisions, and streaming devices.', order: 9 },
  { id: 'troubleshooting', title: 'Troubleshooting', description: 'Diagnose connection, library, playback, account, and recording problems.', order: 10 },
  { id: 'security', title: 'Security and privacy', description: 'Review authentication, direct connections, data ownership, and reporting.', order: 11 },
  { id: 'developers', title: 'Developers', description: 'Work with Portico contracts, clients, integrations, and contribution standards.', order: 12 }
];

export function getDocCategory(id: string) {
  return docCategories.find((category) => category.id === id);
}
