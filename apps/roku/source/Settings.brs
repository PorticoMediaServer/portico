' The Settings pages a Roku shows: the account pages of packages/client-core settings-structure.ts
' for a television, with the words of the shared catalogue (en-US). A row is drawn only when this
' app or the server honours it: the profile's own settings (saved for every device) and the ones the
' server applies to this device's playback. Rows that would change nothing on a Roku are left out.
'
' A row: {key (a preference), composite, type ("switch", "choice", "languages", "info", "action"),
' label, help, sheetHelp (said only in its choice sheet), scope ("This device" for a value kept for this device only), choices: [[value, words]],
' when: {key, equals} (shown only then), id (an info or action row the screen fills or runs)}.

function settingsPages() as object
    skips = [["ask", "Ask each time"], ["auto", "Skip automatically"], ["off", "Never"]]
    qualities = [["original", "Original"], ["automatic", "Automatic"], ["2160p", "4K"], ["1080p-high", "1080p High"], ["1080p-low", "1080p Low"], ["720p-high", "720p High"], ["720p-low", "720p Low"], ["480p", "480p"], ["240p", "240p"]]
    return [
        {id: "profile", title: "Profile & account", icon: "profile", intro: "", sections: [
            {title: "Signed in as", rows: [{type: "info", id: "identity"}]},
            {title: "", rows: [{type: "info", id: "porticoAccount", label: "Portico Account"}]},
            {title: "Devices", rows: [{type: "info", id: "devices"}]},
            {title: "", rows: [{type: "action", id: "signout", label: "Sign out", icon: "signOut", danger: true}]}
        ]},
        {id: "privacy", title: "Privacy", icon: "shield", intro: "The owner of this server can always see what is played on it, by whom and on which device. Pausing or clearing your own history does not change that.", sections: [
            {title: "", rows: [
                {key: "privacy.pauseWatchHistory", type: "switch", label: "Pause history", help: "Nothing new is added to your History, watched marks or recommendations while paused. Existing progress still resumes. The server’s owner can still see what was played."},
                {key: "privacy.showActivityToMembers", type: "switch", label: "Show my activity", help: "Lets others on this server see what you’re watching, and counts it in “Popular on this server”."},
                {key: "privacy.includeInWatchTogether", type: "switch", label: "Include me in Watch Together activity"},
                {key: "search.rememberHistory", type: "switch", label: "Remember recent searches"}
            ]},
            {title: "", rows: [{type: "action", id: "reset", label: "Reset recommendations", help: "Start over with what Portico recommends to you.", icon: "refresh"}]}
        ]},
        {id: "playback", title: "Playback", icon: "play", gapBefore: true, intro: "", sections: [
            {title: "Next episode", rows: [
                {key: "playback.autoplayNext", type: "switch", label: "Play the next episode automatically", help: "Start the next episode when one ends."},
                {key: "playback.upNextCountdownMs", type: "choice", label: "Wait before the next episode", help: "How long the next episode waits before starting.", when: {key: "playback.autoplayNext", equals: true}, choices: [[0, "Immediately"], [5000, "5 seconds"], [10000, "10 seconds"], [15000, "15 seconds"]]},
                {key: "playback.startNextDuringCredits", type: "switch", label: "Start the next episode during the credits", help: "When the credits are clearly marked, count down and start the next episode without waiting for them to end. Off: the next episode starts when this one ends.", when: {key: "playback.autoplayNext", equals: true}},
                {composite: "stillWatching", type: "choice", label: "Ask “Still watching?”", sheetHelp: "Pauses after several episodes in a row when no one has touched the controls.", choices: [["0", "Off"], ["2", "After 2 episodes"], ["3", "After 3 episodes"], ["4", "After 4 episodes"], ["5", "After 5 episodes"]]}
            ]},
            {title: "Skipping", rows: [
                {key: "playback.introSkip", type: "choice", label: "Intros", sheetHelp: "Skip detected intros.", choices: skips},
                {key: "playback.creditsSkip", type: "choice", label: "Credits", sheetHelp: "Skip detected credits.", choices: skips},
                {key: "playback.recapSkip", type: "choice", label: "Recaps", sheetHelp: "Skip detected recaps.", choices: skips},
                {key: "playback.skipBackMs", type: "choice", label: "Skip back", help: "How far the back button rewinds.", choices: [[5000, "5 seconds"], [10000, "10 seconds"], [15000, "15 seconds"], [30000, "30 seconds"]]},
                {key: "playback.skipForwardMs", type: "choice", label: "Skip forward", help: "How far the forward button jumps.", choices: [[10000, "10 seconds"], [15000, "15 seconds"], [30000, "30 seconds"], [60000, "60 seconds"]]}
            ]},
            {title: "Advanced", advanced: true, rows: [
                {key: "playback.playedThresholdPercent", type: "choice", label: "Mark as watched at", help: "Music uses this percentage. Videos use the library’s percentage until you change this value. Audiobooks finish near the end.", choices: [[80, "80%"], [85, "85%"], [90, "90%"], [95, "95%"], [100, "100%"]]},
                {key: "playback.startedThresholdPercent", type: "choice", label: "Counts as started", help: "How much you watch before a title joins Continue Watching.", choices: [[1, "1%"], [2, "2%"], [5, "5%"], [10, "10%"], [15, "15%"], [20, "20%"], [25, "25%"]]}
            ]}
        ]},
        {id: "languages", title: "Audio & subtitles", icon: "subtitles", intro: "", sections: [
            {title: "Audio", rows: [
                {key: "playback.preferredAudioLanguages", type: "languages", label: "Preferred audio languages", help: "Portico picks the first of these a title has. With none chosen, it plays the title’s original track."}
            ]},
            {title: "Subtitles", rows: [
                {key: "playback.subtitleMode", type: "choice", label: "Show subtitles", help: "Forced subtitles translate only the lines spoken in another language.", choices: [["off", "Off"], ["foreign", "When the audio isn’t in my language"], ["forced", "Forced only"], ["always", "Always"]]},
                {key: "playback.preferredSubtitleLanguages", type: "languages", label: "Preferred subtitle languages", help: "Portico picks the first of these a title has."}
            ]},
            {title: "Subtitle appearance", rows: [
                {key: "playback.subtitleSize", type: "choice", label: "Size", choices: [["small", "Small"], ["medium", "Medium"], ["large", "Large"], ["extra-large", "Extra large"]]},
                {key: "playback.subtitleBackground", type: "choice", label: "Background", choices: [["none", "None"], ["outline", "Outline"], ["shadow", "Drop shadow"], ["translucent", "Translucent"], ["opaque", "Solid"]]}
            ]}
        ]},
        {id: "quality", title: "Quality & data", icon: "quality", intro: "", sections: [
            {title: "Default quality", rows: [
                {key: "quality.home", type: "choice", label: "At home", scope: "This device", help: "When this device is on your server’s home network. Original plays every file as it is; Automatic starts there and adjusts to your connection. You can pick another quality for one viewing in the player.", choices: qualities},
                {key: "quality.remote", type: "choice", label: "Away from home", scope: "This device", help: "On any other network. Automatic starts with the original, lowers the quality only while your connection needs it, and raises it again when it can.", choices: qualities}
            ]},
            {title: "", rows: [
                {composite: "playbackMethod", type: "choice", label: "Playback method", scope: "This device", help: "Automatic plays the original file when this device can, and converts it when it can’t.", choices: [["automatic", "Automatic (recommended)"], ["original", "Always play the original"], ["convert", "Always convert"]]}
            ]}
        ]},
        ' Music & audiobooks (tv-more frame 27): only what the server applies to this TV's listening. The
        ' Roku plays songs one after another itself, with no volume leveling, crossfade, equalizer,
        ' lyrics, speed or skip buttons, so those rows would change nothing here and are left out.
        {id: "music", title: "Music & audiobooks", icon: "music", intro: "", sections: [
            {title: "Audiobooks", rows: [
                {key: "audiobooks.resumeRewindMs", type: "choice", label: "Rewind when resuming", choices: [[0, "Start where I stopped"], [5000, "5 seconds earlier"], [15000, "15 seconds earlier"], [30000, "30 seconds earlier"]]}
            ]},
            {title: "Quality", rows: [
                {key: "music.qualityAway", type: "choice", label: "Away from home", scope: "This device", help: "On any other network. At home, music plays as the original file.", choices: [["original", "Original"], ["cd-quality", "CD quality"], ["320", "320 kbps"], ["96", "96 kbps"]]}
            ]}
        ]},
        ' Appearance (tv-more frame 28). Card size and Text size are left out: every page here is laid
        ' out from fixed frames for one card size and one type scale. Layout is for Android boxes only.
        ' Reduce motion comes with the focus zoom it turns off (kit/Card and kit/Shelf).
        {id: "appearance", title: "Appearance", icon: "sliders", gapBefore: true, intro: "", sections: [
            {title: "", rows: [
                {key: "appearance.showBackdrops", type: "switch", label: "Show backdrops", help: "Artwork behind title pages and Home.", scope: "All TVs"},
                {key: "notifications.badges", type: "switch", label: "Show a dot for new notifications", help: "On the profile item in the rail."}
            ]}
        ]},
        {id: "region", title: "Language & region", icon: "globe", intro: "", sections: [
            {title: "", rows: [
                {key: "region.locale", type: "choice", label: "Language", help: "Audio and subtitle languages are in Audio & subtitles.", choices: [["auto", "Same as this device"], ["en-US", "English (United States)"], ["en-CA", "English (Canada)"], ["en-GB", "English (United Kingdom)"]]},
                {key: "region.hourCycle", type: "choice", label: "Clock", choices: [["auto", "Follow system"], ["h12", "12-hour"], ["h23", "24-hour"]]}
            ]},
            {title: "Formats", advanced: true, rows: [
                {key: "region.formats", type: "choice", label: "Region", choices: [["auto", "Same as language"], ["en-US", "United States"], ["en-CA", "Canada"], ["en-GB", "United Kingdom"], ["en-AU", "Australia"], ["fr-FR", "France"], ["de-DE", "Germany"], ["es-ES", "Spain"], ["ru-RU", "Russia"]]},
                {key: "region.dateOrder", type: "choice", label: "Date format", choices: [["auto", "Same as region"], ["dmy", "Day, month, year"], ["mdy", "Month, day, year"], ["ymd", "Year, month, day"]]},
                {key: "region.firstDay", type: "choice", label: "First day of the week", choices: [["auto", "Same as region"], ["mon", "Monday"], ["sun", "Sunday"], ["sat", "Saturday"]]},
                {key: "region.sizeUnits", type: "choice", label: "File sizes", choices: [["binary", "1 GB is 1,024 MB"], ["decimal", "1 GB is 1,000 MB"]]}
            ]}
        ]},
        {id: "device", title: "This device", icon: "tvDevice", intro: "", sections: [
            {title: "", rows: [{type: "info", id: "deviceName", label: "Device name"}]},
            {title: "About Portico", note: "Terms of Use and Privacy Policy: getportico.tv/terms and getportico.tv/privacy. Metadata and artwork are provided by TMDB and other providers configured on your server. Recommendation tags and similar titles in part from the Portico Title Dataset (CC BY-SA 4.0), built from Wikipedia and Wikidata.", rows: [
                {type: "info", id: "appVersion", label: "App version"},
                {type: "info", id: "server", label: "Server"},
                {type: "info", id: "serverVersion", label: "Server version"}
            ]}
        ]}
    ]
