sub init()
    c = colours()
    f = frames().settings
    m.f = f
    heading = m.top.findNode("heading")
    setType(heading, "title", c.text)
    heading.translation = [f.listX + 20, f.top]
    ' the server's card at the list's head (frame 35), where Settings' content starts, one header gap
    ' under the title: its tile, 18 in from the card's edges, "Server", its name, who sees the page
    m.cardY = f.top + heading.boundingRect().height + f.headerGap
    card = m.top.findNode("card")
    card.blendColor = c.raised
    card.width = f.listWidth
    card.height = 52 + 18 * 2
    card.translation = [f.listX, m.cardY]
    tile = m.top.findNode("tile")
    tile.blendColor = c.raisedBright
    tile.width = 52
    tile.height = 52
    tile.translation = [f.listX + 20, m.cardY + 18]
    m.top.findNode("tileIcon").translation = [f.listX + 32, m.cardY + 30]
    m.top.findNode("tileIcon").blendColor = c.textSecondary
    word = m.top.findNode("serverWord")
    setType(word, "listLabel", c.text)
    word.translation = [f.listX + 88, m.cardY + 14]
    m.name = m.top.findNode("serverName")
    setType(m.name, "help", c.textSecondary)
    m.name.text = storeRead("serverName")
    m.name.translation = [f.listX + 88, m.cardY + 48]
    m.who = m.top.findNode("who")
    setType(m.who, "tag", c.textSecondary)
    m.listTop = m.cardY + card.height + f.headerGap
    m.list = m.top.findNode("list")
    m.page = m.top.findNode("page")
    m.plate = m.top.findNode("listPlate")
    m.plate.width = f.listWidth
    m.plate.height = f.listRow
    m.top.findNode("listBar").color = c.accent
    m.ring = m.top.findNode("listRing")

    m.region = "list"
    m.at = 0
    m.row = 0
    m.stops = []
    m.data = {}
    m.pages = []
    m.top.observeField("focusedChild", "paint")
end sub

sub onRole()
    m.pages = serverPages(m.top.role)
    tag = "Owner only"
    if m.top.role <> "owner" then tag = "Administrators"
    m.who.text = tag
    m.who.translation = [m.f.listX + m.f.listWidth - 20 - m.who.boundingRect().width, m.cardY + 30]
    drawList()
    load()
end sub

' Everything the pages read, asked for at once; a page draws what has arrived.
sub load()
    if m.top.role = "owner" then api("GET", "/v1/admin/dashboard", invalid, "onRead", {part: "dashboard"})
    api("GET", "/v1/admin/sessions", invalid, "onRead", {part: "sessions"})
    if m.top.role = "owner" then api("GET", "/v1/admin/libraries", invalid, "onRead", {part: "libraries"})
    api("GET", "/v1/admin/members", invalid, "onRead", {part: "members"})
    api("GET", "/v1/admin/invitations", invalid, "onRead", {part: "invitations"})
    api("GET", "/v1/admin/devices", invalid, "onRead", {part: "devices"})
    if m.top.role = "owner" then api("GET", "/v1/admin/remote-access", invalid, "onRead", {part: "remote"})
end sub

sub onRead(event as object)
    result = event.getData()
    part = event.getRoSGNode().ctx.part
    if result.ok then m.data[part] = result.data else m.data[part] = {failed: true}
    showPage()
end sub

sub drawList()
    c = colours()
    m.list.removeChildrenIndex(m.list.getChildCount(), 0)
    m.listY = []
    y = m.listTop
    for each page in m.pages
        row = m.list.createChild("Group")
        row.translation = [m.f.listX, y]
        icon = row.createChild("Poster")
        icon.uri = "pkg:/images/i-" + page.icon + "-28.png"
        icon.width = 28
        icon.height = 28
        icon.translation = [20, 14]
        label = row.createChild("Label")
        label.text = page.title
        label.translation = [64, 0]
        label.width = m.f.listWidth - 84
        label.height = m.f.listRow
        label.vertAlign = "center"
        setType(label, "listLabel", c.textSecondary)
        m.listY.push(y)
        y = y + m.f.listRow
    end for
    showPage()
end sub

