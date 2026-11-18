sub init()
    m.backdrop = m.top.findNode("backdrop")
    ' the Appearance settings this TV honours (source/Settings.brs appearanceLooks)
    m.global.addFields({looks: {}})
    m.screens = m.top.findNode("screens")
    m.rail = m.top.findNode("rail")
    m.screen = invalid
    m.task = createObject("roSGNode", "ApiTask")
    m.global.addFields({api: m.task, origin: "", pageShownAgain: 0})
    m.task.observeField("signInEnded", "onSignInEnded")
    ' the app's own log, a ring of recent lines (source/AppLog.brs)
    m.global.addFields({appLog: []})
    info = createObject("roDeviceInfo")
    logApp("info", "app", "Portico " + createObject("roAppInfo").getVersion() + " on Roku OS " + info.getOSVersion().major + "." + info.getOSVersion().minor + ", " + info.getModel())
    m.task.control = "run"
    ' the Watch Together group this profile is in, shared by the room page and the player
    m.global.addFields({together: m.top.findNode("together")})
    ' Roku's own dialogs (the keyboard) wear Portico's colours.
    palette = createObject("roSGNode", "RSGPalette")
    palette.colors = {
        DialogBackgroundColor: "0x132634FF", DialogItemColor: "0xEDF5FAFF", DialogTextColor: "0xEDF5FAFF",
        DialogFocusColor: "0xD8E6EEFF", DialogFocusItemColor: "0x03121CFF", DialogSecondaryTextColor: "0xBDCCD6FF",
        DialogSecondaryItemColor: "0x409FD4FF", DialogInputFieldColor: "0x0C1A25FF", DialogKeyboardColor: "0x1D3848FF",
        DialogFootprintColor: "0x1D3848FF"
    }
    m.top.palette = palette
    m.rail.observeField("chosen", "onRailChosen")
    m.rail.observeField("left", "onRailLeft")
    m.sheet = m.top.findNode("sheet")
    m.sheet.observeField("chosen", "onSheetChosen")
    m.confirm = m.top.findNode("confirm")
    m.confirm.observeField("chosen", "onSignOutAnswer")
    m.sheet.observeField("dismissed", "onSheetClosed")
    ' each library's name by id, for the words over a title's menu (kit/CardMenu.brs)
    m.global.addFields({libraryNames: {}})
    m.rail.observeField("isOpen", "onRailOpen")
    if createObject("roAppInfo").isDev()
        m.frames = 0
        m.frameClock = createObject("roTimespan")
        m.top.findNode("frameProbe").observeField("opacity", "onFrame")
        m.top.findNode("frameClock").control = "start"
    end if
end sub

' Development builds print the frames drawn each second, when it falls under 55.
sub onFrame()
    m.frames = m.frames + 1
    ms = m.frameClock.totalMilliseconds()
    if ms >= 1000
        fps = int(m.frames * 1000 / ms)
        if fps < 55 then ? "PORTICO fps "; fps
        m.frames = 0
        m.frameClock.mark()
    end if
end sub

' main.brs hands over the launch parameters once the scene is up; the app starts here.
sub onLaunchArgs()
    if not m.task.ready
        m.task.observeField("ready", "onLaunchArgs")
        return
    end if
    m.task.unobserveField("ready")
    m.task.observeField("serverChanged", "onServerChanged")
    args = m.top.launchArgs
    ' A deep link: Roku started the app to open one title (contentId is the item's id on this server).
    if args <> invalid and args.contentId <> invalid then m.linked = args
    if createObject("roAppInfo").isDev() and args <> invalid
        ' Development builds only: scripts can hand the app a sign-in, or clear it.
        if args.devSignOut <> invalid then storeWrite({origin: "", serverId: "", refreshToken: "", profileId: "", renewalId: ""})
        ' the shared elements page (design decision 4), for bin/roku-kit; never in a viewer's build
        if args.devKit <> invalid
            show("KitScreen")
            m.screen.active = true
            return
        end if
        if args.devOrigin <> invalid and args.devRefresh <> invalid
            storeWrite({origin: args.devOrigin, serverId: args.devServer, refreshToken: args.devRefresh, installationId: args.devInstallation, profileId: args.devProfile, renewalId: ""})
        end if
    end if
    origin = storeRead("origin")
    token = storeRead("refreshToken")
    if origin = "" or token = ""
        showSignIn()
        return
    end if
    m.global.origin = origin
    m.task.session = {origin: origin, serverId: storeRead("serverId"), refreshToken: token, installationId: installationId()}
    api("GET", "/v1/me", invalid, "onMe")
