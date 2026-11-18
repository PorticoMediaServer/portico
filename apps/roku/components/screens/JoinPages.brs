sub init()
    c = colours()
    f = frames().signIn
    x = frames().gutter
    m.start = m.top.findNode("start")
    m.join = m.top.findNode("join")
    m.keyboard = m.top.findNode("keyboard")
    m.top.findNode("mark").translation = [x, f.markY]
    ' the header both pages share (connect.html frames 35 to 38)
    for each part in [["eyebrow", f.startEyebrowY, "eyebrow", c.accentStrong], ["title", f.startTitleY, "authTitle", c.text], ["body", f.startBodyY, "authBody", c.textSecondary]]
        label = m.top.findNode(part[0])
        label.translation = [x, part[1]]
        setType(label, part[2], part[3])
    end for
    ' the notice of frame 38, in its fixed slot above the tiles
    m.top.findNode("notice").translation = [x, f.noticeY]
    plate = m.top.findNode("noticePlate")
    plate.width = frames().rightEdge - x
    plate.blendColor = c.raised
    m.top.findNode("noticeIcon").translation = [30, 25]
    m.top.findNode("noticeIcon").blendColor = c.accentStrong
    text = m.top.findNode("noticeText")
    text.translation = [80, 0]
    text.width = plate.width - 110
    body = typeRoles().authBody
    text.drawingStyles = {
        "default": {fontUri: "pkg:/fonts/manrope-400.ttf", fontSize: body[1], color: "#" + mid(c.textSecondary, 3)},
        "b": {fontUri: "pkg:/fonts/manrope-700.ttf", fontSize: body[1], color: "#" + mid(c.text, 3)}
    }
    ' the two tiles: Join takes the focus; Set up is read and scanned, and has nothing to press
    tile("join", f.joinTileX, f.joinTileWidth)
    tile("setup", f.setupTileX, f.setupTileWidth)
    m.top.findNode("setupPlate").blendColor = c.raised
    m.top.findNode("setupEdge").blendColor = c.line
    qr = m.top.findNode("setupQr")
    qr.translation = [f.setupTileWidth - 262, 190]
    qr.scale = [220 / 300, 220 / 300]      ' QrCode is drawn 300 across; the tile's is 220
    ' the invitation's page: two boxes, the keyboard under them, where to find the code beside them
    lines = {addressLabel: f.addressLabelY, codeLabel: f.codeLabelY}
    for each id in lines
        label = m.top.findNode(id)
        label.translation = [x, lines[id]]
        setType(label, "groupLabel", c.textSecondary)
    end for
    for each part in [["address", f.addressBoxY], ["code", f.codeBoxY]]
        frameNode = m.top.findNode(part[0] + "Box")
        frameNode.translation = [x, part[1]]
        frameNode.width = f.fieldWidth
        frameNode.height = 83
        words = m.top.findNode(part[0] + "Text")
        words.translation = [x + 28, part[1]]
        words.width = f.fieldWidth - 56
        setType(words, "lede", c.text)
    end for
    m.keyboard.width = f.fieldWidth
    m.keyboard.translation = [x, f.keyboardY]
    m.keyboard.observeField("text", "onTyped")
    side = {whereTitle: [f.sideTitleY, "rowHeading", c.text], whereText: [f.sideTextY, "body", c.textSecondary], easierText: [f.easierTextY, "body", c.textSecondary], joinNote: [f.joinNoteY, "body", c.danger]}
    for each id in side
        label = m.top.findNode(id)
        label.translation = [f.sideX, side[id][0]]
        label.width = frames().rightEdge - f.sideX
        setType(label, side[id][1], side[id][2])
    end for
    m.at = "address"      ' on the invitation's page: "address" (its box) or "keys"
    m.top.observeField("focusedChild", "paint")
end sub

