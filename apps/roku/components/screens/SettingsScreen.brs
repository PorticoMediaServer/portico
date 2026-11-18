sub init()
    c = colours()
    f = frames().settings
    m.f = f
    ' Both columns: the title at the frame's top, then their content one header gap (28) under it.
    ' The list's title and group label line up with its rows' icons.
    heading = m.top.findNode("heading")
    setType(heading, "title", c.text)
    heading.translation = [f.listX + 20, f.top]
    m.contentY = f.top + heading.boundingRect().height + f.headerGap
    ' the Search settings box, then the list's group label one header gap under it
    find = m.top.findNode("find")
    find.width = f.listWidth
    find.height = f.findHeight
    find.translation = [f.listX, f.findY]
    glass = m.top.findNode("findGlass")
    glass.blendColor = c.textTertiary
    glass.translation = [f.listX + 20, f.findY + (f.findHeight - 24) / 2]
    words = m.top.findNode("findWords")
    setType(words, "listLabel", c.textTertiary)
    words.translation = [f.listX + 62, f.findY]
    words.height = f.findHeight
    group = m.top.findNode("group")
    setType(group, "groupLabel", c.textSecondary)
    group.translation = [f.listX + 20, f.findY + f.findHeight + f.headerGap]
    m.listTop = group.translation[1] + group.boundingRect().height + 8
    ' the remote's keys for the list, on two lines placed up from the screen's foot
    m.top.findNode("hintsList").hints = [{keys: ["Up", "Down"], text: "Choose a page"}]
    m.top.findNode("hintsList").translation = [f.listX, f.hintsY]
    m.top.findNode("hintsRows").hints = [{keys: ["Right"], text: "Edit its rows"}, {keys: ["Left"], text: "Pages"}]
    m.top.findNode("hintsRows").translation = [f.listX, f.hintsY + 50]
    for each id in ["fadeTop", "fadeBottom"]
        fade = m.top.findNode(id)
        fade.width = f.pageWidth + 80
        fade.height = 1080 - f.foot + 64
    end for
    m.top.findNode("fadeTop").translation = [f.pageX - 40, 0]
    m.top.findNode("fadeBottom").translation = [f.pageX - 40, f.foot - 64]
    m.pageBottom = 0      ' where the open page's content ends, once it is drawn
    m.list = m.top.findNode("list")
    m.pages = m.top.findNode("pages")
    m.plate = m.top.findNode("listPlate")
    m.plate.width = f.listWidth
    m.plate.height = f.listRow
    m.top.findNode("listBar").color = c.accent
    m.ring = m.top.findNode("listRing")
    m.state = m.top.findNode("state")
    m.state.observeField("pressed", "load")
    m.confirm = m.top.findNode("confirm")
    m.confirm.observeField("chosen", "onConfirmed")
    m.spec = settingsPages()
    m.region = "list"     ' or "rows"
    m.page = 0
    m.row = 0
    m.scroll = 0
    m.stops = []
    m.built = {}          ' each page's drawn rows, kept until the data under them changes
    m.advanced = {}       ' the pages whose Advanced group is open
    m.values = invalid
    m.failed = ""         ' the setting whose last change the server refused
    m.resetDone = false
    initSearch()
    drawList()
    m.top.observeField("focusedChild", "paint")
    load()
end sub

' Everything the pages show, asked for at once; each page draws what has arrived.
sub load()
    api("GET", "/v1/preferences?deviceClass=television", invalid, "onPreferences")
    api("GET", "/v1/me", invalid, "onMe")
    api("GET", "/v1/devices", invalid, "onDevices")
    api("GET", "/v1/system", invalid, "onSystem")
end sub

sub onPreferences(event as object)
    result = event.getData()
    if not result.ok
        m.state.icon = "warning"
        m.state.title = "Can't reach the server"
        m.state.body = "You're still signed in. Try again in a moment."
        m.state.buttons = [{id: "retry", text: "Try again now", icon: "refresh"}]
        showPageState(true)
        m.region = "list"
        if m.top.isInFocusChain() then m.state.active = true
        return
    end if
    showPageState(false)
    took(result.data)
end sub