end function

' Two values compared whatever their kind (the server's numbers, the choices' numbers and words).
function sameSetting(a as dynamic, b as dynamic) as boolean
    return formatJson(a) = formatJson(b)
end function

' The words of a choice row's value, or "" when the value is none of its choices.
function choiceWords(choices as object, value as dynamic) as string
    for each choice in choices
        if sameSetting(choice[0], value) then return choice[1]
    end for
    return ""
end function

' "Ask “Still watching?”" and "Playback method" are one choice each that reads and writes several
' preferences (settings-structure.ts readComposite and compositePatch).
function compositeValue(id as string, values as object) as string
    if id = "stillWatching"
        if values["playback.passoutProtection"] = false then return "0"
        return formatJson(values["playback.passoutAfterEpisodes"])
    end if
    if values["delivery.directPlay"] = "never" then return "convert"
    if values["delivery.transcode"] = "never" then return "original"
    return "automatic"
end function

function compositePatch(id as string, choice as string) as object
    if id = "stillWatching"
        if choice = "0" then return {"playback.passoutProtection": false}
        return {"playback.passoutProtection": true, "playback.passoutAfterEpisodes": choice.toInt()}
    end if
    if choice = "original" then return {"delivery.directPlay": "require", "delivery.directStream": "never", "delivery.transcode": "never"}
    if choice = "convert" then return {"delivery.directPlay": "never", "delivery.directStream": "never", "delivery.transcode": "require"}
    return {"delivery.directPlay": "prefer", "delivery.directStream": "allow", "delivery.transcode": "allow"}
