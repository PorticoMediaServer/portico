' Focus on Watch Together. The lobby: Start a group and Join with a code, then the groups (Down).
' The room has two columns. The left runs down from the notice's button (when there is one)
' through the room's buttons and the queue to Leave and End at the foot; the right holds the
' people and New code. Right from the left column reaches the people, Left comes back, and Left
' from the left column opens the rail. The room is read again every few seconds and that never
' moves focus: focus is kept by what it is on (a row of buttons, a queue entry, a person).
' A sheet holds focus until Back closes it.
sub init()
    c = colours()
    f = frames()
    t = f.together
    m.t = t
    m.group = m.global.together
    m.group.observeFieldScoped("room", "onRoom")
    m.group.observeFieldScoped("phase", "onRoom")
    m.group.observeFieldScoped("answer", "onAnswer")
    m.tick = m.top.findNode("tick")
    m.tick.observeField("fire", "onTick")
    m.items = {}         ' the queue's titles by item id, read as they are needed
    m.view = ""          ' "lobby", "room", "ended" or "state"
    m.focusKey = ""      ' what has focus in the lobby or the room
    m.sheetKind = ""     ' "join" or "start" while a sheet is open
    m.sheetAt = ""       ' what has focus in the sheet
    m.asked = ""         ' the ask this page waits on
    m.profileName = ""
    m.groupIds = []      ' the lobby's groups, in order
    m.queueKeys = []     ' the room's focus stops: each queue entry and each person
    m.peopleKeys = []
    m.hasNotice = false  ' the notice strip has a button (Become host, Go ahead without them)
    m.problem = ""       ' a change the group refused, said in the notice strip
    m.queueFrom = 0      ' the first queue row shown
    m.peopleFrom = 0     ' the first person shown

    ' the lobby (frame 11)
    title = m.top.findNode("lobbyTitle")
    setType(title, "title", c.text)
    title.translation = [f.gutter, f.browse.titleY]
    card = m.top.findNode("card")
    card.blendColor = c.raised
    card.translation = [f.gutter, t.cardY]
    card.width = t.cardWidth
    card.height = t.cardHeight
    inner = f.gutter + t.cardPad
    disc = m.top.findNode("cardDisc")
    disc.blendColor = c.raisedBright
    disc.translation = [inner, t.cardY + 48]
    m.top.findNode("cardIcon").translation = [inner + 20, t.cardY + 68]
    m.top.findNode("cardIcon").blendColor = c.textSecondary
    lede = m.top.findNode("lede")
    setType(lede, "pageTitle", c.text)
    lede.lineSpacing = -14      ' two lines set close, as the frame draws them
    lede.width = 920
    lede.translation = [inner, t.ledeY]
    words = m.top.findNode("words")
    setType(words, "authBody", c.textSecondary)
    words.width = 820
    words.height = 100          ' room for two lines: left to itself the label drew one, cut short
    words.translation = [inner, t.wordsY]
    m.lobbyButtons = m.top.findNode("lobbyButtons")
    m.lobbyButtons.translation = [inner, t.cardButtonsY]
    m.lobbyButtons.buttons = [{id: "start", text: "Start a group", icon: "people"}, {id: "join", text: "Join with a code", icon: "keyboard"}]
    m.lobbyButtons.observeField("pressed", "onLobbyButton")
    head = m.top.findNode("groupsHead")
    setType(head, "groupLabel", c.textTertiary)
    head.translation = [f.gutter, t.groupsHeadY]
    m.groups = m.top.findNode("groups")
    m.groups.translation = [f.gutter, t.groupsY]

    ' the room (frames 14 and 15)
    setType(m.top.findNode("eyebrow"), "eyebrow", c.textSecondary)
    m.top.findNode("eyebrow").translation = [f.gutter, t.eyebrowY]
    setType(m.top.findNode("roomName"), "pageTitle", c.text)
    m.top.findNode("roomName").translation = [f.gutter, t.titleY]
    m.top.findNode("roomName").width = f.rightEdge - f.gutter
    for each id in ["nowPlate", "nowArt", "nowShade"]
        part = m.top.findNode(id)
        part.translation = [f.gutter, t.nowY]
        part.width = t.nowWidth
        part.height = t.nowHeight
    end for
    m.top.findNode("nowPlate").blendColor = c.raised
    m.top.findNode("nowArt").blendColor = c.stillDim
    m.top.findNode("nowArt").loadWidth = t.nowWidth
    m.top.findNode("nowArt").loadHeight = t.nowHeight
    x = f.gutter + 32
    words = t.nowWidth - 64
    m.top.findNode("nowDot").translation = [x, t.nowY + 96]
    setType(m.top.findNode("nowState"), "metaStrong", c.healthy)
    m.top.findNode("nowState").translation = [x + 20, t.nowY + 84]
    setType(m.top.findNode("nowTitle"), "title", c.text)
    m.top.findNode("nowTitle").translation = [x, t.nowY + 118]
    m.top.findNode("nowTitle").width = words
    setType(m.top.findNode("nowMeta"), "meta", c.textSecondary)
    m.top.findNode("nowMeta").translation = [x, t.nowY + 178]
    m.top.findNode("nowMeta").width = words
    setType(m.top.findNode("nowReady"), "caption", c.textTertiary)
    m.top.findNode("nowReady").translation = [x, t.nowY + 216]
    m.top.findNode("nowReady").width = words
    m.top.findNode("nowTrack").translation = [x, t.nowY + 258]
    m.top.findNode("nowTrack").width = words
    m.top.findNode("nowTrack").color = c.trackOnArt
    m.top.findNode("nowPlayed").translation = [x, t.nowY + 258]
    m.top.findNode("nowPlayed").color = c.accentStrong
    m.actions = m.top.findNode("actions")
    m.actions.translation = [f.gutter, t.actionsY]
    m.actions.passRight = true
    m.actions.observeField("pressed", "onRoomButton")
    hostControls = m.top.findNode("hostControls")
    setType(hostControls, "caption", c.textTertiary)
    hostControls.height = 72
    hostControls.vertAlign = "center"
    setType(m.top.findNode("queueHead"), "groupLabel", c.textTertiary)
    m.top.findNode("queueHead").translation = [f.gutter, t.queueHeadY]
    m.top.findNode("queuePlate").blendColor = c.raised
    m.top.findNode("queuePlate").translation = [f.gutter, t.queueY]
    m.top.findNode("queuePlate").width = t.nowWidth
    m.queue = m.top.findNode("queue")
    m.queue.translation = [f.gutter, t.queueY]
    setType(m.top.findNode("queueEmpty"), "caption", c.textTertiary)
    m.top.findNode("queueEmpty").translation = [f.gutter, t.queueY]
    m.top.findNode("queueEmpty").width = t.nowWidth
    setType(m.top.findNode("peopleHead"), "groupLabel", c.textTertiary)
    m.top.findNode("peopleHead").translation = [t.peopleX, t.peopleHeadY]
    m.top.findNode("peoplePlate").blendColor = c.raised
    m.top.findNode("peoplePlate").translation = [t.peopleX, t.peopleY]
    m.top.findNode("peoplePlate").width = t.peopleWidth
    m.people = m.top.findNode("people")
    m.people.translation = [t.peopleX, t.peopleY]
    setType(m.top.findNode("inviteHead"), "groupLabel", c.textTertiary)
    m.top.findNode("inviteHead").translation = [t.peopleX, t.inviteHeadY]
    plate = m.top.findNode("invitePlate")
    plate.blendColor = c.raised
    plate.translation = [t.peopleX, t.inviteY]
    plate.width = t.peopleWidth
    plate.height = 296
    setType(m.top.findNode("code"), "code", c.text)
    m.top.findNode("code").translation = [t.peopleX + 32, t.inviteY + 24]
    setType(m.top.findNode("inviteWords"), "caption", c.textTertiary)
    m.top.findNode("inviteWords").translation = [t.peopleX + 32, t.inviteY + 130]
    m.top.findNode("inviteWords").width = t.peopleWidth - 64
    m.inviteButton = m.top.findNode("inviteButton")
    m.inviteButton.translation = [t.peopleX + 32, t.inviteY + 200]
    m.inviteButton.observeField("pressed", "onRoomButton")
    notice = m.top.findNode("notice")
    notice.translation = [f.gutter, t.noticeY]
    across = f.rightEdge - f.gutter
    m.top.findNode("noticePlate").blendColor = c.noticePlate
    m.top.findNode("noticePlate").width = across
    m.top.findNode("noticeEdge").blendColor = c.noticeEdge
    m.top.findNode("noticeEdge").width = across
    m.top.findNode("noticeIcon").blendColor = c.warning
    m.top.findNode("noticeIcon").translation = [26, 34]
    setType(m.top.findNode("noticeText"), "body", c.text)
    m.top.findNode("noticeText").translation = [80, 0]
    m.noticeButton = m.top.findNode("noticeButton")
    m.noticeButton.observeField("pressed", "onRoomButton")
    m.foot = m.top.findNode("foot")
    m.foot.translation = [f.gutter, t.footY]
    m.foot.passRight = true
    m.foot.observeField("pressed", "onRoomButton")
    m.state = m.top.findNode("state")
    m.state.observeField("pressed", "onStateButton")
    m.confirm = m.top.findNode("confirm")
    m.confirm.observeField("chosen", "onConfirmed")

    ' the sheets (frames 12 and 13)
    m.top.findNode("scrim").color = c.ink
    m.top.findNode("panel").color = c.overlay
    setType(m.top.findNode("sheetEyebrow"), "eyebrow", c.textSecondary)
    setType(m.top.findNode("sheetTitle"), "title", c.text)
    setType(m.top.findNode("sheetWords"), "body", c.textSecondary)
    setType(m.top.findNode("whoLabel"), "groupLabel", c.textTertiary)
    setType(m.top.findNode("sheetNote"), "help", c.danger)
    m.keyboard = m.top.findNode("keyboard")
    m.keyboard.translation = [t.keysX, t.keysY]
    m.keyboard.observeField("text", "onTyped")
    m.control = m.top.findNode("control")
    m.control.observeField("pressed", "onControl")
    m.submit = m.top.findNode("submit")
    m.submit.passRight = true
    m.submit.observeField("pressed", "onSubmit")
    m.top.observeField("focusedChild", "paintSheet")
    api("GET", "/v1/me", invalid, "onMe")
    showWhereThisIs()