' The whole-page state replaces the page and its list (rule 9): nothing on it can be shown.
sub showPageState(on as boolean)
    m.state.visible = on
    for each id in ["find", "findGlass", "findWords", "group", "listPlate", "listBar", "listRing", "list", "pages", "hintsList", "hintsRows"]
        m.top.findNode(id).visible = not on
    end for
end sub

' The server's answer to a read or a save: the values in force, where each came from, and the
' scope each setting is saved at.
sub took(snapshot as object)
    m.values = snapshot.effective
    m.sources = snapshot.effectiveSource
    m.scopes = {}
    for each field in snapshot.registry.fields
        m.scopes[field.key] = field.scopes[0]
    end for
    redraw()
end sub

sub onMe(event as object)
    result = event.getData()
    if result.ok then m.me = result.data
    redraw()
end sub

sub onDevices(event as object)
    result = event.getData()
    if result.ok then m.devices = result.data.items
    redraw()
end sub

sub onSystem(event as object)
    result = event.getData()
    if result.ok then m.system = result.data
    redraw()
end sub

' --- the page list -----------------------------------------------------------------------------

sub drawList()
    c = colours()
    y = m.listTop
    m.listY = []
    for each page in m.spec
        if page.gapBefore = true then y = y + 14
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
end sub

' --- the page in focus ----------------------------------------------------------------------------

' Data arrived or a value changed: every page is drawn again from it, the open one now, with focus
' kept on the same row.
sub redraw()
    m.pages.removeChildrenIndex(m.pages.getChildCount(), 0)
    m.built = {}
    showPage()
end sub

sub showPage()
    for each id in m.built
        m.built[id].group.visible = false
    end for
    page = m.spec[m.page]
    if m.built[page.id] = invalid then m.built[page.id] = drawPage(page)
    built = m.built[page.id]
    built.group.visible = true
    m.stops = built.stops
    m.pageBottom = built.bottom
    if m.row >= m.stops.count() then m.row = m.stops.count() - 1
    if m.row < 0 then m.row = 0
    paint()
end sub

' The title and its intro in the fixed header slot, then each section: its label, then its rows on
' one raised card. Sections are 44 apart, from the frame's line, whatever the header holds.
function drawPage(page as object) as object
    c = colours()
    f = m.f
    node = m.pages.createChild("Group")
    stops = []
    title = node.createChild("Label")
    title.text = page.title
    title.translation = [f.pageX, f.top]
    title.width = f.pageWidth
    setType(title, "title", c.text)
    ' sections start one header gap under the title, or under the intro when the page has one
    y = m.contentY
    if page.intro <> ""
        intro = node.createChild("Label")
        intro.text = page.intro
        intro.translation = [f.pageX, f.top + title.boundingRect().height + 14]
        intro.width = f.introWidth
        intro.wrap = true
        intro.maxLines = 2
        setType(intro, "body", c.textSecondary)
        y = intro.translation[1] + intro.boundingRect().height + f.headerGap
    end if
    advancedDrawn = false
    for each section in page.sections
        if section.advanced = true and not advancedDrawn
            ' one closed line at the foot of the page, saying what is changed under it
            advancedDrawn = true
            open = (m.advanced[page.id] = true)
            control = "expand"
            if open then control = "collapse"
            y = drawCard(node, stops, y, [{label: "Advanced", value: advancedSummary(page), control: control, act: {kind: "advanced"}}])
        end if
        if section.advanced <> true or m.advanced[page.id] = true
            rows = []
            for each spec in section.rows
                for each row in rowsFor(spec, section, page)
                    rows.push(row)
                end for
            end for
            if rows.count() > 0
                if section.title <> ""
                    label = node.createChild("Label")
                    label.text = section.title
                    label.translation = [f.pageX + 24, y]
                    setType(label, "groupLabel", c.textSecondary)
                    y = y + label.boundingRect().height + 22
                end if
                if section.note <> invalid
                    note = node.createChild("Label")
                    note.text = section.note
                    note.translation = [f.pageX + 24, y]
                    note.width = f.pageWidth - 48
                    note.wrap = true
                    setType(note, "help", c.textTertiary)
                    note.lineSpacing = 3
                    y = y + note.boundingRect().height + 22
                end if
                y = drawCard(node, stops, y, rows)
            end if
        end if
    end for
    return {group: node, stops: stops, bottom: y - f.sectionGap}
