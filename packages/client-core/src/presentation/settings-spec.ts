import type {MessageId} from '../../../i18n/src/index.ts';

/** Approved Settings structure. Runtime facts and mutations come from checked bindings. */
export const settingsDesignRevision = '812dacacc70e296ac870ec36d1a37a485eaeb8ed';
export type SettingsPlatform = 'web' | 'phone' | 'tv' | 'desktop';
export type SettingsOperatingSystem = 'android' | 'ios' | 'ipados' | 'tvos' | 'androidtv' | 'firetv' | 'roku' | 'webos' | 'tizen' | 'windows' | 'macos' | 'linux' | 'browser';
export type SettingsValue = string | number | boolean | null | readonly SettingsValue[] | Readonly<{[key: string]: SettingsValue}>;
export type SettingsAction = Readonly<{label: MessageId; style?: 'glass' | 'primary' | 'danger'; confirm?: MessageId; remove?: boolean; panel?: string}>;
export type SettingsOption = Readonly<{value: string | number | boolean; label: MessageId; hint?: MessageId; sets?: Readonly<Record<string, SettingsValue>>; locked?: boolean}>;
export type SettingsControl = Readonly<{
 type: 'toggle' | 'select' | 'segmented' | 'matrix' | 'number' | 'text' | 'folder' | 'action' | 'status' | 'link' | 'list' | 'readonly' | 'preview' | 'table';
 default?: SettingsValue; options?: readonly SettingsOption[]; dynamicOptions?: boolean; label?: MessageId; placeholder?: MessageId;
 style?: string; unit?: MessageId; min?: number; max?: number; step?: number; optional?: boolean; secret?: boolean;
 columns?: readonly Readonly<{id?: string; value?: string; label: MessageId; hint?: MessageId; kind?: string; sort?: boolean; width?: string}>[];
 items?: readonly Readonly<{id?: string; code?: string; label: MessageId; default?: string}>[];
 reorder?: boolean; drag?: boolean; addLabel?: MessageId; empty?: MessageId; itemAction?: SettingsAction; itemActions?: readonly SettingsAction[];
 addPanel?: string; addMenu?: readonly Readonly<{label: MessageId; icon: string; do: string}>[];
 panel?: string; confirm?: MessageId; second?: SettingsAction; to?: string; mono?: boolean; kind?: string; text?: MessageId; uses?: Readonly<Record<string, string>>;
 search?: MessageId; noun?: readonly MessageId[]; filters?: readonly Readonly<{id: string; label: MessageId; options: readonly Readonly<{value: string; label: MessageId}>[]; dynamicOptions?: boolean}>[];
 sort?: Readonly<{by: string; dir: 'asc' | 'desc'}>; select?: Readonly<{bulk: readonly SettingsAction[]}>; rowAction?: SettingsAction; rowActions?: readonly SettingsAction[]; height?: number;
}>;
export type SettingsRow = Readonly<{id: string; label: MessageId; help?: MessageId; control: SettingsControl; children?: readonly SettingsRow[];
 scope?: 'server' | 'account' | 'profile' | 'device'; key?: string; platforms?: readonly SettingsPlatform[]; os?: readonly SettingsOperatingSystem[]; shapes?: readonly string[]; audience?: string;
 enableWhen?: Readonly<{id: string; in?: readonly SettingsValue[]; notIn?: readonly SettingsValue[]}>; noChanged?: boolean}>;
export type SettingsGroup = Readonly<{title?: MessageId; description?: MessageId; platforms?: readonly SettingsPlatform[]; rows: readonly SettingsRow[]}>;
export type SettingsHolder = Readonly<{groups?: readonly SettingsGroup[]; advanced?: Readonly<{groups?: readonly SettingsGroup[]}>}>;
export type SettingsPanel = SettingsHolder & Readonly<{title?: MessageId; newTitle?: MessageId; searchName?: MessageId; tabs?: readonly (SettingsHolder & Readonly<{id: string; title: MessageId}>)[]; footer?: Readonly<{primary: MessageId; secondary?: MessageId}>}>;
export type SettingsSection = SettingsHolder & Readonly<{scope: 'account' | 'server'; id: string; title: MessageId; navGroup: string; icon: string; kind: string; platforms?: readonly SettingsPlatform[]; panels?: Readonly<Record<string, SettingsPanel>>; configOnly: readonly Readonly<{key: string; label: MessageId}>[]}>;

export const settingsSpec: readonly SettingsSection[] = [
  {
    "scope": "account",
    "id": "profile",
    "navGroup": "You",
    "icon": "user",
    "kind": "settings",
    "title": "next.settingsSpec.account.profile.title",
    "groups": [
      {
        "title": "next.settingsSpec.account.profile.groups.0.title",
        "rows": [
          {
            "id": "profile.list",
            "scope": "profile",
            "label": "next.settingsSpec.account.profile.groups.0.rows.0.label",
            "control": {
              "type": "list",
              "addLabel": "next.settingsSpec.account.profile.groups.0.rows.0.control.addLabel",
              "itemAction": {
                "label": "next.settingsSpec.account.profile.groups.0.rows.0.control.itemAction.label",
                "style": "glass"
              }
            }
          }
        ]
      },
      {
        "title": "next.settingsSpec.account.profile.groups.1.title",
        "platforms": [
          "web",
          "phone"
        ],
        "rows": [
          {
            "id": "profile.name",
            "scope": "profile",
            "key": "name",
            "label": "next.settingsSpec.account.profile.groups.1.rows.0.label",
            "control": {
              "type": "text",
              "secret": false,
              "placeholder": "next.settingsSpec.account.profile.groups.1.rows.0.control.placeholder",
              "default": ""
            }
          },
          {
            "id": "profile.color",
            "scope": "profile",
            "key": "art",
            "label": "next.settingsSpec.account.profile.groups.1.rows.1.label",
            "help": "next.settingsSpec.account.profile.groups.1.rows.1.help",
            "control": {
              "type": "select",
              "default": "blue",
              "options": [
                {
                  "value": "blue",
                  "label": "next.settingsSpec.account.profile.groups.1.rows.1.control.options.0.label"
                },
                {
                  "value": "violet",
                  "label": "next.settingsSpec.account.profile.groups.1.rows.1.control.options.1.label"
                },
                {
                  "value": "mint",
                  "label": "next.settingsSpec.account.profile.groups.1.rows.1.control.options.2.label"
                },
                {
                  "value": "coral",
                  "label": "next.settingsSpec.account.profile.groups.1.rows.1.control.options.3.label"
                },
                {
                  "value": "gold",
                  "label": "next.settingsSpec.account.profile.groups.1.rows.1.control.options.4.label"
                },
                {
                  "value": "slate",
                  "label": "next.settingsSpec.account.profile.groups.1.rows.1.control.options.5.label"
                },
                {
                  "value": "rose",
                  "label": "next.settingsSpec.account.profile.groups.1.rows.1.control.options.6.label"
                },
                {
                  "value": "sky",
                  "label": "next.settingsSpec.account.profile.groups.1.rows.1.control.options.7.label"
                }
              ]
            }
          },
          {
            "id": "profile.picture",
            "scope": "profile",
            "label": "next.settingsSpec.account.profile.groups.1.rows.2.label",
            "help": "next.settingsSpec.account.profile.groups.1.rows.2.help",
            "control": {
              "type": "action",
              "style": "glass",
              "label": "next.settingsSpec.account.profile.groups.1.rows.2.control.label",
              "second": {
                "label": "next.settingsSpec.account.profile.groups.1.rows.2.control.second.label",
                "style": "glass"
              }
            }
          },
          {
            "id": "profile.delete",
            "scope": "profile",
            "label": "next.settingsSpec.account.profile.groups.1.rows.3.label",
            "help": "next.settingsSpec.account.profile.groups.1.rows.3.help",
            "control": {
              "type": "action",
              "style": "danger",
              "label": "next.settingsSpec.account.profile.groups.1.rows.3.control.label",
              "confirm": "next.settingsSpec.account.profile.groups.1.rows.3.control.confirm"
            }
          }
        ]
      },
      {
        "title": "next.settingsSpec.account.profile.groups.2.title",
        "platforms": [
          "web",
          "phone"
        ],
        "rows": [
          {
            "id": "profile.pin",
            "scope": "profile",
            "key": "pin",
            "label": "next.settingsSpec.account.profile.groups.2.rows.0.label",
            "help": "next.settingsSpec.account.profile.groups.2.rows.0.help",
            "control": {
              "type": "toggle",
              "default": false
            },
            "children": [
              {
                "id": "profile.pinChange",
                "scope": "profile",
                "enableWhen": {
                  "id": "profile.pin",
                  "in": [
                    true
                  ]
                },
                "label": "next.settingsSpec.account.profile.groups.2.rows.0.children.0.label",
                "control": {
                  "type": "action",
                  "style": "glass",
                  "label": "next.settingsSpec.account.profile.groups.2.rows.0.children.0.control.label"
                }
              },
              {
                "id": "profile.pinForget",
                "scope": "profile",
                "enableWhen": {
                  "id": "profile.pin",
                  "in": [
                    true
                  ]
                },
                "label": "next.settingsSpec.account.profile.groups.2.rows.0.children.1.label",
                "help": "next.settingsSpec.account.profile.groups.2.rows.0.children.1.help",
                "control": {
                  "type": "action",
                  "style": "glass",
                  "label": "next.settingsSpec.account.profile.groups.2.rows.0.children.1.control.label",
                  "confirm": "next.settingsSpec.account.profile.groups.2.rows.0.children.1.control.confirm"
                }
              }
            ]
          },
          {
            "id": "profile.pinForgot",
            "scope": "profile",
            "label": "next.settingsSpec.account.profile.groups.2.rows.1.label",
            "help": "next.settingsSpec.account.profile.groups.2.rows.1.help",
            "control": {
              "type": "action",
              "style": "glass",
              "label": "next.settingsSpec.account.profile.groups.2.rows.1.control.label"
            }
          }
        ]
      },
      {
        "title": "next.settingsSpec.account.profile.groups.3.title",
        "platforms": [
          "web",
          "phone"
        ],
        "rows": [
          {
            "id": "profile.maxRating",
            "scope": "profile",
            "key": "maximumAgeRating",
            "label": "next.settingsSpec.account.profile.groups.3.rows.0.label",
            "help": "next.settingsSpec.account.profile.groups.3.rows.0.help",
            "control": {
              "type": "select",
              "default": "none",
              "options": [
                {
                  "value": "none",
                  "label": "next.settingsSpec.account.profile.groups.3.rows.0.control.options.0.label",
                  "hint": "next.settingsSpec.account.profile.groups.3.rows.0.control.options.0.hint"
                },
                {
                  "value": "G",
                  "label": "next.settingsSpec.account.profile.groups.3.rows.0.control.options.1.label",
                  "hint": "next.settingsSpec.account.profile.groups.3.rows.0.control.options.1.hint"
                },
                {
                  "value": "PG",
                  "label": "next.settingsSpec.account.profile.groups.3.rows.0.control.options.2.label",
                  "hint": "next.settingsSpec.account.profile.groups.3.rows.0.control.options.2.hint"
                },
                {
                  "value": "PG-13",
                  "label": "next.settingsSpec.account.profile.groups.3.rows.0.control.options.3.label",
                  "hint": "next.settingsSpec.account.profile.groups.3.rows.0.control.options.3.hint"
                },
                {
                  "value": "R",
                  "label": "next.settingsSpec.account.profile.groups.3.rows.0.control.options.4.label",
                  "hint": "next.settingsSpec.account.profile.groups.3.rows.0.control.options.4.hint"
                }
              ]
            }
          },
          {
            "id": "profile.allowUnrated",
            "scope": "profile",
            "key": "allowUnrated",
            "label": "next.settingsSpec.account.profile.groups.3.rows.1.label",
            "help": "next.settingsSpec.account.profile.groups.3.rows.1.help",
            "control": {
              "type": "toggle",
              "default": true
            }
          },
          {
            "id": "profile.hiddenLabels",
            "scope": "profile",
            "key": "blockedLabels",
            "label": "next.settingsSpec.account.profile.groups.3.rows.2.label",
            "help": "next.settingsSpec.account.profile.groups.3.rows.2.help",
            "control": {
              "type": "list",
              "addLabel": "next.settingsSpec.account.profile.groups.3.rows.2.control.addLabel",
              "itemAction": {
                "label": "next.settingsSpec.account.profile.groups.3.rows.2.control.itemAction.label",
                "style": "glass",
                "remove": true
              }
            }
          },
          {
            "id": "profile.allowDownloads",
            "scope": "profile",
            "key": "allowDownloads",
            "label": "next.settingsSpec.account.profile.groups.3.rows.3.label",
            "control": {
              "type": "toggle",
              "default": true
            }
          },
          {
            "id": "profile.allowLiveTv",
            "scope": "profile",
            "key": "allowLiveTv",
            "label": "next.settingsSpec.account.profile.groups.3.rows.4.label",
            "control": {
              "type": "toggle",
              "default": true
            }
          },
          {
            "id": "profile.allowRecordings",
            "scope": "profile",
            "key": "allowDvr",
            "label": "next.settingsSpec.account.profile.groups.3.rows.5.label",
            "control": {
              "type": "toggle",
              "default": true
            }
          },
          {
            "id": "profile.allowWatchTogether",
            "scope": "profile",
            "key": "allowWatchTogether",
            "label": "next.settingsSpec.account.profile.groups.3.rows.6.label",
            "control": {
              "type": "toggle",
              "default": true
            }
          }
        ]
      },
      {
        "title": "next.settingsSpec.account.profile.groups.4.title",
        "rows": [
          {
            "id": "profile.username",
            "scope": "account",
            "label": "next.settingsSpec.account.profile.groups.4.rows.0.label",
            "control": {
              "type": "readonly"
            }
          }
        ]
      },
      {
        "title": "next.settingsSpec.account.profile.groups.5.title",
        "platforms": [
          "web",
          "phone"
        ],
        "rows": [
          {
            "id": "profile.email",
            "scope": "account",
            "shapes": [
              "portico-account"
            ],
            "label": "next.settingsSpec.account.profile.groups.5.rows.0.label",
            "control": {
              "type": "action",
              "style": "glass",
              "label": "next.settingsSpec.account.profile.groups.5.rows.0.control.label"
            }
          },
          {
            "id": "profile.password",
            "scope": "account",
            "label": "next.settingsSpec.account.profile.groups.5.rows.1.label",
            "control": {
              "type": "action",
              "style": "glass",
              "label": "next.settingsSpec.account.profile.groups.5.rows.1.control.label"
            }
          },
          {
            "id": "profile.twoStep",
            "scope": "account",
            "label": "next.settingsSpec.account.profile.groups.5.rows.2.label",
            "help": "next.settingsSpec.account.profile.groups.5.rows.2.help",
            "control": {
              "type": "toggle",
              "default": false
            },
            "children": [
              {
                "id": "profile.recoveryCodes",
                "scope": "account",
                "enableWhen": {
                  "id": "profile.twoStep",
                  "in": [
                    true
                  ]
                },
                "label": "next.settingsSpec.account.profile.groups.5.rows.2.children.0.label",
                "help": "next.settingsSpec.account.profile.groups.5.rows.2.children.0.help",
                "control": {
                  "type": "action",
                  "style": "glass",
                  "label": "next.settingsSpec.account.profile.groups.5.rows.2.children.0.control.label",
                  "confirm": "next.settingsSpec.account.profile.groups.5.rows.2.children.0.control.confirm"
                }
              }
            ]
          }
        ]
      },
      {
        "title": "next.settingsSpec.account.profile.groups.6.title",
        "rows": [
          {
            "id": "profile.devices",
            "scope": "account",
            "label": "next.settingsSpec.account.profile.groups.6.rows.0.label",
            "control": {
              "type": "list",
              "itemAction": {
                "label": "next.settingsSpec.account.profile.groups.6.rows.0.control.itemAction.label",
                "style": "glass",
                "confirm": "next.settingsSpec.account.profile.groups.6.rows.0.control.itemAction.confirm",
                "remove": true
              }
            }
          }
        ]
      },
      {
        "title": "next.settingsSpec.account.profile.groups.7.title",
        "rows": [
          {
            "id": "appearance.notificationDot",
            "scope": "profile",
            "key": "notifications.badges",
            "label": "next.settingsSpec.account.profile.groups.7.rows.0.label",
            "help": "next.settingsSpec.account.profile.groups.7.rows.0.help",
            "control": {
              "type": "toggle",
              "default": true
            }
          },
          {
            "id": "profile.noticeServerOffline",
            "scope": "account",
            "key": "serverOffline",
            "shapes": [
              "portico-account"
            ],
            "label": "next.settingsSpec.account.profile.groups.7.rows.1.label",
            "control": {
              "type": "toggle",
              "default": true
            }
          },
          {
            "id": "profile.noticeStorageLow",
            "scope": "account",
            "key": "storageNearlyFull",
            "shapes": [
              "portico-account"
            ],
            "label": "next.settingsSpec.account.profile.groups.7.rows.2.label",
            "control": {
              "type": "toggle",
              "default": true
            }
          },
          {
            "id": "profile.noticeInvitation",
            "scope": "account",
            "key": "invitationAccepted",
            "shapes": [
              "portico-account"
            ],
            "label": "next.settingsSpec.account.profile.groups.7.rows.3.label",
            "control": {
              "type": "toggle",
              "default": true
            }
          }
        ]
      },
      {
        "rows": [
          {
            "id": "profile.signOutElsewhere",
            "scope": "account",
            "label": "next.settingsSpec.account.profile.groups.8.rows.0.label",
            "control": {
              "type": "action",
              "style": "glass",
              "label": "next.settingsSpec.account.profile.groups.8.rows.0.control.label",
              "confirm": "next.settingsSpec.account.profile.groups.8.rows.0.control.confirm"
            }
          },
          {
            "id": "profile.signOut",
            "scope": "device",
            "label": "next.settingsSpec.account.profile.groups.8.rows.1.label",
            "control": {
              "type": "action",
              "style": "danger",
              "label": "next.settingsSpec.account.profile.groups.8.rows.1.control.label",
              "confirm": "next.settingsSpec.account.profile.groups.8.rows.1.control.confirm"
            }
          }
        ]
      }
    ],
    "advanced": {
      "groups": [
        {
          "title": "next.settingsSpec.account.profile.advanced.groups.0.title",
          "platforms": [
            "web",
            "phone"
          ],
          "rows": [
            {
              "id": "profile.providers",
              "scope": "account",
              "shapes": [
                "portico-account"
              ],
              "label": "next.settingsSpec.account.profile.advanced.groups.0.rows.0.label",
              "control": {
                "type": "list",
                "addLabel": "next.settingsSpec.account.profile.advanced.groups.0.rows.0.control.addLabel",
                "itemAction": {
                  "label": "next.settingsSpec.account.profile.advanced.groups.0.rows.0.control.itemAction.label",
                  "style": "glass",
                  "confirm": "next.settingsSpec.account.profile.advanced.groups.0.rows.0.control.itemAction.confirm",
                  "remove": true
                }
              }
            }
          ]
        },
        {
          "title": "next.settingsSpec.account.profile.advanced.groups.1.title",
          "platforms": [
            "web",
            "phone"
          ],
          "rows": [
            {
              "id": "profile.tokens",
              "scope": "account",
              "label": "next.settingsSpec.account.profile.advanced.groups.1.rows.0.label",
              "help": "next.settingsSpec.account.profile.advanced.groups.1.rows.0.help",
              "control": {
                "type": "list",
                "addLabel": "next.settingsSpec.account.profile.advanced.groups.1.rows.0.control.addLabel",
                "itemAction": {
                  "label": "next.settingsSpec.account.profile.advanced.groups.1.rows.0.control.itemAction.label",
                  "style": "danger",
                  "confirm": "next.settingsSpec.account.profile.advanced.groups.1.rows.0.control.itemAction.confirm",
                  "remove": true
                }
              }
            },
            {
              "id": "profile.tokenScope",
              "scope": "account",
              "label": "next.settingsSpec.account.profile.advanced.groups.1.rows.1.label",
              "control": {
                "type": "select",
                "default": "read",
                "options": [
                  {
                    "value": "read",
                    "label": "next.settingsSpec.account.profile.advanced.groups.1.rows.1.control.options.0.label",
                    "hint": "next.settingsSpec.account.profile.advanced.groups.1.rows.1.control.options.0.hint"
                  },
                  {
                    "value": "play",
                    "label": "next.settingsSpec.account.profile.advanced.groups.1.rows.1.control.options.1.label",
                    "hint": "next.settingsSpec.account.profile.advanced.groups.1.rows.1.control.options.1.hint"
                  },
                  {
                    "value": "manage",
                    "label": "next.settingsSpec.account.profile.advanced.groups.1.rows.1.control.options.2.label",
                    "hint": "next.settingsSpec.account.profile.advanced.groups.1.rows.1.control.options.2.hint"
                  },
                  {
                    "value": "all",
                    "label": "next.settingsSpec.account.profile.advanced.groups.1.rows.1.control.options.3.label",
                    "hint": "next.settingsSpec.account.profile.advanced.groups.1.rows.1.control.options.3.hint"
                  }
                ]
              }
            },
            {
              "id": "profile.tokenExpiry",
              "scope": "account",
              "label": "next.settingsSpec.account.profile.advanced.groups.1.rows.2.label",
              "control": {
                "type": "select",
                "default": "never",
                "options": [
                  {
                    "value": "never",
                    "label": "next.settingsSpec.account.profile.advanced.groups.1.rows.2.control.options.0.label"
                  },
                  {
                    "value": "d30",
                    "label": "next.settingsSpec.account.profile.advanced.groups.1.rows.2.control.options.1.label"
                  },
                  {
                    "value": "d90",
                    "label": "next.settingsSpec.account.profile.advanced.groups.1.rows.2.control.options.2.label"
                  },
                  {
                    "value": "d365",
                    "label": "next.settingsSpec.account.profile.advanced.groups.1.rows.2.control.options.3.label"
                  }
                ]
              }
            }
          ]
        },
        {
          "title": "next.settingsSpec.account.profile.advanced.groups.2.title",
          "platforms": [
            "web",
            "phone"
          ],
          "rows": [
            {
              "id": "profile.exportData",
              "scope": "account",
              "shapes": [
                "portico-account"
              ],
              "label": "next.settingsSpec.account.profile.advanced.groups.2.rows.0.label",
              "help": "next.settingsSpec.account.profile.advanced.groups.2.rows.0.help",
              "control": {
                "type": "action",
                "style": "glass",
                "label": "next.settingsSpec.account.profile.advanced.groups.2.rows.0.control.label"
              }
            },
            {
              "id": "profile.deleteAccount",
              "scope": "account",
              "shapes": [
                "portico-account"
              ],
              "label": "next.settingsSpec.account.profile.advanced.groups.2.rows.1.label",
              "help": "next.settingsSpec.account.profile.advanced.groups.2.rows.1.help",
              "control": {
                "type": "action",
                "style": "danger",
                "label": "next.settingsSpec.account.profile.advanced.groups.2.rows.1.control.label",
                "confirm": "next.settingsSpec.account.profile.advanced.groups.2.rows.1.control.confirm"
              }
            }
          ]
        },
        {
          "title": "next.settingsSpec.account.profile.advanced.groups.3.title",
          "rows": [
            {
              "id": "profile.leave",
              "scope": "account",
              "label": "next.settingsSpec.account.profile.advanced.groups.3.rows.0.label",
              "help": "next.settingsSpec.account.profile.advanced.groups.3.rows.0.help",
              "control": {
                "type": "action",
                "style": "danger",
                "label": "next.settingsSpec.account.profile.advanced.groups.3.rows.0.control.label",
                "confirm": "next.settingsSpec.account.profile.advanced.groups.3.rows.0.control.confirm"
              }
            }
          ]
        }
      ]
    },
    "configOnly": []
  },
  {
    "scope": "account",
    "id": "privacy",
    "navGroup": "You",
    "icon": "shield",
    "kind": "settings",
    "title": "next.settingsSpec.account.privacy.title",
    "groups": [
      {
        "rows": [
          {
            "id": "privacy.level",
            "scope": "profile",
            "label": "next.settingsSpec.account.privacy.groups.0.rows.0.label",
            "control": {
              "type": "select",
              "default": "normal",
              "options": [
                {
                  "value": "normal",
                  "label": "next.settingsSpec.account.privacy.groups.0.rows.0.control.options.0.label",
                  "hint": "next.settingsSpec.account.privacy.groups.0.rows.0.control.options.0.hint",
                  "sets": {
                    "privacy.pauseHistory": false,
                    "privacy.showActivity": false,
                    "privacy.includeWatchTogether": true,
                    "privacy.rememberSearches": true
                  }
                },
                {
                  "value": "private",
                  "label": "next.settingsSpec.account.privacy.groups.0.rows.0.control.options.1.label",
                  "hint": "next.settingsSpec.account.privacy.groups.0.rows.0.control.options.1.hint",
                  "sets": {
                    "privacy.pauseHistory": false,
                    "privacy.showActivity": false,
                    "privacy.includeWatchTogether": false,
                    "privacy.rememberSearches": true
                  }
                },
                {
                  "value": "incognito",
                  "label": "next.settingsSpec.account.privacy.groups.0.rows.0.control.options.2.label",
                  "hint": "next.settingsSpec.account.privacy.groups.0.rows.0.control.options.2.hint",
                  "sets": {
                    "privacy.pauseHistory": true,
                    "privacy.showActivity": false,
                    "privacy.includeWatchTogether": false,
                    "privacy.rememberSearches": false
                  }
                },
                {
                  "value": "custom",
                  "label": "next.settingsSpec.account.privacy.groups.0.rows.0.control.options.3.label",
                  "hint": "next.settingsSpec.account.privacy.groups.0.rows.0.control.options.3.hint"
                }
              ]
            }
          }
        ]
      },
      {
        "title": "next.settingsSpec.account.privacy.groups.1.title",
        "rows": [
          {
            "id": "privacy.clearHistory",
            "scope": "profile",
            "label": "next.settingsSpec.account.privacy.groups.1.rows.0.label",
            "help": "next.settingsSpec.account.privacy.groups.1.rows.0.help",
            "control": {
              "type": "action",
              "style": "glass",
              "label": "next.settingsSpec.account.privacy.groups.1.rows.0.control.label",
              "confirm": "next.settingsSpec.account.privacy.groups.1.rows.0.control.confirm"
            }
          },
          {
            "id": "privacy.clearSearches",
            "scope": "profile",
            "label": "next.settingsSpec.account.privacy.groups.1.rows.1.label",
            "control": {
              "type": "action",
              "style": "glass",
              "label": "next.settingsSpec.account.privacy.groups.1.rows.1.control.label"
            }
          },
          {
            "id": "privacy.resetRecommendations",
            "scope": "profile",
            "label": "next.settingsSpec.account.privacy.groups.1.rows.2.label",
            "control": {
              "type": "action",
              "style": "glass",
              "label": "next.settingsSpec.account.privacy.groups.1.rows.2.control.label",
              "confirm": "next.settingsSpec.account.privacy.groups.1.rows.2.control.confirm"
            }
          }
        ]
      }
    ],
    "advanced": {
      "groups": [
        {
          "title": "next.settingsSpec.account.privacy.advanced.groups.0.title",
          "rows": [
            {
              "id": "privacy.showActivity",
              "scope": "profile",
              "key": "privacy.showActivityToMembers",
              "label": "next.settingsSpec.account.privacy.advanced.groups.0.rows.0.label",
              "help": "next.settingsSpec.account.privacy.advanced.groups.0.rows.0.help",
              "control": {
                "type": "toggle",
                "default": false
              }
            },
            {
              "id": "privacy.includeWatchTogether",
              "scope": "profile",
              "key": "privacy.includeInWatchTogether",
              "label": "next.settingsSpec.account.privacy.advanced.groups.0.rows.1.label",
              "help": "next.settingsSpec.account.privacy.advanced.groups.0.rows.1.help",
              "control": {
                "type": "toggle",
                "default": true
              }
            }
          ]
        },
        {
          "title": "next.settingsSpec.account.privacy.advanced.groups.1.title",
          "rows": [
            {
              "id": "privacy.pauseHistory",
              "scope": "profile",
              "key": "privacy.pauseWatchHistory",
              "label": "next.settingsSpec.account.privacy.advanced.groups.1.rows.0.label",
              "help": "next.settingsSpec.account.privacy.advanced.groups.1.rows.0.help",
              "control": {
                "type": "toggle",
                "default": false
              }
            },
            {
              "id": "privacy.rememberSearches",
              "scope": "profile",
              "key": "search.rememberHistory",
              "label": "next.settingsSpec.account.privacy.advanced.groups.1.rows.1.label",
              "control": {
                "type": "toggle",
                "default": true
              }
            }
          ]
        }
      ]
    },
    "configOnly": []
  },
  {
    "scope": "account",
    "id": "playback",
    "navGroup": "Watching and listening",
    "icon": "playc",
    "kind": "settings",
    "title": "next.settingsSpec.account.playback.title",
    "groups": [
      {
        "title": "next.settingsSpec.account.playback.groups.0.title",
        "rows": [
          {
            "id": "playback.autoplayNext",
            "scope": "profile",
            "key": "playback.autoplayNext",
            "label": "next.settingsSpec.account.playback.groups.0.rows.0.label",
            "control": {
              "type": "toggle",
              "default": true
            },
            "children": [
              {
                "id": "playback.upNextCountdownMs",
                "scope": "profile",
                "key": "playback.upNextCountdownMs",
                "enableWhen": {
                  "id": "playback.autoplayNext",
                  "in": [
                    true
                  ]
                },
                "label": "next.settingsSpec.account.playback.groups.0.rows.0.children.0.label",
                "control": {
                  "type": "select",
                  "default": 10000,
                  "options": [
                    {
                      "value": 0,
                      "label": "next.settingsSpec.account.playback.groups.0.rows.0.children.0.control.options.0.label"
                    },
                    {
                      "value": 5000,
                      "label": "next.settingsSpec.account.playback.groups.0.rows.0.children.0.control.options.1.label"
                    },
                    {
                      "value": 10000,
                      "label": "next.settingsSpec.account.playback.groups.0.rows.0.children.0.control.options.2.label"
                    },
                    {
                      "value": 15000,
                      "label": "next.settingsSpec.account.playback.groups.0.rows.0.children.0.control.options.3.label"
                    }
                  ]
                }
              },
              {
                "id": "playback.startNextDuringCredits",
                "scope": "profile",
                "key": "playback.startNextDuringCredits",
                "enableWhen": {
                  "id": "playback.autoplayNext",
                  "in": [
                    true
                  ]
                },
                "label": "next.settingsSpec.account.playback.groups.0.rows.0.children.1.label",
                "help": "next.settingsSpec.account.playback.groups.0.rows.0.children.1.help",
                "control": {
                  "type": "toggle",
                  "default": false
                }
              }
            ]
          },
          {
            "id": "playback.stillWatching",
            "scope": "profile",
            "key": "playback.passoutProtection, playback.passoutAfterEpisodes",
            "label": "next.settingsSpec.account.playback.groups.0.rows.1.label",
            "help": "next.settingsSpec.account.playback.groups.0.rows.1.help",
            "control": {
              "type": "select",
              "default": 3,
              "options": [
                {
                  "value": 0,
                  "label": "next.settingsSpec.account.playback.groups.0.rows.1.control.options.0.label"
                },
                {
                  "value": 2,
                  "label": "next.settingsSpec.account.playback.groups.0.rows.1.control.options.1.label"
                },
                {
                  "value": 3,
                  "label": "next.settingsSpec.account.playback.groups.0.rows.1.control.options.2.label"
                },
                {
                  "value": 4,
                  "label": "next.settingsSpec.account.playback.groups.0.rows.1.control.options.3.label"
                },
                {
                  "value": 5,
                  "label": "next.settingsSpec.account.playback.groups.0.rows.1.control.options.4.label"
                }
              ]
            }
          }
        ]
      },
      {
        "title": "next.settingsSpec.account.playback.groups.1.title",
        "rows": [
          {
            "id": "playback.skipping",
            "scope": "profile",
            "key": "playback.introSkip, playback.creditsSkip, playback.recapSkip",
            "label": "next.settingsSpec.account.playback.groups.1.rows.0.label",
            "control": {
              "type": "matrix",
              "columns": [
                {
                  "value": "ask",
                  "label": "next.settingsSpec.account.playback.groups.1.rows.0.control.columns.0.label",
                  "hint": "next.settingsSpec.account.playback.groups.1.rows.0.control.columns.0.hint"
                },
                {
                  "value": "auto",
                  "label": "next.settingsSpec.account.playback.groups.1.rows.0.control.columns.1.label",
                  "hint": "next.settingsSpec.account.playback.groups.1.rows.0.control.columns.1.hint"
                },
                {
                  "value": "off",
                  "label": "next.settingsSpec.account.playback.groups.1.rows.0.control.columns.2.label",
                  "hint": "next.settingsSpec.account.playback.groups.1.rows.0.control.columns.2.hint"
                }
              ],
              "items": [
                {
                  "id": "intro",
                  "label": "next.settingsSpec.account.playback.groups.1.rows.0.control.items.0.label",
                  "default": "ask"
                },
                {
                  "id": "credits",
                  "label": "next.settingsSpec.account.playback.groups.1.rows.0.control.items.1.label",
                  "default": "ask"
                },
                {
                  "id": "recap",
                  "label": "next.settingsSpec.account.playback.groups.1.rows.0.control.items.2.label",
                  "default": "ask"
                }
              ]
            }
          },
          {
            "id": "playback.skipBackMs",
            "scope": "profile",
            "key": "playback.skipBackMs",
            "label": "next.settingsSpec.account.playback.groups.1.rows.1.label",
            "control": {
              "type": "select",
              "default": 10000,
              "options": [
                {
                  "value": 5000,
                  "label": "next.settingsSpec.account.playback.groups.1.rows.1.control.options.0.label"
                },
                {
                  "value": 10000,
                  "label": "next.settingsSpec.account.playback.groups.1.rows.1.control.options.1.label"
                },
                {
                  "value": 15000,
                  "label": "next.settingsSpec.account.playback.groups.1.rows.1.control.options.2.label"
                },
                {
                  "value": 30000,
                  "label": "next.settingsSpec.account.playback.groups.1.rows.1.control.options.3.label"
                }
              ]
            }
          },
          {
            "id": "playback.skipForwardMs",
            "scope": "profile",
            "key": "playback.skipForwardMs",
            "label": "next.settingsSpec.account.playback.groups.1.rows.2.label",
            "control": {
              "type": "select",
              "default": 30000,
              "options": [
                {
                  "value": 10000,
                  "label": "next.settingsSpec.account.playback.groups.1.rows.2.control.options.0.label"
                },
                {
                  "value": 15000,
                  "label": "next.settingsSpec.account.playback.groups.1.rows.2.control.options.1.label"
                },
                {
                  "value": 30000,
                  "label": "next.settingsSpec.account.playback.groups.1.rows.2.control.options.2.label"
                },
                {
                  "value": 60000,
                  "label": "next.settingsSpec.account.playback.groups.1.rows.2.control.options.3.label"
                }
              ]
            }
          }
        ]
      }
    ],
    "advanced": {
      "groups": [
        {
          "title": "next.settingsSpec.account.playback.advanced.groups.0.title",
          "rows": [
            {
              "id": "playback.playedThresholdPercent",
              "scope": "profile",
              "key": "playback.playedThresholdPercent",
              "label": "next.settingsSpec.account.playback.advanced.groups.0.rows.0.label",
              "control": {
                "type": "select",
                "default": "server",
                "options": [
                  {
                    "value": "server",
                    "label": "next.settingsSpec.account.playback.advanced.groups.0.rows.0.control.options.0.label"
                  },
                  {
                    "value": 80,
                    "label": "next.settingsSpec.account.playback.advanced.groups.0.rows.0.control.options.1.label"
                  },
                  {
                    "value": 85,
                    "label": "next.settingsSpec.account.playback.advanced.groups.0.rows.0.control.options.2.label"
                  },
                  {
                    "value": 90,
                    "label": "next.settingsSpec.account.playback.advanced.groups.0.rows.0.control.options.3.label"
                  },
                  {
                    "value": 95,
                    "label": "next.settingsSpec.account.playback.advanced.groups.0.rows.0.control.options.4.label"
                  },
                  {
                    "value": 100,
                    "label": "next.settingsSpec.account.playback.advanced.groups.0.rows.0.control.options.5.label"
                  }
                ]
              }
            },
            {
              "id": "playback.startedThresholdPercent",
              "scope": "profile",
              "key": "playback.startedThresholdPercent",
              "label": "next.settingsSpec.account.playback.advanced.groups.0.rows.1.label",
              "help": "next.settingsSpec.account.playback.advanced.groups.0.rows.1.help",
              "control": {
                "type": "select",
                "default": 5,
                "options": [
                  {
                    "value": 1,
                    "label": "next.settingsSpec.account.playback.advanced.groups.0.rows.1.control.options.0.label"
                  },
                  {
                    "value": 2,
                    "label": "next.settingsSpec.account.playback.advanced.groups.0.rows.1.control.options.1.label"
                  },
                  {
                    "value": 5,
                    "label": "next.settingsSpec.account.playback.advanced.groups.0.rows.1.control.options.2.label"
                  },
                  {
                    "value": 10,
                    "label": "next.settingsSpec.account.playback.advanced.groups.0.rows.1.control.options.3.label"
                  },
                  {
                    "value": 15,
                    "label": "next.settingsSpec.account.playback.advanced.groups.0.rows.1.control.options.4.label"
                  },
                  {
                    "value": 20,
                    "label": "next.settingsSpec.account.playback.advanced.groups.0.rows.1.control.options.5.label"
                  },
                  {
                    "value": 25,
                    "label": "next.settingsSpec.account.playback.advanced.groups.0.rows.1.control.options.6.label"
                  }
                ]
              }
            }
          ]
        },
        {
          "title": "next.settingsSpec.account.playback.advanced.groups.1.title",
          "rows": [
            {
              "id": "playback.resumeRewindMs",
              "scope": "profile",
              "key": "playback.resumeRewindMs",
              "label": "next.settingsSpec.account.playback.advanced.groups.1.rows.0.label",
              "control": {
                "type": "select",
                "default": 0,
                "options": [
                  {
                    "value": 0,
                    "label": "next.settingsSpec.account.playback.advanced.groups.1.rows.0.control.options.0.label"
                  },
                  {
                    "value": 5000,
                    "label": "next.settingsSpec.account.playback.advanced.groups.1.rows.0.control.options.1.label"
                  },
                  {
                    "value": 10000,
                    "label": "next.settingsSpec.account.playback.advanced.groups.1.rows.0.control.options.2.label"
                  },
                  {
                    "value": 30000,
                    "label": "next.settingsSpec.account.playback.advanced.groups.1.rows.0.control.options.3.label"
                  }
                ]
              }
            },
            {
              "id": "playback.stopAtSeasonEnd",
              "scope": "profile",
              "key": "playback.stopAtSeasonEnd",
              "enableWhen": {
                "id": "playback.autoplayNext",
                "in": [
                  true
                ]
              },
              "label": "next.settingsSpec.account.playback.advanced.groups.1.rows.1.label",
              "control": {
                "type": "toggle",
                "default": false
              }
            }
          ]
        }
      ]
    },
    "configOnly": []
  },
  {
    "scope": "account",
    "id": "audio-subtitles",
    "navGroup": "Watching and listening",
    "icon": "cc",
    "kind": "settings",
    "title": "next.settingsSpec.account.audio-subtitles.title",
    "groups": [
      {
        "title": "next.settingsSpec.account.audio-subtitles.groups.0.title",
        "rows": [
          {
            "id": "audioSubtitles.preferredAudio",
            "scope": "profile",
            "key": "playback.preferredAudioLanguages",
            "label": "next.settingsSpec.account.audio-subtitles.groups.0.rows.0.label",
            "help": "next.settingsSpec.account.audio-subtitles.groups.0.rows.0.help",
            "control": {
              "type": "list",
              "reorder": true,
              "addLabel": "next.settingsSpec.account.audio-subtitles.groups.0.rows.0.control.addLabel",
              "empty": "next.settingsSpec.account.audio-subtitles.groups.0.rows.0.control.empty",
              "items": [
                {
                  "code": "@appLanguage",
                  "label": "next.settingsSpec.account.audio-subtitles.groups.0.rows.0.control.items.0.label"
                },
                {
                  "code": "@original",
                  "label": "next.settingsSpec.account.audio-subtitles.groups.0.rows.0.control.items.1.label"
                }
              ]
            }
          }
        ]
      },
      {
        "title": "next.settingsSpec.account.audio-subtitles.groups.1.title",
        "rows": [
          {
            "id": "audioSubtitles.preferredSubtitles",
            "scope": "profile",
            "key": "playback.preferredSubtitleLanguages",
            "label": "next.settingsSpec.account.audio-subtitles.groups.1.rows.0.label",
            "help": "next.settingsSpec.account.audio-subtitles.groups.1.rows.0.help",
            "control": {
              "type": "list",
              "reorder": true,
              "addLabel": "next.settingsSpec.account.audio-subtitles.groups.1.rows.0.control.addLabel",
              "empty": "next.settingsSpec.account.audio-subtitles.groups.1.rows.0.control.empty",
              "items": [
                {
                  "code": "@appLanguage",
                  "label": "next.settingsSpec.account.audio-subtitles.groups.1.rows.0.control.items.0.label"
                }
              ]
            }
          },
          {
            "id": "audioSubtitles.subtitleMode",
            "scope": "profile",
            "key": "playback.subtitleMode",
            "label": "next.settingsSpec.account.audio-subtitles.groups.1.rows.1.label",
            "help": "next.settingsSpec.account.audio-subtitles.groups.1.rows.1.help",
            "control": {
              "type": "select",
              "default": "foreign",
              "options": [
                {
                  "value": "off",
                  "label": "next.settingsSpec.account.audio-subtitles.groups.1.rows.1.control.options.0.label"
                },
                {
                  "value": "foreign",
                  "label": "next.settingsSpec.account.audio-subtitles.groups.1.rows.1.control.options.1.label",
                  "hint": "next.settingsSpec.account.audio-subtitles.groups.1.rows.1.control.options.1.hint"
                },
                {
                  "value": "forced",
                  "label": "next.settingsSpec.account.audio-subtitles.groups.1.rows.1.control.options.2.label",
                  "hint": "next.settingsSpec.account.audio-subtitles.groups.1.rows.1.control.options.2.hint"
                },
                {
                  "value": "always",
                  "label": "next.settingsSpec.account.audio-subtitles.groups.1.rows.1.control.options.3.label"
                }
              ]
            }
          }
        ]
      },
      {
        "title": "next.settingsSpec.account.audio-subtitles.groups.2.title",
        "rows": [
          {
            "id": "audioSubtitles.subtitlePreview",
            "scope": "profile",
            "key": "audioSubtitles.subtitlePreview",
            "label": "next.settingsSpec.account.audio-subtitles.groups.2.rows.0.label",
            "control": {
              "type": "preview",
              "uses": {
                "size": "audioSubtitles.subtitleSize",
                "color": "audioSubtitles.subtitleColor",
                "background": "audioSubtitles.subtitleBackground",
                "position": "audioSubtitles.subtitlePosition"
              },
              "kind": "subtitles",
              "text": "next.settingsSpec.account.audio-subtitles.groups.2.rows.0.control.text"
            }
          },
          {
            "id": "audioSubtitles.subtitleSize",
            "scope": "profile",
            "key": "playback.subtitleSize",
            "label": "next.settingsSpec.account.audio-subtitles.groups.2.rows.1.label",
            "control": {
              "type": "segmented",
              "default": "medium",
              "options": [
                {
                  "value": "small",
                  "label": "next.settingsSpec.account.audio-subtitles.groups.2.rows.1.control.options.0.label"
                },
                {
                  "value": "medium",
                  "label": "next.settingsSpec.account.audio-subtitles.groups.2.rows.1.control.options.1.label"
                },
                {
                  "value": "large",
                  "label": "next.settingsSpec.account.audio-subtitles.groups.2.rows.1.control.options.2.label"
                },
                {
                  "value": "extra-large",
                  "label": "next.settingsSpec.account.audio-subtitles.groups.2.rows.1.control.options.3.label"
                }
              ]
            }
          },
          {
            "id": "audioSubtitles.subtitleColor",
            "scope": "profile",
            "key": "playback.subtitleColor",
            "label": "next.settingsSpec.account.audio-subtitles.groups.2.rows.2.label",
            "control": {
              "type": "select",
              "default": "white",
              "options": [
                {
                  "value": "white",
                  "label": "next.settingsSpec.account.audio-subtitles.groups.2.rows.2.control.options.0.label"
                },
                {
                  "value": "yellow",
                  "label": "next.settingsSpec.account.audio-subtitles.groups.2.rows.2.control.options.1.label"
                },
                {
                  "value": "cyan",
                  "label": "next.settingsSpec.account.audio-subtitles.groups.2.rows.2.control.options.2.label"
                },
                {
                  "value": "green",
                  "label": "next.settingsSpec.account.audio-subtitles.groups.2.rows.2.control.options.3.label"
                },
                {
                  "value": "pink",
                  "label": "next.settingsSpec.account.audio-subtitles.groups.2.rows.2.control.options.4.label"
                }
              ]
            }
          },
          {
            "id": "audioSubtitles.subtitleBackground",
            "scope": "profile",
            "key": "playback.subtitleBackground",
            "label": "next.settingsSpec.account.audio-subtitles.groups.2.rows.3.label",
            "control": {
              "type": "select",
              "default": "translucent",
              "options": [
                {
                  "value": "none",
                  "label": "next.settingsSpec.account.audio-subtitles.groups.2.rows.3.control.options.0.label"
                },
                {
                  "value": "outline",
                  "label": "next.settingsSpec.account.audio-subtitles.groups.2.rows.3.control.options.1.label"
                },
                {
                  "value": "shadow",
                  "label": "next.settingsSpec.account.audio-subtitles.groups.2.rows.3.control.options.2.label"
                },
                {
                  "value": "translucent",
                  "label": "next.settingsSpec.account.audio-subtitles.groups.2.rows.3.control.options.3.label"
                },
                {
                  "value": "opaque",
                  "label": "next.settingsSpec.account.audio-subtitles.groups.2.rows.3.control.options.4.label"
                }
              ]
            }
          },
          {
            "id": "audioSubtitles.subtitlePosition",
            "scope": "profile",
            "key": "playback.subtitlePosition",
            "label": "next.settingsSpec.account.audio-subtitles.groups.2.rows.4.label",
            "control": {
              "type": "select",
              "default": "bottom",
              "options": [
                {
                  "value": "bottom",
                  "label": "next.settingsSpec.account.audio-subtitles.groups.2.rows.4.control.options.0.label"
                },
                {
                  "value": "raised",
                  "label": "next.settingsSpec.account.audio-subtitles.groups.2.rows.4.control.options.1.label",
                  "hint": "next.settingsSpec.account.audio-subtitles.groups.2.rows.4.control.options.1.hint"
                },
                {
                  "value": "top",
                  "label": "next.settingsSpec.account.audio-subtitles.groups.2.rows.4.control.options.2.label"
                }
              ]
            }
          }
        ]
      }
    ],
    "advanced": {
      "groups": [
        {
          "title": "next.settingsSpec.account.audio-subtitles.advanced.groups.0.title",
          "rows": [
            {
              "id": "audioSubtitles.audioDescription",
              "scope": "profile",
              "key": "playback.audioDescription",
              "label": "next.settingsSpec.account.audio-subtitles.advanced.groups.0.rows.0.label",
              "help": "next.settingsSpec.account.audio-subtitles.advanced.groups.0.rows.0.help",
              "control": {
                "type": "select",
                "default": "avoid",
                "options": [
                  {
                    "value": "avoid",
                    "label": "next.settingsSpec.account.audio-subtitles.advanced.groups.0.rows.0.control.options.0.label"
                  },
                  {
                    "value": "prefer",
                    "label": "next.settingsSpec.account.audio-subtitles.advanced.groups.0.rows.0.control.options.1.label",
                    "hint": "next.settingsSpec.account.audio-subtitles.advanced.groups.0.rows.0.control.options.1.hint"
                  }
                ]
              }
            },
            {
              "id": "audioSubtitles.trackChoice",
              "scope": "profile",
              "key": "playback.audioTrackChoice",
              "label": "next.settingsSpec.account.audio-subtitles.advanced.groups.0.rows.1.label",
              "help": "next.settingsSpec.account.audio-subtitles.advanced.groups.0.rows.1.help",
              "control": {
                "type": "select",
                "default": "best",
                "options": [
                  {
                    "value": "best",
                    "label": "next.settingsSpec.account.audio-subtitles.advanced.groups.0.rows.1.control.options.0.label"
                  },
                  {
                    "value": "channels",
                    "label": "next.settingsSpec.account.audio-subtitles.advanced.groups.0.rows.1.control.options.1.label",
                    "hint": "next.settingsSpec.account.audio-subtitles.advanced.groups.0.rows.1.control.options.1.hint"
                  },
                  {
                    "value": "stereo",
                    "label": "next.settingsSpec.account.audio-subtitles.advanced.groups.0.rows.1.control.options.2.label",
                    "hint": "next.settingsSpec.account.audio-subtitles.advanced.groups.0.rows.1.control.options.2.hint"
                  }
                ]
              }
            }
          ]
        },
        {
          "title": "next.settingsSpec.account.audio-subtitles.advanced.groups.1.title",
          "rows": [
            {
              "id": "audioSubtitles.surround",
              "scope": "device",
              "key": "forceStereo",
              "label": "next.settingsSpec.account.audio-subtitles.advanced.groups.1.rows.0.label",
              "help": "next.settingsSpec.account.audio-subtitles.advanced.groups.1.rows.0.help",
              "control": {
                "type": "segmented",
                "default": "asis",
                "options": [
                  {
                    "value": "asis",
                    "label": "next.settingsSpec.account.audio-subtitles.advanced.groups.1.rows.0.control.options.0.label"
                  },
                  {
                    "value": "stereo",
                    "label": "next.settingsSpec.account.audio-subtitles.advanced.groups.1.rows.0.control.options.1.label"
                  }
                ]
              }
            },
            {
              "id": "audioSubtitles.levelDownmix",
              "scope": "device",
              "key": "delivery.normalizeDownmix",
              "label": "next.settingsSpec.account.audio-subtitles.advanced.groups.1.rows.1.label",
              "help": "next.settingsSpec.account.audio-subtitles.advanced.groups.1.rows.1.help",
              "control": {
                "type": "toggle",
                "default": false
              }
            }
          ]
        },
        {
          "title": "next.settingsSpec.account.audio-subtitles.advanced.groups.2.title",
          "rows": [
            {
              "id": "audioSubtitles.audioDelay",
              "scope": "device",
              "key": "audioDelayMs",
              "platforms": [
                "tv",
                "phone"
              ],
              "label": "next.settingsSpec.account.audio-subtitles.advanced.groups.2.rows.0.label",
              "control": {
                "type": "select",
                "default": "0",
                "options": [
                  {
                    "value": "0",
                    "label": "next.settingsSpec.account.audio-subtitles.advanced.groups.2.rows.0.control.options.0.label"
                  },
                  {
                    "value": "50",
                    "label": "next.settingsSpec.account.audio-subtitles.advanced.groups.2.rows.0.control.options.1.label"
                  },
                  {
                    "value": "100",
                    "label": "next.settingsSpec.account.audio-subtitles.advanced.groups.2.rows.0.control.options.2.label"
                  },
                  {
                    "value": "200",
                    "label": "next.settingsSpec.account.audio-subtitles.advanced.groups.2.rows.0.control.options.3.label"
                  },
                  {
                    "value": "-50",
                    "label": "next.settingsSpec.account.audio-subtitles.advanced.groups.2.rows.0.control.options.4.label"
                  },
                  {
                    "value": "-100",
                    "label": "next.settingsSpec.account.audio-subtitles.advanced.groups.2.rows.0.control.options.5.label"
                  },
                  {
                    "value": "-200",
                    "label": "next.settingsSpec.account.audio-subtitles.advanced.groups.2.rows.0.control.options.6.label"
                  }
                ]
              },
              "os": [
                "android",
                "ios",
                "ipados",
                "androidtv",
                "firetv",
                "roku",
                "webos",
                "tizen"
              ]
            },
            {
              "id": "audioSubtitles.systemCaptions",
              "scope": "device",
              "key": "useSystemCaptionStyle",
              "platforms": [
                "phone",
                "tv"
              ],
              "label": "next.settingsSpec.account.audio-subtitles.advanced.groups.2.rows.1.label",
              "help": "next.settingsSpec.account.audio-subtitles.advanced.groups.2.rows.1.help",
              "control": {
                "type": "toggle",
                "default": false
              }
            }
          ]
        },
        {
          "title": "next.settingsSpec.account.audio-subtitles.advanced.groups.3.title",
          "rows": [
            {
              "id": "audioSubtitles.preferSdh",
              "scope": "profile",
              "key": "playback.preferSdhSubtitles",
              "label": "next.settingsSpec.account.audio-subtitles.advanced.groups.3.rows.0.label",
              "help": "next.settingsSpec.account.audio-subtitles.advanced.groups.3.rows.0.help",
              "control": {
                "type": "toggle",
                "default": false
              }
            },
            {
              "id": "audioSubtitles.forcedFallback",
              "scope": "profile",
              "key": "playback.forcedSubtitleFallback",
              "enableWhen": {
                "id": "audioSubtitles.subtitleMode",
                "in": [
                  "foreign",
                  "forced"
                ]
              },
              "label": "next.settingsSpec.account.audio-subtitles.advanced.groups.3.rows.1.label",
              "help": "next.settingsSpec.account.audio-subtitles.advanced.groups.3.rows.1.help",
              "control": {
                "type": "select",
                "default": "none",
                "options": [
                  {
                    "value": "none",
                    "label": "next.settingsSpec.account.audio-subtitles.advanced.groups.3.rows.1.control.options.0.label"
                  },
                  {
                    "value": "full",
                    "label": "next.settingsSpec.account.audio-subtitles.advanced.groups.3.rows.1.control.options.1.label"
                  }
                ]
              }
            },
            {
              "id": "audioSubtitles.untaggedForced",
              "scope": "profile",
              "key": "playback.useUntaggedForced",
              "enableWhen": {
                "id": "audioSubtitles.subtitleMode",
                "in": [
                  "foreign",
                  "forced"
                ]
              },
              "label": "next.settingsSpec.account.audio-subtitles.advanced.groups.3.rows.2.label",
              "help": "next.settingsSpec.account.audio-subtitles.advanced.groups.3.rows.2.help",
              "control": {
                "type": "toggle",
                "default": true
              }
            }
          ]
        },
        {
          "title": "next.settingsSpec.account.audio-subtitles.advanced.groups.4.title",
          "rows": [
            {
              "id": "audioSubtitles.subtitleFont",
              "scope": "profile",
              "key": "playback.subtitleFont",
              "label": "next.settingsSpec.account.audio-subtitles.advanced.groups.4.rows.0.label",
              "control": {
                "type": "select",
                "default": "default",
                "options": [
                  {
                    "value": "default",
                    "label": "next.settingsSpec.account.audio-subtitles.advanced.groups.4.rows.0.control.options.0.label"
                  },
                  {
                    "value": "serif",
                    "label": "next.settingsSpec.account.audio-subtitles.advanced.groups.4.rows.0.control.options.1.label"
                  },
                  {
                    "value": "mono",
                    "label": "next.settingsSpec.account.audio-subtitles.advanced.groups.4.rows.0.control.options.2.label"
                  },
                  {
                    "value": "rounded",
                    "label": "next.settingsSpec.account.audio-subtitles.advanced.groups.4.rows.0.control.options.3.label"
                  }
                ]
              }
            },
            {
              "id": "audioSubtitles.styledSubtitles",
              "scope": "profile",
              "key": "playback.keepSubtitleStyling",
              "label": "next.settingsSpec.account.audio-subtitles.advanced.groups.4.rows.1.label",
              "help": "next.settingsSpec.account.audio-subtitles.advanced.groups.4.rows.1.help",
              "control": {
                "type": "toggle",
                "default": true
              }
            }
          ]
        }
      ]
    },
    "configOnly": []
  },
  {
    "scope": "account",
    "id": "quality",
    "navGroup": "Watching and listening",
    "icon": "gauge",
    "kind": "settings",
    "title": "next.settingsSpec.account.quality.title",
    "groups": [
      {
        "title": "next.settingsSpec.account.quality.groups.0.title",
        "rows": [
          {
            "id": "quality.home",
            "scope": "device",
            "key": "quality.home",
            "label": "next.settingsSpec.account.quality.groups.0.rows.0.label",
            "control": {
              "type": "select",
              "default": "original",
              "options": [
                {
                  "value": "original",
                  "label": "next.settingsSpec.account.quality.groups.0.rows.0.control.options.0.label",
                  "hint": "next.settingsSpec.account.quality.groups.0.rows.0.control.options.0.hint"
                },
                {
                  "value": "automatic",
                  "label": "next.settingsSpec.account.quality.groups.0.rows.0.control.options.1.label",
                  "hint": "next.settingsSpec.account.quality.groups.0.rows.0.control.options.1.hint"
                },
                {
                  "value": "4k",
                  "label": "next.settingsSpec.account.quality.groups.0.rows.0.control.options.2.label",
                  "hint": "next.settingsSpec.account.quality.groups.0.rows.0.control.options.2.hint"
                },
                {
                  "value": "1080p-high",
                  "label": "next.settingsSpec.account.quality.groups.0.rows.0.control.options.3.label",
                  "hint": "next.settingsSpec.account.quality.groups.0.rows.0.control.options.3.hint"
                },
                {
                  "value": "1080p-low",
                  "label": "next.settingsSpec.account.quality.groups.0.rows.0.control.options.4.label",
                  "hint": "next.settingsSpec.account.quality.groups.0.rows.0.control.options.4.hint"
                },
                {
                  "value": "720p-high",
                  "label": "next.settingsSpec.account.quality.groups.0.rows.0.control.options.5.label",
                  "hint": "next.settingsSpec.account.quality.groups.0.rows.0.control.options.5.hint"
                },
                {
                  "value": "720p-low",
                  "label": "next.settingsSpec.account.quality.groups.0.rows.0.control.options.6.label",
                  "hint": "next.settingsSpec.account.quality.groups.0.rows.0.control.options.6.hint"
                },
                {
                  "value": "480p",
                  "label": "next.settingsSpec.account.quality.groups.0.rows.0.control.options.7.label",
                  "hint": "next.settingsSpec.account.quality.groups.0.rows.0.control.options.7.hint"
                },
                {
                  "value": "240p",
                  "label": "next.settingsSpec.account.quality.groups.0.rows.0.control.options.8.label",
                  "hint": "next.settingsSpec.account.quality.groups.0.rows.0.control.options.8.hint"
                }
              ]
            }
          },
          {
            "id": "quality.remote",
            "scope": "device",
            "key": "quality.remote",
            "label": "next.settingsSpec.account.quality.groups.0.rows.1.label",
            "control": {
              "type": "select",
              "default": "original",
              "options": [
                {
                  "value": "original",
                  "label": "next.settingsSpec.account.quality.groups.0.rows.1.control.options.0.label",
                  "hint": "next.settingsSpec.account.quality.groups.0.rows.1.control.options.0.hint"
                },
                {
                  "value": "automatic",
                  "label": "next.settingsSpec.account.quality.groups.0.rows.1.control.options.1.label",
                  "hint": "next.settingsSpec.account.quality.groups.0.rows.1.control.options.1.hint"
                },
                {
                  "value": "4k",
                  "label": "next.settingsSpec.account.quality.groups.0.rows.1.control.options.2.label",
                  "hint": "next.settingsSpec.account.quality.groups.0.rows.1.control.options.2.hint"
                },
                {
                  "value": "1080p-high",
                  "label": "next.settingsSpec.account.quality.groups.0.rows.1.control.options.3.label",
                  "hint": "next.settingsSpec.account.quality.groups.0.rows.1.control.options.3.hint"
                },
                {
                  "value": "1080p-low",
                  "label": "next.settingsSpec.account.quality.groups.0.rows.1.control.options.4.label",
                  "hint": "next.settingsSpec.account.quality.groups.0.rows.1.control.options.4.hint"
                },
                {
                  "value": "720p-high",
                  "label": "next.settingsSpec.account.quality.groups.0.rows.1.control.options.5.label",
                  "hint": "next.settingsSpec.account.quality.groups.0.rows.1.control.options.5.hint"
                },
                {
                  "value": "720p-low",
                  "label": "next.settingsSpec.account.quality.groups.0.rows.1.control.options.6.label",
                  "hint": "next.settingsSpec.account.quality.groups.0.rows.1.control.options.6.hint"
                },
                {
                  "value": "480p",
                  "label": "next.settingsSpec.account.quality.groups.0.rows.1.control.options.7.label",
                  "hint": "next.settingsSpec.account.quality.groups.0.rows.1.control.options.7.hint"
                },
                {
                  "value": "240p",
                  "label": "next.settingsSpec.account.quality.groups.0.rows.1.control.options.8.label",
                  "hint": "next.settingsSpec.account.quality.groups.0.rows.1.control.options.8.hint"
                }
              ]
            }
          },
          {
            "id": "quality.mobileData",
            "scope": "device",
            "key": "quality.mobile",
            "platforms": [
              "phone"
            ],
            "label": "next.settingsSpec.account.quality.groups.0.rows.2.label",
            "help": "next.settingsSpec.account.quality.groups.0.rows.2.help",
            "control": {
              "type": "select",
              "default": "720p-high",
              "options": [
                {
                  "value": "original",
                  "label": "next.settingsSpec.account.quality.groups.0.rows.2.control.options.0.label",
                  "hint": "next.settingsSpec.account.quality.groups.0.rows.2.control.options.0.hint"
                },
                {
                  "value": "automatic",
                  "label": "next.settingsSpec.account.quality.groups.0.rows.2.control.options.1.label",
                  "hint": "next.settingsSpec.account.quality.groups.0.rows.2.control.options.1.hint"
                },
                {
                  "value": "4k",
                  "label": "next.settingsSpec.account.quality.groups.0.rows.2.control.options.2.label",
                  "hint": "next.settingsSpec.account.quality.groups.0.rows.2.control.options.2.hint"
                },
                {
                  "value": "1080p-high",
                  "label": "next.settingsSpec.account.quality.groups.0.rows.2.control.options.3.label",
                  "hint": "next.settingsSpec.account.quality.groups.0.rows.2.control.options.3.hint"
                },
                {
                  "value": "1080p-low",
                  "label": "next.settingsSpec.account.quality.groups.0.rows.2.control.options.4.label",
                  "hint": "next.settingsSpec.account.quality.groups.0.rows.2.control.options.4.hint"
                },
                {
                  "value": "720p-high",
                  "label": "next.settingsSpec.account.quality.groups.0.rows.2.control.options.5.label",
                  "hint": "next.settingsSpec.account.quality.groups.0.rows.2.control.options.5.hint"
                },
                {
                  "value": "720p-low",
                  "label": "next.settingsSpec.account.quality.groups.0.rows.2.control.options.6.label",
                  "hint": "next.settingsSpec.account.quality.groups.0.rows.2.control.options.6.hint"
                },
                {
                  "value": "480p",
                  "label": "next.settingsSpec.account.quality.groups.0.rows.2.control.options.7.label",
                  "hint": "next.settingsSpec.account.quality.groups.0.rows.2.control.options.7.hint"
                },
                {
                  "value": "240p",
                  "label": "next.settingsSpec.account.quality.groups.0.rows.2.control.options.8.label",
                  "hint": "next.settingsSpec.account.quality.groups.0.rows.2.control.options.8.hint"
                }
              ]
            }
          },
          {
            "id": "quality.awayLimit",
            "scope": "server",
            "key": "quality.awayLimit",
            "audience": "owner",
            "label": "next.settingsSpec.account.quality.groups.0.rows.3.label",
            "help": "next.settingsSpec.account.quality.groups.0.rows.3.help",
            "control": {
              "type": "status"
            },
            "children": [
              {
                "id": "quality.awayLimitChange",
                "scope": "server",
                "key": "quality.awayLimitChange",
                "audience": "owner",
                "label": "next.settingsSpec.account.quality.groups.0.rows.3.children.0.label",
                "control": {
                  "type": "link",
                  "label": "next.settingsSpec.account.quality.groups.0.rows.3.children.0.control.label",
                  "to": "server/streaming/streaming.awayQuality"
                }
              }
            ]
          }
        ]
      },
      {
        "title": "next.settingsSpec.account.quality.groups.1.title",
        "platforms": [
          "phone",
          "desktop"
        ],
        "rows": [
          {
            "id": "quality.downloadQuality",
            "scope": "device",
            "key": "downloadQuality",
            "label": "next.settingsSpec.account.quality.groups.1.rows.0.label",
            "help": "next.settingsSpec.account.quality.groups.1.rows.0.help",
            "control": {
              "type": "select",
              "default": "original",
              "options": [
                {
                  "value": "ask",
                  "label": "next.settingsSpec.account.quality.groups.1.rows.0.control.options.0.label",
                  "hint": "next.settingsSpec.account.quality.groups.1.rows.0.control.options.0.hint"
                },
                {
                  "value": "original",
                  "label": "next.settingsSpec.account.quality.groups.1.rows.0.control.options.1.label",
                  "hint": "next.settingsSpec.account.quality.groups.1.rows.0.control.options.1.hint"
                },
                {
                  "value": "4k",
                  "label": "next.settingsSpec.account.quality.groups.1.rows.0.control.options.2.label",
                  "hint": "next.settingsSpec.account.quality.groups.1.rows.0.control.options.2.hint"
                },
                {
                  "value": "1080p-high",
                  "label": "next.settingsSpec.account.quality.groups.1.rows.0.control.options.3.label",
                  "hint": "next.settingsSpec.account.quality.groups.1.rows.0.control.options.3.hint"
                },
                {
                  "value": "1080p-low",
                  "label": "next.settingsSpec.account.quality.groups.1.rows.0.control.options.4.label",
                  "hint": "next.settingsSpec.account.quality.groups.1.rows.0.control.options.4.hint"
                },
                {
                  "value": "720p-high",
                  "label": "next.settingsSpec.account.quality.groups.1.rows.0.control.options.5.label",
                  "hint": "next.settingsSpec.account.quality.groups.1.rows.0.control.options.5.hint"
                },
                {
                  "value": "720p-low",
                  "label": "next.settingsSpec.account.quality.groups.1.rows.0.control.options.6.label",
                  "hint": "next.settingsSpec.account.quality.groups.1.rows.0.control.options.6.hint"
                },
                {
                  "value": "480p",
                  "label": "next.settingsSpec.account.quality.groups.1.rows.0.control.options.7.label",
                  "hint": "next.settingsSpec.account.quality.groups.1.rows.0.control.options.7.hint"
                },
                {
                  "value": "240p",
                  "label": "next.settingsSpec.account.quality.groups.1.rows.0.control.options.8.label",
                  "hint": "next.settingsSpec.account.quality.groups.1.rows.0.control.options.8.hint"
                }
              ]
            }
          }
        ]
      },
      {
        "title": "next.settingsSpec.account.quality.groups.2.title",
        "rows": [
          {
            "id": "quality.tvSupports",
            "scope": "device",
            "key": "tvHdr, tvSurround",
            "platforms": [
              "tv"
            ],
            "label": "next.settingsSpec.account.quality.groups.2.rows.0.label",
            "help": "next.settingsSpec.account.quality.groups.2.rows.0.help",
            "control": {
              "type": "select",
              "default": "none",
              "options": [
                {
                  "value": "none",
                  "label": "next.settingsSpec.account.quality.groups.2.rows.0.control.options.0.label"
                },
                {
                  "value": "hdr",
                  "label": "next.settingsSpec.account.quality.groups.2.rows.0.control.options.1.label"
                },
                {
                  "value": "surround",
                  "label": "next.settingsSpec.account.quality.groups.2.rows.0.control.options.2.label"
                },
                {
                  "value": "hdr-surround",
                  "label": "next.settingsSpec.account.quality.groups.2.rows.0.control.options.3.label"
                }
              ]
            }
          },
          {
            "id": "quality.safeMode",
            "scope": "device",
            "key": "quality.safeMode",
            "label": "next.settingsSpec.account.quality.groups.2.rows.1.label",
            "help": "next.settingsSpec.account.quality.groups.2.rows.1.help",
            "control": {
              "type": "select",
              "default": "automatic",
              "options": [
                {
                  "value": "automatic",
                  "label": "next.settingsSpec.account.quality.groups.2.rows.1.control.options.0.label",
                  "hint": "next.settingsSpec.account.quality.groups.2.rows.1.control.options.0.hint",
                  "sets": {
                    "audioSubtitles.surround": "asis",
                    "quality.disableHdr": false
                  }
                },
                {
                  "value": "safe",
                  "label": "next.settingsSpec.account.quality.groups.2.rows.1.control.options.1.label",
                  "hint": "next.settingsSpec.account.quality.groups.2.rows.1.control.options.1.hint",
                  "sets": {
                    "audioSubtitles.surround": "stereo",
                    "quality.disableHdr": true
                  }
                },
                {
                  "value": "custom",
                  "label": "next.settingsSpec.account.quality.groups.2.rows.1.control.options.2.label",
                  "hint": "next.settingsSpec.account.quality.groups.2.rows.1.control.options.2.hint"
                }
              ]
            }
          }
        ]
      }
    ],
    "advanced": {
      "groups": [
        {
          "title": "next.settingsSpec.account.quality.advanced.groups.0.title",
          "rows": [
            {
              "id": "quality.playbackMethod",
              "scope": "device",
              "key": "delivery.directPlay, delivery.directStream, delivery.transcode",
              "label": "next.settingsSpec.account.quality.advanced.groups.0.rows.0.label",
              "help": "next.settingsSpec.account.quality.advanced.groups.0.rows.0.help",
              "control": {
                "type": "select",
                "default": "automatic",
                "options": [
                  {
                    "value": "automatic",
                    "label": "next.settingsSpec.account.quality.advanced.groups.0.rows.0.control.options.0.label",
                    "hint": "next.settingsSpec.account.quality.advanced.groups.0.rows.0.control.options.0.hint",
                    "sets": {
                      "quality.directPlay": "prefer",
                      "quality.directStream": "allow",
                      "quality.transcode": "allow"
                    }
                  },
                  {
                    "value": "original",
                    "label": "next.settingsSpec.account.quality.advanced.groups.0.rows.0.control.options.1.label",
                    "hint": "next.settingsSpec.account.quality.advanced.groups.0.rows.0.control.options.1.hint",
                    "sets": {
                      "quality.directPlay": "require",
                      "quality.directStream": "never",
                      "quality.transcode": "never"
                    }
                  },
                  {
                    "value": "convert",
                    "label": "next.settingsSpec.account.quality.advanced.groups.0.rows.0.control.options.2.label",
                    "hint": "next.settingsSpec.account.quality.advanced.groups.0.rows.0.control.options.2.hint",
                    "sets": {
                      "quality.directPlay": "never",
                      "quality.directStream": "never",
                      "quality.transcode": "require"
                    }
                  },
                  {
                    "value": "custom",
                    "label": "next.settingsSpec.account.quality.advanced.groups.0.rows.0.control.options.3.label",
                    "hint": "next.settingsSpec.account.quality.advanced.groups.0.rows.0.control.options.3.hint"
                  }
                ]
              }
            }
          ]
        },
        {
          "title": "next.settingsSpec.account.quality.advanced.groups.1.title",
          "rows": [
            {
              "id": "quality.directPlay",
              "scope": "device",
              "key": "delivery.directPlay",
              "label": "next.settingsSpec.account.quality.advanced.groups.1.rows.0.label",
              "help": "next.settingsSpec.account.quality.advanced.groups.1.rows.0.help",
              "control": {
                "type": "select",
                "default": "prefer",
                "options": [
                  {
                    "value": "prefer",
                    "label": "next.settingsSpec.account.quality.advanced.groups.1.rows.0.control.options.0.label",
                    "hint": "next.settingsSpec.account.quality.advanced.groups.1.rows.0.control.options.0.hint"
                  },
                  {
                    "value": "allow",
                    "label": "next.settingsSpec.account.quality.advanced.groups.1.rows.0.control.options.1.label",
                    "hint": "next.settingsSpec.account.quality.advanced.groups.1.rows.0.control.options.1.hint"
                  },
                  {
                    "value": "never",
                    "label": "next.settingsSpec.account.quality.advanced.groups.1.rows.0.control.options.2.label",
                    "hint": "next.settingsSpec.account.quality.advanced.groups.1.rows.0.control.options.2.hint"
                  },
                  {
                    "value": "require",
                    "label": "next.settingsSpec.account.quality.advanced.groups.1.rows.0.control.options.3.label",
                    "hint": "next.settingsSpec.account.quality.advanced.groups.1.rows.0.control.options.3.hint"
                  }
                ]
              }
            },
            {
              "id": "quality.directStream",
              "scope": "device",
              "key": "delivery.directStream",
              "label": "next.settingsSpec.account.quality.advanced.groups.1.rows.1.label",
              "help": "next.settingsSpec.account.quality.advanced.groups.1.rows.1.help",
              "control": {
                "type": "select",
                "default": "allow",
                "options": [
                  {
                    "value": "prefer",
                    "label": "next.settingsSpec.account.quality.advanced.groups.1.rows.1.control.options.0.label",
                    "hint": "next.settingsSpec.account.quality.advanced.groups.1.rows.1.control.options.0.hint"
                  },
                  {
                    "value": "allow",
                    "label": "next.settingsSpec.account.quality.advanced.groups.1.rows.1.control.options.1.label",
                    "hint": "next.settingsSpec.account.quality.advanced.groups.1.rows.1.control.options.1.hint"
                  },
                  {
                    "value": "never",
                    "label": "next.settingsSpec.account.quality.advanced.groups.1.rows.1.control.options.2.label",
                    "hint": "next.settingsSpec.account.quality.advanced.groups.1.rows.1.control.options.2.hint"
                  },
                  {
                    "value": "require",
                    "label": "next.settingsSpec.account.quality.advanced.groups.1.rows.1.control.options.3.label",
                    "hint": "next.settingsSpec.account.quality.advanced.groups.1.rows.1.control.options.3.hint"
                  }
                ]
              }
            },
            {
              "id": "quality.transcode",
              "scope": "device",
              "key": "delivery.transcode",
              "label": "next.settingsSpec.account.quality.advanced.groups.1.rows.2.label",
              "help": "next.settingsSpec.account.quality.advanced.groups.1.rows.2.help",
              "control": {
                "type": "select",
                "default": "allow",
                "options": [
                  {
                    "value": "prefer",
                    "label": "next.settingsSpec.account.quality.advanced.groups.1.rows.2.control.options.0.label",
                    "hint": "next.settingsSpec.account.quality.advanced.groups.1.rows.2.control.options.0.hint"
                  },
                  {
                    "value": "allow",
                    "label": "next.settingsSpec.account.quality.advanced.groups.1.rows.2.control.options.1.label",
                    "hint": "next.settingsSpec.account.quality.advanced.groups.1.rows.2.control.options.1.hint"
                  },
                  {
                    "value": "never",
                    "label": "next.settingsSpec.account.quality.advanced.groups.1.rows.2.control.options.2.label",
                    "hint": "next.settingsSpec.account.quality.advanced.groups.1.rows.2.control.options.2.hint"
                  },
                  {
                    "value": "require",
                    "label": "next.settingsSpec.account.quality.advanced.groups.1.rows.2.control.options.3.label",
                    "hint": "next.settingsSpec.account.quality.advanced.groups.1.rows.2.control.options.3.hint"
                  }
                ]
              }
            }
          ]
        },
        {
          "title": "next.settingsSpec.account.quality.advanced.groups.2.title",
          "rows": [
            {
              "id": "quality.disableHdr",
              "scope": "device",
              "key": "disableHdr",
              "label": "next.settingsSpec.account.quality.advanced.groups.2.rows.0.label",
              "help": "next.settingsSpec.account.quality.advanced.groups.2.rows.0.help",
              "control": {
                "type": "toggle",
                "default": false
              }
            }
          ]
        },
        {
          "title": "next.settingsSpec.account.quality.advanced.groups.3.title",
          "rows": [
            {
              "id": "quality.formatOverrides",
              "scope": "device",
              "key": "formatOverrides",
              "label": "next.settingsSpec.account.quality.advanced.groups.3.rows.0.label",
              "control": {
                "type": "matrix",
                "columns": [
                  {
                    "value": "detect",
                    "label": "next.settingsSpec.account.quality.advanced.groups.3.rows.0.control.columns.0.label",
                    "hint": "next.settingsSpec.account.quality.advanced.groups.3.rows.0.control.columns.0.hint"
                  },
                  {
                    "value": "convert",
                    "label": "next.settingsSpec.account.quality.advanced.groups.3.rows.0.control.columns.1.label",
                    "hint": "next.settingsSpec.account.quality.advanced.groups.3.rows.0.control.columns.1.hint"
                  }
                ],
                "items": [
                  {
                    "id": "hevc",
                    "label": "next.settingsSpec.account.quality.advanced.groups.3.rows.0.control.items.0.label",
                    "default": "detect"
                  },
                  {
                    "id": "av1",
                    "label": "next.settingsSpec.account.quality.advanced.groups.3.rows.0.control.items.1.label",
                    "default": "detect"
                  },
                  {
                    "id": "vp9",
                    "label": "next.settingsSpec.account.quality.advanced.groups.3.rows.0.control.items.2.label",
                    "default": "detect"
                  },
                  {
                    "id": "tenbit",
                    "label": "next.settingsSpec.account.quality.advanced.groups.3.rows.0.control.items.3.label",
                    "default": "detect"
                  },
                  {
                    "id": "dv",
                    "label": "next.settingsSpec.account.quality.advanced.groups.3.rows.0.control.items.4.label",
                    "default": "detect"
                  }
                ]
              }
            }
          ]
        }
      ]
    },
    "configOnly": []
  },
  {
    "scope": "account",
    "id": "music",
    "navGroup": "Watching and listening",
    "icon": "music",
    "kind": "settings",
    "title": "next.settingsSpec.account.music.title",
    "groups": [
      {
        "title": "next.settingsSpec.account.music.groups.0.title",
        "rows": [
          {
            "id": "music.audioNormalization",
            "scope": "profile",
            "key": "music.audioNormalization",
            "label": "next.settingsSpec.account.music.groups.0.rows.0.label",
            "control": {
              "type": "select",
              "default": "off",
              "options": [
                {
                  "value": "off",
                  "label": "next.settingsSpec.account.music.groups.0.rows.0.control.options.0.label"
                },
                {
                  "value": "track",
                  "label": "next.settingsSpec.account.music.groups.0.rows.0.control.options.1.label"
                },
                {
                  "value": "album",
                  "label": "next.settingsSpec.account.music.groups.0.rows.0.control.options.2.label",
                  "hint": "next.settingsSpec.account.music.groups.0.rows.0.control.options.2.hint"
                }
              ]
            }
          },
          {
            "id": "music.crossfadeMs",
            "scope": "profile",
            "key": "music.crossfadeMs",
            "label": "next.settingsSpec.account.music.groups.0.rows.1.label",
            "control": {
              "type": "select",
              "default": 0,
              "options": [
                {
                  "value": 0,
                  "label": "next.settingsSpec.account.music.groups.0.rows.1.control.options.0.label"
                },
                {
                  "value": 2000,
                  "label": "next.settingsSpec.account.music.groups.0.rows.1.control.options.1.label"
                },
                {
                  "value": 4000,
                  "label": "next.settingsSpec.account.music.groups.0.rows.1.control.options.2.label"
                },
                {
                  "value": 6000,
                  "label": "next.settingsSpec.account.music.groups.0.rows.1.control.options.3.label"
                },
                {
                  "value": 8000,
                  "label": "next.settingsSpec.account.music.groups.0.rows.1.control.options.4.label"
                },
                {
                  "value": 10000,
                  "label": "next.settingsSpec.account.music.groups.0.rows.1.control.options.5.label"
                },
                {
                  "value": 12000,
                  "label": "next.settingsSpec.account.music.groups.0.rows.1.control.options.6.label"
                }
              ]
            },
            "children": [
              {
                "id": "music.smoothTransitions",
                "scope": "profile",
                "key": "music.smoothTransitions",
                "enableWhen": {
                  "id": "music.crossfadeMs",
                  "in": [
                    2000,
                    4000,
                    6000,
                    8000,
                    10000,
                    12000
                  ]
                },
                "label": "next.settingsSpec.account.music.groups.0.rows.1.children.0.label",
                "help": "next.settingsSpec.account.music.groups.0.rows.1.children.0.help",
                "control": {
                  "type": "toggle",
                  "default": true
                }
              }
            ]
          },
          {
            "id": "music.gapless",
            "scope": "profile",
            "key": "music.gapless",
            "label": "next.settingsSpec.account.music.groups.0.rows.2.label",
            "help": "next.settingsSpec.account.music.groups.0.rows.2.help",
            "control": {
              "type": "toggle",
              "default": true
            }
          },
          {
            "id": "music.eq",
            "scope": "device",
            "key": "music.eq",
            "label": "next.settingsSpec.account.music.groups.0.rows.3.label",
            "control": {
              "type": "link",
              "label": "next.settingsSpec.account.music.groups.0.rows.3.control.label"
            }
          }
        ]
      },
      {
        "title": "next.settingsSpec.account.music.groups.1.title",
        "rows": [
          {
            "id": "playback.showSyncedLyrics",
            "scope": "profile",
            "key": "playback.showSyncedLyrics",
            "label": "next.settingsSpec.account.music.groups.1.rows.0.label",
            "control": {
              "type": "toggle",
              "default": true
            }
          }
        ]
      },
      {
        "title": "next.settingsSpec.account.music.groups.2.title",
        "rows": [
          {
            "id": "playback.audiobookSpeed",
            "scope": "profile",
            "key": "audiobooks.defaultSpeed",
            "label": "next.settingsSpec.account.music.groups.2.rows.0.label",
            "control": {
              "type": "select",
              "default": 1,
              "options": [
                {
                  "value": 0.5,
                  "label": "next.settingsSpec.account.music.groups.2.rows.0.control.options.0.label"
                },
                {
                  "value": 0.75,
                  "label": "next.settingsSpec.account.music.groups.2.rows.0.control.options.1.label"
                },
                {
                  "value": 1,
                  "label": "next.settingsSpec.account.music.groups.2.rows.0.control.options.2.label"
                },
                {
                  "value": 1.25,
                  "label": "next.settingsSpec.account.music.groups.2.rows.0.control.options.3.label"
                },
                {
                  "value": 1.5,
                  "label": "next.settingsSpec.account.music.groups.2.rows.0.control.options.4.label"
                },
                {
                  "value": 1.75,
                  "label": "next.settingsSpec.account.music.groups.2.rows.0.control.options.5.label"
                },
                {
                  "value": 2,
                  "label": "next.settingsSpec.account.music.groups.2.rows.0.control.options.6.label"
                },
                {
                  "value": 2.5,
                  "label": "next.settingsSpec.account.music.groups.2.rows.0.control.options.7.label"
                },
                {
                  "value": 3,
                  "label": "next.settingsSpec.account.music.groups.2.rows.0.control.options.8.label"
                }
              ]
            }
          },
          {
            "id": "audiobooks.skipBackMs",
            "scope": "profile",
            "key": "audiobooks.skipBackMs",
            "label": "next.settingsSpec.account.music.groups.2.rows.1.label",
            "control": {
              "type": "select",
              "default": 15000,
              "options": [
                {
                  "value": 10000,
                  "label": "next.settingsSpec.account.music.groups.2.rows.1.control.options.0.label"
                },
                {
                  "value": 15000,
                  "label": "next.settingsSpec.account.music.groups.2.rows.1.control.options.1.label"
                },
                {
                  "value": 30000,
                  "label": "next.settingsSpec.account.music.groups.2.rows.1.control.options.2.label"
                },
                {
                  "value": 60000,
                  "label": "next.settingsSpec.account.music.groups.2.rows.1.control.options.3.label"
                }
              ]
            }
          },
          {
            "id": "audiobooks.skipForwardMs",
            "scope": "profile",
            "key": "audiobooks.skipForwardMs",
            "label": "next.settingsSpec.account.music.groups.2.rows.2.label",
            "control": {
              "type": "select",
              "default": 30000,
              "options": [
                {
                  "value": 15000,
                  "label": "next.settingsSpec.account.music.groups.2.rows.2.control.options.0.label"
                },
                {
                  "value": 30000,
                  "label": "next.settingsSpec.account.music.groups.2.rows.2.control.options.1.label"
                },
                {
                  "value": 60000,
                  "label": "next.settingsSpec.account.music.groups.2.rows.2.control.options.2.label"
                },
                {
                  "value": 90000,
                  "label": "next.settingsSpec.account.music.groups.2.rows.2.control.options.3.label"
                }
              ]
            }
          },
          {
            "id": "audiobooks.resumeRewindMs",
            "scope": "profile",
            "key": "audiobooks.resumeRewindMs",
            "label": "next.settingsSpec.account.music.groups.2.rows.3.label",
            "control": {
              "type": "select",
              "default": 0,
              "options": [
                {
                  "value": 0,
                  "label": "next.settingsSpec.account.music.groups.2.rows.3.control.options.0.label"
                },
                {
                  "value": 5000,
                  "label": "next.settingsSpec.account.music.groups.2.rows.3.control.options.1.label"
                },
                {
                  "value": 15000,
                  "label": "next.settingsSpec.account.music.groups.2.rows.3.control.options.2.label"
                },
                {
                  "value": 30000,
                  "label": "next.settingsSpec.account.music.groups.2.rows.3.control.options.3.label"
                }
              ]
            }
          }
        ]
      },
      {
        "title": "next.settingsSpec.account.music.groups.3.title",
        "rows": [
          {
            "id": "music.qualityAway",
            "scope": "device",
            "key": "music.qualityAway",
            "label": "next.settingsSpec.account.music.groups.3.rows.0.label",
            "control": {
              "type": "select",
              "default": "320",
              "options": [
                {
                  "value": "original",
                  "label": "next.settingsSpec.account.music.groups.3.rows.0.control.options.0.label",
                  "hint": "next.settingsSpec.account.music.groups.3.rows.0.control.options.0.hint"
                },
                {
                  "value": "cd",
                  "label": "next.settingsSpec.account.music.groups.3.rows.0.control.options.1.label",
                  "hint": "next.settingsSpec.account.music.groups.3.rows.0.control.options.1.hint"
                },
                {
                  "value": "320",
                  "label": "next.settingsSpec.account.music.groups.3.rows.0.control.options.2.label",
                  "hint": "next.settingsSpec.account.music.groups.3.rows.0.control.options.2.hint"
                },
                {
                  "value": "96",
                  "label": "next.settingsSpec.account.music.groups.3.rows.0.control.options.3.label",
                  "hint": "next.settingsSpec.account.music.groups.3.rows.0.control.options.3.hint"
                }
              ]
            }
          },
          {
            "id": "music.qualityMobile",
            "scope": "device",
            "key": "music.qualityMobile",
            "platforms": [
              "phone"
            ],
            "label": "next.settingsSpec.account.music.groups.3.rows.1.label",
            "control": {
              "type": "select",
              "default": "320",
              "options": [
                {
                  "value": "original",
                  "label": "next.settingsSpec.account.music.groups.3.rows.1.control.options.0.label",
                  "hint": "next.settingsSpec.account.music.groups.3.rows.1.control.options.0.hint"
                },
                {
                  "value": "cd",
                  "label": "next.settingsSpec.account.music.groups.3.rows.1.control.options.1.label",
                  "hint": "next.settingsSpec.account.music.groups.3.rows.1.control.options.1.hint"
                },
                {
                  "value": "320",
                  "label": "next.settingsSpec.account.music.groups.3.rows.1.control.options.2.label",
                  "hint": "next.settingsSpec.account.music.groups.3.rows.1.control.options.2.hint"
                },
                {
                  "value": "96",
                  "label": "next.settingsSpec.account.music.groups.3.rows.1.control.options.3.label",
                  "hint": "next.settingsSpec.account.music.groups.3.rows.1.control.options.3.hint"
                }
              ]
            }
          },
          {
            "id": "music.downloadQuality",
            "scope": "device",
            "key": "music.downloadQuality",
            "platforms": [
              "phone",
              "desktop"
            ],
            "label": "next.settingsSpec.account.music.groups.3.rows.2.label",
            "control": {
              "type": "select",
              "default": "320",
              "options": [
                {
                  "value": "original",
                  "label": "next.settingsSpec.account.music.groups.3.rows.2.control.options.0.label",
                  "hint": "next.settingsSpec.account.music.groups.3.rows.2.control.options.0.hint"
                },
                {
                  "value": "cd",
                  "label": "next.settingsSpec.account.music.groups.3.rows.2.control.options.1.label",
                  "hint": "next.settingsSpec.account.music.groups.3.rows.2.control.options.1.hint"
                },
                {
                  "value": "320",
                  "label": "next.settingsSpec.account.music.groups.3.rows.2.control.options.2.label",
                  "hint": "next.settingsSpec.account.music.groups.3.rows.2.control.options.2.hint"
                },
                {
                  "value": "96",
                  "label": "next.settingsSpec.account.music.groups.3.rows.2.control.options.3.label",
                  "hint": "next.settingsSpec.account.music.groups.3.rows.2.control.options.3.hint"
                }
              ]
            }
          }
        ]
      }
    ],
    "advanced": {
      "groups": [
        {
          "title": "next.settingsSpec.account.music.advanced.groups.0.title",
          "rows": [
            {
              "id": "music.sampleRateMatching",
              "scope": "device",
              "key": "music.sampleRateMatching",
              "platforms": [
                "phone",
                "tv"
              ],
              "label": "next.settingsSpec.account.music.advanced.groups.0.rows.0.label",
              "help": "next.settingsSpec.account.music.advanced.groups.0.rows.0.help",
              "control": {
                "type": "segmented",
                "default": "smart",
                "options": [
                  {
                    "value": "smart",
                    "label": "next.settingsSpec.account.music.advanced.groups.0.rows.0.control.options.0.label"
                  },
                  {
                    "value": "strict",
                    "label": "next.settingsSpec.account.music.advanced.groups.0.rows.0.control.options.1.label"
                  },
                  {
                    "value": "off",
                    "label": "next.settingsSpec.account.music.advanced.groups.0.rows.0.control.options.2.label"
                  }
                ]
              }
            },
            {
              "id": "music.neverBoost",
              "scope": "profile",
              "key": "music.neverBoost",
              "label": "next.settingsSpec.account.music.advanced.groups.0.rows.1.label",
              "help": "next.settingsSpec.account.music.advanced.groups.0.rows.1.help",
              "control": {
                "type": "toggle",
                "default": false
              }
            },
            {
              "id": "music.dsdGainDb",
              "scope": "profile",
              "key": "music.dsdGainDb",
              "label": "next.settingsSpec.account.music.advanced.groups.0.rows.2.label",
              "help": "next.settingsSpec.account.music.advanced.groups.0.rows.2.help",
              "control": {
                "type": "select",
                "default": -6,
                "options": [
                  {
                    "value": 0,
                    "label": "next.settingsSpec.account.music.advanced.groups.0.rows.2.control.options.0.label"
                  },
                  {
                    "value": -3,
                    "label": "next.settingsSpec.account.music.advanced.groups.0.rows.2.control.options.1.label"
                  },
                  {
                    "value": -6,
                    "label": "next.settingsSpec.account.music.advanced.groups.0.rows.2.control.options.2.label"
                  },
                  {
                    "value": -9,
                    "label": "next.settingsSpec.account.music.advanced.groups.0.rows.2.control.options.3.label"
                  },
                  {
                    "value": -12,
                    "label": "next.settingsSpec.account.music.advanced.groups.0.rows.2.control.options.4.label"
                  }
                ]
              }
            }
          ]
        },
        {
          "title": "next.settingsSpec.account.music.advanced.groups.1.title",
          "rows": [
            {
              "id": "music.idlePause",
              "scope": "profile",
              "key": "playback.sleepTimerMinutes",
              "label": "next.settingsSpec.account.music.advanced.groups.1.rows.0.label",
              "help": "next.settingsSpec.account.music.advanced.groups.1.rows.0.help",
              "control": {
                "type": "select",
                "default": 0,
                "options": [
                  {
                    "value": 0,
                    "label": "next.settingsSpec.account.music.advanced.groups.1.rows.0.control.options.0.label"
                  },
                  {
                    "value": 30,
                    "label": "next.settingsSpec.account.music.advanced.groups.1.rows.0.control.options.1.label"
                  },
                  {
                    "value": 60,
                    "label": "next.settingsSpec.account.music.advanced.groups.1.rows.0.control.options.2.label"
                  },
                  {
                    "value": 90,
                    "label": "next.settingsSpec.account.music.advanced.groups.1.rows.0.control.options.3.label"
                  },
                  {
                    "value": 120,
                    "label": "next.settingsSpec.account.music.advanced.groups.1.rows.0.control.options.4.label"
                  }
                ]
              }
            }
          ]
        }
      ]
    },
    "configOnly": []
  },
  {
    "scope": "account",
    "id": "reading",
    "navGroup": "Watching and listening",
    "icon": "book",
    "kind": "settings",
    "platforms": [
      "web",
      "phone"
    ],
    "title": "next.settingsSpec.account.reading.title",
    "groups": [
      {
        "title": "next.settingsSpec.account.reading.groups.0.title",
        "rows": [
          {
            "id": "reading.theme",
            "scope": "profile",
            "key": "reading.theme",
            "label": "next.settingsSpec.account.reading.groups.0.rows.0.label",
            "control": {
              "type": "segmented",
              "default": "dark",
              "options": [
                {
                  "value": "dark",
                  "label": "next.settingsSpec.account.reading.groups.0.rows.0.control.options.0.label"
                },
                {
                  "value": "black",
                  "label": "next.settingsSpec.account.reading.groups.0.rows.0.control.options.1.label"
                },
                {
                  "value": "sepia",
                  "label": "next.settingsSpec.account.reading.groups.0.rows.0.control.options.2.label"
                },
                {
                  "value": "light",
                  "label": "next.settingsSpec.account.reading.groups.0.rows.0.control.options.3.label"
                }
              ]
            }
          },
          {
            "id": "reading.font",
            "scope": "profile",
            "key": "reading.font",
            "label": "next.settingsSpec.account.reading.groups.0.rows.1.label",
            "control": {
              "type": "select",
              "default": "literata",
              "options": [
                {
                  "value": "publisher",
                  "label": "next.settingsSpec.account.reading.groups.0.rows.1.control.options.0.label",
                  "hint": "next.settingsSpec.account.reading.groups.0.rows.1.control.options.0.hint"
                },
                {
                  "value": "literata",
                  "label": "next.settingsSpec.account.reading.groups.0.rows.1.control.options.1.label"
                },
                {
                  "value": "manrope",
                  "label": "next.settingsSpec.account.reading.groups.0.rows.1.control.options.2.label"
                }
              ]
            }
          },
          {
            "id": "reading.textSize",
            "scope": "profile",
            "key": "reading.textSize",
            "label": "next.settingsSpec.account.reading.groups.0.rows.2.label",
            "control": {
              "type": "select",
              "default": 5,
              "options": [
                {
                  "value": 1,
                  "label": "next.settingsSpec.account.reading.groups.0.rows.2.control.options.0.label"
                },
                {
                  "value": 2,
                  "label": "next.settingsSpec.account.reading.groups.0.rows.2.control.options.1.label"
                },
                {
                  "value": 3,
                  "label": "next.settingsSpec.account.reading.groups.0.rows.2.control.options.2.label"
                },
                {
                  "value": 4,
                  "label": "next.settingsSpec.account.reading.groups.0.rows.2.control.options.3.label"
                },
                {
                  "value": 5,
                  "label": "next.settingsSpec.account.reading.groups.0.rows.2.control.options.4.label"
                },
                {
                  "value": 6,
                  "label": "next.settingsSpec.account.reading.groups.0.rows.2.control.options.5.label"
                },
                {
                  "value": 7,
                  "label": "next.settingsSpec.account.reading.groups.0.rows.2.control.options.6.label"
                }
              ]
            }
          }
        ]
      },
      {
        "title": "next.settingsSpec.account.reading.groups.1.title",
        "rows": [
          {
            "id": "reading.lineSpacing",
            "scope": "profile",
            "key": "reading.lineSpacing",
            "label": "next.settingsSpec.account.reading.groups.1.rows.0.label",
            "control": {
              "type": "segmented",
              "default": "normal",
              "options": [
                {
                  "value": "tight",
                  "label": "next.settingsSpec.account.reading.groups.1.rows.0.control.options.0.label"
                },
                {
                  "value": "normal",
                  "label": "next.settingsSpec.account.reading.groups.1.rows.0.control.options.1.label"
                },
                {
                  "value": "loose",
                  "label": "next.settingsSpec.account.reading.groups.1.rows.0.control.options.2.label"
                }
              ]
            }
          },
          {
            "id": "reading.margins",
            "scope": "profile",
            "key": "reading.margins",
            "label": "next.settingsSpec.account.reading.groups.1.rows.1.label",
            "control": {
              "type": "segmented",
              "default": "normal",
              "options": [
                {
                  "value": "narrow",
                  "label": "next.settingsSpec.account.reading.groups.1.rows.1.control.options.0.label"
                },
                {
                  "value": "normal",
                  "label": "next.settingsSpec.account.reading.groups.1.rows.1.control.options.1.label"
                },
                {
                  "value": "wide",
                  "label": "next.settingsSpec.account.reading.groups.1.rows.1.control.options.2.label"
                }
              ]
            }
          },
          {
            "id": "reading.alignment",
            "scope": "profile",
            "key": "reading.alignment",
            "label": "next.settingsSpec.account.reading.groups.1.rows.2.label",
            "control": {
              "type": "segmented",
              "default": "justified",
              "options": [
                {
                  "value": "left",
                  "label": "next.settingsSpec.account.reading.groups.1.rows.2.control.options.0.label"
                },
                {
                  "value": "justified",
                  "label": "next.settingsSpec.account.reading.groups.1.rows.2.control.options.1.label"
                }
              ]
            }
          },
          {
            "id": "reading.layout",
            "scope": "profile",
            "key": "reading.layout",
            "label": "next.settingsSpec.account.reading.groups.1.rows.3.label",
            "control": {
              "type": "segmented",
              "default": "pages",
              "options": [
                {
                  "value": "pages",
                  "label": "next.settingsSpec.account.reading.groups.1.rows.3.control.options.0.label"
                },
                {
                  "value": "scroll",
                  "label": "next.settingsSpec.account.reading.groups.1.rows.3.control.options.1.label"
                }
              ]
            }
          },
          {
            "id": "reading.twoPages",
            "scope": "profile",
            "key": "reading.twoPages",
            "platforms": [
              "web",
              "phone"
            ],
            "label": "next.settingsSpec.account.reading.groups.1.rows.4.label",
            "help": "next.settingsSpec.account.reading.groups.1.rows.4.help",
            "control": {
              "type": "toggle",
              "default": true
            }
          }
        ]
      },
      {
        "title": "next.settingsSpec.account.reading.groups.2.title",
        "rows": [
          {
            "id": "comics.fit",
            "scope": "profile",
            "key": "comics.fit",
            "label": "next.settingsSpec.account.reading.groups.2.rows.0.label",
            "control": {
              "type": "select",
              "default": "auto",
              "options": [
                {
                  "value": "auto",
                  "label": "next.settingsSpec.account.reading.groups.2.rows.0.control.options.0.label",
                  "hint": "next.settingsSpec.account.reading.groups.2.rows.0.control.options.0.hint"
                },
                {
                  "value": "width",
                  "label": "next.settingsSpec.account.reading.groups.2.rows.0.control.options.1.label"
                },
                {
                  "value": "height",
                  "label": "next.settingsSpec.account.reading.groups.2.rows.0.control.options.2.label"
                }
              ]
            }
          },
          {
            "id": "comics.direction",
            "scope": "profile",
            "key": "comics.direction",
            "label": "next.settingsSpec.account.reading.groups.2.rows.1.label",
            "control": {
              "type": "select",
              "default": "file",
              "options": [
                {
                  "value": "file",
                  "label": "next.settingsSpec.account.reading.groups.2.rows.1.control.options.0.label"
                },
                {
                  "value": "ltr",
                  "label": "next.settingsSpec.account.reading.groups.2.rows.1.control.options.1.label"
                },
                {
                  "value": "rtl",
                  "label": "next.settingsSpec.account.reading.groups.2.rows.1.control.options.2.label"
                },
                {
                  "value": "vertical",
                  "label": "next.settingsSpec.account.reading.groups.2.rows.1.control.options.3.label",
                  "hint": "next.settingsSpec.account.reading.groups.2.rows.1.control.options.3.hint"
                }
              ]
            }
          }
        ]
      }
    ],
    "advanced": {
      "groups": [
        {
          "title": "next.settingsSpec.account.reading.advanced.groups.0.title",
          "rows": [
            {
              "id": "reading.tapToTurn",
              "scope": "profile",
              "key": "reading.tapToTurn",
              "platforms": [
                "phone"
              ],
              "label": "next.settingsSpec.account.reading.advanced.groups.0.rows.0.label",
              "help": "next.settingsSpec.account.reading.advanced.groups.0.rows.0.help",
              "control": {
                "type": "toggle",
                "default": true
              }
            },
            {
              "id": "reading.hyphenation",
              "scope": "profile",
              "key": "reading.hyphenation",
              "label": "next.settingsSpec.account.reading.advanced.groups.0.rows.1.label",
              "help": "next.settingsSpec.account.reading.advanced.groups.0.rows.1.help",
              "control": {
                "type": "toggle",
                "default": true
              }
            },
            {
              "id": "reading.highlightColour",
              "scope": "profile",
              "key": "reading.highlightColour",
              "label": "next.settingsSpec.account.reading.advanced.groups.0.rows.2.label",
              "control": {
                "type": "select",
                "default": "yellow",
                "options": [
                  {
                    "value": "yellow",
                    "label": "next.settingsSpec.account.reading.advanced.groups.0.rows.2.control.options.0.label"
                  },
                  {
                    "value": "green",
                    "label": "next.settingsSpec.account.reading.advanced.groups.0.rows.2.control.options.1.label"
                  },
                  {
                    "value": "blue",
                    "label": "next.settingsSpec.account.reading.advanced.groups.0.rows.2.control.options.2.label"
                  },
                  {
                    "value": "pink",
                    "label": "next.settingsSpec.account.reading.advanced.groups.0.rows.2.control.options.3.label"
                  }
                ]
              }
            }
          ]
        },
        {
          "title": "next.settingsSpec.account.reading.advanced.groups.1.title",
          "rows": [
            {
              "id": "comics.firstPageAlone",
              "scope": "profile",
              "key": "comics.firstPageAlone",
              "enableWhen": {
                "id": "reading.twoPages",
                "in": [
                  true
                ]
              },
              "label": "next.settingsSpec.account.reading.advanced.groups.1.rows.0.label",
              "help": "next.settingsSpec.account.reading.advanced.groups.1.rows.0.help",
              "control": {
                "type": "toggle",
                "default": true
              }
            },
            {
              "id": "reading.pdfView",
              "scope": "profile",
              "key": "reading.pdfView",
              "label": "next.settingsSpec.account.reading.advanced.groups.1.rows.1.label",
              "control": {
                "type": "select",
                "default": "automatic",
                "options": [
                  {
                    "value": "automatic",
                    "label": "next.settingsSpec.account.reading.advanced.groups.1.rows.1.control.options.0.label",
                    "hint": "next.settingsSpec.account.reading.advanced.groups.1.rows.1.control.options.0.hint"
                  },
                  {
                    "value": "spread",
                    "label": "next.settingsSpec.account.reading.advanced.groups.1.rows.1.control.options.1.label"
                  },
                  {
                    "value": "width",
                    "label": "next.settingsSpec.account.reading.advanced.groups.1.rows.1.control.options.2.label"
                  },
                  {
                    "value": "page",
                    "label": "next.settingsSpec.account.reading.advanced.groups.1.rows.1.control.options.3.label"
                  }
                ]
              }
            }
          ]
        }
      ]
    },
    "configOnly": []
  },
  {
    "scope": "account",
    "id": "appearance",
    "navGroup": "This app",
    "icon": "sliders",
    "kind": "settings",
    "title": "next.settingsSpec.account.appearance.title",
    "groups": [
      {
        "rows": [
          {
            "id": "appearance.customizeHome",
            "scope": "profile",
            "label": "next.settingsSpec.account.appearance.groups.0.rows.0.label",
            "help": "next.settingsSpec.account.appearance.groups.0.rows.0.help",
            "control": {
              "type": "link",
              "label": "next.settingsSpec.account.appearance.groups.0.rows.0.control.label"
            }
          }
        ]
      },
      {
        "title": "next.settingsSpec.account.appearance.groups.1.title",
        "rows": [
          {
            "id": "appearance.cardSize",
            "scope": "device",
            "key": "appearance.cardSize",
            "label": "next.settingsSpec.account.appearance.groups.1.rows.0.label",
            "control": {
              "type": "select",
              "default": "medium",
              "options": [
                {
                  "value": "small",
                  "label": "next.settingsSpec.account.appearance.groups.1.rows.0.control.options.0.label",
                  "hint": "next.settingsSpec.account.appearance.groups.1.rows.0.control.options.0.hint",
                  "sets": {
                    "appearance.cardSizeExact": 85
                  }
                },
                {
                  "value": "medium",
                  "label": "next.settingsSpec.account.appearance.groups.1.rows.0.control.options.1.label",
                  "hint": "next.settingsSpec.account.appearance.groups.1.rows.0.control.options.1.hint",
                  "sets": {
                    "appearance.cardSizeExact": 100
                  }
                },
                {
                  "value": "large",
                  "label": "next.settingsSpec.account.appearance.groups.1.rows.0.control.options.2.label",
                  "hint": "next.settingsSpec.account.appearance.groups.1.rows.0.control.options.2.hint",
                  "sets": {
                    "appearance.cardSizeExact": 125
                  }
                },
                {
                  "value": "custom",
                  "label": "next.settingsSpec.account.appearance.groups.1.rows.0.control.options.3.label",
                  "hint": "next.settingsSpec.account.appearance.groups.1.rows.0.control.options.3.hint"
                }
              ]
            }
          },
          {
            "id": "appearance.backdrops",
            "scope": "device",
            "key": "appearance.showBackdrops",
            "label": "next.settingsSpec.account.appearance.groups.1.rows.1.label",
            "control": {
              "type": "toggle",
              "default": true
            }
          },
          {
            "id": "appearance.textSize",
            "scope": "device",
            "platforms": [
              "tv"
            ],
            "label": "next.settingsSpec.account.appearance.groups.1.rows.2.label",
            "control": {
              "type": "segmented",
              "default": "standard",
              "options": [
                {
                  "value": "standard",
                  "label": "next.settingsSpec.account.appearance.groups.1.rows.2.control.options.0.label"
                },
                {
                  "value": "large",
                  "label": "next.settingsSpec.account.appearance.groups.1.rows.2.control.options.1.label"
                }
              ]
            }
          },
          {
            "id": "appearance.motion",
            "scope": "device",
            "label": "next.settingsSpec.account.appearance.groups.1.rows.3.label",
            "help": "next.settingsSpec.account.appearance.groups.1.rows.3.help",
            "control": {
              "type": "segmented",
              "default": "follow",
              "options": [
                {
                  "value": "follow",
                  "label": "next.settingsSpec.account.appearance.groups.1.rows.3.control.options.0.label"
                },
                {
                  "value": "reduce",
                  "label": "next.settingsSpec.account.appearance.groups.1.rows.3.control.options.1.label"
                }
              ]
            }
          }
        ]
      }
    ],
    "advanced": {
      "groups": [
        {
          "title": "next.settingsSpec.account.appearance.advanced.groups.0.title",
          "rows": [
            {
              "id": "appearance.cardSizeExact",
              "scope": "device",
              "key": "appearance.cardSizePercent",
              "label": "next.settingsSpec.account.appearance.advanced.groups.0.rows.0.label",
              "help": "next.settingsSpec.account.appearance.advanced.groups.0.rows.0.help",
              "control": {
                "type": "number",
                "default": 100,
                "unit": "next.settingsSpec.account.appearance.advanced.groups.0.rows.0.control.unit",
                "min": 75,
                "max": 150,
                "step": 5
              }
            },
            {
              "id": "appearance.libraryView",
              "scope": "device",
              "key": "libraryView",
              "platforms": [
                "web"
              ],
              "label": "next.settingsSpec.account.appearance.advanced.groups.0.rows.1.label",
              "help": "next.settingsSpec.account.appearance.advanced.groups.0.rows.1.help",
              "control": {
                "type": "segmented",
                "default": "grid",
                "options": [
                  {
                    "value": "grid",
                    "label": "next.settingsSpec.account.appearance.advanced.groups.0.rows.1.control.options.0.label"
                  },
                  {
                    "value": "list",
                    "label": "next.settingsSpec.account.appearance.advanced.groups.0.rows.1.control.options.1.label"
                  }
                ]
              }
            },
            {
              "id": "appearance.episodesLayout",
              "scope": "device",
              "key": "portico.episodes.layout",
              "label": "next.settingsSpec.account.appearance.advanced.groups.0.rows.2.label",
              "control": {
                "type": "select",
                "default": "auto",
                "options": [
                  {
                    "value": "auto",
                    "label": "next.settingsSpec.account.appearance.advanced.groups.0.rows.2.control.options.0.label",
                    "hint": "next.settingsSpec.account.appearance.advanced.groups.0.rows.2.control.options.0.hint"
                  },
                  {
                    "value": "tiles",
                    "label": "next.settingsSpec.account.appearance.advanced.groups.0.rows.2.control.options.1.label"
                  },
                  {
                    "value": "list",
                    "label": "next.settingsSpec.account.appearance.advanced.groups.0.rows.2.control.options.2.label"
                  }
                ]
              }
            }
          ]
        },
        {
          "title": "next.settingsSpec.account.appearance.advanced.groups.1.title",
          "rows": [
            {
              "id": "appearance.layout",
              "scope": "device",
              "platforms": [
                "phone",
                "tv"
              ],
              "label": "next.settingsSpec.account.appearance.advanced.groups.1.rows.0.label",
              "help": "next.settingsSpec.account.appearance.advanced.groups.1.rows.0.help",
              "control": {
                "type": "select",
                "default": "auto",
                "options": [
                  {
                    "value": "auto",
                    "label": "next.settingsSpec.account.appearance.advanced.groups.1.rows.0.control.options.0.label"
                  },
                  {
                    "value": "television",
                    "label": "next.settingsSpec.account.appearance.advanced.groups.1.rows.0.control.options.1.label"
                  },
                  {
                    "value": "touch",
                    "label": "next.settingsSpec.account.appearance.advanced.groups.1.rows.0.control.options.2.label"
                  }
                ]
              },
              "os": [
                "android",
                "androidtv",
                "firetv"
              ]
            }
          ]
        }
      ]
    },
    "configOnly": []
  },
  {
    "scope": "account",
    "id": "language",
    "navGroup": "This app",
    "icon": "globe",
    "kind": "settings",
    "title": "next.settingsSpec.account.language.title",
    "groups": [
      {
        "rows": [
          {
            "id": "language.locale",
            "scope": "profile",
            "key": "region.locale",
            "label": "next.settingsSpec.account.language.groups.0.rows.0.label",
            "help": "next.settingsSpec.account.language.groups.0.rows.0.help",
            "control": {
              "type": "select",
              "default": "auto",
              "options": [
                {
                  "value": "auto",
                  "label": "next.settingsSpec.account.language.groups.0.rows.0.control.options.0.label",
                  "hint": "next.settingsSpec.account.language.groups.0.rows.0.control.options.0.hint"
                },
                {
                  "value": "en-US",
                  "label": "next.settingsSpec.account.language.groups.0.rows.0.control.options.1.label"
                },
                {
                  "value": "en-CA",
                  "label": "next.settingsSpec.account.language.groups.0.rows.0.control.options.2.label",
                  "hint": "next.settingsSpec.account.language.groups.0.rows.0.control.options.2.hint"
                },
                {
                  "value": "en-GB",
                  "label": "next.settingsSpec.account.language.groups.0.rows.0.control.options.3.label",
                  "hint": "next.settingsSpec.account.language.groups.0.rows.0.control.options.3.hint"
                },
                {
                  "value": "fr",
                  "label": "next.settingsSpec.account.language.groups.0.rows.0.control.options.4.label"
                },
                {
                  "value": "es",
                  "label": "next.settingsSpec.account.language.groups.0.rows.0.control.options.5.label"
                },
                {
                  "value": "de",
                  "label": "next.settingsSpec.account.language.groups.0.rows.0.control.options.6.label"
                },
                {
                  "value": "ru",
                  "label": "next.settingsSpec.account.language.groups.0.rows.0.control.options.7.label"
                }
              ]
            }
          },
          {
            "id": "language.hourCycle",
            "scope": "profile",
            "key": "region.hourCycle",
            "label": "next.settingsSpec.account.language.groups.0.rows.1.label",
            "control": {
              "type": "segmented",
              "default": "auto",
              "options": [
                {
                  "value": "auto",
                  "label": "next.settingsSpec.account.language.groups.0.rows.1.control.options.0.label"
                },
                {
                  "value": "h12",
                  "label": "next.settingsSpec.account.language.groups.0.rows.1.control.options.1.label"
                },
                {
                  "value": "h23",
                  "label": "next.settingsSpec.account.language.groups.0.rows.1.control.options.2.label"
                }
              ]
            }
          }
        ]
      }
    ],
    "advanced": {
      "groups": [
        {
          "title": "next.settingsSpec.account.language.advanced.groups.0.title",
          "rows": [
            {
              "id": "language.formats",
              "scope": "profile",
              "key": "region.formats",
              "label": "next.settingsSpec.account.language.advanced.groups.0.rows.0.label",
              "help": "next.settingsSpec.account.language.advanced.groups.0.rows.0.help",
              "control": {
                "type": "select",
                "default": "auto",
                "options": [
                  {
                    "value": "auto",
                    "label": "next.settingsSpec.account.language.advanced.groups.0.rows.0.control.options.0.label"
                  },
                  {
                    "value": "en-US",
                    "label": "next.settingsSpec.account.language.advanced.groups.0.rows.0.control.options.1.label",
                    "hint": "next.settingsSpec.account.language.advanced.groups.0.rows.0.control.options.1.hint"
                  },
                  {
                    "value": "en-CA",
                    "label": "next.settingsSpec.account.language.advanced.groups.0.rows.0.control.options.2.label",
                    "hint": "next.settingsSpec.account.language.advanced.groups.0.rows.0.control.options.2.hint"
                  },
                  {
                    "value": "en-GB",
                    "label": "next.settingsSpec.account.language.advanced.groups.0.rows.0.control.options.3.label",
                    "hint": "next.settingsSpec.account.language.advanced.groups.0.rows.0.control.options.3.hint"
                  },
                  {
                    "value": "en-AU",
                    "label": "next.settingsSpec.account.language.advanced.groups.0.rows.0.control.options.4.label",
                    "hint": "next.settingsSpec.account.language.advanced.groups.0.rows.0.control.options.4.hint"
                  },
                  {
                    "value": "fr-FR",
                    "label": "next.settingsSpec.account.language.advanced.groups.0.rows.0.control.options.5.label",
                    "hint": "next.settingsSpec.account.language.advanced.groups.0.rows.0.control.options.5.hint"
                  },
                  {
                    "value": "de-DE",
                    "label": "next.settingsSpec.account.language.advanced.groups.0.rows.0.control.options.6.label",
                    "hint": "next.settingsSpec.account.language.advanced.groups.0.rows.0.control.options.6.hint"
                  },
                  {
                    "value": "es-ES",
                    "label": "next.settingsSpec.account.language.advanced.groups.0.rows.0.control.options.7.label",
                    "hint": "next.settingsSpec.account.language.advanced.groups.0.rows.0.control.options.7.hint"
                  },
                  {
                    "value": "ru-RU",
                    "label": "next.settingsSpec.account.language.advanced.groups.0.rows.0.control.options.8.label",
                    "hint": "next.settingsSpec.account.language.advanced.groups.0.rows.0.control.options.8.hint"
                  }
                ]
              }
            },
            {
              "id": "language.dateOrder",
              "scope": "profile",
              "key": "region.dateOrder",
              "label": "next.settingsSpec.account.language.advanced.groups.0.rows.1.label",
              "control": {
                "type": "select",
                "default": "auto",
                "options": [
                  {
                    "value": "auto",
                    "label": "next.settingsSpec.account.language.advanced.groups.0.rows.1.control.options.0.label"
                  },
                  {
                    "value": "dmy",
                    "label": "next.settingsSpec.account.language.advanced.groups.0.rows.1.control.options.1.label",
                    "hint": "next.settingsSpec.account.language.advanced.groups.0.rows.1.control.options.1.hint"
                  },
                  {
                    "value": "mdy",
                    "label": "next.settingsSpec.account.language.advanced.groups.0.rows.1.control.options.2.label",
                    "hint": "next.settingsSpec.account.language.advanced.groups.0.rows.1.control.options.2.hint"
                  },
                  {
                    "value": "ymd",
                    "label": "next.settingsSpec.account.language.advanced.groups.0.rows.1.control.options.3.label",
                    "hint": "next.settingsSpec.account.language.advanced.groups.0.rows.1.control.options.3.hint"
                  }
                ]
              }
            },
            {
              "id": "language.firstDay",
              "scope": "profile",
              "key": "region.firstDay",
              "label": "next.settingsSpec.account.language.advanced.groups.0.rows.2.label",
              "help": "next.settingsSpec.account.language.advanced.groups.0.rows.2.help",
              "control": {
                "type": "select",
                "default": "auto",
                "options": [
                  {
                    "value": "auto",
                    "label": "next.settingsSpec.account.language.advanced.groups.0.rows.2.control.options.0.label"
                  },
                  {
                    "value": "mon",
                    "label": "next.settingsSpec.account.language.advanced.groups.0.rows.2.control.options.1.label"
                  },
                  {
                    "value": "sun",
                    "label": "next.settingsSpec.account.language.advanced.groups.0.rows.2.control.options.2.label"
                  },
                  {
                    "value": "sat",
                    "label": "next.settingsSpec.account.language.advanced.groups.0.rows.2.control.options.3.label"
                  }
                ]
              }
            },
            {
              "id": "language.sizeUnits",
              "scope": "profile",
              "key": "region.sizeUnits",
              "label": "next.settingsSpec.account.language.advanced.groups.0.rows.3.label",
              "control": {
                "type": "segmented",
                "default": "binary",
                "options": [
                  {
                    "value": "binary",
                    "label": "next.settingsSpec.account.language.advanced.groups.0.rows.3.control.options.0.label"
                  },
                  {
                    "value": "decimal",
                    "label": "next.settingsSpec.account.language.advanced.groups.0.rows.3.control.options.1.label"
                  }
                ]
              }
            },
            {
              "id": "language.timeZone",
              "scope": "device",
              "label": "next.settingsSpec.account.language.advanced.groups.0.rows.4.label",
              "help": "next.settingsSpec.account.language.advanced.groups.0.rows.4.help",
              "control": {
                "type": "readonly",
                "mono": true
              }
            }
          ]
        }
      ]
    },
    "configOnly": []
  },
  {
    "scope": "account",
    "id": "device",
    "navGroup": "This app",
    "icon": "monitor",
    "kind": "settings",
    "title": "next.settingsSpec.account.device.title",
    "groups": [
      {
        "rows": [
          {
            "id": "device.name",
            "scope": "device",
            "label": "next.settingsSpec.account.device.groups.0.rows.0.label",
            "control": {
              "type": "text",
              "secret": false,
              "placeholder": "next.settingsSpec.account.device.groups.0.rows.0.control.placeholder",
              "default": ""
            }
          },
          {
            "id": "device.linkTv",
            "scope": "device",
            "platforms": [
              "web",
              "phone"
            ],
            "label": "next.settingsSpec.account.device.groups.0.rows.1.label",
            "help": "next.settingsSpec.account.device.groups.0.rows.1.help",
            "control": {
              "type": "link",
              "label": "next.settingsSpec.account.device.groups.0.rows.1.control.label"
            }
          },
          {
            "id": "device.receive",
            "scope": "device",
            "platforms": [
              "tv"
            ],
            "label": "next.settingsSpec.account.device.groups.0.rows.2.label",
            "help": "next.settingsSpec.account.device.groups.0.rows.2.help",
            "control": {
              "type": "toggle",
              "default": true
            }
          }
        ]
      },
      {
        "title": "next.settingsSpec.account.device.groups.1.title",
        "rows": [
          {
            "id": "device.matchFrameRate",
            "scope": "device",
            "key": "matchFrameRate",
            "platforms": [
              "tv"
            ],
            "label": "next.settingsSpec.account.device.groups.1.rows.0.label",
            "help": "next.settingsSpec.account.device.groups.1.rows.0.help",
            "control": {
              "type": "segmented",
              "default": "auto",
              "options": [
                {
                  "value": "auto",
                  "label": "next.settingsSpec.account.device.groups.1.rows.0.control.options.0.label"
                },
                {
                  "value": "off",
                  "label": "next.settingsSpec.account.device.groups.1.rows.0.control.options.1.label"
                }
              ]
            }
          },
          {
            "id": "device.matchFrameRateDesktop",
            "scope": "device",
            "key": "matchFrameRate",
            "platforms": [
              "desktop"
            ],
            "label": "next.settingsSpec.account.device.groups.1.rows.1.label",
            "help": "next.settingsSpec.account.device.groups.1.rows.1.help",
            "control": {
              "type": "segmented",
              "default": "off",
              "options": [
                {
                  "value": "auto",
                  "label": "next.settingsSpec.account.device.groups.1.rows.1.control.options.0.label"
                },
                {
                  "value": "off",
                  "label": "next.settingsSpec.account.device.groups.1.rows.1.control.options.1.label"
                }
              ]
            },
            "os": [
              "windows",
              "macos"
            ]
          },
          {
            "id": "device.matchDynamicRange",
            "scope": "device",
            "key": "matchDynamicRange",
            "platforms": [
              "tv",
              "desktop"
            ],
            "label": "next.settingsSpec.account.device.groups.1.rows.2.label",
            "control": {
              "type": "segmented",
              "default": "auto",
              "options": [
                {
                  "value": "auto",
                  "label": "next.settingsSpec.account.device.groups.1.rows.2.control.options.0.label"
                },
                {
                  "value": "off",
                  "label": "next.settingsSpec.account.device.groups.1.rows.2.control.options.1.label"
                }
              ]
            },
            "os": [
              "tvos",
              "androidtv",
              "firetv",
              "roku",
              "webos",
              "tizen",
              "windows",
              "macos"
            ]
          },
          {
            "id": "device.audioOutput",
            "scope": "device",
            "key": "device.audioOutput",
            "platforms": [
              "desktop"
            ],
            "label": "next.settingsSpec.account.device.groups.1.rows.3.label",
            "control": {
              "type": "select",
              "default": "system",
              "options": [],
              "dynamicOptions": true
            }
          },
          {
            "id": "device.exclusiveOutput",
            "scope": "device",
            "key": "device.exclusiveOutput",
            "platforms": [
              "desktop"
            ],
            "label": "next.settingsSpec.account.device.groups.1.rows.4.label",
            "help": "next.settingsSpec.account.device.groups.1.rows.4.help",
            "control": {
              "type": "toggle",
              "default": false
            },
            "children": [
              {
                "id": "device.sampleRateMatching",
                "scope": "device",
                "key": "music.sampleRateMatching",
                "platforms": [
                  "desktop"
                ],
                "enableWhen": {
                  "id": "device.exclusiveOutput",
                  "in": [
                    true
                  ]
                },
                "label": "next.settingsSpec.account.device.groups.1.rows.4.children.0.label",
                "help": "next.settingsSpec.account.device.groups.1.rows.4.children.0.help",
                "control": {
                  "type": "segmented",
                  "default": "smart",
                  "options": [
                    {
                      "value": "smart",
                      "label": "next.settingsSpec.account.device.groups.1.rows.4.children.0.control.options.0.label"
                    },
                    {
                      "value": "strict",
                      "label": "next.settingsSpec.account.device.groups.1.rows.4.children.0.control.options.1.label"
                    },
                    {
                      "value": "off",
                      "label": "next.settingsSpec.account.device.groups.1.rows.4.children.0.control.options.2.label"
                    }
                  ]
                },
                "os": [
                  "windows",
                  "linux"
                ]
              }
            ]
          },
          {
            "id": "device.sampleRateMatchingMac",
            "scope": "device",
            "key": "music.sampleRateMatching",
            "platforms": [
              "desktop"
            ],
            "label": "next.settingsSpec.account.device.groups.1.rows.5.label",
            "help": "next.settingsSpec.account.device.groups.1.rows.5.help",
            "control": {
              "type": "segmented",
              "default": "smart",
              "options": [
                {
                  "value": "smart",
                  "label": "next.settingsSpec.account.device.groups.1.rows.5.control.options.0.label"
                },
                {
                  "value": "strict",
                  "label": "next.settingsSpec.account.device.groups.1.rows.5.control.options.1.label"
                },
                {
                  "value": "off",
                  "label": "next.settingsSpec.account.device.groups.1.rows.5.control.options.2.label"
                }
              ]
            },
            "os": [
              "macos"
            ]
          },
          {
            "id": "device.passthrough",
            "scope": "device",
            "key": "passthrough",
            "platforms": [
              "tv",
              "phone",
              "desktop"
            ],
            "label": "next.settingsSpec.account.device.groups.1.rows.6.label",
            "help": "next.settingsSpec.account.device.groups.1.rows.6.help",
            "control": {
              "type": "select",
              "default": "auto",
              "options": [
                {
                  "value": "auto",
                  "label": "next.settingsSpec.account.device.groups.1.rows.6.control.options.0.label",
                  "hint": "next.settingsSpec.account.device.groups.1.rows.6.control.options.0.hint",
                  "sets": {
                    "device.passthroughFormats": {
                      "dd": "auto",
                      "ddp": "auto",
                      "truehd": "auto",
                      "dts": "auto",
                      "dtshd": "auto",
                      "dtsx": "auto"
                    },
                    "device.passthroughFormatsMac": {
                      "dd": "auto",
                      "dts": "auto"
                    }
                  }
                },
                {
                  "value": "off",
                  "label": "next.settingsSpec.account.device.groups.1.rows.6.control.options.1.label",
                  "hint": "next.settingsSpec.account.device.groups.1.rows.6.control.options.1.hint",
                  "sets": {
                    "device.passthroughFormats": {
                      "dd": "off",
                      "ddp": "off",
                      "truehd": "off",
                      "dts": "off",
                      "dtshd": "off",
                      "dtsx": "off"
                    },
                    "device.passthroughFormatsMac": {
                      "dd": "off",
                      "dts": "off"
                    }
                  }
                },
                {
                  "value": "custom",
                  "label": "next.settingsSpec.account.device.groups.1.rows.6.control.options.2.label",
                  "hint": "next.settingsSpec.account.device.groups.1.rows.6.control.options.2.hint"
                }
              ]
            },
            "children": [
              {
                "id": "device.receiver",
                "scope": "device",
                "platforms": [
                  "tv",
                  "phone",
                  "desktop"
                ],
                "enableWhen": {
                  "id": "device.passthrough",
                  "in": [
                    "auto",
                    "custom"
                  ]
                },
                "label": "next.settingsSpec.account.device.groups.1.rows.6.children.0.label",
                "control": {
                  "type": "status"
                },
                "os": [
                  "android",
                  "androidtv",
                  "firetv",
                  "roku",
                  "webos",
                  "tizen",
                  "windows",
                  "macos",
                  "linux"
                ]
              }
            ],
            "os": [
              "android",
              "androidtv",
              "firetv",
              "roku",
              "webos",
              "tizen",
              "windows",
              "macos",
              "linux"
            ]
          },
          {
            "id": "device.passthroughApple",
            "scope": "device",
            "platforms": [
              "tv",
              "phone"
            ],
            "label": "next.settingsSpec.account.device.groups.1.rows.7.label",
            "help": "next.settingsSpec.account.device.groups.1.rows.7.help",
            "control": {
              "type": "readonly"
            },
            "os": [
              "ios",
              "ipados",
              "tvos"
            ]
          },
          {
            "id": "device.lossNotices",
            "scope": "device",
            "key": "lossNotices",
            "label": "next.settingsSpec.account.device.groups.1.rows.8.label",
            "help": "next.settingsSpec.account.device.groups.1.rows.8.help",
            "control": {
              "type": "toggle",
              "default": true
            }
          }
        ]
      },
      {
        "title": "next.settingsSpec.account.device.groups.2.title",
        "rows": [
          {
            "id": "device.remote",
            "scope": "profile",
            "label": "next.settingsSpec.account.device.groups.2.rows.0.label",
            "control": {
              "type": "list",
              "itemAction": {
                "label": "next.settingsSpec.account.device.groups.2.rows.0.control.itemAction.label",
                "style": "glass"
              }
            }
          }
        ]
      },
      {
        "rows": [
          {
            "id": "device.server",
            "scope": "device",
            "label": "next.settingsSpec.account.device.groups.3.rows.0.label",
            "control": {
              "type": "link",
              "label": "next.settingsSpec.account.device.groups.3.rows.0.control.label"
            }
          }
        ]
      },
      {
        "rows": [
          {
            "id": "device.downloads",
            "scope": "device",
            "platforms": [
              "phone",
              "desktop"
            ],
            "label": "next.settingsSpec.account.device.groups.4.rows.0.label",
            "control": {
              "type": "link",
              "label": "next.settingsSpec.account.device.groups.4.rows.0.control.label"
            }
          }
        ]
      },
      {
        "title": "next.settingsSpec.account.device.groups.5.title",
        "rows": [
          {
            "id": "device.appVersion",
            "scope": "device",
            "label": "next.settingsSpec.account.device.groups.5.rows.0.label",
            "control": {
              "type": "readonly"
            }
          },
          {
            "id": "device.updates",
            "scope": "device",
            "platforms": [
              "desktop"
            ],
            "label": "next.settingsSpec.account.device.groups.5.rows.1.label",
            "help": "next.settingsSpec.account.device.groups.5.rows.1.help",
            "control": {
              "type": "status"
            }
          },
          {
            "id": "device.serverVersion",
            "scope": "device",
            "label": "next.settingsSpec.account.device.groups.5.rows.2.label",
            "control": {
              "type": "readonly"
            }
          },
          {
            "id": "device.licenses",
            "scope": "device",
            "label": "next.settingsSpec.account.device.groups.5.rows.3.label",
            "control": {
              "type": "link",
              "label": "next.settingsSpec.account.device.groups.5.rows.3.control.label"
            }
          },
          {
            "id": "device.help",
            "scope": "device",
            "platforms": [
              "web",
              "phone"
            ],
            "label": "next.settingsSpec.account.device.groups.5.rows.4.label",
            "control": {
              "type": "readonly"
            }
          },
          {
            "id": "device.sendReport",
            "scope": "device",
            "label": "next.settingsSpec.account.device.groups.5.rows.5.label",
            "help": "next.settingsSpec.account.device.groups.5.rows.5.help",
            "control": {
              "type": "action",
              "style": "glass",
              "label": "next.settingsSpec.account.device.groups.5.rows.5.control.label",
              "confirm": "next.settingsSpec.account.device.groups.5.rows.5.control.confirm"
            }
          }
        ]
      }
    ],
    "advanced": {
      "groups": [
        {
          "title": "next.settingsSpec.account.device.advanced.groups.0.title",
          "platforms": [
            "phone",
            "tv"
          ],
          "rows": [
            {
              "id": "device.hardwareDecoding",
              "scope": "device",
              "label": "next.settingsSpec.account.device.advanced.groups.0.rows.0.label",
              "control": {
                "type": "status"
              }
            },
            {
              "id": "device.screen",
              "scope": "device",
              "label": "next.settingsSpec.account.device.advanced.groups.0.rows.1.label",
              "control": {
                "type": "status"
              }
            },
            {
              "id": "device.sound",
              "scope": "device",
              "label": "next.settingsSpec.account.device.advanced.groups.0.rows.2.label",
              "control": {
                "type": "status"
              }
            }
          ]
        },
        {
          "title": "next.settingsSpec.account.device.advanced.groups.1.title",
          "platforms": [
            "tv",
            "phone",
            "desktop"
          ],
          "rows": [
            {
              "id": "device.passthroughFormats",
              "scope": "device",
              "key": "passthroughFormats",
              "platforms": [
                "tv",
                "phone",
                "desktop"
              ],
              "label": "next.settingsSpec.account.device.advanced.groups.1.rows.0.label",
              "control": {
                "type": "matrix",
                "columns": [
                  {
                    "value": "auto",
                    "label": "next.settingsSpec.account.device.advanced.groups.1.rows.0.control.columns.0.label",
                    "hint": "next.settingsSpec.account.device.advanced.groups.1.rows.0.control.columns.0.hint"
                  },
                  {
                    "value": "on",
                    "label": "next.settingsSpec.account.device.advanced.groups.1.rows.0.control.columns.1.label",
                    "hint": "next.settingsSpec.account.device.advanced.groups.1.rows.0.control.columns.1.hint"
                  },
                  {
                    "value": "off",
                    "label": "next.settingsSpec.account.device.advanced.groups.1.rows.0.control.columns.2.label",
                    "hint": "next.settingsSpec.account.device.advanced.groups.1.rows.0.control.columns.2.hint"
                  }
                ],
                "items": [
                  {
                    "id": "dd",
                    "label": "next.settingsSpec.account.device.advanced.groups.1.rows.0.control.items.0.label",
                    "default": "auto"
                  },
                  {
                    "id": "ddp",
                    "label": "next.settingsSpec.account.device.advanced.groups.1.rows.0.control.items.1.label",
                    "default": "auto"
                  },
                  {
                    "id": "truehd",
                    "label": "next.settingsSpec.account.device.advanced.groups.1.rows.0.control.items.2.label",
                    "default": "auto"
                  },
                  {
                    "id": "dts",
                    "label": "next.settingsSpec.account.device.advanced.groups.1.rows.0.control.items.3.label",
                    "default": "auto"
                  },
                  {
                    "id": "dtshd",
                    "label": "next.settingsSpec.account.device.advanced.groups.1.rows.0.control.items.4.label",
                    "default": "auto"
                  },
                  {
                    "id": "dtsx",
                    "label": "next.settingsSpec.account.device.advanced.groups.1.rows.0.control.items.5.label",
                    "default": "auto"
                  }
                ]
              },
              "os": [
                "android",
                "androidtv",
                "firetv",
                "roku",
                "webos",
                "tizen",
                "windows",
                "linux"
              ]
            }
          ]
        },
        {
          "title": "next.settingsSpec.account.device.advanced.groups.2.title",
          "platforms": [
            "desktop"
          ],
          "rows": [
            {
              "id": "device.passthroughFormatsMac",
              "scope": "device",
              "key": "passthroughFormats",
              "platforms": [
                "desktop"
              ],
              "label": "next.settingsSpec.account.device.advanced.groups.2.rows.0.label",
              "control": {
                "type": "matrix",
                "columns": [
                  {
                    "value": "auto",
                    "label": "next.settingsSpec.account.device.advanced.groups.2.rows.0.control.columns.0.label",
                    "hint": "next.settingsSpec.account.device.advanced.groups.2.rows.0.control.columns.0.hint"
                  },
                  {
                    "value": "on",
                    "label": "next.settingsSpec.account.device.advanced.groups.2.rows.0.control.columns.1.label",
                    "hint": "next.settingsSpec.account.device.advanced.groups.2.rows.0.control.columns.1.hint"
                  },
                  {
                    "value": "off",
                    "label": "next.settingsSpec.account.device.advanced.groups.2.rows.0.control.columns.2.label",
                    "hint": "next.settingsSpec.account.device.advanced.groups.2.rows.0.control.columns.2.hint"
                  }
                ],
                "items": [
                  {
                    "id": "dd",
                    "label": "next.settingsSpec.account.device.advanced.groups.2.rows.0.control.items.0.label",
                    "default": "auto"
                  },
                  {
                    "id": "dts",
                    "label": "next.settingsSpec.account.device.advanced.groups.2.rows.0.control.items.1.label",
                    "default": "auto"
                  }
                ]
              },
              "os": [
                "macos"
              ]
            }
          ]
        },
        {
          "title": "next.settingsSpec.account.device.advanced.groups.3.title",
          "rows": [
            {
              "id": "device.player",
              "scope": "device",
              "key": "playerEngine",
              "platforms": [
                "phone",
                "tv"
              ],
              "label": "next.settingsSpec.account.device.advanced.groups.3.rows.0.label",
              "control": {
                "type": "select",
                "default": "auto",
                "options": [
                  {
                    "value": "auto",
                    "label": "next.settingsSpec.account.device.advanced.groups.3.rows.0.control.options.0.label",
                    "hint": "next.settingsSpec.account.device.advanced.groups.3.rows.0.control.options.0.hint"
                  },
                  {
                    "value": "native",
                    "label": "next.settingsSpec.account.device.advanced.groups.3.rows.0.control.options.1.label",
                    "hint": "next.settingsSpec.account.device.advanced.groups.3.rows.0.control.options.1.hint"
                  },
                  {
                    "value": "mpv",
                    "label": "next.settingsSpec.account.device.advanced.groups.3.rows.0.control.options.2.label",
                    "hint": "next.settingsSpec.account.device.advanced.groups.3.rows.0.control.options.2.hint"
                  }
                ]
              },
              "os": [
                "android",
                "androidtv",
                "firetv"
              ]
            },
            {
              "id": "device.playerLink",
              "scope": "device",
              "platforms": [
                "phone",
                "tv"
              ],
              "label": "next.settingsSpec.account.device.advanced.groups.3.rows.1.label",
              "control": {
                "type": "link",
                "label": "next.settingsSpec.account.device.advanced.groups.3.rows.1.control.label",
                "to": "account/quality"
              },
              "os": [
                "android",
                "androidtv",
                "firetv"
              ]
            }
          ]
        },
        {
          "title": "next.settingsSpec.account.device.advanced.groups.4.title",
          "rows": [
            {
              "id": "device.connection",
              "scope": "device",
              "label": "next.settingsSpec.account.device.advanced.groups.4.rows.0.label",
              "control": {
                "type": "status"
              }
            },
            {
              "id": "device.serverAddress",
              "scope": "device",
              "label": "next.settingsSpec.account.device.advanced.groups.4.rows.1.label",
              "control": {
                "type": "readonly",
                "mono": true
              }
            },
            {
              "id": "device.testConnection",
              "scope": "device",
              "label": "next.settingsSpec.account.device.advanced.groups.4.rows.2.label",
              "control": {
                "type": "action",
                "style": "glass",
                "label": "next.settingsSpec.account.device.advanced.groups.4.rows.2.control.label"
              }
            },
            {
              "id": "device.allowInsecure",
              "scope": "device",
              "key": "allowInsecure",
              "label": "next.settingsSpec.account.device.advanced.groups.4.rows.3.label",
              "control": {
                "type": "segmented",
                "default": "local",
                "options": [
                  {
                    "value": "never",
                    "label": "next.settingsSpec.account.device.advanced.groups.4.rows.3.control.options.0.label"
                  },
                  {
                    "value": "local",
                    "label": "next.settingsSpec.account.device.advanced.groups.4.rows.3.control.options.1.label"
                  },
                  {
                    "value": "always",
                    "label": "next.settingsSpec.account.device.advanced.groups.4.rows.3.control.options.2.label"
                  }
                ]
              }
            }
          ]
        },
        {
          "title": "next.settingsSpec.account.device.advanced.groups.5.title",
          "rows": [
            {
              "id": "device.clearCache",
              "scope": "device",
              "label": "next.settingsSpec.account.device.advanced.groups.5.rows.0.label",
              "control": {
                "type": "action",
                "style": "glass",
                "label": "next.settingsSpec.account.device.advanced.groups.5.rows.0.control.label",
                "confirm": "next.settingsSpec.account.device.advanced.groups.5.rows.0.control.confirm"
              }
            }
          ]
        },
        {
          "title": "next.settingsSpec.account.device.advanced.groups.6.title",
          "rows": [
            {
              "id": "device.logView",
              "scope": "device",
              "label": "next.settingsSpec.account.device.advanced.groups.6.rows.0.label",
              "control": {
                "type": "action",
                "style": "glass",
                "label": "next.settingsSpec.account.device.advanced.groups.6.rows.0.control.label"
              }
            },
            {
              "id": "device.logClear",
              "scope": "device",
              "label": "next.settingsSpec.account.device.advanced.groups.6.rows.1.label",
              "control": {
                "type": "action",
                "style": "glass",
                "label": "next.settingsSpec.account.device.advanced.groups.6.rows.1.control.label",
                "confirm": "next.settingsSpec.account.device.advanced.groups.6.rows.1.control.confirm"
              }
            }
          ]
        }
      ]
    },
    "configOnly": []
  },
  {
    "scope": "server",
    "id": "general",
    "navGroup": "The server",
    "icon": "server",
    "kind": "settings",
    "title": "next.settingsSpec.server.general.title",
    "groups": [
      {
        "title": "next.settingsSpec.server.general.groups.0.title",
        "rows": [
          {
            "id": "general.name",
            "scope": "server",
            "key": "general.name",
            "label": "next.settingsSpec.server.general.groups.0.rows.0.label",
            "help": "next.settingsSpec.server.general.groups.0.rows.0.help",
            "control": {
              "type": "text",
              "secret": false,
              "placeholder": "next.settingsSpec.server.general.groups.0.rows.0.control.placeholder",
              "default": "Portico"
            }
          }
        ]
      },
      {
        "title": "next.settingsSpec.server.general.groups.1.title",
        "rows": [
          {
            "id": "general.version",
            "scope": "server",
            "label": "next.settingsSpec.server.general.groups.1.rows.0.label",
            "help": "next.settingsSpec.server.general.groups.1.rows.0.help",
            "control": {
              "type": "status"
            }
          },
          {
            "id": "general.updateChecks",
            "scope": "server",
            "key": "general.update_checks",
            "label": "next.settingsSpec.server.general.groups.1.rows.1.label",
            "control": {
              "type": "select",
              "default": "auto",
              "options": [
                {
                  "value": "auto",
                  "label": "next.settingsSpec.server.general.groups.1.rows.1.control.options.0.label",
                  "hint": "next.settingsSpec.server.general.groups.1.rows.1.control.options.0.hint"
                },
                {
                  "value": "never",
                  "label": "next.settingsSpec.server.general.groups.1.rows.1.control.options.1.label"
                },
                {
                  "value": "custom",
                  "label": "next.settingsSpec.server.general.groups.1.rows.1.control.options.2.label",
                  "hint": "next.settingsSpec.server.general.groups.1.rows.1.control.options.2.hint"
                }
              ]
            }
          },
          {
            "id": "general.checkNow",
            "scope": "server",
            "enableWhen": {
              "id": "general.updateChecks",
              "in": [
                "auto",
                "custom"
              ]
            },
            "label": "next.settingsSpec.server.general.groups.1.rows.2.label",
            "control": {
              "type": "action",
              "style": "glass",
              "label": "next.settingsSpec.server.general.groups.1.rows.2.control.label"
            }
          }
        ]
      },
      {
        "title": "next.settingsSpec.server.general.groups.2.title",
        "rows": [
          {
            "id": "general.heavyWork",
            "scope": "server",
            "key": "general.heavy_work",
            "label": "next.settingsSpec.server.general.groups.2.rows.0.label",
            "help": "next.settingsSpec.server.general.groups.2.rows.0.help",
            "control": {
              "type": "select",
              "default": "any",
              "options": [
                {
                  "value": "any",
                  "label": "next.settingsSpec.server.general.groups.2.rows.0.control.options.0.label",
                  "hint": "next.settingsSpec.server.general.groups.2.rows.0.control.options.0.hint"
                },
                {
                  "value": "overnight",
                  "label": "next.settingsSpec.server.general.groups.2.rows.0.control.options.1.label",
                  "hint": "next.settingsSpec.server.general.groups.2.rows.0.control.options.1.hint"
                },
                {
                  "value": "custom",
                  "label": "next.settingsSpec.server.general.groups.2.rows.0.control.options.2.label",
                  "hint": "next.settingsSpec.server.general.groups.2.rows.0.control.options.2.hint"
                }
              ]
            }
          },
          {
            "id": "general.backgroundSpeed",
            "scope": "server",
            "key": "general.background_speed",
            "label": "next.settingsSpec.server.general.groups.2.rows.1.label",
            "help": "next.settingsSpec.server.general.groups.2.rows.1.help",
            "control": {
              "type": "select",
              "default": "normal",
              "options": [
                {
                  "value": "gentle",
                  "label": "next.settingsSpec.server.general.groups.2.rows.1.control.options.0.label",
                  "hint": "next.settingsSpec.server.general.groups.2.rows.1.control.options.0.hint"
                },
                {
                  "value": "normal",
                  "label": "next.settingsSpec.server.general.groups.2.rows.1.control.options.1.label",
                  "hint": "next.settingsSpec.server.general.groups.2.rows.1.control.options.1.hint"
                },
                {
                  "value": "fast",
                  "label": "next.settingsSpec.server.general.groups.2.rows.1.control.options.2.label",
                  "hint": "next.settingsSpec.server.general.groups.2.rows.1.control.options.2.hint"
                }
              ]
            }
          },
          {
            "id": "general.pauseBackground",
            "scope": "server",
            "label": "next.settingsSpec.server.general.groups.2.rows.2.label",
            "help": "next.settingsSpec.server.general.groups.2.rows.2.help",
            "control": {
              "type": "action",
              "style": "glass",
              "label": "next.settingsSpec.server.general.groups.2.rows.2.control.label"
            }
          }
        ]
      },
      {
        "title": "next.settingsSpec.server.general.groups.3.title",
        "rows": [
          {
            "id": "general.message",
            "scope": "server",
            "label": "next.settingsSpec.server.general.groups.3.rows.0.label",
            "help": "next.settingsSpec.server.general.groups.3.rows.0.help",
            "control": {
              "type": "action",
              "style": "glass",
              "label": "next.settingsSpec.server.general.groups.3.rows.0.control.label"
            }
          },
          {
            "id": "general.restart",
            "scope": "server",
            "label": "next.settingsSpec.server.general.groups.3.rows.1.label",
            "control": {
              "type": "action",
              "style": "danger",
              "label": "next.settingsSpec.server.general.groups.3.rows.1.control.label",
              "confirm": "next.settingsSpec.server.general.groups.3.rows.1.control.confirm"
            }
          }
        ]
      }
    ],
    "advanced": {
      "groups": [
        {
          "title": "next.settingsSpec.server.general.advanced.groups.0.title",
          "rows": [
            {
              "id": "general.updateFeed",
              "scope": "server",
              "key": "general.update_feed_url",
              "enableWhen": {
                "id": "general.updateChecks",
                "in": [
                  "custom"
                ]
              },
              "label": "next.settingsSpec.server.general.advanced.groups.0.rows.0.label",
              "help": "next.settingsSpec.server.general.advanced.groups.0.rows.0.help",
              "control": {
                "type": "text",
                "secret": false,
                "placeholder": "next.settingsSpec.server.general.advanced.groups.0.rows.0.control.placeholder"
              }
            }
          ]
        },
        {
          "title": "next.settingsSpec.server.general.advanced.groups.1.title",
          "rows": [
            {
              "id": "general.heavyStart",
              "scope": "server",
              "key": "general.heavy_work_start",
              "enableWhen": {
                "id": "general.heavyWork",
                "in": [
                  "custom"
                ]
              },
              "label": "next.settingsSpec.server.general.advanced.groups.1.rows.0.label",
              "help": "next.settingsSpec.server.general.advanced.groups.1.rows.0.help",
              "control": {
                "type": "select",
                "default": "60",
                "options": [
                  {
                    "value": "0",
                    "label": "next.settingsSpec.server.general.advanced.groups.1.rows.0.control.options.0.label"
                  },
                  {
                    "value": "60",
                    "label": "next.settingsSpec.server.general.advanced.groups.1.rows.0.control.options.1.label"
                  },
                  {
                    "value": "120",
                    "label": "next.settingsSpec.server.general.advanced.groups.1.rows.0.control.options.2.label"
                  },
                  {
                    "value": "180",
                    "label": "next.settingsSpec.server.general.advanced.groups.1.rows.0.control.options.3.label"
                  },
                  {
                    "value": "240",
                    "label": "next.settingsSpec.server.general.advanced.groups.1.rows.0.control.options.4.label"
                  },
                  {
                    "value": "300",
                    "label": "next.settingsSpec.server.general.advanced.groups.1.rows.0.control.options.5.label"
                  },
                  {
                    "value": "360",
                    "label": "next.settingsSpec.server.general.advanced.groups.1.rows.0.control.options.6.label"
                  },
                  {
                    "value": "420",
                    "label": "next.settingsSpec.server.general.advanced.groups.1.rows.0.control.options.7.label"
                  },
                  {
                    "value": "480",
                    "label": "next.settingsSpec.server.general.advanced.groups.1.rows.0.control.options.8.label"
                  },
                  {
                    "value": "540",
                    "label": "next.settingsSpec.server.general.advanced.groups.1.rows.0.control.options.9.label"
                  },
                  {
                    "value": "600",
                    "label": "next.settingsSpec.server.general.advanced.groups.1.rows.0.control.options.10.label"
                  },
                  {
                    "value": "660",
                    "label": "next.settingsSpec.server.general.advanced.groups.1.rows.0.control.options.11.label"
                  },
                  {
                    "value": "720",
                    "label": "next.settingsSpec.server.general.advanced.groups.1.rows.0.control.options.12.label"
                  },
                  {
                    "value": "780",
                    "label": "next.settingsSpec.server.general.advanced.groups.1.rows.0.control.options.13.label"
                  },
                  {
                    "value": "840",
                    "label": "next.settingsSpec.server.general.advanced.groups.1.rows.0.control.options.14.label"
                  },
                  {
                    "value": "900",
                    "label": "next.settingsSpec.server.general.advanced.groups.1.rows.0.control.options.15.label"
                  },
                  {
                    "value": "960",
                    "label": "next.settingsSpec.server.general.advanced.groups.1.rows.0.control.options.16.label"
                  },
                  {
                    "value": "1020",
                    "label": "next.settingsSpec.server.general.advanced.groups.1.rows.0.control.options.17.label"
                  },
                  {
                    "value": "1080",
                    "label": "next.settingsSpec.server.general.advanced.groups.1.rows.0.control.options.18.label"
                  },
                  {
                    "value": "1140",
                    "label": "next.settingsSpec.server.general.advanced.groups.1.rows.0.control.options.19.label"
                  },
                  {
                    "value": "1200",
                    "label": "next.settingsSpec.server.general.advanced.groups.1.rows.0.control.options.20.label"
                  },
                  {
                    "value": "1260",
                    "label": "next.settingsSpec.server.general.advanced.groups.1.rows.0.control.options.21.label"
                  },
                  {
                    "value": "1320",
                    "label": "next.settingsSpec.server.general.advanced.groups.1.rows.0.control.options.22.label"
                  },
                  {
                    "value": "1380",
                    "label": "next.settingsSpec.server.general.advanced.groups.1.rows.0.control.options.23.label"
                  }
                ]
              }
            },
            {
              "id": "general.heavyLength",
              "scope": "server",
              "key": "general.heavy_work_hours",
              "enableWhen": {
                "id": "general.heavyWork",
                "in": [
                  "custom"
                ]
              },
              "label": "next.settingsSpec.server.general.advanced.groups.1.rows.1.label",
              "help": "next.settingsSpec.server.general.advanced.groups.1.rows.1.help",
              "control": {
                "type": "select",
                "default": "5",
                "options": [
                  {
                    "value": "1",
                    "label": "next.settingsSpec.server.general.advanced.groups.1.rows.1.control.options.0.label"
                  },
                  {
                    "value": "2",
                    "label": "next.settingsSpec.server.general.advanced.groups.1.rows.1.control.options.1.label"
                  },
                  {
                    "value": "3",
                    "label": "next.settingsSpec.server.general.advanced.groups.1.rows.1.control.options.2.label"
                  },
                  {
                    "value": "4",
                    "label": "next.settingsSpec.server.general.advanced.groups.1.rows.1.control.options.3.label"
                  },
                  {
                    "value": "5",
                    "label": "next.settingsSpec.server.general.advanced.groups.1.rows.1.control.options.4.label"
                  },
                  {
                    "value": "6",
                    "label": "next.settingsSpec.server.general.advanced.groups.1.rows.1.control.options.5.label"
                  },
                  {
                    "value": "8",
                    "label": "next.settingsSpec.server.general.advanced.groups.1.rows.1.control.options.6.label"
                  },
                  {
                    "value": "10",
                    "label": "next.settingsSpec.server.general.advanced.groups.1.rows.1.control.options.7.label"
                  },
                  {
                    "value": "12",
                    "label": "next.settingsSpec.server.general.advanced.groups.1.rows.1.control.options.8.label"
                  }
                ]
              }
            }
          ]
        },
        {
          "title": "next.settingsSpec.server.general.advanced.groups.2.title",
          "rows": [
            {
              "id": "general.heavyDays",
              "scope": "server",
              "key": "general.heavy_work_days",
              "enableWhen": {
                "id": "general.heavyWork",
                "in": [
                  "custom"
                ]
              },
              "label": "next.settingsSpec.server.general.advanced.groups.2.rows.0.label",
              "control": {
                "type": "matrix",
                "columns": [
                  {
                    "value": "runs",
                    "label": "next.settingsSpec.server.general.advanced.groups.2.rows.0.control.columns.0.label"
                  },
                  {
                    "value": "skips",
                    "label": "next.settingsSpec.server.general.advanced.groups.2.rows.0.control.columns.1.label"
                  }
                ],
                "items": [
                  {
                    "id": "monday",
                    "label": "next.settingsSpec.server.general.advanced.groups.2.rows.0.control.items.0.label",
                    "default": "runs"
                  },
                  {
                    "id": "tuesday",
                    "label": "next.settingsSpec.server.general.advanced.groups.2.rows.0.control.items.1.label",
                    "default": "runs"
                  },
                  {
                    "id": "wednesday",
                    "label": "next.settingsSpec.server.general.advanced.groups.2.rows.0.control.items.2.label",
                    "default": "runs"
                  },
                  {
                    "id": "thursday",
                    "label": "next.settingsSpec.server.general.advanced.groups.2.rows.0.control.items.3.label",
                    "default": "runs"
                  },
                  {
                    "id": "friday",
                    "label": "next.settingsSpec.server.general.advanced.groups.2.rows.0.control.items.4.label",
                    "default": "runs"
                  },
                  {
                    "id": "saturday",
                    "label": "next.settingsSpec.server.general.advanced.groups.2.rows.0.control.items.5.label",
                    "default": "runs"
                  },
                  {
                    "id": "sunday",
                    "label": "next.settingsSpec.server.general.advanced.groups.2.rows.0.control.items.6.label",
                    "default": "runs"
                  }
                ]
              }
            }
          ]
        },
        {
          "title": "next.settingsSpec.server.general.advanced.groups.3.title",
          "rows": [
            {
              "id": "general.alertStorageWarn",
              "scope": "server",
              "key": "alerts.storage_warning_percent",
              "label": "next.settingsSpec.server.general.advanced.groups.3.rows.0.label",
              "help": "next.settingsSpec.server.general.advanced.groups.3.rows.0.help",
              "control": {
                "type": "select",
                "default": "10",
                "options": [
                  {
                    "value": "5",
                    "label": "next.settingsSpec.server.general.advanced.groups.3.rows.0.control.options.0.label"
                  },
                  {
                    "value": "10",
                    "label": "next.settingsSpec.server.general.advanced.groups.3.rows.0.control.options.1.label"
                  },
                  {
                    "value": "15",
                    "label": "next.settingsSpec.server.general.advanced.groups.3.rows.0.control.options.2.label"
                  },
                  {
                    "value": "20",
                    "label": "next.settingsSpec.server.general.advanced.groups.3.rows.0.control.options.3.label"
                  },
                  {
                    "value": "30",
                    "label": "next.settingsSpec.server.general.advanced.groups.3.rows.0.control.options.4.label"
                  }
                ]
              }
            },
            {
              "id": "general.alertStorageCritical",
              "scope": "server",
              "key": "alerts.storage_critical_percent",
              "label": "next.settingsSpec.server.general.advanced.groups.3.rows.1.label",
              "help": "next.settingsSpec.server.general.advanced.groups.3.rows.1.help",
              "control": {
                "type": "select",
                "default": "5",
                "options": [
                  {
                    "value": "2",
                    "label": "next.settingsSpec.server.general.advanced.groups.3.rows.1.control.options.0.label"
                  },
                  {
                    "value": "3",
                    "label": "next.settingsSpec.server.general.advanced.groups.3.rows.1.control.options.1.label"
                  },
                  {
                    "value": "5",
                    "label": "next.settingsSpec.server.general.advanced.groups.3.rows.1.control.options.2.label"
                  },
                  {
                    "value": "8",
                    "label": "next.settingsSpec.server.general.advanced.groups.3.rows.1.control.options.3.label"
                  },
                  {
                    "value": "10",
                    "label": "next.settingsSpec.server.general.advanced.groups.3.rows.1.control.options.4.label"
                  }
                ]
              }
            },
            {
              "id": "general.alertCertificateDays",
              "scope": "server",
              "key": "alerts.certificate_warning_days",
              "label": "next.settingsSpec.server.general.advanced.groups.3.rows.2.label",
              "help": "next.settingsSpec.server.general.advanced.groups.3.rows.2.help",
              "control": {
                "type": "select",
                "default": "14",
                "options": [
                  {
                    "value": "7",
                    "label": "next.settingsSpec.server.general.advanced.groups.3.rows.2.control.options.0.label"
                  },
                  {
                    "value": "14",
                    "label": "next.settingsSpec.server.general.advanced.groups.3.rows.2.control.options.1.label"
                  },
                  {
                    "value": "30",
                    "label": "next.settingsSpec.server.general.advanced.groups.3.rows.2.control.options.2.label"
                  },
                  {
                    "value": "60",
                    "label": "next.settingsSpec.server.general.advanced.groups.3.rows.2.control.options.3.label"
                  }
                ]
              }
            }
          ]
        },
        {
          "title": "next.settingsSpec.server.general.advanced.groups.4.title",
          "rows": [
            {
              "id": "general.noticeAudience",
              "scope": "server",
              "key": "integrations.notice_audience",
              "label": "next.settingsSpec.server.general.advanced.groups.4.rows.0.label",
              "help": "next.settingsSpec.server.general.advanced.groups.4.rows.0.help",
              "control": {
                "type": "matrix",
                "columns": [
                  {
                    "value": "both",
                    "label": "next.settingsSpec.server.general.advanced.groups.4.rows.0.control.columns.0.label"
                  },
                  {
                    "value": "owner",
                    "label": "next.settingsSpec.server.general.advanced.groups.4.rows.0.control.columns.1.label"
                  },
                  {
                    "value": "off",
                    "label": "next.settingsSpec.server.general.advanced.groups.4.rows.0.control.columns.2.label"
                  }
                ],
                "items": [
                  {
                    "id": "scan",
                    "label": "next.settingsSpec.server.general.advanced.groups.4.rows.0.control.items.0.label",
                    "default": "both"
                  },
                  {
                    "id": "storage",
                    "label": "next.settingsSpec.server.general.advanced.groups.4.rows.0.control.items.1.label",
                    "default": "both"
                  },
                  {
                    "id": "certificate",
                    "label": "next.settingsSpec.server.general.advanced.groups.4.rows.0.control.items.2.label",
                    "default": "both"
                  },
                  {
                    "id": "recording",
                    "label": "next.settingsSpec.server.general.advanced.groups.4.rows.0.control.items.3.label",
                    "default": "both"
                  },
                  {
                    "id": "feedback",
                    "label": "next.settingsSpec.server.general.advanced.groups.4.rows.0.control.items.4.label",
                    "default": "both"
                  }
                ]
              }
            }
          ]
        },
        {
          "title": "next.settingsSpec.server.general.advanced.groups.5.title",
          "rows": [
            {
              "id": "general.configFile",
              "scope": "server",
              "label": "next.settingsSpec.server.general.advanced.groups.5.rows.0.label",
              "help": "next.settingsSpec.server.general.advanced.groups.5.rows.0.help",
              "control": {
                "type": "readonly",
                "mono": true
              }
            },
            {
              "id": "general.serverId",
              "scope": "server",
              "label": "next.settingsSpec.server.general.advanced.groups.5.rows.1.label",
              "help": "next.settingsSpec.server.general.advanced.groups.5.rows.1.help",
              "control": {
                "type": "readonly",
                "mono": true
              }
            }
          ]
        }
      ]
    },
    "configOnly": [
      {
        "key": "updates.channel",
        "label": "next.settingsSpec.server.general.configOnly.0.label"
      }
    ]
  },
  {
    "scope": "server",
    "id": "dashboard",
    "navGroup": "Status",
    "icon": "pulse",
    "kind": "dashboard",
    "title": "next.settingsSpec.server.dashboard.title",
    "groups": [],
    "configOnly": []
  },
  {
    "scope": "server",
    "id": "history",
    "navGroup": "Status",
    "icon": "clock",
    "kind": "page",
    "title": "next.settingsSpec.server.history.title",
    "groups": [
      {
        "title": "next.settingsSpec.server.history.groups.0.title",
        "rows": [
          {
            "id": "history.plays",
            "scope": "server",
            "label": "next.settingsSpec.server.history.groups.0.rows.0.label",
            "control": {
              "type": "table",
              "height": 640,
              "empty": "next.settingsSpec.server.history.groups.0.rows.0.control.empty",
              "search": "next.settingsSpec.server.history.groups.0.rows.0.control.search",
              "noun": [
                "next.settingsSpec.server.history.groups.0.rows.0.control.noun.0",
                "next.settingsSpec.server.history.groups.0.rows.0.control.noun.1"
              ],
              "filters": [
                {
                  "id": "media",
                  "label": "next.settingsSpec.server.history.groups.0.rows.0.control.filters.0.label",
                  "options": [
                    {
                      "value": "all",
                      "label": "next.settingsSpec.server.history.groups.0.rows.0.control.filters.0.options.0.label"
                    },
                    {
                      "value": "movies",
                      "label": "next.settingsSpec.server.history.groups.0.rows.0.control.filters.0.options.1.label"
                    },
                    {
                      "value": "tv",
                      "label": "next.settingsSpec.server.history.groups.0.rows.0.control.filters.0.options.2.label"
                    },
                    {
                      "value": "music",
                      "label": "next.settingsSpec.server.history.groups.0.rows.0.control.filters.0.options.3.label"
                    },
                    {
                      "value": "audiobooks",
                      "label": "next.settingsSpec.server.history.groups.0.rows.0.control.filters.0.options.4.label"
                    },
                    {
                      "value": "livetv",
                      "label": "next.settingsSpec.server.history.groups.0.rows.0.control.filters.0.options.5.label"
                    }
                  ]
                },
                {
                  "id": "person",
                  "label": "next.settingsSpec.server.history.groups.0.rows.0.control.filters.1.label",
                  "options": [
                    {
                      "value": "all",
                      "label": "next.settingsSpec.server.history.groups.0.rows.0.control.filters.1.options.0.label"
                    }
                  ],
                  "dynamicOptions": true
                },
                {
                  "id": "range",
                  "label": "next.settingsSpec.server.history.groups.0.rows.0.control.filters.2.label",
                  "options": [
                    {
                      "value": "today",
                      "label": "next.settingsSpec.server.history.groups.0.rows.0.control.filters.2.options.0.label"
                    },
                    {
                      "value": "7d",
                      "label": "next.settingsSpec.server.history.groups.0.rows.0.control.filters.2.options.1.label"
                    },
                    {
                      "value": "30d",
                      "label": "next.settingsSpec.server.history.groups.0.rows.0.control.filters.2.options.2.label"
                    },
                    {
                      "value": "all",
                      "label": "next.settingsSpec.server.history.groups.0.rows.0.control.filters.2.options.3.label"
                    }
                  ]
                }
              ],
              "columns": [
                {
                  "id": "title",
                  "label": "next.settingsSpec.server.history.groups.0.rows.0.control.columns.0.label",
                  "kind": "main",
                  "sort": true,
                  "width": "27%"
                },
                {
                  "id": "person",
                  "label": "next.settingsSpec.server.history.groups.0.rows.0.control.columns.1.label",
                  "kind": "main",
                  "sort": true,
                  "width": "15%"
                },
                {
                  "id": "player",
                  "label": "next.settingsSpec.server.history.groups.0.rows.0.control.columns.2.label",
                  "kind": "text",
                  "sort": true,
                  "width": "18%"
                },
                {
                  "id": "played",
                  "label": "next.settingsSpec.server.history.groups.0.rows.0.control.columns.3.label",
                  "kind": "text",
                  "sort": true,
                  "width": "17%"
                },
                {
                  "id": "for",
                  "label": "next.settingsSpec.server.history.groups.0.rows.0.control.columns.4.label",
                  "kind": "text",
                  "sort": true,
                  "width": "12%"
                },
                {
                  "id": "where",
                  "label": "next.settingsSpec.server.history.groups.0.rows.0.control.columns.5.label",
                  "kind": "badge",
                  "sort": true,
                  "width": "11%"
                }
              ],
              "sort": {
                "by": "played",
                "dir": "desc"
              }
            }
          }
        ]
      }
    ],
    "advanced": {
      "groups": [
        {
          "title": "next.settingsSpec.server.history.advanced.groups.0.title",
          "rows": [
            {
              "id": "history.keepHistory",
              "scope": "server",
              "key": "history.keep_play_history",
              "label": "next.settingsSpec.server.history.advanced.groups.0.rows.0.label",
              "help": "next.settingsSpec.server.history.advanced.groups.0.rows.0.help",
              "control": {
                "type": "select",
                "default": "forever",
                "options": [
                  {
                    "value": "forever",
                    "label": "next.settingsSpec.server.history.advanced.groups.0.rows.0.control.options.0.label"
                  },
                  {
                    "value": "3y",
                    "label": "next.settingsSpec.server.history.advanced.groups.0.rows.0.control.options.1.label"
                  },
                  {
                    "value": "1y",
                    "label": "next.settingsSpec.server.history.advanced.groups.0.rows.0.control.options.2.label"
                  },
                  {
                    "value": "180d",
                    "label": "next.settingsSpec.server.history.advanced.groups.0.rows.0.control.options.3.label"
                  },
                  {
                    "value": "90d",
                    "label": "next.settingsSpec.server.history.advanced.groups.0.rows.0.control.options.4.label"
                  },
                  {
                    "value": "30d",
                    "label": "next.settingsSpec.server.history.advanced.groups.0.rows.0.control.options.5.label"
                  }
                ]
              }
            }
          ]
        }
      ]
    },
    "configOnly": []
  },
  {
    "scope": "server",
    "id": "libraries",
    "navGroup": "Your media",
    "icon": "library",
    "kind": "page",
    "title": "next.settingsSpec.server.libraries.title",
    "groups": [
      {
        "title": "next.settingsSpec.server.libraries.groups.0.title",
        "rows": [
          {
            "id": "libraries.list",
            "scope": "server",
            "key": "libraries.order",
            "label": "next.settingsSpec.server.libraries.groups.0.rows.0.label",
            "help": "next.settingsSpec.server.libraries.groups.0.rows.0.help",
            "control": {
              "type": "list",
              "drag": true,
              "addPanel": "newLibrary",
              "addLabel": "next.settingsSpec.server.libraries.groups.0.rows.0.control.addLabel",
              "itemActions": [
                {
                  "label": "next.settingsSpec.server.libraries.groups.0.rows.0.control.itemActions.0.label",
                  "style": "glass"
                },
                {
                  "label": "next.settingsSpec.server.libraries.groups.0.rows.0.control.itemActions.1.label",
                  "style": "glass",
                  "panel": "library"
                }
              ]
            }
          }
        ]
      },
      {
        "title": "next.settingsSpec.server.libraries.groups.1.title",
        "rows": [
          {
            "id": "storage.missingRule",
            "scope": "server",
            "key": "storage.remove_missing_titles",
            "label": "next.settingsSpec.server.libraries.groups.1.rows.0.label",
            "help": "next.settingsSpec.server.libraries.groups.1.rows.0.help",
            "control": {
              "type": "select",
              "default": "30d",
              "options": [
                {
                  "value": "now",
                  "label": "next.settingsSpec.server.libraries.groups.1.rows.0.control.options.0.label"
                },
                {
                  "value": "7d",
                  "label": "next.settingsSpec.server.libraries.groups.1.rows.0.control.options.1.label"
                },
                {
                  "value": "30d",
                  "label": "next.settingsSpec.server.libraries.groups.1.rows.0.control.options.2.label"
                },
                {
                  "value": "90d",
                  "label": "next.settingsSpec.server.libraries.groups.1.rows.0.control.options.3.label"
                },
                {
                  "value": "never",
                  "label": "next.settingsSpec.server.libraries.groups.1.rows.0.control.options.4.label"
                },
                {
                  "value": "custom",
                  "label": "next.settingsSpec.server.libraries.groups.1.rows.0.control.options.5.label"
                }
              ]
            }
          },
          {
            "id": "storage.missingNow",
            "scope": "server",
            "label": "next.settingsSpec.server.libraries.groups.1.rows.1.label",
            "control": {
              "type": "list"
            }
          },
          {
            "id": "storage.removeMissing",
            "scope": "server",
            "label": "next.settingsSpec.server.libraries.groups.1.rows.2.label",
            "control": {
              "type": "action",
              "style": "danger",
              "label": "next.settingsSpec.server.libraries.groups.1.rows.2.control.label",
              "confirm": "next.settingsSpec.server.libraries.groups.1.rows.2.control.confirm"
            }
          }
        ]
      }
    ],
    "advanced": {
      "groups": [
        {
          "title": "next.settingsSpec.server.libraries.advanced.groups.0.title",
          "rows": [
            {
              "id": "storage.missingDaysExact",
              "scope": "server",
              "key": "storage.remove_missing_titles_days",
              "enableWhen": {
                "id": "storage.missingRule",
                "in": [
                  "custom"
                ]
              },
              "label": "next.settingsSpec.server.libraries.advanced.groups.0.rows.0.label",
              "help": "next.settingsSpec.server.libraries.advanced.groups.0.rows.0.help",
              "control": {
                "type": "number",
                "default": 30,
                "unit": "next.settingsSpec.server.libraries.advanced.groups.0.rows.0.control.unit",
                "min": 1,
                "max": 3650,
                "step": 1
              }
            }
          ]
        }
      ]
    },
    "panels": {
      "library": {
        "tabs": [
          {
            "id": "general",
            "title": "next.settingsSpec.server.libraries.panels.library.tabs.0.title",
            "groups": [
              {
                "rows": [
                  {
                    "id": "libraries.name",
                    "scope": "server",
                    "key": "library.name",
                    "noChanged": true,
                    "label": "next.settingsSpec.server.libraries.panels.library.tabs.0.groups.0.rows.0.label",
                    "control": {
                      "type": "text",
                      "placeholder": "next.settingsSpec.server.libraries.panels.library.tabs.0.groups.0.rows.0.control.placeholder",
                      "default": ""
                    }
                  },
                  {
                    "id": "libraries.type",
                    "scope": "server",
                    "key": "library.kind",
                    "label": "next.settingsSpec.server.libraries.panels.library.tabs.0.groups.0.rows.1.label",
                    "control": {
                      "type": "readonly"
                    }
                  },
                  {
                    "id": "libraries.access",
                    "scope": "server",
                    "key": "library.audience",
                    "label": "next.settingsSpec.server.libraries.panels.library.tabs.0.groups.0.rows.2.label",
                    "help": "next.settingsSpec.server.libraries.panels.library.tabs.0.groups.0.rows.2.help",
                    "control": {
                      "type": "readonly"
                    }
                  }
                ]
              },
              {
                "rows": [
                  {
                    "id": "libraries.collections",
                    "scope": "server",
                    "key": "library.group_collections",
                    "label": "next.settingsSpec.server.libraries.panels.library.tabs.0.groups.1.rows.0.label",
                    "help": "next.settingsSpec.server.libraries.panels.library.tabs.0.groups.1.rows.0.help",
                    "control": {
                      "type": "toggle",
                      "default": true
                    }
                  }
                ]
              },
              {
                "title": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.5.title",
                "rows": [
                  {
                    "id": "libraries.cwWeeks",
                    "scope": "server",
                    "key": "library.continue.weeks",
                    "label": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.5.rows.0.label",
                    "control": {
                      "type": "select",
                      "default": 16,
                      "options": [
                        {
                          "value": 4,
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.5.rows.0.control.options.0.label"
                        },
                        {
                          "value": 8,
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.5.rows.0.control.options.1.label"
                        },
                        {
                          "value": 16,
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.5.rows.0.control.options.2.label"
                        },
                        {
                          "value": 26,
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.5.rows.0.control.options.3.label"
                        },
                        {
                          "value": 52,
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.5.rows.0.control.options.4.label"
                        },
                        {
                          "value": 104,
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.5.rows.0.control.options.5.label"
                        }
                      ]
                    }
                  },
                  {
                    "id": "libraries.cwMax",
                    "scope": "server",
                    "key": "library.continue.max_items",
                    "label": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.5.rows.1.label",
                    "control": {
                      "type": "select",
                      "default": 40,
                      "options": [
                        {
                          "value": 20,
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.5.rows.1.control.options.0.label"
                        },
                        {
                          "value": 40,
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.5.rows.1.control.options.1.label"
                        },
                        {
                          "value": 100,
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.5.rows.1.control.options.2.label"
                        },
                        {
                          "value": 200,
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.5.rows.1.control.options.3.label"
                        }
                      ]
                    }
                  },
                  {
                    "id": "libraries.cwPremieres",
                    "scope": "server",
                    "key": "library.continue.include_premieres",
                    "label": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.5.rows.2.label",
                    "help": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.5.rows.2.help",
                    "control": {
                      "type": "toggle",
                      "default": true
                    }
                  },
                  {
                    "id": "libraries.cwWatched",
                    "scope": "server",
                    "key": "library.continue.played_percent",
                    "label": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.5.rows.3.label",
                    "help": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.5.rows.3.help",
                    "control": {
                      "type": "select",
                      "default": "95",
                      "options": [
                        {
                          "value": "80",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.5.rows.3.control.options.0.label"
                        },
                        {
                          "value": "85",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.5.rows.3.control.options.1.label"
                        },
                        {
                          "value": "90",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.5.rows.3.control.options.2.label"
                        },
                        {
                          "value": "95",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.5.rows.3.control.options.3.label"
                        },
                        {
                          "value": "100",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.5.rows.3.control.options.4.label"
                        }
                      ]
                    }
                  },
                  {
                    "id": "libraries.cwRule",
                    "scope": "server",
                    "key": "library.continue.completion",
                    "label": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.5.rows.4.label",
                    "help": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.5.rows.4.help",
                    "control": {
                      "type": "select",
                      "default": "earliest",
                      "options": [
                        {
                          "value": "earliest",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.5.rows.4.control.options.0.label"
                        },
                        {
                          "value": "threshold",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.5.rows.4.control.options.1.label"
                        },
                        {
                          "value": "credits",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.5.rows.4.control.options.2.label",
                          "hint": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.5.rows.4.control.options.2.hint"
                        }
                      ]
                    }
                  }
                ]
              },
              {
                "rows": [
                  {
                    "id": "libraries.remove",
                    "scope": "server",
                    "key": "library.remove",
                    "label": "next.settingsSpec.server.libraries.panels.library.tabs.0.groups.2.rows.0.label",
                    "help": "next.settingsSpec.server.libraries.panels.library.tabs.0.groups.2.rows.0.help",
                    "control": {
                      "type": "action",
                      "style": "danger",
                      "label": "next.settingsSpec.server.libraries.panels.library.tabs.0.groups.2.rows.0.control.label",
                      "confirm": "next.settingsSpec.server.libraries.panels.library.tabs.0.groups.2.rows.0.control.confirm"
                    }
                  }
                ]
              }
            ]
          },
          {
            "id": "folders",
            "title": "next.settingsSpec.server.libraries.panels.library.tabs.1.title",
            "groups": [
              {
                "title": "next.settingsSpec.server.libraries.panels.library.tabs.1.groups.0.title",
                "rows": [
                  {
                    "id": "libraries.folders",
                    "scope": "server",
                    "key": "library.folders",
                    "noChanged": true,
                    "label": "next.settingsSpec.server.libraries.panels.library.tabs.1.groups.0.rows.0.label",
                    "control": {
                      "type": "list",
                      "addLabel": "next.settingsSpec.server.libraries.panels.library.tabs.1.groups.0.rows.0.control.addLabel",
                      "itemActions": [
                        {
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.1.groups.0.rows.0.control.itemActions.0.label",
                          "style": "glass"
                        },
                        {
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.1.groups.0.rows.0.control.itemActions.1.label",
                          "style": "danger",
                          "remove": true,
                          "confirm": "next.settingsSpec.server.libraries.panels.library.tabs.1.groups.0.rows.0.control.itemActions.1.confirm"
                        }
                      ],
                      "addMenu": [
                        {
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.1.groups.0.rows.0.control.addMenu.0.label",
                          "icon": "folder",
                          "do": "folder"
                        },
                        {
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.1.groups.0.rows.0.control.addMenu.4.label",
                          "icon": "plus",
                          "do": "link:server/remote-sources"
                        }
                      ]
                    }
                  }
                ]
              },
              {
                "title": "next.settingsSpec.server.libraries.panels.library.tabs.1.groups.1.title",
                "rows": [
                  {
                    "id": "libraries.folderPath",
                    "scope": "server",
                    "key": "library.folder.path",
                    "noChanged": true,
                    "label": "next.settingsSpec.server.libraries.panels.library.tabs.1.groups.1.rows.0.label",
                    "help": "next.settingsSpec.server.libraries.panels.library.tabs.1.groups.1.rows.0.help",
                    "control": {
                      "type": "folder"
                    }
                  },
                  {
                    "id": "libraries.folderName",
                    "scope": "server",
                    "key": "library.folder.name",
                    "label": "next.settingsSpec.server.libraries.panels.library.tabs.1.groups.1.rows.1.label",
                    "control": {
                      "type": "text",
                      "placeholder": "next.settingsSpec.server.libraries.panels.library.tabs.1.groups.1.rows.1.control.placeholder"
                    }
                  },
                  {
                    "id": "libraries.location",
                    "scope": "server",
                    "key": "library.folder.location",
                    "label": "next.settingsSpec.server.libraries.panels.library.tabs.1.groups.1.rows.2.label",
                    "control": {
                      "type": "select",
                      "default": "local",
                      "options": [
                        {
                          "value": "local",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.1.groups.1.rows.2.control.options.0.label",
                          "hint": "next.settingsSpec.server.libraries.panels.library.tabs.1.groups.1.rows.2.control.options.0.hint"
                        },
                        {
                          "value": "network",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.1.groups.1.rows.2.control.options.1.label",
                          "hint": "next.settingsSpec.server.libraries.panels.library.tabs.1.groups.1.rows.2.control.options.1.hint"
                        }
                      ]
                    }
                  },
                  {
                    "id": "libraries.rescan",
                    "scope": "server",
                    "key": "library.folder.rescan",
                    "label": "next.settingsSpec.server.libraries.panels.library.tabs.1.groups.1.rows.3.label",
                    "control": {
                      "type": "select",
                      "default": "1h",
                      "options": [
                        {
                          "value": "15m",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.1.groups.1.rows.3.control.options.0.label"
                        },
                        {
                          "value": "1h",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.1.groups.1.rows.3.control.options.1.label"
                        },
                        {
                          "value": "6h",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.1.groups.1.rows.3.control.options.2.label"
                        },
                        {
                          "value": "1d",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.1.groups.1.rows.3.control.options.3.label"
                        },
                        {
                          "value": "never",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.1.groups.1.rows.3.control.options.4.label"
                        }
                      ]
                    }
                  }
                ]
              }
            ]
          },
          {
            "id": "metadata",
            "title": "next.settingsSpec.server.libraries.panels.library.tabs.2.title",
            "groups": [
              {
                "rows": [
                  {
                    "id": "libraries.titlesFrom",
                    "scope": "server",
                    "key": "library.metadata_provider",
                    "noChanged": true,
                    "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.0.label",
                    "help": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.0.help",
                    "control": {
                      "type": "select",
                      "default": "tmdb",
                      "options": [
                        {
                          "value": "tmdb",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.0.control.options.0.label",
                          "hint": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.0.control.options.0.hint"
                        },
                        {
                          "value": "tvdb",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.0.control.options.1.label",
                          "hint": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.0.control.options.1.hint"
                        },
                        {
                          "value": "anilist",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.0.control.options.2.label",
                          "hint": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.0.control.options.2.hint"
                        },
                        {
                          "value": "musicbrainz",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.0.control.options.3.label",
                          "hint": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.0.control.options.3.hint"
                        },
                        {
                          "value": "local",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.0.control.options.4.label",
                          "hint": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.0.control.options.4.hint"
                        }
                      ]
                    }
                  },
                  {
                    "id": "libraries.language",
                    "scope": "server",
                    "key": "library.language",
                    "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.1.label",
                    "help": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.1.help",
                    "control": {
                      "type": "select",
                      "default": "en",
                      "options": [
                        {
                          "value": "en",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.1.control.options.0.label"
                        },
                        {
                          "value": "ar",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.1.control.options.1.label"
                        },
                        {
                          "value": "zh",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.1.control.options.2.label"
                        },
                        {
                          "value": "cs",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.1.control.options.3.label"
                        },
                        {
                          "value": "da",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.1.control.options.4.label"
                        },
                        {
                          "value": "nl",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.1.control.options.5.label"
                        },
                        {
                          "value": "fi",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.1.control.options.6.label"
                        },
                        {
                          "value": "fr",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.1.control.options.7.label"
                        },
                        {
                          "value": "de",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.1.control.options.8.label"
                        },
                        {
                          "value": "el",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.1.control.options.9.label"
                        },
                        {
                          "value": "he",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.1.control.options.10.label"
                        },
                        {
                          "value": "hi",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.1.control.options.11.label"
                        },
                        {
                          "value": "hu",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.1.control.options.12.label"
                        },
                        {
                          "value": "it",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.1.control.options.13.label"
                        },
                        {
                          "value": "ja",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.1.control.options.14.label"
                        },
                        {
                          "value": "ko",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.1.control.options.15.label"
                        },
                        {
                          "value": "no",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.1.control.options.16.label"
                        },
                        {
                          "value": "pl",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.1.control.options.17.label"
                        },
                        {
                          "value": "pt",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.1.control.options.18.label"
                        },
                        {
                          "value": "ro",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.1.control.options.19.label"
                        },
                        {
                          "value": "ru",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.1.control.options.20.label"
                        },
                        {
                          "value": "sk",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.1.control.options.21.label"
                        },
                        {
                          "value": "es",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.1.control.options.22.label"
                        },
                        {
                          "value": "sv",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.1.control.options.23.label"
                        },
                        {
                          "value": "th",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.1.control.options.24.label"
                        },
                        {
                          "value": "tr",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.1.control.options.25.label"
                        },
                        {
                          "value": "uk",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.1.control.options.26.label"
                        },
                        {
                          "value": "vi",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.1.control.options.27.label"
                        }
                      ]
                    }
                  },
                  {
                    "id": "libraries.region",
                    "scope": "server",
                    "key": "library.region",
                    "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.label",
                    "help": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.help",
                    "control": {
                      "type": "select",
                      "default": "US",
                      "options": [
                        {
                          "value": "US",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.control.options.0.label",
                          "hint": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.control.options.0.hint"
                        },
                        {
                          "value": "CA",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.control.options.1.label",
                          "hint": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.control.options.1.hint"
                        },
                        {
                          "value": "GB",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.control.options.2.label",
                          "hint": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.control.options.2.hint"
                        },
                        {
                          "value": "AU",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.control.options.3.label",
                          "hint": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.control.options.3.hint"
                        },
                        {
                          "value": "NZ",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.control.options.4.label",
                          "hint": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.control.options.4.hint"
                        },
                        {
                          "value": "IE",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.control.options.5.label",
                          "hint": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.control.options.5.hint"
                        },
                        {
                          "value": "FR",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.control.options.6.label",
                          "hint": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.control.options.6.hint"
                        },
                        {
                          "value": "DE",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.control.options.7.label",
                          "hint": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.control.options.7.hint"
                        },
                        {
                          "value": "ES",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.control.options.8.label",
                          "hint": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.control.options.8.hint"
                        },
                        {
                          "value": "IT",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.control.options.9.label",
                          "hint": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.control.options.9.hint"
                        },
                        {
                          "value": "PT",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.control.options.10.label",
                          "hint": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.control.options.10.hint"
                        },
                        {
                          "value": "NL",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.control.options.11.label",
                          "hint": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.control.options.11.hint"
                        },
                        {
                          "value": "BE",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.control.options.12.label",
                          "hint": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.control.options.12.hint"
                        },
                        {
                          "value": "CH",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.control.options.13.label",
                          "hint": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.control.options.13.hint"
                        },
                        {
                          "value": "AT",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.control.options.14.label",
                          "hint": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.control.options.14.hint"
                        },
                        {
                          "value": "SE",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.control.options.15.label",
                          "hint": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.control.options.15.hint"
                        },
                        {
                          "value": "NO",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.control.options.16.label",
                          "hint": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.control.options.16.hint"
                        },
                        {
                          "value": "DK",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.control.options.17.label",
                          "hint": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.control.options.17.hint"
                        },
                        {
                          "value": "FI",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.control.options.18.label",
                          "hint": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.control.options.18.hint"
                        },
                        {
                          "value": "PL",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.control.options.19.label",
                          "hint": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.control.options.19.hint"
                        },
                        {
                          "value": "CZ",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.control.options.20.label",
                          "hint": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.control.options.20.hint"
                        },
                        {
                          "value": "BR",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.control.options.21.label",
                          "hint": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.control.options.21.hint"
                        },
                        {
                          "value": "MX",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.control.options.22.label",
                          "hint": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.control.options.22.hint"
                        },
                        {
                          "value": "AR",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.control.options.23.label",
                          "hint": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.control.options.23.hint"
                        },
                        {
                          "value": "JP",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.control.options.24.label",
                          "hint": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.control.options.24.hint"
                        },
                        {
                          "value": "KR",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.control.options.25.label",
                          "hint": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.control.options.25.hint"
                        },
                        {
                          "value": "CN",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.control.options.26.label",
                          "hint": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.control.options.26.hint"
                        },
                        {
                          "value": "IN",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.control.options.27.label",
                          "hint": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.control.options.27.hint"
                        },
                        {
                          "value": "ZA",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.control.options.28.label",
                          "hint": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.control.options.28.hint"
                        },
                        {
                          "value": "TR",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.control.options.29.label",
                          "hint": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.control.options.29.hint"
                        },
                        {
                          "value": "RU",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.control.options.30.label",
                          "hint": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.control.options.30.hint"
                        },
                        {
                          "value": "UA",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.control.options.31.label",
                          "hint": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.2.control.options.31.hint"
                        }
                      ]
                    }
                  },
                  {
                    "id": "libraries.localTags",
                    "scope": "server",
                    "key": "library.local_tags",
                    "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.3.label",
                    "help": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.3.help",
                    "control": {
                      "type": "select",
                      "default": "prefer",
                      "options": [
                        {
                          "value": "prefer",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.3.control.options.0.label",
                          "hint": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.3.control.options.0.hint"
                        },
                        {
                          "value": "supplement",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.3.control.options.1.label",
                          "hint": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.3.control.options.1.hint"
                        },
                        {
                          "value": "off",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.3.control.options.2.label",
                          "hint": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.3.control.options.2.hint"
                        }
                      ]
                    }
                  },
                  {
                    "id": "libraries.lyrics",
                    "scope": "server",
                    "key": "library.lyrics_source",
                    "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.4.label",
                    "help": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.4.help",
                    "control": {
                      "type": "select",
                      "default": "files",
                      "options": [
                        {
                          "value": "files",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.4.control.options.0.label"
                        },
                        {
                          "value": "lrclib",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.4.control.options.1.label"
                        }
                      ]
                    },
                    "children": [
                      {
                        "id": "libraries.fetchLyrics",
                        "scope": "server",
                        "enableWhen": {
                          "id": "libraries.lyrics",
                          "in": [
                            "lrclib"
                          ]
                        },
                        "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.4.children.0.label",
                        "control": {
                          "type": "action",
                          "style": "glass",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.4.children.0.control.label"
                        }
                      }
                    ]
                  },
                  {
                    "id": "libraries.unmatched",
                    "scope": "server",
                    "key": "library.unmatched",
                    "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.5.label",
                    "control": {
                      "type": "link",
                      "label": "next.settingsSpec.server.libraries.panels.library.tabs.2.groups.0.rows.5.control.label"
                    }
                  }
                ]
              },
              {
                "title": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.3.title",
                "rows": [
                  {
                    "id": "libraries.titleSource",
                    "scope": "server",
                    "key": "library.title_source",
                    "label": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.3.rows.0.label",
                    "control": {
                      "type": "select",
                      "default": "tags",
                      "options": [
                        {
                          "value": "tags",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.3.rows.0.control.options.0.label",
                          "hint": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.3.rows.0.control.options.0.hint"
                        },
                        {
                          "value": "names",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.3.rows.0.control.options.1.label",
                          "hint": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.3.rows.0.control.options.1.hint"
                        }
                      ]
                    }
                  },
                  {
                    "id": "libraries.episodeNumbering",
                    "scope": "server",
                    "key": "library.episode_numbering",
                    "label": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.3.rows.1.label",
                    "help": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.3.rows.1.help",
                    "control": {
                      "type": "select",
                      "default": "season",
                      "options": [
                        {
                          "value": "season",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.3.rows.1.control.options.0.label",
                          "hint": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.3.rows.1.control.options.0.hint"
                        },
                        {
                          "value": "absolute",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.3.rows.1.control.options.1.label",
                          "hint": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.3.rows.1.control.options.1.hint"
                        }
                      ]
                    }
                  },
                  {
                    "id": "libraries.extras",
                    "scope": "server",
                    "key": "library.find_extras",
                    "label": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.3.rows.2.label",
                    "help": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.3.rows.2.help",
                    "control": {
                      "type": "toggle",
                      "default": true
                    }
                  },
                  {
                    "id": "libraries.sortArticles",
                    "scope": "server",
                    "key": "library.sort_ignore_articles",
                    "label": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.3.rows.3.label",
                    "help": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.3.rows.3.help",
                    "control": {
                      "type": "toggle",
                      "default": true
                    }
                  }
                ]
              },
              {
                "title": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.4.title",
                "rows": [
                  {
                    "id": "libraries.refreshMode",
                    "scope": "server",
                    "key": "library.refresh_mode",
                    "label": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.4.rows.0.label",
                    "help": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.4.rows.0.help",
                    "control": {
                      "type": "select",
                      "default": "replace_unlocked",
                      "options": [
                        {
                          "value": "replace_unlocked",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.4.rows.0.control.options.0.label"
                        },
                        {
                          "value": "fill_missing",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.4.rows.0.control.options.1.label"
                        }
                      ]
                    }
                  },
                  {
                    "id": "libraries.acoustid",
                    "scope": "server",
                    "key": "library.acoustid",
                    "label": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.4.rows.1.label",
                    "help": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.4.rows.1.help",
                    "control": {
                      "type": "toggle",
                      "default": false
                    }
                  }
                ]
              }
            ]
          },
          {
            "id": "scanning",
            "title": "next.settingsSpec.server.libraries.panels.library.tabs.3.title",
            "groups": [
              {
                "rows": [
                  {
                    "id": "libraries.scanNow",
                    "scope": "server",
                    "key": "library.scan",
                    "label": "next.settingsSpec.server.libraries.panels.library.tabs.3.groups.0.rows.0.label",
                    "help": "next.settingsSpec.server.libraries.panels.library.tabs.3.groups.0.rows.0.help",
                    "control": {
                      "type": "action",
                      "style": "primary",
                      "label": "next.settingsSpec.server.libraries.panels.library.tabs.3.groups.0.rows.0.control.label"
                    }
                  },
                  {
                    "id": "libraries.watch",
                    "scope": "server",
                    "key": "library.monitor_changes",
                    "label": "next.settingsSpec.server.libraries.panels.library.tabs.3.groups.0.rows.1.label",
                    "help": "next.settingsSpec.server.libraries.panels.library.tabs.3.groups.0.rows.1.help",
                    "control": {
                      "type": "toggle",
                      "default": true
                    }
                  },
                  {
                    "id": "libraries.scanDepth",
                    "scope": "server",
                    "key": "library.scan_depth",
                    "label": "next.settingsSpec.server.libraries.panels.library.tabs.3.groups.0.rows.2.label",
                    "control": {
                      "type": "select",
                      "default": "basic",
                      "options": [
                        {
                          "value": "file_list_only",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.3.groups.0.rows.2.control.options.0.label",
                          "hint": "next.settingsSpec.server.libraries.panels.library.tabs.3.groups.0.rows.2.control.options.0.hint",
                          "sets": {
                            "libraries.analysisSteps": {
                              "local_metadata": "skip",
                              "subtitle_discovery": "skip",
                              "checksum": "skip",
                              "waveform": "skip",
                              "loudness": "skip",
                              "audio_fingerprint": "skip"
                            }
                          }
                        },
                        {
                          "value": "basic",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.3.groups.0.rows.2.control.options.1.label",
                          "hint": "next.settingsSpec.server.libraries.panels.library.tabs.3.groups.0.rows.2.control.options.1.hint",
                          "sets": {
                            "libraries.analysisSteps": {
                              "local_metadata": "run",
                              "subtitle_discovery": "run",
                              "checksum": "skip",
                              "waveform": "skip",
                              "loudness": "skip",
                              "audio_fingerprint": "skip"
                            }
                          }
                        },
                        {
                          "value": "complete",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.3.groups.0.rows.2.control.options.2.label",
                          "hint": "next.settingsSpec.server.libraries.panels.library.tabs.3.groups.0.rows.2.control.options.2.hint",
                          "sets": {
                            "libraries.analysisSteps": {
                              "local_metadata": "run",
                              "subtitle_discovery": "run",
                              "checksum": "skip",
                              "waveform": "run",
                              "loudness": "run",
                              "audio_fingerprint": "skip"
                            }
                          }
                        },
                        {
                          "value": "custom",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.3.groups.0.rows.2.control.options.3.label",
                          "hint": "next.settingsSpec.server.libraries.panels.library.tabs.3.groups.0.rows.2.control.options.3.hint"
                        }
                      ]
                    }
                  },
                  {
                    "id": "libraries.skipped",
                    "scope": "server",
                    "key": "library.files_skipped",
                    "label": "next.settingsSpec.server.libraries.panels.library.tabs.3.groups.0.rows.3.label",
                    "help": "next.settingsSpec.server.libraries.panels.library.tabs.3.groups.0.rows.3.help",
                    "control": {
                      "type": "link",
                      "label": "next.settingsSpec.server.libraries.panels.library.tabs.3.groups.0.rows.3.control.label"
                    }
                  },
                  {
                    "id": "libraries.intros",
                    "scope": "server",
                    "label": "next.settingsSpec.server.libraries.panels.library.tabs.3.groups.0.rows.4.label",
                    "control": {
                      "type": "link",
                      "label": "next.settingsSpec.server.libraries.panels.library.tabs.3.groups.0.rows.4.control.label",
                      "to": "server/segments"
                    }
                  },
                  {
                    "id": "libraries.sonicAnalysis",
                    "scope": "server",
                    "key": "library.sonic_analysis",
                    "label": "next.settingsSpec.server.libraries.panels.library.tabs.3.groups.0.rows.5.label",
                    "help": "next.settingsSpec.server.libraries.panels.library.tabs.3.groups.0.rows.5.help",
                    "control": {
                      "type": "toggle",
                      "default": true
                    },
                    "children": [
                      {
                        "id": "libraries.sonicStatus",
                        "scope": "server",
                        "enableWhen": {
                          "id": "libraries.sonicAnalysis",
                          "in": [
                            true
                          ]
                        },
                        "noChanged": true,
                        "label": "next.settingsSpec.server.libraries.panels.library.tabs.3.groups.0.rows.5.children.0.label",
                        "control": {
                          "type": "status"
                        }
                      }
                    ]
                  }
                ]
              },
              {
                "title": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.0.title",
                "rows": [
                  {
                    "id": "libraries.symlinks",
                    "scope": "server",
                    "key": "library.folder.follow_symlinks",
                    "label": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.0.rows.0.label",
                    "help": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.0.rows.0.help",
                    "control": {
                      "type": "toggle",
                      "default": false
                    }
                  },
                  {
                    "id": "libraries.strm",
                    "scope": "server",
                    "key": "library.strm_analysis",
                    "label": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.0.rows.1.label",
                    "help": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.0.rows.1.help",
                    "control": {
                      "type": "toggle",
                      "default": false
                    }
                  },
                  {
                    "id": "libraries.scanAll",
                    "scope": "server",
                    "key": "library.scan_all",
                    "label": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.0.rows.2.label",
                    "help": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.0.rows.2.help",
                    "control": {
                      "type": "action",
                      "style": "glass",
                      "label": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.0.rows.2.control.label"
                    }
                  }
                ]
              },
              {
                "title": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.1.title",
                "rows": [
                  {
                    "id": "libraries.analysisSteps",
                    "scope": "server",
                    "key": "library.analysis_steps",
                    "label": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.1.rows.0.label",
                    "help": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.1.rows.0.help",
                    "control": {
                      "type": "matrix",
                      "columns": [
                        {
                          "value": "run",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.1.rows.0.control.columns.0.label",
                          "hint": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.1.rows.0.control.columns.0.hint"
                        },
                        {
                          "value": "skip",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.1.rows.0.control.columns.1.label",
                          "hint": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.1.rows.0.control.columns.1.hint"
                        }
                      ],
                      "items": [
                        {
                          "id": "local_metadata",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.1.rows.0.control.items.0.label",
                          "default": "run"
                        },
                        {
                          "id": "subtitle_discovery",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.1.rows.0.control.items.1.label",
                          "default": "run"
                        },
                        {
                          "id": "checksum",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.1.rows.0.control.items.2.label",
                          "default": "skip"
                        },
                        {
                          "id": "waveform",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.1.rows.0.control.items.3.label",
                          "default": "skip"
                        },
                        {
                          "id": "loudness",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.1.rows.0.control.items.4.label",
                          "default": "skip"
                        },
                        {
                          "id": "audio_fingerprint",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.1.rows.0.control.items.5.label",
                          "default": "skip"
                        },
                        {
                          "id": "trickplay",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.1.rows.0.control.items.6.label",
                          "default": "skip"
                        },
                        {
                          "id": "chapter_images",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.1.rows.0.control.items.7.label",
                          "default": "skip"
                        }
                      ]
                    }
                  }
                ]
              },
              {
                "title": "next.settingsSpec.server.libraries.panels.library.tabs.3.groups.1.title",
                "rows": [
                  {
                    "id": "libraries.scrub",
                    "scope": "server",
                    "key": "library.scrub_previews",
                    "label": "next.settingsSpec.server.libraries.panels.library.tabs.3.groups.1.rows.0.label",
                    "control": {
                      "type": "select",
                      "default": "off",
                      "options": [
                        {
                          "value": "off",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.3.groups.1.rows.0.control.options.0.label",
                          "sets": {
                            "libraries.analysisSteps": {
                              "trickplay": "skip"
                            }
                          }
                        },
                        {
                          "value": "standard",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.3.groups.1.rows.0.control.options.1.label",
                          "hint": "next.settingsSpec.server.libraries.panels.library.tabs.3.groups.1.rows.0.control.options.1.hint",
                          "sets": {
                            "libraries.analysisSteps": {
                              "trickplay": "run"
                            },
                            "libraries.scrubSpacing": 10,
                            "libraries.scrubWidth": 320
                          }
                        },
                        {
                          "value": "smooth",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.3.groups.1.rows.0.control.options.2.label",
                          "hint": "next.settingsSpec.server.libraries.panels.library.tabs.3.groups.1.rows.0.control.options.2.hint",
                          "sets": {
                            "libraries.analysisSteps": {
                              "trickplay": "run"
                            },
                            "libraries.scrubSpacing": 5,
                            "libraries.scrubWidth": 320
                          }
                        },
                        {
                          "value": "compact",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.3.groups.1.rows.0.control.options.3.label",
                          "hint": "next.settingsSpec.server.libraries.panels.library.tabs.3.groups.1.rows.0.control.options.3.hint",
                          "sets": {
                            "libraries.analysisSteps": {
                              "trickplay": "run"
                            },
                            "libraries.scrubSpacing": 20,
                            "libraries.scrubWidth": 240
                          }
                        },
                        {
                          "value": "custom",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.3.groups.1.rows.0.control.options.4.label",
                          "hint": "next.settingsSpec.server.libraries.panels.library.tabs.3.groups.1.rows.0.control.options.4.hint"
                        }
                      ]
                    }
                  },
                  {
                    "id": "libraries.chapterPictures",
                    "scope": "server",
                    "key": "library.chapter_pictures",
                    "label": "next.settingsSpec.server.libraries.panels.library.tabs.3.groups.1.rows.1.label",
                    "control": {
                      "type": "select",
                      "default": "embedded",
                      "options": [
                        {
                          "value": "embedded",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.3.groups.1.rows.1.control.options.0.label",
                          "sets": {
                            "libraries.analysisSteps": {
                              "chapter_images": "skip"
                            }
                          }
                        },
                        {
                          "value": "generated",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.3.groups.1.rows.1.control.options.1.label",
                          "hint": "next.settingsSpec.server.libraries.panels.library.tabs.3.groups.1.rows.1.control.options.1.hint",
                          "sets": {
                            "libraries.analysisSteps": {
                              "chapter_images": "run"
                            }
                          }
                        },
                        {
                          "value": "custom",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.3.groups.1.rows.1.control.options.2.label",
                          "hint": "next.settingsSpec.server.libraries.panels.library.tabs.3.groups.1.rows.1.control.options.2.hint"
                        }
                      ]
                    }
                  },
                  {
                    "id": "libraries.episodeThumbs",
                    "scope": "server",
                    "key": "library.episode_thumbnails",
                    "label": "next.settingsSpec.server.libraries.panels.library.tabs.3.groups.1.rows.2.label",
                    "help": "next.settingsSpec.server.libraries.panels.library.tabs.3.groups.1.rows.2.help",
                    "control": {
                      "type": "select",
                      "default": "provider_then_made",
                      "options": [
                        {
                          "value": "provider_then_made",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.3.groups.1.rows.2.control.options.0.label"
                        },
                        {
                          "value": "provider",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.3.groups.1.rows.2.control.options.1.label"
                        },
                        {
                          "value": "off",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.3.groups.1.rows.2.control.options.2.label"
                        }
                      ]
                    }
                  }
                ]
              },
              {
                "title": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.2.title",
                "rows": [
                  {
                    "id": "libraries.scrubSpacing",
                    "scope": "server",
                    "key": "library.scrub_interval_seconds",
                    "enableWhen": {
                      "id": "libraries.scrub",
                      "in": [
                        "standard",
                        "smooth",
                        "compact",
                        "custom"
                      ]
                    },
                    "label": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.2.rows.0.label",
                    "help": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.2.rows.0.help",
                    "control": {
                      "type": "number",
                      "default": 10,
                      "unit": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.2.rows.0.control.unit",
                      "min": 2,
                      "max": 60,
                      "step": 1
                    }
                  },
                  {
                    "id": "libraries.scrubWidth",
                    "scope": "server",
                    "key": "library.scrub_width",
                    "enableWhen": {
                      "id": "libraries.scrub",
                      "in": [
                        "standard",
                        "smooth",
                        "compact",
                        "custom"
                      ]
                    },
                    "label": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.2.rows.1.label",
                    "help": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.2.rows.1.help",
                    "control": {
                      "type": "number",
                      "default": 320,
                      "unit": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.2.rows.1.control.unit",
                      "min": 120,
                      "max": 640,
                      "step": 40
                    }
                  },
                  {
                    "id": "libraries.scrubMax",
                    "scope": "server",
                    "key": "library.scrub_max_hours",
                    "enableWhen": {
                      "id": "libraries.scrub",
                      "in": [
                        "standard",
                        "smooth",
                        "compact",
                        "custom"
                      ]
                    },
                    "label": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.2.rows.2.label",
                    "help": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.2.rows.2.help",
                    "control": {
                      "type": "select",
                      "default": "3",
                      "options": [
                        {
                          "value": "1",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.2.rows.2.control.options.0.label"
                        },
                        {
                          "value": "2",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.2.rows.2.control.options.1.label"
                        },
                        {
                          "value": "3",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.2.rows.2.control.options.2.label"
                        },
                        {
                          "value": "4",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.2.rows.2.control.options.3.label"
                        },
                        {
                          "value": "6",
                          "label": "next.settingsSpec.server.libraries.panels.library.tabs.4.groups.2.rows.2.control.options.4.label"
                        }
                      ]
                    }
                  }
                ]
              }
            ]
          },
          {
            "id": "advanced",
            "title": "next.settingsSpec.server.libraries.panels.library.tabs.4.title",
            "groups": [
              {
                "title": "next.ownerLibraries.deleting",
                "description": "next.ownerLibraries.deletingOff",
                "rows": [
                  {
                    "id": "libraries.allowDelete",
                    "scope": "server",
                    "key": "library.allow_media_deletion",
                    "label": "next.ownerLibraries.allowDeletion",
                    "help": "next.ownerLibraries.allowDeletionHelp",
                    "control": {
                      "type": "toggle",
                      "default": false
                    }
                  }
                ]
              }
            ]
          }
        ]
      },
      "newLibrary": {
        "title": "next.settingsSpec.server.libraries.panels.newLibrary.title",
        "newTitle": "next.settingsSpec.server.libraries.panels.newLibrary.newTitle",
        "tabs": [
          {
            "id": "general",
            "title": "next.settingsSpec.server.libraries.panels.newLibrary.tabs.0.title",
            "groups": [
              {
                "rows": [
                  {
                    "id": "newLibrary.name",
                    "scope": "server",
                    "label": "next.settingsSpec.server.libraries.panels.newLibrary.tabs.0.groups.0.rows.0.label",
                    "control": {
                      "type": "text",
                      "placeholder": "next.settingsSpec.server.libraries.panels.newLibrary.tabs.0.groups.0.rows.0.control.placeholder"
                    }
                  },
                  {
                    "id": "newLibrary.type",
                    "scope": "server",
                    "label": "next.settingsSpec.server.libraries.panels.newLibrary.tabs.0.groups.0.rows.1.label",
                    "control": {
                      "type": "select",
                      "default": "movies",
                      "options": [
                        {
                          "value": "movies",
                          "label": "next.settingsSpec.server.libraries.panels.newLibrary.tabs.0.groups.0.rows.1.control.options.0.label",
                          "hint": "next.settingsSpec.server.libraries.panels.newLibrary.tabs.0.groups.0.rows.1.control.options.0.hint"
                        },
                        {
                          "value": "tv",
                          "label": "next.settingsSpec.server.libraries.panels.newLibrary.tabs.0.groups.0.rows.1.control.options.1.label",
                          "hint": "next.settingsSpec.server.libraries.panels.newLibrary.tabs.0.groups.0.rows.1.control.options.1.hint"
                        },
                        {
                          "value": "anime",
                          "label": "next.settingsSpec.server.libraries.panels.newLibrary.tabs.0.groups.0.rows.1.control.options.2.label",
                          "hint": "next.settingsSpec.server.libraries.panels.newLibrary.tabs.0.groups.0.rows.1.control.options.2.hint"
                        },
                        {
                          "value": "music",
                          "label": "next.settingsSpec.server.libraries.panels.newLibrary.tabs.0.groups.0.rows.1.control.options.3.label",
                          "hint": "next.settingsSpec.server.libraries.panels.newLibrary.tabs.0.groups.0.rows.1.control.options.3.hint"
                        },
                        {
                          "value": "audiobooks",
                          "label": "next.settingsSpec.server.libraries.panels.newLibrary.tabs.0.groups.0.rows.1.control.options.4.label",
                          "hint": "next.settingsSpec.server.libraries.panels.newLibrary.tabs.0.groups.0.rows.1.control.options.4.hint"
                        },
                        {
                          "value": "books",
                          "label": "next.settingsSpec.server.libraries.panels.newLibrary.tabs.0.groups.0.rows.1.control.options.5.label",
                          "hint": "next.settingsSpec.server.libraries.panels.newLibrary.tabs.0.groups.0.rows.1.control.options.5.hint"
                        },
                        {
                          "value": "comics",
                          "label": "next.settingsSpec.server.libraries.panels.newLibrary.tabs.0.groups.0.rows.1.control.options.6.label",
                          "hint": "next.settingsSpec.server.libraries.panels.newLibrary.tabs.0.groups.0.rows.1.control.options.6.hint"
                        }
                      ]
                    }
                  }
                ]
              }
            ]
          },
          {
            "id": "folders",
            "title": "next.settingsSpec.server.libraries.panels.newLibrary.tabs.1.title",
            "groups": [
              {
                "rows": [
                  {
                    "id": "newLibrary.folders",
                    "scope": "server",
                    "label": "next.settingsSpec.server.libraries.panels.newLibrary.tabs.1.groups.0.rows.0.label",
                    "control": {
                      "type": "list",
                      "addLabel": "next.settingsSpec.server.libraries.panels.newLibrary.tabs.1.groups.0.rows.0.control.addLabel",
                      "empty": "next.settingsSpec.server.libraries.panels.newLibrary.tabs.1.groups.0.rows.0.control.empty",
                      "itemActions": [
                        {
                          "label": "next.settingsSpec.server.libraries.panels.newLibrary.tabs.1.groups.0.rows.0.control.itemActions.0.label",
                          "style": "danger",
                          "remove": true,
                          "confirm": "next.settingsSpec.server.libraries.panels.newLibrary.tabs.1.groups.0.rows.0.control.itemActions.0.confirm"
                        }
                      ],
                      "addMenu": [
                        {
                          "label": "next.settingsSpec.server.libraries.panels.newLibrary.tabs.1.groups.0.rows.0.control.addMenu.0.label",
                          "icon": "folder",
                          "do": "folder"
                        },
                        {
                          "label": "next.settingsSpec.server.libraries.panels.newLibrary.tabs.1.groups.0.rows.0.control.addMenu.4.label",
                          "icon": "plus",
                          "do": "link:server/remote-sources"
                        }
                      ]
                    }
                  }
                ]
              }
            ]
          }
        ],
        "footer": {
          "primary": "next.settingsSpec.server.libraries.panels.newLibrary.footer.primary",
          "secondary": "next.settingsSpec.server.libraries.panels.newLibrary.footer.secondary"
        }
      }
    },
    "configOnly": [
      {
        "key": "analysis.max_preview_frames",
        "label": "next.settingsSpec.server.libraries.configOnly.0.label"
      }
    ]
  },
  {
    "scope": "server",
    "id": "remote-sources",
    "navGroup": "Your media",
    "icon": "globe",
    "kind": "page",
    "title": "next.settingsSpec.server.remote-sources.title",
    "groups": [
      {
        "title": "next.settingsSpec.server.remote-sources.groups.0.title",
        "rows": [
          {
            "id": "storage.remoteSources",
            "scope": "server",
            "label": "next.settingsSpec.server.remote-sources.groups.0.rows.0.label",
            "control": {
              "type": "list",
              "addPanel": "newSource",
              "addLabel": "next.settingsSpec.server.remote-sources.groups.0.rows.0.control.addLabel",
              "itemActions": [
                {
                  "label": "next.settingsSpec.server.remote-sources.groups.0.rows.0.control.itemActions.0.label",
                  "style": "glass",
                  "panel": "source"
                }
              ]
            }
          }
        ]
      }
    ],
    "panels": {
      "source": {
        "tabs": [
          {
            "id": "connection",
            "title": "next.settingsSpec.server.remote-sources.panels.source.tabs.0.title",
            "groups": [
              {
                "rows": [
                  {
                    "id": "source.kind",
                    "scope": "server",
                    "key": "remote_sources.kind",
                    "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.0.groups.0.rows.0.label",
                    "control": {
                      "type": "readonly"
                    }
                  },
                  {
                    "id": "source.address",
                    "scope": "server",
                    "key": "remote_sources.address",
                    "noChanged": true,
                    "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.0.groups.0.rows.1.label",
                    "control": {
                      "type": "text",
                      "placeholder": "next.settingsSpec.server.remote-sources.panels.source.tabs.0.groups.0.rows.1.control.placeholder",
                      "default": ""
                    }
                  },
                  {
                    "id": "source.account",
                    "scope": "server",
                    "key": "remote_sources.account",
                    "noChanged": true,
                    "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.0.groups.0.rows.2.label",
                    "control": {
                      "type": "text",
                      "placeholder": "next.settingsSpec.server.remote-sources.panels.source.tabs.0.groups.0.rows.2.control.placeholder",
                      "default": ""
                    }
                  },
                  {
                    "id": "source.password",
                    "scope": "server",
                    "key": "remote_sources.password",
                    "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.0.groups.0.rows.3.label",
                    "control": {
                      "type": "text",
                      "secret": true,
                      "placeholder": "next.settingsSpec.server.remote-sources.panels.source.tabs.0.groups.0.rows.3.control.placeholder",
                      "default": ""
                    }
                  },
                  {
                    "id": "source.check",
                    "scope": "server",
                    "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.0.groups.0.rows.4.label",
                    "control": {
                      "type": "action",
                      "style": "glass",
                      "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.0.groups.0.rows.4.control.label"
                    }
                  }
                ]
              },
              {
                "rows": [
                  {
                    "id": "source.disconnect",
                    "scope": "server",
                    "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.0.groups.1.rows.0.label",
                    "control": {
                      "type": "action",
                      "style": "danger",
                      "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.0.groups.1.rows.0.control.label",
                      "confirm": "next.settingsSpec.server.remote-sources.panels.source.tabs.0.groups.1.rows.0.control.confirm"
                    }
                  }
                ]
              }
            ]
          },
          {
            "id": "reading",
            "title": "next.settingsSpec.server.remote-sources.panels.source.tabs.1.title",
            "groups": [
              {
                "rows": [
                  {
                    "id": "source.treatment",
                    "scope": "server",
                    "key": "remote_sources.treatment",
                    "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.1.groups.0.rows.0.label",
                    "control": {
                      "type": "select",
                      "default": "once",
                      "options": [
                        {
                          "value": "list",
                          "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.1.groups.0.rows.0.control.options.0.label",
                          "hint": "next.settingsSpec.server.remote-sources.panels.source.tabs.1.groups.0.rows.0.control.options.0.hint",
                          "sets": {
                            "source.checkEvery": "1d",
                            "source.watch": false,
                            "source.scanOnStart": false,
                            "source.recheck": "30d",
                            "source.doubleCheck": false,
                            "source.readOnce": false,
                            "source.wholeFile": false,
                            "source.wholeSteps": {
                              "trickplay": "skip",
                              "chapter_images": "skip",
                              "waveform": "skip",
                              "loudness": "skip",
                              "checksum": "skip"
                            },
                            "source.intros": "off",
                            "source.cache": "0",
                            "source.connections": 2,
                            "source.rps": 2
                          }
                        },
                        {
                          "value": "once",
                          "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.1.groups.0.rows.0.control.options.1.label",
                          "hint": "next.settingsSpec.server.remote-sources.panels.source.tabs.1.groups.0.rows.0.control.options.1.hint",
                          "sets": {
                            "source.checkEvery": "6h",
                            "source.watch": false,
                            "source.scanOnStart": false,
                            "source.recheck": "14d",
                            "source.doubleCheck": false,
                            "source.readOnce": true,
                            "source.wholeFile": false,
                            "source.wholeSteps": {
                              "trickplay": "skip",
                              "chapter_images": "skip",
                              "waveform": "skip",
                              "loudness": "skip",
                              "checksum": "skip"
                            },
                            "source.intros": "off",
                            "source.cache": "8",
                            "source.connections": 4,
                            "source.rps": 4
                          }
                        },
                        {
                          "value": "everything",
                          "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.1.groups.0.rows.0.control.options.2.label",
                          "hint": "next.settingsSpec.server.remote-sources.panels.source.tabs.1.groups.0.rows.0.control.options.2.hint",
                          "sets": {
                            "source.checkEvery": "1h",
                            "source.watch": true,
                            "source.scanOnStart": true,
                            "source.recheck": "7d",
                            "source.doubleCheck": true,
                            "source.readOnce": true,
                            "source.wholeFile": true,
                            "source.wholeSteps": {
                              "trickplay": "run",
                              "chapter_images": "run",
                              "waveform": "run",
                              "loudness": "run",
                              "checksum": "skip"
                            },
                            "source.intros": "server",
                            "source.cache": "32",
                            "source.connections": 8,
                            "source.rps": 10
                          }
                        },
                        {
                          "value": "ask",
                          "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.1.groups.0.rows.0.control.options.3.label",
                          "hint": "next.settingsSpec.server.remote-sources.panels.source.tabs.1.groups.0.rows.0.control.options.3.hint",
                          "sets": {
                            "source.checkEvery": "never",
                            "source.watch": false,
                            "source.scanOnStart": false,
                            "source.recheck": "never",
                            "source.doubleCheck": false,
                            "source.readOnce": true,
                            "source.wholeFile": false,
                            "source.wholeSteps": {
                              "trickplay": "skip",
                              "chapter_images": "skip",
                              "waveform": "skip",
                              "loudness": "skip",
                              "checksum": "skip"
                            },
                            "source.intros": "off",
                            "source.cache": "0",
                            "source.connections": 2,
                            "source.rps": 2
                          }
                        },
                        {
                          "value": "custom",
                          "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.1.groups.0.rows.0.control.options.4.label",
                          "hint": "next.settingsSpec.server.remote-sources.panels.source.tabs.1.groups.0.rows.0.control.options.4.hint"
                        }
                      ]
                    }
                  },
                  {
                    "id": "source.cache",
                    "scope": "server",
                    "key": "remote_sources.read_cache_gb",
                    "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.1.groups.0.rows.1.label",
                    "help": "next.settingsSpec.server.remote-sources.panels.source.tabs.1.groups.0.rows.1.help",
                    "control": {
                      "type": "select",
                      "default": "8",
                      "options": [
                        {
                          "value": "0",
                          "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.1.groups.0.rows.1.control.options.0.label"
                        },
                        {
                          "value": "2",
                          "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.1.groups.0.rows.1.control.options.1.label"
                        },
                        {
                          "value": "4",
                          "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.1.groups.0.rows.1.control.options.2.label"
                        },
                        {
                          "value": "8",
                          "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.1.groups.0.rows.1.control.options.3.label"
                        },
                        {
                          "value": "16",
                          "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.1.groups.0.rows.1.control.options.4.label"
                        },
                        {
                          "value": "32",
                          "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.1.groups.0.rows.1.control.options.5.label"
                        },
                        {
                          "value": "64",
                          "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.1.groups.0.rows.1.control.options.6.label"
                        },
                        {
                          "value": "128",
                          "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.1.groups.0.rows.1.control.options.7.label"
                        }
                      ]
                    }
                  }
                ]
              },
              {
                "title": "next.settingsSpec.server.remote-sources.panels.source.tabs.1.groups.1.title",
                "rows": [
                  {
                    "id": "source.connections",
                    "scope": "server",
                    "key": "remote_sources.connections",
                    "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.1.groups.1.rows.0.label",
                    "help": "next.settingsSpec.server.remote-sources.panels.source.tabs.1.groups.1.rows.0.help",
                    "control": {
                      "type": "number",
                      "default": 4,
                      "unit": "next.settingsSpec.server.remote-sources.panels.source.tabs.1.groups.1.rows.0.control.unit",
                      "min": 1,
                      "max": 32,
                      "step": 1
                    }
                  },
                  {
                    "id": "source.rps",
                    "scope": "server",
                    "key": "remote_sources.requests_per_second",
                    "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.1.groups.1.rows.1.label",
                    "control": {
                      "type": "number",
                      "default": 4,
                      "unit": "next.settingsSpec.server.remote-sources.panels.source.tabs.1.groups.1.rows.1.control.unit",
                      "min": 1,
                      "max": 50,
                      "step": 1
                    }
                  },
                  {
                    "id": "source.speed",
                    "scope": "server",
                    "key": "remote_sources.download_mb_per_second",
                    "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.1.groups.1.rows.2.label",
                    "help": "next.settingsSpec.server.remote-sources.panels.source.tabs.1.groups.1.rows.2.help",
                    "control": {
                      "type": "number",
                      "default": null,
                      "style": "field",
                      "unit": "next.settingsSpec.server.remote-sources.panels.source.tabs.1.groups.1.rows.2.control.unit",
                      "min": 1,
                      "max": 1000,
                      "step": 1,
                      "optional": true,
                      "placeholder": "next.settingsSpec.server.remote-sources.panels.source.tabs.1.groups.1.rows.2.control.placeholder"
                    }
                  }
                ]
              }
            ]
          },
          {
            "id": "used-by",
            "title": "next.settingsSpec.server.remote-sources.panels.source.tabs.2.title",
            "groups": [
              {
                "rows": [
                  {
                    "id": "source.usedBy",
                    "scope": "server",
                    "noChanged": true,
                    "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.2.groups.0.rows.0.label",
                    "control": {
                      "type": "list",
                      "empty": "next.settingsSpec.server.remote-sources.panels.source.tabs.2.groups.0.rows.0.control.empty"
                    }
                  },
                  {
                    "id": "source.openLibraries",
                    "scope": "server",
                    "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.2.groups.0.rows.1.label",
                    "control": {
                      "type": "link",
                      "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.2.groups.0.rows.1.control.label",
                      "to": "server/libraries"
                    }
                  }
                ]
              }
            ]
          },
          {
            "id": "advanced",
            "title": "next.settingsSpec.server.remote-sources.panels.source.tabs.3.title",
            "groups": [
              {
                "title": "next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.0.title",
                "rows": [
                  {
                    "id": "source.checkEvery",
                    "scope": "server",
                    "key": "remote_sources.check_every",
                    "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.0.rows.0.label",
                    "help": "next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.0.rows.0.help",
                    "control": {
                      "type": "select",
                      "default": "6h",
                      "options": [
                        {
                          "value": "15m",
                          "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.0.rows.0.control.options.0.label"
                        },
                        {
                          "value": "1h",
                          "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.0.rows.0.control.options.1.label"
                        },
                        {
                          "value": "6h",
                          "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.0.rows.0.control.options.2.label"
                        },
                        {
                          "value": "1d",
                          "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.0.rows.0.control.options.3.label"
                        },
                        {
                          "value": "1w",
                          "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.0.rows.0.control.options.4.label"
                        },
                        {
                          "value": "never",
                          "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.0.rows.0.control.options.5.label"
                        }
                      ]
                    }
                  },
                  {
                    "id": "source.watch",
                    "scope": "server",
                    "key": "remote_sources.watch",
                    "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.0.rows.1.label",
                    "help": "next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.0.rows.1.help",
                    "control": {
                      "type": "toggle",
                      "default": false
                    }
                  },
                  {
                    "id": "source.scanOnStart",
                    "scope": "server",
                    "key": "remote_sources.scan_on_start",
                    "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.0.rows.2.label",
                    "control": {
                      "type": "toggle",
                      "default": false
                    }
                  },
                  {
                    "id": "source.recheck",
                    "scope": "server",
                    "key": "remote_sources.recheck_every",
                    "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.0.rows.3.label",
                    "help": "next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.0.rows.3.help",
                    "control": {
                      "type": "select",
                      "default": "14d",
                      "options": [
                        {
                          "value": "7d",
                          "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.0.rows.3.control.options.0.label"
                        },
                        {
                          "value": "14d",
                          "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.0.rows.3.control.options.1.label"
                        },
                        {
                          "value": "30d",
                          "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.0.rows.3.control.options.2.label"
                        },
                        {
                          "value": "never",
                          "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.0.rows.3.control.options.3.label"
                        }
                      ]
                    }
                  },
                  {
                    "id": "source.doubleCheck",
                    "scope": "server",
                    "key": "remote_sources.double_check",
                    "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.0.rows.4.label",
                    "help": "next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.0.rows.4.help",
                    "control": {
                      "type": "toggle",
                      "default": false
                    }
                  }
                ]
              },
              {
                "title": "next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.1.title",
                "rows": [
                  {
                    "id": "source.readOnce",
                    "scope": "server",
                    "key": "remote_sources.read_new_files",
                    "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.1.rows.0.label",
                    "help": "next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.1.rows.0.help",
                    "control": {
                      "type": "toggle",
                      "default": true
                    }
                  },
                  {
                    "id": "source.wholeFile",
                    "scope": "server",
                    "key": "remote_sources.whole_file_reads",
                    "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.1.rows.1.label",
                    "help": "next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.1.rows.1.help",
                    "control": {
                      "type": "toggle",
                      "default": false
                    }
                  },
                  {
                    "id": "source.wholeSteps",
                    "scope": "server",
                    "key": "remote_sources.whole_file_steps",
                    "enableWhen": {
                      "id": "source.wholeFile",
                      "in": [
                        true
                      ]
                    },
                    "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.1.rows.2.label",
                    "control": {
                      "type": "matrix",
                      "columns": [
                        {
                          "value": "run",
                          "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.1.rows.2.control.columns.0.label",
                          "hint": "next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.1.rows.2.control.columns.0.hint"
                        },
                        {
                          "value": "skip",
                          "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.1.rows.2.control.columns.1.label",
                          "hint": "next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.1.rows.2.control.columns.1.hint"
                        }
                      ],
                      "items": [
                        {
                          "id": "trickplay",
                          "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.1.rows.2.control.items.0.label",
                          "default": "skip"
                        },
                        {
                          "id": "chapter_images",
                          "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.1.rows.2.control.items.1.label",
                          "default": "skip"
                        },
                        {
                          "id": "waveform",
                          "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.1.rows.2.control.items.2.label",
                          "default": "skip"
                        },
                        {
                          "id": "loudness",
                          "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.1.rows.2.control.items.3.label",
                          "default": "skip"
                        },
                        {
                          "id": "checksum",
                          "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.1.rows.2.control.items.4.label",
                          "default": "skip"
                        }
                      ]
                    }
                  },
                  {
                    "id": "source.intros",
                    "scope": "server",
                    "key": "remote_sources.intros",
                    "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.1.rows.3.label",
                    "help": "next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.1.rows.3.help",
                    "control": {
                      "type": "select",
                      "default": "off",
                      "options": [
                        {
                          "value": "off",
                          "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.1.rows.3.control.options.0.label"
                        },
                        {
                          "value": "server",
                          "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.1.rows.3.control.options.1.label"
                        },
                        {
                          "value": "standard",
                          "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.1.rows.3.control.options.2.label"
                        },
                        {
                          "value": "thorough",
                          "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.1.rows.3.control.options.3.label"
                        }
                      ]
                    }
                  }
                ]
              },
              {
                "title": "next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.2.title",
                "rows": [
                  {
                    "id": "source.diskFree",
                    "scope": "server",
                    "key": "remote_sources.cache_floor_gb",
                    "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.2.rows.0.label",
                    "help": "next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.2.rows.0.help",
                    "control": {
                      "type": "select",
                      "default": "2",
                      "options": [
                        {
                          "value": "1",
                          "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.2.rows.0.control.options.0.label"
                        },
                        {
                          "value": "2",
                          "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.2.rows.0.control.options.1.label"
                        },
                        {
                          "value": "4",
                          "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.2.rows.0.control.options.2.label"
                        },
                        {
                          "value": "8",
                          "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.2.rows.0.control.options.3.label"
                        },
                        {
                          "value": "16",
                          "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.2.rows.0.control.options.4.label"
                        }
                      ]
                    }
                  }
                ]
              },
              {
                "title": "next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.3.title",
                "rows": [
                  {
                    "id": "source.customRclone",
                    "scope": "server",
                    "key": "remote_sources.custom_rclone",
                    "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.3.rows.0.label",
                    "help": "next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.3.rows.0.help",
                    "control": {
                      "type": "toggle",
                      "default": false
                    }
                  },
                  {
                    "id": "source.rclonePath",
                    "scope": "server",
                    "key": "remote_sources.rclone_path",
                    "enableWhen": {
                      "id": "source.customRclone",
                      "in": [
                        true
                      ]
                    },
                    "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.3.rows.1.label",
                    "control": {
                      "type": "folder"
                    }
                  },
                  {
                    "id": "source.rcloneVersion",
                    "scope": "server",
                    "label": "next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.3.rows.2.label",
                    "control": {
                      "type": "readonly"
                    }
                  }
                ]
              }
            ]
          }
        ]
      },
      "newSource": {
        "title": "next.settingsSpec.server.remote-sources.panels.newSource.title",
        "newTitle": "next.settingsSpec.server.remote-sources.panels.newSource.newTitle",
        "tabs": [
          {
            "id": "connection",
            "title": "next.settingsSpec.server.remote-sources.panels.newSource.tabs.0.title",
            "groups": [
              {
                "rows": [
                  {
                    "id": "newSource.kind",
                    "scope": "server",
                    "label": "next.settingsSpec.server.remote-sources.panels.newSource.tabs.0.groups.0.rows.0.label",
                    "control": {
                      "type": "select",
                      "default": "webdav",
                      "options": [
                        {
                          "value": "webdav",
                          "label": "next.settingsSpec.server.remote-sources.panels.newSource.tabs.0.groups.0.rows.0.control.options.0.label",
                          "hint": "next.settingsSpec.server.remote-sources.panels.newSource.tabs.0.groups.0.rows.0.control.options.0.hint"
                        },
                        {
                          "value": "rclone-new",
                          "label": "next.settingsSpec.server.remote-sources.panels.newSource.tabs.0.groups.0.rows.0.control.options.1.label",
                          "hint": "next.settingsSpec.server.remote-sources.panels.newSource.tabs.0.groups.0.rows.0.control.options.1.hint"
                        },
                        {
                          "value": "rclone-config",
                          "label": "next.settingsSpec.server.remote-sources.panels.newSource.tabs.0.groups.0.rows.0.control.options.2.label",
                          "hint": "next.settingsSpec.server.remote-sources.panels.newSource.tabs.0.groups.0.rows.0.control.options.2.hint"
                        }
                      ]
                    }
                  },
                  {
                    "id": "newSource.name",
                    "scope": "server",
                    "label": "next.settingsSpec.server.remote-sources.panels.newSource.tabs.0.groups.0.rows.1.label",
                    "control": {
                      "type": "text",
                      "placeholder": "next.settingsSpec.server.remote-sources.panels.newSource.tabs.0.groups.0.rows.1.control.placeholder"
                    }
                  }
                ]
              },
              {
                "title": "next.settingsSpec.server.remote-sources.panels.newSource.tabs.0.groups.1.title",
                "rows": [
                  {
                    "id": "newSource.address",
                    "scope": "server",
                    "enableWhen": {
                      "id": "newSource.kind",
                      "in": [
                        "webdav"
                      ]
                    },
                    "label": "next.settingsSpec.server.remote-sources.panels.newSource.tabs.0.groups.1.rows.0.label",
                    "control": {
                      "type": "text",
                      "placeholder": "next.settingsSpec.server.remote-sources.panels.newSource.tabs.0.groups.1.rows.0.control.placeholder"
                    }
                  },
                  {
                    "id": "newSource.account",
                    "scope": "server",
                    "enableWhen": {
                      "id": "newSource.kind",
                      "in": [
                        "webdav"
                      ]
                    },
                    "label": "next.settingsSpec.server.remote-sources.panels.newSource.tabs.0.groups.1.rows.1.label",
                    "control": {
                      "type": "text",
                      "placeholder": "next.settingsSpec.server.remote-sources.panels.newSource.tabs.0.groups.1.rows.1.control.placeholder"
                    }
                  },
                  {
                    "id": "newSource.password",
                    "scope": "server",
                    "enableWhen": {
                      "id": "newSource.kind",
                      "in": [
                        "webdav"
                      ]
                    },
                    "label": "next.settingsSpec.server.remote-sources.panels.newSource.tabs.0.groups.1.rows.2.label",
                    "control": {
                      "type": "text",
                      "secret": true,
                      "placeholder": "next.settingsSpec.server.remote-sources.panels.newSource.tabs.0.groups.1.rows.2.control.placeholder",
                      "default": ""
                    }
                  },
                  {
                    "id": "newSource.storageType",
                    "scope": "server",
                    "enableWhen": {
                      "id": "newSource.kind",
                      "in": [
                        "rclone-new"
                      ]
                    },
                    "label": "next.settingsSpec.server.remote-sources.panels.newSource.tabs.0.groups.1.rows.3.label",
                    "control": {
                      "type": "select",
                      "default": "s3",
                      "options": [
                        {
                          "value": "s3",
                          "label": "next.settingsSpec.server.remote-sources.panels.newSource.tabs.0.groups.1.rows.3.control.options.0.label"
                        },
                        {
                          "value": "sftp",
                          "label": "next.settingsSpec.server.remote-sources.panels.newSource.tabs.0.groups.1.rows.3.control.options.1.label"
                        },
                        {
                          "value": "smb",
                          "label": "next.settingsSpec.server.remote-sources.panels.newSource.tabs.0.groups.1.rows.3.control.options.2.label"
                        },
                        {
                          "value": "gdrive",
                          "label": "next.settingsSpec.server.remote-sources.panels.newSource.tabs.0.groups.1.rows.3.control.options.3.label"
                        },
                        {
                          "value": "onedrive",
                          "label": "next.settingsSpec.server.remote-sources.panels.newSource.tabs.0.groups.1.rows.3.control.options.4.label"
                        },
                        {
                          "value": "dropbox",
                          "label": "next.settingsSpec.server.remote-sources.panels.newSource.tabs.0.groups.1.rows.3.control.options.5.label"
                        },
                        {
                          "value": "other",
                          "label": "next.settingsSpec.server.remote-sources.panels.newSource.tabs.0.groups.1.rows.3.control.options.6.label"
                        }
                      ]
                    }
                  },
                  {
                    "id": "newSource.remote",
                    "scope": "server",
                    "enableWhen": {
                      "id": "newSource.kind",
                      "in": [
                        "rclone-config"
                      ]
                    },
                    "label": "next.settingsSpec.server.remote-sources.panels.newSource.tabs.0.groups.1.rows.4.label",
                    "control": {
                      "type": "select",
                      "dynamicOptions": true,
                      "options": []
                    }
                  }
                ]
              },
              {
                "rows": [
                  {
                    "id": "newSource.test",
                    "scope": "server",
                    "label": "next.settingsSpec.server.remote-sources.panels.newSource.tabs.0.groups.2.rows.0.label",
                    "control": {
                      "type": "action",
                      "style": "glass",
                      "label": "next.settingsSpec.server.remote-sources.panels.newSource.tabs.0.groups.2.rows.0.control.label"
                    }
                  },
                  {
                    "id": "storage.addRemote",
                    "scope": "server",
                    "label": "next.settingsSpec.server.remote-sources.panels.newSource.tabs.0.groups.2.rows.1.label",
                    "help": "next.settingsSpec.server.remote-sources.panels.newSource.tabs.0.groups.2.rows.1.help",
                    "control": {
                      "type": "link",
                      "label": "next.settingsSpec.server.remote-sources.panels.newSource.tabs.0.groups.2.rows.1.control.label"
                    }
                  }
                ]
              }
            ]
          },
          {
            "id": "reading",
            "title": "next.settingsSpec.server.remote-sources.panels.newSource.tabs.1.title",
            "groups": [
              {
                "rows": [
                  {
                    "id": "newSource.treatment",
                    "scope": "server",
                    "label": "next.settingsSpec.server.remote-sources.panels.newSource.tabs.1.groups.0.rows.0.label",
                    "control": {
                      "type": "select",
                      "default": "once",
                      "options": [
                        {
                          "value": "list",
                          "label": "next.settingsSpec.server.remote-sources.panels.newSource.tabs.1.groups.0.rows.0.control.options.0.label",
                          "hint": "next.settingsSpec.server.remote-sources.panels.newSource.tabs.1.groups.0.rows.0.control.options.0.hint"
                        },
                        {
                          "value": "once",
                          "label": "next.settingsSpec.server.remote-sources.panels.newSource.tabs.1.groups.0.rows.0.control.options.1.label",
                          "hint": "next.settingsSpec.server.remote-sources.panels.newSource.tabs.1.groups.0.rows.0.control.options.1.hint"
                        },
                        {
                          "value": "everything",
                          "label": "next.settingsSpec.server.remote-sources.panels.newSource.tabs.1.groups.0.rows.0.control.options.2.label",
                          "hint": "next.settingsSpec.server.remote-sources.panels.newSource.tabs.1.groups.0.rows.0.control.options.2.hint"
                        },
                        {
                          "value": "ask",
                          "label": "next.settingsSpec.server.remote-sources.panels.newSource.tabs.1.groups.0.rows.0.control.options.3.label",
                          "hint": "next.settingsSpec.server.remote-sources.panels.newSource.tabs.1.groups.0.rows.0.control.options.3.hint"
                        }
                      ]
                    }
                  }
                ]
              }
            ]
          },
          {
            "id": "folders",
            "title": "next.settingsSpec.server.remote-sources.panels.newSource.tabs.2.title",
            "groups": [
              {
                "rows": [
                  {
                    "id": "newSource.library",
                    "scope": "server",
                    "label": "next.settingsSpec.server.remote-sources.panels.newSource.tabs.2.groups.0.rows.0.label",
                    "control": {
                      "type": "select",
                      "default": "later",
                      "dynamicOptions": true,
                      "options": [
                        {
                          "value": "later",
                          "label": "next.settingsSpec.server.remote-sources.panels.newSource.tabs.2.groups.0.rows.0.control.options.0.label"
                        }
                      ]
                    }
                  },
                  {
                    "id": "newSource.folder",
                    "scope": "server",
                    "enableWhen": {
                      "id": "newSource.library",
                      "in": [
                        "movies",
                        "tv",
                        "anime",
                        "music",
                        "audiobooks"
                      ]
                    },
                    "label": "next.settingsSpec.server.remote-sources.panels.newSource.tabs.2.groups.0.rows.1.label",
                    "control": {
                      "type": "folder"
                    }
                  }
                ]
              }
            ]
          }
        ],
        "footer": {
          "primary": "next.settingsSpec.server.remote-sources.panels.newSource.footer.primary",
          "secondary": "next.settingsSpec.server.remote-sources.panels.newSource.footer.secondary"
        }
      }
    },
    "configOnly": [
      {
        "key": "remote_sources.rclone_options",
        "label": "next.settingsSpec.server.remote-sources.configOnly.0.label"
      }
    ]
  },
  {
    "scope": "server",
    "id": "metadata",
    "navGroup": "Your media",
    "icon": "image",
    "kind": "settings",
    "title": "next.settingsSpec.server.metadata.title",
    "groups": [
      {
        "title": "next.settingsSpec.server.metadata.groups.0.title",
        "rows": [
          {
            "id": "metadata.online",
            "scope": "server",
            "key": "metadata.online_lookups",
            "label": "next.settingsSpec.server.metadata.groups.0.rows.0.label",
            "help": "next.settingsSpec.server.metadata.groups.0.rows.0.help",
            "control": {
              "type": "toggle",
              "default": true
            }
          },
          {
            "id": "metadata.adult",
            "scope": "server",
            "key": "metadata.include_adult",
            "label": "next.settingsSpec.server.metadata.groups.0.rows.1.label",
            "help": "next.settingsSpec.server.metadata.groups.0.rows.1.help",
            "control": {
              "type": "toggle",
              "default": false
            }
          },
          {
            "id": "metadata.perLibrary",
            "scope": "server",
            "key": "metadata.per_library",
            "label": "next.settingsSpec.server.metadata.groups.0.rows.2.label",
            "help": "next.settingsSpec.server.metadata.groups.0.rows.2.help",
            "control": {
              "type": "link",
              "label": "next.settingsSpec.server.metadata.groups.0.rows.2.control.label",
              "to": "server/libraries"
            }
          }
        ]
      },
      {
        "title": "next.settingsSpec.server.metadata.groups.1.title",
        "rows": [
          {
            "id": "metadata.language",
            "scope": "server",
            "key": "metadata.default_language",
            "label": "next.settingsSpec.server.metadata.groups.1.rows.0.label",
            "help": "next.settingsSpec.server.metadata.groups.1.rows.0.help",
            "control": {
              "type": "select",
              "default": "en",
              "options": [
                {
                  "value": "en",
                  "label": "next.settingsSpec.server.metadata.groups.1.rows.0.control.options.0.label"
                },
                {
                  "value": "ar",
                  "label": "next.settingsSpec.server.metadata.groups.1.rows.0.control.options.1.label"
                },
                {
                  "value": "zh",
                  "label": "next.settingsSpec.server.metadata.groups.1.rows.0.control.options.2.label"
                },
                {
                  "value": "cs",
                  "label": "next.settingsSpec.server.metadata.groups.1.rows.0.control.options.3.label"
                },
                {
                  "value": "da",
                  "label": "next.settingsSpec.server.metadata.groups.1.rows.0.control.options.4.label"
                },
                {
                  "value": "nl",
                  "label": "next.settingsSpec.server.metadata.groups.1.rows.0.control.options.5.label"
                },
                {
                  "value": "fi",
                  "label": "next.settingsSpec.server.metadata.groups.1.rows.0.control.options.6.label"
                },
                {
                  "value": "fr",
                  "label": "next.settingsSpec.server.metadata.groups.1.rows.0.control.options.7.label"
                },
                {
                  "value": "de",
                  "label": "next.settingsSpec.server.metadata.groups.1.rows.0.control.options.8.label"
                },
                {
                  "value": "el",
                  "label": "next.settingsSpec.server.metadata.groups.1.rows.0.control.options.9.label"
                },
                {
                  "value": "he",
                  "label": "next.settingsSpec.server.metadata.groups.1.rows.0.control.options.10.label"
                },
                {
                  "value": "hi",
                  "label": "next.settingsSpec.server.metadata.groups.1.rows.0.control.options.11.label"
                },
                {
                  "value": "hu",
                  "label": "next.settingsSpec.server.metadata.groups.1.rows.0.control.options.12.label"
                },
                {
                  "value": "it",
                  "label": "next.settingsSpec.server.metadata.groups.1.rows.0.control.options.13.label"
                },
                {
                  "value": "ja",
                  "label": "next.settingsSpec.server.metadata.groups.1.rows.0.control.options.14.label"
                },
                {
                  "value": "ko",
                  "label": "next.settingsSpec.server.metadata.groups.1.rows.0.control.options.15.label"
                },
                {
                  "value": "no",
                  "label": "next.settingsSpec.server.metadata.groups.1.rows.0.control.options.16.label"
                },
                {
                  "value": "pl",
                  "label": "next.settingsSpec.server.metadata.groups.1.rows.0.control.options.17.label"
                },
                {
                  "value": "pt",
                  "label": "next.settingsSpec.server.metadata.groups.1.rows.0.control.options.18.label"
                },
                {
                  "value": "ro",
                  "label": "next.settingsSpec.server.metadata.groups.1.rows.0.control.options.19.label"
                },
                {
                  "value": "ru",
                  "label": "next.settingsSpec.server.metadata.groups.1.rows.0.control.options.20.label"
                },
                {
                  "value": "sk",
                  "label": "next.settingsSpec.server.metadata.groups.1.rows.0.control.options.21.label"
                },
                {
                  "value": "es",
                  "label": "next.settingsSpec.server.metadata.groups.1.rows.0.control.options.22.label"
                },
                {
                  "value": "sv",
                  "label": "next.settingsSpec.server.metadata.groups.1.rows.0.control.options.23.label"
                },
                {
                  "value": "th",
                  "label": "next.settingsSpec.server.metadata.groups.1.rows.0.control.options.24.label"
                },
                {
                  "value": "tr",
                  "label": "next.settingsSpec.server.metadata.groups.1.rows.0.control.options.25.label"
                },
                {
                  "value": "uk",
                  "label": "next.settingsSpec.server.metadata.groups.1.rows.0.control.options.26.label"
                },
                {
                  "value": "vi",
                  "label": "next.settingsSpec.server.metadata.groups.1.rows.0.control.options.27.label"
                }
              ]
            }
          },
          {
            "id": "metadata.region",
            "scope": "server",
            "key": "metadata.default_region",
            "label": "next.settingsSpec.server.metadata.groups.1.rows.1.label",
            "help": "next.settingsSpec.server.metadata.groups.1.rows.1.help",
            "control": {
              "type": "select",
              "default": "US",
              "options": [
                {
                  "value": "US",
                  "label": "next.settingsSpec.server.metadata.groups.1.rows.1.control.options.0.label",
                  "hint": "next.settingsSpec.server.metadata.groups.1.rows.1.control.options.0.hint"
                },
                {
                  "value": "CA",
                  "label": "next.settingsSpec.server.metadata.groups.1.rows.1.control.options.1.label",
                  "hint": "next.settingsSpec.server.metadata.groups.1.rows.1.control.options.1.hint"
                },
                {
                  "value": "GB",
                  "label": "next.settingsSpec.server.metadata.groups.1.rows.1.control.options.2.label",
                  "hint": "next.settingsSpec.server.metadata.groups.1.rows.1.control.options.2.hint"
                },
                {
                  "value": "AU",
                  "label": "next.settingsSpec.server.metadata.groups.1.rows.1.control.options.3.label",
                  "hint": "next.settingsSpec.server.metadata.groups.1.rows.1.control.options.3.hint"
                },
                {
                  "value": "NZ",
                  "label": "next.settingsSpec.server.metadata.groups.1.rows.1.control.options.4.label",
                  "hint": "next.settingsSpec.server.metadata.groups.1.rows.1.control.options.4.hint"
                },
                {
                  "value": "IE",
                  "label": "next.settingsSpec.server.metadata.groups.1.rows.1.control.options.5.label",
                  "hint": "next.settingsSpec.server.metadata.groups.1.rows.1.control.options.5.hint"
                },
                {
                  "value": "FR",
                  "label": "next.settingsSpec.server.metadata.groups.1.rows.1.control.options.6.label",
                  "hint": "next.settingsSpec.server.metadata.groups.1.rows.1.control.options.6.hint"
                },
                {
                  "value": "DE",
                  "label": "next.settingsSpec.server.metadata.groups.1.rows.1.control.options.7.label",
                  "hint": "next.settingsSpec.server.metadata.groups.1.rows.1.control.options.7.hint"
                },
                {
                  "value": "ES",
                  "label": "next.settingsSpec.server.metadata.groups.1.rows.1.control.options.8.label",
                  "hint": "next.settingsSpec.server.metadata.groups.1.rows.1.control.options.8.hint"
                },
                {
                  "value": "IT",
                  "label": "next.settingsSpec.server.metadata.groups.1.rows.1.control.options.9.label",
                  "hint": "next.settingsSpec.server.metadata.groups.1.rows.1.control.options.9.hint"
                },
                {
                  "value": "PT",
                  "label": "next.settingsSpec.server.metadata.groups.1.rows.1.control.options.10.label",
                  "hint": "next.settingsSpec.server.metadata.groups.1.rows.1.control.options.10.hint"
                },
                {
                  "value": "NL",
                  "label": "next.settingsSpec.server.metadata.groups.1.rows.1.control.options.11.label",
                  "hint": "next.settingsSpec.server.metadata.groups.1.rows.1.control.options.11.hint"
                },
                {
                  "value": "BE",
                  "label": "next.settingsSpec.server.metadata.groups.1.rows.1.control.options.12.label",
                  "hint": "next.settingsSpec.server.metadata.groups.1.rows.1.control.options.12.hint"
                },
                {
                  "value": "CH",
                  "label": "next.settingsSpec.server.metadata.groups.1.rows.1.control.options.13.label",
                  "hint": "next.settingsSpec.server.metadata.groups.1.rows.1.control.options.13.hint"
                },
                {
                  "value": "AT",
                  "label": "next.settingsSpec.server.metadata.groups.1.rows.1.control.options.14.label",
                  "hint": "next.settingsSpec.server.metadata.groups.1.rows.1.control.options.14.hint"
                },
                {
                  "value": "SE",
                  "label": "next.settingsSpec.server.metadata.groups.1.rows.1.control.options.15.label",
                  "hint": "next.settingsSpec.server.metadata.groups.1.rows.1.control.options.15.hint"
                },
                {
                  "value": "NO",
                  "label": "next.settingsSpec.server.metadata.groups.1.rows.1.control.options.16.label",
                  "hint": "next.settingsSpec.server.metadata.groups.1.rows.1.control.options.16.hint"
                },
                {
                  "value": "DK",
                  "label": "next.settingsSpec.server.metadata.groups.1.rows.1.control.options.17.label",
                  "hint": "next.settingsSpec.server.metadata.groups.1.rows.1.control.options.17.hint"
                },
                {
                  "value": "FI",
                  "label": "next.settingsSpec.server.metadata.groups.1.rows.1.control.options.18.label",
                  "hint": "next.settingsSpec.server.metadata.groups.1.rows.1.control.options.18.hint"
                },
                {
                  "value": "PL",
                  "label": "next.settingsSpec.server.metadata.groups.1.rows.1.control.options.19.label",
                  "hint": "next.settingsSpec.server.metadata.groups.1.rows.1.control.options.19.hint"
                },
                {
                  "value": "CZ",
                  "label": "next.settingsSpec.server.metadata.groups.1.rows.1.control.options.20.label",
                  "hint": "next.settingsSpec.server.metadata.groups.1.rows.1.control.options.20.hint"
                },
                {
                  "value": "BR",
                  "label": "next.settingsSpec.server.metadata.groups.1.rows.1.control.options.21.label",
                  "hint": "next.settingsSpec.server.metadata.groups.1.rows.1.control.options.21.hint"
                },
                {
                  "value": "MX",
                  "label": "next.settingsSpec.server.metadata.groups.1.rows.1.control.options.22.label",
                  "hint": "next.settingsSpec.server.metadata.groups.1.rows.1.control.options.22.hint"
                },
                {
                  "value": "AR",
                  "label": "next.settingsSpec.server.metadata.groups.1.rows.1.control.options.23.label",
                  "hint": "next.settingsSpec.server.metadata.groups.1.rows.1.control.options.23.hint"
                },
                {
                  "value": "JP",
                  "label": "next.settingsSpec.server.metadata.groups.1.rows.1.control.options.24.label",
                  "hint": "next.settingsSpec.server.metadata.groups.1.rows.1.control.options.24.hint"
                },
                {
                  "value": "KR",
                  "label": "next.settingsSpec.server.metadata.groups.1.rows.1.control.options.25.label",
                  "hint": "next.settingsSpec.server.metadata.groups.1.rows.1.control.options.25.hint"
                },
                {
                  "value": "CN",
                  "label": "next.settingsSpec.server.metadata.groups.1.rows.1.control.options.26.label",
                  "hint": "next.settingsSpec.server.metadata.groups.1.rows.1.control.options.26.hint"
                },
                {
                  "value": "IN",
                  "label": "next.settingsSpec.server.metadata.groups.1.rows.1.control.options.27.label",
                  "hint": "next.settingsSpec.server.metadata.groups.1.rows.1.control.options.27.hint"
                },
                {
                  "value": "ZA",
                  "label": "next.settingsSpec.server.metadata.groups.1.rows.1.control.options.28.label",
                  "hint": "next.settingsSpec.server.metadata.groups.1.rows.1.control.options.28.hint"
                },
                {
                  "value": "TR",
                  "label": "next.settingsSpec.server.metadata.groups.1.rows.1.control.options.29.label",
                  "hint": "next.settingsSpec.server.metadata.groups.1.rows.1.control.options.29.hint"
                },
                {
                  "value": "RU",
                  "label": "next.settingsSpec.server.metadata.groups.1.rows.1.control.options.30.label",
                  "hint": "next.settingsSpec.server.metadata.groups.1.rows.1.control.options.30.hint"
                },
                {
                  "value": "UA",
                  "label": "next.settingsSpec.server.metadata.groups.1.rows.1.control.options.31.label",
                  "hint": "next.settingsSpec.server.metadata.groups.1.rows.1.control.options.31.hint"
                }
              ]
            }
          }
        ]
      },
      {
        "title": "next.settingsSpec.server.metadata.groups.2.title",
        "rows": [
          {
            "id": "metadata.tmdb",
            "scope": "server",
            "key": "metadata.tmdb_read_token",
            "label": "next.settingsSpec.server.metadata.groups.2.rows.0.label",
            "help": "next.settingsSpec.server.metadata.groups.2.rows.0.help",
            "control": {
              "type": "text",
              "secret": true,
              "placeholder": "next.settingsSpec.server.metadata.groups.2.rows.0.control.placeholder",
              "default": ""
            }
          },
          {
            "id": "metadata.tvdb",
            "scope": "server",
            "key": "metadata.tvdb_project_key",
            "label": "next.settingsSpec.server.metadata.groups.2.rows.1.label",
            "help": "next.settingsSpec.server.metadata.groups.2.rows.1.help",
            "control": {
              "type": "text",
              "secret": true,
              "placeholder": "next.settingsSpec.server.metadata.groups.2.rows.1.control.placeholder",
              "default": ""
            }
          },
          {
            "id": "metadata.noKey",
            "scope": "server",
            "key": "metadata.keyless_providers",
            "label": "next.settingsSpec.server.metadata.groups.2.rows.2.label",
            "help": "next.settingsSpec.server.metadata.groups.2.rows.2.help",
            "control": {
              "type": "status"
            }
          }
        ]
      },
      {
        "title": "next.settingsSpec.server.metadata.groups.3.title",
        "rows": [
          {
            "id": "metadata.artworkPrefer",
            "scope": "server",
            "key": "metadata.artwork_preference",
            "label": "next.settingsSpec.server.metadata.groups.3.rows.0.label",
            "help": "next.settingsSpec.server.metadata.groups.3.rows.0.help",
            "control": {
              "type": "select",
              "default": "folders",
              "options": [
                {
                  "value": "folders",
                  "label": "next.settingsSpec.server.metadata.groups.3.rows.0.control.options.0.label",
                  "hint": "next.settingsSpec.server.metadata.groups.3.rows.0.control.options.0.hint"
                },
                {
                  "value": "online",
                  "label": "next.settingsSpec.server.metadata.groups.3.rows.0.control.options.1.label",
                  "hint": "next.settingsSpec.server.metadata.groups.3.rows.0.control.options.1.hint"
                }
              ]
            }
          },
          {
            "id": "metadata.artworkLanguage",
            "scope": "server",
            "key": "metadata.artwork_language",
            "label": "next.settingsSpec.server.metadata.groups.3.rows.1.label",
            "control": {
              "type": "select",
              "default": "same",
              "options": [
                {
                  "value": "same",
                  "label": "next.settingsSpec.server.metadata.groups.3.rows.1.control.options.0.label"
                },
                {
                  "value": "en",
                  "label": "next.settingsSpec.server.metadata.groups.3.rows.1.control.options.1.label"
                },
                {
                  "value": "textless",
                  "label": "next.settingsSpec.server.metadata.groups.3.rows.1.control.options.2.label"
                }
              ]
            }
          }
        ]
      },
      {
        "title": "next.settingsSpec.server.metadata.groups.4.title",
        "rows": [
          {
            "id": "metadata.osGet",
            "scope": "server",
            "key": "metadata.opensubtitles_get_key",
            "label": "next.settingsSpec.server.metadata.groups.4.rows.0.label",
            "control": {
              "type": "action",
              "style": "glass",
              "label": "next.settingsSpec.server.metadata.groups.4.rows.0.control.label"
            }
          },
          {
            "id": "metadata.osKey",
            "scope": "server",
            "key": "metadata.opensubtitles_api_key",
            "label": "next.settingsSpec.server.metadata.groups.4.rows.1.label",
            "help": "next.settingsSpec.server.metadata.groups.4.rows.1.help",
            "control": {
              "type": "text",
              "secret": true,
              "placeholder": "next.settingsSpec.server.metadata.groups.4.rows.1.control.placeholder",
              "default": ""
            }
          },
          {
            "id": "metadata.osUser",
            "scope": "server",
            "key": "metadata.opensubtitles_username",
            "label": "next.settingsSpec.server.metadata.groups.4.rows.2.label",
            "help": "next.settingsSpec.server.metadata.groups.4.rows.2.help",
            "control": {
              "type": "text",
              "placeholder": "next.settingsSpec.server.metadata.groups.4.rows.2.control.placeholder"
            }
          },
          {
            "id": "metadata.osPass",
            "scope": "server",
            "key": "metadata.opensubtitles_password",
            "label": "next.settingsSpec.server.metadata.groups.4.rows.3.label",
            "help": "next.settingsSpec.server.metadata.groups.4.rows.3.help",
            "control": {
              "type": "text",
              "secret": true,
              "placeholder": "next.settingsSpec.server.metadata.groups.4.rows.3.control.placeholder",
              "default": ""
            }
          },
          {
            "id": "metadata.osStatus",
            "scope": "server",
            "key": "metadata.opensubtitles_status",
            "label": "next.settingsSpec.server.metadata.groups.4.rows.4.label",
            "control": {
              "type": "status"
            }
          },
          {
            "id": "metadata.osTest",
            "scope": "server",
            "key": "metadata.opensubtitles_test",
            "label": "next.settingsSpec.server.metadata.groups.4.rows.5.label",
            "control": {
              "type": "action",
              "style": "glass",
              "label": "next.settingsSpec.server.metadata.groups.4.rows.5.control.label"
            }
          }
        ]
      }
    ],
    "advanced": {
      "groups": [
        {
          "title": "next.settingsSpec.server.metadata.advanced.groups.0.title",
          "rows": [
            {
              "id": "metadata.refresh",
              "scope": "server",
              "key": "metadata.refresh_policy",
              "label": "next.settingsSpec.server.metadata.advanced.groups.0.rows.0.label",
              "help": "next.settingsSpec.server.metadata.advanced.groups.0.rows.0.help",
              "control": {
                "type": "select",
                "default": "regular",
                "options": [
                  {
                    "value": "regular",
                    "label": "next.settingsSpec.server.metadata.advanced.groups.0.rows.0.control.options.0.label",
                    "hint": "next.settingsSpec.server.metadata.advanced.groups.0.rows.0.control.options.0.hint"
                  },
                  {
                    "value": "recent",
                    "label": "next.settingsSpec.server.metadata.advanced.groups.0.rows.0.control.options.1.label",
                    "hint": "next.settingsSpec.server.metadata.advanced.groups.0.rows.0.control.options.1.hint"
                  },
                  {
                    "value": "never",
                    "label": "next.settingsSpec.server.metadata.advanced.groups.0.rows.0.control.options.2.label",
                    "hint": "next.settingsSpec.server.metadata.advanced.groups.0.rows.0.control.options.2.hint"
                  }
                ]
              }
            },
            {
              "id": "metadata.refreshAll",
              "scope": "server",
              "key": "metadata.refresh_all",
              "label": "next.settingsSpec.server.metadata.advanced.groups.0.rows.1.label",
              "help": "next.settingsSpec.server.metadata.advanced.groups.0.rows.1.help",
              "control": {
                "type": "action",
                "style": "glass",
                "label": "next.settingsSpec.server.metadata.advanced.groups.0.rows.1.control.label",
                "confirm": "next.settingsSpec.server.metadata.advanced.groups.0.rows.1.control.confirm"
              }
            }
          ]
        },
        {
          "title": "next.settingsSpec.server.metadata.advanced.groups.1.title",
          "rows": [
            {
              "id": "metadata.fallbackLanguage",
              "scope": "server",
              "key": "metadata.fallback_language",
              "label": "next.settingsSpec.server.metadata.advanced.groups.1.rows.0.label",
              "control": {
                "type": "select",
                "default": "en",
                "options": [
                  {
                    "value": "en",
                    "label": "next.settingsSpec.server.metadata.advanced.groups.1.rows.0.control.options.0.label"
                  },
                  {
                    "value": "original",
                    "label": "next.settingsSpec.server.metadata.advanced.groups.1.rows.0.control.options.1.label"
                  },
                  {
                    "value": "blank",
                    "label": "next.settingsSpec.server.metadata.advanced.groups.1.rows.0.control.options.2.label"
                  }
                ]
              }
            },
            {
              "id": "metadata.animeTitles",
              "scope": "server",
              "key": "metadata.anime_titles",
              "label": "next.settingsSpec.server.metadata.advanced.groups.1.rows.1.label",
              "help": "next.settingsSpec.server.metadata.advanced.groups.1.rows.1.help",
              "control": {
                "type": "select",
                "default": "language",
                "options": [
                  {
                    "value": "language",
                    "label": "next.settingsSpec.server.metadata.advanced.groups.1.rows.1.control.options.0.label"
                  },
                  {
                    "value": "english",
                    "label": "next.settingsSpec.server.metadata.advanced.groups.1.rows.1.control.options.1.label",
                    "hint": "next.settingsSpec.server.metadata.advanced.groups.1.rows.1.control.options.1.hint"
                  },
                  {
                    "value": "romaji",
                    "label": "next.settingsSpec.server.metadata.advanced.groups.1.rows.1.control.options.2.label",
                    "hint": "next.settingsSpec.server.metadata.advanced.groups.1.rows.1.control.options.2.hint"
                  },
                  {
                    "value": "native",
                    "label": "next.settingsSpec.server.metadata.advanced.groups.1.rows.1.control.options.3.label",
                    "hint": "next.settingsSpec.server.metadata.advanced.groups.1.rows.1.control.options.3.hint"
                  }
                ]
              }
            }
          ]
        },
        {
          "title": "next.settingsSpec.server.metadata.advanced.groups.2.title",
          "rows": [
            {
              "id": "metadata.artworkLimit",
              "scope": "server",
              "key": "metadata.local_artwork_limit_gb",
              "label": "next.settingsSpec.server.metadata.advanced.groups.2.rows.0.label",
              "help": "next.settingsSpec.server.metadata.advanced.groups.2.rows.0.help",
              "control": {
                "type": "select",
                "default": "2",
                "options": [
                  {
                    "value": "1",
                    "label": "next.settingsSpec.server.metadata.advanced.groups.2.rows.0.control.options.0.label"
                  },
                  {
                    "value": "2",
                    "label": "next.settingsSpec.server.metadata.advanced.groups.2.rows.0.control.options.1.label"
                  },
                  {
                    "value": "5",
                    "label": "next.settingsSpec.server.metadata.advanced.groups.2.rows.0.control.options.2.label"
                  },
                  {
                    "value": "10",
                    "label": "next.settingsSpec.server.metadata.advanced.groups.2.rows.0.control.options.3.label"
                  },
                  {
                    "value": "25",
                    "label": "next.settingsSpec.server.metadata.advanced.groups.2.rows.0.control.options.4.label"
                  }
                ]
              }
            },
            {
              "id": "metadata.artworkSize",
              "scope": "server",
              "key": "metadata.artwork_max_edge",
              "label": "next.settingsSpec.server.metadata.advanced.groups.2.rows.1.label",
              "help": "next.settingsSpec.server.metadata.advanced.groups.2.rows.1.help",
              "control": {
                "type": "select",
                "default": "standard",
                "options": [
                  {
                    "value": "small",
                    "label": "next.settingsSpec.server.metadata.advanced.groups.2.rows.1.control.options.0.label",
                    "hint": "next.settingsSpec.server.metadata.advanced.groups.2.rows.1.control.options.0.hint"
                  },
                  {
                    "value": "standard",
                    "label": "next.settingsSpec.server.metadata.advanced.groups.2.rows.1.control.options.1.label",
                    "hint": "next.settingsSpec.server.metadata.advanced.groups.2.rows.1.control.options.1.hint"
                  },
                  {
                    "value": "large",
                    "label": "next.settingsSpec.server.metadata.advanced.groups.2.rows.1.control.options.2.label",
                    "hint": "next.settingsSpec.server.metadata.advanced.groups.2.rows.1.control.options.2.hint"
                  }
                ]
              }
            },
            {
              "id": "metadata.castPhotos",
              "scope": "server",
              "key": "metadata.cast_photos",
              "label": "next.settingsSpec.server.metadata.advanced.groups.2.rows.2.label",
              "help": "next.settingsSpec.server.metadata.advanced.groups.2.rows.2.help",
              "control": {
                "type": "toggle",
                "default": true
              }
            }
          ]
        },
        {
          "title": "next.settingsSpec.server.metadata.advanced.groups.3.title",
          "rows": [
            {
              "id": "metadata.acoustid",
              "scope": "server",
              "key": "metadata.acoustid_key",
              "label": "next.settingsSpec.server.metadata.advanced.groups.3.rows.0.label",
              "help": "next.settingsSpec.server.metadata.advanced.groups.3.rows.0.help",
              "control": {
                "type": "text",
                "secret": true,
                "placeholder": "next.settingsSpec.server.metadata.advanced.groups.3.rows.0.control.placeholder",
                "default": ""
              }
            }
          ]
        }
      ]
    },
    "configOnly": [
      {
        "key": "metadata.refresh_budget_per_hour",
        "label": "next.settingsSpec.server.metadata.configOnly.0.label"
      },
      {
        "key": "metadata.refresh_budget_per_day",
        "label": "next.settingsSpec.server.metadata.configOnly.1.label"
      }
    ]
  },
  {
    "scope": "server",
    "id": "segments",
    "navGroup": "Your media",
    "icon": "skip",
    "kind": "settings",
    "title": "next.settingsSpec.server.segments.title",
    "groups": [
      {
        "title": "next.settingsSpec.server.segments.groups.0.title",
        "rows": [
          {
            "id": "segments.mode",
            "scope": "server",
            "key": "library.segment_detection",
            "label": "next.settingsSpec.server.segments.groups.0.rows.0.label",
            "control": {
              "type": "select",
              "default": "standard",
              "options": [
                {
                  "value": "standard",
                  "label": "next.settingsSpec.server.segments.groups.0.rows.0.control.options.0.label",
                  "hint": "next.settingsSpec.server.segments.groups.0.rows.0.control.options.0.hint"
                },
                {
                  "value": "thorough",
                  "label": "next.settingsSpec.server.segments.groups.0.rows.0.control.options.1.label",
                  "hint": "next.settingsSpec.server.segments.groups.0.rows.0.control.options.1.hint"
                },
                {
                  "value": "off",
                  "label": "next.settingsSpec.server.segments.groups.0.rows.0.control.options.2.label",
                  "hint": "next.settingsSpec.server.segments.groups.0.rows.0.control.options.2.hint"
                }
              ]
            }
          },
          {
            "id": "segments.status",
            "scope": "server",
            "key": "segments.progress",
            "label": "next.settingsSpec.server.segments.groups.0.rows.1.label",
            "control": {
              "type": "status"
            }
          }
        ]
      },
      {
        "title": "next.settingsSpec.server.segments.groups.1.title",
        "rows": [
          {
            "id": "segments.libraries",
            "scope": "server",
            "key": "segments.libraries",
            "label": "next.settingsSpec.server.segments.groups.1.rows.0.label",
            "help": "next.settingsSpec.server.segments.groups.1.rows.0.help",
            "control": {
              "type": "list",
              "itemAction": {
                "label": "next.settingsSpec.server.segments.groups.1.rows.0.control.itemAction.label"
              }
            }
          },
          {
            "id": "segments.review",
            "scope": "server",
            "key": "segments.review",
            "label": "next.settingsSpec.server.segments.groups.1.rows.1.label",
            "control": {
              "type": "link",
              "label": "next.settingsSpec.server.segments.groups.1.rows.1.control.label"
            }
          }
        ]
      }
    ],
    "advanced": {
      "groups": [
        {
          "title": "next.settingsSpec.server.segments.advanced.groups.0.title",
          "rows": [
            {
              "id": "segments.viewerFloor",
              "scope": "server",
              "key": "segments.viewer_confidence_percent",
              "label": "next.settingsSpec.server.segments.advanced.groups.0.rows.0.label",
              "help": "next.settingsSpec.server.segments.advanced.groups.0.rows.0.help",
              "control": {
                "type": "select",
                "default": "50",
                "options": [
                  {
                    "value": "70",
                    "label": "next.settingsSpec.server.segments.advanced.groups.0.rows.0.control.options.0.label",
                    "hint": "next.settingsSpec.server.segments.advanced.groups.0.rows.0.control.options.0.hint"
                  },
                  {
                    "value": "50",
                    "label": "next.settingsSpec.server.segments.advanced.groups.0.rows.0.control.options.1.label",
                    "hint": "next.settingsSpec.server.segments.advanced.groups.0.rows.0.control.options.1.hint"
                  },
                  {
                    "value": "30",
                    "label": "next.settingsSpec.server.segments.advanced.groups.0.rows.0.control.options.2.label",
                    "hint": "next.settingsSpec.server.segments.advanced.groups.0.rows.0.control.options.2.hint"
                  }
                ]
              }
            },
            {
              "id": "segments.autoFloor",
              "scope": "server",
              "key": "segments.automatic_confidence_percent",
              "label": "next.settingsSpec.server.segments.advanced.groups.0.rows.1.label",
              "help": "next.settingsSpec.server.segments.advanced.groups.0.rows.1.help",
              "control": {
                "type": "select",
                "default": "85",
                "options": [
                  {
                    "value": "95",
                    "label": "next.settingsSpec.server.segments.advanced.groups.0.rows.1.control.options.0.label",
                    "hint": "next.settingsSpec.server.segments.advanced.groups.0.rows.1.control.options.0.hint"
                  },
                  {
                    "value": "85",
                    "label": "next.settingsSpec.server.segments.advanced.groups.0.rows.1.control.options.1.label",
                    "hint": "next.settingsSpec.server.segments.advanced.groups.0.rows.1.control.options.1.hint"
                  },
                  {
                    "value": "70",
                    "label": "next.settingsSpec.server.segments.advanced.groups.0.rows.1.control.options.2.label",
                    "hint": "next.settingsSpec.server.segments.advanced.groups.0.rows.1.control.options.2.hint"
                  }
                ]
              }
            }
          ]
        },
        {
          "title": "next.settingsSpec.server.segments.advanced.groups.1.title",
          "rows": [
            {
              "id": "segments.chapters",
              "scope": "server",
              "key": "segments.use_chapter_names",
              "label": "next.settingsSpec.server.segments.advanced.groups.1.rows.0.label",
              "help": "next.settingsSpec.server.segments.advanced.groups.1.rows.0.help",
              "control": {
                "type": "toggle",
                "default": true
              }
            },
            {
              "id": "segments.introWindow",
              "scope": "server",
              "key": "segments.intro_window",
              "label": "next.settingsSpec.server.segments.advanced.groups.1.rows.1.label",
              "help": "next.settingsSpec.server.segments.advanced.groups.1.rows.1.help",
              "control": {
                "type": "select",
                "default": "usual",
                "options": [
                  {
                    "value": "usual",
                    "label": "next.settingsSpec.server.segments.advanced.groups.1.rows.1.control.options.0.label",
                    "hint": "next.settingsSpec.server.segments.advanced.groups.1.rows.1.control.options.0.hint"
                  },
                  {
                    "value": "long",
                    "label": "next.settingsSpec.server.segments.advanced.groups.1.rows.1.control.options.1.label",
                    "hint": "next.settingsSpec.server.segments.advanced.groups.1.rows.1.control.options.1.hint"
                  }
                ]
              }
            },
            {
              "id": "segments.creditsWindow",
              "scope": "server",
              "key": "segments.credits_window",
              "label": "next.settingsSpec.server.segments.advanced.groups.1.rows.2.label",
              "control": {
                "type": "select",
                "default": "usual",
                "options": [
                  {
                    "value": "usual",
                    "label": "next.settingsSpec.server.segments.advanced.groups.1.rows.2.control.options.0.label"
                  },
                  {
                    "value": "long",
                    "label": "next.settingsSpec.server.segments.advanced.groups.1.rows.2.control.options.1.label",
                    "hint": "next.settingsSpec.server.segments.advanced.groups.1.rows.2.control.options.1.hint"
                  }
                ]
              }
            },
            {
              "id": "segments.filmCreditsWindow",
              "scope": "server",
              "key": "segments.film_credits_window",
              "label": "next.settingsSpec.server.segments.advanced.groups.1.rows.3.label",
              "help": "next.settingsSpec.server.segments.advanced.groups.1.rows.3.help",
              "control": {
                "type": "select",
                "default": "usual",
                "options": [
                  {
                    "value": "usual",
                    "label": "next.settingsSpec.server.segments.advanced.groups.1.rows.3.control.options.0.label"
                  },
                  {
                    "value": "long",
                    "label": "next.settingsSpec.server.segments.advanced.groups.1.rows.3.control.options.1.label",
                    "hint": "next.settingsSpec.server.segments.advanced.groups.1.rows.3.control.options.1.hint"
                  }
                ]
              }
            },
            {
              "id": "segments.shortestEpisode",
              "scope": "server",
              "key": "segments.shortest_episode_minutes",
              "label": "next.settingsSpec.server.segments.advanced.groups.1.rows.4.label",
              "control": {
                "type": "number",
                "default": 2,
                "unit": "next.settingsSpec.server.segments.advanced.groups.1.rows.4.control.unit",
                "min": 1,
                "max": 30,
                "step": 1
              }
            },
            {
              "id": "segments.shortestFilm",
              "scope": "server",
              "key": "segments.shortest_film_minutes",
              "label": "next.settingsSpec.server.segments.advanced.groups.1.rows.5.label",
              "control": {
                "type": "number",
                "default": 10,
                "unit": "next.settingsSpec.server.segments.advanced.groups.1.rows.5.control.unit",
                "min": 5,
                "max": 60,
                "step": 5
              }
            }
          ]
        },
        {
          "title": "next.settingsSpec.server.segments.advanced.groups.2.title",
          "rows": [
            {
              "id": "segments.libraryModes",
              "scope": "server",
              "key": "segments.library_modes",
              "label": "next.settingsSpec.server.segments.advanced.groups.2.rows.0.label",
              "control": {
                "type": "matrix",
                "columns": [
                  {
                    "value": "server",
                    "label": "next.settingsSpec.server.segments.advanced.groups.2.rows.0.control.columns.0.label"
                  },
                  {
                    "value": "standard",
                    "label": "next.settingsSpec.server.segments.advanced.groups.2.rows.0.control.columns.1.label",
                    "hint": "next.settingsSpec.server.segments.advanced.groups.2.rows.0.control.columns.1.hint"
                  },
                  {
                    "value": "thorough",
                    "label": "next.settingsSpec.server.segments.advanced.groups.2.rows.0.control.columns.2.label",
                    "hint": "next.settingsSpec.server.segments.advanced.groups.2.rows.0.control.columns.2.hint"
                  },
                  {
                    "value": "off",
                    "label": "next.settingsSpec.server.segments.advanced.groups.2.rows.0.control.columns.3.label"
                  }
                ],
                "items": [
                  {
                    "id": "tv",
                    "label": "next.settingsSpec.server.segments.advanced.groups.2.rows.0.control.items.0.label",
                    "default": "server"
                  },
                  {
                    "id": "anime",
                    "label": "next.settingsSpec.server.segments.advanced.groups.2.rows.0.control.items.1.label",
                    "default": "server"
                  },
                  {
                    "id": "movies",
                    "label": "next.settingsSpec.server.segments.advanced.groups.2.rows.0.control.items.2.label",
                    "default": "server"
                  }
                ]
              }
            }
          ]
        },
        {
          "title": "next.settingsSpec.server.segments.advanced.groups.3.title",
          "rows": [
            {
              "id": "segments.keepOld",
              "scope": "server",
              "key": "segments.keep_days",
              "label": "next.settingsSpec.server.segments.advanced.groups.3.rows.0.label",
              "help": "next.settingsSpec.server.segments.advanced.groups.3.rows.0.help",
              "control": {
                "type": "select",
                "default": "7",
                "options": [
                  {
                    "value": "1",
                    "label": "next.settingsSpec.server.segments.advanced.groups.3.rows.0.control.options.0.label"
                  },
                  {
                    "value": "7",
                    "label": "next.settingsSpec.server.segments.advanced.groups.3.rows.0.control.options.1.label"
                  },
                  {
                    "value": "30",
                    "label": "next.settingsSpec.server.segments.advanced.groups.3.rows.0.control.options.2.label"
                  },
                  {
                    "value": "90",
                    "label": "next.settingsSpec.server.segments.advanced.groups.3.rows.0.control.options.3.label"
                  },
                  {
                    "value": "365",
                    "label": "next.settingsSpec.server.segments.advanced.groups.3.rows.0.control.options.4.label"
                  }
                ]
              }
            },
            {
              "id": "segments.redetectAll",
              "scope": "server",
              "key": "segments.redetect_all",
              "label": "next.settingsSpec.server.segments.advanced.groups.3.rows.1.label",
              "help": "next.settingsSpec.server.segments.advanced.groups.3.rows.1.help",
              "control": {
                "type": "action",
                "style": "glass",
                "label": "next.settingsSpec.server.segments.advanced.groups.3.rows.1.control.label",
                "confirm": "next.settingsSpec.server.segments.advanced.groups.3.rows.1.control.confirm"
              }
            },
            {
              "id": "segments.retryFailed",
              "scope": "server",
              "key": "segments.retry_failed",
              "label": "next.settingsSpec.server.segments.advanced.groups.3.rows.2.label",
              "control": {
                "type": "action",
                "style": "glass",
                "label": "next.settingsSpec.server.segments.advanced.groups.3.rows.2.control.label"
              }
            }
          ]
        }
      ]
    },
    "configOnly": []
  },
  {
    "scope": "server",
    "id": "live",
    "navGroup": "Your media",
    "icon": "tv",
    "kind": "settings",
    "title": "next.settingsSpec.server.live.title",
    "groups": [
      {
        "title": "next.settingsSpec.server.live.groups.0.title",
        "rows": [
          {
            "id": "live.sources",
            "scope": "server",
            "key": "live.sources",
            "label": "next.settingsSpec.server.live.groups.0.rows.0.label",
            "control": {
              "type": "list",
              "addLabel": "next.settingsSpec.server.live.groups.0.rows.0.control.addLabel",
              "itemAction": {
                "label": "next.settingsSpec.server.live.groups.0.rows.0.control.itemAction.label"
              }
            }
          }
        ]
      },
      {
        "title": "next.settingsSpec.server.live.groups.1.title",
        "rows": [
          {
            "id": "live.stability",
            "scope": "server",
            "key": "live.buffering",
            "label": "next.settingsSpec.server.live.groups.1.rows.0.label",
            "control": {
              "type": "select",
              "default": "balanced",
              "options": [
                {
                  "value": "balanced",
                  "label": "next.settingsSpec.server.live.groups.1.rows.0.control.options.0.label",
                  "hint": "next.settingsSpec.server.live.groups.1.rows.0.control.options.0.hint",
                  "sets": {
                    "live.buffer": 8,
                    "live.retry": 60
                  }
                },
                {
                  "value": "smooth",
                  "label": "next.settingsSpec.server.live.groups.1.rows.0.control.options.1.label",
                  "hint": "next.settingsSpec.server.live.groups.1.rows.0.control.options.1.hint",
                  "sets": {
                    "live.buffer": 20,
                    "live.retry": 180
                  }
                },
                {
                  "value": "live",
                  "label": "next.settingsSpec.server.live.groups.1.rows.0.control.options.2.label",
                  "hint": "next.settingsSpec.server.live.groups.1.rows.0.control.options.2.hint",
                  "sets": {
                    "live.buffer": 3,
                    "live.retry": 30
                  }
                },
                {
                  "value": "custom",
                  "label": "next.settingsSpec.server.live.groups.1.rows.0.control.options.3.label"
                }
              ]
            }
          },
          {
            "id": "live.guideDays",
            "scope": "server",
            "key": "live.guide_days",
            "label": "next.settingsSpec.server.live.groups.1.rows.1.label",
            "help": "next.settingsSpec.server.live.groups.1.rows.1.help",
            "control": {
              "type": "select",
              "default": 7,
              "options": [
                {
                  "value": 3,
                  "label": "next.settingsSpec.server.live.groups.1.rows.1.control.options.0.label"
                },
                {
                  "value": 7,
                  "label": "next.settingsSpec.server.live.groups.1.rows.1.control.options.1.label"
                },
                {
                  "value": 14,
                  "label": "next.settingsSpec.server.live.groups.1.rows.1.control.options.2.label"
                }
              ]
            }
          },
          {
            "id": "live.rewind",
            "scope": "server",
            "key": "live.timeshift_minutes",
            "label": "next.settingsSpec.server.live.groups.1.rows.2.label",
            "help": "next.settingsSpec.server.live.groups.1.rows.2.help",
            "control": {
              "type": "select",
              "default": "30",
              "options": [
                {
                  "value": "10",
                  "label": "next.settingsSpec.server.live.groups.1.rows.2.control.options.0.label"
                },
                {
                  "value": "30",
                  "label": "next.settingsSpec.server.live.groups.1.rows.2.control.options.1.label"
                },
                {
                  "value": "60",
                  "label": "next.settingsSpec.server.live.groups.1.rows.2.control.options.2.label"
                },
                {
                  "value": "120",
                  "label": "next.settingsSpec.server.live.groups.1.rows.2.control.options.3.label"
                }
              ]
            }
          },
          {
            "id": "live.logos",
            "scope": "server",
            "key": "live.logo_import",
            "label": "next.settingsSpec.server.live.groups.1.rows.3.label",
            "help": "next.settingsSpec.server.live.groups.1.rows.3.help",
            "control": {
              "type": "toggle",
              "default": true
            }
          }
        ]
      },
      {
        "title": "next.settingsSpec.server.live.groups.2.title",
        "rows": [
          {
            "id": "live.src.enabled",
            "scope": "server",
            "key": "live.source.enabled",
            "label": "next.settingsSpec.server.live.groups.2.rows.0.label",
            "help": "next.settingsSpec.server.live.groups.2.rows.0.help",
            "control": {
              "type": "toggle",
              "default": true
            }
          },
          {
            "id": "live.src.access",
            "scope": "server",
            "key": "live.source.viewer_access",
            "label": "next.settingsSpec.server.live.groups.2.rows.1.label",
            "control": {
              "type": "select",
              "default": "everyone",
              "options": [
                {
                  "value": "everyone",
                  "label": "next.settingsSpec.server.live.groups.2.rows.1.control.options.0.label"
                },
                {
                  "value": "me",
                  "label": "next.settingsSpec.server.live.groups.2.rows.1.control.options.1.label"
                }
              ]
            }
          },
          {
            "id": "live.src.guide",
            "scope": "server",
            "key": "live.source.guide_from",
            "label": "next.settingsSpec.server.live.groups.2.rows.2.label",
            "help": "next.settingsSpec.server.live.groups.2.rows.2.help",
            "control": {
              "type": "select",
              "default": "own",
              "options": [
                {
                  "value": "own",
                  "label": "next.settingsSpec.server.live.groups.2.rows.2.control.options.0.label"
                },
                {
                  "value": "harbor",
                  "label": "next.settingsSpec.server.live.groups.2.rows.2.control.options.1.label",
                  "hint": "next.settingsSpec.server.live.groups.2.rows.2.control.options.1.hint"
                },
                {
                  "value": "url",
                  "label": "next.settingsSpec.server.live.groups.2.rows.2.control.options.2.label"
                }
              ]
            },
            "children": [
              {
                "id": "live.src.guideUrl",
                "scope": "server",
                "key": "live.source.guide_url",
                "enableWhen": {
                  "id": "live.src.guide",
                  "in": [
                    "url"
                  ]
                },
                "label": "next.settingsSpec.server.live.groups.2.rows.2.children.0.label",
                "help": "next.settingsSpec.server.live.groups.2.rows.2.children.0.help",
                "control": {
                  "type": "text",
                  "placeholder": "next.settingsSpec.server.live.groups.2.rows.2.children.0.control.placeholder"
                }
              }
            ]
          },
          {
            "id": "live.src.refresh",
            "scope": "server",
            "key": "live.source.refresh",
            "label": "next.settingsSpec.server.live.groups.2.rows.3.label",
            "control": {
              "type": "select",
              "default": "6h",
              "options": [
                {
                  "value": "1h",
                  "label": "next.settingsSpec.server.live.groups.2.rows.3.control.options.0.label"
                },
                {
                  "value": "6h",
                  "label": "next.settingsSpec.server.live.groups.2.rows.3.control.options.1.label"
                },
                {
                  "value": "12h",
                  "label": "next.settingsSpec.server.live.groups.2.rows.3.control.options.2.label"
                },
                {
                  "value": "1d",
                  "label": "next.settingsSpec.server.live.groups.2.rows.3.control.options.3.label"
                }
              ]
            }
          },
          {
            "id": "live.src.logos",
            "scope": "server",
            "key": "live.source.logos",
            "label": "next.settingsSpec.server.live.groups.2.rows.4.label",
            "control": {
              "type": "select",
              "default": "follow",
              "options": [
                {
                  "value": "follow",
                  "label": "next.settingsSpec.server.live.groups.2.rows.4.control.options.0.label"
                },
                {
                  "value": "import",
                  "label": "next.settingsSpec.server.live.groups.2.rows.4.control.options.1.label"
                },
                {
                  "value": "replace",
                  "label": "next.settingsSpec.server.live.groups.2.rows.4.control.options.2.label"
                },
                {
                  "value": "none",
                  "label": "next.settingsSpec.server.live.groups.2.rows.4.control.options.3.label"
                }
              ]
            },
            "children": [
              {
                "id": "live.src.importNow",
                "scope": "server",
                "enableWhen": {
                  "id": "live.src.logos",
                  "in": [
                    "follow",
                    "import",
                    "replace"
                  ]
                },
                "label": "next.settingsSpec.server.live.groups.2.rows.4.children.0.label",
                "help": "next.settingsSpec.server.live.groups.2.rows.4.children.0.help",
                "control": {
                  "type": "action",
                  "style": "glass",
                  "label": "next.settingsSpec.server.live.groups.2.rows.4.children.0.control.label"
                }
              }
            ]
          }
        ]
      },
      {
        "title": "next.settingsSpec.server.live.groups.3.title",
        "rows": [
          {
            "id": "live.src.channels",
            "scope": "server",
            "key": "live.source.channels",
            "label": "next.settingsSpec.server.live.groups.3.rows.0.label",
            "help": "next.settingsSpec.server.live.groups.3.rows.0.help",
            "control": {
              "type": "select",
              "default": "all",
              "options": [
                {
                  "value": "all",
                  "label": "next.settingsSpec.server.live.groups.3.rows.0.control.options.0.label"
                },
                {
                  "value": "groups",
                  "label": "next.settingsSpec.server.live.groups.3.rows.0.control.options.1.label"
                },
                {
                  "value": "picked",
                  "label": "next.settingsSpec.server.live.groups.3.rows.0.control.options.2.label"
                },
                {
                  "value": "filter",
                  "label": "next.settingsSpec.server.live.groups.3.rows.0.control.options.3.label"
                }
              ]
            },
            "children": [
              {
                "id": "live.src.groups",
                "scope": "server",
                "key": "live.source.groups",
                "enableWhen": {
                  "id": "live.src.channels",
                  "in": [
                    "groups"
                  ]
                },
                "label": "next.settingsSpec.server.live.groups.3.rows.0.children.0.label",
                "control": {
                  "type": "matrix",
                  "columns": [
                    {
                      "value": "in",
                      "label": "next.settingsSpec.server.live.groups.3.rows.0.children.0.control.columns.0.label"
                    },
                    {
                      "value": "out",
                      "label": "next.settingsSpec.server.live.groups.3.rows.0.children.0.control.columns.1.label"
                    }
                  ],
                  "items": [
                    {
                      "id": "news",
                      "label": "next.settingsSpec.server.live.groups.3.rows.0.children.0.control.items.0.label",
                      "default": "in"
                    },
                    {
                      "id": "sports",
                      "label": "next.settingsSpec.server.live.groups.3.rows.0.children.0.control.items.1.label",
                      "default": "in"
                    },
                    {
                      "id": "movies",
                      "label": "next.settingsSpec.server.live.groups.3.rows.0.children.0.control.items.2.label",
                      "default": "in"
                    },
                    {
                      "id": "kids",
                      "label": "next.settingsSpec.server.live.groups.3.rows.0.children.0.control.items.3.label",
                      "default": "in"
                    },
                    {
                      "id": "docs",
                      "label": "next.settingsSpec.server.live.groups.3.rows.0.children.0.control.items.4.label",
                      "default": "in"
                    }
                  ]
                }
              },
              {
                "id": "live.src.pick",
                "scope": "server",
                "key": "live.source.picked_channels",
                "enableWhen": {
                  "id": "live.src.channels",
                  "in": [
                    "picked"
                  ]
                },
                "label": "next.settingsSpec.server.live.groups.3.rows.0.children.1.label",
                "control": {
                  "type": "action",
                  "style": "glass",
                  "label": "next.settingsSpec.server.live.groups.3.rows.0.children.1.control.label"
                }
              },
              {
                "id": "live.src.fCats",
                "scope": "server",
                "key": "live.source.filter_categories",
                "enableWhen": {
                  "id": "live.src.channels",
                  "in": [
                    "filter"
                  ]
                },
                "label": "next.settingsSpec.server.live.groups.3.rows.0.children.2.label",
                "control": {
                  "type": "text",
                  "placeholder": "next.settingsSpec.server.live.groups.3.rows.0.children.2.control.placeholder"
                }
              },
              {
                "id": "live.src.fCountries",
                "scope": "server",
                "key": "live.source.filter_countries",
                "enableWhen": {
                  "id": "live.src.channels",
                  "in": [
                    "filter"
                  ]
                },
                "label": "next.settingsSpec.server.live.groups.3.rows.0.children.3.label",
                "control": {
                  "type": "text",
                  "placeholder": "next.settingsSpec.server.live.groups.3.rows.0.children.3.control.placeholder"
                }
              },
              {
                "id": "live.src.fWords",
                "scope": "server",
                "key": "live.source.filter_words",
                "enableWhen": {
                  "id": "live.src.channels",
                  "in": [
                    "filter"
                  ]
                },
                "label": "next.settingsSpec.server.live.groups.3.rows.0.children.4.label",
                "control": {
                  "type": "text",
                  "placeholder": "next.settingsSpec.server.live.groups.3.rows.0.children.4.control.placeholder"
                }
              }
            ]
          }
        ]
      },
      {
        "title": "next.settingsSpec.server.live.groups.4.title",
        "rows": [
          {
            "id": "live.src.numbering",
            "scope": "server",
            "key": "live.source.numbering",
            "label": "next.settingsSpec.server.live.groups.4.rows.0.label",
            "help": "next.settingsSpec.server.live.groups.4.rows.0.help",
            "control": {
              "type": "select",
              "default": "source",
              "options": [
                {
                  "value": "source",
                  "label": "next.settingsSpec.server.live.groups.4.rows.0.control.options.0.label"
                },
                {
                  "value": "ordered",
                  "label": "next.settingsSpec.server.live.groups.4.rows.0.control.options.1.label"
                }
              ]
            },
            "children": [
              {
                "id": "live.src.numberStart",
                "scope": "server",
                "key": "live.source.number_start",
                "enableWhen": {
                  "id": "live.src.numbering",
                  "in": [
                    "ordered"
                  ]
                },
                "label": "next.settingsSpec.server.live.groups.4.rows.0.children.0.label",
                "help": "next.settingsSpec.server.live.groups.4.rows.0.children.0.help",
                "control": {
                  "type": "number",
                  "default": 1,
                  "unit": "next.settingsSpec.server.live.groups.4.rows.0.children.0.control.unit",
                  "min": 1,
                  "max": 99999,
                  "step": 1
                }
              },
              {
                "id": "live.src.numberStep",
                "scope": "server",
                "key": "live.source.number_step",
                "enableWhen": {
                  "id": "live.src.numbering",
                  "in": [
                    "ordered"
                  ]
                },
                "label": "next.settingsSpec.server.live.groups.4.rows.0.children.1.label",
                "help": "next.settingsSpec.server.live.groups.4.rows.0.children.1.help",
                "control": {
                  "type": "number",
                  "default": 1,
                  "unit": "next.settingsSpec.server.live.groups.4.rows.0.children.1.control.unit",
                  "min": 1,
                  "max": 100,
                  "step": 1
                }
              }
            ]
          },
          {
            "id": "live.src.map",
            "scope": "server",
            "key": "live.source.channel_map",
            "label": "next.settingsSpec.server.live.groups.4.rows.1.label",
            "control": {
              "type": "link",
              "label": "next.settingsSpec.server.live.groups.4.rows.1.control.label"
            }
          }
        ]
      },
      {
        "title": "next.settingsSpec.server.live.groups.5.title",
        "rows": [
          {
            "id": "live.rec.tuners",
            "scope": "server",
            "key": "recording.tuners",
            "label": "next.settingsSpec.server.live.groups.5.rows.0.label",
            "help": "next.settingsSpec.server.live.groups.5.rows.0.help",
            "control": {
              "type": "status"
            }
          },
          {
            "id": "live.rec.who",
            "scope": "server",
            "key": "recording.permissions",
            "label": "next.settingsSpec.server.live.groups.5.rows.1.label",
            "help": "next.settingsSpec.server.live.groups.5.rows.1.help",
            "control": {
              "type": "list",
              "itemAction": {
                "label": "next.settingsSpec.server.live.groups.5.rows.1.control.itemAction.label"
              }
            }
          },
          {
            "id": "live.rec.rules",
            "scope": "server",
            "key": "recording.rules",
            "label": "next.settingsSpec.server.live.groups.5.rows.2.label",
            "help": "next.settingsSpec.server.live.groups.5.rows.2.help",
            "control": {
              "type": "list",
              "addLabel": "next.settingsSpec.server.live.groups.5.rows.2.control.addLabel",
              "itemAction": {
                "label": "next.settingsSpec.server.live.groups.5.rows.2.control.itemAction.label"
              }
            }
          }
        ]
      },
      {
        "title": "next.settingsSpec.server.live.groups.6.title",
        "rows": [
          {
            "id": "live.rec.extra",
            "scope": "server",
            "key": "recording.padding",
            "label": "next.settingsSpec.server.live.groups.6.rows.0.label",
            "help": "next.settingsSpec.server.live.groups.6.rows.0.help",
            "control": {
              "type": "select",
              "default": "standard",
              "options": [
                {
                  "value": "standard",
                  "label": "next.settingsSpec.server.live.groups.6.rows.0.control.options.0.label",
                  "hint": "next.settingsSpec.server.live.groups.6.rows.0.control.options.0.hint",
                  "sets": {
                    "live.rec.early": 1,
                    "live.rec.late": 3
                  }
                },
                {
                  "value": "exact",
                  "label": "next.settingsSpec.server.live.groups.6.rows.0.control.options.1.label",
                  "sets": {
                    "live.rec.early": 0,
                    "live.rec.late": 0
                  }
                },
                {
                  "value": "generous",
                  "label": "next.settingsSpec.server.live.groups.6.rows.0.control.options.2.label",
                  "hint": "next.settingsSpec.server.live.groups.6.rows.0.control.options.2.hint",
                  "sets": {
                    "live.rec.early": 5,
                    "live.rec.late": 10
                  }
                },
                {
                  "value": "sport",
                  "label": "next.settingsSpec.server.live.groups.6.rows.0.control.options.3.label",
                  "hint": "next.settingsSpec.server.live.groups.6.rows.0.control.options.3.hint",
                  "sets": {
                    "live.rec.early": 1,
                    "live.rec.late": 15
                  }
                },
                {
                  "value": "custom",
                  "label": "next.settingsSpec.server.live.groups.6.rows.0.control.options.4.label"
                }
              ]
            }
          },
          {
            "id": "live.rec.conflict",
            "scope": "server",
            "key": "recording.tuner_conflict",
            "label": "next.settingsSpec.server.live.groups.6.rows.1.label",
            "control": {
              "type": "select",
              "default": "keep",
              "options": [
                {
                  "value": "keep",
                  "label": "next.settingsSpec.server.live.groups.6.rows.1.control.options.0.label",
                  "hint": "next.settingsSpec.server.live.groups.6.rows.1.control.options.0.hint"
                },
                {
                  "value": "stop",
                  "label": "next.settingsSpec.server.live.groups.6.rows.1.control.options.1.label",
                  "hint": "next.settingsSpec.server.live.groups.6.rows.1.control.options.1.hint"
                }
              ]
            }
          },
          {
            "id": "live.rec.keep",
            "scope": "server",
            "key": "recording.keep",
            "label": "next.settingsSpec.server.live.groups.6.rows.2.label",
            "help": "next.settingsSpec.server.live.groups.6.rows.2.help",
            "control": {
              "type": "select",
              "default": "all",
              "options": [
                {
                  "value": "all",
                  "label": "next.settingsSpec.server.live.groups.6.rows.2.control.options.0.label",
                  "sets": {
                    "live.rec.days": 0,
                    "live.rec.episodes": 0
                  }
                },
                {
                  "value": "d30",
                  "label": "next.settingsSpec.server.live.groups.6.rows.2.control.options.1.label",
                  "sets": {
                    "live.rec.days": 30,
                    "live.rec.episodes": 0
                  }
                },
                {
                  "value": "d90",
                  "label": "next.settingsSpec.server.live.groups.6.rows.2.control.options.2.label",
                  "sets": {
                    "live.rec.days": 90,
                    "live.rec.episodes": 0
                  }
                },
                {
                  "value": "n5",
                  "label": "next.settingsSpec.server.live.groups.6.rows.2.control.options.3.label",
                  "sets": {
                    "live.rec.days": 0,
                    "live.rec.episodes": 5
                  }
                },
                {
                  "value": "n10",
                  "label": "next.settingsSpec.server.live.groups.6.rows.2.control.options.4.label",
                  "sets": {
                    "live.rec.days": 0,
                    "live.rec.episodes": 10
                  }
                },
                {
                  "value": "custom",
                  "label": "next.settingsSpec.server.live.groups.6.rows.2.control.options.5.label"
                }
              ]
            }
          }
        ]
      },
      {
        "title": "next.settingsSpec.server.live.groups.7.title",
        "rows": [
          {
            "id": "live.rec.folder",
            "scope": "server",
            "key": "recording.folder",
            "label": "next.settingsSpec.server.live.groups.7.rows.0.label",
            "help": "next.settingsSpec.server.live.groups.7.rows.0.help",
            "control": {
              "type": "folder"
            }
          },
          {
            "id": "live.rec.space",
            "scope": "server",
            "key": "recording.space",
            "label": "next.settingsSpec.server.live.groups.7.rows.1.label",
            "help": "next.settingsSpec.server.live.groups.7.rows.1.help",
            "control": {
              "type": "select",
              "default": "free10",
              "options": [
                {
                  "value": "free10",
                  "label": "next.settingsSpec.server.live.groups.7.rows.1.control.options.0.label",
                  "sets": {
                    "live.rec.floor": 10,
                    "live.rec.cap": 0
                  }
                },
                {
                  "value": "up250",
                  "label": "next.settingsSpec.server.live.groups.7.rows.1.control.options.1.label",
                  "sets": {
                    "live.rec.floor": 10,
                    "live.rec.cap": 250
                  }
                },
                {
                  "value": "up500",
                  "label": "next.settingsSpec.server.live.groups.7.rows.1.control.options.2.label",
                  "sets": {
                    "live.rec.floor": 10,
                    "live.rec.cap": 500
                  }
                },
                {
                  "value": "up1000",
                  "label": "next.settingsSpec.server.live.groups.7.rows.1.control.options.3.label",
                  "sets": {
                    "live.rec.floor": 10,
                    "live.rec.cap": 1000
                  }
                },
                {
                  "value": "custom",
                  "label": "next.settingsSpec.server.live.groups.7.rows.1.control.options.4.label"
                }
              ]
            }
          }
        ]
      },
      {
        "title": "next.settingsSpec.server.live.groups.8.title",
        "rows": [
          {
            "id": "live.lib.channels",
            "scope": "server",
            "key": "library_channels",
            "label": "next.settingsSpec.server.live.groups.8.rows.0.label",
            "control": {
              "type": "list",
              "addLabel": "next.settingsSpec.server.live.groups.8.rows.0.control.addLabel",
              "itemAction": {
                "label": "next.settingsSpec.server.live.groups.8.rows.0.control.itemAction.label"
              }
            }
          }
        ]
      }
    ],
    "advanced": {
      "groups": [
        {
          "title": "next.settingsSpec.server.live.advanced.groups.0.title",
          "rows": [
            {
              "id": "live.buffer",
              "scope": "server",
              "key": "live.stream_buffer_seconds",
              "label": "next.settingsSpec.server.live.advanced.groups.0.rows.0.label",
              "help": "next.settingsSpec.server.live.advanced.groups.0.rows.0.help",
              "control": {
                "type": "number",
                "default": 8,
                "unit": "next.settingsSpec.server.live.advanced.groups.0.rows.0.control.unit",
                "min": 0,
                "max": 120,
                "step": 1
              }
            },
            {
              "id": "live.retry",
              "scope": "server",
              "key": "live.retry_seconds",
              "label": "next.settingsSpec.server.live.advanced.groups.0.rows.1.label",
              "control": {
                "type": "number",
                "default": 60,
                "unit": "next.settingsSpec.server.live.advanced.groups.0.rows.1.control.unit",
                "min": 5,
                "max": 3600,
                "step": 5
              }
            },
            {
              "id": "live.userAgent",
              "scope": "server",
              "key": "live.user_agent",
              "label": "next.settingsSpec.server.live.advanced.groups.0.rows.2.label",
              "help": "next.settingsSpec.server.live.advanced.groups.0.rows.2.help",
              "control": {
                "type": "text",
                "placeholder": "next.settingsSpec.server.live.advanced.groups.0.rows.2.control.placeholder"
              }
            },
            {
              "id": "live.discovery",
              "scope": "server",
              "key": "live.discover_tuners",
              "label": "next.settingsSpec.server.live.advanced.groups.0.rows.3.label",
              "help": "next.settingsSpec.server.live.advanced.groups.0.rows.3.help",
              "control": {
                "type": "toggle",
                "default": true
              }
            }
          ]
        },
        {
          "title": "next.settingsSpec.server.live.advanced.groups.1.title",
          "rows": [
            {
              "id": "live.limit.channels",
              "scope": "server",
              "key": "live.import_limits.channels",
              "label": "next.settingsSpec.server.live.advanced.groups.1.rows.0.label",
              "help": "next.settingsSpec.server.live.advanced.groups.1.rows.0.help",
              "control": {
                "type": "select",
                "default": 50000,
                "options": [
                  {
                    "value": 5000,
                    "label": "next.settingsSpec.server.live.advanced.groups.1.rows.0.control.options.0.label"
                  },
                  {
                    "value": 10000,
                    "label": "next.settingsSpec.server.live.advanced.groups.1.rows.0.control.options.1.label"
                  },
                  {
                    "value": 50000,
                    "label": "next.settingsSpec.server.live.advanced.groups.1.rows.0.control.options.2.label"
                  },
                  {
                    "value": 100000,
                    "label": "next.settingsSpec.server.live.advanced.groups.1.rows.0.control.options.3.label",
                    "hint": "next.settingsSpec.server.live.advanced.groups.1.rows.0.control.options.3.hint"
                  }
                ]
              }
            },
            {
              "id": "live.limit.programs",
              "scope": "server",
              "key": "live.import_limits.programs",
              "label": "next.settingsSpec.server.live.advanced.groups.1.rows.1.label",
              "help": "next.settingsSpec.server.live.advanced.groups.1.rows.1.help",
              "control": {
                "type": "select",
                "default": 5000000,
                "options": [
                  {
                    "value": 500000,
                    "label": "next.settingsSpec.server.live.advanced.groups.1.rows.1.control.options.0.label"
                  },
                  {
                    "value": 1000000,
                    "label": "next.settingsSpec.server.live.advanced.groups.1.rows.1.control.options.1.label"
                  },
                  {
                    "value": 5000000,
                    "label": "next.settingsSpec.server.live.advanced.groups.1.rows.1.control.options.2.label"
                  },
                  {
                    "value": 10000000,
                    "label": "next.settingsSpec.server.live.advanced.groups.1.rows.1.control.options.3.label",
                    "hint": "next.settingsSpec.server.live.advanced.groups.1.rows.1.control.options.3.hint"
                  }
                ]
              }
            },
            {
              "id": "live.limit.file",
              "scope": "server",
              "key": "live.import_limits.file_mb",
              "label": "next.settingsSpec.server.live.advanced.groups.1.rows.2.label",
              "help": "next.settingsSpec.server.live.advanced.groups.1.rows.2.help",
              "control": {
                "type": "select",
                "default": 512,
                "options": [
                  {
                    "value": 64,
                    "label": "next.settingsSpec.server.live.advanced.groups.1.rows.2.control.options.0.label"
                  },
                  {
                    "value": 128,
                    "label": "next.settingsSpec.server.live.advanced.groups.1.rows.2.control.options.1.label"
                  },
                  {
                    "value": 256,
                    "label": "next.settingsSpec.server.live.advanced.groups.1.rows.2.control.options.2.label"
                  },
                  {
                    "value": 512,
                    "label": "next.settingsSpec.server.live.advanced.groups.1.rows.2.control.options.3.label"
                  },
                  {
                    "value": 1024,
                    "label": "next.settingsSpec.server.live.advanced.groups.1.rows.2.control.options.4.label",
                    "hint": "next.settingsSpec.server.live.advanced.groups.1.rows.2.control.options.4.hint"
                  }
                ]
              }
            }
          ]
        },
        {
          "title": "next.settingsSpec.server.live.advanced.groups.2.title",
          "rows": [
            {
              "id": "live.src.streams",
              "scope": "server",
              "key": "live.source.streams",
              "label": "next.settingsSpec.server.live.advanced.groups.2.rows.0.label",
              "help": "next.settingsSpec.server.live.advanced.groups.2.rows.0.help",
              "control": {
                "type": "select",
                "default": "auto",
                "options": [
                  {
                    "value": "auto",
                    "label": "next.settingsSpec.server.live.advanced.groups.2.rows.0.control.options.0.label"
                  },
                  {
                    "value": 1,
                    "label": "next.settingsSpec.server.live.advanced.groups.2.rows.0.control.options.1.label"
                  },
                  {
                    "value": 2,
                    "label": "next.settingsSpec.server.live.advanced.groups.2.rows.0.control.options.2.label"
                  },
                  {
                    "value": 3,
                    "label": "next.settingsSpec.server.live.advanced.groups.2.rows.0.control.options.3.label"
                  },
                  {
                    "value": 4,
                    "label": "next.settingsSpec.server.live.advanced.groups.2.rows.0.control.options.4.label"
                  },
                  {
                    "value": 6,
                    "label": "next.settingsSpec.server.live.advanced.groups.2.rows.0.control.options.5.label"
                  },
                  {
                    "value": 8,
                    "label": "next.settingsSpec.server.live.advanced.groups.2.rows.0.control.options.6.label"
                  }
                ]
              }
            },
            {
              "id": "live.src.stability",
              "scope": "server",
              "key": "live.source.buffering",
              "label": "next.settingsSpec.server.live.advanced.groups.2.rows.1.label",
              "control": {
                "type": "select",
                "default": "follow",
                "options": [
                  {
                    "value": "follow",
                    "label": "next.settingsSpec.server.live.advanced.groups.2.rows.1.control.options.0.label"
                  },
                  {
                    "value": "smooth",
                    "label": "next.settingsSpec.server.live.advanced.groups.2.rows.1.control.options.1.label"
                  },
                  {
                    "value": "balanced",
                    "label": "next.settingsSpec.server.live.advanced.groups.2.rows.1.control.options.2.label"
                  },
                  {
                    "value": "live",
                    "label": "next.settingsSpec.server.live.advanced.groups.2.rows.1.control.options.3.label"
                  }
                ]
              }
            },
            {
              "id": "live.src.userAgent",
              "scope": "server",
              "key": "live.source.user_agent",
              "label": "next.settingsSpec.server.live.advanced.groups.2.rows.2.label",
              "control": {
                "type": "text",
                "placeholder": "next.settingsSpec.server.live.advanced.groups.2.rows.2.control.placeholder"
              }
            }
          ]
        },
        {
          "title": "next.settingsSpec.server.live.advanced.groups.3.title",
          "rows": [
            {
              "id": "live.rec.early",
              "scope": "server",
              "key": "recording.start_early_minutes",
              "label": "next.settingsSpec.server.live.advanced.groups.3.rows.0.label",
              "control": {
                "type": "number",
                "default": 1,
                "unit": "next.settingsSpec.server.live.advanced.groups.3.rows.0.control.unit",
                "min": 0,
                "max": 30,
                "step": 1
              }
            },
            {
              "id": "live.rec.late",
              "scope": "server",
              "key": "recording.finish_late_minutes",
              "label": "next.settingsSpec.server.live.advanced.groups.3.rows.1.label",
              "control": {
                "type": "number",
                "default": 3,
                "unit": "next.settingsSpec.server.live.advanced.groups.3.rows.1.control.unit",
                "min": 0,
                "max": 120,
                "step": 1
              }
            },
            {
              "id": "live.rec.days",
              "scope": "server",
              "key": "recording.retention_days",
              "label": "next.settingsSpec.server.live.advanced.groups.3.rows.2.label",
              "help": "next.settingsSpec.server.live.advanced.groups.3.rows.2.help",
              "control": {
                "type": "number",
                "default": 0,
                "unit": "next.settingsSpec.server.live.advanced.groups.3.rows.2.control.unit",
                "min": 0,
                "max": 3650,
                "step": 1
              }
            },
            {
              "id": "live.rec.episodes",
              "scope": "server",
              "key": "recording.episode_limit",
              "label": "next.settingsSpec.server.live.advanced.groups.3.rows.3.label",
              "help": "next.settingsSpec.server.live.advanced.groups.3.rows.3.help",
              "control": {
                "type": "number",
                "default": 0,
                "unit": "next.settingsSpec.server.live.advanced.groups.3.rows.3.control.unit",
                "min": 0,
                "max": 500,
                "step": 1
              }
            },
            {
              "id": "live.rec.floor",
              "scope": "server",
              "key": "recording.free_space_gb",
              "label": "next.settingsSpec.server.live.advanced.groups.3.rows.4.label",
              "help": "next.settingsSpec.server.live.advanced.groups.3.rows.4.help",
              "control": {
                "type": "number",
                "default": 10,
                "unit": "next.settingsSpec.server.live.advanced.groups.3.rows.4.control.unit",
                "min": 0,
                "max": 5000,
                "step": 5
              }
            },
            {
              "id": "live.rec.cap",
              "scope": "server",
              "key": "recording.cap_gb",
              "label": "next.settingsSpec.server.live.advanced.groups.3.rows.5.label",
              "help": "next.settingsSpec.server.live.advanced.groups.3.rows.5.help",
              "control": {
                "type": "number",
                "default": 0,
                "unit": "next.settingsSpec.server.live.advanced.groups.3.rows.5.control.unit",
                "min": 0,
                "max": 100000,
                "step": 10
              }
            }
          ]
        },
        {
          "title": "next.settingsSpec.server.live.advanced.groups.4.title",
          "rows": [
            {
              "id": "live.rec.layout",
              "scope": "server",
              "key": "recording.folder_layout",
              "label": "next.settingsSpec.server.live.advanced.groups.4.rows.0.label",
              "control": {
                "type": "select",
                "default": "own",
                "options": [
                  {
                    "value": "own",
                    "label": "next.settingsSpec.server.live.advanced.groups.4.rows.0.control.options.0.label",
                    "sets": {
                      "live.rec.showTemplate": "",
                      "live.rec.movieTemplate": ""
                    }
                  },
                  {
                    "value": "season",
                    "label": "next.settingsSpec.server.live.advanced.groups.4.rows.0.control.options.1.label",
                    "sets": {
                      "live.rec.showTemplate": "{series}/Season {season:02}/{series} S{season:02}E{episode:02}",
                      "live.rec.movieTemplate": "{title} ({year})/{title} ({year})"
                    },
                    "hint": "next.settingsSpec.server.live.advanced.groups.4.rows.0.control.options.1.hint"
                  },
                  {
                    "value": "flat",
                    "label": "next.settingsSpec.server.live.advanced.groups.4.rows.0.control.options.2.label",
                    "sets": {
                      "live.rec.showTemplate": "{series} - S{season:02}E{episode:02}",
                      "live.rec.movieTemplate": "{title} ({year})"
                    }
                  },
                  {
                    "value": "custom",
                    "label": "next.settingsSpec.server.live.advanced.groups.4.rows.0.control.options.3.label"
                  }
                ]
              }
            },
            {
              "id": "live.rec.showTemplate",
              "scope": "server",
              "key": "recording.show_folder",
              "label": "next.settingsSpec.server.live.advanced.groups.4.rows.1.label",
              "help": "next.settingsSpec.server.live.advanced.groups.4.rows.1.help",
              "control": {
                "type": "text",
                "placeholder": "next.settingsSpec.server.live.advanced.groups.4.rows.1.control.placeholder"
              }
            },
            {
              "id": "live.rec.movieTemplate",
              "scope": "server",
              "key": "recording.movie_folder",
              "label": "next.settingsSpec.server.live.advanced.groups.4.rows.2.label",
              "help": "next.settingsSpec.server.live.advanced.groups.4.rows.2.help",
              "control": {
                "type": "text",
                "placeholder": "next.settingsSpec.server.live.advanced.groups.4.rows.2.control.placeholder"
              }
            },
            {
              "id": "live.rec.ruleKeep",
              "scope": "server",
              "key": "recording.default_keep",
              "label": "next.settingsSpec.server.live.advanced.groups.4.rows.3.label",
              "help": "next.settingsSpec.server.live.advanced.groups.4.rows.3.help",
              "control": {
                "type": "select",
                "default": "all",
                "options": [
                  {
                    "value": "all",
                    "label": "next.settingsSpec.server.live.advanced.groups.4.rows.3.control.options.0.label"
                  },
                  {
                    "value": "n5",
                    "label": "next.settingsSpec.server.live.advanced.groups.4.rows.3.control.options.1.label"
                  },
                  {
                    "value": "n10",
                    "label": "next.settingsSpec.server.live.advanced.groups.4.rows.3.control.options.2.label"
                  },
                  {
                    "value": "n25",
                    "label": "next.settingsSpec.server.live.advanced.groups.4.rows.3.control.options.3.label"
                  },
                  {
                    "value": "d30",
                    "label": "next.settingsSpec.server.live.advanced.groups.4.rows.3.control.options.4.label"
                  },
                  {
                    "value": "d90",
                    "label": "next.settingsSpec.server.live.advanced.groups.4.rows.3.control.options.5.label"
                  }
                ]
              }
            }
          ]
        }
      ]
    },
    "configOnly": []
  },
  {
    "scope": "server",
    "id": "people",
    "navGroup": "People and access",
    "icon": "people",
    "kind": "page",
    "title": "next.settingsSpec.server.people.title",
    "groups": [
      {
        "title": "next.settingsSpec.server.people.groups.0.title",
        "rows": [
          {
            "id": "people.table",
            "scope": "server",
            "key": "people.accounts",
            "label": "next.settingsSpec.server.people.groups.0.rows.0.label",
            "control": {
              "type": "table",
              "height": 600,
              "addPanel": "invite",
              "addLabel": "next.settingsSpec.server.people.groups.0.rows.0.control.addLabel",
              "empty": "next.settingsSpec.server.people.groups.0.rows.0.control.empty",
              "search": "next.settingsSpec.server.people.groups.0.rows.0.control.search",
              "noun": [
                "next.settingsSpec.server.people.groups.0.rows.0.control.noun.0",
                "next.settingsSpec.server.people.groups.0.rows.0.control.noun.1"
              ],
              "filters": [
                {
                  "id": "role",
                  "label": "next.settingsSpec.server.people.groups.0.rows.0.control.filters.0.label",
                  "options": [
                    {
                      "value": "all",
                      "label": "next.settingsSpec.server.people.groups.0.rows.0.control.filters.0.options.0.label"
                    },
                    {
                      "value": "admin",
                      "label": "next.settingsSpec.server.people.groups.0.rows.0.control.filters.0.options.1.label"
                    },
                    {
                      "value": "member",
                      "label": "next.settingsSpec.server.people.groups.0.rows.0.control.filters.0.options.2.label"
                    }
                  ]
                },
                {
                  "id": "status",
                  "label": "next.settingsSpec.server.people.groups.0.rows.0.control.filters.1.label",
                  "options": [
                    {
                      "value": "all",
                      "label": "next.settingsSpec.server.people.groups.0.rows.0.control.filters.1.options.0.label"
                    },
                    {
                      "value": "now",
                      "label": "next.settingsSpec.server.people.groups.0.rows.0.control.filters.1.options.1.label"
                    },
                    {
                      "value": "month",
                      "label": "next.settingsSpec.server.people.groups.0.rows.0.control.filters.1.options.2.label"
                    },
                    {
                      "value": "idle",
                      "label": "next.settingsSpec.server.people.groups.0.rows.0.control.filters.1.options.3.label"
                    },
                    {
                      "value": "off",
                      "label": "next.settingsSpec.server.people.groups.0.rows.0.control.filters.1.options.4.label"
                    }
                  ]
                }
              ],
              "columns": [
                {
                  "id": "person",
                  "label": "next.settingsSpec.server.people.groups.0.rows.0.control.columns.0.label",
                  "kind": "main",
                  "sort": true,
                  "width": "26.4%"
                },
                {
                  "id": "role",
                  "label": "next.settingsSpec.server.people.groups.0.rows.0.control.columns.1.label",
                  "kind": "badge",
                  "sort": true,
                  "width": "16.9%"
                },
                {
                  "id": "access",
                  "label": "next.settingsSpec.server.people.groups.0.rows.0.control.columns.2.label",
                  "kind": "text",
                  "sort": true,
                  "width": "20.7%"
                },
                {
                  "id": "profiles",
                  "label": "next.settingsSpec.server.people.groups.0.rows.0.control.columns.3.label",
                  "kind": "muted",
                  "sort": true,
                  "width": "19.7%"
                },
                {
                  "id": "active",
                  "label": "next.settingsSpec.server.people.groups.0.rows.0.control.columns.4.label",
                  "kind": "text",
                  "sort": true,
                  "width": "16.5%"
                }
              ],
              "sort": {
                "by": "active",
                "dir": "desc"
              },
              "select": {
                "bulk": [
                  {
                    "label": "next.settingsSpec.server.people.groups.0.rows.0.control.bulk.0.label",
                    "style": "glass",
                    "panel": "bulkAccess"
                  },
                  {
                    "label": "next.settingsSpec.server.people.groups.0.rows.0.control.bulk.1.label",
                    "style": "glass",
                    "confirm": "next.settingsSpec.server.people.groups.0.rows.0.control.bulk.1.confirm"
                  },
                  {
                    "label": "next.settingsSpec.server.people.groups.0.rows.0.control.bulk.2.label",
                    "style": "danger",
                    "confirm": "next.settingsSpec.server.people.groups.0.rows.0.control.bulk.2.confirm"
                  }
                ]
              },
              "rowAction": {
                "label": "next.settingsSpec.server.people.groups.0.rows.0.control.rowAction.label",
                "panel": "person"
              }
            }
          }
        ]
      },
      {
        "title": "next.settingsSpec.server.people.groups.1.title",
        "rows": [
          {
            "id": "people.invitations",
            "scope": "server",
            "key": "people.invitations",
            "label": "next.settingsSpec.server.people.groups.1.rows.0.label",
            "control": {
              "type": "table",
              "height": 360,
              "addPanel": "invite",
              "addLabel": "next.settingsSpec.server.people.groups.1.rows.0.control.addLabel",
              "empty": "next.settingsSpec.server.people.groups.1.rows.0.control.empty",
              "search": "next.settingsSpec.server.people.groups.1.rows.0.control.search",
              "noun": [
                "next.settingsSpec.server.people.groups.1.rows.0.control.noun.0",
                "next.settingsSpec.server.people.groups.1.rows.0.control.noun.1"
              ],
              "filters": [
                {
                  "id": "status",
                  "label": "next.settingsSpec.server.people.groups.1.rows.0.control.filters.0.label",
                  "options": [
                    {
                      "value": "all",
                      "label": "next.settingsSpec.server.people.groups.1.rows.0.control.filters.0.options.0.label"
                    },
                    {
                      "value": "waiting",
                      "label": "next.settingsSpec.server.people.groups.1.rows.0.control.filters.0.options.1.label"
                    },
                    {
                      "value": "expired",
                      "label": "next.settingsSpec.server.people.groups.1.rows.0.control.filters.0.options.2.label"
                    },
                    {
                      "value": "used",
                      "label": "next.settingsSpec.server.people.groups.1.rows.0.control.filters.0.options.3.label"
                    }
                  ]
                }
              ],
              "columns": [
                {
                  "id": "invite",
                  "label": "next.settingsSpec.server.people.groups.1.rows.0.control.columns.0.label",
                  "kind": "main",
                  "sort": true,
                  "width": "31%"
                },
                {
                  "id": "role",
                  "label": "next.settingsSpec.server.people.groups.1.rows.0.control.columns.1.label",
                  "kind": "badge",
                  "sort": true,
                  "width": "18%"
                },
                {
                  "id": "access",
                  "label": "next.settingsSpec.server.people.groups.1.rows.0.control.columns.2.label",
                  "kind": "text",
                  "sort": true,
                  "width": "17%"
                },
                {
                  "id": "sent",
                  "label": "next.settingsSpec.server.people.groups.1.rows.0.control.columns.3.label",
                  "kind": "text",
                  "sort": true,
                  "width": "14%"
                },
                {
                  "id": "status",
                  "label": "next.settingsSpec.server.people.groups.1.rows.0.control.columns.4.label",
                  "kind": "badge",
                  "sort": true,
                  "width": "20%"
                }
              ],
              "sort": {
                "by": "sent",
                "dir": "desc"
              },
              "rowActions": [
                {
                  "label": "next.settingsSpec.server.people.groups.1.rows.0.control.rowActions.0.label",
                  "style": "glass"
                },
                {
                  "label": "next.settingsSpec.server.people.groups.1.rows.0.control.rowActions.1.label",
                  "style": "danger",
                  "confirm": "next.settingsSpec.server.people.groups.1.rows.0.control.rowActions.1.confirm",
                  "remove": true
                }
              ]
            }
          }
        ]
      }
    ],
    "advanced": {
      "groups": [
        {
          "title": "next.settingsSpec.server.people.advanced.groups.0.title",
          "rows": [
            {
              "id": "people.defaultLibraries",
              "scope": "server",
              "key": "people.default_libraries",
              "label": "next.settingsSpec.server.people.advanced.groups.0.rows.0.label",
              "help": "next.settingsSpec.server.people.advanced.groups.0.rows.0.help",
              "control": {
                "type": "select",
                "default": "all",
                "options": [
                  {
                    "value": "all",
                    "label": "next.settingsSpec.server.people.advanced.groups.0.rows.0.control.options.0.label",
                    "hint": "next.settingsSpec.server.people.advanced.groups.0.rows.0.control.options.0.hint"
                  },
                  {
                    "value": "choose",
                    "label": "next.settingsSpec.server.people.advanced.groups.0.rows.0.control.options.1.label",
                    "hint": "next.settingsSpec.server.people.advanced.groups.0.rows.0.control.options.1.hint"
                  }
                ]
              }
            },
            {
              "id": "people.defaultContentLimit",
              "scope": "server",
              "key": "people.default_max_rating",
              "label": "next.settingsSpec.server.people.advanced.groups.0.rows.1.label",
              "control": {
                "type": "select",
                "default": "none",
                "options": [
                  {
                    "value": "none",
                    "label": "next.settingsSpec.server.people.advanced.groups.0.rows.1.control.options.0.label"
                  },
                  {
                    "value": "G",
                    "label": "next.settingsSpec.server.people.advanced.groups.0.rows.1.control.options.1.label",
                    "hint": "next.settingsSpec.server.people.advanced.groups.0.rows.1.control.options.1.hint"
                  },
                  {
                    "value": "PG",
                    "label": "next.settingsSpec.server.people.advanced.groups.0.rows.1.control.options.2.label",
                    "hint": "next.settingsSpec.server.people.advanced.groups.0.rows.1.control.options.2.hint"
                  },
                  {
                    "value": "PG-13",
                    "label": "next.settingsSpec.server.people.advanced.groups.0.rows.1.control.options.3.label",
                    "hint": "next.settingsSpec.server.people.advanced.groups.0.rows.1.control.options.3.hint"
                  },
                  {
                    "value": "R",
                    "label": "next.settingsSpec.server.people.advanced.groups.0.rows.1.control.options.4.label",
                    "hint": "next.settingsSpec.server.people.advanced.groups.0.rows.1.control.options.4.hint"
                  }
                ]
              }
            },
            {
              "id": "people.defaultAwayAccess",
              "scope": "server",
              "key": "people.default_away_access",
              "label": "next.settingsSpec.server.people.advanced.groups.0.rows.2.label",
              "control": {
                "type": "segmented",
                "default": "allowed",
                "options": [
                  {
                    "value": "allowed",
                    "label": "next.settingsSpec.server.people.advanced.groups.0.rows.2.control.options.0.label"
                  },
                  {
                    "value": "home",
                    "label": "next.settingsSpec.server.people.advanced.groups.0.rows.2.control.options.1.label"
                  }
                ]
              }
            }
          ]
        },
        {
          "title": "next.settingsSpec.server.people.advanced.groups.1.title",
          "rows": [
            {
              "id": "people.maxStreamsEach",
              "scope": "server",
              "key": "streaming.max_streams_per_account",
              "label": "next.settingsSpec.server.people.advanced.groups.1.rows.0.label",
              "help": "next.settingsSpec.server.people.advanced.groups.1.rows.0.help",
              "control": {
                "type": "select",
                "default": "unlimited",
                "options": [
                  {
                    "value": "unlimited",
                    "label": "next.settingsSpec.server.people.advanced.groups.1.rows.0.control.options.0.label"
                  },
                  {
                    "value": "1",
                    "label": "next.settingsSpec.server.people.advanced.groups.1.rows.0.control.options.1.label"
                  },
                  {
                    "value": "2",
                    "label": "next.settingsSpec.server.people.advanced.groups.1.rows.0.control.options.2.label"
                  },
                  {
                    "value": "3",
                    "label": "next.settingsSpec.server.people.advanced.groups.1.rows.0.control.options.3.label"
                  },
                  {
                    "value": "4",
                    "label": "next.settingsSpec.server.people.advanced.groups.1.rows.0.control.options.4.label"
                  },
                  {
                    "value": "6",
                    "label": "next.settingsSpec.server.people.advanced.groups.1.rows.0.control.options.5.label"
                  }
                ]
              }
            }
          ]
        },
        {
          "title": "next.settingsSpec.server.people.advanced.groups.2.title",
          "rows": [
            {
              "id": "people.defaultFeatures",
              "scope": "server",
              "key": "people.default_features",
              "label": "next.settingsSpec.server.people.advanced.groups.2.rows.0.label",
              "control": {
                "type": "matrix",
                "columns": [
                  {
                    "value": "allow",
                    "label": "next.settingsSpec.server.people.advanced.groups.2.rows.0.control.columns.0.label",
                    "hint": "next.settingsSpec.server.people.advanced.groups.2.rows.0.control.columns.0.hint"
                  },
                  {
                    "value": "block",
                    "label": "next.settingsSpec.server.people.advanced.groups.2.rows.0.control.columns.1.label",
                    "hint": "next.settingsSpec.server.people.advanced.groups.2.rows.0.control.columns.1.hint"
                  }
                ],
                "items": [
                  {
                    "id": "downloads",
                    "label": "next.settingsSpec.server.people.advanced.groups.2.rows.0.control.items.0.label",
                    "default": "allow"
                  },
                  {
                    "id": "liveTv",
                    "label": "next.settingsSpec.server.people.advanced.groups.2.rows.0.control.items.1.label",
                    "default": "allow"
                  },
                  {
                    "id": "recordings",
                    "label": "next.settingsSpec.server.people.advanced.groups.2.rows.0.control.items.2.label",
                    "default": "allow"
                  },
                  {
                    "id": "watchTogether",
                    "label": "next.settingsSpec.server.people.advanced.groups.2.rows.0.control.items.3.label",
                    "default": "allow"
                  }
                ]
              }
            }
          ]
        },
        {
          "title": "next.settingsSpec.server.people.advanced.groups.3.title",
          "rows": [
            {
              "id": "people.inviteExpiry",
              "scope": "server",
              "key": "people.invitation_days",
              "label": "next.settingsSpec.server.people.advanced.groups.3.rows.0.label",
              "control": {
                "type": "select",
                "default": "7",
                "options": [
                  {
                    "value": "1",
                    "label": "next.settingsSpec.server.people.advanced.groups.3.rows.0.control.options.0.label"
                  },
                  {
                    "value": "3",
                    "label": "next.settingsSpec.server.people.advanced.groups.3.rows.0.control.options.1.label"
                  },
                  {
                    "value": "7",
                    "label": "next.settingsSpec.server.people.advanced.groups.3.rows.0.control.options.2.label"
                  },
                  {
                    "value": "30",
                    "label": "next.settingsSpec.server.people.advanced.groups.3.rows.0.control.options.3.label"
                  }
                ]
              }
            },
            {
              "id": "people.inviteRole",
              "scope": "server",
              "key": "people.invitation_role",
              "label": "next.settingsSpec.server.people.advanced.groups.3.rows.1.label",
              "control": {
                "type": "select",
                "default": "member",
                "options": [
                  {
                    "value": "member",
                    "label": "next.settingsSpec.server.people.advanced.groups.3.rows.1.control.options.0.label"
                  },
                  {
                    "value": "administrator",
                    "label": "next.settingsSpec.server.people.advanced.groups.3.rows.1.control.options.1.label"
                  }
                ]
              }
            },
            {
              "id": "people.whoMayInvite",
              "scope": "server",
              "key": "people.who_may_invite",
              "label": "next.settingsSpec.server.people.advanced.groups.3.rows.2.label",
              "control": {
                "type": "select",
                "default": "administrators",
                "options": [
                  {
                    "value": "administrators",
                    "label": "next.settingsSpec.server.people.advanced.groups.3.rows.2.control.options.0.label"
                  },
                  {
                    "value": "owner",
                    "label": "next.settingsSpec.server.people.advanced.groups.3.rows.2.control.options.1.label"
                  }
                ]
              }
            }
          ]
        },
        {
          "title": "next.settingsSpec.server.people.advanced.groups.4.title",
          "rows": [
            {
              "id": "people.maxProfiles",
              "scope": "server",
              "key": "people.max_profiles",
              "label": "next.settingsSpec.server.people.advanced.groups.4.rows.0.label",
              "help": "next.settingsSpec.server.people.advanced.groups.4.rows.0.help",
              "control": {
                "type": "select",
                "default": "8",
                "options": [
                  {
                    "value": "1",
                    "label": "next.settingsSpec.server.people.advanced.groups.4.rows.0.control.options.0.label"
                  },
                  {
                    "value": "2",
                    "label": "next.settingsSpec.server.people.advanced.groups.4.rows.0.control.options.1.label"
                  },
                  {
                    "value": "4",
                    "label": "next.settingsSpec.server.people.advanced.groups.4.rows.0.control.options.2.label"
                  },
                  {
                    "value": "8",
                    "label": "next.settingsSpec.server.people.advanced.groups.4.rows.0.control.options.3.label"
                  }
                ]
              }
            }
          ]
        },
        {
          "title": "next.settingsSpec.server.people.advanced.groups.5.title",
          "rows": [
            {
              "id": "people.communityActivity",
              "scope": "server",
              "key": "people.community_activity",
              "label": "next.settingsSpec.server.people.advanced.groups.5.rows.0.label",
              "help": "next.settingsSpec.server.people.advanced.groups.5.rows.0.help",
              "control": {
                "type": "toggle",
                "default": false
              }
            }
          ]
        }
      ]
    },
    "panels": {
      "person": {
        "tabs": [
          {
            "id": "account",
            "title": "next.settingsSpec.server.people.panels.person.tabs.0.title",
            "groups": [
              {
                "rows": [
                  {
                    "id": "people.username",
                    "scope": "account",
                    "key": "account.username",
                    "noChanged": true,
                    "label": "next.settingsSpec.server.people.panels.person.tabs.0.groups.0.rows.0.label",
                    "control": {
                      "type": "readonly"
                    }
                  },
                  {
                    "id": "people.email",
                    "scope": "account",
                    "key": "account.email",
                    "label": "next.settingsSpec.server.people.panels.person.tabs.0.groups.0.rows.1.label",
                    "control": {
                      "type": "readonly"
                    }
                  },
                  {
                    "id": "people.notes",
                    "scope": "account",
                    "key": "account.notes",
                    "noChanged": true,
                    "label": "next.settingsSpec.server.people.panels.person.tabs.0.groups.0.rows.2.label",
                    "help": "next.settingsSpec.server.people.panels.person.tabs.0.groups.0.rows.2.help",
                    "control": {
                      "type": "text",
                      "placeholder": "next.settingsSpec.server.people.panels.person.tabs.0.groups.0.rows.2.control.placeholder",
                      "default": ""
                    }
                  },
                  {
                    "id": "people.role",
                    "scope": "account",
                    "key": "account.role",
                    "noChanged": true,
                    "label": "next.settingsSpec.server.people.panels.person.tabs.0.groups.0.rows.3.label",
                    "control": {
                      "type": "select",
                      "default": "member",
                      "options": [
                        {
                          "value": "member",
                          "label": "next.settingsSpec.server.people.panels.person.tabs.0.groups.0.rows.3.control.options.0.label",
                          "hint": "next.settingsSpec.server.people.panels.person.tabs.0.groups.0.rows.3.control.options.0.hint"
                        },
                        {
                          "value": "administrator",
                          "label": "next.settingsSpec.server.people.panels.person.tabs.0.groups.0.rows.3.control.options.1.label",
                          "hint": "next.settingsSpec.server.people.panels.person.tabs.0.groups.0.rows.3.control.options.1.hint"
                        },
                        {
                          "value": "owner",
                          "label": "next.settingsSpec.server.people.panels.person.tabs.0.groups.0.rows.3.control.options.2.label",
                          "hint": "next.settingsSpec.server.people.panels.person.tabs.0.groups.0.rows.3.control.options.2.hint",
                          "locked": true
                        }
                      ]
                    }
                  }
                ]
              },
              {
                "rows": [
                  {
                    "id": "people.turnOff",
                    "scope": "account",
                    "key": "account.turn_off",
                    "label": "next.settingsSpec.server.people.panels.person.tabs.0.groups.1.rows.0.label",
                    "help": "next.settingsSpec.server.people.panels.person.tabs.0.groups.1.rows.0.help",
                    "control": {
                      "type": "action",
                      "style": "glass",
                      "label": "next.settingsSpec.server.people.panels.person.tabs.0.groups.1.rows.0.control.label",
                      "confirm": "next.settingsSpec.server.people.panels.person.tabs.0.groups.1.rows.0.control.confirm",
                      "second": {
                        "label": "next.settingsSpec.server.people.panels.person.tabs.0.groups.1.rows.0.control.second.label",
                        "style": "glass"
                      }
                    }
                  },
                  {
                    "id": "people.delete",
                    "scope": "account",
                    "key": "account.delete",
                    "label": "next.settingsSpec.server.people.panels.person.tabs.0.groups.1.rows.1.label",
                    "control": {
                      "type": "action",
                      "style": "danger",
                      "label": "next.settingsSpec.server.people.panels.person.tabs.0.groups.1.rows.1.control.label",
                      "confirm": "next.settingsSpec.server.people.panels.person.tabs.0.groups.1.rows.1.control.confirm"
                    }
                  }
                ]
              }
            ]
          },
          {
            "id": "access",
            "title": "next.settingsSpec.server.people.panels.person.tabs.1.title",
            "groups": [
              {
                "rows": [
                  {
                    "id": "people.libraries",
                    "scope": "account",
                    "key": "account.libraries",
                    "label": "next.settingsSpec.server.people.panels.person.tabs.1.groups.0.rows.0.label",
                    "control": {
                      "type": "select",
                      "default": "all",
                      "options": [
                        {
                          "value": "all",
                          "label": "next.settingsSpec.server.people.panels.person.tabs.1.groups.0.rows.0.control.options.0.label",
                          "hint": "next.settingsSpec.server.people.panels.person.tabs.1.groups.0.rows.0.control.options.0.hint"
                        },
                        {
                          "value": "some",
                          "label": "next.settingsSpec.server.people.panels.person.tabs.1.groups.0.rows.0.control.options.1.label",
                          "hint": "next.settingsSpec.server.people.panels.person.tabs.1.groups.0.rows.0.control.options.1.hint"
                        }
                      ]
                    },
                    "children": [
                      {
                        "id": "people.libraryList",
                        "scope": "account",
                        "key": "account.library_list",
                        "enableWhen": {
                          "id": "people.libraries",
                          "in": [
                            "some"
                          ]
                        },
                        "noChanged": true,
                        "label": "next.settingsSpec.server.people.panels.person.tabs.1.groups.0.rows.0.children.0.label",
                        "help": "next.settingsSpec.server.people.panels.person.tabs.1.groups.0.rows.0.children.0.help",
                        "control": {
                          "type": "list",
                          "addLabel": "next.settingsSpec.server.people.panels.person.tabs.1.groups.0.rows.0.children.0.control.addLabel",
                          "itemAction": {
                            "label": "next.settingsSpec.server.people.panels.person.tabs.1.groups.0.rows.0.children.0.control.itemAction.label",
                            "style": "glass",
                            "remove": true
                          }
                        }
                      }
                    ]
                  }
                ]
              },
              {
                "title": "next.settingsSpec.server.people.panels.person.tabs.1.groups.1.title",
                "rows": [
                  {
                    "id": "people.maxStreams",
                    "scope": "account",
                    "key": "account.max_streams",
                    "label": "next.settingsSpec.server.people.panels.person.tabs.1.groups.1.rows.0.label",
                    "help": "next.settingsSpec.server.people.panels.person.tabs.1.groups.1.rows.0.help",
                    "control": {
                      "type": "select",
                      "default": "default",
                      "options": [
                        {
                          "value": "default",
                          "label": "next.settingsSpec.server.people.panels.person.tabs.1.groups.1.rows.0.control.options.0.label",
                          "hint": "next.settingsSpec.server.people.panels.person.tabs.1.groups.1.rows.0.control.options.0.hint"
                        },
                        {
                          "value": "unlimited",
                          "label": "next.settingsSpec.server.people.panels.person.tabs.1.groups.1.rows.0.control.options.1.label"
                        },
                        {
                          "value": "1",
                          "label": "next.settingsSpec.server.people.panels.person.tabs.1.groups.1.rows.0.control.options.2.label"
                        },
                        {
                          "value": "2",
                          "label": "next.settingsSpec.server.people.panels.person.tabs.1.groups.1.rows.0.control.options.3.label"
                        },
                        {
                          "value": "3",
                          "label": "next.settingsSpec.server.people.panels.person.tabs.1.groups.1.rows.0.control.options.4.label"
                        },
                        {
                          "value": "4",
                          "label": "next.settingsSpec.server.people.panels.person.tabs.1.groups.1.rows.0.control.options.5.label"
                        },
                        {
                          "value": "6",
                          "label": "next.settingsSpec.server.people.panels.person.tabs.1.groups.1.rows.0.control.options.6.label"
                        }
                      ]
                    }
                  },
                  {
                    "id": "people.awayAccess",
                    "scope": "account",
                    "key": "account.away_access",
                    "label": "next.settingsSpec.server.people.panels.person.tabs.1.groups.1.rows.1.label",
                    "control": {
                      "type": "segmented",
                      "default": "allowed",
                      "options": [
                        {
                          "value": "allowed",
                          "label": "next.settingsSpec.server.people.panels.person.tabs.1.groups.1.rows.1.control.options.0.label"
                        },
                        {
                          "value": "home",
                          "label": "next.settingsSpec.server.people.panels.person.tabs.1.groups.1.rows.1.control.options.1.label"
                        }
                      ]
                    }
                  },
                  {
                    "id": "people.awayQuality",
                    "scope": "account",
                    "key": "account.away_quality",
                    "label": "next.settingsSpec.server.people.panels.person.tabs.1.groups.1.rows.2.label",
                    "control": {
                      "type": "select",
                      "default": "server",
                      "options": [
                        {
                          "value": "server",
                          "label": "next.settingsSpec.server.people.panels.person.tabs.1.groups.1.rows.2.control.options.0.label",
                          "hint": "next.settingsSpec.server.people.panels.person.tabs.1.groups.1.rows.2.control.options.0.hint"
                        },
                        {
                          "value": "4k",
                          "label": "next.settingsSpec.server.people.panels.person.tabs.1.groups.1.rows.2.control.options.1.label",
                          "hint": "next.settingsSpec.server.people.panels.person.tabs.1.groups.1.rows.2.control.options.1.hint"
                        },
                        {
                          "value": "1080h",
                          "label": "next.settingsSpec.server.people.panels.person.tabs.1.groups.1.rows.2.control.options.2.label",
                          "hint": "next.settingsSpec.server.people.panels.person.tabs.1.groups.1.rows.2.control.options.2.hint"
                        },
                        {
                          "value": "1080l",
                          "label": "next.settingsSpec.server.people.panels.person.tabs.1.groups.1.rows.2.control.options.3.label",
                          "hint": "next.settingsSpec.server.people.panels.person.tabs.1.groups.1.rows.2.control.options.3.hint"
                        },
                        {
                          "value": "720h",
                          "label": "next.settingsSpec.server.people.panels.person.tabs.1.groups.1.rows.2.control.options.4.label",
                          "hint": "next.settingsSpec.server.people.panels.person.tabs.1.groups.1.rows.2.control.options.4.hint"
                        },
                        {
                          "value": "720l",
                          "label": "next.settingsSpec.server.people.panels.person.tabs.1.groups.1.rows.2.control.options.5.label",
                          "hint": "next.settingsSpec.server.people.panels.person.tabs.1.groups.1.rows.2.control.options.5.hint"
                        },
                        {
                          "value": "480",
                          "label": "next.settingsSpec.server.people.panels.person.tabs.1.groups.1.rows.2.control.options.6.label",
                          "hint": "next.settingsSpec.server.people.panels.person.tabs.1.groups.1.rows.2.control.options.6.hint"
                        },
                        {
                          "value": "240",
                          "label": "next.settingsSpec.server.people.panels.person.tabs.1.groups.1.rows.2.control.options.7.label",
                          "hint": "next.settingsSpec.server.people.panels.person.tabs.1.groups.1.rows.2.control.options.7.hint"
                        }
                      ]
                    }
                  },
                  {
                    "id": "people.conversions",
                    "scope": "account",
                    "key": "account.conversions",
                    "label": "next.settingsSpec.server.people.panels.person.tabs.1.groups.1.rows.3.label",
                    "help": "next.settingsSpec.server.people.panels.person.tabs.1.groups.1.rows.3.help",
                    "control": {
                      "type": "select",
                      "default": "server",
                      "options": [
                        {
                          "value": "server",
                          "label": "next.settingsSpec.server.people.panels.person.tabs.1.groups.1.rows.3.control.options.0.label",
                          "hint": "next.settingsSpec.server.people.panels.person.tabs.1.groups.1.rows.3.control.options.0.hint"
                        },
                        {
                          "value": "sound",
                          "label": "next.settingsSpec.server.people.panels.person.tabs.1.groups.1.rows.3.control.options.1.label",
                          "hint": "next.settingsSpec.server.people.panels.person.tabs.1.groups.1.rows.3.control.options.1.hint"
                        },
                        {
                          "value": "none",
                          "label": "next.settingsSpec.server.people.panels.person.tabs.1.groups.1.rows.3.control.options.2.label",
                          "hint": "next.settingsSpec.server.people.panels.person.tabs.1.groups.1.rows.3.control.options.2.hint"
                        }
                      ]
                    }
                  },
                  {
                    "id": "people.hours",
                    "scope": "account",
                    "key": "account.viewing_hours",
                    "label": "next.settingsSpec.server.people.panels.person.tabs.1.groups.1.rows.4.label",
                    "control": {
                      "type": "select",
                      "default": "any",
                      "options": [
                        {
                          "value": "any",
                          "label": "next.settingsSpec.server.people.panels.person.tabs.1.groups.1.rows.4.control.options.0.label"
                        },
                        {
                          "value": "set",
                          "label": "next.settingsSpec.server.people.panels.person.tabs.1.groups.1.rows.4.control.options.1.label",
                          "hint": "next.settingsSpec.server.people.panels.person.tabs.1.groups.1.rows.4.control.options.1.hint"
                        }
                      ]
                    },
                    "children": [
                      {
                        "id": "people.hoursList",
                        "scope": "account",
                        "key": "account.viewing_hours_list",
                        "enableWhen": {
                          "id": "people.hours",
                          "in": [
                            "set"
                          ]
                        },
                        "label": "next.settingsSpec.server.people.panels.person.tabs.1.groups.1.rows.4.children.0.label",
                        "control": {
                          "type": "list",
                          "addLabel": "next.settingsSpec.server.people.panels.person.tabs.1.groups.1.rows.4.children.0.control.addLabel",
                          "itemAction": {
                            "label": "next.settingsSpec.server.people.panels.person.tabs.1.groups.1.rows.4.children.0.control.itemAction.label",
                            "style": "glass",
                            "remove": true
                          }
                        }
                      }
                    ]
                  }
                ]
              },
              {
                "title": "next.settingsSpec.server.people.panels.person.tabs.1.groups.2.title",
                "rows": [
                  {
                    "id": "people.maxRating",
                    "scope": "account",
                    "key": "account.max_rating",
                    "label": "next.settingsSpec.server.people.panels.person.tabs.1.groups.2.rows.0.label",
                    "help": "next.settingsSpec.server.people.panels.person.tabs.1.groups.2.rows.0.help",
                    "control": {
                      "type": "select",
                      "default": "none",
                      "options": [
                        {
                          "value": "none",
                          "label": "next.settingsSpec.server.people.panels.person.tabs.1.groups.2.rows.0.control.options.0.label"
                        },
                        {
                          "value": "G",
                          "label": "next.settingsSpec.server.people.panels.person.tabs.1.groups.2.rows.0.control.options.1.label",
                          "hint": "next.settingsSpec.server.people.panels.person.tabs.1.groups.2.rows.0.control.options.1.hint"
                        },
                        {
                          "value": "PG",
                          "label": "next.settingsSpec.server.people.panels.person.tabs.1.groups.2.rows.0.control.options.2.label",
                          "hint": "next.settingsSpec.server.people.panels.person.tabs.1.groups.2.rows.0.control.options.2.hint"
                        },
                        {
                          "value": "PG-13",
                          "label": "next.settingsSpec.server.people.panels.person.tabs.1.groups.2.rows.0.control.options.3.label",
                          "hint": "next.settingsSpec.server.people.panels.person.tabs.1.groups.2.rows.0.control.options.3.hint"
                        },
                        {
                          "value": "R",
                          "label": "next.settingsSpec.server.people.panels.person.tabs.1.groups.2.rows.0.control.options.4.label",
                          "hint": "next.settingsSpec.server.people.panels.person.tabs.1.groups.2.rows.0.control.options.4.hint"
                        }
                      ]
                    }
                  },
                  {
                    "id": "people.allowUnrated",
                    "scope": "account",
                    "key": "account.allow_unrated",
                    "label": "next.settingsSpec.server.people.panels.person.tabs.1.groups.2.rows.1.label",
                    "help": "next.settingsSpec.server.people.panels.person.tabs.1.groups.2.rows.1.help",
                    "control": {
                      "type": "toggle",
                      "default": true
                    }
                  },
                  {
                    "id": "people.hiddenLabels",
                    "scope": "account",
                    "key": "account.hidden_labels",
                    "label": "next.settingsSpec.server.people.panels.person.tabs.1.groups.2.rows.2.label",
                    "help": "next.settingsSpec.server.people.panels.person.tabs.1.groups.2.rows.2.help",
                    "control": {
                      "type": "list",
                      "addLabel": "next.settingsSpec.server.people.panels.person.tabs.1.groups.2.rows.2.control.addLabel",
                      "itemAction": {
                        "label": "next.settingsSpec.server.people.panels.person.tabs.1.groups.2.rows.2.control.itemAction.label",
                        "style": "glass",
                        "remove": true
                      }
                    }
                  }
                ]
              }
            ]
          },
          {
            "id": "profiles",
            "title": "next.settingsSpec.server.people.panels.person.tabs.2.title",
            "groups": [
              {
                "rows": [
                  {
                    "id": "people.profiles",
                    "scope": "account",
                    "key": "account.profiles",
                    "noChanged": true,
                    "label": "next.settingsSpec.server.people.panels.person.tabs.2.groups.0.rows.0.label",
                    "control": {
                      "type": "list",
                      "itemAction": {
                        "label": "next.settingsSpec.server.people.panels.person.tabs.2.groups.0.rows.0.control.itemAction.label",
                        "style": "glass"
                      }
                    }
                  }
                ]
              }
            ]
          },
          {
            "id": "devices",
            "title": "next.settingsSpec.server.people.panels.person.tabs.3.title",
            "groups": [
              {
                "rows": [
                  {
                    "id": "people.devices",
                    "scope": "server",
                    "key": "people.devices",
                    "noChanged": true,
                    "label": "next.settingsSpec.server.people.panels.person.tabs.3.groups.0.rows.0.label",
                    "control": {
                      "type": "list",
                      "itemAction": {
                        "label": "next.settingsSpec.server.people.panels.person.tabs.3.groups.0.rows.0.control.itemAction.label",
                        "style": "glass",
                        "confirm": "next.settingsSpec.server.people.panels.person.tabs.3.groups.0.rows.0.control.itemAction.confirm",
                        "remove": true
                      }
                    }
                  },
                  {
                    "id": "people.signOutPerson",
                    "scope": "account",
                    "key": "account.sign_out_everywhere",
                    "label": "next.settingsSpec.server.people.panels.person.tabs.3.groups.0.rows.1.label",
                    "control": {
                      "type": "action",
                      "style": "glass",
                      "label": "next.settingsSpec.server.people.panels.person.tabs.3.groups.0.rows.1.control.label",
                      "confirm": "next.settingsSpec.server.people.panels.person.tabs.3.groups.0.rows.1.control.confirm"
                    }
                  }
                ]
              }
            ]
          },
          {
            "id": "advanced",
            "title": "next.settingsSpec.server.people.panels.person.tabs.4.title",
            "groups": [
              {
                "rows": [
                  {
                    "id": "people.accessTokens",
                    "scope": "account",
                    "key": "account.access_tokens",
                    "label": "next.settingsSpec.server.people.panels.person.tabs.4.groups.0.rows.0.label",
                    "help": "next.settingsSpec.server.people.panels.person.tabs.4.groups.0.rows.0.help",
                    "control": {
                      "type": "toggle",
                      "default": true
                    }
                  },
                  {
                    "id": "people.hoursTimezone",
                    "scope": "account",
                    "key": "account.viewing_hours_timezone",
                    "enableWhen": {
                      "id": "people.hours",
                      "in": [
                        "set"
                      ]
                    },
                    "label": "next.settingsSpec.server.people.panels.person.tabs.4.groups.0.rows.1.label",
                    "help": "next.settingsSpec.server.people.panels.person.tabs.4.groups.0.rows.1.help",
                    "control": {
                      "type": "text",
                      "placeholder": "next.settingsSpec.server.people.panels.person.tabs.4.groups.0.rows.1.control.placeholder"
                    }
                  }
                ]
              }
            ]
          }
        ]
      },
      "invite": {
        "groups": [
          {
            "rows": [
              {
                "id": "invite.email",
                "scope": "server",
                "key": "invite.email",
                "label": "next.settingsSpec.server.people.panels.invite.groups.0.rows.0.label",
                "help": "next.settingsSpec.server.people.panels.invite.groups.0.rows.0.help",
                "control": {
                  "type": "text",
                  "placeholder": "next.settingsSpec.server.people.panels.invite.groups.0.rows.0.control.placeholder"
                }
              },
              {
                "id": "invite.notes",
                "scope": "server",
                "key": "invite.notes",
                "label": "next.settingsSpec.server.people.panels.invite.groups.0.rows.1.label",
                "help": "next.settingsSpec.server.people.panels.invite.groups.0.rows.1.help",
                "control": {
                  "type": "text",
                  "placeholder": "next.settingsSpec.server.people.panels.invite.groups.0.rows.1.control.placeholder"
                }
              },
              {
                "id": "invite.role",
                "scope": "server",
                "key": "invite.role",
                "label": "next.settingsSpec.server.people.panels.invite.groups.0.rows.2.label",
                "control": {
                  "type": "select",
                  "default": "member",
                  "options": [
                    {
                      "value": "member",
                      "label": "next.settingsSpec.server.people.panels.invite.groups.0.rows.2.control.options.0.label",
                      "hint": "next.settingsSpec.server.people.panels.invite.groups.0.rows.2.control.options.0.hint"
                    },
                    {
                      "value": "administrator",
                      "label": "next.settingsSpec.server.people.panels.invite.groups.0.rows.2.control.options.1.label",
                      "hint": "next.settingsSpec.server.people.panels.invite.groups.0.rows.2.control.options.1.hint"
                    }
                  ]
                }
              },
              {
                "id": "invite.libraries",
                "scope": "server",
                "key": "invite.libraries",
                "label": "next.settingsSpec.server.people.panels.invite.groups.0.rows.3.label",
                "control": {
                  "type": "select",
                  "default": "all",
                  "options": [
                    {
                      "value": "all",
                      "label": "next.settingsSpec.server.people.panels.invite.groups.0.rows.3.control.options.0.label",
                      "hint": "next.settingsSpec.server.people.panels.invite.groups.0.rows.3.control.options.0.hint"
                    },
                    {
                      "value": "some",
                      "label": "next.settingsSpec.server.people.panels.invite.groups.0.rows.3.control.options.1.label",
                      "hint": "next.settingsSpec.server.people.panels.invite.groups.0.rows.3.control.options.1.hint"
                    }
                  ]
                },
                "children": [
                  {
                    "id": "invite.libraryList",
                    "scope": "server",
                    "key": "invite.library_list",
                    "enableWhen": {
                      "id": "invite.libraries",
                      "in": [
                        "some"
                      ]
                    },
                    "label": "next.settingsSpec.server.people.panels.invite.groups.0.rows.3.children.0.label",
                    "help": "next.settingsSpec.server.people.panels.invite.groups.0.rows.3.children.0.help",
                    "control": {
                      "type": "list",
                      "addLabel": "next.settingsSpec.server.people.panels.invite.groups.0.rows.3.children.0.control.addLabel",
                      "itemAction": {
                        "label": "next.settingsSpec.server.people.panels.invite.groups.0.rows.3.children.0.control.itemAction.label",
                        "style": "glass",
                        "remove": true
                      }
                    }
                  }
                ]
              },
              {
                "id": "invite.maxStreams",
                "scope": "server",
                "key": "invite.max_streams",
                "label": "next.settingsSpec.server.people.panels.invite.groups.0.rows.4.label",
                "help": "next.settingsSpec.server.people.panels.invite.groups.0.rows.4.help",
                "control": {
                  "type": "select",
                  "default": "default",
                  "options": [
                    {
                      "value": "default",
                      "label": "next.settingsSpec.server.people.panels.invite.groups.0.rows.4.control.options.0.label",
                      "hint": "next.settingsSpec.server.people.panels.invite.groups.0.rows.4.control.options.0.hint"
                    },
                    {
                      "value": "unlimited",
                      "label": "next.settingsSpec.server.people.panels.invite.groups.0.rows.4.control.options.1.label"
                    },
                    {
                      "value": "1",
                      "label": "next.settingsSpec.server.people.panels.invite.groups.0.rows.4.control.options.2.label"
                    },
                    {
                      "value": "2",
                      "label": "next.settingsSpec.server.people.panels.invite.groups.0.rows.4.control.options.3.label"
                    },
                    {
                      "value": "3",
                      "label": "next.settingsSpec.server.people.panels.invite.groups.0.rows.4.control.options.4.label"
                    },
                    {
                      "value": "4",
                      "label": "next.settingsSpec.server.people.panels.invite.groups.0.rows.4.control.options.5.label"
                    },
                    {
                      "value": "6",
                      "label": "next.settingsSpec.server.people.panels.invite.groups.0.rows.4.control.options.6.label"
                    }
                  ]
                }
              },
              {
                "id": "invite.awayAccess",
                "scope": "server",
                "key": "invite.away_access",
                "label": "next.settingsSpec.server.people.panels.invite.groups.0.rows.5.label",
                "control": {
                  "type": "segmented",
                  "default": "allowed",
                  "options": [
                    {
                      "value": "allowed",
                      "label": "next.settingsSpec.server.people.panels.invite.groups.0.rows.5.control.options.0.label"
                    },
                    {
                      "value": "home",
                      "label": "next.settingsSpec.server.people.panels.invite.groups.0.rows.5.control.options.1.label"
                    }
                  ]
                }
              }
            ]
          }
        ],
        "advanced": {
          "groups": [
            {
              "rows": [
                {
                  "id": "invite.accessTokens",
                  "scope": "server",
                  "key": "invite.access_tokens",
                  "label": "next.settingsSpec.server.people.panels.invite.advanced.groups.0.rows.0.label",
                  "help": "next.settingsSpec.server.people.panels.invite.advanced.groups.0.rows.0.help",
                  "control": {
                    "type": "toggle",
                    "default": true
                  }
                },
                {
                  "id": "invite.maxRating",
                  "scope": "server",
                  "key": "invite.max_rating",
                  "label": "next.settingsSpec.server.people.panels.invite.advanced.groups.0.rows.1.label",
                  "help": "next.settingsSpec.server.people.panels.invite.advanced.groups.0.rows.1.help",
                  "control": {
                    "type": "select",
                    "default": "none",
                    "options": [
                      {
                        "value": "none",
                        "label": "next.settingsSpec.server.people.panels.invite.advanced.groups.0.rows.1.control.options.0.label"
                      },
                      {
                        "value": "G",
                        "label": "next.settingsSpec.server.people.panels.invite.advanced.groups.0.rows.1.control.options.1.label",
                        "hint": "next.settingsSpec.server.people.panels.invite.advanced.groups.0.rows.1.control.options.1.hint"
                      },
                      {
                        "value": "PG",
                        "label": "next.settingsSpec.server.people.panels.invite.advanced.groups.0.rows.1.control.options.2.label",
                        "hint": "next.settingsSpec.server.people.panels.invite.advanced.groups.0.rows.1.control.options.2.hint"
                      },
                      {
                        "value": "PG-13",
                        "label": "next.settingsSpec.server.people.panels.invite.advanced.groups.0.rows.1.control.options.3.label",
                        "hint": "next.settingsSpec.server.people.panels.invite.advanced.groups.0.rows.1.control.options.3.hint"
                      },
                      {
                        "value": "R",
                        "label": "next.settingsSpec.server.people.panels.invite.advanced.groups.0.rows.1.control.options.4.label",
                        "hint": "next.settingsSpec.server.people.panels.invite.advanced.groups.0.rows.1.control.options.4.hint"
                      }
                    ]
                  }
                },
                {
                  "id": "invite.expiry",
                  "scope": "server",
                  "key": "invite.expiry_days",
                  "label": "next.settingsSpec.server.people.panels.invite.advanced.groups.0.rows.2.label",
                  "control": {
                    "type": "select",
                    "default": "7",
                    "options": [
                      {
                        "value": "1",
                        "label": "next.settingsSpec.server.people.panels.invite.advanced.groups.0.rows.2.control.options.0.label"
                      },
                      {
                        "value": "3",
                        "label": "next.settingsSpec.server.people.panels.invite.advanced.groups.0.rows.2.control.options.1.label"
                      },
                      {
                        "value": "7",
                        "label": "next.settingsSpec.server.people.panels.invite.advanced.groups.0.rows.2.control.options.2.label"
                      },
                      {
                        "value": "30",
                        "label": "next.settingsSpec.server.people.panels.invite.advanced.groups.0.rows.2.control.options.3.label"
                      }
                    ]
                  }
                }
              ]
            }
          ]
        },
        "title": "next.settingsSpec.server.people.panels.invite.title",
        "newTitle": "next.settingsSpec.server.people.panels.invite.newTitle",
        "footer": {
          "primary": "next.settingsSpec.server.people.panels.invite.footer.primary",
          "secondary": "next.settingsSpec.server.people.panels.invite.footer.secondary"
        }
      },
      "bulkAccess": {
        "groups": [
          {
            "rows": [
              {
                "id": "bulk.libraries",
                "scope": "server",
                "key": "bulk_access.libraries",
                "label": "next.settingsSpec.server.people.panels.bulkAccess.groups.0.rows.0.label",
                "control": {
                  "type": "select",
                  "default": "keep",
                  "options": [
                    {
                      "value": "keep",
                      "label": "next.settingsSpec.server.people.panels.bulkAccess.groups.0.rows.0.control.options.0.label"
                    },
                    {
                      "value": "all",
                      "label": "next.settingsSpec.server.people.panels.bulkAccess.groups.0.rows.0.control.options.1.label",
                      "hint": "next.settingsSpec.server.people.panels.bulkAccess.groups.0.rows.0.control.options.1.hint"
                    },
                    {
                      "value": "some",
                      "label": "next.settingsSpec.server.people.panels.bulkAccess.groups.0.rows.0.control.options.2.label",
                      "hint": "next.settingsSpec.server.people.panels.bulkAccess.groups.0.rows.0.control.options.2.hint"
                    }
                  ]
                }
              },
              {
                "id": "bulk.maxStreams",
                "scope": "server",
                "key": "bulk_access.maxstreams",
                "label": "next.settingsSpec.server.people.panels.bulkAccess.groups.0.rows.1.label",
                "control": {
                  "type": "select",
                  "default": "keep",
                  "options": [
                    {
                      "value": "keep",
                      "label": "next.settingsSpec.server.people.panels.bulkAccess.groups.0.rows.1.control.options.0.label"
                    },
                    {
                      "value": "default",
                      "label": "next.settingsSpec.server.people.panels.bulkAccess.groups.0.rows.1.control.options.1.label",
                      "hint": "next.settingsSpec.server.people.panels.bulkAccess.groups.0.rows.1.control.options.1.hint"
                    },
                    {
                      "value": "unlimited",
                      "label": "next.settingsSpec.server.people.panels.bulkAccess.groups.0.rows.1.control.options.2.label"
                    },
                    {
                      "value": "1",
                      "label": "next.settingsSpec.server.people.panels.bulkAccess.groups.0.rows.1.control.options.3.label"
                    },
                    {
                      "value": "2",
                      "label": "next.settingsSpec.server.people.panels.bulkAccess.groups.0.rows.1.control.options.4.label"
                    },
                    {
                      "value": "3",
                      "label": "next.settingsSpec.server.people.panels.bulkAccess.groups.0.rows.1.control.options.5.label"
                    },
                    {
                      "value": "4",
                      "label": "next.settingsSpec.server.people.panels.bulkAccess.groups.0.rows.1.control.options.6.label"
                    },
                    {
                      "value": "6",
                      "label": "next.settingsSpec.server.people.panels.bulkAccess.groups.0.rows.1.control.options.7.label"
                    }
                  ]
                }
              },
              {
                "id": "bulk.awayAccess",
                "scope": "server",
                "key": "bulk_access.awayaccess",
                "label": "next.settingsSpec.server.people.panels.bulkAccess.groups.0.rows.2.label",
                "control": {
                  "type": "segmented",
                  "default": "keep",
                  "options": [
                    {
                      "value": "keep",
                      "label": "next.settingsSpec.server.people.panels.bulkAccess.groups.0.rows.2.control.options.0.label"
                    },
                    {
                      "value": "allowed",
                      "label": "next.settingsSpec.server.people.panels.bulkAccess.groups.0.rows.2.control.options.1.label"
                    },
                    {
                      "value": "home",
                      "label": "next.settingsSpec.server.people.panels.bulkAccess.groups.0.rows.2.control.options.2.label"
                    }
                  ]
                }
              },
              {
                "id": "bulk.maxRating",
                "scope": "server",
                "key": "bulk_access.maxrating",
                "label": "next.settingsSpec.server.people.panels.bulkAccess.groups.0.rows.3.label",
                "control": {
                  "type": "select",
                  "default": "keep",
                  "options": [
                    {
                      "value": "keep",
                      "label": "next.settingsSpec.server.people.panels.bulkAccess.groups.0.rows.3.control.options.0.label"
                    },
                    {
                      "value": "none",
                      "label": "next.settingsSpec.server.people.panels.bulkAccess.groups.0.rows.3.control.options.1.label"
                    },
                    {
                      "value": "G",
                      "label": "next.settingsSpec.server.people.panels.bulkAccess.groups.0.rows.3.control.options.2.label",
                      "hint": "next.settingsSpec.server.people.panels.bulkAccess.groups.0.rows.3.control.options.2.hint"
                    },
                    {
                      "value": "PG",
                      "label": "next.settingsSpec.server.people.panels.bulkAccess.groups.0.rows.3.control.options.3.label",
                      "hint": "next.settingsSpec.server.people.panels.bulkAccess.groups.0.rows.3.control.options.3.hint"
                    },
                    {
                      "value": "PG-13",
                      "label": "next.settingsSpec.server.people.panels.bulkAccess.groups.0.rows.3.control.options.4.label",
                      "hint": "next.settingsSpec.server.people.panels.bulkAccess.groups.0.rows.3.control.options.4.hint"
                    },
                    {
                      "value": "R",
                      "label": "next.settingsSpec.server.people.panels.bulkAccess.groups.0.rows.3.control.options.5.label",
                      "hint": "next.settingsSpec.server.people.panels.bulkAccess.groups.0.rows.3.control.options.5.hint"
                    }
                  ]
                }
              }
            ]
          }
        ],
        "title": "next.settingsSpec.server.people.panels.bulkAccess.title",
        "newTitle": "next.settingsSpec.server.people.panels.bulkAccess.newTitle",
        "footer": {
          "primary": "next.settingsSpec.server.people.panels.bulkAccess.footer.primary",
          "secondary": "next.settingsSpec.server.people.panels.bulkAccess.footer.secondary"
        }
      }
    },
    "configOnly": [
      {
        "key": "people.self_registration",
        "label": "next.settingsSpec.server.people.configOnly.0.label"
      }
    ]
  },
  {
    "scope": "server",
    "id": "signin",
    "navGroup": "People and access",
    "icon": "key",
    "kind": "settings",
    "title": "next.settingsSpec.server.signin.title",
    "groups": [
      {
        "title": "next.settingsSpec.server.signin.groups.0.title",
        "rows": [
          {
            "id": "signin.method",
            "scope": "server",
            "key": "signin.kind",
            "shapes": [
              "portico-account"
            ],
            "label": "next.settingsSpec.server.signin.groups.0.rows.0.label",
            "help": "next.settingsSpec.server.signin.groups.0.rows.0.help",
            "control": {
              "type": "status"
            }
          },
          {
            "id": "signin.methodLocal",
            "scope": "server",
            "key": "signin.kind",
            "shapes": [
              "local-account"
            ],
            "label": "next.settingsSpec.server.signin.groups.0.rows.1.label",
            "help": "next.settingsSpec.server.signin.groups.0.rows.1.help",
            "control": {
              "type": "status"
            }
          },
          {
            "id": "signin.owner",
            "scope": "server",
            "key": "signin.owner",
            "shapes": [
              "portico-account"
            ],
            "label": "next.settingsSpec.server.signin.groups.0.rows.2.label",
            "control": {
              "type": "readonly"
            }
          },
          {
            "id": "signin.ownerLocal",
            "scope": "server",
            "key": "signin.owner",
            "shapes": [
              "local-account"
            ],
            "label": "next.settingsSpec.server.signin.groups.0.rows.3.label",
            "control": {
              "type": "readonly"
            }
          },
          {
            "id": "signin.changeOwner",
            "scope": "server",
            "key": "signin.change_owner",
            "platforms": [
              "web"
            ],
            "shapes": [
              "portico-account"
            ],
            "label": "next.settingsSpec.server.signin.groups.0.rows.4.label",
            "help": "next.settingsSpec.server.signin.groups.0.rows.4.help",
            "control": {
              "type": "action",
              "style": "glass",
              "label": "next.settingsSpec.server.signin.groups.0.rows.4.control.label",
              "confirm": "next.settingsSpec.server.signin.groups.0.rows.4.control.confirm"
            }
          },
          {
            "id": "signin.changeOwnerLocal",
            "scope": "server",
            "key": "signin.change_owner",
            "platforms": [
              "web"
            ],
            "shapes": [
              "local-account"
            ],
            "label": "next.settingsSpec.server.signin.groups.0.rows.5.label",
            "help": "next.settingsSpec.server.signin.groups.0.rows.5.help",
            "control": {
              "type": "action",
              "style": "glass",
              "label": "next.settingsSpec.server.signin.groups.0.rows.5.control.label",
              "confirm": "next.settingsSpec.server.signin.groups.0.rows.5.control.confirm"
            }
          },
          {
            "id": "signin.people",
            "scope": "server",
            "key": "signin.people",
            "label": "next.settingsSpec.server.signin.groups.0.rows.6.label",
            "control": {
              "type": "link",
              "label": "next.settingsSpec.server.signin.groups.0.rows.6.control.label",
              "to": "server/people"
            }
          },
          {
            "id": "signin.outside",
            "scope": "server",
            "key": "signin.outside",
            "label": "next.settingsSpec.server.signin.groups.0.rows.7.label",
            "control": {
              "type": "link",
              "label": "next.settingsSpec.server.signin.groups.0.rows.7.control.label",
              "to": "server/remote"
            }
          }
        ]
      },
      {
        "title": "next.settingsSpec.server.signin.groups.1.title",
        "rows": [
          {
            "id": "signin.switchToLocal",
            "scope": "server",
            "key": "signin.switch_to_local",
            "platforms": [
              "web"
            ],
            "shapes": [
              "portico-account"
            ],
            "label": "next.settingsSpec.server.signin.groups.1.rows.0.label",
            "help": "next.settingsSpec.server.signin.groups.1.rows.0.help",
            "control": {
              "type": "action",
              "style": "glass",
              "label": "next.settingsSpec.server.signin.groups.1.rows.0.control.label",
              "panel": "switchToLocal"
            }
          },
          {
            "id": "signin.startOverLocal",
            "scope": "server",
            "key": "signin.start_over_local",
            "platforms": [
              "web"
            ],
            "shapes": [
              "portico-account"
            ],
            "label": "next.settingsSpec.server.signin.groups.1.rows.1.label",
            "help": "next.settingsSpec.server.signin.groups.1.rows.1.help",
            "control": {
              "type": "action",
              "style": "danger",
              "label": "next.settingsSpec.server.signin.groups.1.rows.1.control.label",
              "panel": "startOverLocal"
            }
          },
          {
            "id": "signin.switchToPortico",
            "scope": "server",
            "key": "signin.switch_to_portico",
            "platforms": [
              "web"
            ],
            "shapes": [
              "local-account"
            ],
            "label": "next.settingsSpec.server.signin.groups.1.rows.2.label",
            "help": "next.settingsSpec.server.signin.groups.1.rows.2.help",
            "control": {
              "type": "action",
              "style": "glass",
              "label": "next.settingsSpec.server.signin.groups.1.rows.2.control.label",
              "panel": "switchToPortico"
            }
          },
          {
            "id": "signin.startOverPortico",
            "scope": "server",
            "key": "signin.start_over_portico",
            "platforms": [
              "web"
            ],
            "shapes": [
              "local-account"
            ],
            "label": "next.settingsSpec.server.signin.groups.1.rows.3.label",
            "help": "next.settingsSpec.server.signin.groups.1.rows.3.help",
            "control": {
              "type": "action",
              "style": "danger",
              "label": "next.settingsSpec.server.signin.groups.1.rows.3.control.label",
              "panel": "startOverPortico"
            }
          }
        ]
      },
      {
        "title": "next.settingsSpec.server.signin.groups.2.title",
        "rows": [
          {
            "id": "signin.dataFolder",
            "scope": "server",
            "key": "signin.data_folder",
            "label": "next.settingsSpec.server.signin.groups.2.rows.0.label",
            "help": "next.settingsSpec.server.signin.groups.2.rows.0.help",
            "control": {
              "type": "readonly",
              "mono": true
            }
          }
        ]
      }
    ],
    "advanced": {
      "groups": [
        {
          "title": "next.settingsSpec.server.signin.advanced.groups.0.title",
          "rows": [
            {
              "id": "signin.stayLength",
              "scope": "server",
              "key": "signin.stay_signed_in_days",
              "label": "next.settingsSpec.server.signin.advanced.groups.0.rows.0.label",
              "help": "next.settingsSpec.server.signin.advanced.groups.0.rows.0.help",
              "control": {
                "type": "select",
                "default": "90",
                "options": [
                  {
                    "value": "7",
                    "label": "next.settingsSpec.server.signin.advanced.groups.0.rows.0.control.options.0.label"
                  },
                  {
                    "value": "30",
                    "label": "next.settingsSpec.server.signin.advanced.groups.0.rows.0.control.options.1.label"
                  },
                  {
                    "value": "90",
                    "label": "next.settingsSpec.server.signin.advanced.groups.0.rows.0.control.options.2.label"
                  },
                  {
                    "value": "365",
                    "label": "next.settingsSpec.server.signin.advanced.groups.0.rows.0.control.options.3.label"
                  }
                ]
              }
            },
            {
              "id": "signin.pinRemember",
              "scope": "server",
              "key": "signin.pin_remember_days",
              "label": "next.settingsSpec.server.signin.advanced.groups.0.rows.1.label",
              "help": "next.settingsSpec.server.signin.advanced.groups.0.rows.1.help",
              "control": {
                "type": "select",
                "default": "30",
                "options": [
                  {
                    "value": "0",
                    "label": "next.settingsSpec.server.signin.advanced.groups.0.rows.1.control.options.0.label"
                  },
                  {
                    "value": "7",
                    "label": "next.settingsSpec.server.signin.advanced.groups.0.rows.1.control.options.1.label"
                  },
                  {
                    "value": "30",
                    "label": "next.settingsSpec.server.signin.advanced.groups.0.rows.1.control.options.2.label"
                  },
                  {
                    "value": "90",
                    "label": "next.settingsSpec.server.signin.advanced.groups.0.rows.1.control.options.3.label"
                  }
                ]
              }
            }
          ]
        },
        {
          "title": "next.settingsSpec.server.signin.advanced.groups.1.title",
          "rows": [
            {
              "id": "signin.passwordAttempts",
              "scope": "server",
              "key": "signin.password_attempts",
              "shapes": [
                "local-account"
              ],
              "label": "next.settingsSpec.server.signin.advanced.groups.1.rows.0.label",
              "help": "next.settingsSpec.server.signin.advanced.groups.1.rows.0.help",
              "control": {
                "type": "select",
                "default": "standard",
                "options": [
                  {
                    "value": "relaxed",
                    "label": "next.settingsSpec.server.signin.advanced.groups.1.rows.0.control.options.0.label",
                    "hint": "next.settingsSpec.server.signin.advanced.groups.1.rows.0.control.options.0.hint"
                  },
                  {
                    "value": "standard",
                    "label": "next.settingsSpec.server.signin.advanced.groups.1.rows.0.control.options.1.label",
                    "hint": "next.settingsSpec.server.signin.advanced.groups.1.rows.0.control.options.1.hint"
                  },
                  {
                    "value": "strict",
                    "label": "next.settingsSpec.server.signin.advanced.groups.1.rows.0.control.options.2.label",
                    "hint": "next.settingsSpec.server.signin.advanced.groups.1.rows.0.control.options.2.hint"
                  }
                ]
              }
            },
            {
              "id": "signin.pinAttempts",
              "scope": "server",
              "key": "signin.pin_attempts",
              "label": "next.settingsSpec.server.signin.advanced.groups.1.rows.1.label",
              "help": "next.settingsSpec.server.signin.advanced.groups.1.rows.1.help",
              "control": {
                "type": "select",
                "default": "standard",
                "options": [
                  {
                    "value": "relaxed",
                    "label": "next.settingsSpec.server.signin.advanced.groups.1.rows.1.control.options.0.label",
                    "hint": "next.settingsSpec.server.signin.advanced.groups.1.rows.1.control.options.0.hint"
                  },
                  {
                    "value": "standard",
                    "label": "next.settingsSpec.server.signin.advanced.groups.1.rows.1.control.options.1.label",
                    "hint": "next.settingsSpec.server.signin.advanced.groups.1.rows.1.control.options.1.hint"
                  },
                  {
                    "value": "strict",
                    "label": "next.settingsSpec.server.signin.advanced.groups.1.rows.1.control.options.2.label",
                    "hint": "next.settingsSpec.server.signin.advanced.groups.1.rows.1.control.options.2.hint"
                  }
                ]
              }
            }
          ]
        },
        {
          "title": "next.settingsSpec.server.signin.advanced.groups.2.title",
          "rows": [
            {
              "id": "signin.passwordLength",
              "scope": "server",
              "key": "signin.password_min_length",
              "shapes": [
                "local-account"
              ],
              "label": "next.settingsSpec.server.signin.advanced.groups.2.rows.0.label",
              "help": "next.settingsSpec.server.signin.advanced.groups.2.rows.0.help",
              "control": {
                "type": "select",
                "default": "8",
                "options": [
                  {
                    "value": "8",
                    "label": "next.settingsSpec.server.signin.advanced.groups.2.rows.0.control.options.0.label"
                  },
                  {
                    "value": "10",
                    "label": "next.settingsSpec.server.signin.advanced.groups.2.rows.0.control.options.1.label"
                  },
                  {
                    "value": "12",
                    "label": "next.settingsSpec.server.signin.advanced.groups.2.rows.0.control.options.2.label"
                  },
                  {
                    "value": "16",
                    "label": "next.settingsSpec.server.signin.advanced.groups.2.rows.0.control.options.3.label"
                  }
                ]
              }
            },
            {
              "id": "signin.resetCodeLife",
              "scope": "server",
              "key": "signin.member_reset_code_minutes",
              "shapes": [
                "local-account"
              ],
              "label": "next.settingsSpec.server.signin.advanced.groups.2.rows.1.label",
              "help": "next.settingsSpec.server.signin.advanced.groups.2.rows.1.help",
              "control": {
                "type": "select",
                "default": "60",
                "options": [
                  {
                    "value": "15",
                    "label": "next.settingsSpec.server.signin.advanced.groups.2.rows.1.control.options.0.label"
                  },
                  {
                    "value": "60",
                    "label": "next.settingsSpec.server.signin.advanced.groups.2.rows.1.control.options.1.label"
                  },
                  {
                    "value": "1440",
                    "label": "next.settingsSpec.server.signin.advanced.groups.2.rows.1.control.options.2.label"
                  }
                ]
              }
            }
          ]
        },
        {
          "title": "next.settingsSpec.server.signin.advanced.groups.3.title",
          "rows": [
            {
              "id": "signin.requireTwoStep",
              "scope": "server",
              "key": "signin.require_two_step",
              "shapes": [
                "local-account"
              ],
              "label": "next.settingsSpec.server.signin.advanced.groups.3.rows.0.label",
              "help": "next.settingsSpec.server.signin.advanced.groups.3.rows.0.help",
              "control": {
                "type": "select",
                "default": "nobody",
                "options": [
                  {
                    "value": "nobody",
                    "label": "next.settingsSpec.server.signin.advanced.groups.3.rows.0.control.options.0.label",
                    "hint": "next.settingsSpec.server.signin.advanced.groups.3.rows.0.control.options.0.hint"
                  },
                  {
                    "value": "administrators",
                    "label": "next.settingsSpec.server.signin.advanced.groups.3.rows.0.control.options.1.label"
                  },
                  {
                    "value": "everyone",
                    "label": "next.settingsSpec.server.signin.advanced.groups.3.rows.0.control.options.2.label"
                  }
                ]
              }
            }
          ]
        },
        {
          "title": "next.settingsSpec.server.signin.advanced.groups.4.title",
          "rows": [
            {
              "id": "signin.rememberAccounts",
              "scope": "server",
              "key": "signin.remember_accounts",
              "shapes": [
                "local-account"
              ],
              "label": "next.settingsSpec.server.signin.advanced.groups.4.rows.0.label",
              "help": "next.settingsSpec.server.signin.advanced.groups.4.rows.0.help",
              "control": {
                "type": "toggle",
                "default": true
              }
            },
            {
              "id": "signin.endedKeep",
              "scope": "server",
              "key": "signin.ended_sessions_days",
              "label": "next.settingsSpec.server.signin.advanced.groups.4.rows.1.label",
              "help": "next.settingsSpec.server.signin.advanced.groups.4.rows.1.help",
              "control": {
                "type": "select",
                "default": "90",
                "options": [
                  {
                    "value": "30",
                    "label": "next.settingsSpec.server.signin.advanced.groups.4.rows.1.control.options.0.label"
                  },
                  {
                    "value": "90",
                    "label": "next.settingsSpec.server.signin.advanced.groups.4.rows.1.control.options.1.label"
                  },
                  {
                    "value": "365",
                    "label": "next.settingsSpec.server.signin.advanced.groups.4.rows.1.control.options.2.label"
                  }
                ]
              }
            }
          ]
        }
      ]
    },
    "panels": {
      "switchToLocal": {
        "groups": [
          {
            "title": "next.settingsSpec.server.signin.panels.switchToLocal.groups.0.title",
            "rows": [
              {
                "id": "signin.sl.people",
                "scope": "server",
                "key": "signin.sl.people",
                "label": "next.settingsSpec.server.signin.panels.switchToLocal.groups.0.rows.0.label",
                "help": "next.settingsSpec.server.signin.panels.switchToLocal.groups.0.rows.0.help",
                "control": {
                  "type": "status"
                }
              },
              {
                "id": "signin.sl.devices",
                "scope": "server",
                "key": "signin.sl.devices",
                "label": "next.settingsSpec.server.signin.panels.switchToLocal.groups.0.rows.1.label",
                "control": {
                  "type": "status"
                }
              },
              {
                "id": "signin.sl.tokens",
                "scope": "server",
                "key": "signin.sl.tokens",
                "label": "next.settingsSpec.server.signin.panels.switchToLocal.groups.0.rows.2.label",
                "help": "next.settingsSpec.server.signin.panels.switchToLocal.groups.0.rows.2.help",
                "control": {
                  "type": "status"
                }
              },
              {
                "id": "signin.sl.address",
                "scope": "server",
                "key": "signin.sl.address",
                "label": "next.settingsSpec.server.signin.panels.switchToLocal.groups.0.rows.3.label",
                "help": "next.settingsSpec.server.signin.panels.switchToLocal.groups.0.rows.3.help",
                "control": {
                  "type": "status"
                }
              },
              {
                "id": "signin.sl.apps",
                "scope": "server",
                "key": "signin.sl.apps",
                "label": "next.settingsSpec.server.signin.panels.switchToLocal.groups.0.rows.4.label",
                "help": "next.settingsSpec.server.signin.panels.switchToLocal.groups.0.rows.4.help",
                "control": {
                  "type": "status"
                }
              },
              {
                "id": "signin.sl.invites",
                "scope": "server",
                "key": "signin.sl.invites",
                "label": "next.settingsSpec.server.signin.panels.switchToLocal.groups.0.rows.5.label",
                "help": "next.settingsSpec.server.signin.panels.switchToLocal.groups.0.rows.5.help",
                "control": {
                  "type": "status"
                }
              },
              {
                "id": "signin.sl.services",
                "scope": "server",
                "key": "signin.sl.services",
                "label": "next.settingsSpec.server.signin.panels.switchToLocal.groups.0.rows.6.label",
                "help": "next.settingsSpec.server.signin.panels.switchToLocal.groups.0.rows.6.help",
                "control": {
                  "type": "status"
                }
              }
            ]
          },
          {
            "title": "next.settingsSpec.server.signin.panels.switchToLocal.groups.1.title",
            "rows": [
              {
                "id": "signin.sl.keepLibraries",
                "scope": "server",
                "key": "signin.sl.keepLibraries",
                "label": "next.settingsSpec.server.signin.panels.switchToLocal.groups.1.rows.0.label",
                "control": {
                  "type": "status"
                }
              },
              {
                "id": "signin.sl.keepProfiles",
                "scope": "server",
                "key": "signin.sl.keepProfiles",
                "label": "next.settingsSpec.server.signin.panels.switchToLocal.groups.1.rows.1.label",
                "help": "next.settingsSpec.server.signin.panels.switchToLocal.groups.1.rows.1.help",
                "control": {
                  "type": "status"
                }
              }
            ]
          },
          {
            "title": "next.settingsSpec.server.signin.panels.switchToLocal.groups.2.title",
            "rows": [
              {
                "id": "signin.sl.username",
                "scope": "server",
                "key": "signin.sl.username",
                "noChanged": true,
                "label": "next.settingsSpec.server.signin.panels.switchToLocal.groups.2.rows.0.label",
                "help": "next.settingsSpec.server.signin.panels.switchToLocal.groups.2.rows.0.help",
                "control": {
                  "type": "text",
                  "placeholder": "next.settingsSpec.server.signin.panels.switchToLocal.groups.2.rows.0.control.placeholder",
                  "default": ""
                }
              },
              {
                "id": "signin.sl.password",
                "scope": "server",
                "key": "signin.sl.password",
                "noChanged": true,
                "label": "next.settingsSpec.server.signin.panels.switchToLocal.groups.2.rows.1.label",
                "control": {
                  "type": "text",
                  "secret": true,
                  "placeholder": "next.settingsSpec.server.signin.panels.switchToLocal.groups.2.rows.1.control.placeholder",
                  "default": ""
                }
              },
              {
                "id": "signin.sl.confirm",
                "scope": "server",
                "key": "signin_panel.sl.confirm",
                "noChanged": true,
                "label": "next.settingsSpec.server.signin.panels.switchToLocal.groups.2.rows.2.label",
                "control": {
                  "type": "text",
                  "secret": true,
                  "placeholder": "next.settingsSpec.server.signin.panels.switchToLocal.groups.2.rows.2.control.placeholder",
                  "default": ""
                }
              },
              {
                "id": "signin.sl.twostep",
                "scope": "server",
                "key": "signin_panel.sl.twostep",
                "noChanged": true,
                "label": "next.settingsSpec.server.signin.panels.switchToLocal.groups.2.rows.3.label",
                "help": "next.settingsSpec.server.signin.panels.switchToLocal.groups.2.rows.3.help",
                "control": {
                  "type": "toggle",
                  "default": false
                }
              }
            ]
          }
        ],
        "title": "next.settingsSpec.server.signin.panels.switchToLocal.title",
        "newTitle": "next.settingsSpec.server.signin.panels.switchToLocal.newTitle",
        "searchName": "next.settingsSpec.server.signin.panels.switchToLocal.searchName",
        "footer": {
          "primary": "next.settingsSpec.server.signin.panels.switchToLocal.footer.primary",
          "secondary": "next.settingsSpec.server.signin.panels.switchToLocal.footer.secondary"
        }
      },
      "switchToPortico": {
        "groups": [
          {
            "title": "next.settingsSpec.server.signin.panels.switchToPortico.groups.0.title",
            "rows": [
              {
                "id": "signin.sp.approval",
                "scope": "server",
                "key": "signin.sp.approval",
                "label": "next.settingsSpec.server.signin.panels.switchToPortico.groups.0.rows.0.label",
                "help": "next.settingsSpec.server.signin.panels.switchToPortico.groups.0.rows.0.help",
                "control": {
                  "type": "status"
                }
              }
            ]
          },
          {
            "title": "next.settingsSpec.server.signin.panels.switchToPortico.groups.1.title",
            "rows": [
              {
                "id": "signin.sp.you",
                "scope": "server",
                "key": "signin.sp.you",
                "label": "next.settingsSpec.server.signin.panels.switchToPortico.groups.1.rows.0.label",
                "help": "next.settingsSpec.server.signin.panels.switchToPortico.groups.1.rows.0.help",
                "control": {
                  "type": "status"
                }
              },
              {
                "id": "signin.sp.people",
                "scope": "server",
                "key": "signin.sp.people",
                "label": "next.settingsSpec.server.signin.panels.switchToPortico.groups.1.rows.1.label",
                "help": "next.settingsSpec.server.signin.panels.switchToPortico.groups.1.rows.1.help",
                "control": {
                  "type": "status"
                }
              },
              {
                "id": "signin.sp.passwords",
                "scope": "server",
                "key": "signin.sp.passwords",
                "label": "next.settingsSpec.server.signin.panels.switchToPortico.groups.1.rows.2.label",
                "help": "next.settingsSpec.server.signin.panels.switchToPortico.groups.1.rows.2.help",
                "control": {
                  "type": "status"
                }
              },
              {
                "id": "signin.sp.devices",
                "scope": "server",
                "key": "signin.sp.devices",
                "label": "next.settingsSpec.server.signin.panels.switchToPortico.groups.1.rows.3.label",
                "control": {
                  "type": "status"
                }
              },
              {
                "id": "signin.sp.tokens",
                "scope": "server",
                "key": "signin.sp.tokens",
                "label": "next.settingsSpec.server.signin.panels.switchToPortico.groups.1.rows.4.label",
                "help": "next.settingsSpec.server.signin.panels.switchToPortico.groups.1.rows.4.help",
                "control": {
                  "type": "status"
                }
              },
              {
                "id": "signin.sp.address",
                "scope": "server",
                "key": "signin.sp.address",
                "label": "next.settingsSpec.server.signin.panels.switchToPortico.groups.1.rows.5.label",
                "help": "next.settingsSpec.server.signin.panels.switchToPortico.groups.1.rows.5.help",
                "control": {
                  "type": "status"
                }
              },
              {
                "id": "signin.sp.invites",
                "scope": "server",
                "key": "signin.sp.invites",
                "label": "next.settingsSpec.server.signin.panels.switchToPortico.groups.1.rows.6.label",
                "help": "next.settingsSpec.server.signin.panels.switchToPortico.groups.1.rows.6.help",
                "control": {
                  "type": "status"
                }
              }
            ]
          },
          {
            "title": "next.settingsSpec.server.signin.panels.switchToPortico.groups.2.title",
            "rows": [
              {
                "id": "signin.sp.keepLibraries",
                "scope": "server",
                "key": "signin.sp.keepLibraries",
                "label": "next.settingsSpec.server.signin.panels.switchToPortico.groups.2.rows.0.label",
                "control": {
                  "type": "status"
                }
              },
              {
                "id": "signin.sp.keepProfiles",
                "scope": "server",
                "key": "signin.sp.keepProfiles",
                "label": "next.settingsSpec.server.signin.panels.switchToPortico.groups.2.rows.1.label",
                "help": "next.settingsSpec.server.signin.panels.switchToPortico.groups.2.rows.1.help",
                "control": {
                  "type": "status"
                }
              }
            ]
          }
        ],
        "title": "next.settingsSpec.server.signin.panels.switchToPortico.title",
        "newTitle": "next.settingsSpec.server.signin.panels.switchToPortico.newTitle",
        "searchName": "next.settingsSpec.server.signin.panels.switchToPortico.searchName",
        "footer": {
          "primary": "next.settingsSpec.server.signin.panels.switchToPortico.footer.primary",
          "secondary": "next.settingsSpec.server.signin.panels.switchToPortico.footer.secondary"
        }
      },
      "startOverLocal": {
        "groups": [
          {
            "title": "next.settingsSpec.server.signin.panels.startOverLocal.groups.0.title",
            "rows": [
              {
                "id": "signin.ol.people",
                "scope": "server",
                "key": "signin.ol.people",
                "label": "next.settingsSpec.server.signin.panels.startOverLocal.groups.0.rows.0.label",
                "help": "next.settingsSpec.server.signin.panels.startOverLocal.groups.0.rows.0.help",
                "control": {
                  "type": "status"
                }
              },
              {
                "id": "signin.ol.profiles",
                "scope": "server",
                "key": "signin.ol.profiles",
                "label": "next.settingsSpec.server.signin.panels.startOverLocal.groups.0.rows.1.label",
                "help": "next.settingsSpec.server.signin.panels.startOverLocal.groups.0.rows.1.help",
                "control": {
                  "type": "status"
                }
              },
              {
                "id": "signin.ol.devices",
                "scope": "server",
                "key": "signin.ol.devices",
                "label": "next.settingsSpec.server.signin.panels.startOverLocal.groups.0.rows.2.label",
                "help": "next.settingsSpec.server.signin.panels.startOverLocal.groups.0.rows.2.help",
                "control": {
                  "type": "status"
                }
              },
              {
                "id": "signin.ol.invites",
                "scope": "server",
                "key": "signin.ol.invites",
                "label": "next.settingsSpec.server.signin.panels.startOverLocal.groups.0.rows.3.label",
                "control": {
                  "type": "status"
                }
              },
              {
                "id": "signin.ol.history",
                "scope": "server",
                "key": "signin.ol.history",
                "label": "next.settingsSpec.server.signin.panels.startOverLocal.groups.0.rows.4.label",
                "help": "next.settingsSpec.server.signin.panels.startOverLocal.groups.0.rows.4.help",
                "control": {
                  "type": "status"
                }
              },
              {
                "id": "signin.ol.address",
                "scope": "server",
                "key": "signin.ol.address",
                "label": "next.settingsSpec.server.signin.panels.startOverLocal.groups.0.rows.5.label",
                "help": "next.settingsSpec.server.signin.panels.startOverLocal.groups.0.rows.5.help",
                "control": {
                  "type": "status"
                }
              }
            ]
          },
          {
            "title": "next.settingsSpec.server.signin.panels.startOverLocal.groups.1.title",
            "rows": [
              {
                "id": "signin.ol.keepProfiles",
                "scope": "server",
                "key": "signin.ol.keepProfiles",
                "label": "next.settingsSpec.server.signin.panels.startOverLocal.groups.1.rows.0.label",
                "help": "next.settingsSpec.server.signin.panels.startOverLocal.groups.1.rows.0.help",
                "control": {
                  "type": "status"
                }
              },
              {
                "id": "signin.ol.keepLibraries",
                "scope": "server",
                "key": "signin.ol.keepLibraries",
                "label": "next.settingsSpec.server.signin.panels.startOverLocal.groups.1.rows.1.label",
                "help": "next.settingsSpec.server.signin.panels.startOverLocal.groups.1.rows.1.help",
                "control": {
                  "type": "status"
                }
              }
            ]
          },
          {
            "title": "next.settingsSpec.server.signin.panels.startOverLocal.groups.2.title",
            "rows": [
              {
                "id": "signin.ol.username",
                "scope": "server",
                "key": "signin.ol.username",
                "noChanged": true,
                "label": "next.settingsSpec.server.signin.panels.startOverLocal.groups.2.rows.0.label",
                "help": "next.settingsSpec.server.signin.panels.startOverLocal.groups.2.rows.0.help",
                "control": {
                  "type": "text",
                  "placeholder": "next.settingsSpec.server.signin.panels.startOverLocal.groups.2.rows.0.control.placeholder",
                  "default": ""
                }
              },
              {
                "id": "signin.ol.password",
                "scope": "server",
                "key": "signin.ol.password",
                "noChanged": true,
                "label": "next.settingsSpec.server.signin.panels.startOverLocal.groups.2.rows.1.label",
                "control": {
                  "type": "text",
                  "secret": true,
                  "placeholder": "next.settingsSpec.server.signin.panels.startOverLocal.groups.2.rows.1.control.placeholder",
                  "default": ""
                }
              },
              {
                "id": "signin.ol.pwconfirm",
                "scope": "server",
                "key": "signin_panel.ol.pwconfirm",
                "noChanged": true,
                "label": "next.settingsSpec.server.signin.panels.startOverLocal.groups.2.rows.2.label",
                "control": {
                  "type": "text",
                  "secret": true,
                  "placeholder": "next.settingsSpec.server.signin.panels.startOverLocal.groups.2.rows.2.control.placeholder",
                  "default": ""
                }
              },
              {
                "id": "signin.ol.twostep",
                "scope": "server",
                "key": "signin_panel.ol.twostep",
                "noChanged": true,
                "label": "next.settingsSpec.server.signin.panels.startOverLocal.groups.2.rows.3.label",
                "help": "next.settingsSpec.server.signin.panels.startOverLocal.groups.2.rows.3.help",
                "control": {
                  "type": "toggle",
                  "default": false
                }
              }
            ]
          },
          {
            "title": "next.settingsSpec.server.signin.panels.startOverLocal.groups.3.title",
            "rows": [
              {
                "id": "signin.ol.confirm",
                "scope": "server",
                "key": "signin.ol.confirm",
                "noChanged": true,
                "label": "next.settingsSpec.server.signin.panels.startOverLocal.groups.3.rows.0.label",
                "control": {
                  "type": "text",
                  "default": ""
                }
              }
            ]
          }
        ],
        "title": "next.settingsSpec.server.signin.panels.startOverLocal.title",
        "newTitle": "next.settingsSpec.server.signin.panels.startOverLocal.newTitle",
        "searchName": "next.settingsSpec.server.signin.panels.startOverLocal.searchName",
        "footer": {
          "primary": "next.settingsSpec.server.signin.panels.startOverLocal.footer.primary",
          "secondary": "next.settingsSpec.server.signin.panels.startOverLocal.footer.secondary"
        }
      },
      "startOverPortico": {
        "groups": [
          {
            "title": "next.settingsSpec.server.signin.panels.startOverPortico.groups.0.title",
            "rows": [
              {
                "id": "signin.op.approval",
                "scope": "server",
                "key": "signin.op.approval",
                "label": "next.settingsSpec.server.signin.panels.startOverPortico.groups.0.rows.0.label",
                "help": "next.settingsSpec.server.signin.panels.startOverPortico.groups.0.rows.0.help",
                "control": {
                  "type": "status"
                }
              }
            ]
          },
          {
            "title": "next.settingsSpec.server.signin.panels.startOverPortico.groups.1.title",
            "rows": [
              {
                "id": "signin.op.people",
                "scope": "server",
                "key": "signin.op.people",
                "label": "next.settingsSpec.server.signin.panels.startOverPortico.groups.1.rows.0.label",
                "help": "next.settingsSpec.server.signin.panels.startOverPortico.groups.1.rows.0.help",
                "control": {
                  "type": "status"
                }
              },
              {
                "id": "signin.op.profiles",
                "scope": "server",
                "key": "signin.op.profiles",
                "label": "next.settingsSpec.server.signin.panels.startOverPortico.groups.1.rows.1.label",
                "help": "next.settingsSpec.server.signin.panels.startOverPortico.groups.1.rows.1.help",
                "control": {
                  "type": "status"
                }
              },
              {
                "id": "signin.op.devices",
                "scope": "server",
                "key": "signin.op.devices",
                "label": "next.settingsSpec.server.signin.panels.startOverPortico.groups.1.rows.2.label",
                "help": "next.settingsSpec.server.signin.panels.startOverPortico.groups.1.rows.2.help",
                "control": {
                  "type": "status"
                }
              },
              {
                "id": "signin.op.invites",
                "scope": "server",
                "key": "signin.op.invites",
                "label": "next.settingsSpec.server.signin.panels.startOverPortico.groups.1.rows.3.label",
                "control": {
                  "type": "status"
                }
              },
              {
                "id": "signin.op.history",
                "scope": "server",
                "key": "signin.op.history",
                "label": "next.settingsSpec.server.signin.panels.startOverPortico.groups.1.rows.4.label",
                "help": "next.settingsSpec.server.signin.panels.startOverPortico.groups.1.rows.4.help",
                "control": {
                  "type": "status"
                }
              },
              {
                "id": "signin.op.you",
                "scope": "server",
                "key": "signin.op.you",
                "label": "next.settingsSpec.server.signin.panels.startOverPortico.groups.1.rows.5.label",
                "help": "next.settingsSpec.server.signin.panels.startOverPortico.groups.1.rows.5.help",
                "control": {
                  "type": "status"
                }
              }
            ]
          },
          {
            "title": "next.settingsSpec.server.signin.panels.startOverPortico.groups.2.title",
            "rows": [
              {
                "id": "signin.op.keepProfiles",
                "scope": "server",
                "key": "signin.op.keepProfiles",
                "label": "next.settingsSpec.server.signin.panels.startOverPortico.groups.2.rows.0.label",
                "help": "next.settingsSpec.server.signin.panels.startOverPortico.groups.2.rows.0.help",
                "control": {
                  "type": "status"
                }
              },
              {
                "id": "signin.op.keepLibraries",
                "scope": "server",
                "key": "signin.op.keepLibraries",
                "label": "next.settingsSpec.server.signin.panels.startOverPortico.groups.2.rows.1.label",
                "help": "next.settingsSpec.server.signin.panels.startOverPortico.groups.2.rows.1.help",
                "control": {
                  "type": "status"
                }
              }
            ]
          },
          {
            "title": "next.settingsSpec.server.signin.panels.startOverPortico.groups.3.title",
            "rows": [
              {
                "id": "signin.op.confirm",
                "scope": "server",
                "key": "signin.op.confirm",
                "noChanged": true,
                "label": "next.settingsSpec.server.signin.panels.startOverPortico.groups.3.rows.0.label",
                "control": {
                  "type": "text",
                  "default": ""
                }
              }
            ]
          }
        ],
        "title": "next.settingsSpec.server.signin.panels.startOverPortico.title",
        "newTitle": "next.settingsSpec.server.signin.panels.startOverPortico.newTitle",
        "searchName": "next.settingsSpec.server.signin.panels.startOverPortico.searchName",
        "footer": {
          "primary": "next.settingsSpec.server.signin.panels.startOverPortico.footer.primary",
          "secondary": "next.settingsSpec.server.signin.panels.startOverPortico.footer.secondary"
        }
      }
    },
    "configOnly": [
      {
        "key": "signin.password_hash_cost",
        "label": "next.settingsSpec.server.signin.configOnly.0.label"
      }
    ]
  },
  {
    "scope": "server",
    "id": "remote",
    "navGroup": "People and access",
    "icon": "wifi",
    "kind": "settings",
    "title": "next.settingsSpec.server.remote.title",
    "groups": [
      {
        "title": "next.settingsSpec.server.remote.groups.0.title",
        "rows": [
          {
            "id": "remote.secure",
            "scope": "server",
            "key": "remote.secure",
            "shapes": [
              "portico-account"
            ],
            "label": "next.settingsSpec.server.remote.groups.0.rows.0.label",
            "help": "next.settingsSpec.server.remote.groups.0.rows.0.help",
            "control": {
              "type": "toggle",
              "default": true
            },
            "children": [
              {
                "id": "remote.secureStatus",
                "scope": "server",
                "key": "remote.secure_status",
                "shapes": [
                  "portico-account"
                ],
                "enableWhen": {
                  "id": "remote.secure",
                  "in": [
                    true
                  ]
                },
                "label": "next.settingsSpec.server.remote.groups.0.rows.0.children.0.label",
                "control": {
                  "type": "status"
                }
              },
              {
                "id": "remote.secureAddress",
                "scope": "server",
                "key": "remote.secure_address",
                "shapes": [
                  "portico-account"
                ],
                "enableWhen": {
                  "id": "remote.secure",
                  "in": [
                    true
                  ]
                },
                "label": "next.settingsSpec.server.remote.groups.0.rows.0.children.1.label",
                "control": {
                  "type": "readonly",
                  "mono": true
                }
              },
              {
                "id": "remote.certificate",
                "scope": "server",
                "key": "remote.certificate",
                "shapes": [
                  "portico-account"
                ],
                "enableWhen": {
                  "id": "remote.secure",
                  "in": [
                    true
                  ]
                },
                "label": "next.settingsSpec.server.remote.groups.0.rows.0.children.2.label",
                "control": {
                  "type": "readonly"
                }
              }
            ]
          },
          {
            "id": "remote.secureConnections",
            "scope": "server",
            "key": "secureConnections",
            "label": "next.settingsSpec.server.remote.groups.0.rows.1.label",
            "help": "next.settingsSpec.server.remote.groups.0.rows.1.help",
            "control": {
              "type": "segmented",
              "default": "preferred",
              "options": [
                {
                  "value": "preferred",
                  "label": "next.settingsSpec.server.remote.groups.0.rows.1.control.options.0.label"
                },
                {
                  "value": "required",
                  "label": "next.settingsSpec.server.remote.groups.0.rows.1.control.options.1.label"
                }
              ]
            }
          }
        ]
      },
      {
        "title": "next.settingsSpec.server.remote.groups.1.title",
        "rows": [
          {
            "id": "remote.ownCertificate",
            "scope": "server",
            "key": "remote.own_certificate",
            "label": "next.settingsSpec.server.remote.groups.1.rows.0.label",
            "help": "next.settingsSpec.server.remote.groups.1.rows.0.help",
            "control": {
              "type": "toggle",
              "default": false
            },
            "children": [
              {
                "id": "remote.externalAddress",
                "scope": "server",
                "key": "remote.external_address",
                "enableWhen": {
                  "id": "remote.ownCertificate",
                  "in": [
                    true
                  ]
                },
                "label": "next.settingsSpec.server.remote.groups.1.rows.0.children.0.label",
                "help": "next.settingsSpec.server.remote.groups.1.rows.0.children.0.help",
                "control": {
                  "type": "text",
                  "placeholder": "next.settingsSpec.server.remote.groups.1.rows.0.children.0.control.placeholder"
                }
              },
              {
                "id": "remote.certFile",
                "scope": "server",
                "key": "remote.certificate_path",
                "enableWhen": {
                  "id": "remote.ownCertificate",
                  "in": [
                    true
                  ]
                },
                "label": "next.settingsSpec.server.remote.groups.1.rows.0.children.1.label",
                "help": "next.settingsSpec.server.remote.groups.1.rows.0.children.1.help",
                "control": {
                  "type": "text",
                  "placeholder": "next.settingsSpec.server.remote.groups.1.rows.0.children.1.control.placeholder"
                }
              },
              {
                "id": "remote.certKey",
                "scope": "server",
                "key": "remote.certificate_key_path",
                "enableWhen": {
                  "id": "remote.ownCertificate",
                  "in": [
                    true
                  ]
                },
                "label": "next.settingsSpec.server.remote.groups.1.rows.0.children.2.label",
                "help": "next.settingsSpec.server.remote.groups.1.rows.0.children.2.help",
                "control": {
                  "type": "text",
                  "placeholder": "next.settingsSpec.server.remote.groups.1.rows.0.children.2.control.placeholder"
                }
              },
              {
                "id": "remote.certStatus",
                "scope": "server",
                "key": "remote.certificate_status",
                "enableWhen": {
                  "id": "remote.ownCertificate",
                  "in": [
                    true
                  ]
                },
                "label": "next.settingsSpec.server.remote.groups.1.rows.0.children.3.label",
                "control": {
                  "type": "status"
                }
              },
              {
                "id": "remote.identityCode",
                "scope": "server",
                "shapes": [
                  "local-account"
                ],
                "enableWhen": {
                  "id": "remote.ownCertificate",
                  "in": [
                    true
                  ]
                },
                "noChanged": true,
                "label": "next.settingsSpec.server.remote.groups.1.rows.0.children.4.label",
                "help": "next.settingsSpec.server.remote.groups.1.rows.0.children.4.help",
                "control": {
                  "type": "readonly",
                  "mono": true
                }
              }
            ]
          }
        ]
      },
      {
        "title": "next.settingsSpec.server.remote.groups.2.title",
        "rows": [
          {
            "id": "remote.who",
            "scope": "server",
            "key": "remote.who_can_connect",
            "label": "next.settingsSpec.server.remote.groups.2.rows.0.label",
            "help": "next.settingsSpec.server.remote.groups.2.rows.0.help",
            "control": {
              "type": "select",
              "default": "anyone",
              "options": [
                {
                  "value": "anyone",
                  "label": "next.settingsSpec.server.remote.groups.2.rows.0.control.options.0.label",
                  "hint": "next.settingsSpec.server.remote.groups.2.rows.0.control.options.0.hint"
                },
                {
                  "value": "owner",
                  "label": "next.settingsSpec.server.remote.groups.2.rows.0.control.options.1.label",
                  "hint": "next.settingsSpec.server.remote.groups.2.rows.0.control.options.1.hint"
                },
                {
                  "value": "none",
                  "label": "next.settingsSpec.server.remote.groups.2.rows.0.control.options.2.label",
                  "hint": "next.settingsSpec.server.remote.groups.2.rows.0.control.options.2.hint"
                }
              ]
            }
          }
        ]
      },
      {
        "title": "next.settingsSpec.server.remote.groups.3.title",
        "rows": [
          {
            "id": "remote.thisDevice",
            "scope": "server",
            "key": "remote.this_device",
            "label": "next.settingsSpec.server.remote.groups.3.rows.0.label",
            "control": {
              "type": "status"
            }
          },
          {
            "id": "remote.homeNets",
            "scope": "server",
            "key": "remote.home_networks",
            "label": "next.settingsSpec.server.remote.groups.3.rows.1.label",
            "help": "next.settingsSpec.server.remote.groups.3.rows.1.help",
            "control": {
              "type": "list",
              "addLabel": "next.settingsSpec.server.remote.groups.3.rows.1.control.addLabel",
              "itemAction": {
                "label": "next.settingsSpec.server.remote.groups.3.rows.1.control.itemAction.label",
                "style": "glass",
                "remove": true
              }
            }
          },
          {
            "id": "remote.discovery",
            "scope": "server",
            "key": "remote.discovery",
            "label": "next.settingsSpec.server.remote.groups.3.rows.2.label",
            "help": "next.settingsSpec.server.remote.groups.3.rows.2.help",
            "control": {
              "type": "toggle",
              "default": true
            }
          },
          {
            "id": "remote.dlna",
            "scope": "server",
            "key": "remote.dlna",
            "label": "next.settingsSpec.server.remote.groups.3.rows.3.label",
            "help": "next.settingsSpec.server.remote.groups.3.rows.3.help",
            "control": {
              "type": "toggle",
              "default": false,
              "style": "checkbox"
            }
          }
        ]
      }
    ],
    "advanced": {
      "groups": [
        {
          "title": "next.settingsSpec.server.remote.advanced.groups.0.title",
          "rows": [
            {
              "id": "remote.addresses",
              "scope": "server",
              "key": "remote.addresses",
              "label": "next.settingsSpec.server.remote.advanced.groups.0.rows.0.label",
              "control": {
                "type": "status"
              }
            },
            {
              "id": "remote.mapping",
              "scope": "server",
              "key": "remote.port_mapping",
              "label": "next.settingsSpec.server.remote.advanced.groups.0.rows.1.label",
              "control": {
                "type": "select",
                "default": "auto",
                "options": [
                  {
                    "value": "auto",
                    "label": "next.settingsSpec.server.remote.advanced.groups.0.rows.1.control.options.0.label",
                    "hint": "next.settingsSpec.server.remote.advanced.groups.0.rows.1.control.options.0.hint"
                  },
                  {
                    "value": "off",
                    "label": "next.settingsSpec.server.remote.advanced.groups.0.rows.1.control.options.1.label",
                    "hint": "next.settingsSpec.server.remote.advanced.groups.0.rows.1.control.options.1.hint"
                  }
                ]
              }
            },
            {
              "id": "remote.gateway",
              "scope": "server",
              "key": "remote.gateway",
              "enableWhen": {
                "id": "remote.mapping",
                "in": [
                  "auto"
                ]
              },
              "label": "next.settingsSpec.server.remote.advanced.groups.0.rows.2.label",
              "help": "next.settingsSpec.server.remote.advanced.groups.0.rows.2.help",
              "control": {
                "type": "text",
                "placeholder": "next.settingsSpec.server.remote.advanced.groups.0.rows.2.control.placeholder"
              }
            }
          ]
        },
        {
          "title": "next.settingsSpec.server.remote.advanced.groups.1.title",
          "rows": [
            {
              "id": "remote.mappingProtocols",
              "scope": "server",
              "key": "remote.mapping_protocols",
              "enableWhen": {
                "id": "remote.mapping",
                "in": [
                  "auto"
                ]
              },
              "label": "next.settingsSpec.server.remote.advanced.groups.1.rows.0.label",
              "control": {
                "type": "matrix",
                "columns": [
                  {
                    "value": "try",
                    "label": "next.settingsSpec.server.remote.advanced.groups.1.rows.0.control.columns.0.label"
                  },
                  {
                    "value": "skip",
                    "label": "next.settingsSpec.server.remote.advanced.groups.1.rows.0.control.columns.1.label"
                  }
                ],
                "items": [
                  {
                    "id": "pcp",
                    "label": "next.settingsSpec.server.remote.advanced.groups.1.rows.0.control.items.0.label",
                    "default": "try"
                  },
                  {
                    "id": "natpmp",
                    "label": "next.settingsSpec.server.remote.advanced.groups.1.rows.0.control.items.1.label",
                    "default": "try"
                  },
                  {
                    "id": "upnp",
                    "label": "next.settingsSpec.server.remote.advanced.groups.1.rows.0.control.items.2.label",
                    "default": "try"
                  }
                ]
              }
            }
          ]
        },
        {
          "title": "next.settingsSpec.server.remote.advanced.groups.2.title",
          "rows": [
            {
              "id": "remote.publicPort",
              "scope": "server",
              "key": "remote.public_port",
              "label": "next.settingsSpec.server.remote.advanced.groups.2.rows.0.label",
              "help": "next.settingsSpec.server.remote.advanced.groups.2.rows.0.help",
              "control": {
                "type": "number",
                "default": 32500,
                "unit": "next.settingsSpec.server.remote.advanced.groups.2.rows.0.control.unit",
                "min": 1024,
                "max": 65535,
                "step": 1
              }
            },
            {
              "id": "remote.ipv6",
              "scope": "server",
              "key": "remote.ipv6",
              "label": "next.settingsSpec.server.remote.advanced.groups.2.rows.1.label",
              "help": "next.settingsSpec.server.remote.advanced.groups.2.rows.1.help",
              "control": {
                "type": "toggle",
                "default": false
              }
            }
          ]
        },
        {
          "title": "next.settingsSpec.server.remote.advanced.groups.3.title",
          "rows": [
            {
              "id": "remote.listen",
              "scope": "server",
              "key": "network.listen",
              "label": "next.settingsSpec.server.remote.advanced.groups.3.rows.0.label",
              "help": "next.settingsSpec.server.remote.advanced.groups.3.rows.0.help",
              "control": {
                "type": "readonly",
                "mono": true
              }
            },
            {
              "id": "remote.interface",
              "scope": "server",
              "key": "network.advertised_interface",
              "label": "next.settingsSpec.server.remote.advanced.groups.3.rows.1.label",
              "help": "next.settingsSpec.server.remote.advanced.groups.3.rows.1.help",
              "control": {
                "type": "select",
                "default": "all",
                "dynamicOptions": true,
                "options": [
                  {
                    "value": "all",
                    "label": "next.settingsSpec.server.remote.advanced.groups.3.rows.1.control.options.0.label"
                  }
                ]
              }
            },
            {
              "id": "remote.connectionLimit",
              "scope": "server",
              "key": "network.connection_limit",
              "label": "next.settingsSpec.server.remote.advanced.groups.3.rows.2.label",
              "help": "next.settingsSpec.server.remote.advanced.groups.3.rows.2.help",
              "control": {
                "type": "select",
                "default": "auto",
                "options": [
                  {
                    "value": "auto",
                    "label": "next.settingsSpec.server.remote.advanced.groups.3.rows.2.control.options.0.label",
                    "hint": "next.settingsSpec.server.remote.advanced.groups.3.rows.2.control.options.0.hint"
                  },
                  {
                    "value": "256",
                    "label": "next.settingsSpec.server.remote.advanced.groups.3.rows.2.control.options.1.label"
                  },
                  {
                    "value": "1024",
                    "label": "next.settingsSpec.server.remote.advanced.groups.3.rows.2.control.options.2.label"
                  },
                  {
                    "value": "4096",
                    "label": "next.settingsSpec.server.remote.advanced.groups.3.rows.2.control.options.3.label"
                  },
                  {
                    "value": "16384",
                    "label": "next.settingsSpec.server.remote.advanced.groups.3.rows.2.control.options.4.label"
                  }
                ]
              }
            }
          ]
        },
        {
          "title": "next.settingsSpec.server.remote.advanced.groups.4.title",
          "rows": [
            {
              "id": "remote.extraAddresses",
              "scope": "server",
              "key": "remote.extra_addresses",
              "label": "next.settingsSpec.server.remote.advanced.groups.4.rows.0.label",
              "help": "next.settingsSpec.server.remote.advanced.groups.4.rows.0.help",
              "control": {
                "type": "list",
                "addLabel": "next.settingsSpec.server.remote.advanced.groups.4.rows.0.control.addLabel",
                "itemAction": {
                  "label": "next.settingsSpec.server.remote.advanced.groups.4.rows.0.control.itemAction.label",
                  "style": "glass",
                  "remove": true
                }
              }
            },
            {
              "id": "remote.trustedProxies",
              "scope": "server",
              "key": "remote.trusted_proxies",
              "label": "next.settingsSpec.server.remote.advanced.groups.4.rows.1.label",
              "help": "next.settingsSpec.server.remote.advanced.groups.4.rows.1.help",
              "control": {
                "type": "list",
                "addLabel": "next.settingsSpec.server.remote.advanced.groups.4.rows.1.control.addLabel",
                "itemAction": {
                  "label": "next.settingsSpec.server.remote.advanced.groups.4.rows.1.control.itemAction.label",
                  "style": "glass",
                  "remove": true
                }
              }
            }
          ]
        }
      ]
    },
    "configOnly": [
      {
        "key": "remote.mapping_lease_seconds",
        "label": "next.settingsSpec.server.remote.configOnly.0.label"
      }
    ]
  },
  {
    "scope": "server",
    "id": "integrations",
    "navGroup": "People and access",
    "icon": "link",
    "kind": "settings",
    "title": "next.settingsSpec.server.integrations.title",
    "groups": [
      {
        "title": "integrations.compatibility.title",
        "rows": [{"id":"integrations.compatibility","scope":"server","key":"integrations.compatibilityEnabled","label":"integrations.compatibility.title","help":"integrations.compatibility.help","control":{"type":"toggle"}}]
      },
      {
        "title": "next.settingsSpec.server.integrations.groups.0.title",
        "rows": [
          {
            "id": "integrations.apiKeys",
            "scope": "server",
            "key": "integrations.api_keys",
            "label": "next.settingsSpec.server.integrations.groups.0.rows.0.label",
            "help": "next.settingsSpec.server.integrations.groups.0.rows.0.help",
            "control": {
              "type": "list",
              "addLabel": "next.settingsSpec.server.integrations.groups.0.rows.0.control.addLabel",
              "itemAction": {
                "label": "next.settingsSpec.server.integrations.groups.0.rows.0.control.itemAction.label",
                "style": "danger",
                "confirm": "next.settingsSpec.server.integrations.groups.0.rows.0.control.itemAction.confirm",
                "remove": true
              }
            }
          }
        ]
      },
      {
        "title": "next.settingsSpec.server.integrations.groups.1.title",
        "rows": [
          {
            "id": "integrations.webhooks",
            "scope": "server",
            "key": "integrations.webhooks",
            "label": "next.settingsSpec.server.integrations.groups.1.rows.0.label",
            "help": "next.settingsSpec.server.integrations.groups.1.rows.0.help",
            "control": {
              "type": "list",
              "addLabel": "next.settingsSpec.server.integrations.groups.1.rows.0.control.addLabel",
              "itemAction": {
                "label": "next.settingsSpec.server.integrations.groups.1.rows.0.control.itemAction.label",
                "style": "danger",
                "confirm": "next.settingsSpec.server.integrations.groups.1.rows.0.control.itemAction.confirm",
                "remove": true
              }
            }
          }
        ]
      },
      {
        "title": "next.settingsSpec.server.integrations.groups.2.title",
        "rows": [
          {
            "id": "integrations.assistantAddress",
            "scope": "server",
            "key": "integrations.assistant_address",
            "label": "next.settingsSpec.server.integrations.groups.2.rows.0.label",
            "control": {
              "type": "readonly",
              "mono": true
            }
          },
          {
            "id": "integrations.assistantKey",
            "scope": "server",
            "key": "integrations.assistant_key",
            "label": "next.settingsSpec.server.integrations.groups.2.rows.1.label",
            "help": "next.settingsSpec.server.integrations.groups.2.rows.1.help",
            "control": {
              "type": "link",
              "label": "next.settingsSpec.server.integrations.groups.2.rows.1.control.label",
              "to": "server/integrations/integrations.apiKeys"
            }
          }
        ]
      }
    ],
    "advanced": {
      "groups": [
        {
          "title": "next.settingsSpec.server.integrations.advanced.groups.0.title",
          "rows": [
            {
              "id": "integrations.webhookRetries",
              "scope": "server",
              "key": "integrations.webhook_retries",
              "label": "next.settingsSpec.server.integrations.advanced.groups.0.rows.0.label",
              "control": {
                "type": "select",
                "default": "long",
                "options": [
                  {
                    "value": "long",
                    "label": "next.settingsSpec.server.integrations.advanced.groups.0.rows.0.control.options.0.label",
                    "hint": "next.settingsSpec.server.integrations.advanced.groups.0.rows.0.control.options.0.hint"
                  },
                  {
                    "value": "short",
                    "label": "next.settingsSpec.server.integrations.advanced.groups.0.rows.0.control.options.1.label",
                    "hint": "next.settingsSpec.server.integrations.advanced.groups.0.rows.0.control.options.1.hint"
                  },
                  {
                    "value": "none",
                    "label": "next.settingsSpec.server.integrations.advanced.groups.0.rows.0.control.options.2.label",
                    "hint": "next.settingsSpec.server.integrations.advanced.groups.0.rows.0.control.options.2.hint"
                  }
                ]
              }
            },
            {
              "id": "integrations.webhookTimeout",
              "scope": "server",
              "key": "integrations.webhook_timeout_seconds",
              "label": "next.settingsSpec.server.integrations.advanced.groups.0.rows.1.label",
              "help": "next.settingsSpec.server.integrations.advanced.groups.0.rows.1.help",
              "control": {
                "type": "select",
                "default": "15",
                "options": [
                  {
                    "value": "5",
                    "label": "next.settingsSpec.server.integrations.advanced.groups.0.rows.1.control.options.0.label"
                  },
                  {
                    "value": "15",
                    "label": "next.settingsSpec.server.integrations.advanced.groups.0.rows.1.control.options.1.label"
                  },
                  {
                    "value": "30",
                    "label": "next.settingsSpec.server.integrations.advanced.groups.0.rows.1.control.options.2.label"
                  },
                  {
                    "value": "60",
                    "label": "next.settingsSpec.server.integrations.advanced.groups.0.rows.1.control.options.3.label"
                  }
                ]
              }
            },
            {
              "id": "integrations.webhookQueue",
              "scope": "server",
              "key": "integrations.webhook_queue_size",
              "label": "next.settingsSpec.server.integrations.advanced.groups.0.rows.2.label",
              "help": "next.settingsSpec.server.integrations.advanced.groups.0.rows.2.help",
              "control": {
                "type": "select",
                "default": "1000",
                "options": [
                  {
                    "value": "100",
                    "label": "next.settingsSpec.server.integrations.advanced.groups.0.rows.2.control.options.0.label"
                  },
                  {
                    "value": "1000",
                    "label": "next.settingsSpec.server.integrations.advanced.groups.0.rows.2.control.options.1.label"
                  },
                  {
                    "value": "5000",
                    "label": "next.settingsSpec.server.integrations.advanced.groups.0.rows.2.control.options.2.label"
                  }
                ]
              }
            },
            {
              "id": "integrations.webhookLog",
              "scope": "server",
              "key": "integrations.webhook_log_size",
              "label": "next.settingsSpec.server.integrations.advanced.groups.0.rows.3.label",
              "help": "next.settingsSpec.server.integrations.advanced.groups.0.rows.3.help",
              "control": {
                "type": "select",
                "default": "200",
                "options": [
                  {
                    "value": "50",
                    "label": "next.settingsSpec.server.integrations.advanced.groups.0.rows.3.control.options.0.label"
                  },
                  {
                    "value": "200",
                    "label": "next.settingsSpec.server.integrations.advanced.groups.0.rows.3.control.options.1.label"
                  },
                  {
                    "value": "1000",
                    "label": "next.settingsSpec.server.integrations.advanced.groups.0.rows.3.control.options.2.label"
                  }
                ]
              }
            },
            {
              "id": "integrations.webhookHttps",
              "scope": "server",
              "key": "integrations.webhook_https_only",
              "label": "next.settingsSpec.server.integrations.advanced.groups.0.rows.4.label",
              "help": "next.settingsSpec.server.integrations.advanced.groups.0.rows.4.help",
              "control": {
                "type": "toggle",
                "default": false
              }
            }
          ]
        },
        {
          "title": "next.settingsSpec.server.integrations.advanced.groups.1.title",
          "rows": [
            {
              "id": "integrations.requestsPerMinute",
              "scope": "server",
              "key": "integrations.api_requests_per_minute",
              "label": "next.settingsSpec.server.integrations.advanced.groups.1.rows.0.label",
              "help": "next.settingsSpec.server.integrations.advanced.groups.1.rows.0.help",
              "control": {
                "type": "number",
                "default": 600,
                "unit": "next.settingsSpec.server.integrations.advanced.groups.1.rows.0.control.unit",
                "min": 60,
                "max": 100000,
                "step": 10
              }
            },
            {
              "id": "integrations.credentialLife",
              "scope": "server",
              "key": "integrations.credential_max_days",
              "label": "next.settingsSpec.server.integrations.advanced.groups.1.rows.1.label",
              "help": "next.settingsSpec.server.integrations.advanced.groups.1.rows.1.help",
              "control": {
                "type": "select",
                "default": "none",
                "options": [
                  {
                    "value": "none",
                    "label": "next.settingsSpec.server.integrations.advanced.groups.1.rows.1.control.options.0.label",
                    "hint": "next.settingsSpec.server.integrations.advanced.groups.1.rows.1.control.options.0.hint"
                  },
                  {
                    "value": "365",
                    "label": "next.settingsSpec.server.integrations.advanced.groups.1.rows.1.control.options.1.label"
                  },
                  {
                    "value": "90",
                    "label": "next.settingsSpec.server.integrations.advanced.groups.1.rows.1.control.options.2.label"
                  },
                  {
                    "value": "30",
                    "label": "next.settingsSpec.server.integrations.advanced.groups.1.rows.1.control.options.3.label"
                  }
                ]
              }
            },
            {
              "id": "integrations.memberTokenScope",
              "scope": "server",
              "key": "integrations.member_token_scope",
              "label": "next.settingsSpec.server.integrations.advanced.groups.1.rows.2.label",
              "help": "next.settingsSpec.server.integrations.advanced.groups.1.rows.2.help",
              "control": {
                "type": "select",
                "default": "same",
                "options": [
                  {
                    "value": "same",
                    "label": "next.settingsSpec.server.integrations.advanced.groups.1.rows.2.control.options.0.label"
                  },
                  {
                    "value": "play",
                    "label": "next.settingsSpec.server.integrations.advanced.groups.1.rows.2.control.options.1.label"
                  },
                  {
                    "value": "read",
                    "label": "next.settingsSpec.server.integrations.advanced.groups.1.rows.2.control.options.2.label"
                  }
                ]
              }
            },
            {
              "id": "integrations.tokensPerMember",
              "scope": "server",
              "key": "integrations.tokens_per_member",
              "label": "next.settingsSpec.server.integrations.advanced.groups.1.rows.3.label",
              "control": {
                "type": "select",
                "default": "25",
                "options": [
                  {
                    "value": "5",
                    "label": "next.settingsSpec.server.integrations.advanced.groups.1.rows.3.control.options.0.label"
                  },
                  {
                    "value": "10",
                    "label": "next.settingsSpec.server.integrations.advanced.groups.1.rows.3.control.options.1.label"
                  },
                  {
                    "value": "25",
                    "label": "next.settingsSpec.server.integrations.advanced.groups.1.rows.3.control.options.2.label"
                  },
                  {
                    "value": "50",
                    "label": "next.settingsSpec.server.integrations.advanced.groups.1.rows.3.control.options.3.label"
                  }
                ]
              }
            },
            {
              "id": "integrations.keysMax",
              "scope": "server",
              "key": "integrations.keys_max",
              "label": "next.settingsSpec.server.integrations.advanced.groups.1.rows.4.label",
              "control": {
                "type": "select",
                "default": "100",
                "options": [
                  {
                    "value": "10",
                    "label": "next.settingsSpec.server.integrations.advanced.groups.1.rows.4.control.options.0.label"
                  },
                  {
                    "value": "25",
                    "label": "next.settingsSpec.server.integrations.advanced.groups.1.rows.4.control.options.1.label"
                  },
                  {
                    "value": "100",
                    "label": "next.settingsSpec.server.integrations.advanced.groups.1.rows.4.control.options.2.label"
                  },
                  {
                    "value": "250",
                    "label": "next.settingsSpec.server.integrations.advanced.groups.1.rows.4.control.options.3.label"
                  }
                ]
              }
            },
            {
              "id": "integrations.keyInAddress",
              "scope": "server",
              "key": "integrations.key_in_address",
              "label": "next.settingsSpec.server.integrations.advanced.groups.1.rows.5.label",
              "help": "next.settingsSpec.server.integrations.advanced.groups.1.rows.5.help",
              "control": {
                "type": "toggle",
                "default": true
              }
            }
          ]
        },
        {
          "title": "next.settingsSpec.server.integrations.advanced.groups.2.title",
          "rows": [
            {
              "id": "integrations.assistantsOn",
              "scope": "server",
              "key": "integrations.assistants_enabled",
              "label": "next.settingsSpec.server.integrations.advanced.groups.2.rows.0.label",
              "help": "next.settingsSpec.server.integrations.advanced.groups.2.rows.0.help",
              "control": {
                "type": "toggle",
                "default": true
              }
            }
          ]
        }
      ]
    },
    "configOnly": [
      {
        "key": "integrations.max_webhooks",
        "label": "next.settingsSpec.server.integrations.configOnly.0.label"
      }
    ]
  },
  {
    "scope": "server",
    "id": "streaming",
    "navGroup": "The server",
    "icon": "swap",
    "kind": "settings",
    "title": "next.settingsSpec.server.streaming.title",
    "groups": [
      {
        "title": "next.settingsSpec.server.streaming.groups.0.title",
        "rows": [
          {
            "id": "streaming.awayQuality",
            "scope": "server",
            "key": "streaming.away_quality",
            "label": "next.settingsSpec.server.streaming.groups.0.rows.0.label",
            "control": {
              "type": "select",
              "default": "original",
              "options": [
                {
                  "value": "original",
                  "label": "next.settingsSpec.server.streaming.groups.0.rows.0.control.options.0.label",
                  "hint": "next.settingsSpec.server.streaming.groups.0.rows.0.control.options.0.hint"
                },
                {
                  "value": "4k",
                  "label": "next.settingsSpec.server.streaming.groups.0.rows.0.control.options.1.label",
                  "hint": "next.settingsSpec.server.streaming.groups.0.rows.0.control.options.1.hint"
                },
                {
                  "value": "1080p-high",
                  "label": "next.settingsSpec.server.streaming.groups.0.rows.0.control.options.2.label",
                  "hint": "next.settingsSpec.server.streaming.groups.0.rows.0.control.options.2.hint"
                },
                {
                  "value": "1080p-low",
                  "label": "next.settingsSpec.server.streaming.groups.0.rows.0.control.options.3.label",
                  "hint": "next.settingsSpec.server.streaming.groups.0.rows.0.control.options.3.hint"
                },
                {
                  "value": "720p-high",
                  "label": "next.settingsSpec.server.streaming.groups.0.rows.0.control.options.4.label",
                  "hint": "next.settingsSpec.server.streaming.groups.0.rows.0.control.options.4.hint"
                },
                {
                  "value": "720p-low",
                  "label": "next.settingsSpec.server.streaming.groups.0.rows.0.control.options.5.label",
                  "hint": "next.settingsSpec.server.streaming.groups.0.rows.0.control.options.5.hint"
                },
                {
                  "value": "480p",
                  "label": "next.settingsSpec.server.streaming.groups.0.rows.0.control.options.6.label",
                  "hint": "next.settingsSpec.server.streaming.groups.0.rows.0.control.options.6.hint"
                },
                {
                  "value": "240p",
                  "label": "next.settingsSpec.server.streaming.groups.0.rows.0.control.options.7.label",
                  "hint": "next.settingsSpec.server.streaming.groups.0.rows.0.control.options.7.hint"
                },
                {
                  "value": "custom",
                  "label": "next.settingsSpec.server.streaming.groups.0.rows.0.control.options.8.label"
                }
              ]
            }
          },
          {
            "id": "streaming.musicAwayQuality",
            "scope": "server",
            "key": "streaming.music_away_quality",
            "label": "next.settingsSpec.server.streaming.groups.0.rows.1.label",
            "control": {
              "type": "select",
              "default": "original",
              "options": [
                {
                  "value": "original",
                  "label": "next.settingsSpec.server.streaming.groups.0.rows.1.control.options.0.label",
                  "hint": "next.settingsSpec.server.streaming.groups.0.rows.1.control.options.0.hint"
                },
                {
                  "value": "cd",
                  "label": "next.settingsSpec.server.streaming.groups.0.rows.1.control.options.1.label",
                  "hint": "next.settingsSpec.server.streaming.groups.0.rows.1.control.options.1.hint"
                },
                {
                  "value": "320",
                  "label": "next.settingsSpec.server.streaming.groups.0.rows.1.control.options.2.label",
                  "hint": "next.settingsSpec.server.streaming.groups.0.rows.1.control.options.2.hint"
                },
                {
                  "value": "96",
                  "label": "next.settingsSpec.server.streaming.groups.0.rows.1.control.options.3.label",
                  "hint": "next.settingsSpec.server.streaming.groups.0.rows.1.control.options.3.hint"
                }
              ]
            }
          },
          {
            "id": "streaming.uploadSpeed",
            "scope": "server",
            "key": "streaming.upload_speed",
            "label": "next.settingsSpec.server.streaming.groups.0.rows.2.label",
            "help": "next.settingsSpec.server.streaming.groups.0.rows.2.help",
            "control": {
              "type": "number",
              "default": null,
              "style": "field",
              "unit": "next.settingsSpec.server.streaming.groups.0.rows.2.control.unit",
              "min": 1,
              "max": 100000,
              "step": 1,
              "optional": true,
              "placeholder": "next.settingsSpec.server.streaming.groups.0.rows.2.control.placeholder"
            }
          }
        ]
      },
      {
        "title": "next.settingsSpec.server.streaming.groups.1.title",
        "rows": [
          {
            "id": "streaming.downloadQuality",
            "scope": "server",
            "key": "downloads.max_quality",
            "label": "next.settingsSpec.server.streaming.groups.1.rows.0.label",
            "help": "next.settingsSpec.server.streaming.groups.1.rows.0.help",
            "control": {
              "type": "select",
              "default": "original",
              "options": [
                {
                  "value": "original",
                  "label": "next.settingsSpec.server.streaming.groups.1.rows.0.control.options.0.label",
                  "hint": "next.settingsSpec.server.streaming.groups.1.rows.0.control.options.0.hint"
                },
                {
                  "value": "4k",
                  "label": "next.settingsSpec.server.streaming.groups.1.rows.0.control.options.1.label",
                  "hint": "next.settingsSpec.server.streaming.groups.1.rows.0.control.options.1.hint"
                },
                {
                  "value": "1080p-high",
                  "label": "next.settingsSpec.server.streaming.groups.1.rows.0.control.options.2.label",
                  "hint": "next.settingsSpec.server.streaming.groups.1.rows.0.control.options.2.hint"
                },
                {
                  "value": "1080p-low",
                  "label": "next.settingsSpec.server.streaming.groups.1.rows.0.control.options.3.label",
                  "hint": "next.settingsSpec.server.streaming.groups.1.rows.0.control.options.3.hint"
                },
                {
                  "value": "720p-high",
                  "label": "next.settingsSpec.server.streaming.groups.1.rows.0.control.options.4.label",
                  "hint": "next.settingsSpec.server.streaming.groups.1.rows.0.control.options.4.hint"
                },
                {
                  "value": "720p-low",
                  "label": "next.settingsSpec.server.streaming.groups.1.rows.0.control.options.5.label",
                  "hint": "next.settingsSpec.server.streaming.groups.1.rows.0.control.options.5.hint"
                },
                {
                  "value": "480p",
                  "label": "next.settingsSpec.server.streaming.groups.1.rows.0.control.options.6.label",
                  "hint": "next.settingsSpec.server.streaming.groups.1.rows.0.control.options.6.hint"
                },
                {
                  "value": "240p",
                  "label": "next.settingsSpec.server.streaming.groups.1.rows.0.control.options.7.label",
                  "hint": "next.settingsSpec.server.streaming.groups.1.rows.0.control.options.7.hint"
                }
              ]
            }
          }
        ]
      },
      {
        "title": "next.settingsSpec.server.streaming.groups.2.title",
        "rows": [
          {
            "id": "streaming.allowConversion",
            "scope": "server",
            "key": "streaming.allow_conversion",
            "label": "next.settingsSpec.server.streaming.groups.2.rows.0.label",
            "help": "next.settingsSpec.server.streaming.groups.2.rows.0.help",
            "control": {
              "type": "select",
              "default": "all",
              "options": [
                {
                  "value": "all",
                  "label": "next.settingsSpec.server.streaming.groups.2.rows.0.control.options.0.label",
                  "hint": "next.settingsSpec.server.streaming.groups.2.rows.0.control.options.0.hint"
                },
                {
                  "value": "sound",
                  "label": "next.settingsSpec.server.streaming.groups.2.rows.0.control.options.1.label",
                  "hint": "next.settingsSpec.server.streaming.groups.2.rows.0.control.options.1.hint"
                },
                {
                  "value": "never",
                  "label": "next.settingsSpec.server.streaming.groups.2.rows.0.control.options.2.label",
                  "hint": "next.settingsSpec.server.streaming.groups.2.rows.0.control.options.2.hint"
                }
              ]
            }
          },
          {
            "id": "streaming.transcodeLocation",
            "scope": "server",
            "key": "streaming.transcode_location",
            "label": "next.settingsSpec.server.streaming.groups.2.rows.1.label",
            "help": "next.settingsSpec.server.streaming.groups.2.rows.1.help",
            "control": {
              "type": "select",
              "default": "memory",
              "options": [
                {
                  "value": "memory",
                  "label": "next.settingsSpec.server.streaming.groups.2.rows.1.control.options.0.label"
                },
                {
                  "value": "disk",
                  "label": "next.settingsSpec.server.streaming.groups.2.rows.1.control.options.1.label"
                }
              ]
            },
            "children": [
              {
                "id": "streaming.transcodeFolder",
                "scope": "server",
                "key": "streaming.transcode_folder",
                "label": "next.settingsSpec.server.streaming.groups.2.rows.1.children.0.label",
                "help": "next.settingsSpec.server.streaming.groups.2.rows.1.children.0.help",
                "control": {
                  "type": "folder"
                }
              }
            ]
          },
          {
            "id": "streaming.transcodeStatus",
            "scope": "server",
            "label": "next.settingsSpec.server.streaming.groups.2.rows.2.label",
            "control": {
              "type": "status"
            }
          }
        ]
      },
      {
        "title": "next.settingsSpec.server.streaming.groups.3.title",
        "rows": [
          {
            "id": "streaming.hwError",
            "scope": "server",
            "label": "next.settingsSpec.server.streaming.groups.3.rows.0.label",
            "control": {
              "type": "status"
            }
          },
          {
            "id": "streaming.hwDevices",
            "scope": "server",
            "noChanged": true,
            "label": "next.settingsSpec.server.streaming.groups.3.rows.1.label",
            "control": {
              "type": "list"
            }
          },
          {
            "id": "streaming.hwLimit",
            "scope": "server",
            "label": "next.settingsSpec.server.streaming.groups.3.rows.2.label",
            "control": {
              "type": "status"
            }
          },
          {
            "id": "streaming.hwFormats",
            "scope": "server",
            "label": "next.settingsSpec.server.streaming.groups.3.rows.3.label",
            "control": {
              "type": "table",
              "style": "grid",
              "noun": [
                "next.settingsSpec.server.streaming.groups.3.rows.3.control.noun.0",
                "next.settingsSpec.server.streaming.groups.3.rows.3.control.noun.1"
              ],
              "filters": [],
              "columns": [
                {
                  "id": "format",
                  "label": "next.settingsSpec.server.streaming.groups.3.rows.3.control.columns.0.label",
                  "kind": "text",
                  "width": "34%"
                },
                {
                  "id": "decode",
                  "label": "next.settingsSpec.server.streaming.groups.3.rows.3.control.columns.1.label",
                  "kind": "badge",
                  "width": "33%"
                },
                {
                  "id": "encode",
                  "label": "next.settingsSpec.server.streaming.groups.3.rows.3.control.columns.2.label",
                  "kind": "badge"
                }
              ]
            }
          },
          {
            "id": "streaming.hwToneMapping",
            "scope": "server",
            "label": "next.settingsSpec.server.streaming.groups.3.rows.4.label",
            "control": {
              "type": "status"
            }
          },
          {
            "id": "streaming.hwTest",
            "scope": "server",
            "label": "next.settingsSpec.server.streaming.groups.3.rows.5.label",
            "control": {
              "type": "action",
              "style": "glass",
              "label": "next.settingsSpec.server.streaming.groups.3.rows.5.control.label"
            }
          }
        ]
      }
    ],
    "advanced": {
      "groups": [
        {
          "title": "next.settingsSpec.server.streaming.advanced.groups.0.title",
          "rows": [
            {
              "id": "streaming.hardware",
              "scope": "server",
              "key": "streaming.hardware_device",
              "enableWhen": {
                "id": "streaming.allowConversion",
                "in": [
                  "all"
                ]
              },
              "label": "next.settingsSpec.server.streaming.advanced.groups.0.rows.0.label",
              "control": {
                "type": "select",
                "default": "auto",
                "options": [
                  {
                    "value": "auto",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.0.rows.0.control.options.0.label",
                    "hint": "next.settingsSpec.server.streaming.advanced.groups.0.rows.0.control.options.0.hint"
                  },
                  {
                    "value": "software",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.0.rows.0.control.options.1.label"
                  }
                ],
                "dynamicOptions": true
              },
              "children": [
                {
                  "id": "streaming.hardwareDecode",
                  "scope": "server",
                  "key": "streaming.hardware_decode",
                  "enableWhen": {
                    "id": "streaming.hardware",
                    "notIn": [
                      "software"
                    ]
                  },
                  "label": "next.settingsSpec.server.streaming.advanced.groups.0.rows.0.children.0.label",
                  "help": "next.settingsSpec.server.streaming.advanced.groups.0.rows.0.children.0.help",
                  "control": {
                    "type": "toggle",
                    "default": true
                  }
                }
              ]
            }
          ]
        },
        {
          "title": "next.settingsSpec.server.streaming.advanced.groups.1.title",
          "rows": [
            {
              "id": "streaming.toneMapping",
              "scope": "server",
              "key": "streaming.tone_mapping",
              "enableWhen": {
                "id": "streaming.allowConversion",
                "in": [
                  "all"
                ]
              },
              "label": "next.settingsSpec.server.streaming.advanced.groups.1.rows.0.label",
              "help": "next.settingsSpec.server.streaming.advanced.groups.1.rows.0.help",
              "control": {
                "type": "toggle",
                "default": true
              },
              "children": [
                {
                  "id": "streaming.toneMappingMethod",
                  "scope": "server",
                  "key": "streaming.tone_mapping_method",
                  "enableWhen": {
                    "id": "streaming.toneMapping",
                    "in": [
                      true
                    ]
                  },
                  "label": "next.settingsSpec.server.streaming.advanced.groups.1.rows.0.children.0.label",
                  "control": {
                    "type": "select",
                    "default": "hable",
                    "options": [
                      {
                        "value": "hable",
                        "label": "next.settingsSpec.server.streaming.advanced.groups.1.rows.0.children.0.control.options.0.label",
                        "hint": "next.settingsSpec.server.streaming.advanced.groups.1.rows.0.children.0.control.options.0.hint"
                      },
                      {
                        "value": "mobius",
                        "label": "next.settingsSpec.server.streaming.advanced.groups.1.rows.0.children.0.control.options.1.label",
                        "hint": "next.settingsSpec.server.streaming.advanced.groups.1.rows.0.children.0.control.options.1.hint"
                      },
                      {
                        "value": "reinhard",
                        "label": "next.settingsSpec.server.streaming.advanced.groups.1.rows.0.children.0.control.options.2.label",
                        "hint": "next.settingsSpec.server.streaming.advanced.groups.1.rows.0.children.0.control.options.2.hint"
                      },
                      {
                        "value": "gamma",
                        "label": "next.settingsSpec.server.streaming.advanced.groups.1.rows.0.children.0.control.options.3.label",
                        "hint": "next.settingsSpec.server.streaming.advanced.groups.1.rows.0.children.0.control.options.3.hint"
                      },
                      {
                        "value": "linear",
                        "label": "next.settingsSpec.server.streaming.advanced.groups.1.rows.0.children.0.control.options.4.label",
                        "hint": "next.settingsSpec.server.streaming.advanced.groups.1.rows.0.children.0.control.options.4.hint"
                      },
                      {
                        "value": "clip",
                        "label": "next.settingsSpec.server.streaming.advanced.groups.1.rows.0.children.0.control.options.5.label",
                        "hint": "next.settingsSpec.server.streaming.advanced.groups.1.rows.0.children.0.control.options.5.hint"
                      }
                    ]
                  }
                }
              ]
            }
          ]
        },
        {
          "title": "next.settingsSpec.server.streaming.advanced.groups.2.title",
          "rows": [
            {
              "id": "streaming.encoderSpeed",
              "scope": "server",
              "key": "streaming.encoder_speed",
              "enableWhen": {
                "id": "streaming.allowConversion",
                "in": [
                  "all"
                ]
              },
              "label": "next.settingsSpec.server.streaming.advanced.groups.2.rows.0.label",
              "help": "next.settingsSpec.server.streaming.advanced.groups.2.rows.0.help",
              "control": {
                "type": "select",
                "default": "veryfast",
                "options": [
                  {
                    "value": "ultrafast",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.2.rows.0.control.options.0.label",
                    "hint": "next.settingsSpec.server.streaming.advanced.groups.2.rows.0.control.options.0.hint"
                  },
                  {
                    "value": "superfast",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.2.rows.0.control.options.1.label"
                  },
                  {
                    "value": "veryfast",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.2.rows.0.control.options.2.label"
                  },
                  {
                    "value": "faster",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.2.rows.0.control.options.3.label"
                  },
                  {
                    "value": "fast",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.2.rows.0.control.options.4.label"
                  },
                  {
                    "value": "medium",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.2.rows.0.control.options.5.label",
                    "hint": "next.settingsSpec.server.streaming.advanced.groups.2.rows.0.control.options.5.hint"
                  },
                  {
                    "value": "slow",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.2.rows.0.control.options.6.label",
                    "hint": "next.settingsSpec.server.streaming.advanced.groups.2.rows.0.control.options.6.hint"
                  }
                ]
              }
            },
            {
              "id": "streaming.whenCantPlay",
              "scope": "server",
              "key": "streaming.when_cant_play",
              "enableWhen": {
                "id": "streaming.allowConversion",
                "in": [
                  "all",
                  "sound"
                ]
              },
              "label": "next.settingsSpec.server.streaming.advanced.groups.2.rows.1.label",
              "control": {
                "type": "select",
                "default": "least",
                "options": [
                  {
                    "value": "least",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.2.rows.1.control.options.0.label"
                  },
                  {
                    "value": "most",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.2.rows.1.control.options.1.label"
                  }
                ]
              }
            },
            {
              "id": "streaming.repackage",
              "scope": "server",
              "key": "streaming.direct_stream_remux",
              "label": "next.settingsSpec.server.streaming.advanced.groups.2.rows.2.label",
              "help": "next.settingsSpec.server.streaming.advanced.groups.2.rows.2.help",
              "control": {
                "type": "toggle",
                "default": true
              }
            },
            {
              "id": "streaming.outputFormat",
              "scope": "server",
              "key": "streaming.output_format",
              "enableWhen": {
                "id": "streaming.allowConversion",
                "in": [
                  "all"
                ]
              },
              "label": "next.settingsSpec.server.streaming.advanced.groups.2.rows.3.label",
              "help": "next.settingsSpec.server.streaming.advanced.groups.2.rows.3.help",
              "control": {
                "type": "select",
                "default": "auto",
                "options": [
                  {
                    "value": "auto",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.2.rows.3.control.options.0.label",
                    "hint": "next.settingsSpec.server.streaming.advanced.groups.2.rows.3.control.options.0.hint"
                  },
                  {
                    "value": "h264",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.2.rows.3.control.options.1.label",
                    "hint": "next.settingsSpec.server.streaming.advanced.groups.2.rows.3.control.options.1.hint"
                  }
                ]
              }
            }
          ]
        },
        {
          "title": "next.settingsSpec.server.streaming.advanced.groups.3.title",
          "rows": [
            {
              "id": "streaming.maxConversions",
              "scope": "server",
              "key": "streaming.max_conversions",
              "enableWhen": {
                "id": "streaming.allowConversion",
                "in": [
                  "all",
                  "sound"
                ]
              },
              "label": "next.settingsSpec.server.streaming.advanced.groups.3.rows.0.label",
              "help": "next.settingsSpec.server.streaming.advanced.groups.3.rows.0.help",
              "control": {
                "type": "select",
                "default": "unlimited",
                "options": [
                  {
                    "value": "unlimited",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.3.rows.0.control.options.0.label"
                  },
                  {
                    "value": "1",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.3.rows.0.control.options.1.label"
                  },
                  {
                    "value": "2",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.3.rows.0.control.options.2.label"
                  },
                  {
                    "value": "3",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.3.rows.0.control.options.3.label"
                  },
                  {
                    "value": "4",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.3.rows.0.control.options.4.label"
                  },
                  {
                    "value": "6",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.3.rows.0.control.options.5.label"
                  },
                  {
                    "value": "8",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.3.rows.0.control.options.6.label"
                  }
                ]
              }
            },
            {
              "id": "streaming.maxHardwareConversions",
              "scope": "server",
              "key": "streaming.max_hardware_conversions",
              "enableWhen": {
                "id": "streaming.hardware",
                "notIn": [
                  "software"
                ]
              },
              "label": "next.settingsSpec.server.streaming.advanced.groups.3.rows.1.label",
              "help": "next.settingsSpec.server.streaming.advanced.groups.3.rows.1.help",
              "control": {
                "type": "select",
                "default": "auto",
                "options": [
                  {
                    "value": "auto",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.3.rows.1.control.options.0.label",
                    "hint": "next.settingsSpec.server.streaming.advanced.groups.3.rows.1.control.options.0.hint"
                  },
                  {
                    "value": "1",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.3.rows.1.control.options.1.label"
                  },
                  {
                    "value": "2",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.3.rows.1.control.options.2.label"
                  },
                  {
                    "value": "3",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.3.rows.1.control.options.3.label"
                  },
                  {
                    "value": "5",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.3.rows.1.control.options.4.label"
                  },
                  {
                    "value": "8",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.3.rows.1.control.options.5.label"
                  }
                ]
              }
            },
            {
              "id": "streaming.maxSoftwareConversions",
              "scope": "server",
              "key": "streaming.max_software_conversions",
              "enableWhen": {
                "id": "streaming.allowConversion",
                "in": [
                  "all",
                  "sound"
                ]
              },
              "label": "next.settingsSpec.server.streaming.advanced.groups.3.rows.2.label",
              "help": "next.settingsSpec.server.streaming.advanced.groups.3.rows.2.help",
              "control": {
                "type": "select",
                "default": "unlimited",
                "options": [
                  {
                    "value": "unlimited",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.3.rows.2.control.options.0.label"
                  },
                  {
                    "value": "1",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.3.rows.2.control.options.1.label"
                  },
                  {
                    "value": "2",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.3.rows.2.control.options.2.label"
                  },
                  {
                    "value": "3",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.3.rows.2.control.options.3.label"
                  },
                  {
                    "value": "4",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.3.rows.2.control.options.4.label"
                  },
                  {
                    "value": "6",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.3.rows.2.control.options.5.label"
                  }
                ]
              }
            },
            {
              "id": "streaming.maxBackgroundConversions",
              "scope": "server",
              "key": "streaming.max_background_conversions",
              "enableWhen": {
                "id": "streaming.allowConversion",
                "in": [
                  "all",
                  "sound"
                ]
              },
              "label": "next.settingsSpec.server.streaming.advanced.groups.3.rows.3.label",
              "help": "next.settingsSpec.server.streaming.advanced.groups.3.rows.3.help",
              "control": {
                "type": "select",
                "default": "unlimited",
                "options": [
                  {
                    "value": "unlimited",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.3.rows.3.control.options.0.label"
                  },
                  {
                    "value": "1",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.3.rows.3.control.options.1.label"
                  },
                  {
                    "value": "2",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.3.rows.3.control.options.2.label"
                  },
                  {
                    "value": "3",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.3.rows.3.control.options.3.label"
                  },
                  {
                    "value": "4",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.3.rows.3.control.options.4.label"
                  }
                ]
              }
            }
          ]
        },
        {
          "title": "next.settingsSpec.server.streaming.advanced.groups.4.title",
          "rows": [
            {
              "id": "streaming.maxStreams",
              "scope": "server",
              "key": "streaming.max_streams",
              "label": "next.settingsSpec.server.streaming.advanced.groups.4.rows.0.label",
              "help": "next.settingsSpec.server.streaming.advanced.groups.4.rows.0.help",
              "control": {
                "type": "select",
                "default": "unlimited",
                "options": [
                  {
                    "value": "unlimited",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.4.rows.0.control.options.0.label"
                  },
                  {
                    "value": "2",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.4.rows.0.control.options.1.label"
                  },
                  {
                    "value": "4",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.4.rows.0.control.options.2.label"
                  },
                  {
                    "value": "6",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.4.rows.0.control.options.3.label"
                  },
                  {
                    "value": "10",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.4.rows.0.control.options.4.label"
                  },
                  {
                    "value": "20",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.4.rows.0.control.options.5.label"
                  },
                  {
                    "value": "50",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.4.rows.0.control.options.6.label"
                  }
                ]
              }
            },
            {
              "id": "streaming.pausedTimeout",
              "scope": "server",
              "key": "streaming.paused_timeout_minutes",
              "label": "next.settingsSpec.server.streaming.advanced.groups.4.rows.1.label",
              "help": "next.settingsSpec.server.streaming.advanced.groups.4.rows.1.help",
              "control": {
                "type": "select",
                "default": "0",
                "options": [
                  {
                    "value": "0",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.4.rows.1.control.options.0.label"
                  },
                  {
                    "value": "15",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.4.rows.1.control.options.1.label"
                  },
                  {
                    "value": "30",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.4.rows.1.control.options.2.label"
                  },
                  {
                    "value": "60",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.4.rows.1.control.options.3.label"
                  },
                  {
                    "value": "120",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.4.rows.1.control.options.4.label"
                  },
                  {
                    "value": "240",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.4.rows.1.control.options.5.label"
                  },
                  {
                    "value": "720",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.4.rows.1.control.options.6.label"
                  },
                  {
                    "value": "1440",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.4.rows.1.control.options.7.label"
                  }
                ]
              }
            },
            {
              "id": "streaming.awayExact",
              "scope": "server",
              "key": "streaming.away_limit_mbps",
              "enableWhen": {
                "id": "streaming.awayQuality",
                "in": [
                  "custom"
                ]
              },
              "label": "next.settingsSpec.server.streaming.advanced.groups.4.rows.2.label",
              "help": "next.settingsSpec.server.streaming.advanced.groups.4.rows.2.help",
              "control": {
                "type": "number",
                "default": 8,
                "unit": "next.settingsSpec.server.streaming.advanced.groups.4.rows.2.control.unit",
                "min": 0.5,
                "max": 200,
                "step": 0.5
              }
            }
          ]
        },
        {
          "title": "next.settingsSpec.server.streaming.advanced.groups.5.title",
          "rows": [
            {
              "id": "streaming.convertAhead",
              "scope": "server",
              "key": "streaming.convert_ahead_seconds",
              "enableWhen": {
                "id": "streaming.allowConversion",
                "in": [
                  "all",
                  "sound"
                ]
              },
              "label": "next.settingsSpec.server.streaming.advanced.groups.5.rows.0.label",
              "help": "next.settingsSpec.server.streaming.advanced.groups.5.rows.0.help",
              "control": {
                "type": "select",
                "default": "60",
                "options": [
                  {
                    "value": "20",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.5.rows.0.control.options.0.label"
                  },
                  {
                    "value": "60",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.5.rows.0.control.options.1.label"
                  },
                  {
                    "value": "120",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.5.rows.0.control.options.2.label"
                  },
                  {
                    "value": "300",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.5.rows.0.control.options.3.label"
                  },
                  {
                    "value": "600",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.5.rows.0.control.options.4.label"
                  }
                ]
              }
            },
            {
              "id": "streaming.rewindKeep",
              "scope": "server",
              "key": "streaming.rewind_keep_seconds",
              "enableWhen": {
                "id": "streaming.allowConversion",
                "in": [
                  "all",
                  "sound"
                ]
              },
              "label": "next.settingsSpec.server.streaming.advanced.groups.5.rows.1.label",
              "help": "next.settingsSpec.server.streaming.advanced.groups.5.rows.1.help",
              "control": {
                "type": "select",
                "default": "180",
                "options": [
                  {
                    "value": "0",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.5.rows.1.control.options.0.label"
                  },
                  {
                    "value": "60",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.5.rows.1.control.options.1.label"
                  },
                  {
                    "value": "180",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.5.rows.1.control.options.2.label"
                  },
                  {
                    "value": "600",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.5.rows.1.control.options.3.label"
                  },
                  {
                    "value": "1800",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.5.rows.1.control.options.4.label"
                  },
                  {
                    "value": "3600",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.5.rows.1.control.options.5.label"
                  }
                ]
              }
            },
            {
              "id": "streaming.sessionSpace",
              "scope": "server",
              "key": "streaming.session_space_mb",
              "enableWhen": {
                "id": "streaming.allowConversion",
                "in": [
                  "all",
                  "sound"
                ]
              },
              "label": "next.settingsSpec.server.streaming.advanced.groups.5.rows.2.label",
              "help": "next.settingsSpec.server.streaming.advanced.groups.5.rows.2.help",
              "control": {
                "type": "select",
                "default": "4096",
                "options": [
                  {
                    "value": "1024",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.5.rows.2.control.options.0.label"
                  },
                  {
                    "value": "2048",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.5.rows.2.control.options.1.label"
                  },
                  {
                    "value": "4096",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.5.rows.2.control.options.2.label"
                  },
                  {
                    "value": "8192",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.5.rows.2.control.options.3.label"
                  },
                  {
                    "value": "16384",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.5.rows.2.control.options.4.label"
                  },
                  {
                    "value": "32768",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.5.rows.2.control.options.5.label"
                  }
                ]
              }
            },
            {
              "id": "streaming.memoryCap",
              "scope": "server",
              "key": "streaming.memory_cap",
              "enableWhen": {
                "id": "streaming.transcodeLocation",
                "in": [
                  "memory"
                ]
              },
              "label": "next.settingsSpec.server.streaming.advanced.groups.5.rows.3.label",
              "help": "next.settingsSpec.server.streaming.advanced.groups.5.rows.3.help",
              "control": {
                "type": "select",
                "default": "quarter",
                "options": [
                  {
                    "value": "quarter",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.5.rows.3.control.options.0.label"
                  },
                  {
                    "value": "half",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.5.rows.3.control.options.1.label"
                  },
                  {
                    "value": "2gb",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.5.rows.3.control.options.2.label"
                  },
                  {
                    "value": "4gb",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.5.rows.3.control.options.3.label"
                  },
                  {
                    "value": "8gb",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.5.rows.3.control.options.4.label"
                  },
                  {
                    "value": "16gb",
                    "label": "next.settingsSpec.server.streaming.advanced.groups.5.rows.3.control.options.5.label"
                  }
                ]
              }
            }
          ]
        },
        {
          "title": "next.settingsSpec.server.streaming.advanced.groups.6.title",
          "rows": [
            {
              "id": "streaming.ffmpegPath",
              "scope": "server",
              "key": "streaming.ffmpeg_path",
              "label": "next.settingsSpec.server.streaming.advanced.groups.6.rows.0.label",
              "help": "next.settingsSpec.server.streaming.advanced.groups.6.rows.0.help",
              "control": {
                "type": "text",
                "secret": false,
                "placeholder": "next.settingsSpec.server.streaming.advanced.groups.6.rows.0.control.placeholder"
              }
            }
          ]
        }
      ]
    },
    "configOnly": []
  },
  {
    "scope": "server",
    "id": "storage",
    "navGroup": "The server",
    "icon": "storage",
    "kind": "settings",
    "title": "next.settingsSpec.server.storage.title",
    "groups": [
      {
        "title": "next.settingsSpec.server.storage.groups.0.title",
        "rows": [
          {
            "id": "storage.dataFolder",
            "scope": "server",
            "label": "next.settingsSpec.server.storage.groups.0.rows.0.label",
            "help": "next.settingsSpec.server.storage.groups.0.rows.0.help",
            "control": {
              "type": "readonly",
              "mono": true
            }
          },
          {
            "id": "storage.networkNotice",
            "scope": "server",
            "label": "next.settingsSpec.server.storage.groups.0.rows.1.label",
            "help": "next.settingsSpec.server.storage.groups.0.rows.1.help",
            "control": {
              "type": "status"
            }
          },
          {
            "id": "storage.usage",
            "scope": "server",
            "label": "next.settingsSpec.server.storage.groups.0.rows.2.label",
            "help": "next.settingsSpec.server.storage.groups.0.rows.2.help",
            "control": {
              "type": "list"
            }
          },
          {
            "id": "storage.emptyCache",
            "scope": "server",
            "label": "next.settingsSpec.server.storage.groups.0.rows.3.label",
            "help": "next.settingsSpec.server.storage.groups.0.rows.3.help",
            "control": {
              "type": "action",
              "style": "glass",
              "label": "next.settingsSpec.server.storage.groups.0.rows.3.control.label",
              "confirm": "next.settingsSpec.server.storage.groups.0.rows.3.control.confirm"
            }
          }
        ]
      },
      {
        "title": "next.settingsSpec.server.storage.groups.1.title",
        "rows": [
          {
            "id": "storage.backupFolder",
            "scope": "server",
            "key": "storage.backup_dir",
            "label": "next.settingsSpec.server.storage.groups.1.rows.0.label",
            "help": "next.settingsSpec.server.storage.groups.1.rows.0.help",
            "control": {
              "type": "folder"
            }
          },
          {
            "id": "storage.backupKeep",
            "scope": "server",
            "key": "storage.backups_to_keep",
            "label": "next.settingsSpec.server.storage.groups.1.rows.1.label",
            "help": "next.settingsSpec.server.storage.groups.1.rows.1.help",
            "control": {
              "type": "select",
              "default": "7",
              "options": [
                {
                  "value": "3",
                  "label": "next.settingsSpec.server.storage.groups.1.rows.1.control.options.0.label"
                },
                {
                  "value": "7",
                  "label": "next.settingsSpec.server.storage.groups.1.rows.1.control.options.1.label"
                },
                {
                  "value": "14",
                  "label": "next.settingsSpec.server.storage.groups.1.rows.1.control.options.2.label"
                },
                {
                  "value": "30",
                  "label": "next.settingsSpec.server.storage.groups.1.rows.1.control.options.3.label"
                },
                {
                  "value": "custom",
                  "label": "next.settingsSpec.server.storage.groups.1.rows.1.control.options.4.label"
                }
              ]
            }
          },
          {
            "id": "storage.backupSchedule",
            "scope": "server",
            "key": "backup.schedule",
            "label": "next.settingsSpec.server.storage.groups.1.rows.2.label",
            "control": {
              "type": "select",
              "default": "nightly",
              "options": [
                {
                  "value": "nightly",
                  "label": "next.settingsSpec.server.storage.groups.1.rows.2.control.options.0.label"
                },
                {
                  "value": "weekdays",
                  "label": "next.settingsSpec.server.storage.groups.1.rows.2.control.options.1.label"
                },
                {
                  "value": "weekends",
                  "label": "next.settingsSpec.server.storage.groups.1.rows.2.control.options.2.label"
                },
                {
                  "value": "weekly",
                  "label": "next.settingsSpec.server.storage.groups.1.rows.2.control.options.3.label"
                },
                {
                  "value": "custom",
                  "label": "next.settingsSpec.server.storage.groups.1.rows.2.control.options.4.label"
                },
                {
                  "value": "never",
                  "label": "next.settingsSpec.server.storage.groups.1.rows.2.control.options.5.label"
                }
              ]
            },
            "children": [
              {
                "id": "storage.backupTime",
                "scope": "server",
                "key": "backup.start_time",
                "enableWhen": {
                  "id": "storage.backupSchedule",
                  "in": [
                    "nightly",
                    "weekdays",
                    "weekends",
                    "weekly",
                    "custom"
                  ]
                },
                "label": "next.settingsSpec.server.storage.groups.1.rows.2.children.0.label",
                "control": {
                  "type": "select",
                  "default": "180",
                  "options": [
                    {
                      "value": "0",
                      "label": "next.settingsSpec.server.storage.groups.1.rows.2.children.0.control.options.0.label"
                    },
                    {
                      "value": "60",
                      "label": "next.settingsSpec.server.storage.groups.1.rows.2.children.0.control.options.1.label"
                    },
                    {
                      "value": "120",
                      "label": "next.settingsSpec.server.storage.groups.1.rows.2.children.0.control.options.2.label"
                    },
                    {
                      "value": "180",
                      "label": "next.settingsSpec.server.storage.groups.1.rows.2.children.0.control.options.3.label"
                    },
                    {
                      "value": "240",
                      "label": "next.settingsSpec.server.storage.groups.1.rows.2.children.0.control.options.4.label"
                    },
                    {
                      "value": "300",
                      "label": "next.settingsSpec.server.storage.groups.1.rows.2.children.0.control.options.5.label"
                    },
                    {
                      "value": "360",
                      "label": "next.settingsSpec.server.storage.groups.1.rows.2.children.0.control.options.6.label"
                    },
                    {
                      "value": "420",
                      "label": "next.settingsSpec.server.storage.groups.1.rows.2.children.0.control.options.7.label"
                    },
                    {
                      "value": "480",
                      "label": "next.settingsSpec.server.storage.groups.1.rows.2.children.0.control.options.8.label"
                    },
                    {
                      "value": "540",
                      "label": "next.settingsSpec.server.storage.groups.1.rows.2.children.0.control.options.9.label"
                    },
                    {
                      "value": "600",
                      "label": "next.settingsSpec.server.storage.groups.1.rows.2.children.0.control.options.10.label"
                    },
                    {
                      "value": "660",
                      "label": "next.settingsSpec.server.storage.groups.1.rows.2.children.0.control.options.11.label"
                    },
                    {
                      "value": "720",
                      "label": "next.settingsSpec.server.storage.groups.1.rows.2.children.0.control.options.12.label"
                    },
                    {
                      "value": "780",
                      "label": "next.settingsSpec.server.storage.groups.1.rows.2.children.0.control.options.13.label"
                    },
                    {
                      "value": "840",
                      "label": "next.settingsSpec.server.storage.groups.1.rows.2.children.0.control.options.14.label"
                    },
                    {
                      "value": "900",
                      "label": "next.settingsSpec.server.storage.groups.1.rows.2.children.0.control.options.15.label"
                    },
                    {
                      "value": "960",
                      "label": "next.settingsSpec.server.storage.groups.1.rows.2.children.0.control.options.16.label"
                    },
                    {
                      "value": "1020",
                      "label": "next.settingsSpec.server.storage.groups.1.rows.2.children.0.control.options.17.label"
                    },
                    {
                      "value": "1080",
                      "label": "next.settingsSpec.server.storage.groups.1.rows.2.children.0.control.options.18.label"
                    },
                    {
                      "value": "1140",
                      "label": "next.settingsSpec.server.storage.groups.1.rows.2.children.0.control.options.19.label"
                    },
                    {
                      "value": "1200",
                      "label": "next.settingsSpec.server.storage.groups.1.rows.2.children.0.control.options.20.label"
                    },
                    {
                      "value": "1260",
                      "label": "next.settingsSpec.server.storage.groups.1.rows.2.children.0.control.options.21.label"
                    },
                    {
                      "value": "1320",
                      "label": "next.settingsSpec.server.storage.groups.1.rows.2.children.0.control.options.22.label"
                    },
                    {
                      "value": "1380",
                      "label": "next.settingsSpec.server.storage.groups.1.rows.2.children.0.control.options.23.label"
                    }
                  ]
                }
              }
            ]
          },
          {
            "id": "storage.backupNow",
            "scope": "server",
            "label": "next.settingsSpec.server.storage.groups.1.rows.3.label",
            "control": {
              "type": "action",
              "style": "glass",
              "label": "next.settingsSpec.server.storage.groups.1.rows.3.control.label"
            }
          },
          {
            "id": "storage.backups",
            "scope": "server",
            "label": "next.settingsSpec.server.storage.groups.1.rows.4.label",
            "help": "next.settingsSpec.server.storage.groups.1.rows.4.help",
            "control": {
              "type": "list",
              "itemAction": {
                "label": "next.settingsSpec.server.storage.groups.1.rows.4.control.itemAction.label",
                "style": "glass",
                "confirm": "next.settingsSpec.server.storage.groups.1.rows.4.control.itemAction.confirm"
              }
            }
          },
          {
            "id": "storage.restoreFile",
            "scope": "server",
            "label": "next.settingsSpec.server.storage.groups.1.rows.5.label",
            "help": "next.settingsSpec.server.storage.groups.1.rows.5.help",
            "control": {
              "type": "link",
              "label": "next.settingsSpec.server.storage.groups.1.rows.5.control.label"
            }
          }
        ]
      }
    ],
    "advanced": {
      "groups": [
        {
          "title": "next.settingsSpec.server.storage.advanced.groups.0.title",
          "rows": [
            {
              "id": "storage.keepHistory",
              "scope": "server",
              "label": "next.settingsSpec.server.storage.advanced.groups.0.rows.0.label",
              "control": {
                "type": "link",
                "label": "next.settingsSpec.server.storage.advanced.groups.0.rows.0.control.label",
                "to": "server/history/history.keepHistory"
              }
            },
            {
              "id": "storage.keepNotifications",
              "scope": "server",
              "key": "storage.keep_notifications_days",
              "label": "next.settingsSpec.server.storage.advanced.groups.0.rows.1.label",
              "control": {
                "type": "select",
                "default": "180",
                "options": [
                  {
                    "value": "7",
                    "label": "next.settingsSpec.server.storage.advanced.groups.0.rows.1.control.options.0.label"
                  },
                  {
                    "value": "30",
                    "label": "next.settingsSpec.server.storage.advanced.groups.0.rows.1.control.options.1.label"
                  },
                  {
                    "value": "90",
                    "label": "next.settingsSpec.server.storage.advanced.groups.0.rows.1.control.options.2.label"
                  },
                  {
                    "value": "180",
                    "label": "next.settingsSpec.server.storage.advanced.groups.0.rows.1.control.options.3.label"
                  }
                ]
              }
            },
            {
              "id": "storage.keepLogsLink",
              "scope": "server",
              "label": "next.settingsSpec.server.storage.advanced.groups.0.rows.2.label",
              "control": {
                "type": "link",
                "label": "next.settingsSpec.server.storage.advanced.groups.0.rows.2.control.label",
                "to": "server/troubleshooting/troubleshooting.keepLogs"
              }
            },
            {
              "id": "storage.keepFiles",
              "scope": "server",
              "key": "storage.keep_generated_files",
              "label": "next.settingsSpec.server.storage.advanced.groups.0.rows.3.label",
              "control": {
                "type": "select",
                "default": "balanced",
                "options": [
                  {
                    "value": "balanced",
                    "label": "next.settingsSpec.server.storage.advanced.groups.0.rows.3.control.options.0.label",
                    "hint": "next.settingsSpec.server.storage.advanced.groups.0.rows.3.control.options.0.hint",
                    "sets": {
                      "storage.keepConversions": "7",
                      "storage.keepPrepared": "90",
                      "storage.keepDownloads": "30",
                      "storage.keepSubtitles": "365",
                      "storage.keepTrickplay": "365"
                    }
                  },
                  {
                    "value": "short",
                    "label": "next.settingsSpec.server.storage.advanced.groups.0.rows.3.control.options.1.label",
                    "hint": "next.settingsSpec.server.storage.advanced.groups.0.rows.3.control.options.1.hint",
                    "sets": {
                      "storage.keepConversions": "1",
                      "storage.keepPrepared": "30",
                      "storage.keepDownloads": "14",
                      "storage.keepSubtitles": "90",
                      "storage.keepTrickplay": "90"
                    }
                  },
                  {
                    "value": "long",
                    "label": "next.settingsSpec.server.storage.advanced.groups.0.rows.3.control.options.2.label",
                    "hint": "next.settingsSpec.server.storage.advanced.groups.0.rows.3.control.options.2.hint",
                    "sets": {
                      "storage.keepConversions": "30",
                      "storage.keepPrepared": "365",
                      "storage.keepDownloads": "90",
                      "storage.keepSubtitles": "1095",
                      "storage.keepTrickplay": "1095"
                    }
                  },
                  {
                    "value": "custom",
                    "label": "next.settingsSpec.server.storage.advanced.groups.0.rows.3.control.options.3.label",
                    "hint": "next.settingsSpec.server.storage.advanced.groups.0.rows.3.control.options.3.hint"
                  }
                ]
              },
              "children": [
                {
                  "id": "storage.keepConversions",
                  "scope": "server",
                  "key": "storage.keep_conversions_days",
                  "label": "next.settingsSpec.server.storage.advanced.groups.0.rows.3.children.0.label",
                  "control": {
                    "type": "select",
                    "default": "7",
                    "options": [
                      {
                        "value": "1",
                        "label": "next.settingsSpec.server.storage.advanced.groups.0.rows.3.children.0.control.options.0.label"
                      },
                      {
                        "value": "3",
                        "label": "next.settingsSpec.server.storage.advanced.groups.0.rows.3.children.0.control.options.1.label"
                      },
                      {
                        "value": "7",
                        "label": "next.settingsSpec.server.storage.advanced.groups.0.rows.3.children.0.control.options.2.label"
                      },
                      {
                        "value": "14",
                        "label": "next.settingsSpec.server.storage.advanced.groups.0.rows.3.children.0.control.options.3.label"
                      },
                      {
                        "value": "30",
                        "label": "next.settingsSpec.server.storage.advanced.groups.0.rows.3.children.0.control.options.4.label"
                      },
                      {
                        "value": "90",
                        "label": "next.settingsSpec.server.storage.advanced.groups.0.rows.3.children.0.control.options.5.label"
                      }
                    ]
                  }
                },
                {
                  "id": "storage.keepPrepared",
                  "scope": "server",
                  "key": "storage.keep_prepared_days",
                  "label": "next.settingsSpec.server.storage.advanced.groups.0.rows.3.children.1.label",
                  "help": "next.settingsSpec.server.storage.advanced.groups.0.rows.3.children.1.help",
                  "control": {
                    "type": "select",
                    "default": "90",
                    "options": [
                      {
                        "value": "7",
                        "label": "next.settingsSpec.server.storage.advanced.groups.0.rows.3.children.1.control.options.0.label"
                      },
                      {
                        "value": "30",
                        "label": "next.settingsSpec.server.storage.advanced.groups.0.rows.3.children.1.control.options.1.label"
                      },
                      {
                        "value": "90",
                        "label": "next.settingsSpec.server.storage.advanced.groups.0.rows.3.children.1.control.options.2.label"
                      },
                      {
                        "value": "180",
                        "label": "next.settingsSpec.server.storage.advanced.groups.0.rows.3.children.1.control.options.3.label"
                      },
                      {
                        "value": "365",
                        "label": "next.settingsSpec.server.storage.advanced.groups.0.rows.3.children.1.control.options.4.label"
                      },
                      {
                        "value": "1095",
                        "label": "next.settingsSpec.server.storage.advanced.groups.0.rows.3.children.1.control.options.5.label"
                      }
                    ]
                  }
                },
                {
                  "id": "storage.keepDownloads",
                  "scope": "server",
                  "key": "storage.keep_downloads_days",
                  "label": "next.settingsSpec.server.storage.advanced.groups.0.rows.3.children.2.label",
                  "control": {
                    "type": "select",
                    "default": "30",
                    "options": [
                      {
                        "value": "7",
                        "label": "next.settingsSpec.server.storage.advanced.groups.0.rows.3.children.2.control.options.0.label"
                      },
                      {
                        "value": "14",
                        "label": "next.settingsSpec.server.storage.advanced.groups.0.rows.3.children.2.control.options.1.label"
                      },
                      {
                        "value": "30",
                        "label": "next.settingsSpec.server.storage.advanced.groups.0.rows.3.children.2.control.options.2.label"
                      },
                      {
                        "value": "90",
                        "label": "next.settingsSpec.server.storage.advanced.groups.0.rows.3.children.2.control.options.3.label"
                      },
                      {
                        "value": "365",
                        "label": "next.settingsSpec.server.storage.advanced.groups.0.rows.3.children.2.control.options.4.label"
                      }
                    ]
                  }
                },
                {
                  "id": "storage.keepSubtitles",
                  "scope": "server",
                  "key": "storage.keep_subtitles_days",
                  "label": "next.settingsSpec.server.storage.advanced.groups.0.rows.3.children.3.label",
                  "help": "next.settingsSpec.server.storage.advanced.groups.0.rows.3.children.3.help",
                  "control": {
                    "type": "select",
                    "default": "365",
                    "options": [
                      {
                        "value": "30",
                        "label": "next.settingsSpec.server.storage.advanced.groups.0.rows.3.children.3.control.options.0.label"
                      },
                      {
                        "value": "90",
                        "label": "next.settingsSpec.server.storage.advanced.groups.0.rows.3.children.3.control.options.1.label"
                      },
                      {
                        "value": "365",
                        "label": "next.settingsSpec.server.storage.advanced.groups.0.rows.3.children.3.control.options.2.label"
                      },
                      {
                        "value": "1095",
                        "label": "next.settingsSpec.server.storage.advanced.groups.0.rows.3.children.3.control.options.3.label"
                      },
                      {
                        "value": "3650",
                        "label": "next.settingsSpec.server.storage.advanced.groups.0.rows.3.children.3.control.options.4.label"
                      }
                    ]
                  }
                },
                {
                  "id": "storage.keepTrickplay",
                  "scope": "server",
                  "key": "storage.keep_trickplay_days",
                  "label": "next.settingsSpec.server.storage.advanced.groups.0.rows.3.children.4.label",
                  "control": {
                    "type": "select",
                    "default": "365",
                    "options": [
                      {
                        "value": "30",
                        "label": "next.settingsSpec.server.storage.advanced.groups.0.rows.3.children.4.control.options.0.label"
                      },
                      {
                        "value": "90",
                        "label": "next.settingsSpec.server.storage.advanced.groups.0.rows.3.children.4.control.options.1.label"
                      },
                      {
                        "value": "365",
                        "label": "next.settingsSpec.server.storage.advanced.groups.0.rows.3.children.4.control.options.2.label"
                      },
                      {
                        "value": "1095",
                        "label": "next.settingsSpec.server.storage.advanced.groups.0.rows.3.children.4.control.options.3.label"
                      },
                      {
                        "value": "3650",
                        "label": "next.settingsSpec.server.storage.advanced.groups.0.rows.3.children.4.control.options.4.label"
                      }
                    ]
                  }
                }
              ]
            }
          ]
        },
        {
          "title": "next.settingsSpec.server.storage.advanced.groups.1.title",
          "rows": [
            {
              "id": "storage.analysisSpace",
              "scope": "server",
              "key": "storage.analysis_space_gb",
              "label": "next.settingsSpec.server.storage.advanced.groups.1.rows.0.label",
              "help": "next.settingsSpec.server.storage.advanced.groups.1.rows.0.help",
              "control": {
                "type": "select",
                "default": "32",
                "options": [
                  {
                    "value": "16",
                    "label": "next.settingsSpec.server.storage.advanced.groups.1.rows.0.control.options.0.label"
                  },
                  {
                    "value": "32",
                    "label": "next.settingsSpec.server.storage.advanced.groups.1.rows.0.control.options.1.label"
                  },
                  {
                    "value": "64",
                    "label": "next.settingsSpec.server.storage.advanced.groups.1.rows.0.control.options.2.label"
                  },
                  {
                    "value": "128",
                    "label": "next.settingsSpec.server.storage.advanced.groups.1.rows.0.control.options.3.label"
                  },
                  {
                    "value": "256",
                    "label": "next.settingsSpec.server.storage.advanced.groups.1.rows.0.control.options.4.label"
                  },
                  {
                    "value": "512",
                    "label": "next.settingsSpec.server.storage.advanced.groups.1.rows.0.control.options.5.label"
                  },
                  {
                    "value": "1024",
                    "label": "next.settingsSpec.server.storage.advanced.groups.1.rows.0.control.options.6.label"
                  }
                ]
              }
            },
            {
              "id": "storage.analysisStage",
              "scope": "server",
              "key": "storage.analysis_stage_limit_mb",
              "label": "next.settingsSpec.server.storage.advanced.groups.1.rows.1.label",
              "help": "next.settingsSpec.server.storage.advanced.groups.1.rows.1.help",
              "control": {
                "type": "select",
                "default": "256",
                "options": [
                  {
                    "value": "64",
                    "label": "next.settingsSpec.server.storage.advanced.groups.1.rows.1.control.options.0.label"
                  },
                  {
                    "value": "128",
                    "label": "next.settingsSpec.server.storage.advanced.groups.1.rows.1.control.options.1.label"
                  },
                  {
                    "value": "256",
                    "label": "next.settingsSpec.server.storage.advanced.groups.1.rows.1.control.options.2.label"
                  },
                  {
                    "value": "512",
                    "label": "next.settingsSpec.server.storage.advanced.groups.1.rows.1.control.options.3.label"
                  },
                  {
                    "value": "1024",
                    "label": "next.settingsSpec.server.storage.advanced.groups.1.rows.1.control.options.4.label"
                  },
                  {
                    "value": "2048",
                    "label": "next.settingsSpec.server.storage.advanced.groups.1.rows.1.control.options.5.label"
                  },
                  {
                    "value": "4096",
                    "label": "next.settingsSpec.server.storage.advanced.groups.1.rows.1.control.options.6.label"
                  }
                ]
              }
            },
            {
              "id": "storage.subtitleTracks",
              "scope": "server",
              "key": "storage.subtitle_tracks_per_file",
              "label": "next.settingsSpec.server.storage.advanced.groups.1.rows.2.label",
              "help": "next.settingsSpec.server.storage.advanced.groups.1.rows.2.help",
              "control": {
                "type": "select",
                "default": "128",
                "options": [
                  {
                    "value": "64",
                    "label": "next.settingsSpec.server.storage.advanced.groups.1.rows.2.control.options.0.label"
                  },
                  {
                    "value": "128",
                    "label": "next.settingsSpec.server.storage.advanced.groups.1.rows.2.control.options.1.label"
                  },
                  {
                    "value": "256",
                    "label": "next.settingsSpec.server.storage.advanced.groups.1.rows.2.control.options.2.label"
                  },
                  {
                    "value": "512",
                    "label": "next.settingsSpec.server.storage.advanced.groups.1.rows.2.control.options.3.label"
                  },
                  {
                    "value": "1024",
                    "label": "next.settingsSpec.server.storage.advanced.groups.1.rows.2.control.options.4.label"
                  },
                  {
                    "value": "4096",
                    "label": "next.settingsSpec.server.storage.advanced.groups.1.rows.2.control.options.5.label"
                  }
                ]
              }
            },
            {
              "id": "storage.subtitleFile",
              "scope": "server",
              "key": "storage.subtitle_file_limit_mb",
              "label": "next.settingsSpec.server.storage.advanced.groups.1.rows.3.label",
              "help": "next.settingsSpec.server.storage.advanced.groups.1.rows.3.help",
              "control": {
                "type": "select",
                "default": "128",
                "options": [
                  {
                    "value": "32",
                    "label": "next.settingsSpec.server.storage.advanced.groups.1.rows.3.control.options.0.label"
                  },
                  {
                    "value": "64",
                    "label": "next.settingsSpec.server.storage.advanced.groups.1.rows.3.control.options.1.label"
                  },
                  {
                    "value": "128",
                    "label": "next.settingsSpec.server.storage.advanced.groups.1.rows.3.control.options.2.label"
                  },
                  {
                    "value": "256",
                    "label": "next.settingsSpec.server.storage.advanced.groups.1.rows.3.control.options.3.label"
                  },
                  {
                    "value": "512",
                    "label": "next.settingsSpec.server.storage.advanced.groups.1.rows.3.control.options.4.label"
                  }
                ]
              }
            }
          ]
        },
        {
          "title": "next.settingsSpec.server.storage.advanced.groups.2.title",
          "rows": [
            {
              "id": "storage.downloadSpace",
              "scope": "server",
              "key": "downloads.max_prepared_gb",
              "label": "next.settingsSpec.server.storage.advanced.groups.2.rows.0.label",
              "help": "next.settingsSpec.server.storage.advanced.groups.2.rows.0.help",
              "control": {
                "type": "select",
                "default": "0",
                "options": [
                  {
                    "value": "0",
                    "label": "next.settingsSpec.server.storage.advanced.groups.2.rows.0.control.options.0.label"
                  },
                  {
                    "value": "10",
                    "label": "next.settingsSpec.server.storage.advanced.groups.2.rows.0.control.options.1.label"
                  },
                  {
                    "value": "25",
                    "label": "next.settingsSpec.server.storage.advanced.groups.2.rows.0.control.options.2.label"
                  },
                  {
                    "value": "50",
                    "label": "next.settingsSpec.server.storage.advanced.groups.2.rows.0.control.options.3.label"
                  },
                  {
                    "value": "100",
                    "label": "next.settingsSpec.server.storage.advanced.groups.2.rows.0.control.options.4.label"
                  },
                  {
                    "value": "250",
                    "label": "next.settingsSpec.server.storage.advanced.groups.2.rows.0.control.options.5.label"
                  },
                  {
                    "value": "500",
                    "label": "next.settingsSpec.server.storage.advanced.groups.2.rows.0.control.options.6.label"
                  }
                ]
              }
            },
            {
              "id": "storage.downloads",
              "scope": "server",
              "label": "next.settingsSpec.server.storage.advanced.groups.2.rows.1.label",
              "help": "next.settingsSpec.server.storage.advanced.groups.2.rows.1.help",
              "control": {
                "type": "list",
                "itemAction": {
                  "label": "next.settingsSpec.server.storage.advanced.groups.2.rows.1.control.itemAction.label",
                  "style": "glass",
                  "confirm": "next.settingsSpec.server.storage.advanced.groups.2.rows.1.control.itemAction.confirm",
                  "remove": true
                }
              }
            }
          ]
        },
        {
          "title": "next.settingsSpec.server.storage.advanced.groups.3.title",
          "rows": [
            {
              "id": "storage.backupKeepExact",
              "scope": "server",
              "key": "storage.backups_to_keep_exact",
              "enableWhen": {
                "id": "storage.backupKeep",
                "in": [
                  "custom"
                ]
              },
              "label": "next.settingsSpec.server.storage.advanced.groups.3.rows.0.label",
              "help": "next.settingsSpec.server.storage.advanced.groups.3.rows.0.help",
              "control": {
                "type": "number",
                "default": 7,
                "unit": "next.settingsSpec.server.storage.advanced.groups.3.rows.0.control.unit",
                "min": 1,
                "max": 365,
                "step": 1
              }
            },
            {
              "id": "storage.backupArtwork",
              "scope": "server",
              "key": "backup.include_artwork",
              "label": "next.settingsSpec.server.storage.advanced.groups.3.rows.1.label",
              "help": "next.settingsSpec.server.storage.advanced.groups.3.rows.1.help",
              "control": {
                "type": "toggle",
                "default": false
              }
            },
            {
              "id": "storage.backupPassphrase",
              "scope": "server",
              "key": "backup.passphrase_protected",
              "label": "next.settingsSpec.server.storage.advanced.groups.3.rows.2.label",
              "help": "next.settingsSpec.server.storage.advanced.groups.3.rows.2.help",
              "control": {
                "type": "toggle",
                "default": false
              },
              "children": [
                {
                  "id": "storage.backupKeysNote",
                  "scope": "server",
                  "enableWhen": {
                    "id": "storage.backupPassphrase",
                    "in": [
                      false
                    ]
                  },
                  "label": "next.settingsSpec.server.storage.advanced.groups.3.rows.2.children.0.label",
                  "control": {
                    "type": "status"
                  }
                }
              ]
            },
            {
              "id": "storage.backupWindowLength",
              "scope": "server",
              "key": "backup.window_hours",
              "enableWhen": {
                "id": "storage.backupSchedule",
                "in": [
                  "nightly",
                  "weekdays",
                  "weekends",
                  "weekly",
                  "custom"
                ]
              },
              "label": "next.settingsSpec.server.storage.advanced.groups.3.rows.3.label",
              "help": "next.settingsSpec.server.storage.advanced.groups.3.rows.3.help",
              "control": {
                "type": "select",
                "default": "4",
                "options": [
                  {
                    "value": "1",
                    "label": "next.settingsSpec.server.storage.advanced.groups.3.rows.3.control.options.0.label"
                  },
                  {
                    "value": "2",
                    "label": "next.settingsSpec.server.storage.advanced.groups.3.rows.3.control.options.1.label"
                  },
                  {
                    "value": "4",
                    "label": "next.settingsSpec.server.storage.advanced.groups.3.rows.3.control.options.2.label"
                  },
                  {
                    "value": "8",
                    "label": "next.settingsSpec.server.storage.advanced.groups.3.rows.3.control.options.3.label"
                  }
                ]
              }
            },
            {
              "id": "storage.backupDays",
              "scope": "server",
              "key": "backup.chosen_days",
              "enableWhen": {
                "id": "storage.backupSchedule",
                "in": [
                  "custom"
                ]
              },
              "label": "next.settingsSpec.server.storage.advanced.groups.3.rows.4.label",
              "control": {
                "type": "matrix",
                "columns": [
                  {
                    "value": "runs",
                    "label": "next.settingsSpec.server.storage.advanced.groups.3.rows.4.control.columns.0.label"
                  },
                  {
                    "value": "skips",
                    "label": "next.settingsSpec.server.storage.advanced.groups.3.rows.4.control.columns.1.label"
                  }
                ],
                "items": [
                  {
                    "id": "monday",
                    "label": "next.settingsSpec.server.storage.advanced.groups.3.rows.4.control.items.0.label",
                    "default": "runs"
                  },
                  {
                    "id": "tuesday",
                    "label": "next.settingsSpec.server.storage.advanced.groups.3.rows.4.control.items.1.label",
                    "default": "runs"
                  },
                  {
                    "id": "wednesday",
                    "label": "next.settingsSpec.server.storage.advanced.groups.3.rows.4.control.items.2.label",
                    "default": "runs"
                  },
                  {
                    "id": "thursday",
                    "label": "next.settingsSpec.server.storage.advanced.groups.3.rows.4.control.items.3.label",
                    "default": "runs"
                  },
                  {
                    "id": "friday",
                    "label": "next.settingsSpec.server.storage.advanced.groups.3.rows.4.control.items.4.label",
                    "default": "runs"
                  },
                  {
                    "id": "saturday",
                    "label": "next.settingsSpec.server.storage.advanced.groups.3.rows.4.control.items.5.label",
                    "default": "runs"
                  },
                  {
                    "id": "sunday",
                    "label": "next.settingsSpec.server.storage.advanced.groups.3.rows.4.control.items.6.label",
                    "default": "runs"
                  }
                ]
              }
            }
          ]
        },
        {
          "title": "next.settingsSpec.server.storage.advanced.groups.4.title",
          "rows": [
            {
              "id": "storage.upgradeCopy",
              "scope": "server",
              "label": "next.settingsSpec.server.storage.advanced.groups.4.rows.0.label",
              "help": "next.settingsSpec.server.storage.advanced.groups.4.rows.0.help",
              "control": {
                "type": "action",
                "style": "danger",
                "label": "next.settingsSpec.server.storage.advanced.groups.4.rows.0.control.label",
                "confirm": "next.settingsSpec.server.storage.advanced.groups.4.rows.0.control.confirm"
              }
            }
          ]
        }
      ]
    },
    "configOnly": []
  },
  {
    "scope": "server",
    "id": "troubleshooting",
    "navGroup": "The server",
    "icon": "wrench",
    "kind": "settings",
    "title": "next.settingsSpec.server.troubleshooting.title",
    "groups": [
      {
        "title": "next.settingsSpec.server.troubleshooting.groups.0.title",
        "rows": [
          {
            "id": "troubleshooting.detailWindow",
            "scope": "server",
            "label": "next.settingsSpec.server.troubleshooting.groups.0.rows.0.label",
            "help": "next.settingsSpec.server.troubleshooting.groups.0.rows.0.help",
            "control": {
              "type": "action",
              "style": "glass",
              "label": "next.settingsSpec.server.troubleshooting.groups.0.rows.0.control.label"
            }
          },
          {
            "id": "troubleshooting.messages",
            "scope": "server",
            "label": "next.settingsSpec.server.troubleshooting.groups.0.rows.1.label",
            "help": "next.settingsSpec.server.troubleshooting.groups.0.rows.1.help",
            "control": {
              "type": "link",
              "label": "next.settingsSpec.server.troubleshooting.groups.0.rows.1.control.label"
            }
          },
          {
            "id": "troubleshooting.feedback",
            "scope": "server",
            "label": "next.settingsSpec.server.troubleshooting.groups.0.rows.2.label",
            "control": {
              "type": "link",
              "label": "next.settingsSpec.server.troubleshooting.groups.0.rows.2.control.label"
            }
          },
          {
            "id": "troubleshooting.reports",
            "scope": "server",
            "label": "next.settingsSpec.server.troubleshooting.groups.0.rows.3.label",
            "help": "next.settingsSpec.server.troubleshooting.groups.0.rows.3.help",
            "control": {
              "type": "link",
              "label": "next.settingsSpec.server.troubleshooting.groups.0.rows.3.control.label"
            }
          },
          {
            "id": "troubleshooting.support",
            "scope": "server",
            "label": "next.settingsSpec.server.troubleshooting.groups.0.rows.4.label",
            "help": "next.settingsSpec.server.troubleshooting.groups.0.rows.4.help",
            "control": {
              "type": "link",
              "label": "next.settingsSpec.server.troubleshooting.groups.0.rows.4.control.label"
            }
          }
        ]
      },
      {
        "title": "next.settingsSpec.server.troubleshooting.groups.1.title",
        "rows": [
          {
            "id": "troubleshooting.prepareExport",
            "scope": "server",
            "label": "next.settingsSpec.server.troubleshooting.groups.1.rows.0.label",
            "help": "next.settingsSpec.server.troubleshooting.groups.1.rows.0.help",
            "control": {
              "type": "action",
              "style": "primary",
              "label": "next.settingsSpec.server.troubleshooting.groups.1.rows.0.control.label"
            },
            "children": [
              {
                "id": "troubleshooting.exportPaths",
                "scope": "server",
                "key": "support.export_include_paths",
                "label": "next.settingsSpec.server.troubleshooting.groups.1.rows.0.children.0.label",
                "help": "next.settingsSpec.server.troubleshooting.groups.1.rows.0.children.0.help",
                "control": {
                  "type": "toggle",
                  "default": false,
                  "style": "checkbox"
                }
              }
            ]
          }
        ]
      },
      {
        "title": "next.settingsSpec.server.troubleshooting.groups.2.title",
        "rows": [
          {
            "id": "troubleshooting.playbackProblems",
            "scope": "server",
            "label": "next.settingsSpec.server.troubleshooting.groups.2.rows.0.label",
            "help": "next.settingsSpec.server.troubleshooting.groups.2.rows.0.help",
            "control": {
              "type": "list",
              "itemAction": {
                "label": "next.settingsSpec.server.troubleshooting.groups.2.rows.0.control.itemAction.label",
                "style": "glass",
                "remove": true
              }
            }
          }
        ]
      }
    ],
    "advanced": {
      "groups": [
        {
          "title": "next.settingsSpec.server.troubleshooting.advanced.groups.0.title",
          "rows": [
            {
              "id": "troubleshooting.logLevel",
              "scope": "server",
              "key": "logging.level",
              "label": "next.settingsSpec.server.troubleshooting.advanced.groups.0.rows.0.label",
              "help": "next.settingsSpec.server.troubleshooting.advanced.groups.0.rows.0.help",
              "control": {
                "type": "select",
                "default": "info",
                "options": [
                  {
                    "value": "error",
                    "label": "next.settingsSpec.server.troubleshooting.advanced.groups.0.rows.0.control.options.0.label",
                    "hint": "next.settingsSpec.server.troubleshooting.advanced.groups.0.rows.0.control.options.0.hint"
                  },
                  {
                    "value": "warn",
                    "label": "next.settingsSpec.server.troubleshooting.advanced.groups.0.rows.0.control.options.1.label"
                  },
                  {
                    "value": "info",
                    "label": "next.settingsSpec.server.troubleshooting.advanced.groups.0.rows.0.control.options.2.label",
                    "hint": "next.settingsSpec.server.troubleshooting.advanced.groups.0.rows.0.control.options.2.hint"
                  },
                  {
                    "value": "debug",
                    "label": "next.settingsSpec.server.troubleshooting.advanced.groups.0.rows.0.control.options.3.label",
                    "hint": "next.settingsSpec.server.troubleshooting.advanced.groups.0.rows.0.control.options.3.hint"
                  }
                ]
              }
            },
            {
              "id": "troubleshooting.detailLength",
              "scope": "server",
              "key": "logging.detail_minutes",
              "label": "next.settingsSpec.server.troubleshooting.advanced.groups.0.rows.1.label",
              "control": {
                "type": "select",
                "default": "30",
                "options": [
                  {
                    "value": "15",
                    "label": "next.settingsSpec.server.troubleshooting.advanced.groups.0.rows.1.control.options.0.label"
                  },
                  {
                    "value": "30",
                    "label": "next.settingsSpec.server.troubleshooting.advanced.groups.0.rows.1.control.options.1.label"
                  },
                  {
                    "value": "60",
                    "label": "next.settingsSpec.server.troubleshooting.advanced.groups.0.rows.1.control.options.2.label"
                  },
                  {
                    "value": "120",
                    "label": "next.settingsSpec.server.troubleshooting.advanced.groups.0.rows.1.control.options.3.label"
                  },
                  {
                    "value": "240",
                    "label": "next.settingsSpec.server.troubleshooting.advanced.groups.0.rows.1.control.options.4.label"
                  }
                ]
              }
            }
          ]
        },
        {
          "title": "next.settingsSpec.server.troubleshooting.advanced.groups.1.title",
          "rows": [
            {
              "id": "troubleshooting.exportWindow",
              "scope": "server",
              "key": "support.export_window",
              "label": "next.settingsSpec.server.troubleshooting.advanced.groups.1.rows.0.label",
              "help": "next.settingsSpec.server.troubleshooting.advanced.groups.1.rows.0.help",
              "control": {
                "type": "select",
                "default": "24h",
                "options": [
                  {
                    "value": "1h",
                    "label": "next.settingsSpec.server.troubleshooting.advanced.groups.1.rows.0.control.options.0.label"
                  },
                  {
                    "value": "6h",
                    "label": "next.settingsSpec.server.troubleshooting.advanced.groups.1.rows.0.control.options.1.label"
                  },
                  {
                    "value": "24h",
                    "label": "next.settingsSpec.server.troubleshooting.advanced.groups.1.rows.0.control.options.2.label"
                  },
                  {
                    "value": "3d",
                    "label": "next.settingsSpec.server.troubleshooting.advanced.groups.1.rows.0.control.options.3.label"
                  },
                  {
                    "value": "7d",
                    "label": "next.settingsSpec.server.troubleshooting.advanced.groups.1.rows.0.control.options.4.label"
                  }
                ]
              }
            },
            {
              "id": "troubleshooting.exportRecords",
              "scope": "server",
              "key": "support.export_server_records",
              "label": "next.settingsSpec.server.troubleshooting.advanced.groups.1.rows.1.label",
              "help": "next.settingsSpec.server.troubleshooting.advanced.groups.1.rows.1.help",
              "control": {
                "type": "toggle",
                "default": true,
                "style": "checkbox"
              }
            },
            {
              "id": "troubleshooting.exportClient",
              "scope": "server",
              "key": "support.export_client_uploads",
              "label": "next.settingsSpec.server.troubleshooting.advanced.groups.1.rows.2.label",
              "help": "next.settingsSpec.server.troubleshooting.advanced.groups.1.rows.2.help",
              "control": {
                "type": "toggle",
                "default": false,
                "style": "checkbox"
              }
            },
            {
              "id": "troubleshooting.exportAudit",
              "scope": "server",
              "key": "support.export_audit",
              "label": "next.settingsSpec.server.troubleshooting.advanced.groups.1.rows.3.label",
              "help": "next.settingsSpec.server.troubleshooting.advanced.groups.1.rows.3.help",
              "control": {
                "type": "toggle",
                "default": true,
                "style": "checkbox"
              }
            },
            {
              "id": "troubleshooting.exportHealth",
              "scope": "server",
              "key": "support.export_health_snapshot",
              "label": "next.settingsSpec.server.troubleshooting.advanced.groups.1.rows.4.label",
              "help": "next.settingsSpec.server.troubleshooting.advanced.groups.1.rows.4.help",
              "control": {
                "type": "toggle",
                "default": true,
                "style": "checkbox"
              }
            }
          ]
        },
        {
          "title": "next.settingsSpec.server.troubleshooting.advanced.groups.2.title",
          "rows": [
            {
              "id": "troubleshooting.keepLogs",
              "scope": "server",
              "key": "logging.keep_days",
              "label": "next.settingsSpec.server.troubleshooting.advanced.groups.2.rows.0.label",
              "help": "next.settingsSpec.server.troubleshooting.advanced.groups.2.rows.0.help",
              "control": {
                "type": "select",
                "default": "30",
                "options": [
                  {
                    "value": "7",
                    "label": "next.settingsSpec.server.troubleshooting.advanced.groups.2.rows.0.control.options.0.label"
                  },
                  {
                    "value": "14",
                    "label": "next.settingsSpec.server.troubleshooting.advanced.groups.2.rows.0.control.options.1.label"
                  },
                  {
                    "value": "30",
                    "label": "next.settingsSpec.server.troubleshooting.advanced.groups.2.rows.0.control.options.2.label"
                  }
                ]
              }
            }
          ]
        },
        {
          "title": "next.settingsSpec.server.troubleshooting.advanced.groups.3.title",
          "rows": [
            {
              "id": "troubleshooting.keepLogsByKind",
              "scope": "server",
              "key": "logging.keep_days_by_kind",
              "label": "next.settingsSpec.server.troubleshooting.advanced.groups.3.rows.0.label",
              "control": {
                "type": "matrix",
                "columns": [
                  {
                    "value": "default",
                    "label": "next.settingsSpec.server.troubleshooting.advanced.groups.3.rows.0.control.columns.0.label"
                  },
                  {
                    "value": "7",
                    "label": "next.settingsSpec.server.troubleshooting.advanced.groups.3.rows.0.control.columns.1.label"
                  },
                  {
                    "value": "14",
                    "label": "next.settingsSpec.server.troubleshooting.advanced.groups.3.rows.0.control.columns.2.label"
                  },
                  {
                    "value": "30",
                    "label": "next.settingsSpec.server.troubleshooting.advanced.groups.3.rows.0.control.columns.3.label"
                  }
                ],
                "items": [
                  {
                    "id": "server",
                    "label": "next.settingsSpec.server.troubleshooting.advanced.groups.3.rows.0.control.items.0.label",
                    "default": "default"
                  },
                  {
                    "id": "playback",
                    "label": "next.settingsSpec.server.troubleshooting.advanced.groups.3.rows.0.control.items.1.label",
                    "default": "default"
                  },
                  {
                    "id": "scan",
                    "label": "next.settingsSpec.server.troubleshooting.advanced.groups.3.rows.0.control.items.2.label",
                    "default": "default"
                  },
                  {
                    "id": "network",
                    "label": "next.settingsSpec.server.troubleshooting.advanced.groups.3.rows.0.control.items.3.label",
                    "default": "default"
                  },
                  {
                    "id": "client",
                    "label": "next.settingsSpec.server.troubleshooting.advanced.groups.3.rows.0.control.items.4.label",
                    "default": "default"
                  }
                ]
              }
            }
          ]
        }
      ]
    },
    "configOnly": []
  }
];

/** Held descriptor shape for the authored People Sign-in reset action (auth19/21).
 * It is deliberately not inserted into settingsSpec yet: the current People action
 * renderer maps an unrecognised action row to bulk disable/enable. Placement is the
 * first row of the person Account tab's Sign-in group, before Turn off account. */
export const personMemberResetCodeRow: SettingsRow = Object.freeze({
  id: 'people.resetCode', scope: 'account', key: 'account.reset_code',
  label: 'next.settings.people.resetCode',
  control: Object.freeze({type: 'action', style: 'glass', label: 'next.settings.people.resetCode'}),
});

export type SettingsRowLocation = Readonly<{section: SettingsSection; row: SettingsRow; group: SettingsGroup; advanced: boolean; ancestors: readonly SettingsRow[]; panelId?: string; tabId?: string}>;
/** One index for rendering, links and search; panel rows never leak into page groups. */
export function indexSettingsSpec(sections: readonly SettingsSection[] = settingsSpec): ReadonlyMap<string, SettingsRowLocation> {
 const result = new Map<string, SettingsRowLocation>();
 for (const section of sections) {
  const visit = (holder: SettingsHolder, panelId?: string, tabId?: string, advanced = false) => {
   const rows = (items: readonly SettingsRow[], group: SettingsGroup, isAdvanced: boolean, ancestors: readonly SettingsRow[] = []) => {
    for (const row of items) {
     if (result.has(row.id)) throw new Error(`Duplicate Settings row: ${row.id}`);
     result.set(row.id, {section, row, group, advanced: isAdvanced, ancestors, ...(panelId ? {panelId} : {}), ...(tabId ? {tabId} : {})});
     rows(row.children ?? [], group, isAdvanced, [...ancestors, row]);
    }
   };
   for (const group of holder.groups ?? []) rows(group.rows, group, advanced);
   for (const group of holder.advanced?.groups ?? []) rows(group.rows, group, true);
  };
  visit(section);
  for (const [panelId, panel] of Object.entries(section.panels ?? {})) {
   visit(panel, panelId);
   for (const tab of panel.tabs ?? []) visit(tab, panelId, tab.id, tab.id === 'advanced');
  }
 }
 return result;
}
export const settingsRowIndex = indexSettingsSpec();