end sub

sub onActive()
    if not m.top.active then return
    m.tick.control = "start"
    focusNow(true)
end sub

sub onMe(event as object)
    result = event.getData()
    if result.ok and result.data.profile <> invalid then m.profileName = result.data.profile.name
end sub

' The group changed (a new reading, joined, left or ended): the page shows where this profile is now.
sub onRoom()
    if not gone() then showWhereThisIs()
end sub

sub showWhereThisIs()
    phase = m.group.phase
    if phase = "live" or phase = "reconnecting"
        if m.sheetKind <> "" then closeSheet()
        showRoom()
    else if phase = "ended"
        showEnded()
    else if m.view <> "lobby"
        showLobby()
    end if
end sub

' A page the scene has put away stops listening to the group (the group outlives it).
function gone() as boolean
    if m.top.getParent() <> invalid then return false
    m.group.unobserveFieldScoped("room")
    m.group.unobserveFieldScoped("phase")
    m.group.unobserveFieldScoped("answer")
    m.tick.control = "stop"
    return true
end function

sub showOnly(view as string)
    m.view = view
    m.top.findNode("lobby").visible = (view = "lobby")
    m.top.findNode("room").visible = (view = "room")
    m.state.visible = (view = "ended" or view = "state")
end sub

' --- the lobby ---------------------------------------------------------------------------------