end function

' The profile's Appearance settings this TV honours, from its preferences (GET /v1/preferences):
' Show backdrops. MainScene keeps them in m.global.looks.
function appearanceLooks(values as object) as object
    return {showBackdrops: (values["appearance.showBackdrops"] <> false)}
end function

' The languages offered for adding, in the shared order (language-list.ts COMMON_LANGUAGES).
function commonLanguages() as object
    return ["en", "ja", "fr", "de", "es", "it", "pt", "nl", "sv", "no", "da", "fi", "pl", "cs", "sk", "hu", "ro", "el", "tr", "ru", "uk", "ar", "he", "hi", "th", "vi", "ko", "zh"]
end function

function languageName(code as string) as string
    names = {"@appLanguage": "Portico’s language", "@original": "Original language",
        en: "English", ja: "Japanese", fr: "French", de: "German", es: "Spanish", it: "Italian", pt: "Portuguese", nl: "Dutch",
        sv: "Swedish", no: "Norwegian", da: "Danish", fi: "Finnish", pl: "Polish", cs: "Czech", sk: "Slovak", hu: "Hungarian",
        ro: "Romanian", el: "Greek", tr: "Turkish", ru: "Russian", uk: "Ukrainian", ar: "Arabic", he: "Hebrew", hi: "Hindi",
        th: "Thai", vi: "Vietnamese", ko: "Korean", zh: "Chinese"}
    if names[code] <> invalid then return names[code]
    return code
