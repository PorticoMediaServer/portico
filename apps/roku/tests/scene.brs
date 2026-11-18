' The scene's own sign-in code (components/MainScene.brs onSignedIn) with stand-ins: the server's id
' is kept beside its address, in the store and in the task's session (security finding F9).

sub main()
    m.failed = 0
    m.passed = 0
    m.store = {}
    m.global = {origin: ""}
    m.task = {session: invalid}
    onSignedIn({data: {origin: "http://home.example", serverName: "Home", serverId: "srv_home",
        session: {accessToken: "a", refreshToken: "r", viewer: {profileId: "p1"}}, profiles: []},
        getData: function() as object : return m.data : end function})
    check("the server's id is stored beside its address", m.store.serverid = "srv_home" and m.store.origin = "http://home.example")
    check("and the task's session carries it", m.task.session.serverId = "srv_home")
    check("a new sign-in remembers no PIN from an earlier one", m.store.pintoken = "")

    ' the app starts on a sign-in whose PIN profile this TV did not remember (Remember was off): Who's watching again
    m.sent = []
    m.starting = true
    onMe({data: {ok: true, data: {profile: {id: "p2", name: "Kid", pinRequired: true}}}, getData: function() as object : return m.data : end function})
    check("a PIN profile not remembered here is asked for again at start", m.sent.count() = 1 and m.sent[0].path = "/v1/profiles")

    ' a PIN profile this TV remembers: its token is sent in place of the PIN, and Who's watching is skipped
    m.sent = []
    m.store.pinprofile = "p2"
    m.store.pintoken = "tok"
    onProfiles({data: {ok: true, data: {items: [{id: "p1"}, {id: "p2", pinRequired: true}]}}, getData: function() as object : return m.data : end function})
    check("the remembered PIN profile is chosen with its token", m.sent.count() = 1 and m.sent[0].path = "/v1/profiles/p2/select" and m.sent[0].body.pinToken = "tok")
    ? "PASSED "; m.passed; " FAILED "; m.failed
end sub

sub check(name as string, ok as boolean)
    if ok
        m.passed = m.passed + 1
    else
        m.failed = m.failed + 1
        ? "FAIL "; name
    end if
end sub

' --- stand-ins for what the scene's other scripts give it ---------------------------------------
sub storeWrite(values as object)
    for each key in values
        m.store[lcase(key)] = values[key]
    end for
end sub

function storeRead(key as string) as string
    value = m.store[lcase(key)]
    if value = invalid then return ""
    return value
end function

function installationId() as string
    return "installation"
end function

function api(method as string, path as string, body as dynamic, callback as string, ctx = invalid as dynamic, isPublic = false as boolean, key = "" as string, origin = "" as string) as object
    if m.sent <> invalid then m.sent.push({path: path, body: body})
    return {}
end function
