' Server settings on TV (tv-more frames 35 to 39): the pages a TV shows and the words of their
' rows, from the owner's routes. View only. The web has fourteen pages; the TV has the ones its
' frames draw and the server's routes read in words: Dashboard, Libraries, People and Remote access
' (in the words of the web's owner page, web/src/screens/settings/server-remote/OwnerRemotePage.tsx).

' The pages for a role: an administrator sees the Dashboard's Now playing and People (frame 39).
function serverPages(role as string) as object
    pages = [{id: "dashboard", title: "Dashboard", icon: "home"}]
    if role = "owner" then pages.push({id: "libraries", title: "Libraries", icon: "collection"})
    pages.push({id: "people", title: "People", icon: "profile"})
    if role = "owner" then pages.push({id: "remote", title: "Remote access", icon: "server"})
    return pages
end function

' Who sees the page, for the band over it.
function serverWho(role as string, page as string) as string
    if role = "owner" and (page = "libraries" or page = "remote") then return "Only you see this page"
    return "Only you and administrators see this page"
end function

' An alert as the web says it (packages/client-core dashboard-alert-words.ts): {title, detail}.
function serverAlertWords(alert as object) as object
    words = {update_available: "Portico {version} is available", upload_budget_exhausted: "Upload speed is holding back {count}",
        state_permissions: "Other accounts can read the data folder", data_folder_readable: "Other accounts can read the data folder",
        storage_nearly_full: "Storage is nearly full", backup_failed: "A backup failed",
        library_unreachable: "A media folder or remote source can’t be reached", remote_unreachable: "A media folder or remote source can’t be reached",
        secure_renewal_failed: "Secure connection couldn’t renew"}
    params = alert.params
    if params = invalid then params = {}
    title = words[alert.code]
    detail = ""
    if title = invalid
        title = "The server needs attention"
        if params.message <> invalid and params.message <> "" then title = params.message
        return {title: title, detail: ""}
    end if
    latest = ""
    if params.latest <> invalid then latest = params.latest
    count = 0
    if alert.count <> invalid then count = alert.count
    streams = count.toStr() + " streams"
    if count = 1 then streams = "1 stream"
    title = title.replace("{version}", latest).replace("{count}", streams)
    if params.message <> invalid and params.message <> ""
        detail = params.message
    else if params.reason <> invalid and alert.code <> "data_folder_readable"
        detail = params.reason
    end if
    return {title: title, detail: detail}
end function

' Now playing in one line: "2 people, 1 at home, 1 away".
function serverNowPlayingWords(sessions as object) as string
    if sessions.count() = 0 then return "Nothing is playing"
    people = {}
    away = 0
    for each s in sessions
        people[s.username] = true
        if s.away = true then away = away + 1
    end for
    count = people.count()
    who = count.toStr() + " people"
    if count = 1 then who = "1 person"
    return who + ", " + (sessions.count() - away).toStr() + " at home, " + away.toStr() + " away"
end function

' A stream's rows: {label, help, scope}: what, then who, where and how it reaches them.
function serverSessionWords(s as object) as object
    label = "A title you can't see"
    if s.title <> invalid and s.title <> "" then label = s.title
    help = ""
    if s.showTitle <> invalid and s.showTitle <> ""
        label = s.showTitle
        code = ""
        if s.seasonNumber <> invalid and s.episodeNumber <> invalid then code = "S" + s.seasonNumber.toStr() + " E" + s.episodeNumber.toStr()
        help = (code + "   " + s.title).trim()
    end if
    who = s.username
    if s.profile <> invalid and s.profile <> "" then who = who + " (" + s.profile + ")"
    where = "Home network"
    if s.away = true then where = "Away from home"
    how = "Original"
    if s.convertingToHeight <> invalid and s.convertingToHeight > 0 then how = "Converted to " + s.convertingToHeight.toStr() + "p"
    if s.route = "repackaged" then how = "Repackaged"
    return {label: label, help: help, scope: who + "   " + s.deviceName + "   " + where + "   " + how}
end function

