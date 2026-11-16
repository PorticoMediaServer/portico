sub init()
    c = colours()
    m.faces = m.top.findNode("faces")
    m.row = m.top.findNode("row")
    m.ring = m.top.findNode("ring")
    m.pin = m.top.findNode("pin")
    m.pad = m.top.findNode("pad")
    m.note = m.top.findNode("note")
    m.pinNote = m.top.findNode("pinNote")
    setType(m.top.findNode("title"), "welcome", c.text)
    setType(m.note, "body", c.danger)
    setType(m.top.findNode("pinTitle"), "pageTitle", c.text)
    setType(m.top.findNode("pinName"), "lede", c.textSecondary)
    setType(m.top.findNode("pinInitial"), "faceInitial", c.text)
    setType(m.pinNote, "body", c.textSecondary)
    for each id in ["playKey", "backKey"]
        setType(m.top.findNode(id), "keycap", c.text)
    end for
    for each id in ["playText", "backText"]
        setType(m.top.findNode(id), "caption", c.textTertiary)
    end for
    m.pad.observeField("entered", "onPin")
    m.pad.observeField("extraPressed", "onForgot")
    m.focus = 0
    m.stage = "faces"
    m.busy = false
    m.top.observeField("focusedChild", "paint")
end sub

' packages/design profileArtColors
function faceColour(name as dynamic) as string
    colours_ = {blue: "0x28587AFF", violet: "0x685294FF", mint: "0x28665DFF", coral: "0x985243FF", gold: "0x796120FF", slate: "0x4A596AFF", rose: "0x854D73FF", sky: "0x376F8CFF"}
    if name <> invalid and colours_[name] <> invalid then return colours_[name]
    return colours_.blue
end function

sub onProfiles()
    c = colours()
    profiles = m.top.profiles
    m.top.findNode("title").text = "Who’s watching on " + m.top.serverName + "?"
    m.row.removeChildrenIndex(m.row.getChildCount(), 0)
    count = profiles.count()
    pitch = 296
    left0 = (1920 - (count * 216 + (count - 1) * 80)) / 2
    m.left0 = left0
    for i = 0 to count - 1
        profile = profiles[i]
        if profile.id = m.top.last then m.focus = i
        cell = m.row.createChild("Group")
        cell.translation = [left0 + i * pitch, 470]
        face = cell.createChild("Poster")
        face.uri = "pkg:/images/disc.png"
        face.width = 216
        face.height = 216
        face.blendColor = faceColour(profile.color)
        initial = cell.createChild("Label")
        initial.width = 216
        initial.height = 216
        initial.horizAlign = "center"
        initial.vertAlign = "center"
        initial.text = ucase(left(profile.name, 1))
        setType(initial, "faceInitial", c.text)
        name = cell.createChild("Label")
        name.width = 296
        name.translation = [-40, 244]
        name.horizAlign = "center"
        name.text = profile.name
        setType(name, "heading", c.textSecondary)
        under = cell.createChild("Label")
        under.width = 296
        under.translation = [-40, 290]
        under.horizAlign = "center"
        if profile.pinRequired then under.text = "Protected by a PIN" else if profile.isPrimary then under.text = "Main profile"
        setType(under, "caption", c.textTertiary)
    end for
    paint()
end sub

sub onActive()
    if not m.top.active then return
    if m.stage = "pin" then m.pad.active = true else m.top.setFocus(true)
    paint()
end sub

sub paint()
    m.ring.visible = (m.stage = "faces" and m.top.isInFocusChain())
    if m.left0 <> invalid then m.ring.translation = [m.left0 + m.focus * 296 - 40, 430]
end sub

sub choose(pin as dynamic)
    if m.busy then return
    m.busy = true
    profile = m.top.profiles[m.focus]
    body = {}
    if pin <> invalid then body = {pin: pin}
    api("POST", "/v1/profiles/" + profile.id + "/select", body, "onSelected")
end sub

sub onSelected(event as object)
    m.busy = false
    result = event.getData()
    if result.ok and result.data.accessToken <> invalid
        m.top.chosen = {session: result.data, profile: m.top.profiles[m.focus]}
    else if m.stage = "pin" and result.status <> 0
        m.pad.wrong = true
        if result.status = 429 then m.pinNote.text = result.error.message
    else if m.stage = "pin"
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
    end if
    onActive()
end sub

sub onPin(event as object)
    choose(event.getData())
end sub

' On a TV, a forgotten PIN is put right on a phone or computer (definition, sign-in rule 30).
sub onForgot()
    m.pinNote.text = "To set a new PIN, open Portico on a phone or computer and go to Settings › Profiles. It asks for the account's password."
end sub

function onKeyEvent(key as string, press as boolean) as boolean
    if not press then return false
    if m.stage = "pin"
        if key = "back"
            showPin(false)
            return true
        end if
        return key <> "back"
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