end function

' The row's value: the languages in order, or "None chosen".
function languagesWords(list as object) as string
    if list.count() = 0 then return "None chosen"
    words = []
    for each code in list
        words.push(languageName(code))
    end for
    return words.join(", ")
end function

' The list with the language at `index` moved one place up (-1) or down (+1).
function movedLanguage(list as object, index as integer, by as integer) as object
    out = []
    for each code in list
        out.push(code)
    end for
    other = index + by
    if other < 0 or other >= out.count() then return out
    out[index] = list[other]
    out[other] = list[index]
    return out
end function

function withoutLanguage(list as object, index as integer) as object
    out = []
    for i = 0 to list.count() - 1
        if i <> index then out.push(list[i])
    end for
    return out
end function

' "Last seen Nov 18" for a device's lastSeenAt, in the TV's own time zone.
function lastSeenWords(iso as dynamic) as string
    if iso = invalid then return ""
    when = createObject("roDateTime")
    when.fromISO8601String(iso)
    when.toLocalTime()
    months = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"]
    return "Last seen " + months[when.getMonth() - 1] + " " + when.getDayOfMonth().toStr()
end function

' The icon for a device in the account's list, from the platform it reported.
function deviceIcon(platform as dynamic) as string
    if platform = "ios" or platform = "android" then return "phone"
    if platform = "ipados" then return "tablet"
    if platform = "roku" or platform = "tvos" or platform = "androidtv" or platform = "firetv" or platform = "webos" or platform = "tizen" then return "tvDevice"
    return "browser"
end function

' Search settings (tv-more frame 34): the rows whose name, help or section title holds the words,
' grouped by page in the list's order; a page whose title holds them is a match with no rows of its
' own. [{page (its place in pages), rows: [{row, section}]}]; nothing for no words.
function settingsMatches(pages as object, query as string) as object
    words = lcase(query.trim())
    out = []
    if words = "" then return out
    for i = 0 to pages.count() - 1
        page = pages[i]
        found = []
        for each section in page.sections
            for each row in section.rows
                if row.label <> invalid
                    text = lcase(row.label) + " " + lcase(section.title)
                    if row.help <> invalid then text = text + " " + lcase(row.help)
                    if instr(1, text, words) > 0 then found.push({row: row, section: section})
                end if
            end for
        end for
        if found.count() > 0 or instr(1, lcase(page.title), words) > 0 then out.push({page: i, rows: found})
    end for
    return out
end function
