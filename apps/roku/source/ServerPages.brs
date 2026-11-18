' Reads and plain rows for the view-only Server pages. The screen owns loading and drawing.
function serverPageReads(page as string) as object
    if page = "general" then return [
        {part: "general", path: "/v1/admin/settings/general"}
        {part: "updates", path: "/v1/admin/updates"}
    ]
    if page = "history" then return [{part: "history", path: "/v1/admin/play-history"}]
    if page = "remote-sources" then return [{part: "remoteSources", path: "/v1/admin/remote-sources"}]
    if page = "metadata" then return [{part: "metadata", path: "/v1/admin/metadata/settings"}]
    if page = "segments" then return [{part: "segments", path: "/v1/admin/segments"}]
    if page = "live" then return [
        {part: "liveSources", path: "/v1/admin/live/sources"}
        {part: "liveSettings", path: "/v1/admin/settings/live"}
    ]
    if page = "signin" then return [
        {part: "signin", path: "/v1/admin/sign-in"}
        {part: "signinPolicy", path: "/v1/admin/settings/sign-in"}
    ]
    if page = "integrations" then return [
        {part: "integrations", path: "/v1/admin/settings/integrations"}
        {part: "apiKeys", path: "/v1/admin/api-keys"}
        {part: "webhooks", path: "/v1/admin/webhooks"}
    ]
    if page = "streaming" then return [{part: "streaming", path: "/v1/admin/settings/streaming"}]
    if page = "storage" then return [
        {part: "backups", path: "/v1/admin/backups"}
        {part: "backupSettings", path: "/v1/admin/settings/backups"}
    ]
    if page = "troubleshooting" then return [{part: "logs", path: "/v1/admin/logs"}]
    return []
end function