sub showLobby()
    first = (m.view <> "lobby")
    showOnly("lobby")
    if first
        m.focusKey = "buttons"
        api("GET", "/v1/groups", invalid, "onGroups")
    end if
    focusNow()
end sub

sub onGroups(event as object)
    result = event.getData()
    if m.view <> "lobby" and m.view <> "state" then return
    if not result.ok
        ' Watch Together turned off for this profile, or the server out of reach: the whole page says so
        m.state.icon = "people"
        m.state.title = "Watch Together couldn’t load"
        m.state.body = result.error.message
        m.state.buttons = [{id: "retry", text: "Try again now", icon: "refresh"}]
        showOnly("state")
        focusNow()
        return
    end if
    if m.view = "state" then showOnly("lobby")
    m.groups.removeChildrenIndex(m.groups.getChildCount(), 0)
    m.groupIds = []
    y = 0
    for each group in result.data.items
        row = m.groups.createChild("ListRow")
        row.rowWidth = m.t.cardWidth
        value = ""
        if group.host then value = "You host"
        row.model = {label: group.name, help: group.members.toStr() + " here", scope: "", value: value, control: "", icon: "people", danger: false}
        row.translation = [0, y]
        y = y + row.rowHeight + 12
        m.groupIds.push(group.id)
    end for
    m.top.findNode("groupsHead").visible = (m.groupIds.count() > 0)
    focusNow()
end sub

sub onLobbyButton(event as object)
    openSheet(event.getData())
end sub

sub onStateButton(event as object)
    if m.view = "ended"
        ' Back to Watch Together: the ended room is put away
        m.group.ask = {do: "forget"}
        showLobby()
    else
        api("GET", "/v1/groups", invalid, "onGroups")
    end if
end sub

' --- the room ----------------------------------------------------------------------------------

sub showRoom()
    if m.view <> "room"
        m.focusKey = ""
        m.problem = ""
        m.queueFrom = 0
        m.peopleFrom = 0
    end if
    showOnly("room")
    drawRoom()
end sub

sub drawRoom()
    c = colours()
    f = frames()
    t = m.t
    room = m.group.room
    if room = invalid or room.clock = invalid then return
    m.top.findNode("roomName").text = room.name
    ' the strip over both columns: an amber notice moves them down (frame 15)
    noticeText = ""
    noticeButton = invalid
    if m.group.phase = "reconnecting"
        noticeText = "Reconnecting to the group. You’ll be back in step as soon as it’s back."
    else if room.host.present <> true and room.you.host <> true
        noticeText = hostAwayLine(room)
        if room.you.mayBecomeHost = true
            noticeButton = {id: "becomeHost", text: "Become host", icon: ""}
            ' once the server's wait has passed, Become host appears and takes focus (frame 15)
            if m.offeredHost <> true then m.focusKey = "notice"
            m.offeredHost = true
        end if
    else if room.cantSee <> invalid and room.cantSee > 0
        noticeText = "Someone in the group can’t see this title on their profile."
        noticeButton = {id: "goAhead", text: "Go ahead without them", icon: ""}
    else if m.problem <> ""
        noticeText = m.problem
    end if
    notice = m.top.findNode("notice")
    notice.visible = (noticeText <> "")
    shift = 0
    if notice.visible then shift = t.noticeShift
    m.top.findNode("left").translation = [0, shift]
    m.top.findNode("right").translation = [0, shift]
    across = f.rightEdge - f.gutter
    if noticeButton <> invalid then m.noticeButton.buttons = [noticeButton] else m.noticeButton.buttons = []
    buttonWidth = 0
    if noticeButton <> invalid then buttonWidth = m.noticeButton.getChild(0).buttonWidth + 32
    m.noticeButton.translation = [across - buttonWidth + 14, 14]
    text = m.top.findNode("noticeText")
    text.text = noticeText
    text.width = across - 80 - buttonWidth - 24
    m.hasNotice = (noticeButton <> invalid)

    drawNow(room)

    ' the room's buttons: its transport for whoever may control, and Open player
    buttons = []
    transport = transportFor(room)
    if transport <> invalid then buttons.push({id: transport.action, text: transport.text, icon: transport.icon})
    entry = roomEntry(room)
    if entry <> invalid and entry.available = true then buttons.push({id: "player", text: "Open player", icon: "tvDevice"})
    m.actions.buttons = buttons
    hostControls = m.top.findNode("hostControls")
    hostControls.visible = (room.you.mayControl <> true)
    width = 0
    for each node in m.actions.getChildren(-1, 0)
        width = width + node.buttonWidth + 18
    end for
    hostControls.translation = [f.gutter + width + 14, t.actionsY]

    drawQueue(room)
    drawPeople(room)

    ' the invitation, the host's only (frame 14): no share sheet on a television, so no Share code
    invite = m.top.findNode("invite")
    invite.visible = (room.you.host = true)
    if room.invitation <> invalid
        m.top.findNode("code").text = codeShown(room.invitation.code)
        m.top.findNode("inviteWords").text = "They open Watch Together and join with this code. It works " + room.invitation.joinsLeft.toStr() + " times until " + localTime(room.invitation.expiresAt) + ", for people with a profile on this server."
        m.inviteButton.buttons = [{id: "newCode", text: "New code", icon: ""}]
    else
        m.top.findNode("code").text = ""
        m.top.findNode("inviteWords").text = ""
        m.inviteButton.buttons = [{id: "newCode", text: "Get a code", icon: ""}]
    end if

    foot = [{id: "leave", text: "Leave group", icon: ""}]
    if room.you.host = true then foot.push({id: "end", text: "End for everyone", icon: ""})
    m.foot.buttons = foot
    focusNow()