' The page in focus: the band saying who sees it and that it is view only, its title, its groups.
sub showPage()
    if m.pages.count() = 0 then return
    c = colours()
    f = m.f
    m.page.removeChildrenIndex(m.page.getChildCount(), 0)
    m.stops = []
    page = m.pages[m.at]
    band = m.page.createChild("Label")
    band.text = "Server   " + serverWho(m.top.role, page.id) + "   View only"
    band.translation = [f.pageX, f.top]
    setType(band, "help", c.textSecondary)
    title = m.page.createChild("Label")
    title.text = page.title
    title.translation = [f.pageX, f.top + 44]
    setType(title, "title", c.text)
    y = f.top + 44 + title.boundingRect().height + f.headerGap
    if page.id = "dashboard" then y = drawDashboard(y)
    if page.id = "libraries" then y = drawLibraries(y)
    if page.id = "people" then y = drawPeople(y)
    if page.id = "remote" then y = drawRemote(y)
    if m.row >= m.stops.count() then m.row = m.stops.count() - 1
    if m.row < 0 then m.row = 0
    paint()
end sub

function drawDashboard(y as integer) as integer
    sessions = []
    if m.data.sessions <> invalid and m.data.sessions.items <> invalid then sessions = m.data.sessions.items
    rows = []
    for each s in sessions
        words = serverSessionWords(s)
        rows.push({label: words.label, help: words.help, scope: words.scope})
    end for
    y = drawGroup(y, "Now playing   " + serverNowPlayingWords(sessions), rows, "Nothing is playing now.")
    if m.top.role <> "owner" or m.data.dashboard = invalid or m.data.dashboard.failed = true then return y
    d = m.data.dashboard
    rows = []
    for each alert in d.alerts
        words = serverAlertWords(alert)
        rows.push({label: words.title, help: words.detail, icon: "warning"})
    end for
    y = drawGroup(y, "Alerts", rows, "No alerts. The server is running normally.")
    rows = []
    for each scan in d.scans
        rows.push(serverTaskWords(scan, true))
    end for
    for each task in d.tasks.items
        rows.push(serverTaskWords(task, false))
    end for
    return drawGroup(y, "Background tasks", rows, "Nothing is running.")
end function

function drawLibraries(y as integer) as integer
    rows = []
    if m.data.libraries <> invalid and m.data.libraries.items <> invalid
        for each library in m.data.libraries.items
            help = ""
            if library.folders <> invalid and library.folders.count() > 0 and library.folders[0].path <> invalid then help = library.folders[0].path
            rows.push({label: library.name, help: help, value: serverScanWords(library)})
        end for
    end if
    return drawGroup(y, "Libraries in the order every device shows them", rows, "No libraries yet.")
end function

function drawPeople(y as integer) as integer
    rows = []
    if m.data.members <> invalid and m.data.members.items <> invalid
        for each member in m.data.members.items
            help = serverRoleWords(member.role)
            if member.porticoAccount = true then help = help + "   Portico Account" else help = help + "   Local account"
            if member.profiles <> invalid then help = help + "   " + countText(member.profiles.count(), "profile", "profiles")
            rows.push({label: member.username, help: help, value: lastSeenServer(member.lastActiveAt)})
        end for
    end if
    y = drawGroup(y, "Accounts", rows, "No accounts.")
    rows = []
    if m.data.invitations <> invalid and m.data.invitations.items <> invalid
        for each invitation in m.data.invitations.items
            if invitation.state = "active" then rows.push({label: "Invitation for a " + lcase(serverRoleWords(invitation.role)), help: "Made by " + wordsOf(invitation.createdBy)})
        end for
    end if
    y = drawGroup(y, "Invitations", rows, "No invitations waiting.")
    rows = []
    if m.data.devices <> invalid and m.data.devices.items <> invalid
        for each device in m.data.devices.items
            rows.push({label: wordsOf(device.name), help: wordsOf(device.account.username), value: countText(device.sessions, "session", "sessions")})
        end for
    end if
    return drawGroup(y, "Devices", rows, "No devices.")
end function

' Remote access: its rows in their groups, values in words (nothing here changes it).
function drawRemote(y as integer) as integer
    if m.data.remote = invalid then return y
    if m.data.remote.failed = true then return drawGroup(y, "Remote access", [], "Couldn't read remote access just now.")
    groups = []
    rowsOf = {}
    for each row in serverRemoteRows(m.data.remote)
        if rowsOf[row.group] = invalid
            groups.push(row.group)
            rowsOf[row.group] = []
        end if
        rowsOf[row.group].push(row)
    end for
    for each name in groups
        y = drawGroup(y, name, rowsOf[name], "")
    end for
    return drawGroup(y, "Changing it", [{label: "Change remote access on your phone or computer", help: "TV shows the server's settings and never changes them."}], "")