sub tile(name as string, x as integer, w as integer)
    c = colours()
    f = frames().signIn
    group = m.top.findNode(name + "Tile")
    group.translation = [x, f.tilesY]
    for each id in [name + "Plate", name + "Edge"]
        part = m.top.findNode(id)
        if part <> invalid
            part.width = w
            part.height = f.tileHeight
        end if
    end for
    m.top.findNode(name + "Disc").translation = [48, 40]
    m.top.findNode(name + "Disc").blendColor = c.raisedBright
    m.top.findNode(name + "Icon").translation = [73, 65]
    m.top.findNode(name + "Icon").blendColor = c.accentStrong
    title = m.top.findNode(name + "Title")
    title.translation = [48, 142]
    setType(title, "title", c.text)
    text = m.top.findNode(name + "Text")
    text.translation = [48, 211]
    text.width = w - 96
    if name = "setup" then text.width = w - 360      ' beside the QR code
    setType(text, "authBody", c.textSecondary)
end sub

sub onPage()
    start = (m.top.page = "start")
    m.start.visible = start
    m.join.visible = not start
    if start
        m.top.findNode("eyebrow").text = "Your Portico Account is ready"
        onNotice()
    else
        m.top.findNode("eyebrow").text = "Join with an invitation"
        m.top.findNode("title").text = "Enter the invitation"
        m.top.findNode("body").text = "Type the server’s address and the code the owner shared."
        m.keyboard.text = ""
        m.top.note = ""
        if m.top.address = "" then m.at = "address" else m.at = "keys"
    end if
    onActive()
end sub

' Frame 38: the same tiles under a notice of what happened, and a title that says what is next.
sub onNotice()
    if m.top.page <> "start" then return
    notice = m.top.notice
    shown = (notice <> invalid and notice.title <> invalid)
    m.top.findNode("notice").visible = shown
    if shown
        m.top.findNode("noticeText").text = "<b>" + notice.title + "</b> " + notice.body
        m.top.findNode("title").text = "Join a server or set up your own"
    else
        m.top.findNode("title").text = "Welcome to Portico"
    end if
    m.top.findNode("body").text = "Portico plays your media from a server. Join one that someone has shared with you, or set up your own."
end sub

sub onAddress()
    m.top.findNode("addressText").text = m.top.address
end sub

sub onNote()
    m.top.findNode("joinNote").text = m.top.note
end sub

' The code shows as typed; once it has all its 16 characters it is sent (the PIN boxes' rule).
sub onTyped()
    code = m.keyboard.text
    m.top.findNode("codeText").text = code
    if len(code.replace("-", "").replace(" ", "")) >= 16 then m.top.submitted = {address: m.top.address, code: code}
end sub

sub onActive()
    if not m.top.active then return
    if m.top.page = "join" and m.at = "keys"
        m.keyboard.active = true
    else
        m.top.setFocus(true)
    end if
    paint()
end sub

sub paint()
    c = colours()
    has = m.top.isInFocusChain()
    ring = m.top.findNode("joinRing")
    out = metrics().focusSpread + 6
    ring.translation = [-out, -out]
    ring.width = frames().signIn.joinTileWidth + out * 2
    ring.height = frames().signIn.tileHeight + out * 2
    ring.visible = (has and m.top.page = "start")
    if has and m.top.page = "start" then m.top.findNode("joinPlate").blendColor = c.raisedHigh else m.top.findNode("joinPlate").blendColor = c.raised
    ' the box being typed into is drawn in the accent, the address box in focus takes the ring
    addressRing = m.top.findNode("addressRing")
    addressRing.visible = (has and m.top.page = "join" and m.at = "address")
    addressRing.translation = [frames().gutter - out, frames().signIn.addressBoxY - out]
    addressRing.width = frames().signIn.fieldWidth + out * 2
    addressRing.height = 83 + out * 2
    m.top.findNode("addressBox").blendColor = c.lineStrong
    if m.at = "keys" then m.top.findNode("codeBox").blendColor = c.accentStrong else m.top.findNode("codeBox").blendColor = c.lineStrong
end sub

function onKeyEvent(key as string, press as boolean) as boolean
    if not press then return false
    if m.top.page = "start"
        if key = "OK" then m.top.pressed = "join"
        return key <> "back"      ' Back is AccountPages'
    end if
    if m.at = "address"
        if key = "OK"
            m.top.pressed = "address"
        else if key = "down"
            m.at = "keys"
            m.keyboard.active = true
            paint()
        end if
        return key <> "back"
    end if
    ' the keyboard's top row goes up to the address box
    if key = "up"
        m.at = "address"
        m.top.setFocus(true)
        paint()
        return true
    end if
    return key <> "back"
end function