end sub

' What is on, over its picture: its state, title, place and the readiness line (frame 14).
sub drawNow(room as object)
    c = colours()
    entry = roomEntry(room)
    state = roomStateText(room, m.group.phase = "reconnecting")
    tint = c.healthy
    if state.wait then tint = c.warning
    m.top.findNode("nowState").text = state.text
    m.top.findNode("nowState").color = tint
    m.top.findNode("nowDot").blendColor = tint
    art = m.top.findNode("nowArt")
    title = m.top.findNode("nowTitle")
    meta = m.top.findNode("nowMeta")
    if entry = invalid
        title.text = "Nothing chosen yet"
        if room.you.mayControl = true then meta.text = "Press * on any title and choose Watch with group." else meta.text = "The host picks what to watch."
        art.uri = ""
        m.top.findNode("nowReady").text = ""
        m.top.findNode("nowState").text = ""
        m.top.findNode("nowDot").visible = false
        m.top.findNode("nowTrack").visible = false
        m.top.findNode("nowPlayed").visible = false
        return
    end if
    m.top.findNode("nowDot").visible = true
    m.top.findNode("nowTrack").visible = true
    m.top.findNode("nowPlayed").visible = true
    if entry.available <> true
        title.text = "Something you can’t see on this profile"
        art.uri = ""
    else
        item = itemOf(entry)
        title.text = entry.title
        if item.kind = "episode" and item.rootTitle <> invalid then title.text = item.rootTitle
        art.uri = imageUrl(pick(item.images, ["backdrop", "still", "thumb"]), m.t.nowWidth, m.t.nowHeight)
    end if
    m.top.findNode("nowReady").text = readinessText(room.clock)
    onTick()
end sub

' Once a second: the group's place moves on, and the host-away countdown with it.
sub onTick()
    if gone() or m.view <> "room" then return
    room = m.group.room
    if room = invalid or room.clock = invalid then return
    entry = roomEntry(room)
    if entry <> invalid and entry.available = true
        item = itemOf(entry)
        place = groupPlaceMs(room.clock, (UpTime(0) - room.receivedAt) * 1000)
        words = []
        if item.kind = "episode"
            code = episodeCode(item)
            if code <> "" then words.push(code)
            if item.title <> invalid then words.push(item.title)
        else if item.year <> invalid
            words.push(item.year.toStr())
        end if
        words.push(placeText(place, item.durationMs))
        m.top.findNode("nowMeta").text = words.join("   ")
        fraction = 0
        if item.durationMs <> invalid and item.durationMs > 0 then fraction = place / item.durationMs
        if fraction > 1 then fraction = 1
        m.top.findNode("nowPlayed").width = m.top.findNode("nowTrack").width * fraction
    end if
    if room.host.present <> true and room.you.host <> true and m.group.phase = "live"
        m.top.findNode("noticeText").text = hostAwayLine(room)
    end if
end sub

' A time the server sends, as the clock on the wall here says it ("4:20 PM").
function localTime(iso as string) as string
    t = createObject("roDateTime")
    t.fromSeconds(whenSeconds(iso))
    t.toLocalTime()
    h = t.getHours()
    suffix = " AM"
    if h >= 12 then suffix = " PM"
    h = h mod 12
    if h = 0 then h = 12
    mins = t.getMinutes()
    text = mins.toStr()
    if mins < 10 then text = "0" + text
    return h.toStr() + ":" + text + suffix
end function

function hostAwayLine(room as object) as string
    now = whenSeconds(room.clock.at) + int(UpTime(0) - room.receivedAt)
    return hostAwayText(whenSeconds(room.host.pausesAt) - now, whenSeconds(room.host.endsAt) - now)
end function