function serverPageGroups(page as string, data as object) as object
    waiting = false
    failed = false
    for each read in serverPageReads(page)
        answer = data[read.part]
        if answer = invalid
            waiting = true
        else if answer.failed = true
            failed = true
        end if
    end for
    if failed
        for each entry in serverPages("owner")
            if entry.id = page
                message = "Couldn't read " + lcase(entry.title) + " just now."
                return [serverPageGroup(entry.title, [serverPageRow(message, "")])]
            end if
        end for
    end if
    if waiting then return []

    if page = "general"
        values = data.general.values
        updateChecks = {automatic: "Automatically", never: "Never", address: "My own address"}
        workHours = {any: "Any time", overnight: "Overnight only (1–6 am)", chosen: "Custom…"}
        speeds = {gentle: "Gentle", normal: "Normal", fast: "Fast"}
        return [
            serverPageGroup("Name", [serverPageRow("Server name", values.name)])
            serverPageGroup("Updates", [
                serverPageRow("Portico Server", data.updates.version)
                serverPageRow("Update checks", updateChecks[values.updateCheck])
            ])
            serverPageGroup("Background work", [
                serverPageRow("Heavy background work", workHours[values.heavyWork.hours])
                serverPageRow("Background speed", speeds[values.backgroundSpeed])
            ])
        ]
    end if

    if page = "history"
        rows = []
        for each play in data.history.items
            facts = []
            if play.subtitle <> invalid then facts.push(play.subtitle)
            person = play.username
            if play.profile <> invalid then person = person + " (" + play.profile + ")"
            facts.push(person)
            if play.deviceName <> invalid then facts.push(play.deviceName)
            rows.push(serverPageRow(play.title, serverPageTimeWords(play.playedAt), facts.join(" · ")))
            if rows.count() = 10 then exit for
        end for
        return [serverPageGroup("Plays", rows, "No plays match.")]
    end if

    if page = "remote-sources"
        rows = []
        kinds = {webdav: "WebDAV source", rclone: "Imported rclone source", drive: "Google Drive"}
        states = {connected: "Ready", setup: "Setup not finished", unreachable: "Can’t be reached",
            sign_in: "Needs its sign-in again", waiting: "Waiting", limited: "Limited", needs_attention: "Needs attention"}
        for each source in data.remoteSources.items
            state = states[source.state]
            if state = invalid then state = "Unavailable"
            rows.push(serverPageRow(source.name, state, kinds[source.kind]))
        end for
        return [serverPageGroup("Sources", rows, "No remote sources yet.")]
    end if

    if page = "metadata"
        metadata = data.metadata
        tmdb = "Portico’s key is in use"
        if metadata.tmdbCredentialConfigured then tmdb = "Your own key is in use"
        tvdb = "Not set up"
        if metadata.tvdbCredentialConfigured then tvdb = "Your own key is in use"
        return [
            serverPageGroup("Looking titles up", [serverPageRow("Include adult titles when matching", serverPageOnOff(metadata.includeAdult))])
            serverPageGroup("Providers", [
                serverPageRow("Your TMDB read token", tmdb)
                serverPageRow("Your TheTVDB project key", tvdb)
            ])
        ]
    end if

    if page = "segments"
        status = data.segments
        modes = {standard: "By listening to episodes", thorough: "By listening and reading the picture", off: "Off"}
        progress = "Up to date"
        if status.mode = "off"
            progress = "Off for this server"
        else if not status.catchUpComplete or status.showsWaiting > 0
            progress = "Working in the background"
        end if
        return [serverPageGroup("Detection", [
            serverPageRow("Find intros and credits", modes[status.mode])
            serverPageRow("Status", progress)
            serverPageRow("Files measured", status.files.complete.toStr())
            serverPageRow("Waiting", status.files.pending.toStr())
            serverPageRow("Couldn't be read", status.files.failed.toStr())
        ])]
    end if

    if page = "live"
        rows = []
        states = {new: "Waiting for its first refresh", refreshing: "Refreshing", up_to_date: "Up to date",
            trouble: "Having trouble", not_working: "Not working", needs_sign_in: "Needs its sign-in again", paused: "Paused"}
        for each source in data.liveSources.items
            state = states[source.state]
            if state = invalid then state = "Not checked"
            channels = ""
            if source.report <> invalid then channels = countText(source.report.channels, "channel", "channels")
            rows.push(serverPageRow(source.name, state, channels))
        end for
        values = data.liveSettings.values
        guide = {"3": "3 days", "7": "A week", "14": "Two weeks"}
        rewind = values.rewindMinutes.toStr() + " minutes"
        if values.rewindMinutes = 60 then rewind = "1 hour"
        if values.rewindMinutes = 120 then rewind = "2 hours"
        return [
            serverPageGroup("Sources", rows, "No live TV sources yet")
            serverPageGroup("Playback and guide", [
                serverPageRow("Guide length", guide[values.guideDays.toStr()])
                serverPageRow("Live rewind", rewind)
                serverPageRow("Logos and program pictures", serverPageOnOff(values.importImages))
            ])
        ]
    end if

    if page = "signin"
        signIn = data.signin
        values = data.signinPolicy.values
        method = "Portico Accounts"
        if signIn.kind = "local" then method = "Local accounts"
        stay = values.staySignedInDays.toStr() + " days"
        if values.staySignedInDays = 365 then stay = "1 year"
        remember = values.pinRememberDays.toStr() + " days"
        if values.pinRememberDays = 0 then remember = "Always ask"
        groups = [
            serverPageGroup("Method", [serverPageRow("Sign-in", method), serverPageRow("Owner", signIn.owner.username)])
            serverPageGroup("Staying signed in", [serverPageRow("Stay signed in for", stay), serverPageRow("Remember PIN for", remember)])
        ]
        if signIn.kind = "local"
            twoStep = {nobody: "Nobody", admins: "The owner and administrators", everyone: "Everyone"}
            groups.push(serverPageGroup("Two-step verification", [serverPageRow("Require two-step verification", twoStep[values.requireTwoStep])]))
        end if
        return groups
    end if

    if page = "integrations"
        keys = data.apiKeys.items.count().toStr()
        if data.apiKeys.nextCursor <> invalid then keys = keys + "+"
        return [
            serverPageGroup("API keys", [serverPageRow("API keys", keys)])
            serverPageGroup("Webhooks", [serverPageRow("Webhooks", data.webhooks.items.count().toStr())])
            serverPageGroup("Assistants", [serverPageRow("Assistants", serverPageOnOff(data.integrations.assistants))])
        ]
    end if

    if page = "streaming"
        values = data.streaming.values
        original = "Original (no limit)"
        qualities = {"0": original, "40000": "4K", "20000": "1080p High", "8000": "1080p Low",
            "4000": "720p High", "2000": "720p Low", "1500": "480p", "500": "240p"}
        video = qualities[values.awayVideoKbps.toStr()]
        if video = invalid then video = (values.awayVideoKbps / 1000).toStr() + " Mbps"
        music = values.awayMusicKbps.toStr() + " kbps"
        if values.awayMusicKbps = 0 then music = original
        if values.awayMusicKbps = 1411 then music = "CD quality"
        upload = "Not set"
        if values.uploadSpeedKbps > 0 then upload = (values.uploadSpeedKbps / 1000).toStr() + " Mbps"
        conversion = {"picture-and-sound": "Picture and sound", sound: "Sound only, never the picture", never: "Never"}
        return [
            serverPageGroup("Away from home", [
                serverPageRow("Quality away from home", video)
                serverPageRow("Music away from home", music)
                serverPageRow("Upload speed", upload)
            ])
            serverPageGroup("Conversion", [serverPageRow("Convert when needed", conversion[values.convertWhenNeeded])])
        ]
    end if

    if page = "storage"
        values = data.backupSettings.values
        schedule = "Never, only when I ask"
        if values.days <> invalid and values.days.count() > 0
            weekdays = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"]
            chosen = []
            for each day in values.days
                chosen.push(weekdays[day])
            end for
            schedule = chosen.join(", ")
            presets = {"Sun, Mon, Tue, Wed, Thu, Fri, Sat": "Every night", "Mon, Tue, Wed, Thu, Fri": "Weekdays",
                "Sun, Sat": "Weekends", Sun: "Once a week, on Sunday"}
            if presets[schedule] <> invalid then schedule = presets[schedule]
        end if
        return [serverPageGroup("Backups", [
            serverPageRow("Back up automatically", schedule)
            serverPageRow("Backups to keep", values.keep.toStr())
            serverPageRow("Your backups", data.backups.items.count().toStr())
            serverPageRow("Protect backups with a passphrase", serverPageOnOff(data.backups.protected))
        ])]
    end if

    if page = "troubleshooting"
        rows = []
        levels = {debug: "Debug", info: "Info", warn: "Warn", error: "Error"}
        kinds = {server: "Server", playback: "Playback", scan: "Scan", network: "Network"}
        ' The web shows the whole message. On a shared TV only show the time, level and category.
        for each line in data.logs.items
            rows.push(serverPageRow(kinds[line.kind], levels[line.level], serverPageTimeWords(line.time)))
            if rows.count() = 10 then exit for
        end for
        return [serverPageGroup("Messages", rows, "No messages match.")]
    end if

    return []
end function

function serverPageGroup(label as string, rows as object, empty = "" as string) as object
    return {label: label, rows: rows, empty: empty}
end function

' A field the server leaves out, or a value with no words here, is a blank, never a stopped app.
function serverPageRow(label as dynamic, value as dynamic, help = "" as dynamic) as object
    return {label: serverPageText(label), help: serverPageText(help), value: serverPageText(value), scope: ""}
end function

function serverPageText(value as dynamic) as string
    if value = invalid then return ""
    if type(value) = "roString" or type(value) = "String" then return value
    if type(value) = "roInt" or type(value) = "roInteger" or type(value) = "Integer" or type(value) = "LongInteger" or type(value) = "roLongInteger" then return value.toStr()
    return ""
end function

function serverPageOnOff(enabled as dynamic) as string
    if enabled = true then return "On"
    return "Off"
end function

function serverPageTimeWords(iso as dynamic) as string
    if iso = invalid then return ""
    when = createObject("roDateTime")
    when.fromISO8601String(iso)
    return serverDayWords(iso) + " · " + guideTime(when.asSeconds(), true)
end function