end function

' Rows on one raised card; returns where the next section starts.
function drawCard(node as object, stops as object, y as integer, rows as object) as integer
    c = colours()
    f = m.f
    card = node.createChild("Poster")
    card.uri = "pkg:/images/round-14.9.png"
    card.blendColor = c.raised
    card.translation = [f.pageX, y]
    card.width = f.pageWidth
    top = y
    first = true
    for each row in rows
        item = node.createChild("ListRow")
        item.rowWidth = f.pageWidth
        item.model = {label: row.label, help: wordsOrNone(row.help), scope: wordsOrNone(row.scope), value: wordsOrNone(row.value),
            control: wordsOrNone(row.control), switchOn: (row.switchOn = true), icon: wordsOrNone(row.icon), danger: (row.danger = true)}
        item.translation = [f.pageX, y]
        stops.push({node: item, act: row.act, first: first})
        first = false
        y = y + item.rowHeight
    end for
    card.height = y - top
    return y + f.sectionGap
end function

' A row's optional words: "" when the row has none.
function wordsOrNone(value as dynamic) as string
    if value = invalid then return ""
    return value
end function

' The rows one spec line becomes: none while its data has not arrived or the server does not
' publish the setting, several for the account's devices.
function rowsFor(spec as object, section as object, page as object) as object
    c = colours()
    if spec.type = "info" or spec.type = "action" then return accountRows(spec)
    if m.values = invalid then return []
    row = {label: spec.label, help: wordsOrNone(spec.help), scope: wordsOrNone(spec.scope), act: {kind: spec.type, spec: spec, section: section, page: page}}
    if spec.when <> invalid
        if not sameSetting(m.values[spec.when.key], spec.when.equals) then return []
    end if
    if spec.composite <> invalid
        if m.values["playback.passoutProtection"] = invalid and spec.composite = "stillWatching" then return []
        if m.values["delivery.directPlay"] = invalid and spec.composite = "playbackMethod" then return []
        value = compositeValue(spec.composite, m.values)
    else
        value = m.values[spec.key]
        if value = invalid then return []
    end if
    if spec.type = "switch"
        row.control = "switch"
        row.switchOn = (value = true)
    else if spec.type = "languages"
        row.value = languagesWords(value)
    else
        row.control = "choice"
        row.value = choiceWords(spec.choices, value)
        if row.value = "" and type(value) = "roString" then row.value = value     ' set elsewhere (the web): shown as it is
    end if
    if m.failed <> "" and m.failed = rowKey(spec) then row.help = "Couldn't save this change. Try again."
    return [row]
end function

function rowKey(spec as object) as string
    if spec.composite <> invalid then return spec.composite
    return spec.key
end function

' The rows that come from the account and this TV rather than from a preference.
function accountRows(spec as object) as object
    does = {kind: spec.type, id: spec.id}
    id = spec.id
    if id = "identity"
        if m.me = invalid then return []
        help = ""
        if m.me.profile <> invalid then help = "Using the " + m.me.profile.name + " profile"
        return [{label: m.me.account.username, help: help, icon: "profile", act: does}]
    else if id = "porticoAccount"
        if m.me = invalid then return []
        if m.me.account.hostedAccountId <> invalid
            return [{label: spec.label, value: "Signed in with your Portico Account", help: "This server keeps no passwords.", act: does}]
        end if
        return [{label: spec.label, value: "Signed in with a local account", help: "This server never contacts Portico Services.", act: does}]
    else if id = "devices"
        if m.devices = invalid then return []
        rows = []
        for each device in m.devices
            row = {label: device.name, icon: deviceIcon(device.platform), act: does}
            if device.current = true then row.value = "This device" else row.help = lastSeenWords(device.lastSeenAt)
            rows.push(row)
        end for
        return rows
    else if id = "deviceName"
        ' the name the server holds for this installation, the one the account's device list shows
        if m.devices = invalid then return []
        for each device in m.devices
            if device.current = true then return [{label: spec.label, value: device.name, act: does}]
        end for
        return []
    else if id = "appVersion"
        return [{label: spec.label, value: createObject("roAppInfo").getVersion(), act: does}]
    else if id = "server" or id = "serverVersion"
        if m.system = invalid then return []
        if id = "server" then value = m.system.name else value = m.system.version
        return [{label: spec.label, value: value, act: does}]
    else if id = "reset"
        help = spec.help
        if m.resetDone then help = "Recommendations reset."
        return [{label: spec.label, help: help, icon: spec.icon, act: does}]
    else if id = "licences"
        return [{label: spec.label, value: spec.value, control: spec.control, act: does}]
    end if
    return [{label: spec.label, icon: wordsOrNone(spec.icon), danger: (spec.danger = true), act: does}]
