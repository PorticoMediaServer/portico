sub init()
    c = colours()
    f = frames().signIn
    ' the front door at the gutter (connect.html frame 40); the other pages from the hand-off column
    m.top.findNode("doorMark").translation = [frames().gutter, f.markY]
    m.top.findNode("welcome").translation = [frames().gutter - 4, f.doorTitleY]     ' 4 left: the W's own margin
    m.top.findNode("doorButtons").translation = [frames().gutter, f.doorButtonsY]
    m.top.findNode("doorNote").translation = [frames().gutter, f.doorNoteY]
    for each id in ["codeMark", "factorMark"]
        m.top.findNode(id).translation = [f.markX, f.markY]
    end for
    lines = {eyebrow: f.eyebrowY, how: f.bodyY, codeButtons: f.buttonsY, codeNote: f.noteY, press: f.pressY, factorTitle: f.factorTitleY, factorBody: f.factorBodyY}
    for each id in lines
        m.top.findNode(id).translation = [f.pageX, lines[id]]
    end for
    m.top.findNode("server").translation = [f.pageX - 3, f.titleY]     ' 3 left: the capital's own margin
    ' Use a local server, at the gutter like the front door
    m.top.findNode("localMark").translation = [frames().gutter, f.markY]
    lines = {localTitle: [f.localTitleY, "authTitle", c.text], localBody: [f.localBodyY, "authBody", c.textSecondary],
        nearbyLabel: [f.nearbyLabelY, "groupLabel", c.textSecondary], elsewhereLabel: [f.elsewhereLabelY, "groupLabel", c.textSecondary],
        localNote: [f.localNoteY, "body", c.danger]}
    for each id in lines
        label = m.top.findNode(id)
        label.translation = [frames().gutter, lines[id][0]]
        setType(label, lines[id][1], lines[id][2])
    end for
    m.top.findNode("nearbyRows").translation = [frames().gutter, f.nearbyRowsY]
    m.top.findNode("localButtons").translation = [frames().gutter, f.localButtonsY]
    m.top.findNode("localButtons").buttons = [{id: "type", text: "Type an address", icon: ""}]
    m.top.findNode("localButtons").observeField("pressed", "onDoor")
    m.top.findNode("press").hints = [{before: "Press", keys: ["Back"], text: "to change the server address."}]
    m.door = m.top.findNode("door")
    m.code = m.top.findNode("code")
    m.factor = m.top.findNode("factor")
    m.doorButtons = m.top.findNode("doorButtons")
    m.doorNote = m.top.findNode("doorNote")
    m.localNote = m.top.findNode("localNote")
    m.local = m.top.findNode("local")
    m.nearbyRows = m.top.findNode("nearbyRows")
    m.codeButtons = m.top.findNode("codeButtons")
    m.codeNote = m.top.findNode("codeNote")
    m.factorPad = m.top.findNode("factorPad")
    m.poll = m.top.findNode("poll")
    m.card = m.top.findNode("card")
    m.card.translation = [f.cardX, f.cardY]
    m.factorPad.translation = [f.keysX, f.keysY]
    m.factorPad.boxesAt = [f.pageX - f.keysX, f.factorBoxesY - f.keysY]
    setType(m.top.findNode("welcome"), "welcome", c.text)
    setType(m.doorNote, "body", c.danger)
    setType(m.top.findNode("eyebrow"), "eyebrow", c.accentStrong)
    setType(m.top.findNode("server"), "authTitle", c.text)
    setType(m.codeNote, "body", c.danger)
    setType(m.top.findNode("factorTitle"), "pageTitle", c.text)
    setType(m.top.findNode("factorBody"), "lede", c.textSecondary)
    ' the steps (auth.html .tvl .lede): 26 px, the names to look for in bold and brighter
    how = m.top.findNode("how")
    body = typeRoles().authBody
    how.lineSpacing = body[2] - body[1] * 1.25
    how.drawingStyles = {
        "default": {fontUri: "pkg:/fonts/manrope-400.ttf", fontSize: body[1], color: "#" + mid(c.textSecondary, 3)},
        "b": {fontUri: "pkg:/fonts/manrope-700.ttf", fontSize: body[1], color: "#" + mid(c.text, 3)}
    }
    ' "Sign in with Portico Account" joins this row when that flow is built (the next change).
    m.doorButtons.buttons = [{id: "local", text: "Use a local server", icon: ""}]
    m.doorButtons.observeField("pressed", "showLocal")
    m.codeButtons.observeField("pressed", "onCodeButton")
    m.factorPad.observeField("entered", "onFactor")
    m.poll.observeField("fire", "onPoll")
    m.stage = "door"
    m.typed = ""
    m.expectedId = ""      ' a found server's id, which its /v1/system must name
    m.nearby = []
    m.listed = []          ' the found servers drawn, in their order
    m.nearbyAt = -1        ' the found server in focus, or -1 for Type an address
    m.step = 0