end sub

' One screen at a time fills the page; the rail shows only once someone is signed in with a profile.
sub show(component as string)
    logApp("info", "screen", component)
    m.sheet.visible = false     ' a menu belongs to the page that opened it, and goes with it
    m.screens.removeChildrenIndex(m.screens.getChildCount(), 0)
    m.screen = m.screens.createChild(component)
    m.rail.visible = false
    m.backdrop.art = ""
    m.backdrop.poster = ""
    m.backdrop.tint = ""
end sub

' Open a page over the one showing; Back returns to it exactly as it was left.
function push(component as string) as object
    logApp("info", "screen", component)
    m.screen.visible = false
    if m.screen.hasField("covered") then m.screen.covered = true
    m.screen = m.screens.createChild(component)
    m.screen.observeField("art", "onArt")
    m.screen.observeField("open", "onOpen")
    if m.screen.hasField("quiet") then m.screen.observeField("quiet", "onQuiet")
    if m.screen.hasField("menu") then m.screen.observeField("menu", "onMenu")
    m.backdrop.quiet = false
    return m.screen
end function

sub pop()
    count = m.screens.getChildCount()
    if count < 2 then return
    m.sheet.visible = false
    m.screens.removeChildIndex(count - 1)
    m.screen = m.screens.getChild(count - 2)
    m.screen.visible = true
    ' a page shown again: its cards draw their cut pictures again (kit/Card)
    m.global.pageShownAgain = m.global.pageShownAgain + 1
    if count = 2 and m.railHome <> invalid
        m.rail.current = m.railHome
        m.railHome = invalid
    end if
    setArt(m.screen.art)
    m.backdrop.quiet = (m.screen.hasField("quiet") and m.screen.quiet = true)
    m.screen.active = true
end sub

' A page asks for the side sheet by setting its `menu` field; the choice comes back in `menuChosen`.
sub onMenu(event as object)
    page = event.getRoSGNode()
    ' the audio player's menus (a song's More, a playlist to add to) open here too, over it
    m.sheetFor = invalid
    if page.isSameNode(m.top.findNode("audioPlayer"))
        m.sheetFor = page
        m.sheet.model = event.getData()
        m.sheet.visible = true
        m.sheet.active = true
        return
    end if
    if not page.isSameNode(m.screen) then return
    ' a page whose sheet no longer applies (the title ended under the player's options) closes it
    if event.getData().close = true
        if m.sheet.visible then closeSheet()
        return
    end if
    m.sheet.model = event.getData()
    m.sheet.visible = true
    m.sheet.active = true
end sub

' The profile and server menu (tv-more frame 10): its rows are this scene's own. `focusOn` names the
' row focus starts on when the menu opens again for what one of its rows opened ("" for the first).
sub openProfileMenu(focusOn as string)
    m.sheetIsOwn = true
    rows = [{id: "switch", text: "Switch profile", icon: "", focus: focusOn = "switch"},
        {id: "together", text: togetherRowText(), icon: "", focus: focusOn = "together"},
        {id: "signout", text: "Sign out", icon: "", gapBefore: true, focus: focusOn = "signout"}]
    ' the owner and administrators have Server settings first (tv-more frame 10)
    if m.role = "owner" or m.role = "admin" then rows.unshift({id: "server", text: "Server settings", icon: ""})
    m.sheet.model = {eyebrow: storeRead("serverName"), title: m.rail.profileName, status: "", rows: rows}
    m.sheet.visible = true
    m.sheet.active = true
end sub