end function

' The closed Advanced line: what is changed under it, or "All at defaults".
function advancedSummary(page as object) as string
    if m.values = invalid then return ""
    changed = []
    for each section in page.sections
        if section.advanced = true
            for each spec in section.rows
                if m.sources[spec.key] <> invalid and m.sources[spec.key] <> "default"
                    changed.push(spec.label + " " + choiceWords(spec.choices, m.values[spec.key]))
                end if
            end for
        end if
    end for
    if changed.count() = 0 then return "All at defaults"
    return changed.join(" · ")
end function

' --- This device: Open-source licenses -----------------------------------------------------------

' The licences take the page's place (states frame 30); Back there returns to This device's row.
sub openLicences()
    if m.licences = invalid
        m.licences = m.top.createChild("LicencesPage")
        m.licences.observeField("done", "onLicencesDone")
    end if
    m.licences.visible = true
    m.pages.visible = false
    m.top.findNode("fadeTop").visible = false
    m.top.findNode("fadeBottom").visible = false
    m.region = "licences"
    m.licences.active = true
end sub

sub onLicencesDone()
    m.licences.visible = false
    m.pages.visible = true
    m.region = "rows"
    m.list.setFocus(true)     ' the licences are inside the screen: the list group takes focus back
    paint()
end sub

' --- focus ----------------------------------------------------------------------------------------

sub onActive()
    if m.top.active and m.region = "licences"     ' the licences page takes the focus itself
        m.licences.active = true
        return
    end if
    if not m.top.active then return
    if m.state.visible
        m.state.active = true
    else if m.region = "search"
        m.keyboard.active = true
    else if m.region = "matches"
        m.matches.setFocus(true)
    else
        m.top.setFocus(true)
    end if
    paint()
end sub

' OK on Settings in the rail: back to the page list, on the page that is open.
sub onToTop()
    if m.region = "search" or m.region = "matches" then leaveSearch()
    m.region = "list"
    m.top.active = true
end sub

sub paint()
    if m.region = "licences" then return     ' the licences page paints itself
    c = colours()
    has = m.top.hasFocus() or m.list.hasFocus()     ' the list group holds focus after the dialog
    inList = (m.region = "list")
    if m.region = "matches" or m.region = "search"
        paintMatches()
        return
    end if
    for i = 0 to m.list.getChildCount() - 1
        row = m.list.getChild(i)
        open = (i = m.page)
        tint = c.textSecondary
        if open then tint = c.text
        row.getChild(1).color = tint
        row.getChild(0).blendColor = tint
        if open and not inList then row.getChild(0).blendColor = c.accent
    end for
    ' the open page: a plate and the accent bar; in focus, the bright plate and the ring as well
    y = m.listY[m.page]
    x = m.f.listX
    m.plate.translation = [x, y]
    m.top.findNode("listBar").translation = [x, y + 14]
    out = metrics().focusSpread + 6
    m.ring.translation = [x - out, y - out]
    m.ring.width = m.f.listWidth + out * 2
    m.ring.height = m.f.listRow + out * 2
    m.ring.visible = (has and inList)
    if has and inList then m.plate.blendColor = c.raisedBright else m.plate.blendColor = c.raisedHigh
    ' the Search settings box: raised, the bright plate and the ring while it has focus
    inFind = (has and m.region = "find")
    findRing = m.top.findNode("findRing")
    findRing.visible = inFind
    findRing.translation = [x - out, m.f.findY - out]
    findRing.width = m.f.listWidth + out * 2
    findRing.height = m.f.findHeight + out * 2
    if inFind then m.top.findNode("find").blendColor = c.raisedBright else m.top.findNode("find").blendColor = c.raised
    if m.region = "find" then m.ring.visible = false
    for i = 0 to m.stops.count() - 1
        here = m.stops[i]
        here.node.on = (has and m.region = "rows" and i = m.row)
        here.node.separator = not here.first and not (has and m.region = "rows" and i - 1 = m.row)
    end for
    scrollTo()
