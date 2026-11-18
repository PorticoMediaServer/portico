sub init()
    c = colours()
    f = frames().signIn
    m.code = m.top.findNode("code")
    m.list = m.top.findNode("list")
    m.state = m.top.findNode("state")
    m.page = m.top.findNode("page")
    m.card = m.top.findNode("card")
    m.rows = m.top.findNode("rows")
    m.codeButtons = m.top.findNode("codeButtons")
    m.listButtons = m.top.findNode("listButtons")
    m.codeNote = m.top.findNode("codeNote")
    m.poll = m.top.findNode("poll")
    m.again = m.top.findNode("again")
    m.tick = m.top.findNode("tick")
    ' the code page as auth-01: words from the hand-off column, the card on the right
    m.top.findNode("codeMark").translation = [f.markX, f.markY]
    lines = {codeTitle: f.activateTitleBottom - 240, codeHow: f.activateBodyY, codeButtons: f.activateButtonsY, codeNote: f.noteY, terms: f.termsY}
    for each id in lines
        m.top.findNode(id).translation = [f.pageX, lines[id]]
    end for
    m.card.translation = [f.cardX, f.cardY]
    ' the whole width up to the card, so it keeps to one line as auth-01 draws it; if it must wrap,
    ' its lines sit as close as the mock-up's (Roku's own gap between 72 px lines is loose)
    setType(m.top.findNode("codeTitle"), "authTitle", c.text)
    m.top.findNode("codeTitle").lineSpacing = -20
    setType(m.codeNote, "body", c.danger)
    setType(m.top.findNode("terms"), "caption", c.textTertiary)
    how = m.top.findNode("codeHow")
    body = typeRoles().authBody
    how.lineSpacing = body[2] - body[1] * 1.25
    how.drawingStyles = {
        "default": {fontUri: "pkg:/fonts/manrope-400.ttf", fontSize: body[1], color: "#" + mid(c.textSecondary, 3)},
        "b": {fontUri: "pkg:/fonts/manrope-700.ttf", fontSize: body[1], color: "#" + mid(c.text, 3)}
    }
    ' auth.html frame 8: Sign in on this TV (the account typed with the remote) is the main button
    m.codeButtons.buttons = [{id: "onTV", text: "Sign in on this TV", icon: ""}, {id: "back", text: "Back", icon: ""}]
    m.codeButtons.observeField("pressed", "onButton")
    ' the chooser as connect-01, at the gutter
    x = frames().gutter
    m.top.findNode("listMark").translation = [x, f.markY]
    m.top.findNode("stateMark").translation = [x, f.markY]
    lines = {signedInAs: f.chooserEyebrowY, listTitle: f.chooserTitleY, listBody: f.chooserBodyY, window: f.rowsY, listButtons: f.chooserButtonsY}
    for each id in lines
        m.top.findNode(id).translation = [x, lines[id]]
    end for
    setType(m.top.findNode("signedInAs"), "eyebrow", c.accentStrong)
    setType(m.top.findNode("listTitle"), "authTitle", c.text)
    setType(m.top.findNode("listBody"), "authBody", c.textSecondary)
    ' the rows show inside their room above the buttons, with space for the focus ring
    m.top.findNode("window").clippingRect = [-60, -40, 1220, f.rowsHeight + 50]
    m.listButtons.buttons = [{id: "refresh", text: "Refresh servers", icon: "refresh"}, {id: "local", text: "Use a local server", icon: ""}]
    m.listButtons.observeField("pressed", "onButton")
    m.page.observeField("pressed", "onButton")
    m.joinPages = m.top.findNode("joinPages")
    m.joinPages.observeField("pressed", "onJoinPressed")
    m.joinPages.observeField("submitted", "onJoinSubmitted")
    for each part in [["listCount", f.chooserCountY, "caption", c.textSecondary], ["listHelp", f.chooserHelpY, "caption", c.textTertiary]]
        label = m.top.findNode(part[0])
        label.translation = [x, part[1]]
        setType(label, part[2], part[3])
    end for
    m.poll.observeField("fire", "onPoll")
    m.again.observeField("fire", "onAgain")
    m.tick.observeField("fire", "onTick")
    m.stage = ""
    m.step = 0
    m.servers = []
    m.focus = 0
    m.inRows = true
end sub

sub onActive()
    if not m.top.active then return
    if m.stage = "code"
        m.codeButtons.active = true
    else if m.stage = "list"
        focusList()
    else if m.stage = "start" or m.stage = "join"
        m.joinPages.active = true
    else if m.stage <> ""
        m.page.active = true
    end if
end sub

sub go(stage as string)
    m.stage = stage
    m.step = m.step + 1
    m.code.visible = (stage = "code")
    m.list.visible = (stage = "list")
    m.joinPages.visible = (stage = "start" or stage = "join")
    m.state.visible = not (m.code.visible or m.list.visible or m.joinPages.visible)
    m.poll.control = "stop"
    m.again.control = "stop"
    m.tick.control = "stop"
    onActive()
end sub

' --- one request, one service or server -----------------------------------------------------
' As on SignInScreen: m.step counts every move of these pages, every request carries the step and
' the address it was sent to, and an answer is taken only while the step is still the same. So an
' answer that arrives after the viewer has moved on (another code, another server, Back) is dropped,
' and a sign-in a server gave is kept only under that server's own address.

sub ask(method as string, origin as string, path as string, body as dynamic, callback as string, more = {} as object, key = "" as string, token = "" as string)
    ctx = {step: m.step, origin: origin}
    ctx.append(more)
    api(method, path, body, callback, ctx, true, key, origin, "", token)
end sub

function late(event as object) as boolean
    return event.getRoSGNode().ctx.step <> m.step
end function

' Services' sign-in for this TV, for the calls that need it.
function accountToken() as string
    if m.tokens = invalid then return ""
    return m.tokens.accessToken
end function

' --- the code (POST /v1/tv-codes, then /redeem until it is approved) --------------------------

sub start()
    m.tokens = invalid
    m.wanted = invalid
    showCode()
end sub

' Switch server's way in: the account's kept sign-in is renewed (POST /v1/sessions/refresh), then
' the list is read as after a code, and the wanted server opens once it has been checked.
sub resume(serverId as string)
    m.tokens = invalid
    m.wanted = serverId
    m.username = storeRead("porticoUser")
    go("list")
    m.rows.removeChildrenIndex(m.rows.getChildCount(), 0)
    m.listButtons.visible = false
    showWait("Looking for your servers…")
    ask("POST", services(), "/v1/sessions/refresh", {refreshToken: storeRead("porticoRefresh")}, "onRenewed")
end sub

sub onRenewed(event as object)
    if late(event) then return
    result = event.getData()
    if result.ok and result.data <> invalid and result.data.accessToken <> invalid
        m.tokens = result.data
        if result.data.refreshToken <> invalid then storeWrite({porticoRefresh: result.data.refreshToken})
        loadServers()
    else if result.status = 401 or result.status = 400
        showCode()     ' the kept sign-in has ended: a new code
    else
        showState("failed")
    end if
end sub

sub showCode()
    go("code")
    m.top.findNode("codeHow").text = "Scan the QR code, or visit <b>web.getportico.tv/device</b> and enter the code. Sign in with Google, Apple, or your Portico Account in the browser."
    m.codeNote.text = ""
    newCode()
end sub

' A new code each time: a secret only this TV knows, whose digest Services keeps, so only this TV
' can collect the sign-in the code is approved for.
sub newCode()
    m.poll.control = "stop"
    m.tv = invalid
    m.card.code = ""
    m.card.link = ""
    m.card.address = "web.getportico.tv/device"
    m.secret = secretText()
    body = {device: {name: "Portico on Roku", kind: "tv"}, secretHash: digestText(m.secret)}
    ask("POST", services(), "/v1/tv-codes", body, "onCode", {}, createObject("roDeviceInfo").getRandomUUID())
end sub

sub onCode(event as object)
    if late(event) then return
    result = event.getData()
    if not result.ok or result.data = invalid or result.data.id = invalid
        m.codeNote.text = "Portico Accounts aren’t answering right now. A new code comes by itself."
        m.poll.duration = 10
        m.poll.control = "start"
        return
    end if
    m.codeNote.text = ""
    m.tv = {id: result.data.id, code: result.data.code, secret: m.secret}
    m.card.code = result.data.code
    m.card.link = result.data.verifyUrlWithCode
    m.card.address = printed(result.data.verifyUrl)
    waitFor(result.data.pollAfterMs)
end sub

sub waitFor(ms as dynamic)
    seconds = 5
    if ms <> invalid then seconds = ms / 1000
    if seconds < 1 then seconds = 1
    m.poll.duration = seconds
    m.poll.control = "start"
end sub

sub onPoll()
    if m.stage <> "code" then return
    if m.tv = invalid
        newCode()
    else
        ask("POST", services(), "/v1/tv-codes/" + m.tv.id + "/redeem", {secret: m.tv.secret}, "onRedeem", {code: m.tv.id})
    end if
end sub

sub onRedeem(event as object)
    if late(event) or m.tv = invalid or event.getRoSGNode().ctx.code <> m.tv.id then return
    result = event.getData()
    if result.ok and result.data.state = "approved" and result.data.tokens <> invalid
        m.tokens = result.data.tokens
        m.tv = invalid
        ask("GET", services(), "/v1/account", invalid, "onAccount", {}, "", accountToken())
    else if result.ok
        waitFor(result.data.pollAfterMs)     ' not approved yet
    else if result.status = 0 or result.status >= 500
        waitFor(5000)     ' Services out of reach for a moment: the same code, asked again
    else
        ' the code ran out or was turned down: say so, and show a new one
        if result.error.code = "tv_code_denied" then m.codeNote.text = "That sign-in was not approved. Here is a new code." else m.codeNote.text = ""
        newCode()
    end if
end sub

' Sign in on this TV: Services answers with the sign-in, or asks for the two-step code first.
sub onSignedInHere(event as object)
    if late(event) then return
    result = event.getData()
    if result.ok and result.data <> invalid and result.data.tokens <> invalid
        m.tokens = result.data.tokens
        m.tv = invalid
        ask("GET", services(), "/v1/account", invalid, "onAccount", {}, "", accountToken())
    else if result.ok and result.data <> invalid and result.data.twoStep <> invalid
        m.challengeId = result.data.twoStep.challengeId
        typeIn("Two-step code", "The code from your authenticator app for your Portico Account.", "", false, "twoStep")
    else if result.status = 0 or result.status >= 500
        m.codeNote.text = "Portico Accounts aren’t answering right now. Try again in a moment."
    else
        m.codeNote.text = "That didn’t work. Check your username or email and the password, then try again."
    end if
end sub

sub onAccount(event as object)
    if late(event) then return
    result = event.getData()
    m.username = ""
    if result.ok and result.data <> invalid and result.data.username <> invalid then m.username = result.data.username
    ' the server list must be this account's (ApiTask checks it against this)
    if result.ok and result.data <> invalid and result.data.id <> invalid then storeWrite({porticoAccountId: result.data.id})
    loadServers()
end sub

' --- the account's servers (GET /v1/servers), each checked at its own addresses ---------------

sub loadServers()
    go("list")
    m.top.findNode("signedInAs").text = "Signed in as " + m.username
    m.rows.removeChildrenIndex(m.rows.getChildCount(), 0)
    m.listButtons.visible = false
    showWait("Looking for your servers…")
    ask("GET", services(), "/v1/servers?signature=rsa", invalid, "onServers", {accountId: storeRead("porticoAccountId")}, "", accountToken())
end sub

sub onServers(event as object)
    if late(event) then return
    result = event.getData()
    if result.status = 401
        showCode()     ' the account's sign-in on this TV ended: a new code
        return
    end if
    if not result.ok or result.data = invalid or result.data.list = invalid
        showState("failed")
        return
    end if
    listed = listedServers(result.data.list.payload)
    if listed.count() = 0
        showStart(invalid)
        return
    end if
    m.servers = []
    for each server in listed
        m.servers.push({id: server.serverId, name: server.name, owner: server.owner = true, addresses: server.addresses, state: "checking", origin: ""})
    end for
    for i = 0 to m.servers.count() - 1
        probe(i, 0, false)
    end for
end sub

' A server is reached at the first of its addresses that answers as that server (its /v1/system
' names the same id); `opening` goes on to sign in there.
sub probe(index as integer, attempt as integer, opening as boolean)
    server = m.servers[index]
    if server.addresses = invalid or attempt >= server.addresses.count()
        server.state = "offline"
        if opening then showState("waiting") else checked()
        return
    end if
    origin = server.addresses[attempt]
    ask("GET", origin, "/v1/system", invalid, "onSystem", {index: index, attempt: attempt, opening: opening})
end sub

sub onSystem(event as object)
    if late(event) then return
    ctx = event.getRoSGNode().ctx
    result = event.getData()
    server = m.servers[ctx.index]
    if result.ok and result.data <> invalid and result.data.id = server.id
        server.state = "online"
        server.origin = ctx.origin
        if ctx.opening then askChallenge() else checked()
    else
        probe(ctx.index, ctx.attempt + 1, ctx.opening)
    end if
end sub

' Once every server has answered or not: online first, and focus on the first one (connect-01).
sub checked()
    for each server in m.servers
        if server.state = "checking" then return
    end for
    ordered = []
    for each want in ["online", "offline"]
        for i = 0 to m.servers.count() - 1
            if m.servers[i].state = want then ordered.push(m.servers[i])
        end for
    end for
    m.servers = ordered
    reopen = (m.allOffline = true)
    m.allOffline = (m.servers.count() > 0 and m.servers[0].state = "offline")
    drawList()
    ' every server away (connect.html frame 37): the list says so and checks again by itself; the
    ' first one to answer opens at once
    if m.allOffline
        m.top.findNode("signedInAs").text = "Your Portico Account is signed in"
        m.top.findNode("listTitle").text = "Your servers aren’t answering"
        showWait("Portico keeps checking and opens one the moment it answers.")
        m.top.findNode("listHelp").text = "If one of these is your own server, check that its computer is on and connected. Your Portico Account is still signed in."
        countDown()
        return
    end if
    m.top.findNode("listTitle").text = "Choose a server"
    m.top.findNode("listCount").text = ""
    m.top.findNode("listHelp").text = ""
    if reopen
        m.focus = 0
        openServer()
        return
    end if
    ' from Switch server: the chosen server opens at once; Back from its pages returns to this list
    if m.wanted <> invalid
        for i = 0 to m.servers.count() - 1
            if m.servers[i].id = m.wanted then m.focus = i
        end for
        found = (m.servers[m.focus].id = m.wanted)
        m.wanted = invalid
        if found then openServer()
    end if
end sub

sub drawList()
    m.top.findNode("signedInAs").text = "Signed in as " + m.username
    showWait("")
    m.rows.removeChildrenIndex(m.rows.getChildCount(), 0)
    y = 0
    for each server in m.servers
        row = m.rows.createChild("ListRow")
        help = "Online"
        if server.state = "offline" then help = "Not answering. OK tries again."
        value = ""
        if server.owner then value = "Owner"
        row.model = {label: server.name, help: help, scope: "", value: value, control: "", switchOn: false, icon: "server", danger: false}
        row.translation = [0, y]
        row.separator = (y > 0)
        y = y + row.rowHeight
    end for
    m.listButtons.visible = true
    m.focus = 0
    m.inRows = true
    focusList()
end sub

sub showWait(text as string)
    body = m.top.findNode("listBody")
    if text = "" then body.text = "Choose the server to watch from." else body.text = text
end sub

sub focusList()
    count = m.rows.getChildCount()
    if count = 0 and not m.listButtons.visible
        m.top.findNode("rowStop").setFocus(true)     ' still looking: nothing to press yet
        return
    end if
    if count = 0 then m.inRows = false
    for i = 0 to count - 1
        m.rows.getChild(i).on = (m.inRows and i = m.focus)
    end for
    ' the row in focus stays inside the rows' room above the buttons
    if count > 0
        top = m.rows.getChild(m.focus).translation[1]
        bottom = top + m.rows.getChild(m.focus).rowHeight
        shift = m.rows.translation[1]
        if top + shift < 0 then shift = -top
        if bottom + shift > frames().signIn.rowsHeight then shift = frames().signIn.rowsHeight - bottom
        m.rows.translation = [0, shift]
    end if
    if m.inRows then m.top.findNode("rowStop").setFocus(true) else m.listButtons.active = true
end sub

' --- opening a server: its challenge, a note for it from Services, the note back to it -------

sub openServer()
    m.invitation = invalid
    m.server = m.servers[m.focus]
    showState("opening")
    probe(m.focus, 0, true)
end sub

sub askChallenge()
    ask("POST", m.server.origin, "/v1/auth/portico/challenge", {}, "onChallenge")
end sub

sub onChallenge(event as object)
    if late(event) then return
    result = event.getData()
    if result.status = 0
        showState("waiting")
    else if not result.ok
        showState("cant", result.error.message)
    else if result.data.serverId <> m.server.id
        showState("cant", "This server could not prove it is the server you chose. Nothing was sent to it.")
    else
        m.challenge = result.data.challenge
        ask("POST", services(), "/v1/servers/" + m.server.id + "/identity", {challenge: m.challenge}, "onIdentity", {}, "", accountToken())
    end if
end sub

sub onIdentity(event as object)
    if late(event) then return
    result = event.getData()
    if result.ok and result.data <> invalid and result.data.identity <> invalid
        m.note = result.data.note
        body = {challenge: m.challenge, identity: result.data.identity, device: thisDevice()}
        if m.invitation <> invalid then body.invitation = m.invitation      ' joining with a code (frame 36)
        ask("POST", m.server.origin, "/v1/auth/portico/exchange", body, "onExchange")
    else if result.status = 401
        showCode()
    else if result.status = 0 or result.status >= 500
        showState("accounts")
    else
        showState("cant", "Portico Accounts could not sign you in to " + m.server.name + ". Try again.")
    end if
end sub

sub onExchange(event as object)
    if late(event) then return
    result = event.getData()
    if result.ok and result.data <> invalid and result.data.session <> invalid
        session = result.data.session
        origin = event.getRoSGNode().ctx.origin
        ' a note for a server that is behind is carried there; it never holds up the sign-in
        if m.note <> invalid then api("POST", "/v1/hosted/notices", {note: m.note}, "onNoticeSent", invalid, true, "", origin, "", session.accessToken)
        profiles = []
        if result.data.profiles <> invalid then profiles = result.data.profiles
        m.stage = ""
        m.step = m.step + 1
        ' the account's sign-in on this TV, kept for Switch server (a renewal may not have changed it)
        kept = m.tokens.refreshToken
        if kept = invalid then kept = storeRead("porticoRefresh")
        m.top.signedIn = {origin: origin, serverName: m.server.name, serverId: m.server.id, session: session, profiles: profiles,
            account: {refreshToken: kept, username: m.username}}
    else if m.invitation <> invalid
        backToJoin(result.error.message)
    else if result.status = 0
        showState("waiting")
    else if (result.error.code = "not_a_member" or result.error.code = "left_server" or result.error.code = "account_off") and m.servers.count() <= 1
        ' the account's only server let it go (connect.html frame 38): the start page, with what happened
        showStart({title: "You no longer have access to " + m.server.name + ".", body: result.error.message})
    else if result.error.code = "not_a_member" or result.error.code = "left_server" or result.error.code = "account_off"
        showState("refused", result.error.message)
    else
        showState("cant", result.error.message)
    end if
end sub

' --- no server yet: the two tiles, and joining with an invitation code (frames 35, 36, 38) ----------

sub showStart(notice as dynamic)
    m.notice = notice
    go("start")
    m.joinPages.notice = notice
    m.joinPages.page = "start"
    m.joinPages.active = true
    ' "this TV finds the server once you've joined": the account's list is read again each minute
    m.again.duration = 60
    m.again.control = "start"
end sub

sub onStartServers(event as object)
    if late(event) then return
    result = event.getData()
    if result.ok and result.data <> invalid and result.data.list <> invalid and listedServers(result.data.list.payload).count() > 0
        loadServers()
    else
        m.again.control = "start"
    end if
end sub

sub onJoinPressed(event as object)
    if event.getData() = "join"
        go("join")
        m.joinPages.page = "join"
        m.joinPages.active = true
    else if event.getData() = "address"
        typeAddress()
    end if
end sub

sub backToJoin(why as string)
    go("join")
    m.joinPages.page = "join"
    m.joinPages.note = why
    m.joinPages.active = true
end sub

' The code is whole: find the server at its address; a Portico Account server is joined by the
' account's sign-in carrying the code, a local one by a new username and password.
sub onJoinSubmitted(event as object)
    m.step = m.step + 1        ' a new attempt: answers to an earlier one are dropped
    m.inviteCode = event.getData().code
    m.candidates = addressCandidates(event.getData().address)
    if m.candidates.count() = 0
        m.joinPages.note = "Enter the server’s address first."
        return
    end if
    m.joinPages.note = "Looking for the server…"
    tryJoinAddress()
end sub

sub tryJoinAddress()
    if m.candidates.count() = 0
        m.joinPages.note = "No Portico server answered at that address. Check the address, and that this TV can reach it."
        return
    end if
    ask("GET", m.candidates.shift(), "/v1/system", invalid, "onJoinSystem")
end sub

sub onJoinSystem(event as object)
    if late(event) then return
    result = event.getData()
    if not result.ok or result.data = invalid or result.data.id = invalid
        tryJoinAddress()
        return
    end if
    origin = event.getRoSGNode().ctx.origin
    m.server = {id: result.data.id, name: result.data.name, origin: origin}
    if result.data.signIn = "portico"
        showState("opening")
        m.invitation = m.inviteCode
        askChallenge()
    else
        ask("GET", origin, "/v1/auth/invitations/" + m.inviteCode, invalid, "onInvitation")
    end if
end sub

sub onInvitation(event as object)
    if late(event) then return
    result = event.getData()
    if not result.ok
        m.joinPages.note = result.error.message
        return
    end if
    m.joinPages.note = ""
    typeIn("Choose a username", "Your new account on " + m.server.name + ".", "", false, "username")
end sub

sub accept(password as string)
    body = {username: m.username, password: password, device: thisDevice()}
    ask("POST", m.server.origin, "/v1/auth/invitations/" + m.inviteCode + "/accept", body, "onAccepted")
end sub

sub onAccepted(event as object)
    if late(event) then return
    result = event.getData()
    if result.ok and result.data <> invalid and result.data.session <> invalid
        profiles = []
        if result.data.profiles <> invalid then profiles = result.data.profiles
        m.stage = ""
        m.step = m.step + 1
        m.top.signedIn = {origin: event.getRoSGNode().ctx.origin, serverName: m.server.name, serverId: m.server.id, session: result.data.session, profiles: profiles}
    else
        m.joinPages.note = result.error.message
    end if
end sub

' --- Roku's keyboard, for the address and a local server's new username and password --------------

sub typeAddress()
    typeIn("Server address", "The address of the server you were invited to, such as 192.168.1.20 or media.example.com.", m.joinPages.address, false, "address")
end sub

sub typeIn(title as string, message as string, text as string, secret as boolean, after as string)
    dialog = keyboardDialog(title, message, text, secret)
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
        m.joinPages.address = text.trim()
    else if after = "username"
        m.username = text.trim()
        if m.username <> "" then typeIn("Choose a password", "The password for " + m.username + " on " + m.server.name + ".", "", true, "password")
    else if after = "password"
        accept(text)
    else if after = "login"
        m.login = text.trim()
        if m.login <> "" then typeIn("Password", "The password of your Portico Account.", "", true, "accountPassword")
    else if after = "accountPassword"
        ask("POST", services(), "/v1/sessions", {login: m.login, password: text, device: {name: "Portico on Roku", kind: "tv"}}, "onSignedInHere")
    else if after = "twoStep"
        ask("POST", services(), "/v1/sessions/two-step", {challengeId: m.challengeId, code: text.trim()}, "onSignedInHere")
    end if
end sub

sub onAskClosed(event as object)
    ' the dialog that closed may be an earlier one (the username's, with the password's already open)
    if m.dialog <> invalid and not event.getRoSGNode().isSameNode(m.dialog) then return
    m.dialog = invalid
    onActive()
end sub

sub onNoticeSent()
end sub

function thisDevice() as object
    info = createObject("roDeviceInfo")
    return {installationId: installationId(), name: info.getFriendlyName(), platform: "roku", app: "portico-roku", appVersion: createObject("roAppInfo").getVersion()}
end function

' --- the pages for what can go wrong (connect.html frames 8 to 13 and 35) --------------------

sub showState(kind as string, why = "" as string)
    ' a join that goes wrong on the way says why on its own page, where the code can be tried again
    if m.invitation <> invalid and kind <> "opening"
        if kind = "waiting" then why = "The server isn’t answering. Try again in a moment."
        if kind = "accounts" then why = "Portico Accounts aren’t answering right now. Try again in a moment."
        backToJoin(why)
        return
    end if
    go(kind)
    name = ""
    if m.server <> invalid then name = m.server.name
    m.page.footnote = ""
    if kind = "opening"
        setPage("", "Opening " + name + "…", "Portico is checking that it’s online and signing you in.", [["choose", "Choose another server"]])
    else if kind = "waiting"
        setPage("warning", "Waiting for " + name, "Portico will connect by itself when it’s back.", [["again", "Check now"], ["choose", "Choose another server"]])
        countDown()
    else if kind = "accounts"
        setPage("warning", "Waiting for Portico Accounts", "Portico Accounts aren’t answering right now. " + name + " isn’t affected; Portico will open it as soon as they’re back.", [["again", "Check now"]])
        countDown()
    else if kind = "refused"
        setPage("warning", "You no longer have access to " + name, why, [["choose", "Choose another server"], ["refresh", "Refresh servers"]])
    else if kind = "cant"
        setPage("warning", "Portico can’t open " + name, why, [["again", "Try again"], ["choose", "Choose another server"]])
    else if kind = "failed"
        setPage("warning", "Your servers couldn’t be loaded", "Check your connection, then try again.", [["refresh", "Try again"]])
    end if
    m.page.active = true
end sub

sub setPage(icon as string, title as string, body as string, buttons as object)
    m.page.icon = icon
    m.page.title = title
    m.page.body = body
    row = []
    for each b in buttons
        row.push({id: b[0], text: b[1], icon: ""})
    end for
    m.page.buttons = []     ' a new page starts on its first button
    m.page.buttons = row
end sub

' Waiting checks again by itself: after 20 seconds, then doubling up to five minutes (connect-03).
sub countDown()
    if m.wait = invalid or m.wait < 20 then m.wait = 20
    m.left = m.wait
    m.again.duration = m.wait
    m.again.control = "start"
    m.tick.control = "start"
    onTick()
    m.wait = m.wait * 2
    if m.wait > 300 then m.wait = 300
end sub

sub onTick()
    if m.left < 0 then m.left = 0
    words = "Checking again in " + m.left.toStr() + " seconds"
    if m.stage = "list" then m.top.findNode("listCount").text = words else m.page.footnote = words
    m.left = m.left - 1
end sub

sub onAgain()
    if m.stage = "start"
        ask("GET", services(), "/v1/servers?signature=rsa", invalid, "onStartServers", {accountId: storeRead("porticoAccountId")}, "", accountToken())
        return
    end if
    if m.stage = "list" and m.allOffline = true
        m.tick.control = "stop"
        m.top.findNode("listCount").text = "Checking now…"
        for i = 0 to m.servers.count() - 1
            m.servers[i].state = "checking"
            probe(i, 0, false)
        end for
        return
    end if
    if m.invitation <> invalid then return      ' a join's pages have no automatic retry
    if m.stage = "waiting" or m.stage = "accounts" or m.stage = "cant"
        m.tick.control = "stop"
        m.page.footnote = "Checking now…"
        openServer()
    end if
end sub

' --- keys and buttons ------------------------------------------------------------------------

' Leaving these pages: answers still on their way are dropped, so nothing signs in after Back.
sub leave(where as string)
    m.step = m.step + 1
    m.stage = ""
    m.poll.control = "stop"
    m.again.control = "stop"
    m.tick.control = "stop"
    m.top.leave = where
end sub

sub onButton(event as object)
    which = event.getData()
    if which = "onTV"
        m.codeNote.text = ""
        typeIn("Username or email", "The username or email of your Portico Account.", "", false, "login")
    else if which = "back"
        leave("door")
    else if which = "local"
        leave("local")
    else if which = "refresh"
        m.wait = 0
        loadServers()
    else if which = "choose"
        m.wait = 0
        loadServers()
    else if which = "again"
        openServer()
    end if
end sub

function onKeyEvent(key as string, press as boolean) as boolean
    if not press then return false
    if key = "back"
        if m.stage = "code" or m.stage = "list" or m.stage = "failed" or m.stage = "start"
            leave("door")
        else if m.stage = "join"
            showStart(m.notice)
        else
            m.wait = 0
            loadServers()
        end if
        return true
    end if
    if m.stage = "list"
        count = m.rows.getChildCount()
        if m.inRows
            if key = "down" and m.focus < count - 1
                m.focus = m.focus + 1
            else if key = "down"
                m.inRows = false
            else if key = "up" and m.focus > 0
                m.focus = m.focus - 1
            else if key = "OK" and count > 0
                m.wait = 0
                openServer()
                return true
            end if
            focusList()
        else if key = "up" and count > 0
            m.inRows = true
            focusList()
        end if
    end if
    ' nothing lies to the left of these pages: the edge stops
    return key = "left" or key = "up" or key = "down" or key = "right"
end function

' --- small things ----------------------------------------------------------------------------

' An address as printed: without its https://.
function printed(url as dynamic) as string
    if url = invalid then return ""
    if left(url, 8) = "https://" then return mid(url, 9)
    return url
end function