sub onSheetChosen(event as object)
    if m.sheetIsOwn = true
        m.sheetIsOwn = false
        m.sheet.visible = false
        which = event.getData()
        if which = "switch"
            api("GET", "/v1/profiles", invalid, "onProfiles")
        else if which = "server"
            showServer()
        else if which = "together"
            showTogether()
        else if which = "signout"
            m.signOutFromMenu = true      ' Back or Cancel returns to the menu's Sign out row
            askSignOut()
        end if
        return
    end if
    closeSheet()
    ' a menu the audio player opened answers to it
    if m.sheetFor <> invalid
        owner = m.sheetFor
        m.sheetFor = invalid
        owner.menuChosen = event.getData()
        return
    end if
    if m.screen <> invalid and m.screen.hasField("menuChosen") then m.screen.menuChosen = event.getData()
end sub

' The account's role on this server: the owner and administrators have Server settings (view only).
sub onRole(event as object)
    result = event.getData()
    m.role = ""
    if result.ok and result.data.role <> invalid then m.role = result.data.role
end sub

sub showServer()
    show("ServerScreen")
    m.screen.role = m.role
    m.rail.visible = true
    m.rail.current = "profile"
    m.screen.active = true
end sub

sub onSheetClosed()
    own = (m.sheetIsOwn = true)
    m.sheetIsOwn = false
    if own
        ' Back from the profile menu returns to what opened it: the rail's profile item
        m.sheet.visible = false
        m.rail.enterOn = "profile"
        m.rail.active = true
        return
    end if
    ' a menu the audio player opened was not the page's: the page hears nothing
    forPlayer = (m.sheetFor <> invalid)
    closeSheet()
    m.sheetFor = invalid
    ' the page that asked for the menu hears that it closed (Settings goes back a step)
    if not own and not forPlayer and m.screen <> invalid and m.screen.hasField("menuClosed") then m.screen.menuClosed = true
end sub

' Sign out asks first (tv-more frame 33): focus starts on Cancel, so a stray OK changes nothing.
sub askSignOut()
    name = storeRead("serverName")
    if name = "" then name = "the server"
    m.confirm.model = {title: "Sign out of " + name + " on this TV?", body: "You’ll need to sign this TV in again.", verb: "Sign out"}
end sub

sub onSignOutAnswer()
    if m.confirm.chosen = "confirm"
        m.signOutFromMenu = false
        ' the server is told this sign-in is over; the TV forgets it either way
        api("DELETE", "/v1/auth/session", invalid, "onSignedOut")
    else if m.signOutFromMenu = true
        ' Back returns to the opener (DESIGN.md, TV focus): the profile menu, on Sign out
        m.signOutFromMenu = false
        openProfileMenu("signout")
    else if m.screen <> invalid
        m.screen.active = true
    end if
end sub

sub onSignedOut()
    m.global.appLog = []     ' the app's log goes with the sign-in
    storeWrite({refreshToken: "", renewalId: "", profileId: ""})
    m.task.session = {origin: storeRead("origin"), installationId: installationId()}
    showSignIn()
end sub

' Focus returns to what opened the sheet.
sub closeSheet()
    m.sheet.visible = false
    if m.sheetFor <> invalid
        m.sheetFor.active = true
    else if m.screen <> invalid
        m.screen.active = true
    end if
end sub

' A song (or a run of them), or an audiobook, plays in the audio player, which covers the page until it is left.
sub playAudio(ask as object)
    player = m.top.findNode("audioPlayer")
    if m.audioWatched <> true
        player.observeField("closed", "onAudioClosed")
        player.observeField("now", "onNowPlaying")
        player.observeField("menu", "onMenu")
        player.observeField("open", "onAudioOpen")
        card = m.top.findNode("nowPlaying")
        card.observeField("command", "onCardCommand")
        card.observeField("dismissed", "onCardDismissed")
        m.audioWatched = true
    end if
    m.top.findNode("nowPlaying").visible = false
    player.play = {item: ask.item, queue: ask.queue, startOver: ask.startOver, startAtMs: ask.startAtMs}
    player.visible = true
    m.rail.visible = false
    player.active = true
end sub

' Go to album or artist from the song's menu: the screen goes (the music carries on) and the page opens.
sub onAudioOpen(event as object)
    onAudioClosed()
    onOpen(event)