end sub

' The page moves only to keep the row in focus clear of the screen's foot (56) and top.
sub scrollTo()
    if m.region <> "rows" or m.stops.count() = 0
        m.scroll = 0
    else
        node = m.stops[m.row].node
        y = node.translation[1]
        bottom = y + node.rowHeight
        if bottom <= m.f.foot
            m.scroll = 0
        else
            if bottom - m.scroll > m.f.foot then m.scroll = bottom - m.f.foot
            if y - m.scroll < m.f.scrollTop then m.scroll = y - m.f.scrollTop
        end if
    end if
    m.pages.translation = [0, -m.scroll]
    m.top.findNode("fadeTop").visible = (m.scroll > 0)
    m.top.findNode("fadeBottom").visible = (m.pageBottom - m.scroll > m.f.foot)
end sub

function pressKey(key as string) as boolean
    by = 1
    if key = "up" then by = -1
    if m.region = "matches"
        want = m.match + by
        if want < 0 or want >= m.matchStops.count() then return false
        m.match = want
        paintMatches()
        return true
    end if
    if m.region = "search" then return false
    if m.region = "find"
        if key <> "down" then return false     ' Up at the top goes on to the scene
        m.region = "list"
        paint()
        return true
    end if
    if m.region = "list"
        want = m.page + by
        if want < 0
            m.region = "find"
            paint()
            return true
        end if
        if want >= m.spec.count() then return false
        m.page = want
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
    if key = "up" or key = "down"
        pressKey(key)
        return true
    end if
    if m.region = "find"
        if key = "right" or key = "OK"
            enterSearch()
            return true
        end if
        return false        ' Left opens the rail
    end if
    if m.region = "search"
        ' the keyboard gave the key up: Right from its last column reaches the matches
        if key = "right" and m.matchStops.count() > 0
            m.region = "matches"
            m.match = 0
            m.matches.setFocus(true)
            paintMatches()
            return true
        end if
        if key = "back"
            leaveSearch()
            return true
        end if
        return key = "right"
    end if
    if m.region = "matches"
        if key = "left" or key = "back"
            m.region = "search"
            m.keyboard.active = true
            paintMatches()
        else if key = "OK"
            openMatch(m.matchStops[m.match])
        end if
        return true
    end if
    if m.region = "list"
        if (key = "right" or key = "OK") and m.stops.count() > 0
            m.region = "rows"
            m.row = 0
            paint()
            return true
        end if
        return false        ' Left opens the rail
    end if
    if key = "left" or key = "back"
        m.region = "list"
        paint()
        return true
    end if
    if key = "OK" then act(m.stops[m.row].act)
    return true
end function

' --- what OK does on a row ------------------------------------------------------------------------