end sub

sub onNotice()
    if m.top.notice <> ""
        m.doorNote.color = colours().textSecondary
        m.doorNote.text = m.top.notice
    end if
    if m.top.address <> "" and m.typed = "" then m.typed = m.top.address
end sub

sub onActive()
    if not m.top.active then return
    if m.stage = "code"
        m.codeButtons.active = true
    else if m.stage = "factor"
        m.factorPad.active = true
    else if m.stage = "local"
        paintNearby()
    else
        m.doorButtons.active = true
    end if
end sub

sub go(stage as string)
    m.stage = stage
    m.step = m.step + 1
    m.door.visible = (stage = "door")
    m.local.visible = (stage = "local")
    m.code.visible = (stage = "code")
    m.factor.visible = (stage = "factor")
    if stage <> "code" then m.poll.control = "stop"
    onActive()
end sub

' --- one sign-in, one server ----------------------------------------------------------------
' m.step counts every move of this screen: to another address, or to another page. A request is
' sent to the server being signed in to at that moment and carries the step, and its reply is
' taken only while the step is still the same. So an answer that arrives after the viewer has
' gone back or chosen another server is dropped: a sign-in one server gave is never kept under
' another server's address.

sub ask(method as string, path as string, body as dynamic, callback as string, code = "" as string, origin = "" as string, more = {} as object)
    if origin = "" then origin = m.origin
    ctx = {step: m.step, origin: origin, code: code}
    ctx.append(more)
    api(method, path, body, callback, ctx, true, "", origin)
end sub

function late(event as object) as boolean
    return event.getRoSGNode().ctx.step <> m.step
end function

' --- Roku's keyboard, for an address, a name or a password ---------------------------------

sub typeIn(title as string, message as string, text as string, secret as boolean, after as string)
    dialog = createObject("roSGNode", "StandardKeyboardDialog")
    dialog.title = title
    dialog.message = [message]
    dialog.buttons = ["OK", "Cancel"]
    dialog.text = text
    if secret then dialog.textEditBox.secureMode = true
    dialog.observeField("buttonSelected", "onAnswered")
    dialog.observeField("wasClosed", "onAskClosed")
    m.after = after
    m.dialog = dialog
    m.top.getScene().dialog = dialog
end sub

sub onAnswered()
    dialog = m.dialog
    if dialog = invalid then return
    text = dialog.text
    ok = (dialog.buttonSelected = 0)
    after = m.after
    m.after = ""
    dialog.close = true
    if not ok then return
    if after = "address"
        tryAddress(text)
    else if after = "username"
        m.username = text.trim()
        if m.username <> "" then typeIn("Password", "The password for " + m.username + " on " + m.system.name + ".", "", true, "password")
    else if after = "password"
        signInWithPassword(text)
    end if
end sub

sub onAskClosed(event as object)
    ' the dialog that closed may be an earlier one (the username's, with the password's already open)
    if m.dialog <> invalid and not event.getRoSGNode().isSameNode(m.dialog) then return
    m.dialog = invalid
    onActive()
end sub

' --- the front door and the server's address ------------------------------------------------

' --- Use a local server: servers found on this network, then a typed address ---------------------

