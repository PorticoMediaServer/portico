sub init()
    c = colours()
    m.door = m.top.findNode("door")
    m.code = m.top.findNode("code")
    m.factor = m.top.findNode("factor")
    m.doorButtons = m.top.findNode("doorButtons")
    m.doorNote = m.top.findNode("doorNote")
    m.codeButtons = m.top.findNode("codeButtons")
    m.codeNote = m.top.findNode("codeNote")
    m.factorPad = m.top.findNode("factorPad")
    m.poll = m.top.findNode("poll")
    setType(m.top.findNode("welcome"), "welcome", c.text)
    setType(m.doorNote, "body", c.danger)
    setType(m.top.findNode("eyebrow"), "eyebrow", c.accentStrong)
    setType(m.top.findNode("server"), "pageTitle", c.text)
    setType(m.codeNote, "body", c.danger)
    setType(m.top.findNode("press"), "caption", c.textTertiary)
    setType(m.top.findNode("pressRest"), "caption", c.textTertiary)
    setType(m.top.findNode("backKey"), "keycap", c.text)
    setType(m.top.findNode("where"), "ledeStrong", c.textSecondary)
    setType(m.top.findNode("digits"), "code", c.text)
    setType(m.top.findNode("keep"), "caption", c.textTertiary)
    setType(m.top.findNode("factorTitle"), "pageTitle", c.text)
    setType(m.top.findNode("factorBody"), "lede", c.textSecondary)
    how = m.top.findNode("how")
    how.drawingStyles = {
        "default": {fontUri: "pkg:/fonts/manrope-400.ttf", fontSize: 28, color: "#BDCCD6FF"},
        "b": {fontUri: "pkg:/fonts/manrope-700.ttf", fontSize: 28, color: "#EDF5FAFF"}
    }
    ' "Sign in with Portico Account" joins this row when that flow is built (the next change).
    m.doorButtons.buttons = [{id: "local", text: "Use a local server", icon: ""}]
    m.doorButtons.observeField("pressed", "onDoor")
    m.codeButtons.observeField("pressed", "onCodeButton")
    m.factorPad.observeField("entered", "onFactor")
    m.poll.observeField("fire", "onPoll")
    m.stage = "door"
    m.typed = ""
    m.step = 0
end sub

sub onActive()
    if not m.top.active then return
    if m.stage = "code"
        m.codeButtons.active = true
    else if m.stage = "factor"
        m.factorPad.active = true
    else
        m.doorButtons.active = true
    end if
end sub

sub go(stage as string)
    m.stage = stage
    m.step = m.step + 1
    m.door.visible = (stage = "door")
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

sub ask(method as string, path as string, body as dynamic, callback as string, code = "" as string)
    api(method, path, body, callback, {step: m.step, origin: m.origin, code: code}, true, "", m.origin)
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

sub onDoor()
    m.doorNote.text = ""
    typeIn("Server address", "The address of your Portico server, such as 192.168.1.20 or media.example.com.", m.typed, false, "address")
end sub

sub tryAddress(typed as string)
    m.typed = typed
    m.candidates = addressCandidates(typed)
    if m.candidates.count() = 0
        m.doorNote.text = "Enter a server address."
        return
    end if
    m.doorNote.color = colours().textSecondary
    m.doorNote.text = "Looking for the server…"
    tryNext()
end sub

sub tryNext()
    if m.candidates.count() = 0
        m.doorNote.color = colours().danger
        m.doorNote.text = "No Portico server answered at that address. Check the address, and that this TV is on the same network as the server."
        return
    end if
    m.origin = m.candidates.shift()
    m.step = m.step + 1
    ask("GET", "/v1/system", invalid, "onSystem")
end sub

sub onSystem(event as object)
    result = event.getData()
    if late(event) then return
    if not result.ok or result.data = invalid or result.data.id = invalid
        tryNext()
        return
    end if
    m.system = result.data
    m.doorNote.color = colours().danger
    if m.system.setupRequired = true
        m.doorNote.text = m.system.name + " has not been set up yet. Set it up from a phone or a computer, then come back here."
        return
    end if
    m.doorNote.text = ""
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
    m.top.findNode("where").text = shown + "/device"
    m.top.findNode("digits").text = ""
    m.top.findNode("qr").text = ""
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
    m.top.findNode("digits").text = m.pending.code
    m.top.findNode("qr").text = m.origin + "/device#code=" + m.pending.code
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
        go("door")
        onDoor()
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
    m.top.signedIn = {origin: event.getRoSGNode().ctx.origin, serverName: m.system.name, session: signIn.session, profiles: profiles}
end sub

function onKeyEvent(key as string, press as boolean) as boolean
    if not press then return false
    if key = "back"
        if m.stage = "code"
            go("door")
            return true
        else if m.stage = "factor"
            go("code")
            newCode()
            return true
        end if
        return false   ' Back at the front door leaves the app
    end if
    ' nothing lies to the left of these pages: the edge stops
    return key = "left" or key = "up" or key = "down"
end function
