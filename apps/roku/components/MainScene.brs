sub init()
    m.backdrop = m.top.findNode("backdrop")
    m.screens = m.top.findNode("screens")
    m.rail = m.top.findNode("rail")
    m.screen = invalid
    m.task = createObject("roSGNode", "ApiTask")
    m.global.addFields({api: m.task, origin: ""})
    m.task.observeField("signInEnded", "onSignInEnded")
    m.task.control = "run"
    m.rail.observeField("chosen", "onRailChosen")
    m.rail.observeField("left", "onRailLeft")
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
    m.screens.removeChildrenIndex(m.screens.getChildCount(), 0)
    m.screen = m.screens.createChild(component)
    m.rail.visible = false
    m.backdrop.art = ""
end sub

' Open a page over the one showing; Back returns to it exactly as it was left.
function push(component as string) as object
    m.screen.visible = false
    m.screen = m.screens.createChild(component)
    m.screen.observeField("art", "onArt")
    m.screen.observeField("open", "onOpen")
    return m.screen
end function

sub pop()
    count = m.screens.getChildCount()
    if count < 2 then return
    m.screens.removeChildIndex(count - 1)
    m.screen = m.screens.getChild(count - 2)
    m.screen.visible = true
    m.backdrop.art = imageUrl(m.screen.art, 1344, 756)
    m.screen.active = true
end sub

sub onPlayerClosed()
    m.rail.visible = true
    pop()
end sub

' A page asks for a title to be opened or played.
sub onOpen(event as object)
    ask = event.getData()
    kind = ask.item.kind
    if ask.play = true and (kind = "movie" or kind = "episode")
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
    if page <> invalid
        page.item = ask.item
        page.active = true
    end if
end sub

sub showSignIn()
    show("SignInScreen")
    m.screen.observeField("signedIn", "onSignedIn")
    m.screen.active = true
end sub

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
        if result.status = 401 or result.status = 403
            showSignIn()
        else
            showHome()   ' out of reach: Home says so and offers to try again
        end if
        return
    end if
    profile = result.data.profile
    if profile = invalid
        api("GET", "/v1/profiles", invalid, "onProfiles")
        return
    end if
    m.rail.profileName = profile.name
    m.rail.profileColour = "0x28587AFF"
    showHome()
end sub

sub onCapabilitiesSent(event as object)
    result = event.getData()
    if not result.ok then ? "PORTICO capabilities refused "; result.status; " "; result.error
end sub

sub showHome()
    api("PUT", "/v1/devices/current/capabilities", deviceProfile(), "onCapabilitiesSent")
    api("GET", "/v1/libraries", invalid, "onLibraries")
    api("GET", "/v1/live/channels?limit=1", invalid, "onLive")
    show("HomeScreen")
    m.screen.observeField("art", "onArt")
    m.screen.observeField("open", "onOpen")
    m.rail.visible = true
    m.rail.current = "home"
    m.screen.active = true
end sub

sub onArt(event as object)
    m.backdrop.art = imageUrl(event.getData(), 1344, 756)
end sub

function libraryIcon(kind as string) as string
    if kind = "movies" or kind = "movie" then return "film"
    if kind = "shows" or kind = "tv" or kind = "anime" then return "tv"
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
    showSignIn()
end sub

sub onRailChosen(event as object)
    if m.screen <> invalid then m.screen.active = true
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
    return false
end function
