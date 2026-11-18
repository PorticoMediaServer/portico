sub init()
    m.backdrop = m.top.findNode("backdrop")
    m.screens = m.top.findNode("screens")
    m.rail = m.top.findNode("rail")
    m.screen = invalid
    m.task = createObject("roSGNode", "ApiTask")
    m.global.addFields({api: m.task, origin: ""})
    m.task.observeField("signInEnded", "onSignInEnded")
    m.task.control = "run"
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
    m.sheet.observeField("dismissed", "onSheetClosed")
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
    args = m.top.launchArgs
    ' A deep link: Roku started the app to open one title (contentId is the item's id on this server).
    if args <> invalid and args.contentId <> invalid then m.linked = args
    if createObject("roAppInfo").isDev() and args <> invalid
        ' Development builds only: scripts can hand the app a sign-in, or clear it.
        if args.devSignOut <> invalid then storeWrite({origin: "", refreshToken: "", profileId: "", renewalId: ""})
        if args.devOrigin <> invalid and args.devRefresh <> invalid
            storeWrite({origin: args.devOrigin, refreshToken: args.devRefresh, installationId: args.devInstallation, profileId: args.devProfile, renewalId: ""})
        end if
    end if
    origin = storeRead("origin")
    token = storeRead("refreshToken")
    if origin = "" or token = ""
        showSignIn()
        return
    end if
    m.global.origin = origin
    m.task.session = {origin: origin, refreshToken: token, installationId: installationId()}
    api("GET", "/v1/me", invalid, "onMe")
end sub

' One screen at a time fills the page; the rail shows only once someone is signed in with a profile.
sub show(component as string)
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

sub onSheetChosen(event as object)
    if m.sheetIsOwn = true
        m.sheetIsOwn = false
        m.sheet.visible = false
        which = event.getData()
        if which = "switch"
            api("GET", "/v1/profiles", invalid, "onProfiles")
        else if which = "signout"
            ' the server is told this sign-in is over; the TV forgets it either way
            api("DELETE", "/v1/auth/session", invalid, "onSignedOut")
        end if
        return
    end if
    closeSheet()
    if m.screen <> invalid and m.screen.hasField("menuChosen") then m.screen.menuChosen = event.getData()
end sub

sub onSheetClosed()
    own = (m.sheetIsOwn = true)
    m.sheetIsOwn = false
    closeSheet()
    ' the page that asked for the menu hears that it closed (Settings goes back a step)
    if not own and m.screen <> invalid and m.screen.hasField("menuClosed") then m.screen.menuClosed = true
end sub

sub onSignedOut()
    storeWrite({refreshToken: "", renewalId: "", profileId: ""})
    m.task.session = {origin: storeRead("origin"), installationId: installationId()}
    showSignIn()
end sub

' Focus returns to what opened the sheet.
sub closeSheet()
    m.sheet.visible = false
    if m.screen <> invalid then m.screen.active = true
end sub

sub onPlayerClosed()
    m.rail.visible = true
    pop()
end sub

' A page asks for a title to be opened or played.
sub onOpen(event as object)
    onOpenAsk(event.getData())
end sub

sub onOpenAsk(ask as object)
    kind = ask.item.kind
    if ask.play = true and (kind = "movie" or kind = "episode" or kind = "channel")
        player = push("PlayerScreen")
        player.observeField("closed", "onPlayerClosed")
        player.item = ask.item
        player.startOver = (ask.startOver = true)
        m.rail.visible = false
        player.active = true
        return
    end if
    page = invalid
    if kind = "movie" then page = push("TitleScreen")
    if kind = "show" then page = push("ShowScreen")
    if kind = "collection" or kind = "person" then page = push("GroupScreen")
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

' The front door. `why` says so when it is not a first run (the server ended the sign-in).
sub showSignIn(why = "" as string)
    if m.screen <> invalid and m.screen.subtype() = "SignInScreen" then return
    m.entered = false
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

sub onSignedIn(event as object)
    signIn = event.getData()
    session = signIn.session
    m.global.origin = signIn.origin
    m.task.session = {origin: signIn.origin, accessToken: session.accessToken, refreshToken: session.refreshToken, installationId: installationId()}
    storeWrite({origin: signIn.origin, serverName: signIn.serverName, refreshToken: session.refreshToken, renewalId: ""})
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
        api("GET", "/v1/libraries", invalid, "onLibraries")
        api("GET", "/v1/live/channels?limit=1", invalid, "onLive")
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
    ' an episode only to look at opens its show's page
    if kind = "episode" and not play and item.rootId <> invalid then item = {id: item.rootId, kind: "show", title: item.rootTitle}
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

sub setArt(art as string)
    if left(art, 7) = "poster|"
        m.backdrop.poster = imageUrl(mid(art, 8), 400, 600)
        m.backdrop.tint = ""
        m.backdrop.art = ""
    else if left(art, 6) = "cover|"
        m.backdrop.poster = ""
        m.backdrop.tint = "pkg:/images/cover-" + mid(art, 7) + ".jpg"
        m.backdrop.art = ""
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
        show("LiveScreen")
        m.screen.observeField("open", "onOpen")
        m.screen.observeField("menu", "onMenu")
        m.rail.visible = true
        m.rail.current = id
        m.screen.active = true
        return
    end if
    if id = "profile"
        ' the profile and server menu (tv-more frame 10): its rows are this scene's own
        m.sheetIsOwn = true
        m.sheet.model = {eyebrow: storeRead("serverName"), title: m.rail.profileName, status: "", rows: [
            {id: "switch", text: "Switch profile", icon: ""},
            {id: "signout", text: "Sign out", icon: "", gapBefore: true}]}
        m.sheet.visible = true
        m.sheet.active = true
        return
    end if
    if id = "search"
        show("SearchScreen")
        m.screen.observeField("art", "onArt")
        m.screen.observeField("open", "onOpen")
        m.rail.visible = true
        m.rail.current = id
        m.screen.active = true
        return
    end if
    if id = "settings"
        show("SettingsScreen")
        m.screen.observeField("menu", "onMenu")
        m.rail.visible = true
        m.rail.current = id
        m.screen.active = true
        return
    end if
    if m.libraries <> invalid and m.libraries.ok
        for each library in m.libraries.data.items
            if library.id = id
                show("LibraryScreen")
                m.screen.observeField("art", "onArt")
                m.screen.observeField("open", "onOpen")
                m.screen.observeField("menu", "onMenu")
                m.rail.visible = true
                m.rail.current = id
                m.screen.library = library
                m.screen.active = true
                return
            end if
        end for
    end if
    m.screen.active = true     ' a destination not built yet: stay where you were
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
    ' Back at the top of any other rail page goes to Home's opening state; only Home's own Back
    ' leaves the app (DESIGN.md, "Back, step by step").
    if key = "back" and m.screen <> invalid and m.entered = true and m.rail.visible and m.rail.current <> "home"
        showHome()
        return true
    end if
    return false
end function