sub act(a as object)
    if a.kind = "switch"
        save({key: a.spec.key}, invalid)
    else if a.kind = "choice"
        spec = a.spec
        if spec.composite <> invalid then now = compositeValue(spec.composite, m.values) else now = m.values[spec.key]
        rows = []
        for i = 0 to spec.choices.count() - 1
            rows.push({id: i.toStr(), text: spec.choices[i][1], current: sameSetting(spec.choices[i][0], now)})
        end for
        m.asking = {kind: "choice", spec: spec}
        help = wordsOrNone(spec.help)
        if spec.sheetHelp <> invalid then help = spec.sheetHelp
        m.top.menu = {eyebrow: eyebrowFor(a), title: spec.label, status: help, rows: rows, hints: [{keys: ["OK"], text: "Choose"}, {keys: ["Back"], text: "Close"}]}
    else if a.kind = "languages"
        m.asking = {kind: "languages", spec: a.spec, eyebrow: eyebrowFor(a)}
        languageSheet(-1)
    else if a.kind = "advanced"
        id = m.spec[m.page].id
        m.advanced[id] = not (m.advanced[id] = true)
        redraw()
    else if a.id = "signout"
        m.top.account = "signout"      ' the scene asks first, as it does from the rail's profile menu
    else if a.id = "reset"
        m.confirm.model = {title: "Reset recommendations?", body: "Portico will forget what it has learned about your taste. Your watch history, ratings and lists stay.", verb: "Reset"}
    else if a.id = "licences"
        openLicences()
    end if
    ' a row that only shows a fact does nothing
end sub

function eyebrowFor(a as object) as string
    if a.section.title <> "" then return a.section.title
    return a.page.title
end function

' The ordered list of a language row (tv-more frame 26): each language with its place, then Add.
sub languageSheet(focusAt as integer)
    spec = m.asking.spec
    list = m.values[spec.key]
    rows = []
    for i = 0 to list.count() - 1
        rows.push({id: "l" + i.toStr(), text: languageName(list[i]), value: (i + 1).toStr(), focus: (i = focusAt)})
    end for
    if list.count() < 8 then rows.push({id: "add", text: "Add a language", gapBefore: true, focus: (focusAt = list.count())})
    m.asking.kind = "languages"
    m.top.menu = {eyebrow: m.asking.eyebrow, title: spec.label, status: wordsOrNone(spec.help), rows: rows, hints: [{keys: ["OK"], text: "Move or remove"}, {keys: ["Back"], text: "Done"}]}
end sub

' Back in a language's own menu, or in the list of languages to add, returns to the ordered list;
' Back in the list itself (or any other sheet) is done.
sub onMenuClosed()
    a = m.asking
    m.asking = invalid
    if a = invalid then return
    if a.kind = "language" or a.kind = "add"
        m.asking = a
        focusAt = m.values[a.spec.key].count()
        if a.kind = "language" then focusAt = a.index
        languageSheet(focusAt)
    end if
end sub

sub onMenuChosen()
    chosen = m.top.menuChosen
    a = m.asking
    if a = invalid then return
    spec = a.spec
    if a.kind = "choice"
        value = spec.choices[chosen.toInt()][0]
        if spec.composite <> invalid then save(compositePatch(spec.composite, value), spec) else save({key: spec.key, value: value}, spec)
        m.asking = invalid
        return
    end if
    list = m.values[spec.key]
    if a.kind = "languages"
        if chosen = "add"
            ' the languages not chosen yet, in the shared order
            rows = []
            for each code in commonLanguages()
                taken = false
                for each have in list
                    if have = code then taken = true
                end for
                if not taken then rows.push({id: code, text: languageName(code)})
            end for
            a.kind = "add"
            m.top.menu = {eyebrow: spec.label, title: "Add a language", status: "", rows: rows, hints: [{keys: ["OK"], text: "Add"}, {keys: ["Back"], text: "Back"}]}
        else
            index = mid(chosen, 2).toInt()
            name = languageName(list[index])
            rows = []
            if index > 0 then rows.push({id: "up", text: "Move " + name + " up"})
            if index < list.count() - 1 then rows.push({id: "down", text: "Move " + name + " down"})
            rows.push({id: "remove", text: "Remove " + name})
            a.kind = "language"
            a.index = index
            m.top.menu = {eyebrow: spec.label, title: name, status: "", rows: rows, hints: [{keys: ["OK"], text: "Choose"}, {keys: ["Back"], text: "Back"}]}
        end if
        return
    end if
    ' a language moved, removed or added: saved at once, and the list opens again on it
    focusAt = list.count()
    if a.kind = "add"
        changed = []
        changed.append(list)
        changed.push(chosen)
        focusAt = list.count()
    else if chosen = "up"
        changed = movedLanguage(list, a.index, -1)
        focusAt = a.index - 1
    else if chosen = "down"
        changed = movedLanguage(list, a.index, 1)
        focusAt = a.index + 1
    else
        changed = withoutLanguage(list, a.index)
        focusAt = a.index
        if focusAt >= changed.count() then focusAt = changed.count()
    end if
    save({key: spec.key, value: changed}, spec)
    languageSheet(focusAt)
