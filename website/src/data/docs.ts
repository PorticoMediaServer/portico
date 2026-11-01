export type DocCategory = {
  id: string;
  title: string;
  description: string;
  order: number;
};

export const docCategories: DocCategory[] = [
  { id: 'start', title: 'Start here', description: 'What Portico is, and how to get a server ready for its first library.', order: 1 },
  { id: 'install', title: 'Install Portico Server', description: 'Choose a server computer, install Portico and finish first-run setup.', order: 2 },
  { id: 'media', title: 'Organize and add media', description: 'Set up folders, create libraries, scan files and fix matches.', order: 3 },
  { id: 'watch', title: 'Watch with Portico', description: 'Browse, search, play, track progress, and pick audio and subtitles.', order: 4 },
  { id: 'remote', title: 'Remote access', description: 'Connect securely to a Portico Server from outside its local network.', order: 5 },
  { id: 'people', title: 'People and access', description: 'Create accounts, give people access to libraries and set permissions.', order: 6 },
  { id: 'live-tv', title: 'Live TV and DVR', description: 'Set up channels and guide data, and manage recordings, schedules and conflicts.', order: 7 },
  { id: 'administration', title: 'Server administration', description: 'Run, monitor, update, back up and maintain a Portico Server.', order: 8 },
  { id: 'clients', title: 'Apps and devices', description: 'Use Portico on the web, phones, tablets, TVs and streaming devices.', order: 9 },
  { id: 'troubleshooting', title: 'Troubleshooting', description: 'Fix connection, library, playback, sign-in and recording problems.', order: 10 },
  { id: 'security', title: 'Security and privacy', description: 'How sign-in, direct connections and data storage work, and how to report a security issue.', order: 11 },
  { id: 'developers', title: 'Developers', description: 'The server API, client architecture, security requirements and how to report issues.', order: 12 }
];

export function getDocCategory(id: string) {
  return docCategories.find((category) => category.id === id);
}
