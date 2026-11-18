sub init()
    c = colours()
    f = frames().signIn
    m.faces = m.top.findNode("faces")
    m.row = m.top.findNode("row")
    m.pin = m.top.findNode("pin")
    m.pad = m.top.findNode("pad")
    m.note = m.top.findNode("note")
    m.pinNote = m.top.findNode("pinNote")
    m.top.findNode("mark").translation = [f.markX, f.markY]
    title = m.top.findNode("title")
    title.translation = [frames().gutter, f.whoTitleBottom - title.height]
    title.width = 1920 - frames().gutter * 2
    setType(title, "authTitle", c.text)
    m.note.translation = [0, f.whoNoteY]
    setType(m.note, "body", c.danger)
    ' the PIN's column on the left, the pad on the right; the boxes centred in the column
    faceX = f.pinX + (f.pinWidth - 196) / 2
    for each id in ["pinFace", "pinShade", "pinInitial"]
        m.top.findNode(id).translation = [faceX, f.pinFaceY]
    end for
    lines = {pinTitle: f.pinTitleY, pinName: f.pinNameY, pinNote: f.pinNoteY}
    for each id in lines
        label = m.top.findNode(id)
        label.translation = [f.pinX, lines[id]]
        label.width = f.pinWidth
    end for
    m.pad.translation = [f.keysX, f.keysY]
    m.rememberStop = m.top.findNode("rememberStop")
    setType(m.top.findNode("rememberLabel"), "body", c.textSecondary)
    m.top.findNode("rememberBox").blendColor = c.lineStrong
    m.rememberOn = true          ' on unless the person turns it off (owner, 18 Nov)
    m.onRemember = false
    m.pad.boxesAt = [f.pinX + (f.pinWidth - 470) / 2 - f.keysX, f.boxesY - f.keysY]     ' four boxes are 470 across
    hints = m.top.findNode("hints")
    hints.translation = [960, f.hintsY]
    hints.hints = [{keys: ["Play/Pause"], text: "Delete a digit"}, {keys: ["Back"], text: "Other profiles"}]
    m.forgot = m.top.findNode("forgot")
    m.forgotButtons = m.top.findNode("forgotButtons")
    lines = {forgotName: f.eyebrowY, forgotTitle: f.titleY, forgotBody: f.bodyY, forgotButtons: f.buttonsY}
    for each id in lines
        m.top.findNode(id).translation = [f.pageX, lines[id]]
    end for
    m.top.findNode("forgotCard").translation = [f.cardX, f.cardY]
    setType(m.top.findNode("forgotName"), "eyebrow", c.accentStrong)
    setType(m.top.findNode("forgotTitle"), "authTitle", c.text)
    setType(m.top.findNode("forgotBody"), "lede", c.textSecondary)
    m.forgotButtons.buttons = [{id: "back", text: "Back", icon: ""}]
    m.forgotButtons.observeField("pressed", "onForgotBack")
    setType(m.top.findNode("pinTitle"), "pinTitle", c.text)
    setType(m.top.findNode("pinName"), "lede", c.textSecondary)
    setType(m.top.findNode("pinInitial"), "faceInitial", c.text)
    setType(m.pinNote, "body", c.textSecondary)
    m.pad.observeField("entered", "onPin")
    m.pad.observeField("extraPressed", "onForgot")
    m.cells = []
    m.shift = 0
    m.focus = 0
    m.stage = "faces"
    m.busy = false
    m.top.observeField("focusedChild", "paint")
end sub