sub showLocal()
    m.doorNote.text = ""
    go("local")
    m.localNote.text = ""
    m.nearbyAt = -1
    ' servers found earlier stay listed while this page is open (Back to it asks again)
    if m.nearby = invalid then m.nearby = []
    kept = []
    for each entry in m.nearby
        if entry.origin <> "" then kept.push(entry)
    end for
    m.nearby = kept
    drawNearby()
    if m.nearby.count() = 0 then m.top.findNode("nearbyLabel").text = "Looking on this network…"
    ' the server this TV last signed in to is asked first, held to the id it had then
    lastOrigin = storeRead("origin")
    lastId = storeRead("serverId")
    if lastOrigin <> "" and lastId <> "" and nearbyIndex(lastId) < 0
        m.nearby.push({id: lastId, ip: "", last: true, candidates: [lastOrigin], origin: ""})
        checkNearby(m.nearby.count() - 1)
    end if
    discover()
end sub

' One round of asking the network (DiscoveryTask); another starts when a round ends with nothing listed.
sub discover()
    m.discovery = createObject("roSGNode", "DiscoveryTask")
    m.discovery.observeField("found", "onFound")
    m.discovery.observeField("done", "onDiscoveryDone")
    m.discovery.control = "run"
end sub

sub onDiscoveryDone(event as object)
    if m.stage <> "local" or not event.getRoSGNode().isSameNode(m.discovery) then return
    if m.listed.count() = 0
        m.top.findNode("nearbyLabel").text = "No Portico server has answered on this network yet. Still looking…"
        discover()
    end if
end sub

function nearbyIndex(id as string) as integer
    for i = 0 to m.nearby.count() - 1
        if m.nearby[i].id = id then return i
    end for
    return -1
end function

' Each answer is only a hint: the server is asked at its own address, and listed under the name it
' gives there only when it names the same id.
sub onFound(event as object)
    if m.stage <> "local" or not event.getRoSGNode().isSameNode(m.discovery) then return
    ' each answer as it arrives; a server already listed (or being asked) is not asked again
    for each record in event.getData()
        if nearbyIndex(record.id) < 0
            m.nearby.push({id: record.id, ip: record.ip, candidates: addressCandidates(record.ip + ":" + record.port.toStr()), origin: ""})
            checkNearby(m.nearby.count() - 1)
        end if
    end for
end sub

sub checkNearby(index as integer)
    entry = m.nearby[index]
    if entry.candidates.count() = 0 then return      ' it did not answer as itself at its address: not listed
    ask("GET", "/v1/system", invalid, "onNearbySystem", "", entry.candidates.shift(), {index: index})
end sub

sub onNearbySystem(event as object)
    if late(event) then return
    ctx = event.getRoSGNode().ctx
    result = event.getData()
    entry = m.nearby[ctx.index]
    if result.ok and result.data <> invalid and result.data.id = entry.id
        entry.origin = ctx.origin
        entry.name = result.data.name
        entry.setupRequired = (result.data.setupRequired = true)
        drawNearby()
    else
        checkNearby(ctx.index)
    end if
end sub

sub drawNearby()
    m.nearbyRows.removeChildrenIndex(m.nearbyRows.getChildCount(), 0)
    m.listed = []
    y = 0
    for each entry in m.nearby
        if entry.origin <> "" and m.listed.count() < 4      ' four rows fit above Somewhere else
            row = m.nearbyRows.createChild("ListRow")
            help = entry.ip
            if entry.last = true then help = "Last used on this TV"
            if entry.setupRequired then help = "Nobody has set it up yet"
            row.model = {label: entry.name, help: help, scope: "", value: "", control: "", switchOn: false, icon: "server", danger: false}
            row.translation = [0, y]
            row.separator = (y > 0)
            y = y + row.rowHeight
            m.listed.push(entry)
        end if
    end for
    m.top.findNode("nearbyLabel").text = "On this network"
    paintNearby()
end sub

' Focus starts on Type an address; servers that turn up appear above it and never take the focus.
sub paintNearby()
    for i = 0 to m.nearbyRows.getChildCount() - 1
        m.nearbyRows.getChild(i).on = (i = m.nearbyAt)
    end for
    if m.nearbyAt >= 0 then m.top.setFocus(true) else m.top.findNode("localButtons").active = true