' A queue title as read from the server, or what the room says of it until then.
function itemOf(entry as object) as object
    item = m.items[entry.itemId]
    if item <> invalid then return item
    m.items[entry.itemId] = {id: entry.itemId, kind: entry.kind, title: entry.title}
    api("GET", "/v1/items/" + entry.itemId, invalid, "onItem", {id: entry.itemId})
    return m.items[entry.itemId]
end function

sub onItem(event as object)
    result = event.getData()
    if not result.ok then return
    m.items[event.getRoSGNode().ctx.id] = result.data
    if m.view = "room" then drawRoom()
end sub

' The shared queue: what the group will watch, the one on marked Now playing.
sub drawQueue(room as object)
    t = m.t
    m.queue.removeChildrenIndex(m.queue.getChildCount(), 0)
    m.queueKeys = []
    empty = m.top.findNode("queueEmpty")
    empty.text = ""
    if room.queue.count() = 0
        if room.you.mayControl = true then empty.text = "Nothing queued. Press * on any title and choose Add to group queue." else empty.text = "Nothing queued."
    end if
    shift = m.top.findNode("left").translation[1]
    space = t.footY - 24 - (t.queueY + shift)
    fits = int(space / frames().settings.row)
    if fits < 1 then fits = 1
    m.queueFits = fits
    for i = 0 to room.queue.count() - 1
        m.queueKeys.push("queue:" + room.queue[i].entryId)
    end for
    keepInView(m.queueKeys, "queueFrom", fits)
    y = 0
    last = m.queueFrom + fits - 1
    if last > room.queue.count() - 1 then last = room.queue.count() - 1
    for i = m.queueFrom to last
        entry = room.queue[i]
        row = m.queue.createChild("ListRow")
        row.rowWidth = t.nowWidth
        model = {label: "Something you can’t see on this profile", help: "Not available to you", scope: "", value: "", control: "", icon: "", danger: false}
        if entry.available = true
            item = itemOf(entry)
            model.label = entry.title
            model.help = queueHelp(item)
            if item.kind = "episode" and item.rootTitle <> invalid then model.label = item.rootTitle
        end if
        if entry.entryId = room.clock.entryId
            model.value = "Now playing"
            model.icon = "play"
        end if
        row.model = model
        row.separator = (i > m.queueFrom)
        row.translation = [0, y]
        y = y + row.rowHeight
    end for
    plate = m.top.findNode("queuePlate")
    plate.visible = (y > 0)
    plate.height = y
end sub

' The line under a queued title: an episode's code and name, a film's kind and year.
function queueHelp(item as object) as string
    if item.kind = "episode" then return (episodeCode(item) + "   " + item.title).trim()
    words = []
    if item.kind = "movie" then words.push("Movie")
    if item.year <> invalid then words.push(item.year.toStr())
    return words.join("   ")
end function

' Who is here, each with their part and state (frame 14), on a face of their own colour.
sub drawPeople(room as object)
    c = colours()
    t = m.t
    m.people.removeChildrenIndex(m.people.getChildCount(), 0)
    m.peopleKeys = []
    count = room.members.count()
    head = count.toStr() + " people"
    if count = 1 then head = "Just you so far"
    m.top.findNode("peopleHead").text = head
    bottom = t.footY - 24
    if room.you.host = true then bottom = t.inviteHeadY - 24
    fits = int((bottom - t.peopleY) / (64 + 24))
    if fits < 1 then fits = 1
    for each member in room.members
        m.peopleKeys.push("person:" + member.id)
    end for
    keepInView(m.peopleKeys, "peopleFrom", fits)
    order = ["blue", "violet", "coral", "mint", "gold", "sky", "rose", "slate"]
    y = 0
    last = m.peopleFrom + fits - 1
    if last > count - 1 then last = count - 1
    for i = m.peopleFrom to last
        member = room.members[i]
        name = memberName(member)
        state = memberState(member.status)
        help = ""
        if member.host = true then help = "Host"
        row = m.people.createChild("ListRow")
        row.rowWidth = t.peopleWidth
        row.model = {label: name, help: help, scope: "", value: state.text, tone: state.tone, control: "", icon: "", danger: false, initial: ucase(left(name, 1)), face: c.faces[order[i mod 8]]}
        row.separator = (i > m.peopleFrom)
        row.translation = [0, y]
        y = y + row.rowHeight
    end for
    plate = m.top.findNode("peoplePlate")
    plate.height = y
end sub

' Keeps the row with focus inside the stretch of a list that is drawn.
sub keepInView(keys as object, field as string, fits as integer)
    for i = 0 to keys.count() - 1
        if keys[i] = m.focusKey
            if i < m[field] then m[field] = i
            if i > m[field] + fits - 1 then m[field] = i - fits + 1
        end if
    end for
    if m[field] > keys.count() - fits then m[field] = keys.count() - fits
    if m[field] < 0 then m[field] = 0
end sub

sub onRoomButton(event as object)
    id = event.getData()
    room = m.group.room
    if id = "player"
        entry = roomEntry(room)
        if entry <> invalid then m.top.open = {item: itemOf(entry), play: true, together: true}
    else if id = "leave"
        if leaveAsks(room)
            m.confirming = "leave"
            m.confirm.model = {title: "Leave while you’re the host?", body: "The group pauses after two minutes without a host, then ends. Make someone else the host first to keep it going.", verb: "Leave anyway"}
        else
            ask({do: "leave"})
        end if
    else if id = "end"
        m.confirming = "end"
        m.confirm.model = {title: "End the group for everyone?", body: "Everyone’s playback carries on by itself, out of step.", verb: "End group"}
    else
        ' play, pause, goAhead, becomeHost, newCode: the group answers with the room
        ask({do: id})
    end if