' Each face (auth.html frame 16): the colour with its light and shade, the initial, a lock for a
' profile with a PIN, the name and the line under it. The one in focus grows and takes the ring.
sub onProfiles()
    c = colours()
    f = frames().signIn
    profiles = m.top.profiles
    m.top.findNode("title").text = "Who’s watching on " + m.top.serverName + "?"
    m.row.removeChildrenIndex(m.row.getChildCount(), 0)
    m.cells = []
    count = profiles.count()
    m.rowWidth = count * f.facePitch - 80      ' faces are 216 across, 80 apart
    m.left0 = (1920 - m.rowWidth) / 2
    m.shift = 0
    for i = 0 to count - 1
        profile = profiles[i]
        if profile.id = m.top.last then m.focus = i
        cell = m.row.createChild("Group")
        cell.translation = [m.left0 + i * f.facePitch, f.facesY]
        cell.scaleRotateCenter = [108, 160]
        ring = cell.createChild("Poster")
        ring.uri = "pkg:/images/focus-disc.png"
        ring.width = 296
        ring.height = 296
        ring.translation = [-40, -40]
        face = cell.createChild("Poster")
        face.uri = "pkg:/images/disc.png"
        face.width = 216
        face.height = 216
        face.blendColor = faceColour(profile.color)
        shade = cell.createChild("Poster")
        shade.uri = "pkg:/images/face-shade.png"
        shade.width = 216
        shade.height = 216
        initial = cell.createChild("Label")
        initial.width = 216
        initial.height = 216
        initial.horizAlign = "center"
        initial.vertAlign = "center"
        initial.text = ucase(left(profile.name, 1))
        setType(initial, "faceInitial", c.text)
        if profile.pinRequired = true then addLock(cell)
        name = cell.createChild("Label")
        name.width = 296
        name.translation = [-40, 242]
        name.horizAlign = "center"
        name.text = profile.name
        setType(name, "faceName", c.textSecondary)
        under = cell.createChild("Label")
        under.width = 296
        under.translation = [-40, 289]
        under.horizAlign = "center"
        if profile.pinRequired = true then under.text = "Protected by a PIN" else if profile.isPrimary = true then under.text = "Main profile"
        setType(under, "faceNote", c.textTertiary)
        m.cells.push({node: cell, ring: ring, name: name})
    end for
    paint()
end sub

' The lock at the face's lower right: a raised disc with the ink round it, a quarter of the face across.
sub addLock(cell as object)
    c = colours()
    for each part in [[151, 60, c.ink], [155, 52, c.raisedBright]]
        disc = cell.createChild("Poster")
        disc.uri = "pkg:/images/disc.png"
        disc.translation = [part[0], part[0]]
        disc.width = part[1]
        disc.height = part[1]
        disc.blendColor = part[2]
    end for
    lock = cell.createChild("Poster")
    lock.uri = "pkg:/images/i-lock-28.png"
    lock.translation = [167, 167]
    lock.width = 28
    lock.height = 28
    lock.blendColor = c.text
end sub

sub onActive()
    if not m.top.active then return
    ' The faces take focus, not the screen: the screen holds the pad, so Roku would leave focus on
    ' the hidden pad and the next OK would type a digit into it.
    if m.stage = "pin"
        m.pad.active = true
    else if m.stage = "forgot"
        m.forgotButtons.active = true
    else
        m.faces.setFocus(true)
    end if
    paint()
end sub

sub paint()
    c = colours()
    ' Seven or eight faces are wider than the screen: the row moves with focus (as the other TV
    ' apps' row does), keeping the face in focus between the gutter and the right edge.
    if m.rowWidth <> invalid and m.rowWidth > 1920
        x = m.left0 + m.focus * frames().signIn.facePitch
        if x + m.shift < frames().gutter then m.shift = frames().gutter - x
        if x + m.shift + 216 > frames().rightEdge then m.shift = frames().rightEdge - 216 - x
    end if
    m.row.translation = [m.shift, 0]
    shown = (m.stage = "faces" and m.top.isInFocusChain())
    for i = 0 to m.cells.count() - 1
        cell = m.cells[i]
        on = (shown and i = m.focus)
        if on then cell.node.scale = [focusZoom(), focusZoom()] else cell.node.scale = [1, 1]
        cell.ring.visible = on
        if on then cell.name.color = c.text else cell.name.color = c.textSecondary
    end for
end sub

sub choose(pin as dynamic)
    if m.busy then return
    m.busy = true
    profile = m.top.profiles[m.focus]
    body = {}
    if pin <> invalid then body = {pin: pin}
    if pin <> invalid and m.top.remember and m.rememberOn then body.rememberPin = true
    api("POST", "/v1/profiles/" + profile.id + "/select", body, "onSelected")
end sub

sub onSelected(event as object)
    m.busy = false
    result = event.getData()
    if result.ok and result.data.accessToken <> invalid
        m.top.chosen = {session: result.data, profile: m.top.profiles[m.focus], pinToken: result.data.pinToken}
    else if m.stage = "pin" and result.error.code = "profile_pin_invalid"
        m.pad.wrong = true
    else if m.stage = "pin"
        ' any other refusal (too many tries, a limit on the account) is not a wrong PIN: give the reason
        m.pinNote.text = result.error.message
    else
        m.note.text = result.error.message
    end if