' A library's scan or another task: {label, help, value} ("Movies", "Library scan   51 of 80 files", "64%").
function serverTaskWords(task as object, isScan as boolean) as object
    label = "A background task"
    if task.libraryName <> invalid and task.libraryName <> "" then label = task.libraryName
    what = "Library scan"
    unit = "files"
    done = 0
    total = -1
    if isScan
        if task.analyzed <> invalid then done = task.analyzed
        if task.total <> invalid then total = task.total
        if task.phase = "waiting" then what = "Library scan, waiting"
        if task.phase = "walking" then what = "Library scan, finding the files"
    else
        kinds = {scan: "Library scan", musicanalysis: "Sound analysis", backup: "Backup", upgrade: "Finishing an update"}
        what = kinds[task.kind]
        if what = invalid then what = "Background task"
        if task.unit <> invalid and task.unit <> "" then unit = task.unit
        if task.processed <> invalid then done = task.processed
        if task.total <> invalid then total = task.total
    end if
    help = what
    value = ""
    if total > 0
        help = help + "   " + done.toStr() + " of " + total.toStr() + " " + unit
        value = int(done * 100 / total + 0.5).toStr() + "%"
    end if
    return {label: label, help: help, value: value}
end function

' A library's scan in words, never a count of files to fix (frame 36): "Scanning", "Scanned Nov 18".
function serverScanWords(library as object) as string
    if library.scanning = true then return "Scanning"
    if library.lastScanFinishedAt = invalid then return "Not scanned yet"
    when = createObject("roDateTime")
    when.fromISO8601String(library.lastScanFinishedAt)
    when.toLocalTime()
    months = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"]
    return "Scanned " + months[when.getMonth() - 1] + " " + when.getDayOfMonth().toStr()
end function

function serverRoleWords(role as string) as string
    words = {owner: "Owner", admin: "Administrator", member: "Member"}
    if words[role] <> invalid then return words[role]
    return role
end function

' Remote access as the web's owner page says it (GET /v1/admin/remote-access, tv-more frame 38): the
' outside check and each address it tried, the secure connection, and the router's mapping, as
' [{group, label, help, value}]. `when` turns a time into words.
function serverRemoteRows(status as object) as object
    rows = []
    outside = status.outside
    answers = []
    if outside <> invalid and outside.addresses <> invalid then answers = outside.addresses
    reached = answers.count() > 0
    for each answer in answers
        if answer.reachable <> true then reached = false
    end for
    words = "Not checked from outside"
    if outside <> invalid and outside.checkedAt <> invalid
        if reached then words = "Reachable from outside" else if answers.count() > 0 then words = "Not reachable from outside" else words = "Outside check did not finish"
    end if
    help = ""
    if outside <> invalid and outside.checkedAt <> invalid then help = "Checked " + serverDayWords(outside.checkedAt)
    if outside <> invalid and outside.problem <> invalid and outside.problem <> "" then help = (help + ". " + outside.problem).trim()
    if left(help, 2) = ". " then help = mid(help, 3)
    rows.push({group: "Connection", label: "Remote access", help: help, value: words})
    for each answer in answers
        said = "Not reachable from outside"
        if answer.reachable = true then said = "Reachable from outside"
        rows.push({group: "Connection", label: answer.address + ":" + answer.port.toStr(), help: "", value: said})
    end for
    certificate = status.managedCertificate
    if certificate <> invalid
        value = "Not serving yet"
        if certificate.ready = true then value = "Serving"
        help = ""
        if certificate.name <> invalid and certificate.name <> "" then help = certificate.name
        if certificate.expiresAt <> invalid then help = (help + "   expires " + serverDayWords(certificate.expiresAt)).trim()
        if certificate.problem <> invalid and certificate.problem <> "" then help = (help + "   " + certificate.problem).trim()
        rows.push({group: "Connection", label: "Secure connection", help: help, value: value})
    end if
    for each router in [status.router, status.routerIPv6]
        if router <> invalid and router.state <> invalid and router.state <> "off"
            value = "Port not opened"
            if router.state = "open" then value = "Port opened"
            help = []
            if router.protocol <> invalid and router.protocol <> "" then help.push(router.protocol)
            if router.externalAddress <> invalid and router.externalAddress <> "" then help.push(router.externalAddress + ":" + router.externalPort.toStr())
            if router.problem <> invalid and router.problem <> "" then help.push(router.problem)
            rows.push({group: "Router", label: "Router mapping", help: help.join("   "), value: value})
        end if
    end for
    if status.warnings <> invalid
        for each warning in status.warnings
            if warning.message <> invalid then rows.push({group: "Needs attention", label: warning.message, help: "", value: "", icon: "warning"})
        end for
    end if
    return rows
end function

' "Nov 18" for a time, in the TV's own zone.
function serverDayWords(iso as string) as string
    when = createObject("roDateTime")
    when.fromISO8601String(iso)
    when.toLocalTime()
    months = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"]
    return months[when.getMonth() - 1] + " " + when.getDayOfMonth().toStr()
end function