end sub

' A change is drawn and sent at once; the server's answer is what stays (or the old value, if it refused).
' `change` is {key, value} for one preference (no value: flip a switch), or a composite's whole patch.
sub save(change as object, spec as dynamic)
    if change.key <> invalid
        value = change.value
        if value = invalid then value = not (m.values[change.key] = true)
        patch = {}
        patch[change.key] = value
    else
        patch = change
    end if
    old = {}
    scope = ""
    for each key in patch
        old[key] = m.values[key]
        m.values[key] = patch[key]
        scope = m.scopes[key]
    end for
    failedKey = ""
    if spec <> invalid then failedKey = rowKey(spec) else failedKey = change.key
    if m.failed = failedKey then m.failed = ""
    redraw()
    api("PATCH", "/v1/preferences", {scope: scope, deviceClass: "television", values: patch}, "onSaved", {old: old, key: failedKey})
end sub

sub onSaved(event as object)
    result = event.getData()
    if result.ok
        took(result.data)
        return
    end if
    ctx = event.getRoSGNode().ctx
    for each key in ctx.old
        m.values[key] = ctx.old[key]
    end for
    m.failed = ctx.key
    redraw()
end sub

sub onConfirmed()
    ' the dialog is inside the screen, so the screen cannot take focus back from it: its list group does
    m.list.setFocus(true)
    paint()
    if m.confirm.chosen <> "confirm" then return
    api("POST", "/v1/me/recommendations:reset", {}, "onReset", invalid, false, createObject("roDeviceInfo").getRandomUUID())
end sub

sub onReset(event as object)
    m.resetDone = event.getData().ok
    redraw()
end sub

' --- Search settings (tv-more frame 34) -------------------------------------------------------------

' Search's frame: the box on the page title's line, the keys under it, the matches from `resultsX`.
sub initSearch()
    c = colours()
    f = frames()
    s = f.search
    m.keyboard = m.top.findNode("keyboard")
    m.keyboard.translation = [f.gutter, s.keysY]
    m.keyboard.observeField("text", "onSearchTyped")
    field = m.top.findNode("box")
    field.blendColor = c.raised
    field.width = s.boxWidth
    field.height = s.boxHeight
    field.translation = [f.gutter, f.browse.titleY]
    glass = m.top.findNode("glass")
    glass.blendColor = c.textTertiary
    glass.translation = [f.gutter + 26, f.browse.titleY + (s.boxHeight - 28) / 2]
    m.query = m.top.findNode("query")
    setType(m.query, "heading", c.text)
    m.query.width = s.boxWidth - 100
    m.query.height = s.boxHeight
    m.query.translation = [f.gutter + 72, f.browse.titleY]
    none = m.top.findNode("none")
    setType(none, "lede", c.textSecondary)
    none.width = f.rightEdge - s.resultsX
    none.translation = [s.resultsX, f.browse.titleY]
    m.matches = m.top.findNode("matches")
    m.top.findNode("matchWindow").clippingRect = [s.resultsX - 40, 0, 1920 - s.resultsX + 40, 1080]
    m.matchStops = []     ' {node, page, label, advanced}: every matching row, top to bottom
    m.match = 0
end sub

sub enterSearch()
    m.region = "search"
    showSearch(true)
    m.keyboard.active = true
    drawMatches()
end sub

' Back from the keys: the Settings list again, on the box.
sub leaveSearch()
    m.region = "find"
    showSearch(false)
    m.list.setFocus(true)     ' the keyboard is inside the screen: the list group takes focus back from it
    paint()
end sub

sub showSearch(on as boolean)
    m.top.findNode("searching").visible = on
    for each id in ["heading", "find", "findGlass", "findWords", "findRing", "group", "listPlate", "listBar", "listRing", "list", "pages", "hintsList", "hintsRows"]
        m.top.findNode(id).visible = not on
    end for
    if on
        m.top.findNode("fadeTop").visible = false
        m.top.findNode("fadeBottom").visible = false
    else
        showPage()
    end if