end sub

sub onAudioClosed()
    m.top.findNode("audioPlayer").visible = false
    m.rail.visible = true
    showCard()
    if m.screen <> invalid then m.screen.active = true
end sub

' The card shows on the pages while music plays and its screen is away, never over a video.
sub showCard()
    now = m.top.findNode("audioPlayer").now
    playing = (now <> invalid and now.title <> invalid)
    overVideo = (m.screen <> invalid and m.screen.subtype() = "PlayerScreen")
    m.top.findNode("nowPlaying").visible = playing and not m.top.findNode("audioPlayer").visible and not overVideo
end sub

sub onNowPlaying(event as object)
    m.top.findNode("nowPlaying").now = event.getData()
    ' a page that lists songs marks the one playing
    if m.screen <> invalid and m.screen.hasField("playingId")
        id = ""
        if event.getData().itemId <> invalid then id = event.getData().itemId
        if m.screen.playingId <> id then m.screen.playingId = id
    end if
    if m.top.findNode("nowPlaying").visible or event.getData().title = invalid then showCard()
end sub

sub onCardCommand(event as object)
    kind = event.getData()
    if kind = "open"
        card = m.top.findNode("nowPlaying")
        card.active = false
        card.visible = false
        player = m.top.findNode("audioPlayer")
        player.visible = true
        m.rail.visible = false
        player.active = true
    else
        m.top.findNode("audioPlayer").command = kind
        if kind = "stop" and m.screen <> invalid then m.screen.active = true
    end if
end sub

sub onCardDismissed()
    if m.cardFrom <> invalid
        m.cardFrom.setFocus(true)
        m.cardFrom = invalid
    else if m.screen <> invalid
        m.screen.active = true
    end if
end sub

' The node deepest in the focus chain: the one a key would reach now.
function focusedLeaf() as dynamic
    node = m.top
    while node.focusedChild <> invalid and not node.focusedChild.isSameNode(node)
        node = node.focusedChild
    end while
    if node.isSameNode(m.top) then return invalid
    return node
end function

sub onPlayerClosed()
    m.rail.visible = true
    pop()
    showCard()
end sub

' A page asks for a title to be opened or played.
sub onOpen(event as object)
    onOpenAsk(event.getData())
end sub

sub onOpenAsk(ask as object)
    kind = ask.item.kind
    ' a song's menu puts it into what is playing
    if ask.enqueue <> invalid
        m.top.findNode("audioPlayer").enqueue = {item: ask.item, placement: ask.enqueue}
        return
    end if
    ' Play on an album or an artist plays it in the audio player
    if ask.play = true and (kind = "album" or kind = "artist")
        if ask.queue = invalid then ask.queue = {source: {container: ask.item.id}}
        playAudio(ask)
        return
    end if
    ' a song plays in the audio player
    if kind = "track"
        playAudio(ask)
        return
    end if
    ' Resume or Play on an audiobook plays it there too; its card opens its page
    if kind = "audiobook" and ask.play = true
        playAudio(ask)
        return
    end if
    if ask.play = true and (kind = "movie" or kind = "episode" or kind = "channel")
        ' a video sets the music aside (Roku plays one stream at a time); its card comes back, paused,
        ' when the video ends, as the other apps do
        music = m.top.findNode("audioPlayer")
        if music.now <> invalid and music.now.title <> invalid then music.command = "park"
        m.top.findNode("nowPlaying").visible = false
        player = push("PlayerScreen")
        player.observeField("closed", "onPlayerClosed")
        if ask.queue <> invalid then player.queue = ask.queue     ' a collection's Play all or Shuffle
        player.item = ask.item
        player.startOver = (ask.startOver = true)
        player.together = (ask.together = true)     ' played with the group: it follows the group's clock
        m.rail.visible = false
        player.active = true
        return
    end if
    page = invalid
    if kind = "album" or kind = "artist" then page = push("MusicScreen")
    if kind = "audiobook" or kind = "author" or kind = "series" then page = push("BookScreen")
    if kind = "list" then page = push("LibraryScreen")     ' Search's See all, a genre's titles
    if kind = "live"
        showLive()      ' Search's Live TV chip: the guide
        return
    end if
    if kind = "movie" then page = push("TitleScreen")
    ' a profile's playlist or collection from Saved is a page of rows; a saved view, a page of what it finds
    if kind = "savedList" and (ask.item.listKind = "playlist" or ask.item.listKind = "collection") then page = push("ListScreen")
    if kind = "savedList" and ask.item.listKind = "saved_view" then page = push("GroupScreen")
    if kind = "show" then page = push("ShowScreen")
    if kind = "collection" or kind = "person" then page = push("GroupScreen")
    if kind = "recordings" then page = push("RecordingsScreen")    ' Live TV's Recordings
    if page <> invalid
        page.item = ask.item
        setArt(page.art)          ' a page with no art of its own clears the one before it
        page.active = true
        markLibrary(ask.item)
    end if