end sub

' A found server goes on exactly as a typed address does, held to the id it announced.
sub openNearby(entry as object)
    m.localNote.color = colours().danger
    if entry.setupRequired
        m.localNote.text = "Nobody has set up " + entry.name + " yet. Set it up from a phone or a computer, then come back here."
        return
    end if
    m.expectedId = entry.id
    m.candidates = [entry.origin]
    m.localNote.color = colours().textSecondary
    m.localNote.text = "Looking for the server…"
    tryNext()
end sub

sub onDoor()
    m.localNote.text = ""
    typeIn("Server address", "The address of your Portico server, such as 192.168.1.20 or media.example.com.", m.typed, false, "address")
end sub

sub tryAddress(typed as string)
    m.typed = typed
    m.expectedId = ""
    m.candidates = addressCandidates(typed)
    if m.candidates.count() = 0
        m.localNote.text = "Enter a server address."
        return
    end if
    m.localNote.color = colours().textSecondary
    m.localNote.text = "Looking for the server…"
    tryNext()
end sub

sub tryNext()
    if m.candidates.count() = 0
        m.localNote.color = colours().danger
        m.localNote.text = "No Portico server answered at that address. Check the address, and that this TV is on the same network as the server."
        return
    end if
    m.origin = m.candidates.shift()
    m.step = m.step + 1
    ask("GET", "/v1/system", invalid, "onSystem")
end sub

sub onSystem(event as object)
    result = event.getData()
    if late(event) then return
    ' a found server must answer as the server that announced itself
    if not result.ok or result.data = invalid or result.data.id = invalid or (m.expectedId <> "" and result.data.id <> m.expectedId)
        tryNext()
        return
    end if
    m.system = result.data
    m.localNote.color = colours().danger
    if m.system.setupRequired = true
        m.localNote.text = m.system.name + " has not been set up yet. Set it up from a phone or a computer, then come back here."
        return
    end if
    m.localNote.text = ""
    showCode()
end sub

' --- the code a phone or computer approves ---------------------------------------------------

function thisDevice() as object
    info = createObject("roDeviceInfo")
    app = createObject("roAppInfo")
    return {installationId: installationId(), name: info.getFriendlyName(), platform: "roku", app: "portico-roku", appVersion: app.getVersion()}
end function

sub showCode()
    shown = addressShown(m.origin)
    m.top.findNode("server").text = m.system.name
    m.top.findNode("how").text = "On a phone signed in to " + m.system.name + ", open <b>Portico › Settings › Link a TV</b>, or on a computer open <b>" + shown + "/device</b>."
    m.card.address = shown + "/device"
    m.card.code = ""
    m.card.link = ""
    m.codeNote.text = ""
    buttons = []
    for each method in m.system.signInMethods
        if method = "password" then buttons.push({id: "password", text: "Use a password instead", icon: ""})
    end for
    m.codeButtons.buttons = buttons
    go("code")
    newCode()
end sub

sub newCode()
    m.poll.control = "stop"
    ask("POST", "/v1/auth/codes", {device: thisDevice()}, "onCode")
end sub

sub onCode(event as object)
    result = event.getData()
    if late(event) then return
    if not result.ok
        m.codeNote.text = result.error.message
        m.pending = invalid
        m.poll.duration = 10
        m.poll.control = "start"   ' try for a code again shortly
        return
    end if
    m.pending = result.data
    if createObject("roAppInfo").isDev() then ? "PORTICO code "; m.pending.code
    m.card.code = m.pending.code
    m.card.link = m.origin + "/device#code=" + m.pending.code
    seconds = 3
    if m.pending.intervalMs <> invalid then seconds = m.pending.intervalMs / 1000
    if seconds < 1 then seconds = 1
    m.poll.duration = seconds
    m.poll.control = "start"
end sub

sub onPoll()
    if m.stage <> "code" then return
    if m.pending = invalid
        newCode()
    else
        ask("POST", "/v1/auth/codes/" + m.pending.code + "/redeem", {deviceSecret: m.pending.deviceSecret}, "onRedeem", m.pending.code)
    end if