end sub

sub onConfirmed()
    if m.confirm.chosen = "confirm" then ask({do: m.confirming})
    focusNow(true)
end sub

sub ask(what as object)
    m.asked = what.do
    m.group.ask = what
end sub

' A change the group refused is said in the notice strip; a sheet's refusal goes under its field.
sub onAnswer()
    if gone() then return
    answer = m.group.answer
    if answer.do <> m.asked then return
    m.asked = ""
    if m.sheetKind = "join"
        if not answer.ok then showRefusal(answer)
        return
    end if
    if m.sheetKind = "start"
        if not answer.ok then showRefusal(answer)
        return
    end if
    m.problem = ""
    if not answer.ok then m.problem = answer.message
    if m.view = "room" then drawRoom()
end sub

sub showRefusal(answer as object)
    message = answer.message
    if answer.code = "code_invalid" then message = "That code didn’t work."
    if m.sheetKind = "join"
        field = m.top.findNode("codeField")
        field.note = message
        field.refused = true
    else
        m.top.findNode("sheetNote").text = message
    end if
end sub

' A queue row's menu (Watch this, Remove from queue) and a person's (Make host).
sub onMenuChosen(event as object)
    id = event.getData()
    if left(id, 6) = "watch:" then ask({do: "watchEntry", entryId: mid(id, 7)})
    if left(id, 7) = "remove:" then ask({do: "remove", entryId: mid(id, 8)})
    if left(id, 5) = "host:" then ask({do: "makeHost", memberId: mid(id, 6)})
end sub

sub openRowMenu()
    room = m.group.room
    if left(m.focusKey, 6) = "queue:"
        if room.you.mayControl <> true then return
        entryId = mid(m.focusKey, 7)
        title = ""
        for each entry in room.queue
            if entry.entryId = entryId and entry.available = true then title = entry.title
        end for
        rows = []
        if title <> "" and entryId <> room.clock.entryId then rows.push({id: "watch:" + entryId, text: "Watch this", icon: "play"})
        rows.push({id: "remove:" + entryId, text: "Remove from queue", icon: "close"})
        m.top.menu = {eyebrow: "Queue", title: title, status: "", rows: rows, hints: [{keys: ["Back"], text: "Close"}]}
    else if left(m.focusKey, 7) = "person:"
        for each member in room.members
            if "person:" + member.id = m.focusKey and mayMakeHost(room, member)
                m.top.menu = {eyebrow: room.name, title: memberName(member), status: "", rows: [{id: "host:" + member.id, text: "Make host", icon: ""}], hints: [{keys: ["Back"], text: "Close"}]}
            end if
        end for
    end if
end sub

' --- the group has ended (frame 17) --------------------------------------------------------------

sub showEnded()
    if m.sheetKind <> "" then closeSheet()
    reason = invalid
    if m.group.room <> invalid and m.group.room.ended <> invalid then reason = m.group.room.ended.reason
    m.state.icon = "people"
    m.state.title = "This group has ended"
    m.state.body = endedText(reason)
    m.state.buttons = [{id: "lobby", text: "Back to Watch Together", icon: ""}]
    showOnly("ended")
    focusNow()
end sub

' --- the sheets: Join with a code, Start a group ------------------------------------------------

sub openSheet(kind as string)
    f = frames()
    t = m.t
    m.sheetKind = kind
    m.top.findNode("sheet").visible = true
    x = t.joinX
    if kind = "start" then x = t.startX
    pad = x + t.sheetPad
    panel = m.top.findNode("panel")
    panel.translation = [x, 0]
    panel.width = 1920 - x
    m.top.findNode("sheetEyebrow").translation = [pad, t.sheetTop]
    m.top.findNode("sheetTitle").translation = [pad, t.sheetTop + 34]
    words = m.top.findNode("sheetWords")
    words.translation = [pad, t.sheetTop + 104]
    code = m.top.findNode("codeField")
    group = m.top.findNode("groupField")
    name = m.top.findNode("nameField")
    name.text = m.profileName
    m.top.findNode("sheetNote").text = ""
    join = (kind = "join")
    code.visible = join
    m.keyboard.visible = join
    group.visible = not join
    m.top.findNode("whoLabel").visible = not join
    m.control.visible = not join
    hints = m.top.findNode("sheetHints")
    hints.translation = [pad, f.sheet.hintsY]
    if join
        m.top.findNode("sheetTitle").text = "Join with a code"
        words.text = "Ask the host for the eight-character code."
        words.width = t.fieldWidth
        code.fieldWidth = t.fieldWidth
        name.fieldWidth = t.fieldWidth
        code.translation = [pad, t.joinFieldsY]
        name.translation = [pad, t.joinFieldsY + t.fieldPitch]
        m.submit.translation = [pad, t.joinSubmitY]
        m.keyboard.text = ""
        code.text = ""
        code.note = ""
        code.refused = false
        hints.hints = [{keys: ["Back"], text: "Close"}, {keys: ["OK"], text: "Type"}]
        m.sheetAt = "keys"
        drawJoin()
    else
        width = f.rightEdge - pad
        m.top.findNode("sheetTitle").text = "Start a group"
        words.text = "You get a code to share. Then press * on any title and choose Watch with group."
        words.width = width
        group.fieldWidth = width
        name.fieldWidth = width
        group.text = ""
        if m.profileName <> "" then group.text = m.profileName + "’s group"
        group.translation = [pad, t.startFieldsY]
        name.translation = [pad, t.startFieldsY + t.fieldPitch]
        m.top.findNode("whoLabel").translation = [pad + 4, t.startWhoY]
        m.control.translation = [pad, t.startWhoY + 38]
        m.top.findNode("sheetNote").translation = [pad, t.startNoteY]
        m.top.findNode("sheetNote").width = width
        m.submit.translation = [pad, t.startSubmitY]
        m.submit.buttons = [{id: "start", text: "Start", icon: ""}]
        m.controlChoice = "host"
        drawControl()
        hints.hints = [{keys: ["Back"], text: "Close"}]
        m.sheetAt = "group"
    end if
    focusSheet()