end sub

sub onSearchTyped()
    m.query.text = m.keyboard.text
    drawMatches()
end sub

' Each page with matches: its name and how many, then its matching rows on one raised card, each
' with its value now, so a setting can be read without opening its page.
sub drawMatches()
    c = colours()
    f = frames()
    s = f.search
    m.matches.removeChildrenIndex(m.matches.getChildCount(), 0)
    m.matchStops = []
    m.match = 0
    width = f.rightEdge - s.resultsX
    none = m.top.findNode("none")
    found = settingsMatches(m.spec, m.keyboard.text)
    none.visible = (found.count() = 0 and m.keyboard.text.trim() <> "")
    none.text = "No settings match “" + m.keyboard.text.trim() + "”."
    y = f.browse.titleY
    for each group in found
        page = m.spec[group.page]
        title = m.matches.createChild("Label")
        title.text = page.title
        title.translation = [s.resultsX, y]
        setType(title, "rowHeading", c.text)
        rows = []
        for each match in group.rows
            for each row in rowsFor(match.row, match.section, page)
                ' a row without help of its own says which part of the page it is in
                if wordsOrNone(row.help) = "" then row.help = match.section.title
                rows.push({row: row, advanced: (match.section.advanced = true)})
            end for
        end for
        if rows.count() = 0 then rows.push({row: {label: "Open " + page.title}, advanced: false})
        count = m.matches.createChild("Label")
        if group.rows.count() = 1 then count.text = "1 match" else count.text = group.rows.count().toStr() + " matches"
        if group.rows.count() = 0 then count.text = ""
        setType(count, "matchCount", c.textTertiary)
        count.translation = [s.resultsX + title.boundingRect().width + 14, y + 8]
        y = y + title.boundingRect().height + 20
        card = m.matches.createChild("Poster")
        card.uri = "pkg:/images/round-14.9.png"
        card.blendColor = c.raised
        card.translation = [s.resultsX, y]
        card.width = width
        top = y
        first = true
        for each entry in rows
            row = entry.row
            item = m.matches.createChild("ListRow")
            item.rowWidth = width
            item.model = {label: row.label, help: wordsOrNone(row.help), scope: wordsOrNone(row.scope), value: wordsOrNone(row.value),
                control: wordsOrNone(row.control), switchOn: (row.switchOn = true), icon: wordsOrNone(row.icon)}
            item.translation = [s.resultsX, y]
            m.matchStops.push({node: item, page: group.page, label: row.label, advanced: entry.advanced, first: first})
            first = false
            y = y + item.rowHeight
        end for
        card.height = y - top
        y = y + f.settings.sectionGap
    end for
    if m.region = "matches" and m.matchStops.count() = 0
        m.region = "search"
        m.keyboard.active = true
    end if
    paintMatches()
end sub

' The match in focus has the row's focus look; the matches move only to keep it on the screen.
sub paintMatches()
    f = frames()
    on = (m.region = "matches" and m.top.isInFocusChain())
    for i = 0 to m.matchStops.count() - 1
        here = m.matchStops[i]
        here.node.on = (on and i = m.match)
        here.node.separator = not here.first and not (on and i - 1 = m.match)
    end for
    scroll = 0
    if on and m.matchStops.count() > 0
        node = m.matchStops[m.match].node
        bottom = node.translation[1] + node.rowHeight
        if bottom > f.settings.foot then scroll = bottom - f.settings.foot
    end if
    m.matches.translation = [0, -scroll]
end sub

' OK on a match: its page opens with that row in focus (an Advanced group opened for it).
sub openMatch(match as object)
    page = m.spec[match.page]
    if match.advanced then m.advanced[page.id] = true
    m.page = match.page
    m.region = "rows"
    showSearch(false)
    redraw()
    for i = 0 to m.stops.count() - 1
        if m.stops[i].node.model.label = match.label then m.row = i
    end for
    if left(match.label, 5) = "Open " and match.label = "Open " + page.title then m.row = 0
    m.list.setFocus(true)
    paint()
end sub