end sub

sub onRedeem(event as object)
    result = event.getData()
    if late(event) or m.pending = invalid or event.getRoSGNode().ctx.code <> m.pending.code then return
    if result.ok and result.data.status = "approved" and result.data.signIn <> invalid
        finish(event, result.data.signIn)
    else if result.ok or result.status = 0
        m.poll.control = "start"   ' still waiting (or the server was out of reach for a moment)
    else
        ' The code ran out or was refused: say so, and show a new one.
        if result.error.code = "sign_in_denied" then m.codeNote.text = "That sign-in was not approved. Here is a new code." else m.codeNote.text = ""
        newCode()
    end if
end sub

sub onCodeButton(event as object)
    if event.getData() = "password"
        m.codeNote.text = ""
        typeIn("Username", "Your username on " + m.system.name + ".", "", false, "username")
    else
        showLocal()
    end if
end sub

' --- a password, and the two-step code after it ---------------------------------------------

sub signInWithPassword(password as string)
    ask("POST", "/v1/auth/sign-in", {username: m.username, password: password, device: thisDevice()}, "onPasswordAnswer")
end sub

sub onPasswordAnswer(event as object)
    result = event.getData()
    if late(event) then return
    if not result.ok
        m.codeNote.text = result.error.message
        return
    end if
    if result.data.challenge <> invalid
        m.challenge = result.data.challenge.token
        m.factorPad.digits = 6     ' a fresh pad: digits typed for an earlier attempt are gone
        go("factor")
        return
    end if
    finish(event, result.data)
end sub

sub onFactor(event as object)
    ask("POST", "/v1/auth/two-factor", {challenge: m.challenge, code: event.getData()}, "onFactorAnswer")
end sub

sub onFactorAnswer(event as object)
    result = event.getData()
    if late(event) then return
    if result.ok and result.data.session <> invalid
        finish(event, result.data)
    else if result.error.code = "two_factor_invalid"
        m.factorPad.wrong = true
    else
        ' The waiting sign-in ran out or ended (or the server is out of reach): no code can work
        ' now, so back to the code screen with the reason.
        go("code")
        m.codeNote.text = result.error.message
        newCode()
    end if
end sub

' The sign-in is kept under the address its own request was sent to.
sub finish(event as object, signIn as object)
    m.poll.control = "stop"
    if signIn.session = invalid then return
    if signIn.session.twoStepSetupRequired = true
        ' The server refuses everything from this sign-in until two-step sign-in is set up, and a
        ' television cannot set it up: say so here rather than fail at the next screen.
        go("code")
        m.codeNote.text = "Set up two-step sign-in for this account on a phone or computer, then sign in here again."
        newCode()
        return
    end if
    profiles = []
    if signIn.profiles <> invalid then profiles = signIn.profiles
    m.top.signedIn = {origin: event.getRoSGNode().ctx.origin, serverName: m.system.name, serverId: m.system.id, session: signIn.session, profiles: profiles}
end sub

function onKeyEvent(key as string, press as boolean) as boolean
    if not press then return false
    if key = "back"
        if m.stage = "code"
            showLocal()      ' the step before: Use a local server
            return true
        else if m.stage = "local"
            go("door")
            return true
        else if m.stage = "factor"
            go("code")
            newCode()
            return true
        end if
        return false   ' Back at the front door leaves the app
    end if
    if m.stage = "local"
        count = m.nearbyRows.getChildCount()
        if key = "up" and m.nearbyAt = -1 and count > 0
            m.nearbyAt = count - 1
        else if key = "up" and m.nearbyAt > 0
            m.nearbyAt = m.nearbyAt - 1
        else if key = "down" and m.nearbyAt >= 0
            m.nearbyAt = m.nearbyAt + 1
            if m.nearbyAt >= count then m.nearbyAt = -1
        else if key = "OK" and m.nearbyAt >= 0
            openNearby(m.listed[m.nearbyAt])
            return true
        end if
        paintNearby()
    end if
    ' nothing lies to the left of these pages: the edge stops
    return key = "left" or key = "up" or key = "down"
end function