end sub

sub closeSheet()
    m.sheetKind = ""
    m.top.findNode("sheet").visible = false
    focusNow(true)
end sub

' Join turns on at four characters (frame 12): until then there is no button to press.
sub drawJoin()
    if codeReady(m.keyboard.text) then m.submit.buttons = [{id: "join", text: "Join", icon: ""}] else m.submit.buttons = []
end sub

sub drawControl()
    m.control.buttons = [{id: "host", text: "Only me", icon: "", switchedOn: m.controlChoice = "host"}, {id: "everyone", text: "Everyone", icon: "", switchedOn: m.controlChoice = "everyone"}]
end sub

sub onControl(event as object)
    m.controlChoice = event.getData()
    drawControl()
end sub

' What Portico's keyboard types goes into the code, tidied as it goes (letters and digits, eight at most).
sub onTyped()
    tidy = tidyCode(m.keyboard.text)
    if lcase(tidy) <> m.keyboard.text
        m.keyboard.text = lcase(tidy)      ' a space or a ninth character is not kept (and this runs again)
        return
    end if
    field = m.top.findNode("codeField")
    field.text = codeShown(tidy)
    field.note = ""
    field.refused = false
    drawJoin()
end sub

sub onSubmit(event as object)
    name = m.top.findNode("nameField").text.trim()
    m.top.findNode("sheetNote").text = ""
    if event.getData() = "join"
        ask({do: "join", code: m.keyboard.text, displayName: name})
    else
        group = m.top.findNode("groupField").text.trim()
        if group = "" then group = m.profileName + "’s group"
        ask({do: "create", name: group, displayName: name, control: m.controlChoice})
    end if
end sub

' Names are typed with Roku's own keyboard, which has capitals and spaces.
sub typeName(which as string)
    field = m.top.findNode(which)
    dialog = createObject("roSGNode", "StandardKeyboardDialog")
    dialog.title = field.label
    dialog.buttons = ["OK", "Cancel"]
    dialog.text = field.text
    dialog.observeField("buttonSelected", "onNameTyped")
    dialog.observeField("wasClosed", "onNameClosed")
    m.naming = which
    m.dialog = dialog
    m.top.getScene().dialog = dialog
end sub

sub onNameTyped()
    if m.dialog = invalid then return
    if m.dialog.buttonSelected = 0 then m.top.findNode(m.naming).text = m.dialog.text.trim()
    m.dialog.close = true
end sub

sub onNameClosed()
    m.dialog = invalid
    focusSheet()
end sub

' The sheet's stops, top to bottom; Portico's keyboard sits to the right of Join's.
function sheetStops() as object
    if m.sheetKind = "join"
        stops = ["code", "name"]
        if m.submit.buttons.count() > 0 then stops.push("submit")
        return stops
    end if
    return ["group", "name", "control", "submit"]
end function

sub focusSheet()
    at = m.sheetAt
    if at = "keys"
        m.keyboard.active = true
    else if at = "control"
        m.control.active = true
    else if at = "submit" and m.submit.buttons.count() > 0
        m.submit.active = true
    else
        m.top.findNode("rowStop").setFocus(true)
    end if
    paintSheet()
end sub

sub paintSheet()
    if m.sheetKind = "" then return
    m.top.findNode("codeField").on = (m.sheetAt = "code")
    m.top.findNode("codeField").typing = (m.sheetAt = "keys")
    m.top.findNode("groupField").on = (m.sheetAt = "group")
    m.top.findNode("nameField").on = (m.sheetAt = "name")
end sub

function sheetKey(key as string) as boolean
    stops = sheetStops()
    at = -1
    for i = 0 to stops.count() - 1
        if stops[i] = m.sheetAt then at = i
    end for
    if key = "back"
        closeSheet()
    else if m.sheetAt = "keys"
        ' the keyboard gave the key up: Left from its first column reaches the fields and Join
        if key = "left"
            m.sheetAt = stops[stops.count() - 1]
            focusSheet()
        end if
    else if key = "right" and m.sheetKind = "join"
        m.sheetAt = "keys"
        focusSheet()
    else if key = "up" and at > 0
        m.sheetAt = stops[at - 1]
        focusSheet()
    else if key = "down" and at >= 0 and at < stops.count() - 1
        m.sheetAt = stops[at + 1]
        focusSheet()
    else if key = "OK"
        if m.sheetAt = "code"
            m.sheetAt = "keys"
            focusSheet()
        else if m.sheetAt = "name"
            typeName("nameField")
        else if m.sheetAt = "group"
            typeName("groupField")
        end if
    end if
    return true      ' the sheet holds focus until it closes