end function

function wordsOf(value as dynamic) as string
    if value = invalid then return ""
    return value
end function

function lastSeenServer(iso as dynamic) as string
    if iso = invalid then return ""
    when = createObject("roDateTime")
    when.fromISO8601String(iso)
    when.toLocalTime()
    months = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"]
    return "Active " + months[when.getMonth() - 1] + " " + when.getDayOfMonth().toStr()
end function

' A group's label, then its rows on one raised card (or one line saying there is nothing).
function drawGroup(y as integer, label as string, rows as object, empty as string) as integer
    c = colours()
    f = m.f
    heading = m.page.createChild("Label")
    heading.text = label
    heading.translation = [f.pageX + 24, y]
    setType(heading, "groupLabel", c.textSecondary)
    y = y + heading.boundingRect().height + 22
    if rows.count() = 0 then rows = [{label: empty}]
    card = m.page.createChild("Poster")
    card.uri = "pkg:/images/round-14.9.png"
    card.blendColor = c.raised
    card.translation = [f.pageX, y]
    card.width = f.pageWidth
    top = y
    first = true
    for each row in rows
        item = m.page.createChild("ListRow")
        item.rowWidth = f.pageWidth
        item.model = {label: row.label, help: wordsOf(row.help), scope: wordsOf(row.scope), value: wordsOf(row.value), control: "", switchOn: false, icon: wordsOf(row.icon)}
        item.translation = [f.pageX, y]
        m.stops.push({node: item, first: first})
        first = false
        y = y + item.rowHeight
    end for
    card.height = y - top
    return y + f.sectionGap
end function

sub onActive()
    if not m.top.active then return
    m.top.setFocus(true)
    paint()
end sub

sub onToTop()
    m.region = "list"
    m.top.active = true
end sub

sub paint()
    if m.pages.count() = 0 then return
    c = colours()
    has = m.top.hasFocus()
    inList = (m.region = "list")
    for i = 0 to m.list.getChildCount() - 1
        row = m.list.getChild(i)
        tint = c.textSecondary
        if i = m.at then tint = c.text
        row.getChild(1).color = tint
        row.getChild(0).blendColor = tint
        if i = m.at and not inList then row.getChild(0).blendColor = c.accent
    end for
    y = m.listY[m.at]
    x = m.f.listX
    m.plate.translation = [x, y]
    m.top.findNode("listBar").translation = [x, y + 14]
    out = metrics().focusSpread + 6
    m.ring.translation = [x - out, y - out]
    m.ring.width = m.f.listWidth + out * 2
    m.ring.height = m.f.listRow + out * 2
    m.ring.visible = (has and inList)
    if has and inList then m.plate.blendColor = c.raisedBright else m.plate.blendColor = c.raisedHigh
    for i = 0 to m.stops.count() - 1
        here = m.stops[i]
        here.node.on = (has and not inList and i = m.row)
        here.node.separator = not here.first and not (has and not inList and i - 1 = m.row)
    end for
    ' the page moves only to keep the row in focus clear of the screen's foot
    scroll = 0
    if not inList and m.stops.count() > 0
        node = m.stops[m.row].node
        bottom = node.translation[1] + node.rowHeight
        if bottom > m.f.foot then scroll = bottom - m.f.foot
    end if
    m.page.translation = [0, -scroll]
end sub

function pressKey(key as string) as boolean
    by = 1
    if key = "up" then by = -1
    if m.region = "list"
        want = m.at + by
        if want < 0 or want >= m.pages.count() then return false
        m.at = want
        m.row = 0
        showPage()
        return true
    end if
    want = m.row + by
    if want < 0 or want >= m.stops.count() then return false
    m.row = want
    paint()
    return true
end function

function onKeyEvent(key as string, press as boolean) as boolean
    holdKey(key, press and (key = "up" or key = "down"))
    if not press then return false
    if key = "up" or key = "down" then return pressKey(key)     ' Up at the top goes on to the scene
    if m.region = "list"
        if (key = "right" or key = "OK") and m.stops.count() > 0
            m.region = "rows"
            m.row = 0
            paint()
            return true
        end if
        return false        ' Left opens the rail; Back at the top goes Home
    end if
    if key = "left" or key = "back"
        m.region = "list"
        paint()
        return true
    end if
    return true     ' the rows are read, not changed
end function