end sub

' On a title's page the rail marks the title's library (and Home again once the page is left).
sub markLibrary(item as object)
    if item.libraryId = invalid then return
    for each entry in m.rail.entries
        if entry.id = item.libraryId
            if m.railHome = invalid then m.railHome = m.rail.current
            m.rail.current = item.libraryId
        end if
    end for
end sub

' Another library chosen from a library's name: opened as the rail opens it.
sub onSwitchLibrary(event as object)
    m.rail.chosen = event.getData()
end sub

' The front door. `why` says so when it is not a first run (the server ended the sign-in).
sub showSignIn(why = "" as string)
    if m.screen <> invalid and m.screen.subtype() = "SignInScreen" then return
    m.entered = false
    m.global.appLog = []     ' a sign-in that ended, or another server: the app's log starts again
    show("SignInScreen")
    m.screen.observeField("signedIn", "onSignedIn")
    m.screen.address = addressShown(storeRead("origin"))
    m.screen.notice = why
    m.screen.active = true
end sub

function signedOutText() as string
    name = storeRead("serverName")
    if name = "" then name = "the server"
    return "You were signed out of " + name + ". Sign in again to carry on."
end function

' The saved address answers as another server, or not as Portico (ApiTask.renew): one plain page.
' The sign-in is kept, unsent: Try again asks the address again; Sign in again goes to the front door.
sub onServerChanged(event as object)
    name = storeRead("serverName")
    if name = "" then name = "your server"
    body = "This address now answers as a different server. Nothing was sent to it. Try again, or sign in again if the server was reinstalled or moved."
    if event.getData() = "notPortico" then body = "This address answers, but not as a Portico server. Nothing was sent to it. Check the address, then try again, or sign in again."
    show("PageState")
    m.screen.icon = "warning"
    m.screen.title = "Portico couldn’t confirm this is " + name
    m.screen.body = body
    m.screen.buttons = [{id: "again", text: "Try again", icon: "refresh"}, {id: "signin", text: "Sign in again", icon: ""}]
    m.screen.observeField("pressed", "onServerChangedButton")
    m.screen.active = true
end sub

sub onServerChangedButton(event as object)
    if event.getData() = "again" then enter() else showSignIn()
end sub

sub onSignedIn(event as object)
    signIn = event.getData()
    session = signIn.session
    m.global.origin = signIn.origin
    ' the server's id is kept beside its address: no renewal sends the token where it is not (ApiTask.renew)
    m.task.session = {origin: signIn.origin, serverId: signIn.serverId, accessToken: session.accessToken, refreshToken: session.refreshToken, installationId: installationId()}
    storeWrite({origin: signIn.origin, serverId: signIn.serverId, serverName: signIn.serverName, refreshToken: session.refreshToken, renewalId: ""})
    if session.viewer <> invalid and session.viewer.profileId <> invalid
        enter()
    else
        showProfiles(signIn.profiles)
    end if
end sub

' Who's watching, unless there is one profile with no PIN: then there is nothing to ask.
sub showProfiles(profiles as object)
    show("ProfilesScreen")
    m.screen.serverName = storeRead("serverName")
    m.screen.last = storeRead("profileId")
    m.screen.profiles = profiles
    m.screen.observeField("chosen", "onProfileChosen")
    m.screen.active = true
    if profiles.count() = 1 and profiles[0].pinRequired <> true then m.screen.callFunc("chooseOnly")