end sub

sub showPin(show as boolean)
    profile = m.top.profiles[m.focus]
    if show then m.stage = "pin" else m.stage = "faces"
    m.pin.visible = show
    m.faces.visible = not show
    if show
        m.top.findNode("pinFace").blendColor = faceColour(profile.color)
        m.top.findNode("pinInitial").text = ucase(left(profile.name, 1))
        m.top.findNode("pinName").text = profile.name
        m.pinNote.text = ""
        m.pad.digits = 4
        m.onRemember = false
        m.rememberStop.visible = m.top.remember
        m.pad.passLeft = m.top.remember
        paintRemember()
    end if
    onActive()
end sub

sub onPin(event as object)
    choose(event.getData())
end sub

' Forgot PIN? on a TV hands over to a phone or computer, as the other TV apps do: setting a new PIN
' needs the account's password. A server of its own resets it on its profile page; a server people
' sign in to with a Portico Account, at web.getportico.tv, which opens this server from the list.
sub onForgot()
    m.pinNote.text = ""
    api("GET", "/v1/system", invalid, "onForgotSystem")
end sub

sub onForgotSystem(event as object)
    result = event.getData()
    if not result.ok or result.data = invalid
        m.pinNote.text = result.error.message
        return
    end if
    card = m.top.findNode("forgotCard")
    body = m.top.findNode("forgotBody")
    if result.data.signIn = "portico"
        card.link = "https://web.getportico.tv/servers?open=" + result.data.id
        card.address = "web.getportico.tv/servers"
        body.text = "Reset this PIN on your phone or computer: sign in to your Portico Account, choose this profile, then Forgot PIN?"
    else
        card.link = m.global.origin + "/settings/profile"
        card.address = addressShown(m.global.origin) + "/settings/profile"
        body.text = "Manage profiles, PINs and two-step verification on your phone or computer."
    end if
    m.top.findNode("forgotName").text = m.top.profiles[m.focus].name
    m.stage = "forgot"
    m.pin.visible = false
    m.forgot.visible = true
    onActive()
end sub

' Back returns to the PIN pad as it was left, on Forgot PIN?.
sub onForgotBack()
    m.stage = "pin"
    m.forgot.visible = false
    m.pin.visible = true
    onActive()
end sub

function onKeyEvent(key as string, press as boolean) as boolean
    if not press then return false
    if m.stage = "forgot"
        if key = "back" then onForgotBack()
        return true
    end if
    if m.stage = "pin"
        if key = "back"
            showPin(false)
            return true
        end if
        if m.onRemember
            ' on Remember this profile: OK turns it on or off; Right or Up goes back to the pad
            if key = "OK" then m.rememberOn = not m.rememberOn
            if key = "right" or key = "up"
                m.onRemember = false
                m.pad.active = true
            end if
        else if key = "left" and m.top.remember
            m.onRemember = true
            m.rememberStop.setFocus(true)
        end if
        paintRemember()
        return true
    end if
    if key = "right" and m.focus < m.top.profiles.count() - 1
        m.focus = m.focus + 1
    else if key = "left" and m.focus > 0
        m.focus = m.focus - 1
    else if key = "OK"
        m.note.text = ""
        if m.top.profiles[m.focus].pinRequired then showPin(true) else choose(invalid)
    else if key = "back"
        return false
    end if
    paint()
    return true
end function

' With one profile and no PIN there is nothing to choose.
sub chooseOnly()
    m.focus = 0
    choose(invalid)
end sub

sub paintRemember()
    m.top.findNode("rememberTick").visible = m.rememberOn
    ring = m.top.findNode("rememberRing")
    ring.visible = m.onRemember
    out = metrics().focusSpread + 6
    ring.translation = [-out, -out]
    ring.width = 40 + out * 2
    ring.height = 40 + out * 2
    label = m.top.findNode("rememberLabel")
    if m.onRemember then label.color = colours().text else label.color = colours().textSecondary
    ' centred in the PIN's column, as the boxes are
    f = frames().signIn
    width = 58 + label.boundingRect().width
    m.rememberStop.translation = [f.pinX + (f.pinWidth - width) / 2, f.pinRememberY]
end sub