end function

' --- focus -------------------------------------------------------------------------------------

' The stops of the room's two columns, top to bottom.
function leftStops() as object
    stops = []
    if m.hasNotice then stops.push("notice")
    if m.actions.buttons.count() > 0 then stops.push("actions")
    stops.append(m.queueKeys)
    stops.push("foot")
    return stops
end function

function rightStops() as object
    stops = []
    stops.append(m.peopleKeys)
    if m.top.findNode("invite").visible then stops.push("invite")
    return stops
end function

function lobbyStops() as object
    stops = ["buttons"]
    for each id in m.groupIds
        stops.push("group:" + id)
    end for
    return stops
end function

' Puts focus on what m.focusKey names, or on the nearest stop when that is gone. A new reading of
' the room does this only while the page has focus: never under the player, a menu or a dialog.
sub focusNow(given = false as boolean)
    if not given and not m.top.isInFocusChain() then return
    if m.confirm.visible then return
    if m.sheetKind <> ""
        focusSheet()
        return
    end if
    if m.view = "ended" or m.view = "state"
        m.state.active = true
        return
    end if
    if m.view = "lobby"
        if m.groupIds = invalid then m.groupIds = []
        if not hasStop(lobbyStops(), m.focusKey) then m.focusKey = "buttons"
        if m.focusKey = "buttons" then m.lobbyButtons.active = true else m.top.findNode("rowStop").setFocus(true)
        paintRows()
        return
    end if
    if m.view <> "room" then return
    if not hasStop(leftStops(), m.focusKey) and not hasStop(rightStops(), m.focusKey)
        ' a stop that went away (a title removed, a person gone): the room's own buttons, or the foot
        m.focusKey = leftStops()[0]
        if hasStop(leftStops(), "actions") then m.focusKey = "actions"
    end if
    key = m.focusKey
    if key = "notice"
        m.noticeButton.active = true
    else if key = "actions"
        m.actions.active = true
    else if key = "foot"
        m.foot.active = true
    else if key = "invite"
        m.inviteButton.active = true
    else
        m.top.findNode("rowStop").setFocus(true)
    end if
    paintRows()
end sub

function hasStop(stops as object, key as string) as boolean
    for each one in stops
        if one = key then return true
    end for
    return false
end function

' The row with focus is drawn in focus; the others rest.
sub paintRows()
    if m.view = "lobby"
        for i = 0 to m.groups.getChildCount() - 1
            m.groups.getChild(i).on = (m.focusKey = "group:" + m.groupIds[i])
        end for
        return
    end if
    for i = 0 to m.queue.getChildCount() - 1
        m.queue.getChild(i).on = (m.focusKey = m.queueKeys[m.queueFrom + i])
    end for
    for i = 0 to m.people.getChildCount() - 1
        m.people.getChild(i).on = (m.focusKey = m.peopleKeys[m.peopleFrom + i])
    end for
end sub

' Moves focus to another stop; a list that scrolls is drawn again around it.
sub moveTo(key as string)
    m.focusKey = key
    if left(key, 6) = "queue:" or left(key, 7) = "person:"
        drawRoom()
    else
        focusNow()
    end if
end sub

function moveBy(stops as object, by as integer) as boolean
    for i = 0 to stops.count() - 1
        if stops[i] = m.focusKey
            if i + by < 0 or i + by > stops.count() - 1 then return false
            moveTo(stops[i + by])
            return true
        end if
    end for
    return false
end function

function onKeyEvent(key as string, press as boolean) as boolean
    if not press then return false
    if m.confirm.visible then return true
    if m.sheetKind <> "" then return sheetKey(key)
    if m.view = "lobby"
        stops = lobbyStops()
        if key = "down" then return moveBy(stops, 1)
        if key = "up" then return moveBy(stops, -1)
        if key = "OK" and left(m.focusKey, 6) = "group:"
            ask({do: "open", id: mid(m.focusKey, 7)})
            return true
        end if
        return false
    end if
    if m.view <> "room" then return false
    onLeft = hasStop(leftStops(), m.focusKey)
    if key = "down" or key = "up"
        by = 1
        if key = "up" then by = -1
        if onLeft
            moveBy(leftStops(), by)
            return true
        end if
        if moveBy(rightStops(), by) then return true
        ' Up from the first person reaches the notice's button, over both columns
        if key = "up" and m.hasNotice then moveTo("notice")
        return true
    end if
    if key = "right" and onLeft and m.focusKey <> "notice"
        stops = rightStops()
        if stops.count() > 0 then moveTo(stops[0])
        return true
    end if
    if key = "left" and not onLeft
        moveTo(leftStops()[0])
        if hasStop(leftStops(), "actions") then moveTo("actions")
        return true
    end if
    if key = "OK"
        openRowMenu()
        return true
    end if
    return false     ' Left from the left column: the rail; Back: the scene
end function