end sub

sub onProfileChosen(event as object)
    chosen = event.getData()
    session = m.task.session
    session.accessToken = chosen.session.accessToken
    if chosen.session.refreshToken <> invalid
        session.refreshToken = chosen.session.refreshToken
        storeWrite({refreshToken: session.refreshToken})
    end if
    m.task.session = session
    storeWrite({profileId: chosen.profile.id})
    enter()
end sub

sub onProfiles(event as object)
    result = event.getData()
    if result.ok and result.data.items <> invalid then showProfiles(result.data.items) else showSignIn()
end sub

' Signed in with a profile: the rail's facts, then Home.
sub enter()
    api("GET", "/v1/me", invalid, "onMe")
end sub

sub onMe(event as object)
    result = event.getData()
    if not result.ok and result.error <> invalid and result.error.code = "server_changed" then return     ' its own page comes (onServerChanged)
    if not result.ok
        if result.status = 401
            storeWrite({refreshToken: "", renewalId: ""})
            showSignIn(signedOutText())
        else
            showHome()   ' out of reach, or not allowed just now: Home says why and offers to try again
        end if
        return
    end if
    profile = result.data.profile
    if profile = invalid
        api("GET", "/v1/profiles", invalid, "onProfiles")
        return
    end if
    m.rail.profileName = profile.name
    m.global.together.ask = {do: "forget"}     ' a group belongs to the profile that joined it
    m.rail.profileColour = faceColour(profile.color)
    showHome()
end sub

sub onCapabilitiesSent(event as object)
    result = event.getData()
    if not result.ok then ? "PORTICO capabilities refused "; result.status; " "; result.error
end sub

sub showHome()
    if m.entered <> true
        m.entered = true
        api("PUT", "/v1/devices/current/capabilities", deviceProfile(), "onCapabilitiesSent")
        api("GET", "/v1/preferences?deviceClass=television", invalid, "onLooks")
        api("GET", "/v1/libraries", invalid, "onLibraries")
        api("GET", "/v1/live/channels?limit=1", invalid, "onLive")
        api("GET", "/v1/me", invalid, "onRole")
    end if
    show("HomeScreen")
    m.screen.observeField("menu", "onMenu")
    m.screen.observeField("art", "onArt")
    m.screen.observeField("open", "onOpen")
    m.rail.visible = true
    m.rail.current = "home"
    m.screen.active = true
    openLinked()
end sub

' The same while the app is running. Before anyone is signed in it waits for Home.
sub onDeepLink()
    link = m.top.deepLink
    if link = invalid or link.contentId = invalid then return
    m.linked = link
    if m.entered = true and m.rail.visible then openLinked()
end sub

sub openLinked()
    if m.linked = invalid then return
    api("GET", "/v1/items/" + m.linked.contentId, invalid, "onLinked", {mediaType: m.linked.mediaType})
    m.linked = invalid
end sub

' A film or an episode asked for by its media type plays at once, as Roku expects; anything
' else opens its page.
sub onLinked(event as object)
    result = event.getData()
    if not result.ok then return
    item = result.data
    kind = item.kind
    play = event.getRoSGNode().ctx.mediaType <> invalid and (kind = "movie" or kind = "episode")
    ' an episode only to look at opens its show's page, on its season, at the episode
    if kind = "episode" and not play and item.rootId <> invalid then item = {id: item.rootId, kind: "show", title: item.rootTitle, seasonId: item.parentId, episodeId: item.id}
    onOpenAsk({item: item, play: play})
end sub

' A page names its art as a picture path, or "poster|<path>" for a title with only a poster, or
' "cover|<n>" for one with no picture at all (mock.html, "When the artwork isn't there").
sub onArt(event as object)
    ' only the page on top sets the backdrop; one underneath keeps its art for when it comes back
    if m.screen <> invalid and event.getRoSGNode().isSameNode(m.screen) then setArt(event.getData())
end sub

sub onQuiet(event as object)
    if event.getRoSGNode().isSameNode(m.screen) then m.backdrop.quiet = event.getData()
end sub

sub onLooks(event as object)
    result = event.getData()
    if result.ok then m.global.looks = appearanceLooks(result.data.effective)
end sub

' `art` is invalid for a page without art of its own (Watch Together's room): the plain backdrop.
sub setArt(art as dynamic)
    if art = invalid then art = ""
    ' Show backdrops off (Settings, Appearance): the plain backdrop only
    if m.global.looks.showBackdrops = false then art = ""
    if left(art, 7) = "poster|"
        m.backdrop.poster = imageUrl(mid(art, 8), 400, 600)
        m.backdrop.tint = ""
        m.backdrop.art = ""
    else if left(art, 6) = "cover|"
        m.backdrop.poster = ""
        m.backdrop.tint = "pkg:/images/cover-" + mid(art, 7) + ".jpg"
        m.backdrop.art = ""
    else if left(art, 5) = "glow|"
        ' a music or book page: the room takes its colour from the cover (tv-more frame 6)
        m.backdrop.poster = ""
        m.backdrop.tint = ""
        m.backdrop.art = ambientUrl(mid(art, 6))
    else
        m.backdrop.poster = ""
        m.backdrop.tint = ""
        m.backdrop.art = imageUrl(art, 1344, 756)
    end if
end sub

function libraryIcon(kind as string) as string
    if kind = "movies" or kind = "movie" then return "film"
    if kind = "shows" or kind = "tv" then return "tv"
    if kind = "music" then return "music"
    if kind = "audiobooks" or kind = "audiobook" then return "book"
    return "folder"
end function

sub onLibraries(event as object)
    result = event.getData()
    m.libraries = invalid
    entries = [{id: "search", icon: "search", label: "Search"}, {id: "home", icon: "home", label: "Home"}]
    if result.ok and result.data.items <> invalid
        names = {}
        for each library in result.data.items
            names[library.id] = library.name
        end for
        m.global.libraryNames = names
        for each library in result.data.items
            ' Reading libraries are not on TV (definition, area 10).
            if library.kind <> "books" and library.kind <> "comics"
                entries.push({id: library.id, icon: libraryIcon(library.kind), label: library.name})
            end if
        end for
    end if
    if m.hasLive = true then entries.push({id: "live", icon: "live", label: "Live TV"})
    entries.push({id: "saved", icon: "bookmark", label: "Saved"})
    m.libraries = result
    m.rail.entries = entries
    m.rail.current = "home"
end sub

' Live TV is in the rail only when this profile has channels to watch.
sub onLive(event as object)
    result = event.getData()
    m.hasLive = result.ok and result.data.items <> invalid and result.data.items.count() > 0
    if m.hasLive and m.libraries <> invalid then onLibrariesAgain()
end sub

sub onLibrariesAgain()
    entries = m.rail.entries
    last = entries.pop()
    entries.push({id: "live", icon: "live", label: "Live TV"})
    entries.push(last)
    m.rail.entries = entries
    m.rail.current = "home"
end sub

' The server ended this sign-in (signed out elsewhere, or it ran out): start again at the front door.
sub onSignInEnded()
    storeWrite({refreshToken: "", renewalId: ""})
    showSignIn(signedOutText())
end sub

' The open rail pushes the page right, so none of it is covered (tv.html frame 2).
sub onRailOpen()
    if m.rail.isOpen then m.screens.translation = [metrics().pageShift, 0] else m.screens.translation = [0, 0]
end sub

' The Live TV guide, from the rail or from Search's Live TV chip.
sub showLive()
    show("LiveScreen")
    m.screen.observeField("open", "onOpen")
    m.screen.observeField("menu", "onMenu")
    m.rail.visible = true
    m.rail.current = "live"
    m.screen.active = true
end sub

sub onRailChosen(event as object)
    if m.screen = invalid then return
    ' OK on the page you are on goes to its top, the same place as Back.
    id = event.getData()
    if id = m.rail.current
        if m.screen.hasField("toTop") then m.screen.toTop = true
        m.screen.active = true
        return
    end if
    if id = "home"
        showHome()
        return
    end if
    if id = "live"
        showLive()
        return
    end if
    if id = "profile"
        openProfileMenu("")
        return
    end if
    if id = "search"
        show("SearchScreen")
        m.screen.observeField("art", "onArt")
        m.screen.observeField("open", "onOpen")
        m.screen.observeField("menu", "onMenu")
        m.rail.visible = true
        m.rail.current = id
        m.screen.active = true
        return
    end if
    if id = "saved"
        show("SavedScreen")
        m.screen.observeField("open", "onOpen")
        m.screen.observeField("menu", "onMenu")
        m.screen.observeField("goHome", "showHome")
        m.rail.visible = true
        m.rail.current = id
        m.screen.active = true
        return
    end if
    if id = "settings"
        show("SettingsScreen")
        m.screen.observeField("menu", "onMenu")
        m.screen.observeField("account", "onSettingsAsk")
        m.rail.visible = true
        m.rail.current = id
        m.screen.active = true
        return
    end if
    if m.libraries <> invalid and m.libraries.ok
        for each library in m.libraries.data.items
            if library.id = id
                show("LibraryScreen")
                m.screen.observeField("goHome", "showHome")
                m.screen.observeField("art", "onArt")
                m.screen.observeField("open", "onOpen")
                m.screen.observeField("menu", "onMenu")
                m.rail.visible = true
                m.rail.current = id
                m.screen.library = library
                m.screen.observeField("switchTo", "onSwitchLibrary")
                m.screen.active = true
                return
            end if
        end for
    end if
    m.screen.active = true     ' a destination not built yet: stay where you were
end sub

' Watch Together (tv-more frames 11 to 17), from the profile menu: the rail marks the profile.
sub showTogether()
    show("TogetherScreen")
    m.screen.observeField("open", "onOpen")
    m.screen.observeField("menu", "onMenu")
    m.rail.visible = true
    m.rail.current = "profile"
    m.screen.active = true
end sub

' Inside a group the menu's row says so (tv-more frame 10).
function togetherRowText() as string
    if m.global.together.phase = "live" or m.global.together.phase = "reconnecting" then return "You’re in a group"
    return "Watch Together"
end function

' Settings' Sign out row asks the scene, as the profile menu's does
sub onSettingsAsk(event as object)
    m.sheetIsOwn = true
    onSheetChosen(event)
    m.signOutFromMenu = false     ' asked from Settings: Back returns to its row there
end sub

sub onRailLeft()
    if m.screen <> invalid then m.screen.active = true
end sub

function onKeyEvent(key as string, press as boolean) as boolean
    if not press then return false
    if key = "left" and m.screen <> invalid and m.rail.visible
        ' Left from a page's leftmost element opens the rail.
        m.rail.active = true
        return true
    end if
    if key = "back" and m.screens.getChildCount() > 1
        pop()
        return true
    end if
    ' Back from Watch Together (its lobby, a room or the ended page) returns to its opener: the profile
    ' menu, on Watch Together. The page's own sheets and dialogs answer Back first.
    if key = "back" and m.screen <> invalid and m.screen.subtype() = "TogetherScreen"
        showHome()
        openProfileMenu("together")
        return true
    end if
    ' Back at the top of any other rail page goes to Home's opening state; only Home's own Back
    ' leaves the app (DESIGN.md, "Back, step by step").
    if key = "back" and m.screen <> invalid and m.entered = true and m.rail.visible and m.rail.current <> "home"
        showHome()
        return true
    end if
    ' Back from Who's watching while someone is signed in with a profile goes to Home too
    if key = "back" and m.screen <> invalid and m.entered = true and m.screen.subtype() = "ProfilesScreen"
        showHome()
        return true
    end if
    ' Up from a page's top reaches the now-playing card, which opens in place
    if key = "up" and m.top.findNode("nowPlaying").visible
        m.cardFrom = focusedLeaf()      ' where focus goes back to when the card folds away
        m.top.findNode("nowPlaying").active = true
        return true
    end if
    return false
end function
